package llm

import (
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	coreast "github.com/yuin/goldmark/ast"
)

var (
	tableParser     goldmark.Markdown
	tableParserOnce sync.Once
)

// tableMarkdown returns a goldmark instance configured with the GFM table
// extension. It mirrors the viewer's table support without pulling in math or
// link preprocessing, keeping the validator self-contained.
func tableMarkdown() goldmark.Markdown {
	tableParserOnce.Do(func() {
		tableParser = goldmark.New(goldmark.WithExtensions(extension.NewTable()))
	})
	return tableParser
}

// parseTableDocument parses src and returns the root document node.
func parseTableDocument(src []byte) coreast.Node {
	return tableMarkdown().Parser().Parse(text.NewReader(src))
}

// walkTable runs fn over every node of the parsed document. The walker never
// returns an error, so the (always-nil) Walk error is ignored.
func walkTable(src []byte, fn func(n coreast.Node, entering bool) coreast.WalkStatus) {
	_ = coreast.Walk(parseTableDocument(src), func(n coreast.Node, entering bool) (coreast.WalkStatus, error) {
		return fn(n, entering), nil
	})
}

// HasTable reports whether src contains at least one GFM table node.
func HasTable(src []byte) bool {
	found := false
	walkTable(src, func(n coreast.Node, entering bool) coreast.WalkStatus {
		if entering && n.Kind() == gast.KindTable {
			found = true
			return coreast.WalkStop
		}
		return coreast.WalkContinue
	})
	return found
}

// IsTableOnly reports whether every non-empty top-level block in src is a
// table. It is used to reject table repairs that smuggle in unrelated prose,
// headings, or lists alongside the corrected table.
func IsTableOnly(src []byte) bool {
	hasTable := false
	for c := parseTableDocument(src).FirstChild(); c != nil; c = c.NextSibling() {
		switch c.Kind() {
		case gast.KindTable:
			hasTable = true
		case coreast.KindParagraph:
			if strings.TrimSpace(string(c.Text(src))) == "" {
				continue
			}
			return false
		default:
			// Any heading, list, code block, etc. alongside a table is stray.
			return false
		}
	}
	return hasTable
}

// TableCellTexts returns the trimmed text of every cell in the first table of
// src, in document order. Empty cells are included as empty strings.
func TableCellTexts(src []byte) []string {
	var texts []string
	walkTable(src, func(n coreast.Node, entering bool) coreast.WalkStatus {
		if entering && n.Kind() == gast.KindTableCell {
			texts = append(texts, strings.TrimSpace(string(n.Text(src))))
		}
		return coreast.WalkContinue
	})
	return texts
}

// TableDimensions returns the number of rows and the maximum column count of
// the first table in src, or (0, 0) when src contains no table.
func TableDimensions(src []byte) (rows, cols int) {
	walkTable(src, func(n coreast.Node, entering bool) coreast.WalkStatus {
		if !entering || !isTableRowLike(n.Kind()) {
			return coreast.WalkContinue
		}
		rows++
		col := 0
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			col++
		}
		if col > cols {
			cols = col
		}
		return coreast.WalkContinue
	})
	return rows, cols
}

// isTableRowLike reports whether a node kind is a table header or body row.
// The GFM extension models the header as a distinct node wrapping cells rather
// than a body TableRow, so both must be counted.
func isTableRowLike(kind coreast.NodeKind) bool {
	return kind == gast.KindTableRow || kind == gast.KindTableHeader
}
