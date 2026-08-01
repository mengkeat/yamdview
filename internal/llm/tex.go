package llm

import (
	"strings"
	"unicode/utf8"
)

// ErrInvalidTeX is the sentinel base for TeX sanity failures.
var errInvalidTeX = "invalid tex"

// ValidateTeX runs structural sanity checks on a TeX expression. It is not a
// full parser: it enforces the properties that matter for safe KaTeX rendering
// and for rejecting garbled model output:
//
//   - braces are balanced (escaped \{ and \} are ignored);
//   - \left and \right delimiters are balanced;
//   - no raw control characters (other than tab/newline);
//   - no dangling backslash at the end of the expression.
//
// It returns the first issue found, or nil when the expression is structurally
// sound. An empty expression passes; callers that require TeX should check
// non-emptiness separately.
func ValidateTeX(tex string) error {
	if tex == "" {
		return nil
	}
	if err := checkControlChars(tex); err != nil {
		return err
	}
	if err := checkBraces(tex); err != nil {
		return err
	}
	if err := checkLeftRight(tex); err != nil {
		return err
	}
	if err := checkDanglingBackslash(tex); err != nil {
		return err
	}
	return nil
}

// checkControlChars rejects bytes below 0x20 other than tab, newline, and CR.
func checkControlChars(tex string) error {
	for i := 0; i < len(tex); {
		r, size := utf8.DecodeRuneInString(tex[i:])
		if r == utf8.RuneError && size == 1 {
			return validationErrorf("%s: invalid utf-8 at byte %d", errInvalidTeX, i)
		}
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return validationErrorf("%s: control character 0x%02x at byte %d", errInvalidTeX, r, i)
		}
		i += size
	}
	return nil
}

// checkBraces ensures curly braces balance, treating a backslash escape as
// consuming the following byte so that \{ and \} are ignored.
func checkBraces(tex string) error {
	depth := 0
	for i := 0; i < len(tex); i++ {
		if tex[i] == '\\' && i+1 < len(tex) {
			i++ // skip the escaped character
			continue
		}
		switch tex[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return validationErrorf("%s: unbalanced closing brace", errInvalidTeX)
			}
		}
	}
	if depth != 0 {
		return validationErrorf("%s: %d unclosed brace(s)", errInvalidTeX, depth)
	}
	return nil
}

// checkLeftRight ensures \left and \right delimiter commands are balanced.
func checkLeftRight(tex string) error {
	left := countTeXCommand(tex, `\left`)
	right := countTeXCommand(tex, `\right`)
	if left != right {
		return validationErrorf("%s: %d \\left but %d \\right", errInvalidTeX, left, right)
	}
	return nil
}

// checkDanglingBackslash rejects a trailing backslash with nothing after it.
func checkDanglingBackslash(tex string) error {
	if strings.HasSuffix(tex, `\`) {
		return validationErrorf("%s: trailing backslash", errInvalidTeX)
	}
	return nil
}

// countTeXCommand counts whole-word occurrences of cmd (e.g. "\left") that are
// not part of a longer command name such as "\leftarrow".
func countTeXCommand(tex, cmd string) int {
	count := 0
	search := tex
	for {
		j := strings.Index(search, cmd)
		if j < 0 {
			return count
		}
		end := j + len(cmd)
		if end >= len(search) || !isASCIILetterByte(search[end]) {
			count++
		}
		search = search[end:]
	}
}

func isASCIILetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
