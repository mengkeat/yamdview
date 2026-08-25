package cli

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/fixer"
	"github.com/mengkeat/yamdview/internal/llm"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/watcher"
)

var ErrUsage = errors.New("usage: yamdview [flags] file.md")

// Mode selects the application flow requested by the command line.
type Mode string

const (
	ModeView   Mode = "view"
	ModeReview Mode = "review"
	ModeServe  Mode = "serve"
	ModeMCP    Mode = "mcp"

	// ModeViewer, ReviewMode, ServeMode, and MCPMode are descriptive aliases
	// for the canonical mode names.
	ModeViewer = ModeView
	ReviewMode = ModeReview
	ServeMode  = ModeServe
	MCPMode    = ModeMCP
)

// ReviewConfig contains options specific to a review session.
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

const DefaultDebounce = watcher.DefaultDebounce

type Config struct {
	Mode         Mode
	MarkdownPath string
	Addr         string // HTTP bind address (host:port)
	NoOpen       bool   // suppress automatic browser opening
	API          bool   // serve mode: run the long-running agent HTTP API
	UnsafeBind   bool   // serve/mcp mode: allow non-loopback listen addresses
	Debounce     time.Duration
	Export       string // export standalone HTML to this path (empty = serve)
	ExportView   string // viewport target for export: phone, tablet, laptop, desktop
	WriteFixes   fixer.WriteMode
	BackupDir    string // directory for backup files when WriteFixes is backup
	LLM          llm.Settings
	Review       ReviewConfig
}

