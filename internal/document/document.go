// Package document builds block-oriented Markdown snapshots and diffs them.
package document

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"

	"github.com/mengkeat/yamdview/internal/markdown"
	"github.com/mengkeat/yamdview/internal/mdfence"
	"github.com/mengkeat/yamdview/internal/tablefix"
)

// BlockKind identifies a top-level Markdown block category.
type BlockKind string

const (
	BlockHeading       BlockKind = "heading"
	BlockParagraph     BlockKind = "paragraph"
	BlockList          BlockKind = "list"
	BlockBlockquote    BlockKind = "blockquote"
	BlockCodeFence     BlockKind = "code_fence"
	BlockThematicBreak BlockKind = "thematic_break"
	BlockTable         BlockKind = "table"
	BlockMath          BlockKind = "math_block"
	BlockHTML          BlockKind = "html_block"
	BlockUnknown       BlockKind = "unknown"
)

// Diagnostic is a per-block warning or error surfaced during rendering.
type Diagnostic struct {
	Severity  string `json:"severity"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	BlockID   string `json:"block_id,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

// Block is a top-level Markdown block rendered independently for DOM patching.
type Block struct {
	ID          string
	Kind        BlockKind
	SourceStart int
	SourceEnd   int
	StartLine   int
	EndLine     int
	Source      string // original source slice (unrepaired)
	Normalized  string // source that was actually rendered (may be repaired)
	HTML        string
	Diagnostics []Diagnostic
}

// WasNormalized reports whether the rendered block used source that differs
// from the original slice. When true, the difference between Source and
// Normalized is a candidate SourcePatch for safe fix persistence.
func (b Block) WasNormalized() bool {
	return b.Source != b.Normalized
}

// DocumentSnapshot is the rendered state of one source file version.
type DocumentSnapshot struct {
	Blocks         []Block
	HTML           string
	FullResetOnly  bool
	FallbackReason string
}

// Patch operation names used by the browser DOM patch protocol.
const (
	OpReplace      = "replace"
	OpInsertAfter  = "insert_after"
	OpInsertBefore = "insert_before"
	OpDelete       = "delete"
	OpReset        = "reset"
)

const maxDiffMatrixCells = 250_000

// PatchOp describes a single browser-side DOM update.
type PatchOp struct {
	Op     string `json:"op"`
	ID     string `json:"id,omitempty"`
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`
	HTML   string `json:"html,omitempty"`
}

// DiffResult contains patch operations plus the next snapshot with stable IDs
// retained for unchanged blocks.
type DiffResult struct {
	Snapshot DocumentSnapshot
	Ops      []PatchOp
	Reset    bool
}

// BuildSnapshot segments and renders source into independently patchable blocks.
func BuildSnapshot(md goldmark.Markdown, src []byte) (DocumentSnapshot, error) {
	snapshot := DocumentSnapshot{}

	if reason, ok := fullResetReason(src); ok {
		rendered, err := markdown.Render(md, src)
		if err != nil {
			return DocumentSnapshot{}, err
		}
		snapshot.HTML = rendered
		snapshot.FullResetOnly = true
		snapshot.FallbackReason = reason
		return snapshot, nil
	}

	spans := segmentBlocks(src)
	snapshot.Blocks = make([]Block, 0, len(spans))
	for i, span := range spans {
		blockSource := string(src[span.start:span.end])
		renderSource, diagnostics := renderSourceForBlock(blockSource, span)
		rendered, err := markdown.Render(md, []byte(renderSource))
		if err != nil {
			return DocumentSnapshot{}, err
		}

		normalized := normalizeSource(blockSource)
		block := Block{
			Kind:        span.kind,
			SourceStart: span.start,
			SourceEnd:   span.end,
			StartLine:   span.startLine,
			EndLine:     span.endLine,
			Source:      blockSource,
			Normalized:  renderSource,
			HTML:        rendered,
			Diagnostics: diagnostics,
		}
		block.ID = blockID(i, block.Kind, normalized)
		for i := range block.Diagnostics {
			block.Diagnostics[i].BlockID = block.ID
		}
		snapshot.Blocks = append(snapshot.Blocks, block)
	}

	snapshot.HTML = RenderBlocks(snapshot.Blocks)
	return snapshot, nil
}

// Diff computes patch operations from oldSnapshot to nextSnapshot. Unchanged
// blocks retain their previous DOM IDs in the returned Snapshot.
func Diff(oldSnapshot, nextSnapshot DocumentSnapshot) DiffResult {
	next := nextSnapshot.clone()

	if oldSnapshot.FullResetOnly || next.FullResetOnly {
		return DiffResult{Snapshot: next, Reset: true}
	}
	if diffMatrixTooLarge(len(oldSnapshot.Blocks), len(next.Blocks)) {
		return DiffResult{Snapshot: next, Reset: true}
	}

	matches := lcsMatches(oldSnapshot.Blocks, next.Blocks)
	matchedNew := make(map[int]bool, len(matches))
	usedIDs := make(map[string]bool, len(next.Blocks))
	for _, m := range matches {
		next.Blocks[m.newIndex].ID = oldSnapshot.Blocks[m.oldIndex].ID
		matchedNew[m.newIndex] = true
		usedIDs[next.Blocks[m.newIndex].ID] = true
	}

	for i := range next.Blocks {
		if matchedNew[i] {
			continue
		}
		next.Blocks[i].ID = uniqueID(next.Blocks[i].ID, usedIDs)
	}
	next.HTML = RenderBlocks(next.Blocks)

	ops, reset := patchOps(oldSnapshot.Blocks, next.Blocks, matches)
	return DiffResult{Snapshot: next, Ops: ops, Reset: reset}
}

// RenderBlocks joins rendered blocks into the section-wrapped document HTML.
func RenderBlocks(blocks []Block) string {
	var out strings.Builder
	for _, block := range blocks {
		out.WriteString(block.WrappedHTML())
	}
	return out.String()
}

// WrappedHTML returns the rendered block wrapped in its patchable DOM section.
func (b Block) WrappedHTML() string {
	var out strings.Builder
	out.WriteString(`<section class="md-block" id="`)
	out.WriteString(html.EscapeString(b.ID))
	out.WriteString(`" data-kind="`)
	out.WriteString(html.EscapeString(string(b.Kind)))
	out.WriteString(`">`)
	if b.HTML != "" && !strings.HasPrefix(b.HTML, "\n") {
		out.WriteByte('\n')
	}
	out.WriteString(b.HTML)
	if b.HTML != "" && !strings.HasSuffix(b.HTML, "\n") {
		out.WriteByte('\n')
	}
	if len(b.Diagnostics) > 0 {
		b.writeDiagnostics(&out)
	}
	out.WriteString("</section>\n")
	return out.String()
}

func (b Block) writeDiagnostics(out *strings.Builder) {
	out.WriteString(`<div class="diagnostics" role="note" aria-label="Diagnostics">`)
	out.WriteByte('\n')
	for _, diag := range b.Diagnostics {
		severity := diag.Severity
		if severity == "" {
			severity = "warning"
		}
		out.WriteString(`<div class="diagnostic diagnostic-`)
		out.WriteString(html.EscapeString(severity))
		out.WriteString(`">`)
		out.WriteString(`<span class="diagnostic-code">`)
		out.WriteString(html.EscapeString(diag.Code))
		out.WriteString(`</span>`)
		if diag.Message != "" {
			out.WriteString(`: `)
			out.WriteString(html.EscapeString(diag.Message))
		}
		out.WriteString(`</div>`)
		out.WriteByte('\n')
	}
	out.WriteString(`</div>`)
	out.WriteByte('\n')
}

func renderSourceForBlock(blockSource string, span blockSpan) (string, []Diagnostic) {
	if span.kind != BlockTable {
		return blockSource, nil
	}

	result := tablefix.Fix(blockSource)
	if !result.TableLike {
		return blockSource, nil
	}

	diagnostics := make([]Diagnostic, 0, len(result.Diagnostics))
	for _, diag := range result.Diagnostics {
		diagnostics = append(diagnostics, Diagnostic{
			Severity:  diag.Severity,
			Code:      diag.Code,
			Message:   diag.Message,
			StartLine: span.startLine,
			EndLine:   span.endLine,
		})
	}

	return result.Markdown, diagnostics
}

func (s DocumentSnapshot) clone() DocumentSnapshot {
	clone := s
	clone.Blocks = append([]Block(nil), s.Blocks...)
	return clone
}

func blockID(ordinal int, kind BlockKind, normalized string) string {
	h := sha1.Sum([]byte(string(kind) + "\n" + normalized))
	return fmt.Sprintf("block-%d-%s", ordinal+1, hex.EncodeToString(h[:])[:8])
}

func uniqueID(id string, used map[string]bool) string {
	if !used[id] {
		used[id] = true
		return id
	}
	base := id
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func normalizeSource(source string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(source)), " ")
}

