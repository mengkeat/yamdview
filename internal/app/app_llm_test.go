package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/llm"
)

// injectMockProvider replaces the App's provider with a mock, used by tests in
// this package to exercise the repair path without a real endpoint.
func injectMockProvider(t *testing.T, application *App) *llm.Mock {
	t.Helper()
	mock := llm.NewMock("mock")
	application.provider = mock
	application.llmMode = llm.ModeAuto
	return mock
}

func TestNewLLMOffByDefaultHasNilProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	application := New(Config{MarkdownPath: path}, testAssets)
	if application.provider != nil {
		t.Errorf("expected nil provider in off mode, got %T", application.provider)
	}
}

func TestExportLLMAutoRepairsAmbiguousTable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")
	if err := os.WriteFile(src, []byte("a | b | c | d\ne | f\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{MarkdownPath: src, Export: dst, LLM: llm.Settings{Mode: llm.ModeAuto}}, testAssets)
	mock := injectMockProvider(t, application)
	mock.Queue(llm.MockText(`{"replacement_markdown":"| h1 | h2 |\n| --- | --- |\n| a | b |\n","confidence":0.9}`))

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<table>") {
		t.Errorf("expected exported file to contain a repaired table, got:\n%s", data)
	}
}

func TestExportLLMAutoLeavesUnchangedOnRejection(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")
	original := "a | b | c | d\ne | f\n"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{MarkdownPath: src, Export: dst, LLM: llm.Settings{Mode: llm.ModeAuto}}, testAssets)
	mock := injectMockProvider(t, application)
	// Link injection is rejected by semantic validation.
	mock.Queue(llm.MockText(`{"replacement_markdown":"| h |\n| --- |\n| a | see [x](http://y) |\n","confidence":0.9}`))

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(dst)
	if strings.Contains(string(data), "<table>") {
		t.Errorf("rejected repair should not produce a table, got:\n%s", data)
	}
	// The source file must be unchanged (LLM is render-only).
	srcData, _ := os.ReadFile(src)
	if string(srcData) != original {
		t.Errorf("LLM repair must not modify the source file: got %q", srcData)
	}
}

func TestExportLLMAskDoesNotAutoRepair(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")
	if err := os.WriteFile(src, []byte("a | b | c | d\ne | f\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{MarkdownPath: src, Export: dst, LLM: llm.Settings{Mode: llm.ModeAsk}}, testAssets)
	mock := llm.NewMock("mock")
	application.provider = mock
	// llmMode stays ask, so repairSnapshot is a no-op.

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	if len(mock.Calls()) != 0 {
		t.Errorf("ask mode must not auto-call the provider, got %d calls", len(mock.Calls()))
	}
	data, _ := os.ReadFile(dst)
	if strings.Contains(string(data), "<table>") {
		t.Errorf("ask mode must not auto-repair, got:\n%s", data)
	}
}
