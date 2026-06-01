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
// of 2+ letters.
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
	// Extension uses two word-length thresholds:
	//   - direct adjacency (no space between): allow words up to 2 letters
	//     (e.g. "dx" in "∫₀¹ x² dx" where the 'd' is directly reachable)
	//   - through a space: allow only single letters (word length 1)
	//     to avoid pulling in English words like "to", "of", "in".
	var rawSpans []mathSpan

	for _, seed := range seeds {
		s := seed
		e := seed + 1

		// Extend left: digits, operators, parentheses directly adjacent.
		// Do NOT cross spaces on the left to avoid pulling in unrelated text.
		for s > 0 {
			if codeRuneSet[s-1] {
				break
			}
			r := runes[s-1]
			if canExtendLeft(r, runes, s-1) {
				s--
			} else {
				break
			}
		}

		// Extend right: allow 2-letter words directly adjacent,
		// but only single letters across spaces.
		for e < n {
			if codeRuneSet[e] {
				break
			}
			r := runes[e]
			if r == ' ' {
				// Peek past spaces to decide whether to cross.
				j := e
				for j < n && runes[j] == ' ' {
					j++
				}
				if j < n && canExtendThroughSpace(runes[j], runes, j) {
					e = j // skip past spaces
					continue
				}
				break
			}
			if canExtendRight(r, runes, e) {
				e++
			} else {
				break
			}
		}

		rawSpans = append(rawSpans, mathSpan{s, e})
	}

	// Merge overlapping or adjacent spans.
	merged := make([]mathSpan, 0, len(rawSpans))
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

	// Trim leading/trailing spaces and unbalanced delimiters from spans.
	for i := range merged {
		for merged[i].end > merged[i].start && runes[merged[i].end-1] == ' ' {
			merged[i].end--
		}
		for merged[i].start < merged[i].end && runes[merged[i].start] == ' ' {
			merged[i].start++
		}
		// Trim unbalanced closing delimiters at the end.
		merged[i] = trimUnbalanced(runes, merged[i])
	}

	// Convert to mathSpan.
	return merged
}

// trimUnbalanced removes unbalanced parentheses, brackets, and braces from
// the edges of a mathSpan. For example, if the span ends with ) but has no
// matching ( inside, the trailing ) is trimmed.
func trimUnbalanced(runes []rune, s mathSpan) mathSpan {
	for {
		changed := false
		if s.end > s.start {
			ch := runes[s.end-1]
			if ch == ')' || ch == ']' || ch == '}' {
				if !hasBalancedPair(runes, s.start, s.end, ch) {
					s.end--
					// Also trim any trailing space revealed.
					for s.end > s.start && runes[s.end-1] == ' ' {
						s.end--
					}
					changed = true
				}
			}
		}
		if s.start < s.end {
			ch := runes[s.start]
			if ch == '(' || ch == '[' || ch == '{' {
				closing := matchOpening(ch)
				if !hasBalancedPairForward(runes, s.start, s.end, closing) {
					s.start++
					for s.start < s.end && runes[s.start] == ' ' {
						s.start++
					}
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return s
}

// hasBalancedPair checks if there's a matching opening delimiter inside the
// span for the closing delimiter at the end.
func hasBalancedPair(runes []rune, start, end int, closing rune) bool {
	opening := matchClosing(closing)
	depth := 1
	for i := end - 2; i >= start; i-- {
		if runes[i] == closing {
			depth++
		} else if runes[i] == opening {
			depth--
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

// hasBalancedPairForward checks if there's a matching closing delimiter inside
// the span for the opening delimiter at the start.
func hasBalancedPairForward(runes []rune, start, end int, closing rune) bool {
	opening := runes[start]
	depth := 1
	for i := start + 1; i < end; i++ {
		if runes[i] == opening {
			depth++
		} else if runes[i] == closing {
			depth--
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

func matchClosing(closing rune) rune {
	switch closing {
	case ')':
		return '('
	case ']':
		return '['
	case '}':
		return '{'
	}
	return closing
}

func matchOpening(opening rune) rune {
	switch opening {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	}
	return opening
}

// canExtendLeft reports whether the character at position pos can be included
// in a math span when extending left. Does not cross spaces to avoid pulling
// in unrelated text (e.g. "0.551" from "0.551 m/s²").
func canExtendLeft(r rune, runes []rune, pos int) bool {
	switch {
	case isUnicodeMathChar(r):
		return true
	case r >= '0' && r <= '9':
		return true
	case isASCIIAlpha(r):
		return wordLengthAt(runes, pos) <= 1
	case r == '+' || r == '-' || r == '=' || r == '<' || r == '>' || r == '*':
		return true
	case r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}':
		return true
	case r == ',' || r == '/' || r == '.':
		return true
	case r == '^' || r == '_':
		return true
	}
	return false
}

// canExtendRight reports whether character at position pos can be included when
// extending right. If spaceCrossed is true, only single letters are allowed;
// otherwise 2-letter words are also accepted for cases like "dx".
func canExtendRight(r rune, runes []rune, pos int) bool {
	switch {
	case isUnicodeMathChar(r):
		return true
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
	case r == '^' || r == '_':
		return true
	}
	return false
}

// canExtendThroughSpace reports whether the first non-space character after
// a space gap can be included in a math span. This is the gatekeeper for
// crossing spaces — it should only allow crossing when the content after
// the space looks math-like.
func canExtendThroughSpace(r rune, runes []rune, pos int) bool {
	switch {
	case isUnicodeMathChar(r):
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '+' || r == '-' || r == '=' || r == '<' || r == '>' || r == '*':
		return true
	case r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}':
		return true
	case isASCIIAlpha(r):
		// Only single letters after a space: likely variable names (x, n, i).
		// Do not allow 2+ letter words through spaces — they are almost
		// always English words like "in", "of", "to", "dx".
		return wordLengthAt(runes, pos) <= 1
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
