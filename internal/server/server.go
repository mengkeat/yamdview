// Package server provides the local HTTP server that serves the Markdown viewer.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mengkeat/yamdview/internal/annotation"
	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/session"
)

// ExportView is a named viewport target for standalone export.
type ExportView string

const (
	ExportViewPhone   ExportView = "phone"
	ExportViewTablet  ExportView = "tablet"
	ExportViewLaptop  ExportView = "laptop"
	ExportViewDesktop ExportView = "desktop"
)

// ExportViewNames lists the recognised viewport targets, for error messages.
const ExportViewNames = "phone, tablet, laptop, desktop"

// ValidExportView reports whether v is a recognised viewport target.
func ValidExportView(v string) bool {
	switch ExportView(v) {
	case ExportViewPhone, ExportViewTablet, ExportViewLaptop, ExportViewDesktop:
		return true
	default:
		return false
	}
}

// exportViewMeasure returns the fixed --measure value for the given viewport.
// An empty string means "use the responsive default".
func exportViewMeasure(v ExportView) string {
	switch v {
	case ExportViewPhone:
		return "22rem"
	case ExportViewTablet:
		return "40rem"
	case ExportViewLaptop:
		return "52rem"
	case ExportViewDesktop:
		return "62rem"
	default:
		return ""
	}
}

// PageData holds the data injected into the HTML template.
type PageData struct {
	Title       string
	Content     template.HTML
	CSS         template.CSS
	JS          template.JS
	AnnotatorJS template.JS
	Review      *ReviewPageData
}

// ReviewPageData is the small amount of review state rendered into a review
// page. Token is intentionally page-only; API responses use session metadata.
type ReviewPageData struct {
	ID      string
	Title   string
	Prompt  string
	Choices []string
	State   string
	Token   string

	// Respond metadata tells the browser which reformulation backend and
	// mode are configured. Names only — API keys never reach this struct.
	RespondProvider string
	RespondModel    string
	RespondModels   []string
	RespondMode     string

	// RespondModelsJoined is the comma-joined model list for the HTML
	// template's data attribute.
	RespondModelsJoined string
}

// Assets provides the embedded web assets (template, CSS, JS).
type Assets struct {
	IndexHTML   string
	ViewerCSS   string
	ViewerJS    string
	AnnotatorJS string
}

// ensureAssets fills empty CSS/JS fields from the provided assets.
func (pd *PageData) ensureAssets(assets Assets) {
	if pd.CSS == "" {
		pd.CSS = template.CSS(assets.ViewerCSS)
	}
	if pd.JS == "" {
		pd.JS = template.JS(assets.ViewerJS)
	}
	if pd.AnnotatorJS == "" {
		pd.AnnotatorJS = template.JS(assets.AnnotatorJS)
	}
}

// PageDataFromAssets creates a PageData with CSS and JS populated from assets.
func PageDataFromAssets(assets Assets, title string, content template.HTML) PageData {
	pd := PageData{Title: title, Content: content}
	pd.ensureAssets(assets)
	return pd
}

// ClientError represents a client-side render error reported by the browser.
type ClientError struct {
	BlockID string `json:"block_id"`
	Kind    string `json:"kind"` // "math", "table", etc.
	Message string `json:"message"`
	TeX     string `json:"tex"` // original TeX for math errors
}

// Server is the local HTTP server for the Markdown viewer.
type Server struct {
	listener net.Listener
	handler  http.Handler
	http     *http.Server
	mu       sync.RWMutex
	pageData PageData
	tmpl     *template.Template
	katexFS  fs.FS
	review   *session.Session

	reformulator ReformulateFunc
	respondMeta  RespondMeta

	lifecycleMu sync.Mutex
	started     bool
	closed      bool
	serveDone   chan struct{}

	clientsMu sync.Mutex
	clients   map[chan sseEvent]struct{}

	onClientError func(ClientError)
}

type sseEvent struct {
	name string
	data string
}

