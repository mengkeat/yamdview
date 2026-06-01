// Package tablefix detects and repairs simple Markdown tables for render-only use.
package tablefix

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/mengkeat/yamdview/internal/mdfence"
)

const (
	CodeAmbiguous = "table.ambiguous"
	CodeUnfixable = "table.unfixable"
)

// Diagnostic describes why a table-like block could not be safely repaired.
type Diagnostic struct {
	Severity string
	Code     string
	Message  string
}

// Result is the outcome of table detection and deterministic repair.
type Result struct {
	Original    string
	Markdown    string
	Applied     bool
	TableLike   bool
	Diagnostics []Diagnostic
}

type alignment string

const (
	alignNone   alignment = ""
	alignLeft   alignment = "left"
	alignRight  alignment = "right"
	alignCenter alignment = "center"
)

type row struct {
	raw           string
	cells         []string
	separatorLike bool
	separator     bool
	alignments    []alignment
}

// Fix detects a table-like Markdown block and returns a repaired Markdown table
// when the repair is deterministic. The source is never modified in place.
func Fix(source string) Result {
	result := Result{Original: source, Markdown: source}
	body, trailing := splitTrailingNewlines(source)
	if strings.TrimSpace(body) == "" {
		return result
	}

	lines := strings.Split(body, "\n")
	if len(lines) < 2 {
		return result
	}

	rows := make([]row, 0, len(lines))
	pipeRows := 0
	for _, line := range lines {
		parsed := parseRow(line)
		if len(parsed.cells) >= 2 {
			pipeRows++
		}
		rows = append(rows, parsed)
	}

	if pipeRows < 2 {
		return result
	}
	result.TableLike = true

	if rows[0].separatorLike {
		result.Diagnostics = append(result.Diagnostics, warning(CodeUnfixable, "table-like block starts with a separator row"))
		return result
	}

	if len(rows[0].cells) < 2 {
		result.Diagnostics = append(result.Diagnostics, warning(CodeAmbiguous, "table header row has fewer than two cells"))
		return result
	}

	if len(rows) > 1 && rows[1].separatorLike {
		return repairWithSeparator(result, rows, trailing)
	}

	return repairMissingSeparator(result, rows, trailing)
}

// Preprocess repairs obvious table-like blocks in a full Markdown document.
// Fenced code blocks are preserved. Repairs are render-only; callers decide
// whether to use the returned bytes for rendering.
func Preprocess(src []byte) []byte {
	if len(src) == 0 || !bytes.Contains(src, []byte("|")) {
		return src
	}

	lines := splitLinesKeep(string(src))
	out := make([]string, 0, len(lines))
	applied := false
	inFence := false
	fenceMarker := ""

	for i := 0; i < len(lines); {
		lineText := trimLineEnding(lines[i])
		trimmed := strings.TrimSpace(lineText)

		if isFenceOpen(trimmed) {
			out = append(out, lines[i])
			if !inFence {
				inFence = true
				fenceMarker = fenceMarkerPrefix(trimmed)
			} else if isFenceClose(trimmed, fenceMarker) {
				inFence = false
				fenceMarker = ""
			}
			i++
			continue
		}

		if inFence || !LooksLikeTableContinuation(lineText) {
			out = append(out, lines[i])
			i++
			continue
		}

		j := i
		for j < len(lines) {
			candidate := trimLineEnding(lines[j])
			if strings.TrimSpace(candidate) == "" || !LooksLikeTableContinuation(candidate) {
				break
			}
			j++
		}

		if j-i < 2 {
			out = append(out, lines[i])
			i++
			continue
		}

		block := strings.Join(lines[i:j], "")
		fixed := Fix(block)
		if fixed.TableLike && fixed.Applied {
			out = append(out, fixed.Markdown)
			applied = true
			i = j
			continue
		}

		out = append(out, lines[i:j]...)
		i = j
	}

	if !applied {
		return src
	}
	return []byte(strings.Join(out, ""))
}

// LooksLikeTableStart reports whether two adjacent lines look like the start of
// a pipe-delimited Markdown table, including common missing-separator forms.
func LooksLikeTableStart(current, next string) bool {
	first := parseRow(current)
	second := parseRow(next)
	if first.separatorLike || len(first.cells) < 2 {
		return false
	}
	return second.separatorLike || len(second.cells) >= 2
}

// LooksLikeTableContinuation reports whether a line can continue a pipe table.
func LooksLikeTableContinuation(line string) bool {
	parsed := parseRow(line)
	return parsed.separatorLike || len(parsed.cells) >= 2
}

