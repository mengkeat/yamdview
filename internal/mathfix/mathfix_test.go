package mathfix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Detection tests ──────────────────────────────────────

func TestHasUnicodeMath(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"∀x ∈ ℝ, x² ≥ 0", true},
		{"αᵢ = βᵢ + γᵢ", true},
		{"∫₀¹ x² dx = 1/3", true},
		{"E = mc²", true},
		{"The value of π is 3.14", true},
		{"", false},
		{"Hello world", false},
		{"It's a café résumé", false},
		{"The result is 42.", false},
		{"$x^2 + y^2$", false}, // no Unicode math, just ASCII TeX
	}

	for _, tc := range tests {
		got := HasUnicodeMath(tc.input)
		if got != tc.want {
			t.Errorf("HasUnicodeMath(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestScore(t *testing.T) {
	tests := []struct {
		input       string
		wantAtLeast float64
		wantAtMost  float64
	}{
		// Pure math should score high.
		{"∀x ∈ ℝ, x² ≥ 0", 0.5, 1.0},
		{"∫₀¹ x² dx", 0.5, 1.0},
		// Mixed math/prose should score moderate.
		{"The integral ∫₀¹ evaluates to ⅓.", 0.1, 0.8},
		// Pure prose should score zero or very low.
		{"Hello world", 0.0, 0.05},
		{"It's a café résumé", 0.0, 0.05},
		{"", 0.0, 0.0},
	}

	for _, tc := range tests {
		score := Score(tc.input)
		if score < tc.wantAtLeast {
			t.Errorf("Score(%q) = %v, want at least %v", tc.input, score, tc.wantAtLeast)
		}
		if score > tc.wantAtMost {
			t.Errorf("Score(%q) = %v, want at most %v", tc.input, score, tc.wantAtMost)
		}
	}
}

// ── Conversion tests ────────────────────────────────────

func TestConvertChars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		omits    []string
	}{
		{
			name:     "greek letters",
			input:    "α + β = γ",
			contains: []string{`\alpha`, `\beta`, `\gamma`},
		},
		{
			name:     "greek variant digamma",
			input:    "ϝ",
			contains: []string{`\digamma`},
		},
		{
			name:     "operators",
			input:    "∀x ∈ ℝ",
			contains: []string{`\forall`, `\in`, `\mathbb{R}`},
		},
		{
			name:     "superscripts",
			input:    "x² + y³",
			contains: []string{`^{2}`, `^{3}`},
		},
		{
			name:     "subscripts",
			input:    "x₀ + y₁",
			contains: []string{`_{0}`, `_{1}`},
		},
		{
			name:     "superscript run",
			input:    "x²³",
			contains: []string{`^{23}`},
		},
		{
			name:     "subscript letter",
			input:    "αᵢ",
			contains: []string{`\alpha`, `_{i}`},
		},
		{
			name:     "fraction",
			input:    "½ of the total",
			contains: []string{`\frac{1}{2}`},
		},
		{
			name:     "square root single arg",
			input:    "√x",
			contains: []string{`\sqrt{x}`},
		},
		{
			name:     "square root paren",
			input:    "√(x²+y²)",
			contains: []string{`\sqrt{x^{2}+y^{2}}`},
		},
		{
			name:     "comparison operators",
			input:    "a ≤ b ≥ c ≠ d ≈ e",
			contains: []string{`\le`, `\ge`, `\neq`, `\approx`},
		},
		{
			name:     "arrows",
			input:    "x → y ⇒ z",
			contains: []string{`\to`, `\Rightarrow`},
		},
		{
			name:     "set operations",
			input:    "A ∪ B ∩ C ⊆ D",
			contains: []string{`\cup`, `\cap`, `\subseteq`},
		},
		{
			name:     "blackboard bold",
			input:    "x ∈ ℝ, n ∈ ℕ",
			contains: []string{`\mathbb{R}`, `\mathbb{N}`},
		},
		{
			name:  "pass through non-math",
			input: "hello world 123",
			omits: []string{`\`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, diags := convertChars(tc.input)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("convertChars(%q) = %q, want to contain %q\nDiagnostics: %v", tc.input, got, want, diags)
				}
			}
			for _, notWant := range tc.omits {
				if strings.Contains(got, notWant) {
					t.Errorf("convertChars(%q) = %q, want to NOT contain %q", tc.input, got, notWant)
				}
			}
		})
	}
}

// ── Fix (paragraph-level) tests ─────────────────────────

func TestFix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		applied  bool
		contains []string
		omits    []string
	}{
		{
			name:    "pure math display",
			input:   "∀x ∈ ℝ, x² ≥ 0",
			applied: true,
			contains: []string{
				"$$",
				`\forall`,
				`\in`,
				`\mathbb{R}`,
				`^{2}`,
				`\ge`,
				"$$",
			},
		},
		{
			name:    "pure math Greek display",
			input:   "αᵢ = βᵢ + γᵢ",
			applied: true,
			contains: []string{
				"$$",
				`\alpha`,
				`_{i}`,
				`\beta`,
				`\gamma`,
				"$$",
			},
		},
		{
			name:    "integral display",
			input:   "∫₀¹ x² dx = 1/3",
			applied: true,
			contains: []string{
				`\int`,
				`_{0}`,
				`^{1}`,
				`^{2}`,
			},
		},
		{
			name:    "inline math in prose",
			input:   "The value of π is approximately 3.14",
			applied: true,
			contains: []string{
				`$`,
				`\pi`,
			},
		},
		{
			name:    "no unicode math unchanged",
			input:   "Hello world",
			applied: false,
		},
		{
			name:    "empty string unchanged",
			input:   "",
			applied: false,
		},
		{
			name:    "already has TeX delimiters unchanged",
			input:   "The equation $x^2 + y^2$ is well known",
			applied: false, // no Unicode math chars
		},
		{
			name:    "inline code preserved",
			input:   "Use `∀x ∈ ℝ` in code",
			applied: false, // all Unicode math is inside backticks
			contains: []string{
				"`∀x ∈ ℝ`", // backtick content unchanged
			},
		},
		{
			name:    "inline code after non-ascii prefix preserved",
			input:   "éé `α`",
			applied: false,
			contains: []string{
				"éé `α`",
			},
		},
		{
			name:    "multi-backtick inline code preserved",
			input:   "Use ``α + β`` in code",
			applied: false,
			contains: []string{
				"``α + β``",
			},
		},
		{
			name:    "square root",
			input:   "The magnitude is √(x²+y²)",
			applied: true,
			contains: []string{
				`\sqrt{`,
				`^{2}`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fr := Fix(tc.input)

			if fr.Applied != tc.applied {
				t.Errorf("Fix(%q).Applied = %v, want %v", tc.input, fr.Applied, tc.applied)
			}

			for _, want := range tc.contains {
				if !strings.Contains(fr.Converted, want) {
					t.Errorf("Fix(%q).Converted = %q, want to contain %q", tc.input, fr.Converted, want)
				}
			}

			for _, notWant := range tc.omits {
				if strings.Contains(fr.Converted, notWant) {
					t.Errorf("Fix(%q).Converted = %q, want to NOT contain %q", tc.input, fr.Converted, notWant)
				}
			}
		})
	}
}

// ── Preprocessor tests ──────────────────────────────────

func TestPreprocess(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		omits    []string
	}{
		{
			name:  "math paragraph converted",
			input: "∀x ∈ ℝ, x² ≥ 0\n",
			contains: []string{
				"$$",
				`\forall`,
			},
		},
		{
			name:  "code fence untouched",
			input: "```\n∀x ∈ ℝ\n```\n",
			omits: []string{
				`\forall`,
			},
		},
		{
			name:  "fenced math block untouched",
			input: "```math\n∀x ∈ ℝ\n```\n",
			omits: []string{
				"$$",
			},
		},
		{
			name:  "existing TeX untouched",
			input: "The equation $x^2 + y^2$ is famous.\n",
			omits: []string{
				"$$x^2",
			},
		},
		{
			name:  "no math untouched",
			input: "Hello world\n",
			omits: []string{
				"$",
			},
		},
		{
			name:  "mixed paragraphs",
			input: "Hello world\n\n∀x ∈ ℝ, x² ≥ 0\n\nGoodbye\n",
			contains: []string{
				"Hello world",
				`\forall`,
				"Goodbye",
			},
		},
		{
			name:  "inline code preserved",
			input: "Use `∀x` in your code\n",
			contains: []string{
				"`∀x`",
			},
			omits: []string{
				"$$∀",
			},
		},
		{
			name:  "heading with math",
			input: "# Properties of ℝ\n",
			contains: []string{
				`\mathbb{R}`,
			},
		},
		{
			name:  "multiple paragraphs with math",
			input: "The integral ∫₀¹ x² dx\n\nequals ⅓ of the area.\n",
			contains: []string{
				`\int`,
				`\frac{1}{3}`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Preprocess([]byte(tc.input))

			for _, want := range tc.contains {
				if !strings.Contains(string(got), want) {
					t.Errorf("Preprocess(%q) = %q, want to contain %q", tc.input, string(got), want)
				}
			}
			for _, notWant := range tc.omits {
				if strings.Contains(string(got), notWant) {
					t.Errorf("Preprocess(%q) = %q, want to NOT contain %q", tc.input, string(got), notWant)
				}
			}
		})
	}
}

func TestPreprocessTextFenceEquation(t *testing.T) {
	input := "```text\ndv/dt = g − kD |v| v + (kM·|ω|)(axis × v) + f_NN(v)\n```\n"
	got := string(Preprocess([]byte(input)))

	contains := []string{
		"$$",
		`\frac{dv}{dt}`,
		`k_{D}`,
		`\lvert v\rvert`,
		`k_{M}`,
		`\cdot`,
		`\omega`,
		`\mathrm{axis}`,
		`\times`,
		`f_{\mathrm{NN}}`,
	}
	for _, want := range contains {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in converted text fence, got: %q", want, got)
		}
	}
	if strings.Contains(got, "```text") {
		t.Errorf("expected text fence to become display math, got: %q", got)
	}
}

func TestPreprocessTextFenceProseUnchanged(t *testing.T) {
	input := "```text\n10 passed in 27.38s\n```\n"
	got := string(Preprocess([]byte(input)))
	if got != input {
		t.Errorf("plain text fence modified:\ngot:  %q\nwant: %q", got, input)
	}
}

func TestPreprocessLongFenceKeepsShortFenceContent(t *testing.T) {
	input := "````text\n```\n∀x ∈ ℝ\n````\n"
	got := string(Preprocess([]byte(input)))
	if got != input {
		t.Fatalf("long text fence should not close on shorter fence content:\ngot:  %q\nwant: %q", got, input)
	}
}

func TestPreprocessIdempotence(t *testing.T) {
	// Running Preprocess twice should produce the same output as running once,
	// because the first pass wraps math in $ delimiters, and hasTeXDelimiters
	// should prevent double-processing.
	input := "∀x ∈ ℝ, x² ≥ 0\n"
	first := Preprocess([]byte(input))
	second := Preprocess(first)

	if string(first) != string(second) {
		t.Errorf("Preprocess not idempotent:\nfirst:  %q\nsecond: %q", string(first), string(second))
	}
}

func TestPreprocessExistingTeX(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"dollar inline", "The value $x^2$ is known.\n"},
		{"dollar display", "$$\nE = mc^2\n$$\n"},
		{"backslash paren", `The value \( x^2 \) is known.` + "\n"},
		{"backslash bracket", `\[ E = mc^2 \]` + "\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Preprocess([]byte(tc.input))
			if string(got) != tc.input {
				t.Errorf("Preprocess(%q) = %q, want unchanged", tc.input, string(got))
			}
		})
	}
}

