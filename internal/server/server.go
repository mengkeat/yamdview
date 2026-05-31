// Package server provides the local HTTP server that serves the Markdown viewer.
package server

import (
	"bytes"
	"fmt"
	"html/template"
	"net"
	"net/http"
)

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
	pageData PageData
	tmpl     *template.Template
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
	}

	// Serve the viewer page at /.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, s.pageData); err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
		}
	})

	// Snapshot endpoint returns only the rendered content HTML.
	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, s.pageData.Content)
	})

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
	s.pageData.Content = content
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
