package mathfix

import (
	"strings"
)

// ── Character mapping tables ─────────────────────────────

// charMap maps Unicode characters to their TeX equivalents.
// Commands do NOT include trailing spaces; spacing is handled by the
// converter which adds a space after commands only when the next
// character is an ASCII letter (to prevent \alphax instead of \alpha x).
var charMap = map[rune]string{
	// Greek lowercase
	'α': `\alpha`, 'β': `\beta`, 'γ': `\gamma`, 'δ': `\delta`,
	'ε': `\varepsilon`, 'ζ': `\zeta`, 'η': `\eta`, 'θ': `\theta`,
	'ι': `\iota`, 'κ': `\kappa`, 'λ': `\lambda`, 'μ': `\mu`,
	'ν': `\nu`, 'ξ': `\xi`, 'π': `\pi`, 'ρ': `\rho`,
	'σ': `\sigma`, 'τ': `\tau`, 'υ': `\upsilon`, 'φ': `\phi`,
	'χ': `\chi`, 'ψ': `\psi`, 'ω': `\omega`,

	// Greek uppercase (only those that differ from Latin shapes)
	'Γ': `\Gamma`, 'Δ': `\Delta`, 'Θ': `\Theta`, 'Λ': `\Lambda`,
	'Ξ': `\Xi`, 'Π': `\Pi`, 'Σ': `\Sigma`, 'Υ': `\Upsilon`,
	'Φ': `\Phi`, 'Ψ': `\Psi`, 'Ω': `\Omega`,

	// Greek variants
	'ϵ': `\epsilon`, 'ϑ': `\vartheta`, 'ϕ': `\varphi`, 'ϱ': `\varrho`,
	'ϰ': `\varkappa`, 'ϖ': `\varpi`, 'ℓ': `\ell`,

	// Math operators
	'∀': `\forall`, '∃': `\exists`, '∈': `\in`, '∉': `\notin`,
	'∑': `\sum`, '∏': `\prod`, '∫': `\int`, '∮': `\oint`,
	'∂': `\partial`, '∇': `\nabla`, '∞': `\infty`,
	'≤': `\le`, '≥': `\ge`, '≠': `\neq`, '≈': `\approx`,
	'∝': `\propto`, '∼': `\sim`, '≅': `\cong`, '≡': `\equiv`,
	'±': `\pm`, '∓': `\mp`, '×': `\times`, '÷': `\div`,
	'→': `\to`, '↦': `\mapsto`, '⇒': `\Rightarrow`, '⇔': `\Leftrightarrow`,
	'←': `\leftarrow`, '↔': `\leftrightarrow`, '↓': `\downarrow`, '↑': `\uparrow`,
	'⊂': `\subset`, '⊃': `\supset`, '⊆': `\subseteq`, '⊇': `\supseteq`,
	'⊄': `\not\subset`, '⊅': `\not\supset`,
	'∪': `\cup`, '∩': `\cap`, '∅': `\emptyset`,
	'¬': `\neg`, '∧': `\land`, '∨': `\lor`,
	'⊕': `\oplus`, '⊗': `\otimes`, '⊥': `\perp`, '∠': `\angle`, '‖': `\parallel`,
	'·': `\cdot`, '…': `\ldots`,
	'∘': `\circ`, '⋆': `\star`,

	// Blackboard bold
	'ℝ': `\mathbb{R}`, 'ℕ': `\mathbb{N}`, 'ℤ': `\mathbb{Z}`,
	'ℚ': `\mathbb{Q}`, 'ℂ': `\mathbb{C}`, 'ℙ': `\mathbb{P}`,
	'𝔽': `\mathbb{F}`, 'ℍ': `\mathbb{H}`,

	// Fractions
	'½': `\frac{1}{2}`, '⅓': `\frac{1}{3}`, '⅔': `\frac{2}{3}`,
	'¼': `\frac{1}{4}`, '¾': `\frac{3}{4}`,
	'⅕': `\frac{1}{5}`, '⅖': `\frac{2}{5}`, '⅗': `\frac{3}{5}`, '⅘': `\frac{4}{5}`,
	'⅙': `\frac{1}{6}`, '⅚': `\frac{5}{6}`,
	'⅛': `\frac{1}{8}`, '⅜': `\frac{3}{8}`, '⅝': `\frac{5}{8}`, '⅞': `\frac{7}{8}`,
}

// superMap maps superscript characters to their base equivalents.
var superMap = map[rune]rune{
	'⁰': '0', '¹': '1', '²': '2', '³': '3', '⁴': '4',
	'⁵': '5', '⁶': '6', '⁷': '7', '⁸': '8', '⁹': '9',
	'⁺': '+', '⁻': '-', '⁼': '=', '⁽': '(', '⁾': ')',
	'ⁿ': 'n', 'ⁱ': 'i',
}

