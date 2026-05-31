package markdown

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenderBasicMarkdown(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "markdown", "basic.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, src)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("expected non-empty HTML output")
	}

	// Verify key structural elements are present.
	checks := []string{
		"<h1>", "Hello",
		"<strong>", "bootstrap",
		"<h2>", "Subheading",
		"<li>", "item one",
		"<table>", "<th>", "Name",
	}
	for _, want := range checks {
		if !contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRenderEmptyInput(t *testing.T) {
	md := NewRenderer()
	got, err := Render(md, nil)
	if err != nil {
		t.Fatalf("render empty: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty output for nil input, got %q", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