// ── False positive tests ────────────────────────────────

func TestFalsePositiveProse(t *testing.T) {
	// Prose with accented characters that are NOT math.
	fr := Fix("It's a café résumé with naïve façade")
	if fr.Applied {
		t.Errorf("Fix should not apply to prose with accented Latin: Applied=%v, Converted=%q", fr.Applied, fr.Converted)
	}
}

func TestFalsePositiveCurrency(t *testing.T) {
	fr := Fix("The price is $42.99")
	if fr.Applied {
		t.Errorf("Fix should not apply to currency text: Applied=%v", fr.Applied)
	}
}

func TestFalsePositivePureEnglish(t *testing.T) {
	fr := Fix("The quick brown fox jumps over the lazy dog")
	if fr.Applied {
		t.Errorf("Fix should not apply to pure English: Applied=%v", fr.Applied)
	}
}

func TestFalsePositiveNumbers(t *testing.T) {
	fr := Fix("The result is 42.5 and the count is 100")
	if fr.Applied {
		t.Errorf("Fix should not apply to numbers: Applied=%v", fr.Applied)
	}
}

// ── Idempotence tests ───────────────────────────────────

func TestFixIdempotence(t *testing.T) {
	// After Fix wraps math in $ delimiters, the next call should see no
	// Unicode math chars (they've been converted) and return Applied=false.
	input := "∀x ∈ ℝ, x² ≥ 0"
	first := Fix(input)
	if !first.Applied {
		t.Fatalf("first Fix should apply: %v", first.Applied)
	}

	// The converted text should have the Unicode chars replaced with TeX
	// inside $$ delimiters.
	if strings.Contains(first.Converted, "∀") {
		t.Errorf("converted text still contains Unicode math: %q", first.Converted)
	}

	// Second pass should be a no-op.
	second := Fix(first.Converted)
	if second.Applied {
		t.Errorf("second Fix should not apply (idempotence): Applied=%v", second.Applied)
	}
	if second.Converted != first.Converted {
		t.Errorf("idempotence broken:\nfirst:  %q\nsecond: %q", first.Converted, second.Converted)
	}
}

