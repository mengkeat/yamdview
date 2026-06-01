package mathfix

import (
	"strings"
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

		if !isUnicodeMathChar(r) {
			continue
		}

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
	if r == '√' {
		return true
	}
	if _, ok := charMap[r]; ok {
		return true
	}
	if _, ok := superMap[r]; ok {
		return true
	}
	if _, ok := subMap[r]; ok {
		return true
	}
	return false
}

// isMathOperator reports whether r is a Unicode math operator or symbol.
func isMathOperator(r rune) bool {
	if r == '√' {
		return true
	}
	return isUnicodeMathChar(r) && !isGreekLetter(r) && !isSuperscript(r) &&
		!isSubscript(r) && !isBlackboardBold(r) && !isMathFraction(r)
}

// isGreekLetter reports whether r is a Greek letter used in math.
func isGreekLetter(r rune) bool {
	_, ok := charMap[r]
	return ok && (unicode.In(r, unicode.Greek) || r == 'ℓ')
}

// isSuperscript reports whether r is a Unicode superscript character.
func isSuperscript(r rune) bool {
	_, ok := superMap[r]
	return ok
}

// isSubscript reports whether r is a Unicode subscript character.
func isSubscript(r rune) bool {
	_, ok := subMap[r]
	return ok
}

// isBlackboardBold reports whether r is a Unicode blackboard bold character.
func isBlackboardBold(r rune) bool {
	tex, ok := charMap[r]
	return ok && strings.HasPrefix(tex, `\mathbb{`)
}

// isMathFraction reports whether r is a Unicode fraction character.
func isMathFraction(r rune) bool {
	tex, ok := charMap[r]
	return ok && strings.HasPrefix(tex, `\frac{`)
}
