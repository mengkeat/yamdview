package cli

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
)

var ErrUsage = errors.New("usage: yamdview [flags] file.md")

type Config struct {
	MarkdownPath string
	Addr         string // HTTP bind address (host:port)
	NoOpen       bool   // suppress automatic browser opening
}

func Parse(args []string) (Config, error) {
	flags := flag.NewFlagSet("yamdview", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	addr := flags.String("addr", "127.0.0.1:0", "HTTP bind address")
	noOpen := flags.Bool("no-open", false, "do not open system browser automatically")

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

	return Config{
		MarkdownPath: path,
		Addr:         *addr,
		NoOpen:       *noOpen,
	}, nil
}
