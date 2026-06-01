// Package app orchestrates the yamdview application lifecycle.
package app

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yuin/goldmark"

	"github.com/mengkeat/yamdview/internal/browser"
	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/markdown"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/watcher"
	"github.com/mengkeat/yamdview/web"
)

// Config holds the application configuration.
type Config struct {
	MarkdownPath string
	Addr         string // bind address, e.g. "127.0.0.1:0"
	NoOpen       bool   // do not open browser
	Debounce     time.Duration
	Export       string // export standalone HTML to this path (empty = serve)
	ExportView   string // viewport target for export
}

// App orchestrates rendering, serving, and browser opening.
type App struct {
	cfg    Config
	md     goldmark.Markdown
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

// Run executes the main application flow: export or serve + live reload.
func (a *App) Run() error {
	if a.cfg.Export != "" {
		return a.exportStandalone()
	}
	return a.serve()
}

// exportStandalone renders the Markdown file to a self-contained HTML document
// and writes it to the path specified by cfg.Export.
func (a *App) exportStandalone() error {
	snapshot, err := a.snapshotFile()
	if err != nil {
		return fmt.Errorf("render markdown: %w", err)
	}

	html, err := server.ExportStandalone(a.assets, server.PageData{
		Title:   a.cfg.MarkdownPath,
		Content: template.HTML(snapshot.HTML),
	}, a.cfg.ExportView)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}

	if err := os.WriteFile(a.cfg.Export, []byte(html), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", a.cfg.Export, err)
	}

	log.Printf("exported %s → %s", a.cfg.MarkdownPath, a.cfg.Export)
	return nil
}

// serve renders, starts the HTTP server, opens the browser, and reloads on changes.
func (a *App) serve() error {
	// Read, segment, and render the Markdown file.
	snapshot, err := a.snapshotFile()
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	// Create and start HTTP server.
	srv, err := server.New(a.cfg.Addr, a.assets, server.PageData{
		Title:   a.cfg.MarkdownPath,
		Content: template.HTML(snapshot.HTML),
	}, server.WithKatexFS(web.KatexFS()))
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}
	defer srv.Close()

	srv.Start()
	log.Printf("serving %s at %s", a.cfg.MarkdownPath, srv.URL())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fileWatcher, err := watcher.New(a.cfg.MarkdownPath, a.cfg.Debounce)
	if err != nil {
		return fmt.Errorf("watch markdown file: %w", err)
	}
	defer fileWatcher.Close()

	changes, watchErrs := fileWatcher.Watch(ctx)
	go a.reloadLoop(ctx, srv, changes, watchErrs, snapshot)

	// Open browser unless suppressed.
	if !a.cfg.NoOpen {
		if err := browser.Open(srv.URL()); err != nil {
			log.Printf("warning: could not open browser: %v", err)
		}
	}

	// Wait for interrupt.
	<-ctx.Done()
	log.Println("shutting down")
	return nil
}

// renderFile reads the Markdown file and returns section-wrapped rendered HTML.
func (a *App) renderFile() (template.HTML, error) {
	snapshot, err := a.snapshotFile()
	if err != nil {
		return "", err
	}
	return template.HTML(snapshot.HTML), nil
}

// snapshotFile reads the Markdown file and builds a block-oriented snapshot.
func (a *App) snapshotFile() (document.DocumentSnapshot, error) {
	data, err := os.ReadFile(a.cfg.MarkdownPath)
	if err != nil {
		return document.DocumentSnapshot{}, fmt.Errorf("read %s: %w", a.cfg.MarkdownPath, err)
	}

	snapshot, err := document.BuildSnapshot(a.md, data)
	if err != nil {
		return document.DocumentSnapshot{}, fmt.Errorf("render markdown: %w", err)
	}

	return snapshot, nil
}

func (a *App) reloadLoop(ctx context.Context, srv *server.Server, changes <-chan watcher.Event, watchErrs <-chan error, current document.DocumentSnapshot) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-watchErrs:
			if !ok {
				watchErrs = nil
				continue
			}
			log.Printf("warning: watcher error: %v", err)
		case _, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			next, err := a.snapshotFile()
			if err != nil {
				log.Printf("warning: could not reload markdown: %v", err)
				continue
			}

			diff := document.Diff(current, next)
			content := template.HTML(diff.Snapshot.HTML)
			if diff.Reset {
				if err := srv.BroadcastReset(content); err != nil {
					log.Printf("warning: could not broadcast reset: %v", err)
					continue
				}
				current = diff.Snapshot
				log.Printf("reloaded %s with full reset", a.cfg.MarkdownPath)
				continue
			}

			if err := srv.BroadcastPatches(content, diff.Ops); err != nil {
				log.Printf("warning: could not broadcast patches: %v", err)
				continue
			}
			current = diff.Snapshot
			log.Printf("patched %s (%d ops)", a.cfg.MarkdownPath, len(diff.Ops))
		}
	}
}
