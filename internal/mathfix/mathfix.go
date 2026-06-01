package mathfix

import (
	"strings"
)

// Diagnostic represents an issue found during math conversion.
type Diagnostic struct {
	Severity string // "info", "warning", "error"
	Code     string // e.g. "math.unresolved", "math.sqrt_no_arg"
	Message  string
}

// FixResult holds the result of a Unicode math conversion.
type FixResult struct {
	Original    string
	Converted   string
	Confidence  float64
	Applied     bool
	Diagnostics []Diagnostic
}

// mathSpan represents a range of rune positions [start, end) identified as math.
type mathSpan struct {
	start int // rune index
	end   int // rune index
}

// Fix detects and converts Unicode math in a paragraph of text.
// It returns a FixResult with the converted text wrapped in TeX delimiters
// ($...$ for inline, $$...$$ for display) suitable for goldmark math parsing.
//
// The original text is not modified; the converted text is meant for
// render-only use.
func Fix(text string) FixResult {
	if !HasUnicodeMath(text) {
		return FixResult{
			Original:   text,
			Converted:  text,
			Confidence: 0,
			Applied:    false,
		}
	}

	score := Score(text)
	if score < 0.05 {
		return FixResult{
			Original:   text,
			Converted:  text,
			Confidence: score,
			Applied:    false,
		}
	}

	// Find inline code ranges to exclude (byte positions).
	codeRanges := findInlineCodeRanges(text)

	// Find math spans as rune indices.
	spans := findMathSpans(text, codeRanges)

	if len(spans) == 0 {
		return FixResult{
			Original:   text,
			Converted:  text,
			Confidence: score,
			Applied:    false,
		}
	}

	// Build result by slicing at rune boundaries.
	runes := []rune(text)
	var buf strings.Builder
	lastEnd := 0
	var allDiags []Diagnostic

	for _, span := range spans {
		// Copy prose before this span (rune slicing).
		if span.start > lastEnd {
			buf.WriteString(string(runes[lastEnd:span.start]))
		}

		// Convert the span text.
		spanText := string(runes[span.start:span.end])
		tex, diags := convertChars(spanText)
		allDiags = append(allDiags, diags...)

		// Determine delimiter style.
		// Use display math $$ if the span covers most of the text and is
		// the dominant content.
		spanRatio := float64(span.end-span.start) / float64(len(runes))
		if spanRatio > 0.6 && len(spans) == 1 {
			buf.WriteString("$$")
			buf.WriteString(strings.TrimSpace(tex))
			buf.WriteString("$$")
		} else {
			buf.WriteString("$")
			buf.WriteString(strings.TrimSpace(tex))
			buf.WriteString("$")
		}

		lastEnd = span.end
	}

	// Copy remaining prose.
	if lastEnd < len(runes) {
		buf.WriteString(string(runes[lastEnd:]))
	}

	return FixResult{
		Original:    text,
		Converted:   buf.String(),
		Confidence:  score,
		Applied:     true,
		Diagnostics: allDiags,
	}
}

// ── Inline code range detection ─────────────────────────

// codeRange represents a [start, end) range of byte positions inside inline
// code (backtick) spans.
type codeRange struct {
	start int
	end   int
}

func findInlineCodeRanges(text string) []codeRange {
	var ranges []codeRange
	i := 0
	for i < len(text) {
		if text[i] == '`' {
			start := i
			j := i + 1
			for j < len(text) && text[j] != '`' {
				j++
			}
			if j < len(text) {
				ranges = append(ranges, codeRange{start: start, end: j + 1})
				i = j + 1
			} else {
				i++
			}
		} else {
			i++
		}
	}
	return ranges
}

// inCodeRangeByte reports whether the given byte position is inside an inline
// code range.
func inCodeRangeByte(pos int, ranges []codeRange) bool {
	for _, r := range ranges {
		if pos >= r.start && pos < r.end {
			return true
		}
	}
	return false
}

// ── Math span detection ─────────────────────────────────

// findMathSpans identifies contiguous ranges of text (as rune indices) that
// contain Unicode math notation. It starts from Unicode math character
// positions and extends through adjacent math-relevant characters (digits,
// single-letter variables, operators, parentheses), stopping at English words
// of 3+ letters.
func findMathSpans(text string, codeRanges []codeRange) []mathSpan {
	runes := []rune(text)
	n := len(runes)

	// Build a set of rune positions that are inside inline code.
	// We need to map byte-based codeRanges to rune positions.
	codeRuneSet := make(map[int]bool)
	bytePos := 0
	for runeIdx, r := range text {
		if inCodeRangeByte(bytePos, codeRanges) {
			codeRuneSet[runeIdx] = true
		}
		bytePos += len(string(r))
	}

	// Find seed positions: Unicode math chars not inside code spans.
	var seeds []int
	for i, r := range runes {
		if isUnicodeMathChar(r) && !codeRuneSet[i] {
			seeds = append(seeds, i)
		}
	}

	if len(seeds) == 0 {
		return nil
	}

	// For each seed, extend left and right through math-relevant chars.
	type span struct{ start, end int }
	var rawSpans []span

	for _, seed := range seeds {
		s := seed
		e := seed + 1

		// Extend left.
		for s > 0 {
			if codeRuneSet[s-1] {
				break
			}
			r := runes[s-1]
			if canExtendInto(r, runes, s-1) {
				s--
			} else {
				break
			}
		}

		// Extend right.
		for e < n {
			if codeRuneSet[e] {
				break
			}
			r := runes[e]
			if canExtendInto(r, runes, e, n) {
				e++
			} else {
				break
			}
		}

		rawSpans = append(rawSpans, span{s, e})
	}

	// Merge overlapping or adjacent spans.
	merged := make([]span, 0, len(rawSpans))
	merged = append(merged, rawSpans[0])
	for _, s := range rawSpans[1:] {
		last := &merged[len(merged)-1]
		if s.start <= last.end {
			if s.end > last.end {
				last.end = s.end
			}
		} else {
			merged = append(merged, s)
		}
	}

	// Trim leading/trailing spaces from spans.
	for i := range merged {
		for merged[i].end > merged[i].start && runes[merged[i].end-1] == ' ' {
			merged[i].end--
		}
		for merged[i].start < merged[i].end && runes[merged[i].start] == ' ' {
			merged[i].start++
		}
	}

	// Convert to mathSpan.
	result := make([]mathSpan, len(merged))
	for i, s := range merged {
		result[i] = mathSpan{start: s.start, end: s.end}
	}

	return result
}

// canExtendInto reports whether the character at position pos can be included
// in a math span during extension.
func canExtendInto(r rune, runes []rune, pos int, extra ...int) bool {
	switch {
	case r >= '0' && r <= '9':
		return true
	case isASCIIAlpha(r):
		return wordLengthAt(runes, pos) <= 2
	case r == '+' || r == '-' || r == '=' || r == '<' || r == '>' || r == '*':
		return true
	case r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}':
		return true
	case r == ',' || r == '/':
		return true
	case r == ' ':
		return true
	case r == '^' || r == '_':
		return true
	}
	return false
}

// wordLengthAt returns the length of the ASCII letter word containing position
// pos in the rune slice.
func wordLengthAt(runes []rune, pos int) int {
	if pos < 0 || pos >= len(runes) || !isASCIIAlpha(runes[pos]) {
		return 0
	}
	start := pos
	for start > 0 && isASCIIAlpha(runes[start-1]) {
		start--
	}
	end := pos
	for end < len(runes)-1 && isASCIIAlpha(runes[end+1]) {
		end++
	}
	return end - start + 1
}

func isASCIIAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
