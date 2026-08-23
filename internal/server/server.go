// Package server provides the local HTTP server that serves the Markdown viewer.
package server

import (
	"bytes"
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

	"github.com/mengkeat/yamdview/internal/document"
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
	Title   string
	Content template.HTML
	CSS     template.CSS
	JS      template.JS
	Review  *ReviewPageData
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
}

// Assets provides the embedded web assets (template, CSS, JS).
type Assets struct {
	IndexHTML string
	ViewerCSS string
	ViewerJS  string
}

// ensureAssets fills empty CSS/JS fields from the provided assets.
func (pd *PageData) ensureAssets(assets Assets) {
	if pd.CSS == "" {
		pd.CSS = template.CSS(assets.ViewerCSS)
	}
	if pd.JS == "" {
		pd.JS = template.JS(assets.ViewerJS)
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
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Prompt      string    `json:"prompt"`
	Choices     []string  `json:"choices"`
	State       string    `json:"state"`
	Verdict     string    `json:"verdict"`
	Summary     string    `json:"summary"`
	Revision    int       `json:"revision"`
	OpenedAt    time.Time `json:"opened_at"`
	SubmittedAt time.Time `json:"submitted_at,omitempty"`
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
	})
}

type sessionSubmitRequest struct {
	Verdict *string `json:"verdict"`
	Summary *string `json:"summary"`
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
