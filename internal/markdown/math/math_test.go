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

func TestFencedCodeBlockNotMathEscapesCode(t *testing.T) {
	md := newTestRenderer()
	src := []byte("```go\nif a < b && c > d {\n}\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `if a &lt; b &amp;&amp; c &gt; d {`) {
		t.Fatalf("non-math fenced code was not escaped: %s", got)
	}
	if strings.Contains(got, `if a < b && c > d {`) {
		t.Fatalf("non-math fenced code leaked raw HTML-sensitive characters: %s", got)
	}
}

// ── Fenced math block parser tests ───────────────────────

func TestFencedMathExactHTML(t *testing.T) {
	md := newTestRenderer()
	src := []byte("```math\n\\sum_{i=1}^{n} i\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	want := `<div class="math math-display" data-tex="\sum_{i=1}^{n} i"></div>` + "\n"
	if buf.String() != want {
		t.Errorf("fenced math HTML changed:\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestFencedMathWithExtraInfoTokens(t *testing.T) {
	md := newTestRenderer()
	src := []byte("```math line-numbers\nx^2\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="x^2"`) {
		t.Errorf("extra info tokens after math should not change rendering: %s", got)
	}
	if !strings.Contains(got, `math-display`) {
		t.Errorf("expected display math: %s", got)
	}
	if strings.Contains(got, "<code") {
		t.Errorf("fenced math should not render as code: %s", got)
	}
}

func TestFencedMathLanguageIsCaseInsensitive(t *testing.T) {
	// The document snapshot layer already classifies "```Math" as a
	// math block; the renderer now matches that classification.
	md := newTestRenderer()
	src := []byte("```Math\nx^2\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="x^2"`) {
		t.Errorf("case-insensitive math fence should render math: %s", got)
	}
	if strings.Contains(got, "language-Math") {
		t.Errorf("case-insensitive math fence should not render as code: %s", got)
	}
}

func TestFencedMathUnclosed(t *testing.T) {
	// An unterminated ```math fence renders everything to EOF as math.
	md := newTestRenderer()
	src := []byte("```math\ne^{i\\pi} + 1 = 0\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="e^{i\pi} + 1 = 0"`) {
		t.Errorf("unclosed math fence should render as math: %s", got)
	}
}

func TestFencedMathSingleLineIsNotMathFence(t *testing.T) {
	// goldmark refuses backtick fences whose info string contains a
	// backtick, so "```math x^2 ```" stays a paragraph + code span.
	md := newTestRenderer()
	src := []byte("```math x^2 ```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	want := "<p><code>math x^2 </code></p>\n"
	if buf.String() != want {
		t.Errorf("single-line ```math should not be a math fence:\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestTildeMathFence(t *testing.T) {
	md := newTestRenderer()
	src := []byte("~~~math\nx^2\n~~~\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="x^2"`) {
		t.Errorf("tilde math fence should render math: %s", got)
	}
	if !strings.Contains(got, `math-display`) {
		t.Errorf("tilde math fence should be display math: %s", got)
	}
}

func TestFencedTextNotMath(t *testing.T) {
	md := newTestRenderer()
	src := []byte("```text\nhello\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	want := "<pre><code class=\"language-text\">hello\n</code></pre>\n"
	if buf.String() != want {
		t.Errorf("```text fence changed:\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestTildeJSNotMath(t *testing.T) {
	md := newTestRenderer()
	src := []byte("~~~js\nvar x = 1;\n~~~\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	want := "<pre><code class=\"language-js\">var x = 1;\n</code></pre>\n"
	if buf.String() != want {
		t.Errorf("~~~js fence changed:\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestFencedMathDollarContentPreserved(t *testing.T) {
	// extractBlockTeX strips leading/trailing "$$" only from the first and
	// last lines, matching the previous renderer's behavior.
	md := newTestRenderer()
	src := []byte("```math\n$$x$$\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="x"`) {
		t.Errorf("single-line $$ content should have delimiters stripped: %s", got)
	}
}

func TestFencedMathFenceLikeContentIsNotClose(t *testing.T) {
	// A line starting with the fence marker but followed by text is content
	// (goldmark only closes on a blank run of the marker).
	md := newTestRenderer()
	src := []byte("```math\n```more\ny\n```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, "data-tex=\"```more\ny\"") {
		t.Errorf("fence-like content line should stay inside the math block: %s", got)
	}
}

func TestFencedMathLongerFenceCloses(t *testing.T) {
	// A longer run of the fence character closes the fence, matching
	// goldmark's fenced-code semantics.
	md := newTestRenderer()
	src := []byte("```math\nx^2\n````\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `data-tex="x^2"`) {
		t.Errorf("longer fence run should close the math block: %s", got)
	}
}

func TestFencedMathIndentedCloseIsContent(t *testing.T) {
	// A closing-fence line indented 4+ columns is content, matching
	// goldmark's fenced-code semantics.
	md := newTestRenderer()
	src := []byte("```math\nx^2\n    ```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, "data-tex=\"x^2\n    ```\"") {
		t.Errorf("4-space-indented fence line should stay inside the math block: %s", got)
	}
}

func TestFencedMathInBlockquote(t *testing.T) {
	md := newTestRenderer()
	src := []byte("> ```math\n> x^2\n> y^2\n> ```\n")
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		t.Fatal(err)
	}
	want := "<blockquote>\n<div class=\"math math-display\" data-tex=\"x^2\ny^2\"></div>\n</blockquote>\n"
	if buf.String() != want {
		t.Errorf("math fence inside blockquote changed:\n got: %q\nwant: %q", buf.String(), want)
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
