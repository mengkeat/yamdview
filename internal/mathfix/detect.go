package mathfix

import (
	"unicode"

	"github.com/mengkeat/yamdview/internal/mathchars"
)

// HasUnicodeMath reports whether text contains Unicode characters used in math
// notation that have TeX equivalents (operators, Greek letters, superscripts,
// subscripts, blackboard bold, or math fractions).
func HasUnicodeMath(text string) bool {
	for _, r := range text {
		if mathchars.IsUnicodeMathChar(r) {
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

		if !mathchars.IsUnicodeMathChar(r) {
			continue
		}

		switch {
		case mathchars.IsMathOperator(r):
			ops++
		case mathchars.IsGreekLetter(r):
			greek++
		case mathchars.IsSuperscript(r):
			super++
		case mathchars.IsSubscript(r):
			sub++
		case mathchars.IsBlackboardBold(r):
			bbold++
		case mathchars.IsMathFraction(r):
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

// Character classification is owned by the mathchars package, shared with
// the table repair pipeline.