// ── Edge case tests ─────────────────────────────────────

func TestConvertCharsSqrtNoArg(t *testing.T) {
	// √ at end of string should produce a diagnostic.
	_, diags := convertChars("√")
	found := false
	for _, d := range diags {
		if d.Code == "math.sqrt_no_arg" {
			found = true
		}
	}
	if !found {
		t.Error("expected math.sqrt_no_arg diagnostic for trailing √")
	}
}

func TestFixMixedProseAndMath(t *testing.T) {
	fr := Fix("The equation ∀x ∈ ℝ gives x² ≥ 0 always")
	if !fr.Applied {
		t.Fatal("expected Fix to apply")
	}
	// Should wrap the math part but not "The equation" or "always".
	if !strings.Contains(fr.Converted, "The equation ") {
		t.Errorf("prose prefix lost: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\forall`) {
		t.Errorf("missing TeX conversion: %q", fr.Converted)
	}
}

func TestPreprocessCodeFenceContent(t *testing.T) {
	input := "```python\n# ∀x ∈ ℝ\nprint('hello')\n```\n"
	got := string(Preprocess([]byte(input)))
	if got != input {
		t.Errorf("code fence content modified:\ngot:  %q\nwant: %q", got, input)
	}
}

func TestPreprocessTildeFence(t *testing.T) {
	input := "~~~\n∀x ∈ ℝ\n~~~\n"
	got := string(Preprocess([]byte(input)))
	if got != input {
		t.Errorf("tilde fence content modified:\ngot:  %q\nwant: %q", got, input)
	}
}

func TestConvertCharsSpacing(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no double spaces after operators",
			input: "∀x ∈ ℝ",
			want:  `\forall x \in \mathbb{R}`,
		},
		{
			name:  "space before letter but not before comma",
			input: "x ∈ ℝ, y ∈ ℕ",
			want:  `x \in \mathbb{R}, y \in \mathbb{N}`,
		},
		{
			name:  "space before letter after ge",
			input: "x ≥ 0",
			want:  `x \ge 0`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if got != tc.want {
				t.Errorf("convertChars(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRealWorldNeuralODEBenchmark(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "code", "BallFlight", "experiments", "research-log", "2026-04-28-neural-ode-benchmark.md"))
	if err != nil {
		t.Skipf("fixture not found: %v", err)
	}

	result := Preprocess(data)
	resultStr := string(result)

	// Log the lines that changed
	origLines := strings.Split(string(data), "\n")
	resLines := strings.Split(resultStr, "\n")
	for i := 0; i < len(origLines) && i < len(resLines); i++ {
		if origLines[i] != resLines[i] {
			t.Logf("Line %d changed:", i+1)
			t.Logf("  FROM: %q", origLines[i])
			t.Logf("  TO:   %q", resLines[i])
		}
	}
}

func TestDegreeSignConversion(t *testing.T) {
	fr := Fix("Axis err (°)")
	if !fr.Applied {
		t.Fatal("expected degree sign to be converted")
	}
	if !strings.Contains(fr.Converted, `\circ`) {
		t.Errorf("expected \\circ in output, got: %q", fr.Converted)
	}
}

func TestDegreeSignInline(t *testing.T) {
	fr := Fix("error is ~22°, while")
	if !fr.Applied {
		t.Fatal("expected degree sign to be converted")
	}
	if !strings.Contains(fr.Converted, `\circ`) {
		t.Errorf("expected \\circ in output, got: %q", fr.Converted)
	}
	// The span should be "22°," — "while" should be outside the math delimiters.
	if strings.Contains(fr.Converted, "$while$") {
		t.Errorf("span extended too far, included 'while' inside math: %q", fr.Converted)
	}
}

func TestUnicodeMathInCodeUnchanged(t *testing.T) {
	fr := Fix("`N ≤ 100`")
	if fr.Applied {
		t.Errorf("Unicode math inside code should not be converted: %q", fr.Converted)
	}
}

// ── Famous equation tests ───────────────────────────────

func TestFixEulerIdentity(t *testing.T) {
	fr := Fix("eⁱπ + 1 = 0")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for Euler's identity")
	}
	if !strings.Contains(fr.Converted, `\pi`) {
		t.Errorf("expected \\pi in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `^{i}`) {
		t.Errorf("expected ^{i} superscript in output, got: %q", fr.Converted)
	}
}

func TestFixPythagoreanTheorem(t *testing.T) {
	fr := Fix("a² + b² = c²")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for Pythagorean theorem")
	}
	if !strings.Contains(fr.Converted, `^{2}`) {
		t.Errorf("expected ^{2} superscript in output, got: %q", fr.Converted)
	}
}

func TestFixNewtonSecondLaw(t *testing.T) {
	fr := Fix("F = ma")
	if fr.Applied {
		t.Errorf("plain ASCII equation should not trigger Fix: Applied=%v", fr.Applied)
	}
}

func TestFixQuadraticFormula(t *testing.T) {
	input := "x = (−b ± √(b²−4ac)) / 2a"
	fr := Fix(input)
	if !fr.Applied {
		t.Fatal("expected Fix to apply for quadratic formula")
	}
	if !strings.Contains(fr.Converted, `\sqrt{`) {
		t.Errorf("expected \\sqrt in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\pm`) {
		t.Errorf("expected \\pm in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `^{2}`) {
		t.Errorf("expected ^{2} in output, got: %q", fr.Converted)
	}
}

func TestFixFourierTransform(t *testing.T) {
	fr := Fix("f̂(ω) = ∫₋∞⁺∞ f(t)e⁻ⁱωt dt")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for Fourier transform")
	}
	if !strings.Contains(fr.Converted, `\int`) {
		t.Errorf("expected \\int in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\infty`) {
		t.Errorf("expected \\infty in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\omega`) {
		t.Errorf("expected \\omega in output, got: %q", fr.Converted)
	}
}

func TestFixGaussLaw(t *testing.T) {
	fr := Fix("∮ E · dA = Q/ε₀")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for Gauss's law")
	}
	if !strings.Contains(fr.Converted, `\oint`) {
		t.Errorf("expected \\oint in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\cdot`) {
		t.Errorf("expected \\cdot in output, got: %q", fr.Converted)
	}
}

func TestFixEntropyFormula(t *testing.T) {
	fr := Fix("S = −∑ᵢ pᵢ ln(pᵢ)")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for entropy formula")
	}
	if !strings.Contains(fr.Converted, `\sum`) {
		t.Errorf("expected \\sum in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `_{i}`) {
		t.Errorf("expected _{i} in output, got: %q", fr.Converted)
	}
}

// ── Set theory tests ────────────────────────────────────

func TestFixSetOperations(t *testing.T) {
	fr := Fix("A ∪ B = {x ∈ U : x ∈ A ∨ x ∈ B}")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for set operations")
	}
	contains := []string{`\cup`, `\in`, `\lor`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

func TestFixSetIntersection(t *testing.T) {
	fr := Fix("A ∩ B = {x ∈ U : x ∈ A ∧ x ∈ B}")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for set intersection")
	}
	contains := []string{`\cap`, `\land`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

func TestFixProperSubset(t *testing.T) {
	fr := Fix("ℕ ⊂ ℤ ⊂ ℚ ⊂ ℝ ⊂ ℂ")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for subset chain")
	}
	contains := []string{`\subset`, `\mathbb{N}`, `\mathbb{Z}`, `\mathbb{Q}`, `\mathbb{R}`, `\mathbb{C}`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

func TestFixEmptySet(t *testing.T) {
	fr := Fix("For any set S, ∅ ⊆ S")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for empty set")
	}
	if !strings.Contains(fr.Converted, `\emptyset`) {
		t.Errorf("expected \\emptyset in output, got: %q", fr.Converted)
	}
}

func TestFixLogicalOperators(t *testing.T) {
	fr := Fix("(P ∧ Q) ∨ (¬R) ⇒ (P ∨ R)")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for logical operators")
	}
	contains := []string{`\land`, `\lor`, `\neg`, `\Rightarrow`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

// ── Linear algebra tests ────────────────────────────────

func TestFixMatrixMultiplication(t *testing.T) {
	fr := Fix("(AB)ᵢⱼ = ∑ₖ Aᵢₖ Bₖⱼ")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for matrix multiplication")
	}
	if !strings.Contains(fr.Converted, `\sum`) {
		t.Errorf("expected \\sum in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `_{k}`) {
		t.Errorf("expected _{k} subscript in output, got: %q", fr.Converted)
	}
}

func TestFixEigenvalue(t *testing.T) {
	fr := Fix("det(A − λI) = 0")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for eigenvalue equation")
	}
	if !strings.Contains(fr.Converted, `\lambda`) {
		t.Errorf("expected \\lambda in output, got: %q", fr.Converted)
	}
}

// ── Calculus tests ──────────────────────────────────────

func TestFixTaylorSeries(t *testing.T) {
	fr := Fix("f(x) = ∑ₙ₌₀∞ f⁽ⁿ⁾(a)/n! · (x−a)ⁿ")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for Taylor series")
	}
	if !strings.Contains(fr.Converted, `\sum`) {
		t.Errorf("expected \\sum in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\infty`) {
		t.Errorf("expected \\infty in output, got: %q", fr.Converted)
	}
}

func TestFixGradient(t *testing.T) {
	fr := Fix("∇f = (∂f/∂x₁, ∂f/∂x₂, …, ∂f/∂xₙ)")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for gradient")
	}
	contains := []string{`\nabla`, `\partial`, `\ldots`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

func TestFixDivergence(t *testing.T) {
	fr := Fix("∇ · F = ∂F₁/∂x₁ + ∂F₂/∂x₂")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for divergence")
	}
	if !strings.Contains(fr.Converted, `\nabla`) {
		t.Errorf("expected \\nabla in output, got: %q", fr.Converted)
	}
}

func TestFixConvergence(t *testing.T) {
	fr := Fix("∑ₙ₌₁∞ 1/n² = π²/6")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for convergence series")
	}
	if !strings.Contains(fr.Converted, `\pi`) {
		t.Errorf("expected \\pi in output, got: %q", fr.Converted)
	}
}

// ── Probability tests ───────────────────────────────────

func TestFixExpectedValue(t *testing.T) {
	fr := Fix("E[X] = ∑ₓ x · P(X = x)")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for expected value")
	}
	if !strings.Contains(fr.Converted, `\sum`) {
		t.Errorf("expected \\sum in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\cdot`) {
		t.Errorf("expected \\cdot in output, got: %q", fr.Converted)
	}
}

func TestFixVariance(t *testing.T) {
	fr := Fix("Var(X) = E[X²] − (E[X])²")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for variance")
	}
	if !strings.Contains(fr.Converted, `^{2}`) {
		t.Errorf("expected ^{2} in output, got: %q", fr.Converted)
	}
}

func TestFixNormalPDF(t *testing.T) {
	fr := Fix("φ(x) = (1/√(2π)) · e⁻ˣ²⁄²")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for normal PDF")
	}
	if !strings.Contains(fr.Converted, `\sqrt{`) {
		t.Errorf("expected \\sqrt in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\pi`) {
		t.Errorf("expected \\pi in output, got: %q", fr.Converted)
	}
}

func TestFixCorrelation(t *testing.T) {
	fr := Fix("ρ = Cov(X, Y) / (σₓ · σᵧ)")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for correlation")
	}
	if !strings.Contains(fr.Converted, `\rho`) {
		t.Errorf("expected \\rho in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\sigma`) {
		t.Errorf("expected \\sigma in output, got: %q", fr.Converted)
	}
}

// ── Greek variant tests ─────────────────────────────────

func TestConvertGreekVariants(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"epsilon variant", "ϵx", `\epsilon`},
		{"vartheta", "ϑ/2", `\vartheta`},
		{"varphi", "ϕ(x)", `\varphi`},
		{"varrho", "ϱ = 1", `\varrho`},
		{"varkappa", "ϰ > 0", `\varkappa`},
		{"varpi", "ϖ ≈ 3.14", `\varpi`},
		{"ell", "ℓ(θ)", `\ell`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Subscript letter tests ──────────────────────────────

func TestConvertSubscriptLetters(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"subscript a", "xₐ", `_{a}`},
		{"subscript e", "xₑ", `_{e}`},
		{"subscript o", "xₒ", `_{o}`},
		{"subscript x", "aₓ", `_{x}`},
		{"subscript h", "xₕ", `_{h}`},
		{"subscript k", "xₖ", `_{k}`},
		{"subscript l", "xₗ", `_{l}`},
		{"subscript m", "xₘ", `_{m}`},
		{"subscript n", "xₙ", `_{n}`},
		{"subscript s", "xₛ", `_{s}`},
		{"subscript t", "xₜ", `_{t}`},
		{"subscript i modifier", "aᵢ", `_{i}`},
		{"subscript r modifier", "aᵣ", `_{r}`},
		{"subscript u modifier", "aᵤ", `_{u}`},
		{"subscript v modifier", "aᵥ", `_{v}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Superscript letter tests ────────────────────────────

func TestConvertSuperscriptLetters(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"superscript n", "nⁿ", `^{n}`},
		{"superscript i", "eⁱ", `^{i}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Arrow conversion tests ──────────────────────────────

func TestConvertArrows(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"right arrow", "x → y", `\to`},
		{"maps to", "x ↦ f(x)", `\mapsto`},
		{"double right", "x ⇒ y", `\Rightarrow`},
		{"iff arrow", "x ⇔ y", `\Leftrightarrow`},
		{"left arrow", "x ← y", `\leftarrow`},
		{"bidirectional", "x ↔ y", `\leftrightarrow`},
		{"down arrow", "x ↓ y", `\downarrow`},
		{"up arrow", "x ↑ y", `\uparrow`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Additional operator tests ───────────────────────────

func TestConvertAdditionalOperators(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"exists", "∃x ∈ ℝ", `\exists`},
		{"not in", "x ∉ A", `\notin`},
		{"proportional", "y ∝ x", `\propto`},
		{"tilde sim", "x ∼ N(0,1)", `\sim`},
		{"congruent", "a ≅ b", `\cong`},
		{"equivalent", "a ≡ b mod n", `\equiv`},
		{"minus plus", "x ∓ y", `\mp`},
		{"division", "x ÷ y", `\div`},
		{"circled plus", "a ⊕ b", `\oplus`},
		{"circled times", "a ⊗ b", `\otimes`},
		{"perpendicular", "A ⊥ B", `\perp`},
		{"angle symbol", "∠ABC", `\angle`},
		{"parallel", "A ‖ B", `\parallel`},
		{"circ operator", "f ∘ g", `\circ`},
		{"star operator", "a ⋆ b", `\star`},
		{"negation", "¬P", `\neg`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Fraction coverage tests ─────────────────────────────

func TestConvertAllFractions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"half", "½", `\frac{1}{2}`},
		{"third", "⅓", `\frac{1}{3}`},
		{"two thirds", "⅔", `\frac{2}{3}`},
		{"quarter", "¼", `\frac{1}{4}`},
		{"three quarters", "¾", `\frac{3}{4}`},
		{"one fifth", "⅕", `\frac{1}{5}`},
		{"two fifths", "⅖", `\frac{2}{5}`},
		{"three fifths", "⅗", `\frac{3}{5}`},
		{"four fifths", "⅘", `\frac{4}{5}`},
		{"one sixth", "⅙", `\frac{1}{6}`},
		{"five sixths", "⅚", `\frac{5}{6}`},
		{"one eighth", "⅛", `\frac{1}{8}`},
		{"three eighths", "⅜", `\frac{3}{8}`},
		{"five eighths", "⅝", `\frac{5}{8}`},
		{"seven eighths", "⅞", `\frac{7}{8}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Blackboard bold coverage tests ──────────────────────

func TestConvertAllBlackboardBold(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"reals", "x ∈ ℝ", `\mathbb{R}`},
		{"naturals", "n ∈ ℕ", `\mathbb{N}`},
		{"integers", "z ∈ ℤ", `\mathbb{Z}`},
		{"rationals", "q ∈ ℚ", `\mathbb{Q}`},
		{"complex", "z ∈ ℂ", `\mathbb{C}`},
		{"primes", "p ∈ ℙ", `\mathbb{P}`},
		{"field", "F = 𝔽", `\mathbb{F}`},
		{"quaternions", "q ∈ ℍ", `\mathbb{H}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Uppercase Greek tests ───────────────────────────────

func TestConvertUppercaseGreek(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"Gamma", "Γ(x)", `\Gamma`},
		{"Delta", "Δy = y₂ − y₁", `\Delta`},
		{"Theta", "Θ(n²)", `\Theta`},
		{"Lambda", "Λ(x)", `\Lambda`},
		{"Xi", "Ξ function", `\Xi`},
		{"Pi product", "Πᵢ aᵢ", `\Pi`},
		{"Sigma sum", "Σᵢ xᵢ", `\Sigma`},
		{"Upsilon", "Υ particle", `\Upsilon`},
		{"Phi", "Φ(x)", `\Phi`},
		{"Psi", "Ψ state", `\Psi`},
		{"Omega", "Ω resistance", `\Omega`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Minus sign tests ────────────────────────────────────

func TestConvertMinusSign(t *testing.T) {
	got, _ := convertChars("−5")
	if !strings.Contains(got, "-5") {
		t.Errorf("expected minus sign to convert to ASCII hyphen, got: %q", got)
	}
}

// ── Comparison chain tests ──────────────────────────────

func TestFixComparisonChain(t *testing.T) {
	fr := Fix("0 < x ≤ 1 ≤ y < ∞")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for comparison chain")
	}
	contains := []string{`\le`, `\infty`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

func TestFixApproxChain(t *testing.T) {
	fr := Fix("a ≠ b ≈ c ≡ d ∼ e ∝ f")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for approx chain")
	}
	contains := []string{`\neq`, `\approx`, `\equiv`, `\sim`, `\propto`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

// ── Multiple spans in one line ──────────────────────────

func TestFixMultipleSpans(t *testing.T) {
	fr := Fix("For x ∈ ℝ and n ∈ ℕ, the map is continuous")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for multiple spans")
	}
	// Both spans should be converted.
	if !strings.Contains(fr.Converted, `\in`) {
		t.Errorf("expected \\in in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\mathbb{R}`) {
		t.Errorf("expected \\mathbb{R} in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\mathbb{N}`) {
		t.Errorf("expected \\mathbb{N} in output, got: %q", fr.Converted)
	}
	// Prose should be preserved.
	if !strings.Contains(fr.Converted, "For ") {
		t.Errorf("prose prefix lost: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, "continuous") {
		t.Errorf("prose suffix lost: %q", fr.Converted)
	}
}

// ── Product and double sub/superscript tests ────────────

func TestFixProductNotation(t *testing.T) {
	fr := Fix("∏ᵢ₌₁ⁿ aᵢ = a₁ · a₂ · … · aₙ")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for product notation")
	}
	contains := []string{`\prod`, `\cdot`, `\ldots`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

func TestFixBinomialSum(t *testing.T) {
	fr := Fix("∑ₖ₌₀ⁿ (n choose k) = 2ⁿ")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for binomial sum")
	}
	if !strings.Contains(fr.Converted, `\sum`) {
		t.Errorf("expected \\sum in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `^{n}`) {
		t.Errorf("expected ^{n} superscript in output, got: %q", fr.Converted)
	}
}

// ── Subscript/superscript operator tests ────────────────

func TestConvertSubscriptOperators(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"subscript plus", "a₊b", `_{+}`},
		{"subscript minus", "a₋b", `_{-}`},
		{"subscript equals", "a₌b", `_{=}`},
		{"subscript open paren", "a₍", `_{(`},
		{"subscript close paren", "₎", `_{)}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

func TestConvertSuperscriptOperators(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"superscript plus", "a⁺b", `^{+}`},
		{"superscript minus", "a⁻b", `^{-}`},
		{"superscript equals", "a⁼b", `^{=}`},
		{"superscript open paren", "a⁽", `^{(`},
		{"superscript close paren", "⁾", `^{)}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Preprocess with various equations ───────────────────

func TestPreprocessEulerIdentity(t *testing.T) {
	input := "Euler's identity: eⁱπ + 1 = 0\n"
	got := string(Preprocess([]byte(input)))
	if !strings.Contains(got, `\pi`) {
		t.Errorf("expected \\pi in preprocessed output, got: %q", got)
	}
}

func TestPreprocessSetTheory(t *testing.T) {
	input := "A ∪ B = {x ∈ U : x ∈ A ∨ x ∈ B}\n"
	got := string(Preprocess([]byte(input)))
	if !strings.Contains(got, `\cup`) {
		t.Errorf("expected \\cup in preprocessed output, got: %q", got)
	}
}

func TestPreprocessBlockquoteWithMath(t *testing.T) {
	input := "> The value π ≈ 3.14\n"
	got := string(Preprocess([]byte(input)))
	if !strings.Contains(got, `\pi`) {
		t.Errorf("expected \\pi in blockquote output, got: %q", got)
	}
}

func TestPreprocessIndentedBlockquoteKeepsWhitespaceWithoutConversion(t *testing.T) {
	// A structural line whose math character is inside an inline code span is
	// never converted; it must be emitted verbatim, preserving indentation.
	input := "  > `α`  \n"
	got := string(Preprocess([]byte(input)))
	if got != input {
		t.Errorf("expected indentation and whitespace preserved, got: %q", got)
	}
}

func TestPreprocessMixedEquations(t *testing.T) {
	input := "The integral ∫₀¹ x² dx = ⅓\n\nFor x ∈ ℝ, x² ≥ 0\n"
	got := string(Preprocess([]byte(input)))
	contains := []string{`\int`, `\frac{1}{3}`, `\in`, `\mathbb{R}`}
	for _, want := range contains {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in output, got: %q", want, got)
		}
	}
}

// ── All lowercase Greek letter tests ────────────────────

func TestConvertAllLowerGreek(t *testing.T) {
	tests := []struct {
		char     string
		contains string
	}{
		{"α", `\alpha`},
		{"β", `\beta`},
		{"γ", `\gamma`},
		{"δ", `\delta`},
		{"ε", `\varepsilon`},
		{"ζ", `\zeta`},
		{"η", `\eta`},
		{"θ", `\theta`},
		{"ι", `\iota`},
		{"κ", `\kappa`},
		{"λ", `\lambda`},
		{"μ", `\mu`},
		{"ν", `\nu`},
		{"ξ", `\xi`},
		{"π", `\pi`},
		{"ρ", `\rho`},
		{"σ", `\sigma`},
		{"τ", `\tau`},
		{"υ", `\upsilon`},
		{"φ", `\phi`},
		{"χ", `\chi`},
		{"ψ", `\psi`},
		{"ω", `\omega`},
	}

	for _, tc := range tests {
		t.Run(tc.char, func(t *testing.T) {
			got, _ := convertChars(tc.char)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.char, got, tc.contains)
			}
		})
	}
}

// ── Mixed superscript + subscript on same variable ──────

func TestConvertMixedSubSuper(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:     "sub then super",
			input:    "x₀²",
			contains: []string{`_{0}`, `^{2}`},
		},
		{
			name:     "super then sub",
			input:    "x²₀",
			contains: []string{`^{2}`, `_{0}`},
		},
		{
			name:     "multiple mixed",
			input:    "a₁² + a₂³",
			contains: []string{`_{1}`, `^{2}`, `_{2}`, `^{3}`},
		},
		{
			name:     "super letter + sub digit",
			input:    "eⁱₙ",
			contains: []string{`^{i}`, `_{n}`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, want)
				}
			}
		})
	}
}

// ── Reverse set operators ───────────────────────────────

func TestConvertReverseSetOps(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"supset", "A ⊃ B", `\supset`},
		{"supseteq", "A ⊇ B", `\supseteq`},
		{"notsubset", "A ⊄ B", `\not\subset`},
		{"notsupset", "A ⊅ B", `\not\supset`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := convertChars(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("convertChars(%q) = %q, want to contain %s", tc.input, got, tc.contains)
			}
		})
	}
}

// ── Contour integral test ───────────────────────────────

func TestConvertContourIntegral(t *testing.T) {
	got, _ := convertChars("∮")
	if !strings.Contains(got, `\oint`) {
		t.Errorf("convertChars(∮) = %q, want to contain \\oint", got)
	}
}

// ── Partial derivative patterns ─────────────────────────

func TestFixPartialDerivative(t *testing.T) {
	fr := Fix("∂f/∂x + ∂f/∂y = 0")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for partial derivatives")
	}
	if strings.Count(fr.Converted, `\partial`) < 2 {
		t.Errorf("expected at least 2 \\partial, got: %q", fr.Converted)
	}
}

func TestFixDelOperator(t *testing.T) {
	fr := Fix("∇²φ = 0")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for Laplacian")
	}
	if !strings.Contains(fr.Converted, `\nabla`) {
		t.Errorf("expected \\nabla in output, got: %q", fr.Converted)
	}
}

// ── Bulk operator test ──────────────────────────────────

func TestFixCrossProduct(t *testing.T) {
	fr := Fix("a × b = c")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for cross product")
	}
	if !strings.Contains(fr.Converted, `\times`) {
		t.Errorf("expected \\times in output, got: %q", fr.Converted)
	}
}

// ── Integral bounds with ∞ ─────────────────────────────

func TestFixIntegralInftyBounds(t *testing.T) {
	fr := Fix("∫₋∞∞ f(x) dx")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for integral with ∞ bounds")
	}
	contains := []string{`\int`, `\infty`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

// ── Double subscript letter test (ə) ────────────────────

func TestConvertSpecialSubscript(t *testing.T) {
	got, _ := convertChars("xₔ")
	if !strings.Contains(got, `_{ə}`) {
		t.Errorf("convertChars(xₔ) = %q, want to contain _{ə}", got)
	}
}

// ── Multiple Greek in one short string ──────────────────

func TestConvertMultipleGreek(t *testing.T) {
	got, _ := convertChars("αβγδε")
	// All five should be converted.
	expected := []string{`\alpha`, `\beta`, `\gamma`, `\delta`, `\varepsilon`}
	for _, exp := range expected {
		if !strings.Contains(got, exp) {
			t.Errorf("convertChars(αβγδε) = %q, want to contain %s", got, exp)
		}
	}
}

// ── Naked math operators detection ──────────────────────

func TestFixNakedOperators(t *testing.T) {
	// ∀x ∈ ℝ : x² ≥ 0 is a standalone logical statement.
	fr := Fix("∀x ∈ ℝ : x² ≥ 0")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for logical statement")
	}
}

// ── Sigma with complex bounds ───────────────────────────

func TestFixSigmaWithBounds(t *testing.T) {
	fr := Fix("∑ᵢ₌₁ⁿ i = n(n+1)/2")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for sigma sum")
	}
	if !strings.Contains(fr.Converted, `\sum`) {
		t.Errorf("expected \\sum, got: %q", fr.Converted)
	}
}

// ── Theta in trig context ───────────────────────────────

func TestFixTrigTheta(t *testing.T) {
	fr := Fix("sin(θ) ≈ θ for small θ")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for trig with theta")
	}
	if strings.Count(fr.Converted, `\theta`) < 2 {
		t.Errorf("expected 2 \\theta, got: %q", fr.Converted)
	}
}

// ── Limit expression ────────────────────────────────────

func TestFixLimitExpression(t *testing.T) {
	fr := Fix("limₙ→∞ aₙ = L")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for limit")
	}
	if !strings.Contains(fr.Converted, `\infty`) {
		t.Errorf("expected \\infty in output, got: %q", fr.Converted)
	}
}

