package mathfix

import (
	"bytes"
	"strings"
)

// Preprocess takes raw Markdown source and converts Unicode math in
// non-code paragraphs to TeX-delimited notation. The returned source can
// be passed directly to goldmark for rendering.
//
// Lines already containing TeX math delimiters ($, $$, \(, \[) are left
// unchanged, ensuring idempotence. Content inside fenced code blocks and
// inline code spans is never modified.
func Preprocess(src []byte) []byte {
	if !HasUnicodeMath(string(src)) {
		return src
	}

	lines := bytes.Split(src, []byte("\n"))
	var result [][]byte

	inCodeFence := false
	var fenceMarker string

	var paragraph [][]byte // accumulates non-code, non-blank lines

	flush := func() {
		if len(paragraph) == 0 {
			return
		}
		text := string(bytes.Join(paragraph, []byte("\n")))
		fixed := fixParagraphText(text)
		result = append(result, []byte(fixed))
		paragraph = nil
	}

	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)

		// Track fenced code blocks.
		if isFenceOpen(trimmed) && !inCodeFence {
			flush()
			inCodeFence = true
			fenceMarker = fenceChar(trimmed)
			result = append(result, line)
			continue
		}
		if inCodeFence && isFenceClose(trimmed, fenceMarker) {
			inCodeFence = false
			fenceMarker = ""
			result = append(result, line)
			continue
		}
		if inCodeFence {
			result = append(result, line)
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
				result = append(result, []byte(fixed.Converted))
			} else {
				result = append(result, line)
			}
			continue
		}

		// Regular text line: accumulate into current paragraph.
		paragraph = append(paragraph, line)
	}

	flush()
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

// isFenceOpen reports whether a trimmed line opens a fenced code block.
func isFenceOpen(line []byte) bool {
	return bytes.HasPrefix(line, []byte("```")) ||
		bytes.HasPrefix(line, []byte("~~~"))
}

// isFenceClose reports whether a trimmed line closes the current fence.
func isFenceClose(line []byte, marker string) bool {
	if marker == "" {
		return false
	}
	return bytes.HasPrefix(line, []byte(strings.Repeat(marker, 3)))
}

// fenceChar returns the first character of a fence marker ("`" or "~").
func fenceChar(line []byte) string {
	if len(line) == 0 {
		return ""
	}
	return string(line[0])
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
	return false
}