var referenceDefinitionRE = regexp.MustCompile(`(?m)^[ \t]{0,3}\[[^\]\n]+\]:`)

func fullResetReason(src []byte) (string, bool) {
	if referenceDefinitionRE.Match(src) {
		return "reference-style link or footnote definitions require full-document rendering", true
	}
	return "", false
}

type line struct {
	start int
	end   int
	text  []byte
}

type blockSpan struct {
	kind      BlockKind
	start     int
	end       int
	startLine int
	endLine   int
}

func segmentBlocks(src []byte) []blockSpan {
	lines := splitLines(src)
	var spans []blockSpan

	for i := 0; i < len(lines); {
		if isBlank(lines[i].text) {
			i++
			continue
		}

		start := i
		kind := classifyStart(lines, i)
		switch kind {
		case BlockCodeFence, BlockMath:
			i = consumeFenceOrMath(lines, i, kind)
		case BlockHeading:
			if isSetextHeading(lines, i) {
				i += 2
			} else {
				i++
			}
		case BlockThematicBreak:
			i++
		case BlockTable:
			i = consumeTable(lines, i)
		case BlockBlockquote, BlockList, BlockHTML:
			i = consumeUntilBlank(lines, i)
		default:
			i = consumeParagraph(lines, i)
		}

		if i <= start {
			i = start + 1
		}
		spans = append(spans, blockSpan{
			kind:      kind,
			start:     lines[start].start,
			end:       lines[i-1].end,
			startLine: start + 1,
			endLine:   i,
		})
	}

	return spans
}