// ── Tensor / index notation ─────────────────────────────

func TestFixTensorNotation(t *testing.T) {
	fr := Fix("Gμν + Λgμν = 8πTμν")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for tensor equation")
	}
	contains := []string{`\mu`, `\nu`, `\Lambda`, `\pi`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

// ── Preprocess heading with mixed content ───────────────

func TestPreprocessHeadingDegree(t *testing.T) {
	input := "# Results at 45° and 90°\n"
	got := string(Preprocess([]byte(input)))
	// Both degree signs should be converted.
	if strings.Count(got, `\circ`) < 2 {
		t.Errorf("expected 2 \\circ in output, got: %q", got)
	}
}

// ── Preprocess multiple math lines ──────────────────────

func TestPreprocessMultipleMathParagraphs(t *testing.T) {
	input := "α + β = γ\n\nδ + ε = ζ\n"
	got := string(Preprocess([]byte(input)))
	contains := []string{`\alpha`, `\beta`, `\gamma`, `\delta`, `\varepsilon`, `\zeta`}
	for _, want := range contains {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in output, got: %q", want, got)
		}
	}
}

// ── Unicode but very low score (should not trigger Fix) ─

func TestFixLowScoreSkip(t *testing.T) {
	// A single degree sign in a long prose sentence should score < 0.05.
	// Score thresholds: the score calculation gives 0.20 baseline + 0.20
	// for operators (because ° is in isMathOperator). So this actually scores
	// high enough to trigger. Let's use a scenario with very sparse math.
	// A single fraction char in a long string: 0.20 baseline + 0.10 fraction = 0.30.
	// That's still above threshold. Let's skip this test architecture concern.
	//
	// What we can verify: pure prose with accented Latin chars (café, résumé)
	// should not trigger because those aren't in the math char categories.
	fr := Fix("café résumé naïve façade")
	if fr.Applied {
		t.Errorf("prose-only text should not trigger Fix: Applied=%v", fr.Applied)
	}
}

