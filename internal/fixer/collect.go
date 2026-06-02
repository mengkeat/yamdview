package fixer

import (
	"bytes"
	"fmt"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/mathfix"
)

// CollectTablePatches returns SourcePatches for every block whose rendered
// source differs from the original slice because of a heuristic table repair.
// Blocks that were rendered unchanged, or that produced diagnostics without
// modifying the source, are not returned.
func CollectTablePatches(snapshot document.DocumentSnapshot) []SourcePatch {
	var patches []SourcePatch
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
	if !mathfix.HasUnicodeMath(string(src)) && !hasTextFenceCandidate(src) {
		return nil, nil
	}
	preprocessed := mathfix.Preprocess(src)
	if bytes.Equal(src, preprocessed) {
		return nil, nil
	}
	return diffLineRanges(src, preprocessed, "heuristic math conversion", SourceHeuristicMath, 0.8), nil
}

// hasTextFenceCandidate mirrors the relevant guard from mathfix.Preprocess so
// the CollectMathPatches caller can short-circuit the diff when no work would
// happen. The detection logic intentionally matches mathfix to avoid false
// negatives.
func hasTextFenceCandidate(src []byte) bool {
	for _, line := range bytes.Split(src, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		// Cheap fence marker check: must start with three backticks or tildes.
		if len(trimmed) < 3 {
			continue
		}
		c := trimmed[0]
		if c != '`' && c != '~' {
			continue
		}
		if trimmed[0] != trimmed[1] || trimmed[1] != trimmed[2] {
			continue
		}
		// Detect a ```text / ~~~text info string.
		info := bytes.TrimLeft(trimmed, "`~")
		info = bytes.TrimLeft(info, " \t")
		if len(info) == 0 {
			continue
		}
		// First whitespace-separated token is the info string.
		for i := 0; i < len(info); i++ {
			if info[i] == ' ' || info[i] == '\t' {
				info = info[:i]
				break
			}
		}
		lower := string(bytes.ToLower(info))
		if lower == "text" || lower == "txt" || lower == "math" {
			return true
		}
	}
	return false
}

// diffLineRanges computes a line-based diff between original and modified
// sources and returns one SourcePatch per contiguous changed line range.
// Identical line ranges are not emitted.
func diffLineRanges(original, modified []byte, reason string, source PatchSource, confidence float64) []SourcePatch {
	oldLines := splitLinesKeep(original)
	newLines := splitLinesKeep(modified)
	oldOffsets := lineOffsets(original, oldLines)
	newOffsets := lineOffsets(modified, newLines)

	matches := lcsMatches(oldLines, newLines)
	matchedOld := make([]bool, len(oldLines))
	matchedNew := make([]bool, len(newLines))
	for _, m := range matches {
		matchedOld[m.old] = true
		matchedNew[m.new] = true
	}

	var patches []SourcePatch
	oldIdx, newIdx := 0, 0
	for oldIdx < len(oldLines) || newIdx < len(newLines) {
		// Skip matched lines on both sides.
		for oldIdx < len(oldLines) && matchedOld[oldIdx] {
			oldIdx++
		}
		for newIdx < len(newLines) && matchedNew[newIdx] {
			newIdx++
		}
		if oldIdx >= len(oldLines) && newIdx >= len(newLines) {
			break
		}
		// Find the end of the next changed run on each side.
		oldEnd := oldIdx
		for oldEnd < len(oldLines) && !matchedOld[oldEnd] {
			oldEnd++
		}
		newEnd := newIdx
		for newEnd < len(newLines) && !matchedNew[newEnd] {
			newEnd++
		}
		patch, err := buildLinePatch(original, modified, oldLines, newLines, oldOffsets, newOffsets, oldIdx, oldEnd, newIdx, newEnd, reason, source, confidence)
		if err != nil {
			// Fall back to skipping this run rather than failing the whole diff.
			oldIdx = oldEnd
			newIdx = newEnd
			continue
		}
		patches = append(patches, patch)
		oldIdx = oldEnd
		newIdx = newEnd
	}
	return patches
}

func buildLinePatch(
	original, modified []byte,
	oldLines, newLines []string,
	oldOffsets, newOffsets []int,
	oldStart, oldEnd, newStart, newEnd int,
	reason string,
	source PatchSource,
	confidence float64,
) (SourcePatch, error) {
	if oldStart == oldEnd && newStart == newEnd {
		return SourcePatch{}, fmt.Errorf("empty changed run")
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

// lcsMatches is a simple line-level longest-common-subsequence match finder
// over potentially multi-line strings. Memory is O(n*m); callers are expected
// to bound the inputs upstream.
func lcsMatches(oldLines, newLines []string) []lineMatch {
	n, m := len(oldLines), len(newLines)
	if n == 0 || m == 0 {
		return nil
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var matches []lineMatch
	i, j := 0, 0
	for i < n && j < m {
		if oldLines[i] == newLines[j] {
			matches = append(matches, lineMatch{old: i, new: j})
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

func lineOffsets(src []byte, lines []string) []int {
	offsets := make([]int, len(lines))
	pos := 0
	for i, l := range lines {
		_ = src
		offsets[i] = pos
		pos += len(l)
	}
	return offsets
}
