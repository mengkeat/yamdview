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
	"github.com/mengkeat/yamdview/internal/llm"
	"github.com/mengkeat/yamdview/internal/llmapp"
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
	LLM          llm.Settings
}

// App orchestrates rendering, serving, and browser opening.
type App struct {
	cfg    Config
	md     goldmark.Markdown
	assets server.Assets

	// LLM repair state. provider is nil when repair is disabled (mode off) or
	// when provider construction failed. llmMode governs whether the render
	// path runs repairs automatically.
	provider   llm.Provider
	llmMode    llm.Mode
	llmTimeout time.Duration
}

// New creates a new App with the given configuration. LLM provider
// construction failures are logged as warnings rather than fatal: a
// misconfigured repair backend must not prevent the viewer from rendering.
func New(cfg Config, assets server.Assets) *App {
	provider, mode, timeout, err := buildLLMProvider(cfg.LLM)
	if err != nil {
		log.Printf("warning: llm provider not available: %v", err)
	}
	return &App{
		cfg:        cfg,
		md:         markdown.NewRenderer(),
		assets:     assets,
		provider:   provider,
		llmMode:    mode,
		llmTimeout: timeout,
	}
}

// buildLLMProvider loads the optional config file and resolves the provider
// described by settings. It returns a nil provider (and no error) when repair
// is off.
func buildLLMProvider(s llm.Settings) (llm.Provider, llm.Mode, time.Duration, error) {
	if s.Mode == llm.ModeOff {
		return nil, s.Mode, s.Timeout, nil
	}
	var cfg llm.Config
	if s.ConfigPath != "" {
		data, err := os.ReadFile(s.ConfigPath)
		if err != nil {
			return nil, s.Mode, s.Timeout, fmt.Errorf("read llm config: %w", err)
		}
		cfg, err = llm.ParseConfig(data)
		if err != nil {
			return nil, s.Mode, s.Timeout, err
		}
	}
	provider, err := llm.ResolveProvider(cfg, s)
	if err != nil {
		return nil, s.Mode, s.Timeout, err
	}
	return provider, s.Mode, s.Timeout, nil
}

// llmContext returns a context bounded by the configured per-call timeout.
func (a *App) llmContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := a.llmTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(parent, timeout)
}

// repairSnapshot runs the LLM repair pass over the snapshot when repair is
// enabled and in automatic mode. It never mutates the caller's snapshot:
// accepted repairs produce a new snapshot; rejected/stale/timed-out/failed
// candidates are reported as diagnostics and leave the rendering unchanged.
// It returns the (possibly repaired) snapshot and a flat diagnostic list.
func (a *App) repairSnapshot(ctx context.Context, src []byte, snap document.DocumentSnapshot) (document.DocumentSnapshot, []document.Diagnostic) {
	if a.provider == nil || a.llmMode != llm.ModeAuto {
		return snap, nil
	}
	res := llmapp.Repair(ctx, a.md, a.provider, snap, src)
	for _, d := range res.Diagnostics {
		log.Printf("llm: %s lines %d-%d: %s", d.Code, d.StartLine, d.EndLine, d.Message)
	}
	if res.Applied > 0 || res.Rejected > 0 {
		log.Printf("llm: %d applied, %d rejected via %s", res.Applied, res.Rejected, a.provider.Name())
	}
	return res.Snapshot, res.Diagnostics
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
	src, snapshot, err := a.readAndSnapshot(document.DocumentSnapshot{})
	if err != nil {
		return fmt.Errorf("render markdown: %w", err)
	}
	if a.cfg.WriteFixes != fixer.WriteModeNever {
		if err := a.persistFixes(src, snapshot); err != nil {
			return fmt.Errorf("persist fixes: %w", err)
		}
	}

	// LLM repair is render-only and runs after heuristic persistence so it can
	// never accidentally write a provider-suggested change to the source file.
	ctx, cancel := a.llmContext(context.Background())
	defer cancel()
	snapshot, _ = a.repairSnapshot(ctx, src, snapshot)

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
	src, snapshot, err := a.readAndSnapshot(document.DocumentSnapshot{})
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
	repairCtx, repairCancel := a.llmContext(context.Background())
	snapshot, _ = a.repairSnapshot(repairCtx, src, snapshot)
	repairCancel()

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

// readAndSnapshot reads the Markdown file and builds a block-oriented
// snapshot. prev supplies the previous snapshot so unchanged blocks can be
// reused without re-rendering; pass a zero value for the first render.
func (a *App) readAndSnapshot(prev document.DocumentSnapshot) ([]byte, document.DocumentSnapshot, error) {
	data, err := os.ReadFile(a.cfg.MarkdownPath)
	if err != nil {
		return nil, document.DocumentSnapshot{}, fmt.Errorf("read %s: %w", a.cfg.MarkdownPath, err)
	}

	snapshot, err := document.BuildSnapshot(a.md, data, prev)
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
			src, next, err := a.readAndSnapshot(current)
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