// sseEventPatch names the SSE event carrying block-level DOM patch ops.
// The full-reset event reuses document.OpReset.
const sseEventPatch = "patch"

type resetPayload struct {
	Op   string `json:"op"`
	HTML string `json:"html"`
}

type patchPayload struct {
	Ops []document.PatchOp `json:"ops"`
}

// Option configures a Server during construction.
type Option func(*Server)

// WithKatexFS configures the server to serve KaTeX static assets from the
// given filesystem (rooted at the katex distribution directory).
func WithKatexFS(fsys fs.FS) Option {
	return func(s *Server) { s.katexFS = fsys }
}

// SessionTokenHeader is the documented header required when submitting a
// review. Its value is the token belonging to the attached session.
const (
	SessionTokenHeader = "X-Yamdview-Token"
	maxClientErrorBody = 1 << 20
	maxClientErrors    = 100
	maxAnnotationBody  = 1 << 20
)

// WithSession attaches a review session to the served page and session API.
// Without this option the server remains an ordinary viewer.
func WithSession(review *session.Session) Option {
	return func(s *Server) { s.review = review }
}

// WithClientErrorHandler registers a callback for client-side render errors
// reported via POST /client-error.
func WithClientErrorHandler(fn func(ClientError)) Option {
	return func(s *Server) { s.onClientError = fn }
}

// ReformulateFunc runs one feedback reformulation attempt. It returns a
// ReformulateResult with silent-fallback semantics (Applied=false plus
// diagnostics instead of an error); the server never turns reformulation
// failures into HTTP 5xx responses.
type ReformulateFunc func(ctx context.Context, model string, req feedback.ReformulateRequest, annotations []annotation.Annotation) feedback.ReformulateResult

// RespondMeta describes the configured reformulation capability to the
// browser. It carries provider/model names only, never secrets.
type RespondMeta struct {
	Provider string
	Model    string
	Models   []string
	Mode     string // "off", "ask", or "auto"
}

// WithReformulator enables POST /api/session/reformulate by injecting the
// reformulation capability and its public metadata. The actual provider is
// constructed by the application layer; the server only sees this function.
func WithReformulator(fn ReformulateFunc, meta RespondMeta) Option {
	return func(s *Server) {
		s.reformulator = fn
		s.respondMeta = meta
	}
}

// New creates a new Server that will listen on the given address.
// If addr is empty it defaults to "127.0.0.1:0" (random available port).
func New(addr string, assets Assets, data PageData, opts ...Option) (*Server, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	data.ensureAssets(assets)

	tmpl, err := template.New("index").Parse(assets.IndexHTML)
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("parse template: %w", err)
	}

	mux := http.NewServeMux()

	s := &Server{
		listener:  ln,
		pageData:  data,
		tmpl:      tmpl,
		clients:   make(map[chan sseEvent]struct{}),
		serveDone: make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	// Serve the viewer page at /.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := s.pageDataForViewer()
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
		}
	})

	// Snapshot endpoint returns only the rendered content HTML.
	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, s.currentPageData().Content)
	})

	// KaTeX static assets at /katex/.
	if s.katexFS != nil {
		katexHandler := http.FileServer(http.FS(s.katexFS))
		mux.Handle("/katex/", http.StripPrefix("/katex/", katexHandler))
	}

	// Review session metadata and token-gated submission endpoints.
	mux.HandleFunc("/api/session", s.handleSessionMetadata)
	mux.HandleFunc("/api/session/submit", s.handleSessionSubmit)
	mux.HandleFunc("/api/session/reformulate", s.handleSessionReformulate)
	mux.HandleFunc("/api/session/annotations", s.handleAnnotationCollection)
	mux.HandleFunc("/api/session/annotations/", s.handleAnnotationItem)

	// Client error reporting endpoint.
	mux.HandleFunc("/client-error", s.handleClientError)

	// Events endpoint streams live reload messages to the browser.
	mux.HandleFunc("/events", s.handleEvents)

	s.handler = mux
	s.http = &http.Server{Handler: mux}

	return s, nil
}