func repairWithSeparator(result Result, rows []row, trailing string) Result {
	headerCols := len(rows[0].cells)
	targetCols := maxColumns(rows)
	if targetCols < headerCols {
		targetCols = headerCols
	}
	if targetCols < 2 {
		result.Diagnostics = append(result.Diagnostics, warning(CodeAmbiguous, "table has fewer than two columns"))
		return result
	}

	if !columnCountsPlausible(rows, targetCols) {
		result.Diagnostics = append(result.Diagnostics, warning(CodeAmbiguous, "table rows have inconsistent column counts"))
		return result
	}

	if rows[1].separator && allRowsHaveColumns(rows, targetCols) && !needsCodePipeEscapes(rows) {
		return result
	}

	aligns := make([]alignment, targetCols)
	copy(aligns, rows[1].alignments)
	normalized := normalizedRows(rows, targetCols)
	result.Markdown = renderRows(normalized, aligns, trailing)
	result.Applied = true
	return result
}

func repairMissingSeparator(result Result, rows []row, trailing string) Result {
	if !obviousPipeTable(rows) {
		result.Diagnostics = append(result.Diagnostics, warning(CodeAmbiguous, "pipe-delimited lines are not clearly a table"))
		return result
	}

	targetCols := maxColumns(rows)
	if targetCols < 2 {
		result.Diagnostics = append(result.Diagnostics, warning(CodeAmbiguous, "table has fewer than two columns"))
		return result
	}
	if !columnCountsPlausible(rows, targetCols) {
		result.Diagnostics = append(result.Diagnostics, warning(CodeAmbiguous, "table rows have inconsistent column counts"))
		return result
	}

	aligns := make([]alignment, targetCols)
	normalized := normalizedRows(rows, targetCols)
	result.Markdown = renderRows(normalized, aligns, trailing)
	result.Applied = true
	return result
}

func normalizedRows(rows []row, targetCols int) [][]string {
	normalized := make([][]string, 0, len(rows))
	for i, r := range rows {
		if i == 1 && r.separatorLike {
			continue
		}
		cells := append([]string(nil), r.cells...)
		for len(cells) < targetCols {
			cells = append(cells, "")
		}
		normalized = append(normalized, cells[:targetCols])
	}
	return normalized
}

func renderRows(rows [][]string, aligns []alignment, trailing string) string {
	var b strings.Builder
	for i, cells := range rows {
		if i == 1 {
			renderSeparator(&b, len(cells), aligns)
		}
		renderDataRow(&b, cells)
	}
	out := strings.TrimSuffix(b.String(), "\n")
	return out + trailing
}

func renderDataRow(b *strings.Builder, cells []string) {
	b.WriteString("|")
	for _, cell := range cells {
		b.WriteByte(' ')
		b.WriteString(escapePipesInCodeSpans(strings.TrimSpace(cell)))
		b.WriteString(" |")
	}
	b.WriteByte('\n')
}

func escapePipesInCodeSpans(cell string) string {
	var b strings.Builder
	inCode := false
	escaped := false
	for _, r := range cell {
		if r == '`' && !escaped {
			inCode = !inCode
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '|' && inCode && !escaped {
			b.WriteRune('\\')
			b.WriteRune(r)
			escaped = false
			continue
		}
		b.WriteRune(r)
		escaped = r == '\\' && !escaped
		if r != '\\' {
			escaped = false
		}
	}
	return b.String()
}

func needsCodePipeEscapes(rows []row) bool {
	for _, row := range rows {
		if row.separatorLike {
			continue
		}
		for _, cell := range row.cells {
			if hasUnescapedPipeInCodeSpan(cell) {
				return true
			}
		}
	}
	return false
}

func hasUnescapedPipeInCodeSpan(cell string) bool {
	inCode := false
	escaped := false
	for _, r := range cell {
		if r == '`' && !escaped {
			inCode = !inCode
			escaped = false
			continue
		}
		if r == '|' && inCode && !escaped {
			return true
		}
		escaped = r == '\\' && !escaped
		if r != '\\' {
			escaped = false
		}
	}
	return false
}

func renderSeparator(b *strings.Builder, cols int, aligns []alignment) {
	b.WriteString("|")
	for i := 0; i < cols; i++ {
		cell := "---"
		if i < len(aligns) {
			switch aligns[i] {
			case alignLeft:
				cell = ":---"
			case alignRight:
				cell = "---:"
			case alignCenter:
				cell = ":---:"
			}
		}
		b.WriteByte(' ')
		b.WriteString(cell)
		b.WriteString(" |")
	}
	b.WriteByte('\n')
}

func maxColumns(rows []row) int {
	max := 0
	for _, row := range rows {
		if row.separatorLike {
			continue
		}
		if len(row.cells) > max {
			max = len(row.cells)
		}
	}
	return max
}

func allRowsHaveColumns(rows []row, cols int) bool {
	for _, row := range rows {
		if len(row.cells) != cols {
			return false
		}
	}
	return true
}

func columnCountsPlausible(rows []row, targetCols int) bool {
	for _, row := range rows {
		if row.separatorLike {
			continue
		}
		if len(row.cells) < 2 || len(row.cells) > targetCols {
			return false
		}
		if targetCols-len(row.cells) > 1 {
			return false
		}
	}
	return true
}

func obviousPipeTable(rows []row) bool {
	if len(rows) >= 3 {
		return true
	}
	for _, row := range rows {
		trimmed := strings.TrimSpace(row.raw)
		if strings.HasPrefix(trimmed, "|") || hasTrailingPipe(trimmed) {
			return true
		}
	}
	return headerish(rows[0].cells) && shortCells(rows[1].cells)
}

func headerish(cells []string) bool {
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if cell == "" || len([]rune(cell)) > 32 {
			return false
		}
		if strings.ContainsAny(cell, ".?!") {
			return false
		}
		if wordCount(cell) > 4 {
			return false
		}
	}
	return true
}

