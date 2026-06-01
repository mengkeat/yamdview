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

func TestRenderRepairsMalformedPipeTable(t *testing.T) {
	md := NewRenderer()
	got, err := Render(md, []byte("Name | Score\nAlice | 10\nBob | 9\n"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	checks := []string{"<table>", "<th>Name</th>", "<td>10</td>", "<td>9</td>"}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered table missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Name | Score") {
		t.Fatalf("malformed table rendered as paragraph:\n%s", got)
	}
}

func TestRenderTableRepairPreservesEscapedPipesAndCode(t *testing.T) {
	md := NewRenderer()
	input := "Pattern | Meaning\n`a | b` | inline code\nx \\| y | escaped pipe\n"
	got, err := Render(md, []byte(input))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	checks := []string{"<table>", "<code>a | b</code>", "x | y"}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered table missing %q:\n%s", want, got)
		}
	}
}

func TestRenderAmbiguousTableStaysParagraph(t *testing.T) {
	md := NewRenderer()
	input := "Name | Score | Note\nAlice | 10\nBob | 9 | ok | extra\n"
	got, err := Render(md, []byte(input))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if strings.Contains(got, "<table>") {
		t.Fatalf("ambiguous table should not render as a table:\n%s", got)
	}
	if !strings.Contains(got, "Name | Score | Note") {
		t.Fatalf("ambiguous table source not preserved:\n%s", got)
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

func TestRenderTextFenceEquation(t *testing.T) {
	md := NewRenderer()
	input := "```text\ndv/dt = g − kD |v| v + (kM·|ω|)(axis × v) + f_NN(v)\n```\n"
	got, err := Render(md, []byte(input))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	contains := []string{`math-display`, `\frac{dv}{dt}`, `\lvert v\rvert`, `f_{\mathrm{NN}}`}
	for _, want := range contains {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q\nGot: %s", want, got)
		}
	}
	if strings.Contains(got, "<pre><code") {
		t.Errorf("expected equation text fence to render as math, got code block:\n%s", got)
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

func TestRenderUnicodeMathFixtures(t *testing.T) {
	fixtures := []struct {
		name     string
		contains []string
		omits    []string
	}{
		{
			name: "unicode-operators.md",
			contains: []string{
				`\forall`,
				`\in`,
				`\mathbb{R}`,
				`^{2}`,
				`\ge`,
				`\alpha`,
				`\int`,
				`\sum`,
			},
			omits: []string{"∀", "∈", "ℝ", "∫₀¹", "αᵢ"},
		},
		{
			name: "unicode-greek.md",
			contains: []string{
				`\pi`,
				`\theta`,
			},
		},
		{
			name: "unicode-superscripts.md",
			contains: []string{
				`^{2}`,
				`^{3}`,
				`_{0}`,
				`_{1}`,
			},
			omits: []string{"mc²", "x₀"},
		},
		{
			name: "unicode-mixed.md",
			contains: []string{
				`\forall`,
				`\int`,
				`\frac{1}{3}`,
				`\sqrt{`,
				`\cup`,
				`\cap`,
			},
			omits: []string{"∀x", "∫₀¹"},
		},
		{
			name: "unicode-falsepositive.md",
			contains: []string{
				"café",
				"résumé",
				"$42.99",
				"quick brown fox",
				"∀",
			},
			omits: []string{
				`\forall`, // the ∀ in code fence should NOT be converted
			},
		},
	}

	md := NewRenderer()

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", tc.name))
			if err != nil {
				t.Fatalf("fixture not found: %v", err)
			}

			got, err := Render(md, data)
			if err != nil {
				t.Fatalf("render: %v", err)
			}

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

func TestRenderUnicodeMathEquations(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", "unicode-equations.md"))
	if err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Spot-check key conversions from famous equations.
	checks := []struct {
		desc     string
		contains string
	}{
		{"Euler pi", `\pi`},
		{"Euler superscript i", `^{i}`},
		{"Pythagorean squared", `^{2}`},
		{"quadratic sqrt", `\sqrt{`},
		{"quadratic pm", `\pm`},
		{"Einstein mu nu", `\mu`},
		{"Schrodinger partial", `\partial`},
		{"Schrodinger psi", `\psi`},
		{"normal distribution sqrt", `\sqrt{`},
		{"Gauss oint", `\oint`},
		{"Fourier int", `\int`},
		{"Fourier infty", `\infty`},
		{"Fourier omega", `\omega`},
		{"Stokes oint", `\oint`},
		{"Euler-Lagrange partial", `\partial`},
		{"Matrix det sum", `\sum`},
		{"logistic map subscript n+1", `_{n}`},
		{"entropy sum", `\sum`},
	}

	for _, tc := range checks {
		if !strings.Contains(got, tc.contains) {
			t.Errorf("%s: expected output to contain %q", tc.desc, tc.contains)
		}
	}
}

func TestRenderUnicodeSetTheory(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", "unicode-set-theory.md"))
	if err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	checks := []struct {
		desc     string
		contains string
	}{
		{"subseteq", `\subseteq`},
		{"cup", `\cup`},
		{"cap", `\cap`},
		{"lor", `\lor`},
		{"land", `\land`},
		{"notin", `\notin`},
		{"emptyset", `\emptyset`},
		{"subset chain", `\subset`},
		{"reals", `\mathbb{R}`},
		{"naturals", `\mathbb{N}`},
		{"integers", `\mathbb{Z}`},
		{"rationals", `\mathbb{Q}`},
		{"complex", `\mathbb{C}`},
		{"exists", `\exists`},
		{"forall", `\forall`},
		{"Rightarrow", `\Rightarrow`},
	}

	for _, tc := range checks {
		if !strings.Contains(got, tc.contains) {
			t.Errorf("%s: expected output to contain %q", tc.desc, tc.contains)
		}
	}
}

func TestRenderUnicodeLinearAlgebra(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", "unicode-linear-algebra.md"))
	if err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	checks := []struct {
		desc     string
		contains string
	}{
		{"sum", `\sum`},
		{"lambda", `\lambda`},
		{"reals", `\mathbb{R}`},
		{"sqrt", `\sqrt{`},
		{"cdot", `\cdot`},
	}

	for _, tc := range checks {
		if !strings.Contains(got, tc.contains) {
			t.Errorf("%s: expected output to contain %q", tc.desc, tc.contains)
		}
	}
}

func TestRenderUnicodeCalculus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", "unicode-calculus.md"))
	if err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	checks := []struct {
		desc     string
		contains string
	}{
		{"int", `\int`},
		{"partial", `\partial`},
		{"nabla", `\nabla`},
		{"sum", `\sum`},
		{"infty", `\infty`},
		{"ldots", `\ldots`},
		{"pi", `\pi`},
	}

	for _, tc := range checks {
		if !strings.Contains(got, tc.contains) {
			t.Errorf("%s: expected output to contain %q", tc.desc, tc.contains)
		}
	}
}

func TestRenderUnicodeProbability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", "unicode-probability.md"))
	if err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	checks := []struct {
		desc     string
		contains string
	}{
		{"sum", `\sum`},
		{"sqrt", `\sqrt{`},
		{"sigma", `\sigma`},
		{"rho", `\rho`},
		{"infty", `\infty`},
		{"pi", `\pi`},
		{"cdot", `\cdot`},
	}

	for _, tc := range checks {
		if !strings.Contains(got, tc.contains) {
			t.Errorf("%s: expected output to contain %q", tc.desc, tc.contains)
		}
	}
}

func TestRenderUnicodeEdgeCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", "unicode-edge-cases.md"))
	if err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Inline code with math: the code span content goes through goldmark's
	// <code> rendering. The preprocessor should protect it from full TeX
	// conversion, but partial conversion of individual chars inside code
	// may still occur (known limitation).
	// Verify that the non-code math IS converted.
	if !strings.Contains(got, `\in`) {
		t.Error("expected non-code math to be converted")
	}

	// Degree sign should be converted.
	if !strings.Contains(got, `\circ`) {
		t.Error("expected \\circ for degree sign")
	}

	// Greek variants.
	if !strings.Contains(got, `\epsilon`) {
		t.Error("expected \\epsilon variant")
	}
	if !strings.Contains(got, `\vartheta`) {
		t.Error("expected \\vartheta variant")
	}
	if !strings.Contains(got, `\varphi`) {
		t.Error("expected \\varphi variant")
	}
	if !strings.Contains(got, `\ell`) {
		t.Error("expected \\ell")
	}

	// Fractions.
	if !strings.Contains(got, `\frac{1}{2}`) {
		t.Error("expected 1/2 fraction")
	}
	if !strings.Contains(got, `\frac{5}{6}`) {
		t.Error("expected 5/6 fraction")
	}
	if !strings.Contains(got, `\frac{7}{8}`) {
		t.Error("expected 7/8 fraction")
	}

	// Blackboard bold coverage.
	for _, bb := range []string{`\mathbb{N}`, `\mathbb{Z}`, `\mathbb{Q}`, `\mathbb{R}`, `\mathbb{C}`, `\mathbb{P}`, `\mathbb{F}`, `\mathbb{H}`} {
		if !strings.Contains(got, bb) {
			t.Errorf("expected %s in output", bb)
		}
	}

	// Logical operators.
	if !strings.Contains(got, `\land`) {
		t.Error("expected \\land")
	}
	if !strings.Contains(got, `\lor`) {
		t.Error("expected \\lor")
	}
	if !strings.Contains(got, `\neg`) {
		t.Error("expected \\neg")
	}
	if !strings.Contains(got, `\Rightarrow`) {
		t.Error("expected \\Rightarrow")
	}

	// Arrows.
	if !strings.Contains(got, `\to`) {
		t.Error("expected \\to")
	}
	if !strings.Contains(got, `\mapsto`) {
		t.Error("expected \\mapsto")
	}
	if !strings.Contains(got, `\Leftrightarrow`) {
		t.Error("expected \\Leftrightarrow")
	}

	// Special symbols.
	if !strings.Contains(got, `\angle`) {
		t.Error("expected \\angle")
	}
	if !strings.Contains(got, `\perp`) {
		t.Error("expected \\perp")
	}
	if !strings.Contains(got, `\oplus`) {
		t.Error("expected \\oplus")
	}
	if !strings.Contains(got, `\otimes`) {
		t.Error("expected \\otimes")
	}
	if !strings.Contains(got, `\emptyset`) {
		t.Error("expected \\emptyset")
	}

	// Comparison chain.
	if !strings.Contains(got, `\le`) {
		t.Error("expected \\le")
	}
	if !strings.Contains(got, `\infty`) {
		t.Error("expected \\infty")
	}

	// Approx chain.
	if !strings.Contains(got, `\neq`) {
		t.Error("expected \\neq")
	}
	if !strings.Contains(got, `\approx`) {
		t.Error("expected \\approx")
	}
	if !strings.Contains(got, `\equiv`) {
		t.Error("expected \\equiv")
	}
	if !strings.Contains(got, `\sim`) {
		t.Error("expected \\sim")
	}
	if !strings.Contains(got, `\propto`) {
		t.Error("expected \\propto")
	}

	// Product notation.
	if !strings.Contains(got, `\prod`) {
		t.Error("expected \\prod")
	}
	if !strings.Contains(got, `\ldots`) {
		t.Error("expected \\ldots")
	}
	if !strings.Contains(got, `\cdot`) {
		t.Error("expected \\cdot")
	}

	// Sum notation.
	if !strings.Contains(got, `\sum`) {
		t.Error("expected \\sum")
	}
}

func TestRenderUnicodePhysics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", "unicode-physics.md"))
	if err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	checks := []struct {
		desc     string
		contains string
	}{
		{"fraction kinetic energy", `\frac{1}{2}`},
		{"cdot momentum", `\cdot`},
		{"cross product torque", `\times`},
		{"sqrt angular freq", `\sqrt{`},
		{"omega angular freq", `\omega`},
		{"nabla E field", `\nabla`},
		{"partial A/partial t", `\partial`},
		{"mu_0", `\mu`},
		{"geq entropy", `\ge`},
		{"bra-ket not crash", `\psi`}, // at least ψ should convert
	}

	for _, tc := range checks {
		if !strings.Contains(got, tc.contains) {
			t.Errorf("%s: expected output to contain %q", tc.desc, tc.contains)
		}
	}

	// Should NOT crash or produce empty output.
	if len(got) == 0 {
		t.Error("expected non-empty output")
	}
}