// Addr returns the actual listening address (useful when port 0 was requested).
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// URL returns the http:// URL for the viewer.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/", s.Addr())
}

// Start begins serving HTTP requests in a new goroutine.
func (s *Server) Start() {
	s.lifecycleMu.Lock()
	if s.started || s.closed {
		s.lifecycleMu.Unlock()
		return
	}
	s.started = true
	s.lifecycleMu.Unlock()

	go func() {
		if err := s.serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server: %v", err)
		}
	}()
}

// Serve serves HTTP requests on the listener. It blocks until the server
// encounters an error (including http.ErrServerClosed).
func (s *Server) Serve() error {
	s.lifecycleMu.Lock()
	if s.closed {
		s.lifecycleMu.Unlock()
		return http.ErrServerClosed
	}
	if s.started {
		s.lifecycleMu.Unlock()
		return errors.New("server already serving")
	}
	s.started = true
	s.lifecycleMu.Unlock()
	return s.serve()
}

func (s *Server) serve() error {
	defer close(s.serveDone)
	return s.http.Serve(s.listener)
}

// Close immediately closes the listener and active connections.
func (s *Server) Close() error {
	s.lifecycleMu.Lock()
	if s.closed {
		s.lifecycleMu.Unlock()
		return nil
	}
	s.closed = true
	started := s.started
	s.lifecycleMu.Unlock()

	// http.Server.Close also closes active connections, including SSE clients.
	// Waiting for Serve to return keeps the server and its request handlers from
	// outliving a completed review session.
	err := s.http.Close()
	if started {
		<-s.serveDone
	}
	return err
}

// SetContent updates the rendered content served by the viewer.
func (s *Server) SetContent(content template.HTML) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageData.Content = content
}

// BroadcastReset updates the current content and sends a full-document reset
// event to connected browsers.
func (s *Server) BroadcastReset(content template.HTML) error {
	payload, err := json.Marshal(resetPayload{Op: document.OpReset, HTML: string(content)})
	if err != nil {
		return fmt.Errorf("marshal reset payload: %w", err)
	}

	s.SetContent(content)
	s.broadcast(sseEvent{name: document.OpReset, data: string(payload)})
	return nil
}

// BroadcastPatches updates the current content and sends block-level DOM patch
// operations to connected browsers. An empty operation list only updates the
// stored snapshot content.
func (s *Server) BroadcastPatches(content template.HTML, ops []document.PatchOp) error {
	s.SetContent(content)
	if len(ops) == 0 {
		return nil
	}

	payload, err := json.Marshal(patchPayload{Ops: ops})
	if err != nil {
		return fmt.Errorf("marshal patch payload: %w", err)
	}

	s.broadcast(sseEvent{name: sseEventPatch, data: string(payload)})
	return nil
}

func (s *Server) currentPageData() PageData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pageData
}

func (s *Server) pageDataForViewer() PageData {
	data := s.currentPageData()
	if s.review != nil {
		metadata := s.review.Metadata()
		data.Review = &ReviewPageData{
			ID:      metadata.ID,
			Title:   metadata.Title,
			Prompt:  metadata.Prompt,
			Choices: metadata.Choices,
			State:   string(metadata.State),
			Token:   s.reviewToken(),

			RespondModelsJoined: strings.Join(s.respondMeta.Models, ","),
			RespondProvider:     s.respondMeta.Provider,
			RespondModel:        s.respondMeta.Model,
			RespondModels:       s.respondMeta.Models,
			RespondMode:         s.respondMeta.Mode,
		}
	}
	return data
}

func (s *Server) reviewToken() string {
	if s.review == nil {
		return ""
	}
	return s.review.Token
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")

	ch := make(chan sseEvent, 8)
	s.addClient(ch)
	defer s.removeClient(ch)

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			writeSSE(w, event)
			flusher.Flush()
		}
	}
}

