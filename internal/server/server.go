// Package server provides the local HTTP server that serves the Markdown viewer.
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
)

// ExportView is a named viewport target for standalone export.
type ExportView string

const (
	ExportViewPhone   ExportView = "phone"
	ExportViewTablet  ExportView = "tablet"
	ExportViewLaptop  ExportView = "laptop"
	ExportViewDesktop ExportView = "desktop"
)

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
}

// Assets provides the embedded web assets (template, CSS, JS).
type Assets struct {
	IndexHTML string
	ViewerCSS string
	ViewerJS  string
}

// PageDataFromAssets creates a PageData with CSS and JS populated from assets.
func PageDataFromAssets(assets Assets, title string, content template.HTML) PageData {
	return PageData{
		Title:   title,
		Content: content,
		CSS:     template.CSS(assets.ViewerCSS),
		JS:      template.JS(assets.ViewerJS),
	}
}

// Server is the local HTTP server for the Markdown viewer.
type Server struct {
	listener net.Listener
	handler  http.Handler
	mu       sync.RWMutex
	pageData PageData
	tmpl     *template.Template

	clientsMu sync.Mutex
	clients   map[chan sseEvent]struct{}
}

type sseEvent struct {
	name string
	data string
}

type resetPayload struct {
	Op   string `json:"op"`
	HTML string `json:"html"`
}

// New creates a new Server that will listen on the given address.
// If addr is empty it defaults to "127.0.0.1:0" (random available port).
func New(addr string, assets Assets, data PageData) (*Server, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	// Inject assets into page data if not already set.
	if data.CSS == "" {
		data.CSS = template.CSS(assets.ViewerCSS)
	}
	if data.JS == "" {
		data.JS = template.JS(assets.ViewerJS)
	}

	tmpl, err := template.New("index").Parse(assets.IndexHTML)
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("parse template: %w", err)
	}

	mux := http.NewServeMux()

	s := &Server{
		listener: ln,
		pageData: data,
		tmpl:     tmpl,
		clients:  make(map[chan sseEvent]struct{}),
	}

	// Serve the viewer page at /.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := s.currentPageData()
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
		}
	})

	// Snapshot endpoint returns only the rendered content HTML.
	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, s.currentPageData().Content)
	})

	// Events endpoint streams live reload messages to the browser.
	mux.HandleFunc("/events", s.handleEvents)

	s.handler = mux

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
func (s *Server) Start() error {
	go func() {
		_ = s.Serve()
	}()
	return nil
}

// Serve serves HTTP requests on the listener. It blocks until the server
// encounters an error (including http.ErrServerClosed).
func (s *Server) Serve() error {
	return http.Serve(s.listener, s.handler)
}

// Close immediately closes the listener.
func (s *Server) Close() error {
	return s.listener.Close()
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
	payload, err := json.Marshal(resetPayload{Op: "reset", HTML: string(content)})
	if err != nil {
		return fmt.Errorf("marshal reset payload: %w", err)
	}

	s.SetContent(content)
	s.broadcast(sseEvent{name: "reset", data: string(payload)})
	return nil
}

func (s *Server) currentPageData() PageData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pageData
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

// RenderPage renders the page template to a byte slice using the given assets.
// This is useful for generating static HTML or for testing.
func RenderPage(assets Assets, data PageData) ([]byte, error) {
	if data.CSS == "" {
		data.CSS = template.CSS(assets.ViewerCSS)
	}
	if data.JS == "" {
		data.JS = template.JS(assets.ViewerJS)
	}

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
	if view != "" {
		if !ValidExportView(view) {
			return "", fmt.Errorf("unknown --export-view %q (valid: phone, tablet, laptop, desktop)", view)
		}
		override := "\n/* yamdview export: fixed viewport */\n:root{--measure:" +
			exportViewMeasure(ExportView(view)) + " !important}\n"
		data.CSS = template.CSS(string(data.CSS) + override)
	}

	b, err := RenderPage(assets, data)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