func splitLines(src []byte) []line {
	if len(src) == 0 {
		return nil
	}

	lines := make([]line, 0, bytes.Count(src, []byte{'\n'})+1)
	for start := 0; start < len(src); {
		end := start + bytes.IndexByte(src[start:], '\n')
		if end < start {
			end = len(src)
		} else {
			end++
		}
		lines = append(lines, line{start: start, end: end, text: src[start:end]})
		start = end
	}
	return lines
}

func classifyStart(lines []line, i int) BlockKind {
	trimmed := trimLine(lines[i].text)

	if isFenceOpen(trimmed) {
		if fenceInfo(trimmed) == "math" {
			return BlockMath
		}
		return BlockCodeFence
	}
	if isDisplayMathStart(trimmed) {
		return BlockMath
	}
	if isATXHeading(trimmed) || isSetextHeading(lines, i) {
		return BlockHeading
	}
	if isThematicBreak(trimmed) {
		return BlockThematicBreak
	}
	if isTableLikeStart(lines, i) {
		return BlockTable
	}
	if bytes.HasPrefix(trimmed, []byte(">")) {
		return BlockBlockquote
	}
	if isListMarker(trimmed) {
		return BlockList
	}
	if bytes.HasPrefix(trimmed, []byte("<")) {
		return BlockHTML
	}
	return BlockParagraph
}

func consumeFenceOrMath(lines []line, i int, kind BlockKind) int {
	trimmed := trimLine(lines[i].text)
	if kind == BlockMath && isDisplayMathStart(trimmed) {
		return consumeDisplayMath(lines, i)
	}

	marker := fenceMarker(trimmed)
	if marker == "" {
		return i + 1
	}
	for j := i + 1; j < len(lines); j++ {
		if isFenceClose(trimLine(lines[j].text), marker) {
			return j + 1
		}
	}
	return len(lines)
}

