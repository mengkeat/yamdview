package agentapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/session"
)

// maxAPIBody caps JSON request bodies at 10 MiB, comfortably above any sane
// Markdown document while bounding memory use per request.
const maxAPIBody = 10 << 20

// tokenBytes is the entropy of the API bearer token (hex-encoded on the wire).
const tokenBytes = 32

// Server is the long-running agent API server: bearer-token-authenticated
// /api/v1 endpoints plus per-session viewer pages under /sessions/<id>/.
type Server struct {
	manager  *Manager
	token    string
	listener net.Listener
	handler  http.Handler
	httpSrv  *http.Server

	lifecycleMu sync.Mutex
	started     bool
	closed      bool
	serveDone   chan struct{}
}

// New opens a listener on addr (empty defaults to "127.0.0.1:0") and builds
// the API server around it. Loopback enforcement is the CLI layer's job;
// this package itself accepts any address so tests and embedding are simple.
func New(assets server.Assets, addr string) (*Server, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	token, err := randomToken()
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("generate api token: %w", err)
	}

	s := &Server{
		manager:   NewManager(assets, "http://"+ln.Addr().String()),
		token:     token,
		listener:  ln,
		serveDone: make(chan struct{}),
	}
	s.handler = s.buildHandler()
	s.httpSrv = &http.Server{Handler: s.handler}
	return s, nil
}

// buildHandler wires the route table: authenticated /api/v1 endpoints and
// per-session viewer mounts.
func (s *Server) buildHandler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/sessions", s.handleCreate)
	api.HandleFunc("POST /api/v1/sessions/{id}/append", s.handleAppend)
	api.HandleFunc("POST /api/v1/sessions/{id}/complete", s.handleComplete)
	api.HandleFunc("PUT /api/v1/sessions/{id}/document", s.handleReplaceDocument)
	api.HandleFunc("GET /api/v1/sessions/{id}/feedback", s.handleFeedback)
	api.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDelete)

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", s.requireAuth(api))
	mux.HandleFunc("/sessions/", s.handleViewer)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Minimal root page: enough for a human to see the server is alive.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "yamdview agent api")
	})
	return mux
}

// Token returns the API bearer token so the application layer can log it.
// It is a secret: print it to the local terminal, never to API responses.
func (s *Server) Token() string { return s.token }

// Manager returns the underlying session manager, shared with the MCP
// server in-process integration.
func (s *Server) Manager() *Manager { return s.manager }

// Handler returns the routed handler, for tests or custom mounts.
func (s *Server) Handler() http.Handler { return s.handler }

// Addr returns the listening address.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// URL returns the http:// base URL of the API server (no trailing slash).
func (s *Server) URL() string { return "http://" + s.Addr() }

// Start begins serving API requests in a new goroutine.
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
			s.Close()
		}
	}()
}

// Close shuts the listener, drops connections, and deletes every session
// (cancelling still-open ones so feedback waiters wake).
func (s *Server) Close() error {
	s.lifecycleMu.Lock()
	if s.closed {
		s.lifecycleMu.Unlock()
		return nil
	}
	s.closed = true
	started := s.started
	s.lifecycleMu.Unlock()

	err := s.httpSrv.Close()
	if started {
		<-s.serveDone
	}

	for _, id := range s.manager.ids() {
		_ = s.manager.Delete(id)
	}
	return err
}

func (s *Server) serve() error {
	defer close(s.serveDone)
	return s.httpSrv.Serve(s.listener)
}

// requireAuth enforces the bearer token on every /api/v1 route.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r.Header.Get("Authorization")) {
			writeAPIError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorized checks an Authorization header against the API token in
// constant time.
func (s *Server) authorized(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(s.token)) == 1
}

