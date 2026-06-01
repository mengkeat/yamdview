// Package mdfence contains small helpers for Markdown fenced code blocks.
package mdfence

import (
	"bytes"
	"strings"
)

// Marker returns the full opening fence marker from a trimmed Markdown fence
// line, such as "```" or "~~~~". It returns "" when line is not a fence.
func Marker(line []byte) string {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return ""
	}

	marker := line[0]
	count := 0
	for count < len(line) && line[count] == marker {
		count++
	}
	if count < 3 {
		return ""
	}
	return strings.Repeat(string(marker), count)
}

// IsOpen reports whether a trimmed line opens a fenced code block.
func IsOpen(line []byte) bool {
	return Marker(line) != ""
}

// IsClose reports whether a trimmed line closes the current fence marker.
func IsClose(line []byte, marker string) bool {
	return marker != "" && bytes.HasPrefix(line, []byte(marker))
}

// Info returns the first info-string field from a trimmed opening fence line.
func Info(line []byte) string {
	marker := Marker(line)
	if marker == "" {
		return ""
	}

	fields := strings.Fields(strings.TrimSpace(string(line[len(marker):])))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
