// Package math provides a goldmark extension that parses math notation
// and renders KaTeX-compatible HTML placeholders.
//
// Supported syntax:
//
//	Inline:  $x^2$          \(x^2\)
//	Display: $$x^2$$        \[x^2\]
//	Fenced:  ```math\nx^2\n```
package math

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// ── AST node types ───────────────────────────────────────

// KindInlineMath is the NodeKind for inline math nodes ($...$ or \(...\)).
var KindInlineMath = gast.NewNodeKind("InlineMath")

// KindDisplayMath is the NodeKind for display math blocks ($$...$$ or \[...\]).
var KindDisplayMath = gast.NewNodeKind("DisplayMath")

// InlineMath represents an inline math expression.
type InlineMath struct {
	gast.BaseInline
}

// Dump implements gast.Node.Dump.
func (n *InlineMath) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, nil, nil)
}

// Kind implements gast.Node.Kind.
func (n *InlineMath) Kind() gast.NodeKind {
	return KindInlineMath
}

// NewInlineMath creates a new InlineMath node.
func NewInlineMath() *InlineMath {
	return &InlineMath{}
}

// DisplayMath represents a display math block.
type DisplayMath struct {
	gast.BaseBlock
}

// Dump implements gast.Node.Dump.
func (n *DisplayMath) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, nil, nil)
}

// Kind implements gast.Node.Kind.
func (n *DisplayMath) Kind() gast.NodeKind {
	return KindDisplayMath
}

// IsRaw implements gast.Node.IsRaw.
func (n *DisplayMath) IsRaw() bool {
	return true
}

// NewDisplayMath creates a new DisplayMath node.
func NewDisplayMath() *DisplayMath {
	return &DisplayMath{}
}

// ── Inline parser: $...$ and \(...\) ─────────────────────

type inlineMathParser struct{}

var defaultInlineMathParser = &inlineMathParser{}

// NewInlineMathParser returns an InlineParser for $...$ and \(...\).
func NewInlineMathParser() parser.InlineParser {
	return defaultInlineMathParser
}

func (p *inlineMathParser) Trigger() []byte {
	return []byte{'$', '\\'}
}

var backslashOpenParen = []byte(`\(`)
var backslashCloseParen = []byte(`\)`)

func (p *inlineMathParser) Parse(parent gast.Node, block text.Reader, pc parser.Context) gast.Node {
	line, segment := block.PeekLine()

	if len(line) < 2 {
		return nil
	}

	switch line[0] {
	case '$':
		return p.parseDollar(parent, block, pc, line, segment)
	case '\\':
		return p.parseBackslashParen(parent, block, pc, line, segment)
	}
	return nil
}

func (p *inlineMathParser) parseDollar(parent gast.Node, block text.Reader, pc parser.Context, line []byte, segment text.Segment) gast.Node {
	// Reject $$ (display math) — handled by block parser.
	if len(line) > 1 && line[1] == '$' {
		return nil
	}

	// Find closing $ that is not preceded by backslash.
	start := 1
	rest := line[start:]
	end := bytes.IndexByte(rest, '$')
	if end < 0 {
		return nil
	}
	// Check not escaped
	for end > 0 && rest[end-1] == '\\' {
		next := bytes.IndexByte(rest[end+1:], '$')
		if next < 0 {
			return nil
		}
		end += 1 + next
	}

	tex := rest[:end]
	if len(tex) == 0 {
		return nil
	}

	// Content is not allowed to span multiple lines for inline $...$.
	if bytes.Contains(tex, []byte{'\n'}) {
		return nil
	}

	consumed := 1 + end + 1 // opening $ + content + closing $

	node := NewInlineMath()
	node.AppendChild(node, gast.NewRawTextSegment(segment.WithStop(segment.Start+consumed)))
	block.Advance(consumed)
	return node
}

func (p *inlineMathParser) parseBackslashParen(parent gast.Node, block text.Reader, pc parser.Context, line []byte, segment text.Segment) gast.Node {
	if !bytes.HasPrefix(line, backslashOpenParen) {
		return nil
	}

	rest := line[2:]
	end := bytes.Index(rest, backslashCloseParen)
	if end < 0 {
		return nil
	}

	tex := rest[:end]
	if len(tex) == 0 {
		return nil
	}

	if bytes.Contains(tex, []byte{'\n'}) {
		return nil
	}

	consumed := 2 + end + 2 // \( + content + \)

	node := NewInlineMath()
	node.AppendChild(node, gast.NewRawTextSegment(segment.WithStop(segment.Start+consumed)))
	block.Advance(consumed)
	return node
}

