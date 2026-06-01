package mathfix

import (
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
		name    string
		input   string
		contains []string
		omits   []string
	}{
		{
			name:     "greek letters",
			input:    "α + β = γ",
			contains: []string{`\alpha`, `\beta`, `\gamma`},
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
			name:   "pass through non-math",
			input:  "hello world 123",
			omits:  []string{`\`},
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
		name       string
		input      string
		applied    bool
		contains   []string
		omits      []string
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
