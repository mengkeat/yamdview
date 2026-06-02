package cli

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/mengkeat/yamdview/internal/fixer"
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
	WriteFixes   fixer.WriteMode
	BackupDir    string // directory for backup files when WriteFixes is backup
}

func Parse(args []string) (Config, error) {
	flags := flag.NewFlagSet("yamdview", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	addr := flags.String("addr", "127.0.0.1:0", "HTTP bind address")
	noOpen := flags.Bool("no-open", false, "do not open system browser automatically")
	debounce := flags.Duration("debounce", DefaultDebounce, "file watcher debounce duration")
	export := flags.String("export", "", "export standalone HTML to this file path")
	exportView := flags.String("export-view", "", "viewport target for export: phone, tablet, laptop, desktop")
	writeFixes := flags.String("write-fixes", string(fixer.WriteModeNever), "whether to persist heuristic fixes: never, backup, in-place")
	backupDir := flags.String("backup-dir", "", "directory for backup files when --write-fixes=backup (default: same directory as source)")

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

	return Config{
		MarkdownPath: path,
		Addr:         *addr,
		NoOpen:       *noOpen,
		Debounce:     *debounce,
		Export:       *export,
		ExportView:   *exportView,
		WriteFixes:   writeMode,
		BackupDir:    *backupDir,
	}, nil
}
