package math_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/mengkeat/yamdview/internal/markdown/math"
)

// newTestRenderer creates a goldmark instance with the math extension for testing.
func newTestRenderer() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.NewTable(),
			extension.Strikethrough,
			extension.TaskList,
			extension.Linkify,
			math.Math,
		),
	)
}

func TestInlineDollarMath(t *testing.T) {
	md := newTestRenderer()
	src := []byte(`Inline: $x^2 + y^2$.` + "\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="x^2 + y^2"`) {
		t.Errorf("missing data-tex attribute in: %s", got)
	}
	if !strings.Contains(got, `math-inline`) {
		t.Errorf("missing math-inline class in: %s", got)
	}
	if strings.Contains(got, "$x^2") {
		t.Errorf("raw $ delimiters should not appear in output: %s", got)
	}
}

func TestInlineBackslashParenMath(t *testing.T) {
	md := newTestRenderer()
	src := []byte(`Inline: \( \alpha + \beta \).` + "\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="\alpha + \beta"`) {
		t.Errorf("missing data-tex attribute in: %s", got)
	}
	if !strings.Contains(got, `math-inline`) {
		t.Errorf("missing math-inline class in: %s", got)
	}
}

func TestDisplayDollarMath(t *testing.T) {
	md := newTestRenderer()
	src := []byte("$$\nE = mc^2\n$$\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="E = mc^2"`) {
		t.Errorf("missing data-tex attribute in: %s", got)
	}
	if !strings.Contains(got, `math-display`) {
		t.Errorf("missing math-display class in: %s", got)
	}
	if !strings.Contains(got, `<div`) {
		t.Errorf("display math should be a div: %s", got)
	}
}

func TestDisplayBackslashBracketMath(t *testing.T) {
	md := newTestRenderer()
	src := []byte("\\[\n\\int_0^1 x^2 dx\n\\]\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="\int_0^1 x^2 dx"`) {
		t.Errorf("missing data-tex attribute in: %s", got)
	}
	if !strings.Contains(got, `math-display`) {
		t.Errorf("missing math-display class in: %s", got)
	}
}

func TestFencedMathBlock(t *testing.T) {
	md := newTestRenderer()
	src := []byte("```math\n\\sum_{i=1}^{n} i\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="\sum_{i=1}^{n} i"`) {
		t.Errorf("missing data-tex attribute in: %s", got)
	}
	if !strings.Contains(got, `math-display`) {
		t.Errorf("missing math-display class in: %s", got)
	}
	// Should NOT be rendered as a code block.
	if strings.Contains(got, "<code>") {
		t.Errorf("fenced math should not render as code block: %s", got)
	}
}

func TestFencedCodeBlockNotMath(t *testing.T) {
	md := newTestRenderer()
	src := []byte("```go\nfmt.Println()\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if strings.Contains(got, `data-tex`) {
		t.Errorf("non-math fenced code should not have data-tex: %s", got)
	}
	if !strings.Contains(got, `<code`) {
		t.Errorf("non-math fenced code should render as code: %s", got)
	}
}

func TestNonMathUnaffected(t *testing.T) {
	md := newTestRenderer()
	src := []byte("Hello world. This has no math.\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if strings.Contains(got, `data-tex`) {
		t.Errorf("plain text should not have data-tex: %s", got)
	}
}

func TestMixedMathRendering(t *testing.T) {
	md := newTestRenderer()
	src := []byte("The equation $E = mc^2$ is famous.\n\n$$\nx^2\n$$\n\nAnd \\( \\pi \\) too.\n\n```math\na/b\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	// Should have 4 math elements.
	count := strings.Count(got, `data-tex=`)
	if count != 4 {
		t.Errorf("expected 4 math elements, got %d in: %s", count, got)
	}
	// 2 inline, 2 display.
	inlineCount := strings.Count(got, `math-inline`)
	displayCount := strings.Count(got, `math-display`)
	if inlineCount != 2 {
		t.Errorf("expected 2 inline math, got %d", inlineCount)
	}
	if displayCount != 2 {
		t.Errorf("expected 2 display math, got %d", displayCount)
	}
}

func TestSingleLineDisplayDollar(t *testing.T) {
	md := newTestRenderer()
	src := []byte("$$E = mc^2$$\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="E = mc^2"`) {
		t.Errorf("single-line $$ should render math: %s", got)
	}
	if !strings.Contains(got, `math-display`) {
		t.Errorf("single-line $$ should be display math: %s", got)
	}
}

func TestGoldenFixtures(t *testing.T) {
	fixtures := []struct {
		name     string
		contains []string
		omits    []string
	}{
		{
			name:     "inline-dollar.md",
			contains: []string{`data-tex="x^2 + y^2 = z^2"`, `math-inline`},
			omits:    []string{`$x^2`},
		},
		{
			name:     "inline-backslash.md",
			contains: []string{`data-tex="\alpha + \beta"`, `math-inline`},
			omits:    []string{`\( \alpha`},
		},
		{
			name:     "display-dollar.md",
			contains: []string{`data-tex="E = mc^2"`, `math-display`},
			omits:    []string{},
		},
		{
			name:     "display-backslash.md",
			contains: []string{`data-tex="\int_0^1 x^2 dx"`, `math-display`},
			omits:    []string{},
		},
		{
			name:     "fenced-math.md",
			contains: []string{`data-tex="\sum_{i=1}^{n} i"`, `math-display`},
			omits:    []string{`<code`},
		},
		{
			name:     "mixed.md",
			contains: []string{`data-tex="E = mc^2"`, `math-display`, `math-inline`, `data-tex="\pi \approx 3.14"`},
			omits:    []string{},
		},
	}

	md := newTestRenderer()

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "math", tc.name))
			if err != nil {
				t.Skipf("fixture not found: %v", err)
			}

			var buf strings.Builder
			if err := md.Convert(data, &buf); err != nil {
				t.Fatal(err)
			}
			got := buf.String()

			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("expected output to contain %q\nGot: %s", want, got)
				}
			}
			for _, notWant := range tc.omits {
				if strings.Contains(got, notWant) {
					t.Errorf("expected output to NOT contain %q\nGot: %s", notWant, got)
				}
			}
		})
	}
}
