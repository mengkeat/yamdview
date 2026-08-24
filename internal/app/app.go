// Package app orchestrates the yamdview application lifecycle.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/yuin/goldmark"

	"github.com/mengkeat/yamdview/internal/annotation"
	"github.com/mengkeat/yamdview/internal/browser"
	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/fixer"
	"github.com/mengkeat/yamdview/internal/llm"
	"github.com/mengkeat/yamdview/internal/llmapp"
	"github.com/mengkeat/yamdview/internal/markdown"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/session"
	"github.com/mengkeat/yamdview/internal/watcher"
	"github.com/mengkeat/yamdview/web"
)

// Mode selects the application flow requested by the command line.
type Mode string

const (
	ModeView   Mode = "view"
	ModeReview Mode = "review"
)

// ReviewConfig contains options specific to a blocking review session.
type ReviewConfig struct {
	Title   string
	Prompt  string
	Choices []string
	Format  feedback.Format
	Output  string
	Timeout time.Duration
	Watch   bool
	Respond llm.RespondSettings // LLM feedback reformulation for this review
}

// ReviewExitStatus is the stable process status for a completed review.
type ReviewExitStatus int

const (
	ReviewSubmitted ReviewExitStatus = 0
	ReviewTimeout   ReviewExitStatus = 2
	ReviewCancelled ReviewExitStatus = 3
	ReviewInternal  ReviewExitStatus = 4
)

func (s ReviewExitStatus) Code() int { return int(s) }

// ReviewExitError distinguishes a non-submitted review from an ordinary
// viewer error when callers use Run rather than RunReview.
type ReviewExitError struct{ Status ReviewExitStatus }

func (e *ReviewExitError) Error() string { return fmt.Sprintf("review ended with status %d", e.Status) }