func (s *Server) addClient(ch chan sseEvent) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.clients[ch] = struct{}{}
}

func (s *Server) removeClient(ch chan sseEvent) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, ch)
}

func (s *Server) broadcast(event sseEvent) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- event:
		default:
		}
	}
}

func writeSSE(w http.ResponseWriter, event sseEvent) {
	fmt.Fprintf(w, "event: %s\n", event.name)
	for _, line := range strings.Split(event.data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
}

type sessionMetadataResponse struct {
	ID          string                  `json:"id"`
	Title       string                  `json:"title"`
	Prompt      string                  `json:"prompt"`
	Choices     []string                `json:"choices"`
	State       string                  `json:"state"`
	Verdict     string                  `json:"verdict"`
	Summary     string                  `json:"summary"`
	Revision    int                     `json:"revision"`
	OpenedAt    time.Time               `json:"opened_at"`
	SubmittedAt time.Time               `json:"submitted_at,omitempty"`
	Annotations []annotation.Annotation `json:"annotations"`
}

// handleSessionMetadata returns session metadata without the authentication
// token. The endpoint is intentionally read-only.
func (s *Server) handleSessionMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.review == nil {
		http.NotFound(w, r)
		return
	}

	metadata := s.review.Metadata()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(sessionMetadataResponse{
		ID:          metadata.ID,
		Title:       metadata.Title,
		Prompt:      metadata.Prompt,
		Choices:     metadata.Choices,
		State:       string(metadata.State),
		Verdict:     metadata.Verdict,
		Summary:     metadata.Summary,
		Revision:    metadata.Revision,
		OpenedAt:    metadata.OpenedAt,
		SubmittedAt: metadata.SubmittedAt,
		Annotations: s.review.AnnotationSnapshot(),
	})
}

// annotationCreateRequest is the browser-facing create shape. Server-owned
// fields such as IDs, timestamps, status, and source spans are not accepted.
type annotationCreateRequest struct {
	Kind                 annotation.Kind             `json:"kind"`
	Pieces               []annotation.SelectionPiece `json:"pieces"`
	BlockID              string                      `json:"block_id"`
	StartLine            int                         `json:"start_line"`
	EndLine              int                         `json:"end_line"`
	Quote                string                      `json:"quote"`
	Prefix               string                      `json:"prefix"`
	Suffix               string                      `json:"suffix"`
	Comment              string                      `json:"comment"`
	SuggestedReplacement string                      `json:"suggested_replacement"`
}

type annotationPatchRequest struct {
	Kind                 *annotation.Kind `json:"kind"`
	BlockID              *string          `json:"block_id"`
	StartLine            *int             `json:"start_line"`
	EndLine              *int             `json:"end_line"`
	Quote                *string          `json:"quote"`
	Prefix               *string          `json:"prefix"`
	Suffix               *string          `json:"suffix"`
	Comment              *string          `json:"comment"`
	SuggestedReplacement *string          `json:"suggested_replacement"`
}

type apiErrorResponse struct {
	Error string `json:"error"`
}

// handleAnnotationCollection creates one or more annotations. A selection
// with multiple pieces is split into annotations sharing one group ID.
func (s *Server) handleAnnotationCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeAnnotationMutation(w, r) {
		return
	}

	var req annotationCreateRequest
	fields, ok := decodeAnnotationBody(w, r, &req)
	if !ok {
		return
	}
	items, err := buildAnnotationCreateRequest(req, fields)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	created := make([]annotation.Annotation, 0, len(items))
	for _, item := range items {
		item, err = s.review.CreateAnnotation(item)
		if err != nil {
			writeAnnotationMutationError(w, err)
			return
		}
		created = append(created, item)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	if len(created) == 1 {
		_ = json.NewEncoder(w).Encode(created[0])
		return
	}
	_ = json.NewEncoder(w).Encode(created)
}

