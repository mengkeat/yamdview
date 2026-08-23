package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if cfg.Debounce != DefaultDebounce {
		t.Errorf("expected default debounce %s, got %s", DefaultDebounce, cfg.Debounce)
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

func TestParseDebounceFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--debounce", "25ms", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Debounce != 25*time.Millisecond {
		t.Errorf("expected debounce 25ms, got %s", cfg.Debounce)
	}
}

func TestParseRejectsNegativeDebounce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Parse([]string{"--debounce", "-1ms", path})
	if err == nil || !strings.Contains(err.Error(), "debounce must be non-negative") {
		t.Fatalf("expected negative debounce error, got %v", err)
	}
}

func TestParseExportFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--export", "out.html", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Export != "out.html" {
		t.Errorf("expected Export %q, got %q", "out.html", cfg.Export)
	}
}

func TestParseExportViewFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--export", "out.html", "--export-view", "tablet", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExportView != "tablet" {
		t.Errorf("expected ExportView tablet, got %q", cfg.ExportView)
	}
}

func TestParseRejectsInvalidExportView(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Parse([]string{"--export-view", "watch", path})
	if err == nil || !strings.Contains(err.Error(), "unknown --export-view") {
		t.Fatalf("expected unknown export-view error, got %v", err)
	}
}

func TestParseExportViewWithoutExportIsValid(t *testing.T) {
	// Setting --export-view without --export is allowed; it will simply be ignored.
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--export-view", "desktop", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExportView != "desktop" {
		t.Errorf("expected ExportView desktop, got %q", cfg.ExportView)
	}
}

func TestParseWriteFixesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WriteFixes != "never" {
		t.Errorf("expected default WriteFixes=never, got %q", cfg.WriteFixes)
	}
}

func TestParseWriteFixesModes(t *testing.T) {
	tests := []struct {
		flag string
		want string
	}{
		{"never", "never"},
		{"backup", "backup"},
		{"in-place", "in-place"},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "doc.md")
			if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Parse([]string{"--write-fixes", tt.flag, path})
			if err != nil {
				t.Fatal(err)
			}
			if string(cfg.WriteFixes) != tt.want {
				t.Errorf("got %q, want %q", cfg.WriteFixes, tt.want)
			}
		})
	}
}

func TestParseRejectsInvalidWriteFixes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Parse([]string{"--write-fixes", "always", path})
	if err == nil || !strings.Contains(err.Error(), "--write-fixes") {
		t.Fatalf("expected --write-fixes error, got %v", err)
	}
}

func TestParseBackupDirFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	backup := t.TempDir()
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{"--write-fixes", "backup", "--backup-dir", backup, path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackupDir != backup {
		t.Errorf("expected BackupDir %q, got %q", backup, cfg.BackupDir)
	}
}

func TestParseRejectsNonexistentBackupDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Parse([]string{"--write-fixes", "backup", "--backup-dir", filepath.Join(dir, "missing"), path})
	if err == nil || !strings.Contains(err.Error(), "backup directory") {
		t.Fatalf("expected backup directory error, got %v", err)
	}
}

func TestParseRejectsFileAsBackupDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Parse([]string{"--write-fixes", "backup", "--backup-dir", file, path})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected not-a-directory error, got %v", err)
	}
}

func TestParseLegacyInvocationSelectsViewMode(t *testing.T) {
	cfg, err := Parse([]string{writeTempMD(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeView {
		t.Fatalf("expected view mode, got %q", cfg.Mode)
	}
}

func TestParseExplicitViewSelectsViewMode(t *testing.T) {
	cfg, err := Parse([]string{"view", "--no-open", writeTempMD(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeView || !cfg.NoOpen {
		t.Fatalf("expected view mode with NoOpen, got mode=%q noOpen=%v", cfg.Mode, cfg.NoOpen)
	}
}

func TestParseReviewFlags(t *testing.T) {
	cfg, err := Parse([]string{
		"review",
		"--title", "Plan review",
		"--prompt", "Does this look right?",
		"--choices", "Approve, Request changes,Reject",
		"--format", "markdown",
		"--output", "feedback.md",
		"--timeout", "15m",
		"--watch",
		writeTempMD(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeReview {
		t.Errorf("expected review mode, got %q", cfg.Mode)
	}
	if cfg.Review.Title != "Plan review" || cfg.Review.Prompt != "Does this look right?" {
		t.Errorf("unexpected title/prompt: %+v", cfg.Review)
	}
	wantChoices := []string{"Approve", "Request changes", "Reject"}
	if strings.Join(cfg.Review.Choices, "|") != strings.Join(wantChoices, "|") {
		t.Errorf("choices = %v, want %v", cfg.Review.Choices, wantChoices)
	}
	if cfg.Review.Format != "markdown" || cfg.Review.Output != "feedback.md" {
		t.Errorf("unexpected format/output: %q/%q", cfg.Review.Format, cfg.Review.Output)
	}
	if cfg.Review.Timeout != 15*time.Minute || !cfg.Review.Watch {
		t.Errorf("unexpected timeout/watch: %s/%v", cfg.Review.Timeout, cfg.Review.Watch)
	}
}

func TestParseReviewDefaults(t *testing.T) {
	cfg, err := Parse([]string{"review", writeTempMD(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Review.Format != "json" {
		t.Errorf("expected default JSON format, got %q", cfg.Review.Format)
	}
	if cfg.Review.Timeout != 0 || cfg.Review.Watch || cfg.Review.Output != "" {
		t.Errorf("unexpected review defaults: %+v", cfg.Review)
	}
}

func TestParseReviewAcceptsStdin(t *testing.T) {
	cfg, err := Parse([]string{"review", "-"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeReview || cfg.MarkdownPath != "-" {
		t.Fatalf("unexpected stdin config: %+v", cfg)
	}
}

func TestParseViewerStillValidatesStdinPath(t *testing.T) {
	_, err := Parse([]string{"-"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected viewer path validation error, got %v", err)
	}
}

func TestParseReviewRejectsInvalidFormat(t *testing.T) {
	_, err := Parse([]string{"review", "--format", "yaml", writeTempMD(t)})
	if err == nil || !strings.Contains(err.Error(), "--format") {
		t.Fatalf("expected format error, got %v", err)
	}
}

func TestParseReviewRejectsNegativeTimeout(t *testing.T) {
	_, err := Parse([]string{"review", "--timeout", "-1s", writeTempMD(t)})
	if err == nil || !strings.Contains(err.Error(), "--timeout must be non-negative") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestParseRejectsUnknownSubcommand(t *testing.T) {
	_, err := Parse([]string{"unknown", writeTempMD(t)})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("expected ErrUsage for unknown subcommand, got %v", err)
	}
}
