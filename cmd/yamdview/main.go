package main

import (
	"fmt"
	"os"

	"github.com/mengkeat/yamdview/internal/app"
	"github.com/mengkeat/yamdview/internal/cli"
	"github.com/mengkeat/yamdview/web"
)

func main() {
	cfg, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	assets, err := web.LoadAssets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load assets: %v\n", err)
		os.Exit(1)
	}

	application := app.New(app.Config{
		MarkdownPath: cfg.MarkdownPath,
		Addr:         cfg.Addr,
		NoOpen:       cfg.NoOpen,
		Debounce:     cfg.Debounce,
		Export:       cfg.Export,
		ExportView:   cfg.ExportView,
		WriteFixes:   cfg.WriteFixes,
		BackupDir:    cfg.BackupDir,
	}, assets)

	if err := application.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