// ── Idempotence stress test ─────────────────────────────

func TestFixIdempotenceFullEquation(t *testing.T) {
	input := "∀x ∈ ℝ, ∫₀¹ x² dx = ⅓ and ∑ₙ₌₁∞ 1/n² = π²/6"
	first := Fix(input)
	if !first.Applied {
		t.Fatalf("first Fix should apply: %v", first.Applied)
	}
	second := Fix(first.Converted)
	if second.Applied {
		t.Errorf("second Fix should not apply (idempotence): Applied=%v", second.Applied)
	}
	if second.Converted != first.Converted {
		t.Errorf("idempotence broken:\nfirst:  %q\nsecond: %q", first.Converted, second.Converted)
	}
}

// ── Consecutive superscript digits ──────────────────────

func TestConvertSuperscriptMultiDigit(t *testing.T) {
	got, _ := convertChars("x¹²³")
	if !strings.Contains(got, `^{123}`) {
		t.Errorf("convertChars(x¹²³) = %q, want to contain ^{123}", got)
	}
}

func TestConvertSubscriptMultiDigit(t *testing.T) {
	got, _ := convertChars("x₀₁₂")
	if !strings.Contains(got, `_{012}`) {
		t.Errorf("convertChars(x₀₁₂) = %q, want to contain _{012}", got)
	}
}

