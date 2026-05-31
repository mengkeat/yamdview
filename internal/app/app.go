// Package app orchestrates the yamdview application lifecycle.
package app

import (
	"fmt"
	"html/template"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mengkeat/yamdview/internal/browser"
	"github.com/mengkeat/yamdview/internal/markdown"
	"github.com/mengkeat/yamdview/internal/server"
)

// Config holds the application configuration.
type Config struct {
	MarkdownPath string
	Addr         string // bind address, e.g. "127.0.0.1:0"
	NoOpen       bool   // do not open browser
}

// App orchestrates rendering, serving, and browser opening.
type App struct {
	cfg    Config
	md     markdown.Renderer
	assets server.Assets
}

// New creates a new App with the given configuration.
func New(cfg Config, assets server.Assets) *App {
	return &App{
		cfg:    cfg,
		md:     markdown.NewRenderer(),
		assets: assets,
	}
}

// Run executes the main application flow: render, serve, open browser, wait for signal.
func (a *App) Run() error {
	// Read and render the Markdown file.
	content, err := a.renderFile()
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	// Create and start HTTP server.
	srv, err := server.New(a.cfg.Addr, a.assets, server.PageData{
		Title:   a.cfg.MarkdownPath,
		Content: content,
	})
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}
	defer srv.Close()

	srv.Start()
	log.Printf("serving %s at %s", a.cfg.MarkdownPath, srv.URL())

	// Open browser unless suppressed.
	if !a.cfg.NoOpen {
		if err := browser.Open(srv.URL()); err != nil {
			log.Printf("warning: could not open browser: %v", err)
		}
	}

	// Wait for interrupt.
	a.waitForSignal()
	log.Println("shutting down")
	return nil
}

// renderFile reads the Markdown file and returns rendered HTML.
func (a *App) renderFile() (template.HTML, error) {
	data, err := os.ReadFile(a.cfg.MarkdownPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", a.cfg.MarkdownPath, err)
	}

	html, err := markdown.Render(a.md, data)
	if err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}

	return template.HTML(html), nil
}

// waitForSignal blocks until SIGINT or SIGTERM is received.
func (a *App) waitForSignal() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}