func (p *inlineMathParser) CloseBlock(parent gast.Node, pc parser.Context) {}

// ── Block parser: $$...$$ and \[...\] ────────────────────

type displayMathBlockParser struct{}

var defaultDisplayMathBlockParser = &displayMathBlockParser{}

// NewDisplayMathBlockParser returns a BlockParser for $$...$$ and \[...\].
func NewDisplayMathBlockParser() parser.BlockParser {
	return defaultDisplayMathBlockParser
}

func (b *displayMathBlockParser) Trigger() []byte {
	return []byte{'$', '\\'}
}

func (b *displayMathBlockParser) Open(parent gast.Node, reader text.Reader, pc parser.Context) (gast.Node, parser.State) {
	line, segment := reader.PeekLine()
	// Skip indented lines (code block territory).
	indent := pc.BlockIndent()
	if indent > 3 {
		return nil, parser.NoChildren
	}
	content := line[indent:]

	// $$ display math
	if bytes.HasPrefix(content, []byte("$$")) {
		// Check if it's a single-line $$...$$
		afterOpen := content[2:]
		closeIdx := bytes.Index(afterOpen, []byte("$$"))
		if closeIdx >= 0 {
			// Single line $$...$$
			node := NewDisplayMath()
			tex := bytes.TrimSpace(afterOpen[:closeIdx])
			node.Lines().Append(text.NewSegment(segment.Start+indent+2, segment.Start+indent+2+len(tex)))
			reader.AdvanceToEOL()
			return node, parser.Close | parser.NoChildren
		}
		// Multi-line $$ ... $$
		node := NewDisplayMath()
		pc.Set(displayMathContextKey, &displayMathData{kind: 'd', node: node})
		reader.AdvanceToEOL()
		return node, parser.NoChildren
	}

	// \[ display math
	if bytes.HasPrefix(content, []byte("\\[")) {
		afterOpen := content[2:]
		closeIdx := bytes.Index(afterOpen, []byte("\\]"))
		if closeIdx >= 0 {
			node := NewDisplayMath()
			tex := bytes.TrimSpace(afterOpen[:closeIdx])
			node.Lines().Append(text.NewSegment(segment.Start+indent+2, segment.Start+indent+2+len(tex)))
			reader.AdvanceToEOL()
			return node, parser.Close | parser.NoChildren
		}
		node := NewDisplayMath()
		pc.Set(displayMathContextKey, &displayMathData{kind: 'b', node: node})
		reader.AdvanceToEOL()
		return node, parser.NoChildren
	}

	return nil, parser.NoChildren
}

type displayMathData struct {
	kind byte // 'd' for $$, 'b' for \[
	node gast.Node
}

var displayMathContextKey = parser.NewContextKey()

func (b *displayMathBlockParser) Continue(node gast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, segment := reader.PeekLine()
	data := pc.Get(displayMathContextKey).(*displayMathData)

	switch data.kind {
	case 'd':
		if bytes.HasPrefix(line, []byte("$$")) {
			reader.AdvanceToEOL()
			return parser.Close
		}
	case 'b':
		if bytes.Contains(line, []byte("\\]")) {
			// Add content up to \]
			idx := bytes.Index(line, []byte("\\]"))
			node.Lines().Append(text.NewSegment(segment.Start, segment.Start+idx))
			reader.AdvanceToEOL()
			return parser.Close
		}
	}

	node.Lines().Append(segment)
	reader.AdvanceToEOL()
	return parser.Continue | parser.NoChildren
}

func (b *displayMathBlockParser) Close(node gast.Node, reader text.Reader, pc parser.Context) {
	pc.Set(displayMathContextKey, nil)
}

func (b *displayMathBlockParser) CanInterruptParagraph() bool {
	return true
}

func (b *displayMathBlockParser) CanAcceptIndentedLine() bool {
	return false
}

// ── HTML renderer ────────────────────────────────────────

type mathHTMLRenderer struct {
	html.Config
}

// NewMathHTMLRenderer returns a renderer for math nodes.
func NewMathHTMLRenderer(opts ...html.Option) renderer.NodeRenderer {
	r := &mathHTMLRenderer{Config: html.NewConfig()}
	for _, opt := range opts {
		opt.SetHTMLOption(&r.Config)
	}
	return r
}

// RegisterFuncs implements renderer.NodeRenderer.RegisterFuncs.
func (r *mathHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindInlineMath, r.renderInlineMath)
	reg.Register(KindDisplayMath, r.renderDisplayMath)
	// Also intercept fenced code blocks with language "math".
	reg.Register(gast.KindFencedCodeBlock, r.renderFencedMath)
}