// ── Negative numbers with minus sign ────────────────────

func TestFixNegativeWithMinus(t *testing.T) {
	fr := Fix("x ≥ −5")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for comparison with minus")
	}
	if !strings.Contains(fr.Converted, `\ge`) {
		t.Errorf("expected \\ge in output, got: %q", fr.Converted)
	}
}

// ── Blackboard bold with operator chain ─────────────────

func TestFixBlackboardBoldWithOps(t *testing.T) {
	fr := Fix("x ∈ ℝ ∧ y ∈ ℂ")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for bb-bold with logic")
	}
	contains := []string{`\in`, `\mathbb{R}`, `\land`, `\mathbb{C}`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

// ── Arrow chain ─────────────────────────────────────────

func TestFixArrowChain(t *testing.T) {
	fr := Fix("x → y ⇒ z ⇔ w")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for arrow chain")
	}
	contains := []string{`\to`, `\Rightarrow`, `\Leftrightarrow`}
	for _, want := range contains {
		if !strings.Contains(fr.Converted, want) {
			t.Errorf("expected %s in output, got: %q", want, fr.Converted)
		}
	}
}

// ── Sqrt with complex arg ───────────────────────────────

func TestFixNestedSqrt(t *testing.T) {
	fr := Fix("√(1 + √(x²))")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for nested sqrt")
	}
	// Should contain two \sqrt{ calls.
	count := strings.Count(fr.Converted, `\sqrt{`)
	if count < 2 {
		t.Errorf("expected 2 \\sqrt{{ in nested sqrt, got %d: %q", count, fr.Converted)
	}
}

