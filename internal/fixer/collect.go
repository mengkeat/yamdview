package fixer

import (
	"bytes"
	"fmt"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/mathfix"
	"github.com/mengkeat/yamdview/internal/tablefix"
)

// CollectTablePatches returns SourcePatches for every block whose rendered
// source differs from the original slice because of a heuristic table repair.
// Blocks that were rendered unchanged, or that produced diagnostics without
// modifying the source, are not returned.
func CollectTablePatches(snapshot document.DocumentSnapshot) []SourcePatch {
	patches := make([]SourcePatch, 0, len(snapshot.Blocks))
	for _, b := range snapshot.Blocks {
		if !b.WasNormalized() {
			continue
		}
		if b.Kind != document.BlockTable {
			// Non-table repairs (e.g. math) are collected separately.
			continue
		}
		patches = append(patches, SourcePatch{
			StartByte:  b.SourceStart,
			EndByte:    b.SourceEnd,
			OldText:    b.Source,
			NewText:    b.Normalized,
			Reason:     "heuristic table repair",
			Confidence: 0.9,
			Source:     SourceHeuristicTable,
			BlockID:    b.ID,
		})
	}
	return patches
}

// CollectMathPatches runs the deterministic math preprocessor on the supplied
// source and returns SourcePatches describing every line range that changed.
// The result is empty when the preprocessor is a no-op for this source.
func CollectMathPatches(src []byte) ([]SourcePatch, error) {
	if len(src) == 0 {
		return nil, nil
	}
	if !mathfix.HasUnicodeMath(string(src)) && !mathfix.HasTextFenceCandidate(src) {
		return nil, nil
	}
	preprocessed := mathfix.Preprocess(src)
	if bytes.Equal(src, preprocessed) {
		return nil, nil
	}
	return diffLineRanges(src, preprocessed, "heuristic math conversion", SourceHeuristicMath, 0.8), nil
}

// CollectDocumentPatches returns a non-overlapping patch set for all
// deterministic source repairs. Table repairs come from the rendered block
// snapshot, which records exactly what the renderer produced, so the table
// preprocessing pass is not re-run. Math conversion is recomputed with a
// mathfix pass because the snapshot does not capture it. The result composes
// table repair before math conversion, matching the renderer pipeline, then
// diffs the original source against the final repaired source. The returned
// counts describe how many table and math candidate ranges contributed to
// the final patch set.
func CollectDocumentPatches(src []byte, snapshot document.DocumentSnapshot) ([]SourcePatch, int, int, error) {
	if len(src) == 0 {
		return nil, 0, 0, nil
	}

	var tablePatches []SourcePatch
	var tableFixed []byte
	if snapshot.FullResetOnly {
		// Full-reset documents (reference definitions) are rendered as a
		// single unit and carry no per-block snapshot, so fall back to
		// whole-document table preprocessing, matching the renderer's
		// full-document path exactly.
		tableFixed = tablefix.Preprocess(src)
		tablePatches = collectTablePatchesFromPreprocess(src, tableFixed)
	} else {
		tablePatches = CollectTablePatches(snapshot)
		tableFixed = Apply(src, tablePatches)
	}

	mathPatches, err := CollectMathPatches(tableFixed)
	if err != nil {
		return nil, len(tablePatches), 0, err
	}

	final := tableFixed
	if len(mathPatches) > 0 {
		if err := ValidatePatches(tableFixed, mathPatches); err != nil {
			// If the intermediate line diff cannot be represented as independently
			// valid patches, still compose the renderer-equivalent final source and
			// let the original→final diff below produce a safe fallback patch.
			final = mathfix.Preprocess(tableFixed)
		} else {
			final = Apply(tableFixed, mathPatches)
		}
	}

	if bytes.Equal(src, final) {
		return nil, len(tablePatches), len(mathPatches), nil
	}

	patches := diffLineRanges(src, final, "heuristic source repair", SourceHeuristicBoth, 0.8)
	annotateDocumentPatches(patches, tablePatches, len(mathPatches) > 0)
	if len(patches) == 0 {
		patches = []SourcePatch{wholeDocumentPatch(src, final, len(tablePatches), len(mathPatches))}
	} else if err := ValidatePatches(src, patches); err != nil {
		patches = []SourcePatch{wholeDocumentPatch(src, final, len(tablePatches), len(mathPatches))}
	}
	return patches, len(tablePatches), len(mathPatches), nil
}

func collectTablePatchesFromPreprocess(original, tableFixed []byte) []SourcePatch {
	if bytes.Equal(original, tableFixed) {
		return nil
	}
	return diffLineRanges(original, tableFixed, "heuristic table repair", SourceHeuristicTable, 0.9)
}

func annotateDocumentPatches(patches, tablePatches []SourcePatch, hasMath bool) {
	for i := range patches {
		overlapsTable := overlapsAny(patches[i], tablePatches)
		switch {
		case overlapsTable && hasMath:
			patches[i].Source = SourceHeuristicBoth
			patches[i].Reason = "heuristic table repair and math conversion"
			patches[i].Confidence = 0.8
		case overlapsTable:
			patches[i].Source = SourceHeuristicTable
			patches[i].Reason = "heuristic table repair"
			patches[i].Confidence = 0.9
		case hasMath:
			patches[i].Source = SourceHeuristicMath
			patches[i].Reason = "heuristic math conversion"
			patches[i].Confidence = 0.8
		}
	}
}