func consumeDisplayMath(lines []line, i int) int {
	trimmed := trimLine(lines[i].text)
	if bytes.HasPrefix(trimmed, []byte("$$")) {
		if bytes.Count(trimmed, []byte("$$")) >= 2 && len(bytes.TrimSpace(trimmed[2:])) > 0 {
			return i + 1
		}
		for j := i + 1; j < len(lines); j++ {
			if bytes.HasPrefix(trimLine(lines[j].text), []byte("$$")) {
				return j + 1
			}
		}
		return len(lines)
	}

	if bytes.HasPrefix(trimmed, []byte(`\[`)) {
		if bytes.Contains(trimmed[2:], []byte(`\]`)) {
			return i + 1
		}
		for j := i + 1; j < len(lines); j++ {
			if bytes.Contains(trimLine(lines[j].text), []byte(`\]`)) {
				return j + 1
			}
		}
		return len(lines)
	}

	return i + 1
}

func consumeTable(lines []line, i int) int {
	j := i + 2
	for j < len(lines) {
		trimmed := trimLine(lines[j].text)
		if isBlank(lines[j].text) || !tablefix.LooksLikeTableContinuation(string(trimmed)) {
			break
		}
		j++
	}
	return j
}

func isTableLikeStart(lines []line, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	return tablefix.LooksLikeTableStart(string(trimLine(lines[i].text)), string(trimLine(lines[i+1].text)))
}

func consumeUntilBlank(lines []line, i int) int {
	j := i + 1
	for j < len(lines) && !isBlank(lines[j].text) {
		j++
	}
	return j
}

func consumeParagraph(lines []line, i int) int {
	j := i + 1
	for j < len(lines) {
		if isBlank(lines[j].text) {
			break
		}
		kind := classifyStart(lines, j)
		if kind != BlockParagraph {
			break
		}
		j++
	}
	return j
}

func isBlank(text []byte) bool {
	return len(bytes.TrimSpace(text)) == 0
}

func trimLine(text []byte) []byte {
	return bytes.TrimSpace(text)
}

func isATXHeading(line []byte) bool {
	if len(line) == 0 || line[0] != '#' {
		return false
	}
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	return count > 0 && count <= 6 && (count == len(line) || line[count] == ' ' || line[count] == '\t')
}

func isSetextHeading(lines []line, i int) bool {
	if i+1 >= len(lines) || isBlank(lines[i].text) {
		return false
	}
	next := trimLine(lines[i+1].text)
	if len(next) == 0 {
		return false
	}
	marker := next[0]
	if marker != '=' && marker != '-' {
		return false
	}
	for _, b := range next {
		if b != marker && b != ' ' && b != '\t' {
			return false
		}
	}
	return len(next) >= 1
}

func isThematicBreak(line []byte) bool {
	if len(line) < 3 {
		return false
	}
	marker := line[0]
	if marker != '-' && marker != '*' && marker != '_' {
		return false
	}
	count := 0
	for _, b := range line {
		if b == marker {
			count++
			continue
		}
		if b != ' ' && b != '\t' {
			return false
		}
	}
	return count >= 3
}

func isFenceOpen(line []byte) bool {
	return mdfence.IsOpen(line)
}

func fenceMarker(line []byte) string {
	return mdfence.Marker(line)
}

func fenceInfo(line []byte) string {
	return strings.ToLower(mdfence.Info(line))
}

func isFenceClose(line []byte, marker string) bool {
	return mdfence.IsClose(line, marker)
}

func isDisplayMathStart(line []byte) bool {
	return bytes.HasPrefix(line, []byte("$$")) || bytes.HasPrefix(line, []byte(`\[`))
}

func isListMarker(line []byte) bool {
	if len(line) < 2 {
		return false
	}
	if (line[0] == '-' || line[0] == '+' || line[0] == '*') && isSpace(line[1]) {
		return true
	}
	idx := 0
	for idx < len(line) && line[idx] >= '0' && line[idx] <= '9' {
		idx++
	}
	return idx > 0 && idx < len(line)-1 && (line[idx] == '.' || line[idx] == ')') && isSpace(line[idx+1])
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t'
}

type diffMatch struct {
	oldIndex int
	newIndex int
}

