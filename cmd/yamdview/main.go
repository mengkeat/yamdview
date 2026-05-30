package main

import (
	"fmt"
	"os"

	"github.com/mengkeat/yamdview/internal/cli"
)

func main() {
	cfg, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	fmt.Printf("yamdview bootstrap: %s\n", cfg.MarkdownPath)
}