func Parse(args []string) (Config, error) {
	mode := ModeView
	if len(args) > 0 {
		switch args[0] {
		case string(ModeView):
			mode = ModeView
			args = args[1:]
		case string(ModeReview):
			mode = ModeReview
			args = args[1:]
		case string(ModeServe):
			mode = ModeServe
			args = args[1:]
		case string(ModeMCP):
			mode = ModeMCP
			args = args[1:]
		}
	}
	if len(args) == 1 && looksLikeUnknownSubcommand(args[0]) {
		return Config{}, ErrUsage
	}

	flags := flag.NewFlagSet("yamdview", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	addr := flags.String("addr", "127.0.0.1:0", "HTTP bind address")
	noOpen := flags.Bool("no-open", false, "do not open system browser automatically")
	debounce := flags.Duration("debounce", DefaultDebounce, "file watcher debounce duration")
	export := flags.String("export", "", "export standalone HTML to this file path")
	exportView := flags.String("export-view", "", "viewport target for export: phone, tablet, laptop, desktop")
	writeFixes := flags.String("write-fixes", string(fixer.WriteModeNever), "whether to persist heuristic fixes: never, backup, in-place")
	backupDir := flags.String("backup-dir", "", "directory for backup files when --write-fixes=backup (default: same directory as source)")
	llmMode := flags.String("llm", string(llm.ModeOff), "LLM repair mode for math/tables that heuristics cannot fix: off, ask, auto")
	llmProvider := flags.String("llm-provider", "", "named LLM provider from the config file")
	llmLocal := flags.String("llm-local", "", "local LLM profile shortcut: ollama, lm-studio, llama.cpp, command")
	llmLocalURL := flags.String("llm-local-url", "", "override base URL for the local OpenAI-compatible profile (required for llama.cpp)")
	llmModel := flags.String("llm-model", "", "override the model name for the configured or local LLM provider")
	llmConfig := flags.String("llm-config", "", "path to an LLM provider JSON config file")
	llmTimeout := flags.Duration("llm-timeout", 0, "per-call timeout for LLM repair (0 uses the provider default)")

	var title, prompt, choices, format, output string
	var reviewTimeout time.Duration
	var watch bool
	var respondLLM, respondProvider, respondModel string
	if mode == ModeReview {
		flags.StringVar(&title, "title", "", "session title shown in the review viewer")
		flags.StringVar(&prompt, "prompt", "", "question or request shown above the document")
		flags.StringVar(&choices, "choices", "", "comma-separated quick verdict choices")
		flags.StringVar(&format, "format", string(feedback.FormatJSON), "feedback output format: json or markdown")
		flags.StringVar(&output, "output", "", "feedback output path (or - for stdout)")
		flags.DurationVar(&reviewTimeout, "timeout", 0, "automatically end the review after this duration")
		flags.BoolVar(&watch, "watch", false, "watch the review source for changes")
		flags.StringVar(&respondLLM, "respond-llm", string(llm.ModeOff), "feedback reformulation via a configured LLM: off, ask, auto")
		flags.StringVar(&respondProvider, "respond-provider", "", "named LLM provider (from --llm-config) used to reformulate feedback")
		flags.StringVar(&respondModel, "respond-model", "", "override the model used for feedback reformulation")
	}
	var serveAPI, unsafeBind bool
	if mode == ModeServe || mode == ModeMCP {
		if mode == ModeServe {
			flags.BoolVar(&serveAPI, "api", false, "run the long-running agent HTTP API (sessions are created dynamically)")
		}
		// serve --api and mcp share the rule: authenticated surfaces stay
		// loopback unless explicitly overridden.
		flags.BoolVar(&unsafeBind, "unsafe-bind", false, "allow binding to non-loopback addresses")
	}

	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}

	remaining := flags.Args()
	if mode == ModeServe || mode == ModeMCP {
		if len(remaining) != 0 {
			return Config{}, ErrUsage
		}
		if err := ValidateLoopbackBind(*addr, unsafeBind); err != nil {
			return Config{}, err
		}
		if mode == ModeServe {
			if !serveAPI {
				return Config{}, errors.New("serve requires --api (the long-running agent HTTP API is currently the only serve variant)")
			}
			return Config{
				Mode:       mode,
				Addr:       *addr,
				API:        serveAPI,
				UnsafeBind: unsafeBind,
				LLM:        llm.Settings{Mode: llm.ModeOff}, // serve mode has no document repair
			}, nil
		}
		return Config{
			Mode:       ModeMCP,
			Addr:       *addr,
			UnsafeBind: unsafeBind,
			LLM:        llm.Settings{Mode: llm.ModeOff}, // mcp mode has no document repair
		}, nil
	}
	if len(remaining) != 1 {
		return Config{}, ErrUsage
	}

	path := remaining[0]
	var info fs.FileInfo
	var err error
	if !(mode == ModeReview && path == "-") {
		info, err = os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return Config{}, fmt.Errorf("markdown file does not exist: %s", path)
			}
			return Config{}, fmt.Errorf("cannot access markdown file %s: %w", path, err)
		}
		if info.IsDir() {
			return Config{}, fmt.Errorf("markdown path is a directory: %s", path)
		}
	}
	if *debounce < 0 {
		return Config{}, fmt.Errorf("debounce must be non-negative: %s", debounce.String())
	}

	var reviewFormat feedback.Format
	var reviewChoices []string
	if mode == ModeReview {
		reviewFormat, err = feedback.ParseFormat(format)
		if err != nil {
			return Config{}, fmt.Errorf("--format: %w", err)
		}
		if reviewTimeout < 0 {
			return Config{}, fmt.Errorf("--timeout must be non-negative: %s", reviewTimeout)
		}
		reviewChoices = splitChoices(choices)
	}

	if *exportView != "" && !server.ValidExportView(*exportView) {
		return Config{}, fmt.Errorf(
			"unknown --export-view %q; valid values: %s",
			*exportView,
			server.ExportViewNames,
		)
	}

	writeMode, err := fixer.ParseWriteMode(*writeFixes)
	if err != nil {
		return Config{}, fmt.Errorf("--write-fixes: %w", err)
	}
	if writeMode == fixer.WriteModeBackup && *backupDir != "" {
		if info, err := os.Stat(*backupDir); err != nil {
			return Config{}, fmt.Errorf("backup directory %s: %w", *backupDir, err)
		} else if !info.IsDir() {
			return Config{}, fmt.Errorf("backup path is not a directory: %s", *backupDir)
		}
	}
	llmModeValue, err := llm.ParseMode(*llmMode)
	if err != nil {
		return Config{}, fmt.Errorf("--llm: %w", err)
	}
	var profile llm.LocalProfile
	if *llmLocal != "" {
		profile, err = llm.ParseLocalProfile(*llmLocal)
		if err != nil {
			return Config{}, fmt.Errorf("--llm-local: %w", err)
		}
	}
	if *llmConfig != "" {
		if info, err := os.Stat(*llmConfig); err != nil {
			return Config{}, fmt.Errorf("llm config %s: %w", *llmConfig, err)
		} else if info.IsDir() {
			return Config{}, fmt.Errorf("llm config is a directory: %s", *llmConfig)
		}
	}
	settings := llm.Settings{
		Mode:         llmModeValue,
		ProviderName: *llmProvider,
		LocalProfile: profile,
		LocalURL:     *llmLocalURL,
		Model:        *llmModel,
		ConfigPath:   *llmConfig,
		Timeout:      *llmTimeout,
	}
	if llmModeValue != llm.ModeOff && !settings.HasProviderSelection() {
		return Config{}, fmt.Errorf("--llm %q requires --llm-local or --llm-provider", llmModeValue)
	}

	respondMode, err := llm.ParseRespondMode(respondLLM)
	if err != nil {
		return Config{}, fmt.Errorf("--respond-llm: %w", err)
	}
	respond := llm.RespondSettings{
		Mode:         respondMode,
		ProviderName: respondProvider,
		Model:        respondModel,
		ConfigPath:   *llmConfig,
	}
	if respondMode != llm.ModeOff {
		// Reformulation always calls a named hosted/config-file provider;
		// local profile shortcuts are intentionally not supported here.
		if respond.ProviderName == "" {
			return Config{}, fmt.Errorf("--respond-llm %q requires --respond-provider naming a provider in the config file", respondMode)
		}
		if respond.ConfigPath == "" {
			return Config{}, fmt.Errorf("--respond-llm %q requires --llm-config pointing at the provider config file", respondMode)
		}
	}

	return Config{
		Mode:         mode,
		MarkdownPath: path,
		Addr:         *addr,
		NoOpen:       *noOpen,
		Debounce:     *debounce,
		Export:       *export,
		ExportView:   *exportView,
		WriteFixes:   writeMode,
		BackupDir:    *backupDir,
		LLM:          settings,
		Review: ReviewConfig{
			Title:   title,
			Prompt:  prompt,
			Choices: reviewChoices,
			Format:  reviewFormat,
			Output:  output,
			Timeout: reviewTimeout,
			Watch:   watch,
			Respond: respond,
		},
	}, nil
}