func shortCells(cells []string) bool {
	for _, cell := range cells {
		if len([]rune(strings.TrimSpace(cell))) > 48 {
			return false
		}
	}
	return true
}

func wordCount(s string) int {
	return len(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-' || r == '_' || r == '/'
	}))
}

func parseRow(line string) row {
	trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
	cells := splitPipeCells(trimmed)
	separatorLike, separator, aligns := parseSeparator(cells)
	return row{
		raw:           line,
		cells:         cells,
		separatorLike: separatorLike,
		separator:     separator,
		alignments:    aligns,
	}
}

func splitPipeCells(line string) []string {
	if !hasTablePipe(line) {
		return nil
	}

	var cells []string
	start := 0
	inCode := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '`' {
			inCode = !inCode
			continue
		}
		if r == '|' && !inCode {
			cells = append(cells, strings.TrimSpace(line[start:i]))
			start = i + len("|")
		}
	}
	cells = append(cells, strings.TrimSpace(line[start:]))

	if len(cells) > 0 && cells[0] == "" && strings.HasPrefix(strings.TrimSpace(line), "|") {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" && hasTrailingPipe(line) {
		cells = cells[:len(cells)-1]
	}
	return cells
}

func hasTablePipe(line string) bool {
	inCode := false
	escaped := false
	for _, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '`' {
			inCode = !inCode
			continue
		}
		if r == '|' && !inCode {
			return true
		}
	}
	return false
}

func hasTrailingPipe(line string) bool {
	trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
	if !strings.HasSuffix(trimmed, "|") {
		return false
	}
	backslashes := 0
	for i := len(trimmed) - 2; i >= 0 && trimmed[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 0
}

func parseSeparator(cells []string) (bool, bool, []alignment) {
	if len(cells) == 0 {
		return false, false, nil
	}

	aligns := make([]alignment, 0, len(cells))
	allLike := true
	allValid := true
	for _, cell := range cells {
		like, valid, align := parseSeparatorCell(cell)
		if !like {
			allLike = false
		}
		if !valid {
			allValid = false
		}
		aligns = append(aligns, align)
	}
	return allLike, allLike && allValid, aligns
}

func parseSeparatorCell(cell string) (bool, bool, alignment) {
	trimmed := strings.TrimSpace(cell)
	if trimmed == "" {
		return false, false, alignNone
	}

	leftColon := strings.HasPrefix(trimmed, ":")
	rightColon := strings.HasSuffix(trimmed, ":")
	inner := strings.Trim(trimmed, ":")
	if inner == "" {
		return false, false, alignNone
	}

	hyphens := 0
	for _, r := range inner {
		if r != '-' {
			return false, false, alignNone
		}
		hyphens++
	}

	align := alignNone
	switch {
	case leftColon && rightColon:
		align = alignCenter
	case leftColon:
		align = alignLeft
	case rightColon:
		align = alignRight
	}

	return true, hyphens >= 3, align
}

func splitTrailingNewlines(s string) (string, string) {
	idx := len(s)
	for idx > 0 && s[idx-1] == '\n' {
		idx--
	}
	return s[:idx], s[idx:]
}

func splitLinesKeep(s string) []string {
	if s == "" {
		return nil
	}
	lines := make([]string, 0, strings.Count(s, "\n")+1)
	start := 0
	for start < len(s) {
		idx := strings.IndexByte(s[start:], '\n')
		if idx < 0 {
			lines = append(lines, s[start:])
			break
		}
		end := start + idx + 1
		lines = append(lines, s[start:end])
		start = end
	}
	return lines
}

func trimLineEnding(s string) string {
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s
}

func isFenceOpen(line string) bool {
	return mdfence.IsOpen([]byte(line))
}

func isFenceClose(line, marker string) bool {
	return mdfence.IsClose([]byte(line), marker)
}

func fenceMarkerPrefix(line string) string {
	return mdfence.Marker([]byte(line))
}

func warning(code, message string) Diagnostic {
	return Diagnostic{Severity: "warning", Code: code, Message: message}
}