func (r *mathHTMLRenderer) renderInlineMath(w util.BufWriter, source []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkContinue, nil
	}

	tex := extractInlineTeX(source, node)
	fmt.Fprintf(w, `<span class="math math-inline" data-tex="%s"></span>`, escapeHTMLAttr(tex))
	return gast.WalkSkipChildren, nil
}

func (r *mathHTMLRenderer) renderDisplayMath(w util.BufWriter, source []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkContinue, nil
	}

	tex := extractBlockTeX(source, node)
	fmt.Fprintf(w, `<div class="math math-display" data-tex="%s"></div>`+"\n", escapeHTMLAttr(tex))
	return gast.WalkSkipChildren, nil
}

func (r *mathHTMLRenderer) renderFencedMath(w util.BufWriter, source []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	n := node.(*gast.FencedCodeBlock)
	lang := n.Language(source)
	if lang == nil || string(lang) != "math" {
		// Not a math fence — delegate to the default renderer.
		return r.renderDefaultFencedCode(w, source, n, entering)
	}

	if !entering {
		return gast.WalkContinue, nil
	}

	tex := extractBlockTeX(source, n)
	fmt.Fprintf(w, `<div class="math math-display" data-tex="%s"></div>`+"\n", escapeHTMLAttr(tex))
	return gast.WalkContinue, nil
}

func (r *mathHTMLRenderer) renderDefaultFencedCode(w util.BufWriter, source []byte, n *gast.FencedCodeBlock, entering bool) (gast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<pre><code")
		if lang := n.Language(source); lang != nil {
			_, _ = w.WriteString(" class=\"language-")
			_, _ = w.Write(lang)
			_, _ = w.WriteString("\"")
		}
		_ = w.WriteByte('>')
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			_, _ = w.Write(seg.Value(source))
		}
	} else {
		_, _ = w.WriteString("</code></pre>\n")
	}
	return gast.WalkContinue, nil
}

// ── Helpers ──────────────────────────────────────────────

// TeX delimiters to strip from math content.
var (
	dollarDelimRe     = regexp.MustCompile(`^\$+|\$+$`)
	backslashParenRe  = regexp.MustCompile(`^\\\(|\\\)$`)
	backslashBracketRe = regexp.MustCompile(`^\\\[|\\\]$`)
)

func extractInlineTeX(source []byte, node gast.Node) string {
	var buf bytes.Buffer
	for c := node.FirstChild(); c != nil; c = c.NextSibling() {
		seg, ok := c.(*gast.Text)
		if !ok {
			continue
		}
		value := seg.Value(source)

		// Strip surrounding delimiters.
		value = stripInlineDelimiters(value)
		buf.Write(value)
	}
	return strings.TrimSpace(buf.String())
}

func extractBlockTeX(source []byte, node gast.Node) string {
	var buf bytes.Buffer
	lines := node.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		line := seg.Value(source)
		// Trim the $$ delimiters from first and last lines if present.
		line = bytes.TrimRight(line, "\n")
		line = stripBlockDelimiters(line, i, lines.Len())
		if len(line) > 0 {
			buf.Write(line)
			buf.WriteByte('\n')
		}
	}
	return strings.TrimSpace(buf.String())
}

func stripInlineDelimiters(data []byte) []byte {
	s := string(data)
	// $...$
	s = dollarDelimRe.ReplaceAllString(s, "")
	// \(...\)
	s = backslashParenRe.ReplaceAllString(s, "")
	return []byte(s)
}

func stripBlockDelimiters(line []byte, lineIdx int, totalLines int) []byte {
	s := string(line)
	if lineIdx == 0 {
		s = strings.TrimPrefix(s, "$$")
		s = strings.TrimPrefix(s, "\\[")
	}
	if lineIdx == totalLines-1 {
		s = strings.TrimSuffix(s, "$$")
		s = strings.TrimSuffix(s, "\\]")
	}
	return []byte(s)
}

func escapeHTMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// ── Extension ────────────────────────────────────────────

type mathExtension struct{}

// Math is a goldmark extension that adds math notation support.
var Math = &mathExtension{}

// Extend implements goldmark.Extender.
func (e *mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithInlineParsers(
			util.Prioritized(NewInlineMathParser(), 501),
		),
		parser.WithBlockParsers(
			util.Prioritized(NewDisplayMathBlockParser(), 501),
		),
	)
	m.Renderer().AddOptions(
		renderer.WithNodeRenderers(
			util.Prioritized(NewMathHTMLRenderer(), 501),
		),
	)
}