func lcsMatches(oldBlocks, newBlocks []Block) []diffMatch {
	n := len(oldBlocks)
	m := len(newBlocks)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}

	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if sameBlock(oldBlocks[i], newBlocks[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var matches []diffMatch
	for i, j := 0, 0; i < n && j < m; {
		if sameBlock(oldBlocks[i], newBlocks[j]) {
			matches = append(matches, diffMatch{oldIndex: i, newIndex: j})
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return matches
}

func diffMatrixTooLarge(oldCount, newCount int) bool {
	return oldCount > 0 && newCount > maxDiffMatrixCells/oldCount
}

func sameBlock(a, b Block) bool {
	return a.Kind == b.Kind && a.HTML == b.HTML && sameDiagnostics(a.Diagnostics, b.Diagnostics)
}

func sameDiagnostics(a, b []Diagnostic) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Severity != b[i].Severity ||
			a[i].Code != b[i].Code ||
			a[i].Message != b[i].Message ||
			a[i].StartLine != b[i].StartLine ||
			a[i].EndLine != b[i].EndLine {
			return false
		}
	}
	return true
}

func patchOps(oldBlocks, newBlocks []Block, matches []diffMatch) ([]PatchOp, bool) {
	if len(oldBlocks) == 0 && len(newBlocks) > 0 {
		return nil, true
	}

	var ops []PatchOp
	prevOld := -1
	prevNew := -1

	flushGap := func(oldEnd, newEnd int) bool {
		gapOps, reset := gapPatchOps(oldBlocks, newBlocks, prevOld+1, oldEnd, prevNew+1, newEnd)
		if reset {
			return true
		}
		ops = append(ops, gapOps...)
		return false
	}

	for _, match := range matches {
		if flushGap(match.oldIndex, match.newIndex) {
			return nil, true
		}
		prevOld = match.oldIndex
		prevNew = match.newIndex
	}

	if flushGap(len(oldBlocks), len(newBlocks)) {
		return nil, true
	}

	return ops, false
}

func gapPatchOps(oldBlocks, newBlocks []Block, oldStart, oldEnd, newStart, newEnd int) ([]PatchOp, bool) {
	oldCount := oldEnd - oldStart
	newCount := newEnd - newStart
	if oldCount == 0 && newCount == 0 {
		return nil, false
	}

	if oldCount == 0 {
		return insertOps(oldBlocks, newBlocks, oldStart, newStart, newEnd)
	}
	if newCount == 0 {
		ops := make([]PatchOp, 0, oldCount)
		for i := oldStart; i < oldEnd; i++ {
			ops = append(ops, PatchOp{Op: OpDelete, ID: oldBlocks[i].ID})
		}
		return ops, false
	}

	shared := oldCount
	if newCount < shared {
		shared = newCount
	}

	ops := make([]PatchOp, 0, oldCount+newCount)
	for i := 0; i < shared; i++ {
		ops = append(ops, PatchOp{
			Op:   OpReplace,
			ID:   oldBlocks[oldStart+i].ID,
			HTML: newBlocks[newStart+i].WrappedHTML(),
		})
	}
	for i := oldStart + shared; i < oldEnd; i++ {
		ops = append(ops, PatchOp{Op: OpDelete, ID: oldBlocks[i].ID})
	}
	if newStart+shared < newEnd {
		anchor := newBlocks[newStart+shared-1].ID
		for i := newStart + shared; i < newEnd; i++ {
			ops = append(ops, PatchOp{
				Op:    OpInsertAfter,
				ID:    newBlocks[i].ID,
				After: anchor,
				HTML:  newBlocks[i].WrappedHTML(),
			})
			anchor = newBlocks[i].ID
		}
	}
	return ops, false
}

func insertOps(oldBlocks, newBlocks []Block, oldStart, newStart, newEnd int) ([]PatchOp, bool) {
	if newStart >= newEnd {
		return nil, false
	}
	ops := make([]PatchOp, 0, newEnd-newStart)

	if oldStart > 0 {
		anchor := oldBlocks[oldStart-1].ID
		for i := newStart; i < newEnd; i++ {
			ops = append(ops, PatchOp{
				Op:    OpInsertAfter,
				ID:    newBlocks[i].ID,
				After: anchor,
				HTML:  newBlocks[i].WrappedHTML(),
			})
			anchor = newBlocks[i].ID
		}
		return ops, false
	}

	if oldStart < len(oldBlocks) {
		before := oldBlocks[oldStart].ID
		for i := newStart; i < newEnd; i++ {
			ops = append(ops, PatchOp{
				Op:     OpInsertBefore,
				ID:     newBlocks[i].ID,
				Before: before,
				HTML:   newBlocks[i].WrappedHTML(),
			})
		}
		return ops, false
	}

	return nil, true
}