func (s *Server) handleAnnotationItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/session/annotations/")
	if id == "" || strings.Contains(id, "/") {
		writeAPIError(w, http.StatusNotFound, "annotation not found")
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeAnnotationMutation(w, r) {
		return
	}

	if r.Method == http.MethodDelete {
		if err := s.review.DeleteAnnotation(id); err != nil {
			writeAnnotationMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var req annotationPatchRequest
	fields, ok := decodeAnnotationBody(w, r, &req)
	if !ok {
		return
	}
	if len(fields) == 0 {
		writeAPIError(w, http.StatusBadRequest, "at least one annotation field is required")
		return
	}

	current, err := s.review.GetAnnotation(id)
	if err != nil {
		writeAnnotationMutationError(w, err)
		return
	}
	if err := validateAnnotationPatch(req, current); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	input := annotation.Annotation{}
	if req.Kind != nil {
		input.Kind = *req.Kind
	}
	if req.BlockID != nil {
		input.BlockID = *req.BlockID
	}
	if req.StartLine != nil {
		input.StartLine = *req.StartLine
	}
	if req.EndLine != nil {
		input.EndLine = *req.EndLine
	}
	if req.Quote != nil {
		input.Quote = *req.Quote
	}
	if req.Prefix != nil {
		input.Prefix = *req.Prefix
	}
	if req.Suffix != nil {
		input.Suffix = *req.Suffix
	}
	if req.Comment != nil {
		input.Comment = *req.Comment
	}
	if req.SuggestedReplacement != nil {
		input.SuggestedReplacement = *req.SuggestedReplacement
	}

	updated, err := s.review.UpdateAnnotation(id, input)
	if err != nil {
		writeAnnotationMutationError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(updated)
}

func (s *Server) authorizeAnnotationMutation(w http.ResponseWriter, r *http.Request) bool {
	if s.review == nil {
		writeAPIError(w, http.StatusNotFound, "review session not found")
		return false
	}
	if !s.review.TokenMatches(r.Header.Get(SessionTokenHeader)) {
		writeAPIError(w, http.StatusForbidden, "invalid or missing session token")
		return false
	}
	if s.review.CurrentState() != session.Open {
		writeAPIError(w, http.StatusConflict, "session is no longer open")
		return false
	}
	return true
}

func buildAnnotationCreateRequest(req annotationCreateRequest, fields map[string]json.RawMessage) ([]annotation.Annotation, error) {
	if req.Kind == "" {
		return nil, errors.New("annotation kind is required")
	}
	if err := validateAnnotationKindAndSuggestion(req.Kind, req.SuggestedReplacement); err != nil {
		return nil, err
	}

	if _, hasPieces := fields["pieces"]; hasPieces {
		if len(req.Pieces) == 0 {
			return nil, errors.New("annotation pieces must not be empty")
		}
		if hasAnyField(fields, "block_id", "start_line", "end_line", "quote", "prefix", "suffix") {
			return nil, errors.New("pieces cannot be combined with a single annotation anchor")
		}
		seenBlocks := make(map[string]struct{}, len(req.Pieces))
		for i, piece := range req.Pieces {
			if err := validateSelectionPiece(i, piece); err != nil {
				return nil, err
			}
			if _, exists := seenBlocks[piece.BlockID]; exists {
				return nil, fmt.Errorf("selection piece %d repeats block_id %q", i, piece.BlockID)
			}
			seenBlocks[piece.BlockID] = struct{}{}
		}
		items, err := annotation.SplitSelection(req.Pieces)
		if err != nil {
			return nil, fmt.Errorf("invalid annotation selection: %w", err)
		}
		for i := range items {
			items[i].Kind = req.Kind
			items[i].Comment = req.Comment
			items[i].SuggestedReplacement = req.SuggestedReplacement
		}
		return items, nil
	}

	if err := validateAnchor(req.BlockID, req.Quote, req.StartLine, req.EndLine); err != nil {
		return nil, err
	}
	return []annotation.Annotation{{
		Kind: req.Kind, BlockID: req.BlockID, StartLine: req.StartLine, EndLine: req.EndLine,
		Quote: req.Quote, Prefix: req.Prefix, Suffix: req.Suffix,
		Comment: req.Comment, SuggestedReplacement: req.SuggestedReplacement,
	}}, nil
}

func validateSelectionPiece(index int, piece annotation.SelectionPiece) error {
	if err := validateAnchor(piece.BlockID, piece.Quote, piece.StartLine, piece.EndLine); err != nil {
		return fmt.Errorf("selection piece %d: %w", index, err)
	}
	return nil
}

func validateAnchor(blockID, quote string, startLine, endLine int) error {
	if strings.TrimSpace(blockID) == "" {
		return errors.New("annotation block_id is required")
	}
	if strings.TrimSpace(quote) == "" {
		return errors.New("annotation quote is required")
	}
	if startLine < 0 || endLine < 0 {
		return errors.New("annotation line numbers cannot be negative")
	}
	if startLine > 0 && endLine > 0 && endLine < startLine {
		return errors.New("annotation end_line cannot be before start_line")
	}
	return nil
}

func validateAnnotationKindAndSuggestion(kind annotation.Kind, replacement string) error {
	if err := (annotation.Annotation{Kind: kind, BlockID: "block", Quote: "quote"}).Validate(); err != nil {
		return err
	}
	if kind == annotation.KindSuggestion && strings.TrimSpace(replacement) == "" {
		return errors.New("suggestion suggested_replacement is required")
	}
	if kind != annotation.KindSuggestion && strings.TrimSpace(replacement) != "" {
		return errors.New("suggested_replacement is only valid for suggestions")
	}
	return nil
}

func validateAnnotationPatch(req annotationPatchRequest, current annotation.Annotation) error {
	if req.Kind != nil && *req.Kind == "" {
		return errors.New("annotation kind cannot be empty")
	}
	if req.BlockID != nil && strings.TrimSpace(*req.BlockID) == "" {
		return errors.New("annotation block_id cannot be empty")
	}
	if req.Quote != nil && strings.TrimSpace(*req.Quote) == "" {
		return errors.New("annotation quote cannot be empty")
	}
	if (req.StartLine != nil && *req.StartLine < 0) || (req.EndLine != nil && *req.EndLine < 0) {
		return errors.New("annotation line numbers cannot be negative")
	}
	startLine, endLine := current.StartLine, current.EndLine
	if req.StartLine != nil {
		startLine = *req.StartLine
	}
	if req.EndLine != nil {
		endLine = *req.EndLine
	}
	if startLine > 0 && endLine > 0 && endLine < startLine {
		return errors.New("annotation end_line cannot be before start_line")
	}

	kind := current.Kind
	if req.Kind != nil {
		kind = *req.Kind
	}
	replacement := current.SuggestedReplacement
	if req.Kind != nil && kind != current.Kind && req.SuggestedReplacement == nil {
		replacement = ""
	}
	if req.SuggestedReplacement != nil {
		replacement = *req.SuggestedReplacement
	}
	return validateAnnotationKindAndSuggestion(kind, replacement)
}

func hasAnyField(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		if _, ok := fields[name]; ok {
			return true
		}
	}
	return false
}

func decodeAnnotationBody(w http.ResponseWriter, r *http.Request, target any) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAnnotationBody))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "annotation request body is too large")
		} else {
			writeAPIError(w, http.StatusBadRequest, "could not read annotation request body")
		}
		return nil, false
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeAPIError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return nil, false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		writeAPIError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return nil, false
	}
	for name, value := range fields {
		if string(value) == "null" {
			writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("annotation field %q cannot be null", name))
			return nil, false
		}
	}
	return fields, true
}

func writeAnnotationMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrAnnotationNotFound):
		writeAPIError(w, http.StatusNotFound, "annotation not found")
	case errors.Is(err, session.ErrTerminalSessionMutation):
		writeAPIError(w, http.StatusConflict, "session is no longer open")
	case errors.Is(err, session.ErrInvalidAnnotation):
		writeAPIError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, session.ErrAnnotationExists):
		writeAPIError(w, http.StatusConflict, "annotation already exists")
	default:
		writeAPIError(w, http.StatusInternalServerError, "could not mutate annotation")
	}
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiErrorResponse{Error: message})
}

type sessionSubmitRequest struct {
	Verdict         *string `json:"verdict"`
	Summary         *string `json:"summary"`
	UseReformulated *bool   `json:"use_reformulated"`
}

// handleSessionSubmit accepts one token-authenticated review submission. The
// X-Yamdview-Token header is required and is never accepted in the JSON body.
func (s *Server) handleSessionSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.review == nil {
		http.NotFound(w, r)
		return
	}
	if !s.review.TokenMatches(r.Header.Get(SessionTokenHeader)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	var req sessionSubmitRequest
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(w, "request body must contain one JSON object", http.StatusBadRequest)
		return
	}
	if req.Verdict == nil || strings.TrimSpace(*req.Verdict) == "" || req.Summary == nil {
		http.Error(w, "verdict and summary are required", http.StatusBadRequest)
		return
	}

	if req.UseReformulated != nil && *req.UseReformulated {
		stored := s.review.ReformulatedResult()
		if stored == nil {
			http.Error(w, "no reformulation preview exists", http.StatusBadRequest)
			return
		}
		// Mark approval and store before Submit so ordering stays safe even
		// if Submit fails below. A concurrent SetReformulated racing this
		// write is acceptable: last writer wins on an idempotent preview.
		stored.ApprovedByUser = true
		s.review.SetReformulated(stored)
	}

	metadata := s.review.Metadata()
	if len(metadata.Choices) > 0 && !containsChoice(metadata.Choices, *req.Verdict) {
		http.Error(w, "verdict is not one of the session choices", http.StatusBadRequest)
		return
	}
	if err := s.review.Submit(*req.Verdict, *req.Summary); err != nil {
		if errors.Is(err, session.ErrInvalidTransition) {
			http.Error(w, "session is no longer open", http.StatusConflict)
			return
		}
		http.Error(w, "could not submit session", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"state": string(session.Submitted)})
}