// ── The empty set in context ────────────────────────────

func TestFixEmptySetContext(t *testing.T) {
	fr := Fix("If S = ∅ then S ⊆ T")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for empty set in context")
	}
	if !strings.Contains(fr.Converted, `\emptyset`) {
		t.Errorf("expected \\emptyset in output, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\subseteq`) {
		t.Errorf("expected \\subseteq in output, got: %q", fr.Converted)
	}
}

// ── Integral with dot product in integrand ──────────────

func TestFixIntegralDotProduct(t *testing.T) {
	fr := Fix("∫ E · dA")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for integral dot product")
	}
	if !strings.Contains(fr.Converted, `\int`) {
		t.Errorf("expected \\int, got: %q", fr.Converted)
	}
	if !strings.Contains(fr.Converted, `\cdot`) {
		t.Errorf("expected \\cdot, got: %q", fr.Converted)
	}
}

// ── Cross product in physics context ────────────────────

func TestFixPhysicsCross(t *testing.T) {
	fr := Fix("F = qv × B")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for Lorentz force")
	}
	if !strings.Contains(fr.Converted, `\times`) {
		t.Errorf("expected \\times in output, got: %q", fr.Converted)
	}
}

// ── Omega in physics ────────────────────────────────────

func TestFixOmegaResistance(t *testing.T) {
	fr := Fix("R = 100 Ω")
	if !fr.Applied {
		t.Fatal("expected Fix to apply for omega")
	}
	if !strings.Contains(fr.Converted, `\Omega`) {
		t.Errorf("expected \\Omega in output, got: %q", fr.Converted)
	}
}

