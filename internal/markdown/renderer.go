// Package markdown renders Markdown source to HTML using goldmark.
package markdown

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/mengkeat/yamdview/internal/markdown/math"
	"github.com/mengkeat/yamdview/internal/mathfix"
	"github.com/mengkeat/yamdview/internal/tablefix"
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
// It preprocesses deterministic render-only repairs (tables, then Unicode
// math notation) before passing the source to goldmark. The original source
// bytes are not modified.
func Render(md goldmark.Markdown, src []byte) (string, error) {
	// Repair obvious malformed pipe tables before math conversion so the table
	// structure is available to goldmark's table extension.
	src = tablefix.Preprocess(src)

	// Convert Unicode math to TeX notation (render-only).
	src = mathfix.Preprocess(src)

	var buf strings.Builder
	if err := md.Convert(src, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