// Config holds the application configuration.
type Config struct {
	Mode         Mode
	MarkdownPath string
	Addr         string // bind address, e.g. "127.0.0.1:0"
	NoOpen       bool   // do not open browser
	Debounce     time.Duration
	Export       string // export standalone HTML to this path (empty = serve)
	ExportView   string // viewport target for export
	WriteFixes   fixer.WriteMode
	BackupDir    string // directory for backup files when WriteFixes is backup
	LLM          llm.Settings
	Review       ReviewConfig
	Input        io.Reader
	Output       io.Writer
	Context      context.Context
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

	reviewMu     sync.RWMutex
	review       *session.Session
	reviewServer *server.Server

	// resolveRespond constructs the feedback-reformulation provider from a
	// loaded llm.Config and the Respond settings. It defaults to
	// llm.ResolveRespondProvider; tests stub this field to inject mock
	// providers without network access or credential environment variables.
	resolveRespond func(cfg llm.Config, s llm.RespondSettings) (llm.Provider, string, error)
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
		cfg:            cfg,
		md:             markdown.NewRenderer(),
		assets:         assets,
		provider:       provider,
		llmMode:        mode,
		llmTimeout:     timeout,
		resolveRespond: llm.ResolveRespondProvider,
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
		var err error
		cfg, err = llm.ParseConfigFile(s.ConfigPath, os.ReadFile)
		if err != nil {
			return nil, s.Mode, s.Timeout, fmt.Errorf("load llm config: %w", err)
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

// respondContext returns a context bounded by the configured reformulation
// call timeout, or 30 seconds when no explicit timeout is set.
func (a *App) respondContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := a.cfg.Review.Respond.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(parent, timeout)
}

// buildReformulator constructs the review-mode feedback-reformulation
// capability from cfg.Review.Respond: it loads the provider config file,
// resolves the named provider (including credential checks), and wraps it in
// a server.ReformulateFunc with silent-fallback semantics. Misconfiguration
// never fails the review; it logs one warning and reports ok=false so the
// review server simply runs without the reformulate endpoint.
func (a *App) buildReformulator() (fn server.ReformulateFunc, meta server.RespondMeta, ok bool) {
	settings := a.cfg.Review.Respond
	if settings.Mode == llm.ModeOff {
		return nil, server.RespondMeta{}, false
	}
	cfg, err := llm.ParseConfigFile(settings.ConfigPath, os.ReadFile)
	if err != nil {
		log.Printf("warning: respond llm disabled: %v", err)
		return nil, server.RespondMeta{}, false
	}
	resolve := a.resolveRespond
	if resolve == nil {
		resolve = llm.ResolveRespondProvider
	}
	provider, model, err := resolve(cfg, settings)
	if err != nil {
		log.Printf("warning: respond llm disabled: %v", err)
		return nil, server.RespondMeta{}, false
	}
	var models []string
	if pc, found := cfg.Providers[settings.ProviderName]; found {
		models = pc.ModelChoices()
	}
	meta = server.RespondMeta{
		Provider: provider.Name(),
		Model:    model,
		Models:   models,
		Mode:     string(settings.Mode),
	}
	fn = func(ctx context.Context, model string, req feedback.ReformulateRequest, annotations []annotation.Annotation) feedback.ReformulateResult {
		callCtx, cancel := a.respondContext(ctx)
		defer cancel()
		result, _ := feedback.Reformulate(callCtx, provider, model, req, annotations)
		return result
	}
	return fn, meta, true
}

// repairSnapshot runs the LLM repair pass over the snapshot when repair is
// enabled and in automatic mode. It never mutates the caller's snapshot:
// accepted repairs produce a new snapshot; rejected/stale/timed-out/failed
// candidates are reported as diagnostics and leave the rendering unchanged.
// It returns the (possibly repaired) snapshot.
func (a *App) repairSnapshot(ctx context.Context, src []byte, snap document.DocumentSnapshot) document.DocumentSnapshot {
	if a.provider == nil || a.llmMode != llm.ModeAuto {
		return snap
	}
	res := llmapp.Repair(ctx, a.md, a.provider, snap, src)
	for _, d := range res.Diagnostics {
		log.Printf("llm: %s lines %d-%d: %s", d.Code, d.StartLine, d.EndLine, d.Message)
	}
	if res.Applied > 0 || res.Rejected > 0 {
		log.Printf("llm: %d applied, %d rejected via %s", res.Applied, res.Rejected, a.provider.Name())
	}
	return res.Snapshot
}

// Run executes the configured application flow.
func (a *App) Run() error {
	if a.cfg.Mode == ModeReview {
		status, err := a.RunReview()
		if err != nil {
			return err
		}
		if status != ReviewSubmitted {
			return &ReviewExitError{Status: status}
		}
		return nil
	}
	return a.RunViewer()
}

// RunViewer executes the ordinary export or live-viewer flow.
func (a *App) RunViewer() error {
	if a.cfg.Export != "" {
		return a.exportStandalone()
	}
	return a.serve()
}

// ReviewSession returns the active review session, if one is running.
func (a *App) ReviewSession() *session.Session {
	a.reviewMu.RLock()
	defer a.reviewMu.RUnlock()
	return a.review
}

// ReviewURL returns the active review server URL, or empty before setup.
func (a *App) ReviewURL() string {
	a.reviewMu.RLock()
	defer a.reviewMu.RUnlock()
	if a.reviewServer == nil {
		return ""
	}
	return a.reviewServer.URL()
}

// RunReview starts a frozen review session and blocks until submission,
// timeout, or cancellation. It emits one payload for non-internal outcomes.
func (a *App) RunReview() (ReviewExitStatus, error) {
	src, snapshot, err := a.readReviewSnapshot()
	if err != nil {
		return ReviewInternal, fmt.Errorf("render review: %w", err)
	}
	id, err := newReviewID()
	if err != nil {
		return ReviewInternal, fmt.Errorf("create review session: %w", err)
	}
	title := a.cfg.Review.Title
	if title == "" {
		title = a.cfg.MarkdownPath
	}
	choices := a.cfg.Review.Choices
	if len(choices) == 0 {
		choices = []string{"approve", "request_changes", "comment"}
	}
	review, err := session.New(id, title, a.cfg.Review.Prompt, choices, src, snapshot)
	if err != nil {
		return ReviewInternal, fmt.Errorf("create review session: %w", err)
	}
	opts := []server.Option{server.WithKatexFS(web.KatexFS()), server.WithSession(review)}
	reformulateFn, respondMeta, respondOK := a.buildReformulator()
	if respondOK {
		// Built before Start so the reformulate endpoint is available from the
		// first served page.
		opts = append(opts, server.WithReformulator(reformulateFn, respondMeta))
	}
	srv, err := server.New(a.cfg.Addr, a.assets, server.PageDataFromAssets(a.assets, title, template.HTML(snapshot.HTML)), opts...)
	if err != nil {
		return ReviewInternal, fmt.Errorf("create review server: %w", err)
	}
	a.reviewMu.Lock()
	a.review = review
	a.reviewServer = srv
	a.reviewMu.Unlock()
	defer func() {
		_ = srv.Close()
		a.reviewMu.Lock()
		a.review = nil
		a.reviewServer = nil
		a.reviewMu.Unlock()
	}()
	srv.Start()
	log.Printf("reviewing %s at %s", a.cfg.MarkdownPath, srv.URL())

	ctx := a.cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	reviewCtx, cancelReview := context.WithCancel(ctx)
	var reloadDone chan struct{}
	defer func() {
		cancelReview()
		if reloadDone != nil {
			<-reloadDone
		}
	}()

	// The default is frozen. Stdin is never passed to watcher.New.
	if a.cfg.Review.Watch && a.cfg.MarkdownPath != "-" {
		fileWatcher, watchErr := watcher.New(a.cfg.MarkdownPath, a.cfg.Debounce)
		if watchErr != nil {
			return ReviewInternal, fmt.Errorf("watch markdown file: %w", watchErr)
		}
		defer fileWatcher.Close()
		changes, watchErrs := fileWatcher.Watch(reviewCtx)
		reloadDone = make(chan struct{})
		go func() {
			defer close(reloadDone)
			a.reloadLoopWithReview(reviewCtx, srv, changes, watchErrs, snapshot, review)
		}()
	}
	if !a.cfg.NoOpen {
		if err := browser.Open(srv.URL()); err != nil {
			log.Printf("warning: could not open browser: %v", err)
		}
	}

	var timer <-chan time.Time
	var timerStop func()
	if a.cfg.Review.Timeout > 0 {
		t := time.NewTimer(a.cfg.Review.Timeout)
		timer = t.C
		timerStop = func() { t.Stop() }
	}
	if timerStop != nil {
		defer timerStop()
	}
	var status ReviewExitStatus
	select {
	case <-review.Done():
	case <-timer:
		if err := review.Timeout(); err != nil && !errors.Is(err, session.ErrInvalidTransition) {
			return ReviewInternal, fmt.Errorf("timeout review: %w", err)
		}
	case <-ctx.Done():
		if err := review.Cancel(); err != nil && !errors.Is(err, session.ErrInvalidTransition) {
			return ReviewInternal, fmt.Errorf("cancel review: %w", err)
		}
	}
	cancelReview()
	if reloadDone != nil {
		<-reloadDone
	}
	switch review.CurrentState() {
	case session.Submitted:
		status = ReviewSubmitted
	case session.Timeout:
		status = ReviewTimeout
	case session.Cancelled:
		status = ReviewCancelled
	default:
		return ReviewInternal, errors.New("review ended without a terminal state")
	}
	if respondOK && a.cfg.Review.Respond.Mode == llm.ModeAuto {
		a.runAutoReformulate(review, reformulateFn)
	}
	if err := a.writeReviewFeedback(review); err != nil {
		return ReviewInternal, err
	}
	return status, nil
}

// runAutoReformulate performs the automatic reformulation pass for auto mode:
// one attempt after the review reaches a terminal state, skipped when a user
// already generated (and possibly approved) a preview. Diagnostics are logged;
// an applied result is stored unapproved alongside the raw feedback.
func (a *App) runAutoReformulate(review *session.Session, fn server.ReformulateFunc) {
	if review.ReformulatedResult() != nil {
		return
	}
	metadata := review.Metadata()
	ctx, cancel := a.respondContext(context.Background())
	defer cancel()
	result := fn(ctx, "", feedback.ReformulateRequest{
		Title:   metadata.Title,
		Prompt:  metadata.Prompt,
		Verdict: metadata.Verdict,
		Summary: metadata.Summary,
	}, review.AnnotationSnapshot())
	for _, diag := range result.Diagnostics {
		log.Printf("respond llm: %s: %s", diag.Code, diag.Message)
	}
	if result.Applied && result.Reformulated != nil {
		review.SetReformulated(result.Reformulated)
	}
}

func (a *App) writeReviewFeedback(review *session.Session) error {
	metadata := review.Metadata()
	duration := metadata.SubmittedAt.Sub(metadata.OpenedAt).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	payload := feedback.Payload{
		Version: feedback.CurrentVersion, SessionID: metadata.ID,
		Title: metadata.Title, Prompt: metadata.Prompt,
		Verdict: metadata.Verdict, Summary: metadata.Summary, Comments: review.AnnotationSnapshot(),
		Timing: feedback.Timing{OpenedAt: metadata.OpenedAt, SubmittedAt: metadata.SubmittedAt, DurationMS: duration},
	}
	format := a.cfg.Review.Format
	if format == "" {
		format = feedback.FormatJSON
	}
	data, err := feedback.Render(payload, string(format))
	if err != nil {
		return fmt.Errorf("render review feedback: %w", err)
	}
	if a.cfg.Review.Output != "" && a.cfg.Review.Output != "-" {
		file, err := os.Create(a.cfg.Review.Output)
		if err != nil {
			return fmt.Errorf("create feedback output %s: %w", a.cfg.Review.Output, err)
		}
		defer file.Close()
		if _, err := io.WriteString(file, data); err != nil {
			return fmt.Errorf("write feedback output %s: %w", a.cfg.Review.Output, err)
		}
		return nil
	}
	output := a.cfg.Output
	if output == nil {
		output = os.Stdout
	}
	if _, err := io.WriteString(output, data); err != nil {
		return fmt.Errorf("write review feedback: %w", err)
	}
	return nil
}

func (a *App) readReviewSnapshot() ([]byte, document.DocumentSnapshot, error) {
	var data []byte
	var err error
	if a.cfg.MarkdownPath == "-" {
		input := a.cfg.Input
		if input == nil {
			input = os.Stdin
		}
		data, err = io.ReadAll(input)
	} else {
		data, err = os.ReadFile(a.cfg.MarkdownPath)
	}
	if err != nil {
		return nil, document.DocumentSnapshot{}, fmt.Errorf("read %s: %w", a.cfg.MarkdownPath, err)
	}
	snapshot, err := document.BuildSnapshot(a.md, data, document.DocumentSnapshot{})
	if err != nil {
		return nil, document.DocumentSnapshot{}, err
	}
	return data, snapshot, nil
}

func newReviewID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("s-%d-%s", time.Now().UTC().UnixNano(), hex.EncodeToString(random[:])), nil
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
	snapshot = a.repairSnapshot(ctx, src, snapshot)

	html, err := server.ExportStandalone(a.assets, server.PageDataFromAssets(a.assets, a.cfg.MarkdownPath, template.HTML(snapshot.HTML)), a.cfg.ExportView)
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
	snapshot = a.repairSnapshot(repairCtx, src, snapshot)
	repairCancel()

	// Create and start HTTP server.
	srv, err := server.New(a.cfg.Addr, a.assets, server.PageDataFromAssets(a.assets, a.cfg.MarkdownPath, template.HTML(snapshot.HTML)), server.WithKatexFS(web.KatexFS()))
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
	a.reloadLoopWithReview(ctx, srv, changes, watchErrs, current, nil)
}

func (a *App) reloadLoopWithReview(ctx context.Context, srv *server.Server, changes <-chan watcher.Event, watchErrs <-chan error, current document.DocumentSnapshot, review *session.Session) {
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
			if review != nil {
				if err := review.UpdateSnapshot(src, diff.Snapshot); err != nil {
					if errors.Is(err, session.ErrTerminalSessionMutation) {
						return
					}
					log.Printf("warning: could not update review snapshot: %v", err)
					continue
				}
			}
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