func TestRenderUnicodeAllChars(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "math", "unicode-all-chars.md"))
	if err != nil {
		t.Fatalf("fixture not found: %v", err)
	}

	md := NewRenderer()
	got, err := Render(md, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// All Greek lowercase should be converted.
	lowerGreek := []string{
		`\alpha`, `\beta`, `\gamma`, `\delta`, `\varepsilon`, `\zeta`, `\eta`,
		`\theta`, `\iota`, `\kappa`, `\lambda`, `\mu`, `\nu`, `\xi`, `\pi`,
		`\rho`, `\sigma`, `\tau`, `\upsilon`, `\phi`, `\chi`, `\psi`, `\omega`,
	}
	for _, tex := range lowerGreek {
		if !strings.Contains(got, tex) {
			t.Errorf("expected %s in all-chars render", tex)
		}
	}

	// Greek variants.
	variants := []string{`\epsilon`, `\vartheta`, `\varphi`, `\varrho`, `\varkappa`, `\varpi`, `\ell`}
	for _, v := range variants {
		if !strings.Contains(got, v) {
			t.Errorf("expected variant %s in all-chars render", v)
		}
	}

	// All blackboard bold.
	bb := []string{`\mathbb{R}`, `\mathbb{N}`, `\mathbb{Z}`, `\mathbb{Q}`, `\mathbb{C}`, `\mathbb{P}`, `\mathbb{F}`, `\mathbb{H}`}
	for _, b := range bb {
		if !strings.Contains(got, b) {
			t.Errorf("expected %s in all-chars render", b)
		}
	}

	// Key operators.
	ops := []string{`\forall`, `\exists`, `\in`, `\notin`, `\sum`, `\prod`, `\int`, `\oint`, `\partial`, `\nabla`, `\infty`}
	for _, o := range ops {
		if !strings.Contains(got, o) {
			t.Errorf("expected operator %s in all-chars render", o)
		}
	}

	// Comparisons and binary ops.
	comps := []string{`\le`, `\ge`, `\neq`, `\approx`, `\propto`, `\sim`, `\cong`, `\equiv`, `\pm`, `\mp`, `\times`, `\div`}
	for _, c := range comps {
		if !strings.Contains(got, c) {
			t.Errorf("expected comp %s in all-chars render", c)
		}
	}

	// Arrows.
	arrows := []string{`\to`, `\mapsto`, `\Rightarrow`, `\Leftrightarrow`, `\leftarrow`, `\leftrightarrow`, `\downarrow`, `\uparrow`}
	for _, a := range arrows {
		if !strings.Contains(got, a) {
			t.Errorf("expected arrow %s in all-chars render", a)
		}
	}

	// Set ops.
	setops := []string{`\subset`, `\supset`, `\subseteq`, `\supseteq`, `\not\subset`, `\not\supset`, `\cup`, `\cap`, `\emptyset`}
	for _, s := range setops {
		if !strings.Contains(got, s) {
			t.Errorf("expected set op %s in all-chars render", s)
		}
	}

	// Logic and special.
	extras := []string{`\neg`, `\land`, `\lor`, `\oplus`, `\otimes`, `\perp`, `\angle`, `\parallel`, `\cdot`, `\ldots`}
	for _, e := range extras {
		if !strings.Contains(got, e) {
			t.Errorf("expected extra %s in all-chars render", e)
		}
	}

	// Fractions in the table.
	fracs := []string{`\frac{1}{2}`, `\frac{1}{3}`, `\frac{2}{3}`, `\frac{1}{4}`}
	for _, f := range fracs {
		if !strings.Contains(got, f) {
			t.Errorf("expected fraction %s in all-chars render", f)
		}
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
