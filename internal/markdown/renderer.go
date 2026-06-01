// Package markdown renders Markdown source to HTML using goldmark.
package markdown

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/mengkeat/yamdview/internal/markdown/math"
)

// NewRenderer creates a goldmark instance configured with sensible defaults
// for viewer rendering.
func NewRenderer() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.NewTable(),
			extension.Strikethrough,
			extension.TaskList,
			extension.Linkify,
			math.Math,
		),
	)
}

// Render converts Markdown source bytes to an HTML string.
// It returns the rendered HTML fragment (no <html>/<head>/<body> wrapper).
func Render(md goldmark.Markdown, src []byte) (string, error) {
	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