func overlapsAny(patch SourcePatch, candidates []SourcePatch) bool {
	for _, candidate := range candidates {
		if patch.StartByte < candidate.EndByte && candidate.StartByte < patch.EndByte {
			return true
		}
	}
	return false
}

func wholeDocumentPatch(original, final []byte, tableCount, mathCount int) SourcePatch {
	source := SourceHeuristicBoth
	reason := "heuristic table repair and math conversion"
	confidence := 0.8
	switch {
	case tableCount > 0 && mathCount == 0:
		source = SourceHeuristicTable
		reason = "heuristic table repair"
		confidence = 0.9
	case tableCount == 0 && mathCount > 0:
		source = SourceHeuristicMath
		reason = "heuristic math conversion"
	}
	return SourcePatch{
		StartByte:  0,
		EndByte:    len(original),
		OldText:    string(original),
		NewText:    string(final),
		Reason:     reason,
		Confidence: confidence,
		Source:     source,
	}
}

// diffLineRanges computes a line-based diff between original and modified
// sources and returns one SourcePatch per contiguous changed line range.
// Identical line ranges are not emitted.
func diffLineRanges(original, modified []byte, reason string, source PatchSource, confidence float64) []SourcePatch {
	oldLines := splitLinesKeep(original)
	newLines := splitLinesKeep(modified)
	oldOffsets := lineOffsets(oldLines)

	matches := lcsMatches(oldLines, newLines)
	var patches []SourcePatch
	oldIdx, newIdx := 0, 0
	for _, m := range matches {
		if oldIdx < m.old || newIdx < m.new {
			patch, err := buildLinePatch(original, oldLines, newLines, oldOffsets, oldIdx, m.old, newIdx, m.new, reason, source, confidence)
			if err == nil {
				patches = append(patches, patch)
			}
		}
		oldIdx = m.old + 1
		newIdx = m.new + 1
	}

	if oldIdx < len(oldLines) || newIdx < len(newLines) {
		patch, err := buildLinePatch(original, oldLines, newLines, oldOffsets, oldIdx, len(oldLines), newIdx, len(newLines), reason, source, confidence)
		if err == nil {
			patches = append(patches, patch)
		}
	}
	return patches
}

func buildLinePatch(
	original []byte,
	oldLines, newLines []string,
	oldOffsets []int,
	oldStart, oldEnd, newStart, newEnd int,
	reason string,
	source PatchSource,
	confidence float64,
) (SourcePatch, error) {
	if oldStart == oldEnd && newStart == newEnd {
		return SourcePatch{}, fmt.Errorf("empty changed run")
	}
	if oldStart == oldEnd {
		// Pure insertions cannot be validated safely with empty OldText. Expand
		// the replacement to include an adjacent unchanged line as an anchor.
		switch {
		case oldEnd < len(oldLines) && newEnd < len(newLines):
			oldEnd++
			newEnd++
		case oldStart > 0 && newStart > 0:
			oldStart--
			newStart--
		default:
			return SourcePatch{}, fmt.Errorf("insertion has no anchor line")
		}
	}
	oldStartByte, oldEndByte := lineRangeBytes(original, oldLines, oldOffsets, oldStart, oldEnd)
	newText := joinLines(newLines, newStart, newEnd)
	oldText := string(original[oldStartByte:oldEndByte])
	return SourcePatch{
		StartByte:  oldStartByte,
		EndByte:    oldEndByte,
		OldText:    oldText,
		NewText:    newText,
		Reason:     reason,
		Confidence: confidence,
		Source:     source,
	}, nil
}

func lineRangeBytes(src []byte, lines []string, offsets []int, startLine, endLine int) (int, int) {
	start := offsets[startLine]
	if endLine >= len(lines) {
		return start, len(src)
	}
	return start, offsets[endLine]
}

func joinLines(lines []string, start, end int) string {
	if start >= end {
		return ""
	}
	var b []byte
	for i := start; i < end; i++ {
		b = append(b, lines[i]...)
	}
	return string(b)
}

type lineMatch struct {
	old int
	new int
}

// maxDiffMatrixCells bounds the LCS matrix so pathologically large diffs
// degrade to a single whole-range patch instead of a large allocation.
const maxDiffMatrixCells = 250_000

// lcsMatches is a simple line-level longest-common-subsequence match finder
// over potentially multi-line strings. Memory is O(n*m); when the matrix
// would exceed maxDiffMatrixCells it returns no matches, which causes the
// caller to emit a single whole-range patch.
func lcsMatches(oldLines, newLines []string) []lineMatch {
	n, m := len(oldLines), len(newLines)
	if n == 0 || m == 0 || n*m > maxDiffMatrixCells {
		return nil
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case oldLines[i] == newLines[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var matches []lineMatch
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			matches = append(matches, lineMatch{old: i, new: j})
			i++
			j++
			continue
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return matches
}

func splitLinesKeep(s []byte) []string {
	if len(s) == 0 {
		return nil
	}
	var lines []string
	start := 0
	for start < len(s) {
		idx := bytes.IndexByte(s[start:], '\n')
		if idx < 0 {
			lines = append(lines, string(s[start:]))
			return lines
		}
		end := start + idx + 1
		lines = append(lines, string(s[start:end]))
		start = end
	}
	return lines
}

func lineOffsets(lines []string) []int {
	offsets := make([]int, len(lines))
	pos := 0
	for i, l := range lines {
		offsets[i] = pos
		pos += len(l)
	}
	return offsets
}
