package mathfix

import (
	"unicode"
)

// HasUnicodeMath reports whether text contains Unicode characters used in math
// notation that have TeX equivalents (operators, Greek letters, superscripts,
// subscripts, blackboard bold, or math fractions).
func HasUnicodeMath(text string) bool {
	for _, r := range text {
		if isUnicodeMathChar(r) {
			return true
		}
	}
	return false
}

// Score returns a confidence score in [0, 1] indicating how likely text
// contains Unicode math notation. Higher scores indicate stronger confidence.
func Score(text string) float64 {
	var ops, greek, super, sub, bbold, frac int
	var totalNonSpace int

	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		totalNonSpace++

		switch {
		case isMathOperator(r):
			ops++
		case isGreekLetter(r):
			greek++
		case isSuperscript(r):
			super++
		case isSubscript(r):
			sub++
		case isBlackboardBold(r):
			bbold++
		case isMathFraction(r):
			frac++
		}
	}

	if totalNonSpace == 0 {
		return 0
	}

	mathChars := ops + greek + super + sub + bbold + frac
	if mathChars == 0 {
		return 0
	}

	density := float64(mathChars) / float64(totalNonSpace)

	var score float64

	// Baseline: any Unicode math character is a positive signal.
	score = 0.20

	// Strong signals from specific categories.
	if ops > 0 {
		score += 0.20
	}
	if super+sub > 0 {
		score += 0.15
	}
	if greek > 0 {
		score += 0.10
	}
	if bbold > 0 {
		score += 0.10
	}
	if frac > 0 {
		score += 0.10
	}

	// Density bonuses: math-heavy text gets a confidence boost.
	if density >= 0.15 {
		score += 0.10
	}
	if density >= 0.40 {
		score += 0.15
	}

	if score > 1.0 {
		score = 1.0
	}

	return score
}

// isUnicodeMathChar reports whether r is any Unicode math character we can
// convert to TeX.
func isUnicodeMathChar(r rune) bool {
	return isMathOperator(r) || isGreekLetter(r) || isSuperscript(r) ||
		isSubscript(r) || isBlackboardBold(r) || isMathFraction(r)
}

// isMathOperator reports whether r is a Unicode math operator or symbol.
func isMathOperator(r rune) bool {
	switch r {
	case '∀', '∃', '∈', '∉', '∑', '∏', '∫', '∮',
		'∂', '∇', '∞',
		'≤', '≥', '≠', '≈', '∝', '∼', '≅', '≡',
		'±', '∓', '×', '÷',
		'→', '↦', '⇒', '⇔', '←', '↔', '↓', '↑',
		'⊂', '⊃', '⊆', '⊇', '⊄', '⊅',
		'∪', '∩', '∅',
		'¬', '∧', '∨',
		'⊕', '⊗', '⊥', '∠', '‖',
		'·', '…',
		'√',
		'∘', '⋆':
		return true
	default:
		return false
	}
}

// isGreekLetter reports whether r is a Greek letter used in math.
func isGreekLetter(r rune) bool {
	// Lowercase Greek: α-ω (U+03B1 to U+03C9) plus variants.
	if r >= 'α' && r <= 'ω' {
		return true
	}
	// Uppercase Greek: Α-Ω (U+0391 to U+03A9).
	if r >= 'Α' && r <= 'Ω' {
		return true
	}
	// Greek variants.
	switch r {
	case 'ϵ', 'ϑ', 'ϕ', 'ϱ', 'ϰ', 'ϖ', 'ϝ', 'ℓ':
		return true
	default:
		return false
	}
}

// isSuperscript reports whether r is a Unicode superscript character.
func isSuperscript(r rune) bool {
	// Superscript digits ⁰-⁹ (U+2070, U+00B9, U+00B2, U+00B3, U+2074-U+2079).
	switch r {
	case '⁰', '¹', '²', '³', '⁴', '⁵', '⁶', '⁷', '⁸', '⁹':
		return true
	case '⁺', '⁻', '⁼', '⁽', '⁾': // operators
		return true
	case 'ⁿ', 'ⁱ': // letter superscripts
		return true
	default:
		return false
	}
}

// isSubscript reports whether r is a Unicode subscript character.
func isSubscript(r rune) bool {
	// Subscript digits ₀-₉ (U+2080 to U+2089).
	switch r {
	case '₀', '₁', '₂', '₃', '₄', '₅', '₆', '₇', '₈', '₉':
		return true
	case '₊', '₋', '₌', '₍', '₎': // operators
		return true
	case 'ₐ', 'ₑ', 'ₒ', 'ₓ', 'ₔ': // letter subscripts
		return true
	case 'ₕ', 'ₖ', 'ₗ', 'ₘ', 'ₙ', 'ₛ', 'ₜ': // letter subscripts h-t
		return true
	case 'ᵢ', 'ᵣ', 'ᵤ', 'ᵥ': // Latin subscript letters (modified small caps)
		return true
	default:
		return false
	}
}

// isBlackboardBold reports whether r is a Unicode blackboard bold character.
func isBlackboardBold(r rune) bool {
	switch r {
	case 'ℝ', 'ℕ', 'ℤ', 'ℚ', 'ℂ', 'ℙ', '𝔽', 'ℍ':
		return true
	default:
		return false
	}
}

// isMathFraction reports whether r is a Unicode fraction character.
func isMathFraction(r rune) bool {
	switch r {
	case '½', '⅓', '⅔', '¼', '¾', '⅕', '⅖', '⅗', '⅘',
		'⅙', '⅚', '⅛', '⅜', '⅝', '⅞':
		return true
	default:
		return false
	}
}