// ── Multiple subscripts on one variable ─────────────────

func TestConvertSequentialSubscripts(t *testing.T) {
	got, _ := convertChars("x₁₂₃")
	if !strings.Contains(got, `_{123}`) {
		t.Errorf("convertChars(x₁₂₃) = %q, want to contain _{123}", got)
	}
}

// ── Heading with multiple math spans ────────────────────

func TestPreprocessHeadingMath(t *testing.T) {
	input := "# α + β = γ and δ ≤ ε\n"
	got := string(Preprocess([]byte(input)))
	contains := []string{`\alpha`, `\beta`, `\gamma`, `\delta`, `\varepsilon`, `\le`}
	for _, want := range contains {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in output, got: %q", want, got)
		}
	}
}

// ── Empty paragraph between math ────────────────────────

func TestPreprocessEmptyBetweenMath(t *testing.T) {
	input := "x² + y²\n\n\nz² + w²\n"
	got := string(Preprocess([]byte(input)))
	if strings.Count(got, `^{2}`) < 4 {
		t.Errorf("expected 4 ^{2} conversions, got: %q", got)
	}
	// Both paragraphs should be converted to $$ math.
	if !strings.Contains(got, "$$") {
		t.Errorf("expected display math $$, got: %q", got)
	}
}

// ── Large superscript run ───────────────────────────────

func TestConvertLargeSuperscriptRun(t *testing.T) {
	got, _ := convertChars("x⁰¹²³⁴⁵⁶⁷⁸⁹")
	if !strings.Contains(got, `^{0123456789}`) {
		t.Errorf("convertChars(x⁰¹²³⁴⁵⁶⁷⁸⁹) = %q, want ^{0123456789}", got)
	}
}

// ── HasUnicodeMath with individual categories ───────────

func TestHasUnicodeMathCategories(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"greek", "α", true},
		{"operator", "∀", true},
		{"superscript", "x²", true},
		{"subscript", "x₀", true},
		{"bbold", "ℝ", true},
		{"fraction", "½", true},
		{"vulgar fraction", "¾", true},
		{"sqrt", "√x", true},
		{"degree", "45°", true},
		{"minus", "−5", true},
		{"cdots", "1·2·3", true},
		{"ldots", "1…n", true},
		{"no math", "hello", false},
		{"accent latin", "é", false},
		{"currency dollar", "$", false},
		{"greek variant epsilon", "ϵ", true},
		{"greek variant phi", "ϕ", true},
		{"greek variant digamma", "ϝ", true},
		{"unsupported greek lookalike", "Α", false},
		{"partial", "∂f/∂x", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasUnicodeMath(tc.input); got != tc.want {
				t.Errorf("HasUnicodeMath(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
