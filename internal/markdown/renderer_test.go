package markdown

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRenderUnicodeMathConverted(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		omits    []string
	}{
		{
			name:     "unicode operators to KaTeX",
			input:    "∀x ∈ ℝ, x² ≥ 0\n",
			contains: []string{`data-tex="`, `\forall`, `\mathbb{R}`},
			omits:    []string{"∀", "∈", "ℝ"},
		},
		{
			name:     "unicode Greek to KaTeX",
			input:    "αᵢ = βᵢ + γᵢ\n",
			contains: []string{`data-tex="`, `\alpha`, `\beta`},
			omits:    []string{"αᵢ", "βᵢ"},
		},
		{
			name:     "unicode integral to KaTeX",
			input:    "∫₀¹ x² dx\n",
			contains: []string{`data-tex="`, `\int`},
			omits:    []string{"∫₀¹"},
		},
		{
			name:     "code fence untouched",
			input:    "```python\n# ∀x ∈ ℝ\nprint('hello')\n```\n",
			contains: []string{"∀x ∈ ℝ"},
			omits:    []string{`\forall`},
		},
		{
			name:     "existing TeX untouched",
			input:    "The value $x^2$ is known.\n",
			contains: []string{`data-tex="x^2"`, "math-inline"},
		},
		{
			name:     "plain text unchanged",
			input:    "Hello world\n",
			contains: []string{"<p>", "Hello world"},
			omits:    []string{"data-tex"},
		},
	}

	md := NewRenderer()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(md, []byte(tc.input))
			if err != nil {
				t.Fatalf("render: %v", err)
			}

			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\nGot: %s", want, got)
				}
			}
			for _, notWant := range tc.omits {
				if strings.Contains(got, notWant) {
					t.Errorf("output should not contain %q\nGot: %s", notWant, got)
				}
			}
		})
	}
}

func TestRenderUnicodeMathFraction(t *testing.T) {
	md := NewRenderer()
	got, err := Render(md, []byte("The result is ½ of the total.\n"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, `\frac{1}{2}`) {
		t.Errorf("expected fraction TeX in output, got: %s", got)
	}
}

func TestRenderUnicodeMathSquareRoot(t *testing.T) {
	md := NewRenderer()
	got, err := Render(md, []byte("The magnitude is √(x²+y²)\n"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, `\sqrt{`) {
		t.Errorf("expected sqrt TeX in output, got: %s", got)
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
