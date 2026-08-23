package mathfix

import (
	"bytes"
	"strings"

	"github.com/mengkeat/yamdview/internal/mdfence"
)

// Preprocess takes raw Markdown source and converts Unicode math in
// non-code paragraphs to TeX-delimited notation. The returned source can
// be passed directly to goldmark for rendering.
//
// Lines already containing TeX math delimiters ($, $$, \(, \[) are left
// unchanged, ensuring idempotence. Content inside inline code spans is never
// modified. Fenced code blocks are preserved except for short ```text fences
// that look like displayed equations.
func Preprocess(src []byte) []byte {
	if !HasUnicodeMath(string(src)) && !HasTextFenceCandidate(src) {
		return src
	}

	lines := bytes.Split(src, []byte("\n"))
	var result [][]byte
	changed := false

	inCodeFence := false
	var fenceMarker string
	var fenceInfo string
	var fenceLines [][]byte

	paragraph := make([][]byte, 0, len(lines)) // accumulates non-code, non-blank lines

	flush := func() {
		if len(paragraph) == 0 {
			return
		}
		text := string(bytes.Join(paragraph, []byte("\n")))
		fixed := fixParagraphText(text)
		if fixed != text {
			changed = true
		}
		result = append(result, []byte(fixed))
		paragraph = nil
	}

	flushFence := func() {
		if len(fenceLines) == 0 {
			return
		}

		last := bytes.TrimSpace(fenceLines[len(fenceLines)-1])
		closed := mdfence.IsClose(last, fenceMarker)
		if closed && isTextFenceInfo(fenceInfo) && len(fenceLines) >= 2 {
			contentEnd := len(fenceLines) - 1

			content := string(bytes.Join(fenceLines[1:contentEnd], []byte("\n")))
			if fixed, ok := fixProbableEquationBlock(content); ok {
				changed = true
				result = append(result, []byte(fixed))
				fenceLines = nil
				return
			}
		}

		result = append(result, fenceLines...)
		fenceLines = nil
	}

	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)

		// Track fenced code blocks.
		if marker := mdfence.Marker(trimmed); marker != "" && !inCodeFence {
			flush()
			inCodeFence = true
			fenceMarker = marker
			fenceInfo = mdfence.Info(trimmed)
			fenceLines = append(fenceLines, line)
			continue
		}
		if inCodeFence {
			fenceLines = append(fenceLines, line)
			if mdfence.IsClose(trimmed, fenceMarker) {
				flushFence()
				inCodeFence = false
				fenceMarker = ""
				fenceInfo = ""
			}
			continue
		}

		// Blank line = paragraph boundary.
		if len(trimmed) == 0 {
			flush()
			result = append(result, line)
			continue
		}

		// Structural elements: headings, blockquotes, thematic breaks,
		// HTML tags, pipe tables. Flush paragraph, then handle inline.
		if isStructuralLine(trimmed) {
			flush()
			if HasUnicodeMath(string(trimmed)) && !hasTeXDelimiters(string(trimmed)) {
				fixed := Fix(string(trimmed))
				// Compare against the trimmed line: Converted is derived from
				// it, so a mismatch means a real conversion happened. When no
				// conversion occurs the original line is emitted unchanged,
				// preserving any leading/trailing whitespace.
				if fixed.Converted != string(trimmed) {
					changed = true
					result = append(result, []byte(fixed.Converted))
				} else {
					result = append(result, line)
				}
			} else {
				result = append(result, line)
			}
			continue
		}

		// Regular text line: accumulate into current paragraph.
		paragraph = append(paragraph, line)
	}

	if inCodeFence {
		flushFence()
	}
	flush()
	if !changed {
		return src
	}
	return bytes.Join(result, []byte("\n"))
}

// fixParagraphText converts a paragraph of text (potentially multi-line).
func fixParagraphText(text string) string {
	// If the paragraph already has TeX delimiters, leave it alone.
	if hasTeXDelimiters(text) {
		return text
	}

	if !HasUnicodeMath(text) {
		return text
	}

	fr := Fix(text)
	if !fr.Applied {
		return text
	}
	return fr.Converted
}

// hasTeXDelimiters reports whether text already contains TeX math delimiters.
// We check for $$ (display), \(…\), \[…\], and fenced math blocks.
// A lone $ is ambiguous (currency) and not treated as a delimiter here;
// the Fix function handles idempotence by not re-wrapping content that
// already lacks Unicode math chars.
func hasTeXDelimiters(text string) bool {
	return strings.Contains(text, "$$") ||
		strings.Contains(text, `\(`) ||
		strings.Contains(text, `\[`)
}

func isTextFenceInfo(info string) bool {
	return strings.EqualFold(info, "text") || strings.EqualFold(info, "txt")
}

// HasTextFenceCandidate reports whether src contains a fenced code block with
// a text/txt info string — the only fence kind the preprocessor treats as
// containing displayable equations.
func HasTextFenceCandidate(src []byte) bool {
	for _, line := range bytes.Split(src, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if marker := mdfence.Marker(trimmed); marker != "" && isTextFenceInfo(mdfence.Info(trimmed)) {
			return true
		}
	}
	return false
}

// isStructuralLine reports whether a trimmed line is a Markdown structural
// element (heading, blockquote, thematic break, HTML tag, or pipe table row).
func isStructuralLine(line []byte) bool {
	if len(line) == 0 {
		return false
	}
	// Headings.
	if line[0] == '#' {
		return true
	}
	// Blockquotes.
	if line[0] == '>' {
		return true
	}
	// Thematic breaks: ---, ***, ___ (3+ chars).
	if len(line) >= 3 {
		c := line[0]
		if (c == '-' || c == '*' || c == '_') &&
			line[1] == c && line[2] == c {
			// Check all same char or trailing spaces.
			allSame := true
			for _, b := range line {
				if b != c && b != ' ' && b != '\t' {
					allSame = false
					break
				}
			}
			if allSame {
				return true
			}
		}
	}
	// HTML block.
	if bytes.HasPrefix(line, []byte("<")) {
		return true
	}
	// Pipe table row. Handle row-by-row so math conversion does not merge
	// table syntax across lines.
	if bytes.Contains(line, []byte("|")) {
		return true
	}
	return false
}
