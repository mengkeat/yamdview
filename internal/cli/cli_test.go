package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRejectsMissingFileArgument(t *testing.T) {
	_, err := Parse(nil)
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("expected ErrUsage, got %v", err)
	}
}

func TestParseRejectsTooManyArguments(t *testing.T) {
	_, err := Parse([]string{"one.md", "two.md"})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("expected ErrUsage, got %v", err)
	}
}

func TestParseRejectsNonexistentFile(t *testing.T) {
	_, err := Parse([]string{filepath.Join(t.TempDir(), "missing.md")})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing file error, got %v", err)
	}
}

func TestParseRejectsDirectory(t *testing.T) {
	_, err := Parse([]string{t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestParseAcceptsExistingMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{path})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cfg.MarkdownPath != path {
		t.Fatalf("expected path %q, got %q", path, cfg.MarkdownPath)
	}
}

func TestParseDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:0" {
		t.Errorf("expected default addr, got %q", cfg.Addr)
	}
	if cfg.NoOpen {
		t.Error("expected NoOpen to be false by default")
	}
}

func TestParseNoOpenFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--no-open", path})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NoOpen {
		t.Error("expected NoOpen to be true")
	}
}

func TestParseAddrFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--addr", "0.0.0.0:8080", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:8080" {
		t.Errorf("expected addr %q, got %q", "0.0.0.0:8080", cfg.Addr)
	}
}
