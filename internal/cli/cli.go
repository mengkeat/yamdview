package cli

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/watcher"
)

var ErrUsage = errors.New("usage: yamdview [flags] file.md")

const DefaultDebounce = watcher.DefaultDebounce

type Config struct {
	MarkdownPath string
	Addr         string // HTTP bind address (host:port)
	NoOpen       bool   // suppress automatic browser opening
	Debounce     time.Duration
	Export       string // export standalone HTML to this path (empty = serve)
	ExportView   string // viewport target for export: phone, tablet, laptop, desktop
}

func Parse(args []string) (Config, error) {
	flags := flag.NewFlagSet("yamdview", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	addr := flags.String("addr", "127.0.0.1:0", "HTTP bind address")
	noOpen := flags.Bool("no-open", false, "do not open system browser automatically")
	debounce := flags.Duration("debounce", DefaultDebounce, "file watcher debounce duration")
	export := flags.String("export", "", "export standalone HTML to this file path")
	exportView := flags.String("export-view", "", "viewport target for export: phone, tablet, laptop, desktop")

	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}

	remaining := flags.Args()
	if len(remaining) != 1 {
		return Config{}, ErrUsage
	}

	path := remaining[0]
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("markdown file does not exist: %s", path)
		}
		return Config{}, fmt.Errorf("cannot access markdown file %s: %w", path, err)
	}
	if info.IsDir() {
		return Config{}, fmt.Errorf("markdown path is a directory: %s", path)
	}
	if *debounce < 0 {
		return Config{}, fmt.Errorf("debounce must be non-negative: %s", debounce.String())
	}

	if *exportView != "" && !server.ValidExportView(*exportView) {
		return Config{}, fmt.Errorf(
			"unknown --export-view %q; valid values: phone, tablet, laptop, desktop",
			*exportView,
		)
	}

	return Config{
		MarkdownPath: path,
		Addr:         *addr,
		NoOpen:       *noOpen,
		Debounce:     *debounce,
		Export:       *export,
		ExportView:   *exportView,
	}, nil
}