// reformulateRequest is the browser-facing body of POST
// /api/session/reformulate. Both fields are optional; an empty object {} is
// valid and means "use the configured defaults".
type reformulateRequest struct {
	Summary *string `json:"summary"`
	Model   *string `json:"model"`
}

// reformulatedPreviewJSON mirrors feedback.Reformulated on the wire. It is a
// separate type so the preview shape stays stable even if the payload struct
// grows review-internal fields.
type reformulatedPreviewJSON struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Text           string `json:"text"`
	ApprovedByUser bool   `json:"approved_by_user"`
}

// diagnosticJSON gives llm.Diagnostic explicit wire names for the preview.
type diagnosticJSON struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// reformulateResponse is the always-200 preview response. Silent fallback
// contract: provider or validation failures yield applied=false plus
// diagnostics here — never an HTTP 5xx.
type reformulateResponse struct {
	Applied      bool                     `json:"applied"`
	Reformulated *reformulatedPreviewJSON `json:"reformulated"`
	Diagnostics  []diagnosticJSON         `json:"diagnostics"`
	Provider     string                   `json:"provider"`
	Model        string                   `json:"model"`
}

// handleSessionReformulate produces one token-gated reformulation preview.
// The reformulation capability itself is injected via WithReformulator; the
// endpoint returns 404 when no session or no capability is configured.
func (s *Server) handleSessionReformulate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.review == nil || s.reformulator == nil {
		writeAPIError(w, http.StatusNotFound, "review session not found")
		return
	}

	if !s.review.TokenMatches(r.Header.Get(SessionTokenHeader)) {
		writeAPIError(w, http.StatusForbidden, "invalid or missing session token")
		return
	}
	if s.review.CurrentState() != session.Open {
		writeAPIError(w, http.StatusConflict, "session is no longer open")
		return
	}

	var req reformulateRequest
	if _, ok := decodeReformulateBody(w, r, &req); !ok {
		return
	}

	metadata := s.review.Metadata()
	model := s.respondMeta.Model
	if req.Model != nil && strings.TrimSpace(*req.Model) != "" {
		model = strings.TrimSpace(*req.Model)
	}
	reformulateReq := feedback.ReformulateRequest{
		Title:   metadata.Title,
		Prompt:  metadata.Prompt,
		Verdict: "",
		Summary: metadata.Summary,
	}
	if req.Summary != nil {
		reformulateReq.Summary = *req.Summary
	}

	result := s.reformulator(r.Context(), model, reformulateReq, s.review.AnnotationSnapshot())

	resp := reformulateResponse{
		Diagnostics: make([]diagnosticJSON, 0, len(result.Diagnostics)),
		Provider:    s.respondMeta.Provider,
		Model:       model,
	}
	for _, diag := range result.Diagnostics {
		resp.Diagnostics = append(resp.Diagnostics, diagnosticJSON{
			Severity: diag.Severity,
			Code:     diag.Code,
			Message:  diag.Message,
		})
	}
	if result.Applied && result.Reformulated != nil {
		resp.Applied = true
		resp.Provider = result.Reformulated.Provider
		resp.Model = result.Reformulated.Model
		resp.Reformulated = &reformulatedPreviewJSON{
			Provider:       result.Reformulated.Provider,
			Model:          result.Reformulated.Model,
			Text:           result.Reformulated.Text,
			ApprovedByUser: false,
		}
		// Store the unapproved preview so a later submit with
		// use_reformulated=true can approve it.
		s.review.SetReformulated(result.Reformulated)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// decodeReformulateBody decodes the strict JSON request body. It reuses the
// annotation body rules: size-capped read, unknown fields rejected, exactly
// one JSON object, no null values.
func decodeReformulateBody(w http.ResponseWriter, r *http.Request, target any) (map[string]json.RawMessage, bool) {
	return decodeAnnotationBody(w, r, target)
}

// containsChoice reports whether verdict appears in choices verbatim.
// A missing choice list means any verdict is accepted.
func containsChoice(choices []string, verdict string) bool {
	for _, choice := range choices {
		if choice == verdict {
			return true
		}
	}
	return false
}

func (s *Server) handleClientError(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var errs []ClientError
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxClientErrorBody))
	if err := decoder.Decode(&errs); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(errs) > maxClientErrors {
		http.Error(w, "too many client errors", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(w, "request body must contain one JSON array", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "ok")

	for _, ce := range errs {
		log.Printf("client error: block=%s kind=%s msg=%s", ce.BlockID, ce.Kind, ce.Message)
		if s.onClientError != nil {
			s.onClientError(ce)
		}
	}
}

// RenderPage renders the page template to a byte slice using the given assets.
// This is useful for generating static HTML or for testing.
func RenderPage(assets Assets, data PageData) ([]byte, error) {
	data.ensureAssets(assets)

	tmpl, err := template.New("index").Parse(assets.IndexHTML)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

// ExportStandalone renders a single self-contained HTML document suitable
// for distribution. When view is non-empty, it injects a CSS override that
// fixes the content column width for the named target viewport (phone,
// tablet, laptop, desktop).
func ExportStandalone(assets Assets, data PageData, view string) (string, error) {
	data.ensureAssets(assets)

	if view != "" {
		if !ValidExportView(view) {
			return "", fmt.Errorf("unknown --export-view %q; valid values: %s", view, ExportViewNames)
		}
		override := fmt.Sprintf(
			"\n/* yamdview export: fixed viewport */\n:root{--measure:%s !important}\n",
			exportViewMeasure(ExportView(view)),
		)
		data.CSS = template.CSS(string(data.CSS) + override)
	}

	b, err := RenderPage(assets, data)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
