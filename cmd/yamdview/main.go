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
		if cfg.Mode == cli.ModeReview {
			os.Exit(app.ReviewInternal.Code())
		}
		os.Exit(1)
	}

	application := app.New(app.Config{
		Mode:         app.Mode(cfg.Mode),
		MarkdownPath: cfg.MarkdownPath,
		Addr:         cfg.Addr,
		NoOpen:       cfg.NoOpen,
		API:          cfg.API,
		UnsafeBind:   cfg.UnsafeBind,
		Debounce:     cfg.Debounce,
		Export:       cfg.Export,
		ExportView:   cfg.ExportView,
		WriteFixes:   cfg.WriteFixes,
		BackupDir:    cfg.BackupDir,
		LLM:          cfg.LLM,
		Review: app.ReviewConfig{
			Title: cfg.Review.Title, Prompt: cfg.Review.Prompt, Choices: cfg.Review.Choices,
			Format: cfg.Review.Format, Output: cfg.Review.Output, Timeout: cfg.Review.Timeout, Watch: cfg.Review.Watch,
		},
		Input: os.Stdin, Output: os.Stdout,
	}, assets)

	if cfg.Mode == cli.ModeReview {
		status, err := application.RunReview()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(app.ReviewInternal.Code())
		}
		os.Exit(status.Code())
	}

	if cfg.Mode == cli.ModeServe {
		if err := application.RunServeAPI(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := application.RunViewer(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
