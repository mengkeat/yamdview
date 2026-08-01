// Package mathchars owns the Unicode math character tables and the
// classification predicates derived from them. Both the Unicode math
// conversion pipeline (mathfix) and the table repair pipeline (tablefix)
// use these tables so the two packages cannot drift apart.
package mathchars

import (
	"strings"
	"unicode"
)

// ── Character mapping tables ─────────────────────────────

// CharMap maps Unicode characters to their TeX equivalents.
// Commands do NOT include trailing spaces; spacing is handled by the
// converter which adds a space after commands only when the next
// character is an ASCII letter (to prevent \alphax instead of \alpha x).
var CharMap = map[rune]string{
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
	'ϰ': `\varkappa`, 'ϖ': `\varpi`, 'ϝ': `\digamma`, 'ℓ': `\ell`,

	// Math operators
	'∀': `\forall`, '∃': `\exists`, '∈': `\in`, '∉': `\notin`,
	'∑': `\sum`, '∏': `\prod`, '∫': `\int`, '∮': `\oint`,
	'∂': `\partial`, '∇': `\nabla`, '∞': `\infty`,
	'≤': `\le`, '≥': `\ge`, '≠': `\neq`, '≈': `\approx`,
	'∝': `\propto`, '∼': `\sim`, '≅': `\cong`, '≡': `\equiv`,
	// Operators and symbols
	'°': `{}^\circ`,
	'−': `-`,
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

// SuperMap maps superscript characters to their base equivalents.
var SuperMap = map[rune]rune{
	'⁰': '0', '¹': '1', '²': '2', '³': '3', '⁴': '4',
	'⁵': '5', '⁶': '6', '⁷': '7', '⁸': '8', '⁹': '9',
	'⁺': '+', '⁻': '-', '⁼': '=', '⁽': '(', '⁾': ')',
	'ⁿ': 'n', 'ⁱ': 'i',
}

// SubMap maps subscript characters to their base equivalents.
var SubMap = map[rune]rune{
	'₀': '0', '₁': '1', '₂': '2', '₃': '3', '₄': '4',
	'₅': '5', '₆': '6', '₇': '7', '₈': '8', '₉': '9',
	'₊': '+', '₋': '-', '₌': '=', '₍': '(', '₎': ')',
	'ₐ': 'a', 'ₑ': 'e', 'ₒ': 'o', 'ₓ': 'x', 'ₔ': 'ə',
	'ₕ': 'h', 'ₖ': 'k', 'ₗ': 'l', 'ₘ': 'm', 'ₙ': 'n',
	'ₛ': 's', 'ₜ': 't',
	'ᵢ': 'i', 'ᵣ': 'r', 'ᵤ': 'u', 'ᵥ': 'v',
}

// ── Classification predicates ────────────────────────────

// IsUnicodeMathChar reports whether r is any Unicode math character that has
// a TeX conversion (operators, Greek letters, superscripts, subscripts,
// blackboard bold, math fractions, or the square root symbol).
func IsUnicodeMathChar(r rune) bool {
	if r == '√' {
		return true
	}
	if _, ok := CharMap[r]; ok {
		return true
	}
	if _, ok := SuperMap[r]; ok {
		return true
	}
	if _, ok := SubMap[r]; ok {
		return true
	}
	return false
}

// IsMathOperator reports whether r is a Unicode math operator or symbol.
func IsMathOperator(r rune) bool {
	if r == '√' {
		return true
	}
	return IsUnicodeMathChar(r) && !IsGreekLetter(r) && !IsSuperscript(r) &&
		!IsSubscript(r) && !IsBlackboardBold(r) && !IsMathFraction(r)
}

// IsGreekLetter reports whether r is a Greek letter used in math.
func IsGreekLetter(r rune) bool {
	_, ok := CharMap[r]
	return ok && (unicode.In(r, unicode.Greek) || r == 'ℓ')
}

// IsSuperscript reports whether r is a Unicode superscript character.
func IsSuperscript(r rune) bool {
	_, ok := SuperMap[r]
	return ok
}

// IsSubscript reports whether r is a Unicode subscript character.
func IsSubscript(r rune) bool {
	_, ok := SubMap[r]
	return ok
}

// IsBlackboardBold reports whether r is a Unicode blackboard bold character.
func IsBlackboardBold(r rune) bool {
	tex, ok := CharMap[r]
	return ok && strings.HasPrefix(tex, `\mathbb{`)
}

// IsMathFraction reports whether r is a Unicode fraction character.
func IsMathFraction(r rune) bool {
	tex, ok := CharMap[r]
	return ok && strings.HasPrefix(tex, `\frac{`)
}
