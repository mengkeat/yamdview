package mathfix

import (
	"strings"

	"github.com/mengkeat/yamdview/internal/mathchars"
)

// ── Character mapping tables ─────────────────────────────

// The authoritative character tables live in the mathchars package, shared
// with the table repair pipeline so the two cannot drift apart.
var charMap = mathchars.CharMap
var superMap = mathchars.SuperMap
var subMap = mathchars.SubMap

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
		if _, ok := superMap[r]; ok && mathchars.IsSuperscript(r) {
			run, runStr := collectRun(runes[i:], superMap)
			buf.WriteString("^{")
			buf.WriteString(runStr)
			buf.WriteString("}")
			i += len(run)
			continue
		}

		// Subscript run: collect consecutive subscript chars.
		if _, ok := subMap[r]; ok && mathchars.IsSubscript(r) {
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
	if isAlphaOrDigit(next) && !mathchars.IsSuperscript(next) && !mathchars.IsSubscript(next) {
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
	return isASCIIAlpha(r) || (r >= '0' && r <= '9')
}
