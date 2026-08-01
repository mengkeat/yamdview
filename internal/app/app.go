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
	"github.com/mengkeat/yamdview/internal/fixer"
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
	WriteFixes   fixer.WriteMode
	BackupDir    string // directory for backup files when WriteFixes is backup
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
	src, snapshot, err := a.readAndSnapshot()
	if err != nil {
		return fmt.Errorf("render markdown: %w", err)
	}
	if a.cfg.WriteFixes != fixer.WriteModeNever {
		if err := a.persistFixes(src, snapshot); err != nil {
			return fmt.Errorf("persist fixes: %w", err)
		}
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
	src, snapshot, err := a.readAndSnapshot()
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	// Apply any heuristic fixes to the source file according to the
	// configured write mode. Render-only (never) leaves the file untouched.
	if a.cfg.WriteFixes != fixer.WriteModeNever {
		if err := a.persistFixes(src, snapshot); err != nil {
			log.Printf("warning: could not persist fixes: %v", err)
		}
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

// readAndSnapshot reads the Markdown file and builds a block-oriented snapshot.
func (a *App) readAndSnapshot() ([]byte, document.DocumentSnapshot, error) {
	data, err := os.ReadFile(a.cfg.MarkdownPath)
	if err != nil {
		return nil, document.DocumentSnapshot{}, fmt.Errorf("read %s: %w", a.cfg.MarkdownPath, err)
	}

	snapshot, err := document.BuildSnapshot(a.md, data)
	if err != nil {
		return nil, document.DocumentSnapshot{}, fmt.Errorf("render markdown: %w", err)
	}
	return data, snapshot, nil
}

// persistFixes collects table and math patches for the current source, writes
// them according to the configured WriteMode, and reports a concise summary
// to the CLI. The snapshot supplies the authoritative per-block table
// repairs; math conversion is recomputed from the repaired source.
func (a *App) persistFixes(src []byte, snapshot document.DocumentSnapshot) error {
	allPatches, tableCount, mathCount, err := fixer.CollectDocumentPatches(src, snapshot)
	if err != nil {
		return fmt.Errorf("collect patches: %w", err)
	}
	if len(allPatches) == 0 {
		logFixSummary(a.cfg.MarkdownPath, 0, 0, nil, "", a.cfg.WriteFixes)
		return nil
	}

	result, err := fixer.WriteFixes(a.cfg.MarkdownPath, a.cfg.WriteFixes, a.cfg.BackupDir, allPatches)
	if err != nil {
		return err
	}
	logFixSummary(a.cfg.MarkdownPath, tableCount, mathCount, allPatches, result.BackupPath, a.cfg.WriteFixes)
	return nil
}

// logFixSummary emits a single, parseable line describing the applied (or
// intentionally skipped) fixes. The format is stable so users and tests can
// rely on it.
func logFixSummary(path string, tableCount, mathCount int, patches []fixer.SourcePatch, backup string, mode fixer.WriteMode) {
	if len(patches) == 0 {
		if mode == fixer.WriteModeNever {
			log.Printf("fixes: 0 applied (mode=never) for %s", path)
		} else {
			log.Printf("fixes: 0 candidates for %s", path)
		}
		return
	}
	log.Printf("fixes: applied %d (%d table, %d math) to %s (mode=%s)", len(patches), tableCount, mathCount, path, mode)
	if backup != "" {
		log.Printf("fixes: backup written to %s", backup)
	}
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
			src, next, err := a.readAndSnapshot()
			if err != nil {
				log.Printf("warning: could not reload markdown: %v", err)
				continue
			}

			if a.cfg.WriteFixes != fixer.WriteModeNever {
				if err := a.persistFixes(src, next); err != nil {
					log.Printf("warning: could not persist fixes: %v", err)
				}
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
			if len(diff.Ops) == 0 {
				current = diff.Snapshot
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