// handleViewer mounts each session's viewer under /sessions/<id>/ using
// StripPrefix, so the viewer's page-relative asset and API paths resolve
// inside the session subtree.
func (s *Server) handleViewer(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/sessions/")
	id, _, hasTail := strings.Cut(rest, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if !hasTail {
		// /sessions/<id> (no trailing slash): redirect so the viewer's
		// page-relative URLs resolve against /sessions/<id>/.
		http.Redirect(w, r, "/sessions/"+url.PathEscape(id)+"/", http.StatusMovedPermanently)
		return
	}
	handler, ok := s.manager.ViewerHandler(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/sessions/"+id, handler).ServeHTTP(w, r)
}

// createSessionRequest is the strict body of POST /api/v1/sessions.
type createSessionRequest struct {
	Markdown *string  `json:"markdown"`
	Title    string   `json:"title"`
	Prompt   string   `json:"prompt"`
	Choices  []string `json:"choices"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Markdown == nil {
		writeAPIError(w, http.StatusBadRequest, "markdown is required")
		return
	}

	info, err := s.manager.Create([]byte(*req.Markdown), CreateOptions{
		Title:   req.Title,
		Prompt:  req.Prompt,
		Choices: req.Choices,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

// appendRequest is the strict body of POST /api/v1/sessions/{id}/append.
type appendRequest struct {
	Markdown *string `json:"markdown"`
	Complete bool    `json:"complete"`
}

func (s *Server) handleAppend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req appendRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Markdown == nil {
		writeAPIError(w, http.StatusBadRequest, "markdown is required")
		return
	}

	result, err := s.manager.Append(id, []byte(*req.Markdown), req.Complete)
	if !writeManagerError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.manager.Complete(id); !writeManagerError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": StateComplete})
}

// replaceDocumentRequest is the strict body of PUT /api/v1/sessions/{id}/document.
type replaceDocumentRequest struct {
	Markdown *string `json:"markdown"`
}

func (s *Server) handleReplaceDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req replaceDocumentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Markdown == nil {
		writeAPIError(w, http.StatusBadRequest, "markdown is required")
		return
	}

	result, err := s.manager.ReplaceDocument(id, []byte(*req.Markdown))
	if !writeManagerError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	wait := time.Duration(0)
	if raw := r.URL.Query().Get("wait"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			writeAPIError(w, http.StatusBadRequest, `invalid "wait" duration; use e.g. ?wait=10s`)
			return
		}
		wait = d
	}

	sess, ok := s.manager.Get(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "session not found")
		return
	}
	if sess.CurrentState() == session.Open {
		if wait > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), wait)
			defer cancel()
			if err := s.manager.WaitFeedback(ctx, id); err != nil {
				switch {
				case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
					writeAPIError(w, http.StatusRequestTimeout, "feedback not ready; retry with a longer wait")
				case errors.Is(err, ErrSessionNotFound):
					writeAPIError(w, http.StatusNotFound, "session not found")
				default:
					writeAPIError(w, http.StatusInternalServerError, "could not wait for feedback")
				}
				return
			}
		}
		// Re-check: the session may have reached a terminal state just now.
		sess, ok = s.manager.Get(id)
		if !ok {
			writeAPIError(w, http.StatusNotFound, "session not found")
			return
		}
		if sess.CurrentState() == session.Open {
			writeAPIError(w, http.StatusRequestTimeout, "feedback not ready; retry with a longer wait")
			return
		}
	}

	payload, err := s.manager.Feedback(id)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			writeAPIError(w, http.StatusNotFound, "session not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "could not build feedback")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Delete(r.PathValue("id")); !writeManagerError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeManagerError maps Manager errors to HTTP responses, reporting false
// when the response has been written.
func writeManagerError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, ErrSessionNotFound):
		writeAPIError(w, http.StatusNotFound, "session not found")
	case errors.Is(err, session.ErrTerminalSessionMutation), errors.Is(err, session.ErrInvalidTransition):
		writeAPIError(w, http.StatusConflict, "session is no longer open")
	case errors.Is(err, session.ErrSessionStreaming):
		writeAPIError(w, http.StatusConflict, "document still streaming")
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal error")
	}
	return false
}

// decodeBody strictly decodes one capped-size JSON object.
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAPIBody))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		} else {
			writeAPIError(w, http.StatusBadRequest, "could not read request body")
		}
		return false
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeAPIError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return false
	}
	return true
}

type apiErrorResponse struct {
	Error string `json:"error"`
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiErrorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomToken() (string, error) {
	var token [tokenBytes]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}