// ValidateLoopbackBind refuses listen addresses that would expose an
// authenticated API/MCP surface beyond the local machine. Allowed without
// --unsafe-bind: "localhost" and literal loopback IPs (127.0.0.0/8, ::1).
// Everything else is refused, including the empty-host form ":8080" (which
// binds all interfaces), wildcard addresses, and non-loopback hostnames or
// IPs. When unsafe is true any address is accepted. Both the serve and mcp
// modes enforce this rule at parse time.
func ValidateLoopbackBind(addr string, unsafe bool) error {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if unsafe {
		return nil
	}
	switch {
	case host == "":
		return fmt.Errorf("--addr %q binds all interfaces; use a loopback address such as 127.0.0.1 or [::1], or pass --unsafe-bind", addr)
	case strings.EqualFold(host, "localhost"):
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("--addr %q is not a loopback address; pass --unsafe-bind to allow non-loopback binds", addr)
	}
	return fmt.Errorf("--addr %q is a hostname; use \"localhost\", a loopback IP, or pass --unsafe-bind", addr)
}

func splitChoices(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	choices := make([]string, 0, len(parts))
	for _, part := range parts {
		if choice := strings.TrimSpace(part); choice != "" {
			choices = append(choices, choice)
		}
	}
	return choices
}

// looksLikeUnknownSubcommand resolves the otherwise ambiguous one-token
// invocation without breaking the legacy bare-file form. Existing files and
// path-like names remain file arguments; a missing, extensionless word is
// treated as a misspelled subcommand.
func looksLikeUnknownSubcommand(value string) bool {
	if value == "-" || strings.HasPrefix(value, "-") || filepath.Ext(value) != "" || strings.ContainsAny(value, `/\\`) {
		return false
	}
	_, err := os.Stat(value)
	return errors.Is(err, fs.ErrNotExist)
}