// subMap maps subscript characters to their base equivalents.
var subMap = map[rune]rune{
	'₀': '0', '₁': '1', '₂': '2', '₃': '3', '₄': '4',
	'₅': '5', '₆': '6', '₇': '7', '₈': '8', '₉': '9',
	'₊': '+', '₋': '-', '₌': '=', '₍': '(', '₎': ')',
	'ₐ': 'a', 'ₑ': 'e', 'ₒ': 'o', 'ₓ': 'x', 'ₔ': 'ə',
	'ₕ': 'h', 'ₖ': 'k', 'ₗ': 'l', 'ₘ': 'm', 'ₙ': 'n',
	'ₛ': 's', 'ₜ': 't',
	'ᵢ': 'i', 'ᵣ': 'r', 'ᵤ': 'u', 'ᵥ': 'v',
}

// ── Conversion logic ────────────────────────────────────

// convertChars converts Unicode math characters in text to TeX equivalents.
// It handles superscript/subscript runs and the √ symbol specially.
// Returns the converted text and any diagnostics for ambiguous conversions.
func convertChars(text string) (string, []Diagnostic) {
	var buf strings.Builder
	var diags []Diagnostic
	runes := []rune(text)
	i := 0

	for i < len(runes) {
		r := runes[i]

		// Superscript run: collect consecutive superscript chars.
		if _, ok := superMap[r]; ok && isSuperscript(r) {
			run, runStr := collectRun(runes[i:], superMap)
			buf.WriteString("^{")
			buf.WriteString(runStr)
			buf.WriteString("}")
			i += len(run)
			continue
		}

		// Subscript run: collect consecutive subscript chars.
		if _, ok := subMap[r]; ok && isSubscript(r) {
			run, runStr := collectRun(runes[i:], subMap)
			buf.WriteString("_{")
			buf.WriteString(runStr)
			buf.WriteString("}")
			i += len(run)
			continue
		}

		// Square root: √ followed by argument.
		if r == '√' {
			handled, advance := handleSqrt(runes[i:], &buf, &diags)
			if handled {
				i += advance
				continue
			}
		}

		// Character map lookup.
		if tex, ok := charMap[r]; ok {
			buf.WriteString(tex)
			// Add a space after the command if the next character is an
			// ASCII letter that would otherwise extend the command name
			// (e.g. \forall x → needs space, but \forall, → no space needed).
			if i+1 < len(runes) {
				next := runes[i+1]
				if isASCIIAlpha(next) {
					buf.WriteByte(' ')
				}
			}
			i++
			continue
		}

		// Pass through unchanged.
		buf.WriteRune(r)
		i++
	}

	return strings.TrimSpace(buf.String()), diags
}

// collectRun collects a consecutive run of characters present in the given
// mapping, converts each to its base character, and returns the run and the
// converted string.
func collectRun(runes []rune, m map[rune]rune) ([]rune, string) {
	var run []rune
	var base strings.Builder
	for _, r := range runes {
		if b, ok := m[r]; ok {
			run = append(run, r)
			base.WriteRune(b)
		} else {
			break
		}
	}
	return run, base.String()
}

// handleSqrt handles the √ (square root) symbol.
// Returns (true, advance) if handled, (false, 0) otherwise.
func handleSqrt(runes []rune, buf *strings.Builder, diags *[]Diagnostic) (bool, int) {
	if len(runes) < 2 {
		buf.WriteString("\\sqrt{}")
		*diags = append(*diags, Diagnostic{
			Severity: "warning",
			Code:     "math.sqrt_no_arg",
			Message:  "square root symbol without argument",
		})
		return true, 1
	}

	next := runes[1]

	// √(x²+y²) → \sqrt{x^{2}+y^{2}}
	if next == '(' {
		depth := 1
		j := 2
		for j < len(runes) && depth > 0 {
			if runes[j] == '(' {
				depth++
			} else if runes[j] == ')' {
				depth--
			}
			if depth > 0 {
				j++
			}
		}
		if depth == 0 {
			arg := string(runes[2:j])
			argTeX, _ := convertChars(arg)
			buf.WriteString("\\sqrt{")
			buf.WriteString(argTeX)
			buf.WriteString("}")
			return true, j + 1 // skip √ and ( and content and )
		}
		// Unmatched parenthesis.
		buf.WriteString("\\sqrt{}")
		*diags = append(*diags, Diagnostic{
			Severity: "warning",
			Code:     "math.sqrt_unmatched",
			Message:  "square root with unmatched parenthesis",
		})
		return true, 1
	}

	// √x or √2 → \sqrt{x} or \sqrt{2}
	if isAlphaOrDigit(next) && !isSuperscript(next) && !isSubscript(next) {
		buf.WriteString("\\sqrt{")
		buf.WriteRune(next)
		buf.WriteString("}")
		return true, 2
	}

	// √ followed by something complex or a space — just emit \sqrt{}.
	buf.WriteString("\\sqrt{}")
	*diags = append(*diags, Diagnostic{
		Severity: "warning",
		Code:     "math.sqrt_no_arg",
		Message:  "square root symbol without clear argument",
	})
	return true, 1
}

func isAlphaOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
