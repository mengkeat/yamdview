package mathfix

import (
	"regexp"
	"strings"
)

var (
	equationRelationRE  = regexp.MustCompile(`[=<>≤≥≠≈≡∼∝]`)
	asciiDerivativeRE   = regexp.MustCompile(`\b[dD]\s*([A-Za-z])\s*/\s*[dD]\s*([A-Za-z])\b`)
	partialDerivativeRE = regexp.MustCompile(`∂\s*([A-Za-z])\s*/\s*∂\s*([A-Za-z])`)
	absoluteValueRE     = regexp.MustCompile(`\|([^|\n]+)\|`)
	identifierSubRE     = regexp.MustCompile(`\b([A-Za-z])_([A-Za-z0-9]+)\b`)
	camelSubRE          = regexp.MustCompile(`\b([a-z])([A-Z])\b`)
	numericFractionRE   = regexp.MustCompile(`\b([0-9]+)\s*/\s*([0-9]+)\b`)
	axisWordRE          = regexp.MustCompile(`\baxis\b`)
)

// fixProbableEquationBlock converts a short plaintext block that looks like a
// displayed equation into TeX display math. It is intentionally stricter than
// Unicode paragraph fixing: callers use it for ```text fences, where code and
// command output must usually remain untouched.
func fixProbableEquationBlock(text string) (string, bool) {
	if !looksLikeEquationBlock(text) {
		return text, false
	}

	lines := strings.Split(text, "\n")
	converted := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			converted = append(converted, "")
			continue
		}
		converted = append(converted, equationLineToTeX(trimmed))
	}

	return "$$\n" + strings.Join(converted, "\n") + "\n$$", true
}

func looksLikeEquationBlock(text string) bool {
	if strings.TrimSpace(text) == "" || hasTeXDelimiters(text) {
		return false
	}

	lines := strings.Split(text, "\n")
	nonEmpty := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		nonEmpty++
		if !looksLikeEquationLine(trimmed) {
			return false
		}
	}

	// Keep the first pass conservative: this catches short equation snippets
	// without turning large text/code listings into math blocks.
	return nonEmpty > 0 && nonEmpty <= 4
}

func looksLikeEquationLine(line string) bool {
	if len(line) < 3 || strings.Contains(line, "`") || !equationRelationRE.MatchString(line) {
		return false
	}

	score := 0
	if equationRelationRE.MatchString(line) {
		score += 2
	}
	if asciiDerivativeRE.MatchString(line) || partialDerivativeRE.MatchString(line) {
		score += 3
	}
	if HasUnicodeMath(line) {
		score += 2
	}
	if strings.Contains(line, "_") {
		score++
	}
	if strings.Contains(line, "|") {
		score++
	}
	if strings.ContainsAny(line, "+-*/()[]") || strings.ContainsAny(line, "−×·") {
		score++
	}

	return score >= 4
}

func equationLineToTeX(line string) string {
	tex := partialDerivativeRE.ReplaceAllString(line, `\frac{\partial $1}{\partial $2}`)
	tex = asciiDerivativeRE.ReplaceAllString(tex, `\frac{d$1}{d$2}`)

	converted, _ := convertChars(tex)
	converted = normalizeEquationTeX(converted)
	return converted
}

func normalizeEquationTeX(tex string) string {
	tex = numericFractionRE.ReplaceAllString(tex, `\frac{$1}{$2}`)
	tex = absoluteValueRE.ReplaceAllStringFunc(tex, func(match string) string {
		inner := strings.TrimSpace(match[1 : len(match)-1])
		return `\lvert ` + inner + `\rvert`
	})
	tex = identifierSubRE.ReplaceAllStringFunc(tex, func(match string) string {
		parts := identifierSubRE.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		if len(parts[2]) > 1 && allASCIIUpper(parts[2]) {
			return parts[1] + `_{\mathrm{` + parts[2] + `}}`
		}
		return parts[1] + `_{` + parts[2] + `}`
	})
	tex = camelSubRE.ReplaceAllString(tex, `${1}_{${2}}`)
	tex = axisWordRE.ReplaceAllString(tex, `\mathrm{axis}`)
	return strings.TrimSpace(tex)
}

func allASCIIUpper(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return s != ""
}
