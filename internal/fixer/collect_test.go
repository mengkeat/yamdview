package fixer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/markdown"
	"github.com/mengkeat/yamdview/internal/tablefix"
)

func TestCollectTablePatchesFromMalformedTable(t *testing.T) {
	src := "Name | Score\nAlice | 10\nBob | 9\n"
	snapshot := buildSnapshot(t, src)
	patches := CollectTablePatches(snapshot)

	if len(patches) != 1 {
		t.Fatalf("expected 1 table patch, got %d", len(patches))
	}
	p := patches[0]
	if p.Source != SourceHeuristicTable {
		t.Errorf("Source = %q, want %q", p.Source, SourceHeuristicTable)
	}
	if p.OldText != src {
		t.Errorf("OldText = %q, want %q", p.OldText, src)
	}
	if !strings.Contains(p.NewText, "| --- |") {
		t.Errorf("NewText missing repaired separator: %q", p.NewText)
	}
	if p.StartByte != 0 || p.EndByte != len(src) {
		t.Errorf("patch offsets [%d:%d], want [0:%d]", p.StartByte, p.EndByte, len(src))
	}
}

func TestCollectTablePatchesSkipsUnchangedBlocks(t *testing.T) {
	src := "A paragraph without tables.\n\nAnother paragraph.\n"
	snapshot := buildSnapshot(t, src)
	if patches := CollectTablePatches(snapshot); len(patches) != 0 {
		t.Fatalf("expected no table patches, got %d", len(patches))
	}
}

func TestCollectTablePatchesSkipsAmbiguousTables(t *testing.T) {
	// Inconsistent column counts → ambiguous; no destructive repair.
	src := "Name | Score | Note\nAlice | 10\nBob | 9 | ok | extra\n"
	snapshot := buildSnapshot(t, src)
	if patches := CollectTablePatches(snapshot); len(patches) != 0 {
		t.Fatalf("ambiguous table should not produce a patch, got %d", len(patches))
	}
}

func TestCollectMathPatchesForUnicodeEquations(t *testing.T) {
	src := "For all x in R, x^2 >= 0 with αᵢ = βᵢ + γᵢ\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) == 0 {
		t.Fatalf("expected at least one math patch for unicode equation input")
	}
	for _, p := range patches {
		if p.Source != SourceHeuristicMath {
			t.Errorf("patch Source = %q, want %q", p.Source, SourceHeuristicMath)
		}
		if p.StartByte < 0 || p.EndByte > len(src) {
			t.Errorf("patch offsets out of range: [%d:%d]", p.StartByte, p.EndByte)
		}
		if p.OldText != src[p.StartByte:p.EndByte] {
			t.Errorf("OldText does not match recorded offsets: %q vs %q", p.OldText, src[p.StartByte:p.EndByte])
		}
		if !strings.Contains(p.NewText, "$") {
			t.Errorf("NewText should contain math delimiters: %q", p.NewText)
		}
	}
}

func TestCollectMathPatchesEmptyForPlainProse(t *testing.T) {
	src := "Just a regular paragraph with no math.\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 0 {
		t.Fatalf("expected no patches for plain prose, got %d", len(patches))
	}
}

func TestCollectMathPatchesLeavesFencesAlone(t *testing.T) {
	src := "```\nalpha + beta = gamma\n```\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 0 {
		t.Fatalf("fenced code block should not be patched: %+v", patches)
	}
}

func TestCollectMathPatchesHandlesMultipleParagraphs(t *testing.T) {
	src := "α + β = γ\n\nPlain prose paragraph.\n\nα² + β² = γ²\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) < 2 {
		t.Fatalf("expected at least 2 math patches across paragraphs, got %d", len(patches))
	}
	// Patches must not overlap.
	if err := ValidatePatches([]byte(src), patches); err != nil {
		t.Fatalf("math patches failed validation: %v", err)
	}
}

func TestMathPatchesRoundTrip(t *testing.T) {
	src := "α + β = γ\n\nPlain prose.\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) == 0 {
		t.Skip("no math patches produced for this input")
	}
	rewritten := Apply([]byte(src), patches)
	if string(rewritten) == src {
		t.Fatalf("expected rewritten source to differ from original")
	}
	// Re-applying the collector should be idempotent: no more patches.
	patches2, err := CollectMathPatches(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if len(patches2) != 0 {
		t.Errorf("expected idempotent patches, got %d additional patches", len(patches2))
	}
}

func TestCollectDocumentPatchesCombinesOverlappingTableAndMath(t *testing.T) {
	src := "Name | Formula\nf | α²\n"
	patches, tableCount, mathCount, err := CollectDocumentPatches([]byte(src), buildSnapshot(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if tableCount == 0 {
		t.Fatal("expected a table repair candidate")
	}
	if mathCount == 0 {
		t.Fatal("expected a math conversion candidate")
	}
	if err := ValidatePatches([]byte(src), patches); err != nil {
		t.Fatalf("combined patches should be valid and non-overlapping: %v", err)
	}

	rewritten := string(Apply([]byte(src), patches))
	if !strings.Contains(rewritten, "| --- |") {
		t.Fatalf("rewritten source missing table separator: %q", rewritten)
	}
	if !strings.Contains(rewritten, "$") || !strings.Contains(rewritten, `\alpha`) {
		t.Fatalf("rewritten source missing math conversion: %q", rewritten)
	}
}

func TestCollectDocumentPatchesAnchorsInsertedTableSeparator(t *testing.T) {
	src := "| A | B |\n| C | D |\n"
	patches, tableCount, _, err := CollectDocumentPatches([]byte(src), buildSnapshot(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if tableCount == 0 || len(patches) == 0 {
		t.Fatalf("expected table insertion patch, tableCount=%d patches=%d", tableCount, len(patches))
	}
	if err := ValidatePatches([]byte(src), patches); err != nil {
		t.Fatalf("inserted separator patch should be anchored by existing text: %v", err)
	}

	rewritten := string(Apply([]byte(src), patches))
	if !strings.Contains(rewritten, "| --- | --- |") {
		t.Fatalf("rewritten source missing separator: %q", rewritten)
	}
}

func TestCollectDocumentPatchesRepairsTableWhenSnapshotNeedsFullReset(t *testing.T) {
	src := "Name | Score\nAlice | 10\n\n[docs]: https://example.com\n"
	patches, tableCount, _, err := CollectDocumentPatches([]byte(src), buildSnapshot(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if tableCount == 0 {
		t.Fatal("expected table repair candidate")
	}
	if err := ValidatePatches([]byte(src), patches); err != nil {
		t.Fatalf("patches should validate: %v", err)
	}

	rewritten := string(Apply([]byte(src), patches))
	if !strings.Contains(rewritten, "| --- |") {
		t.Fatalf("rewritten source missing table separator: %q", rewritten)
	}
	if !strings.Contains(rewritten, "[docs]: https://example.com") {
		t.Fatalf("rewritten source dropped reference definition: %q", rewritten)
	}
}

// TestCollectTablePatchesMatchPreprocess locks in the equivalence between the
// snapshot-driven table repairs and the whole-document tablefix.Preprocess
// pass for realistic block-rendered inputs: multi-table documents, code-span
// and escaped pipes, table+math combinations, and blockquote/list table
// forms. Full-reset documents are excluded here: they carry no per-block
// snapshot and deliberately fall back to whole-document preprocessing
// (covered by TestCollectDocumentPatchesFullResetRepairsEveryTable and
// TestCollectDocumentPatchesRepairsTableWhenSnapshotNeedsFullReset).
func TestCollectTablePatchesMatchPreprocess(t *testing.T) {
	tests := []string{
		"Name | Score\nAlice | 10\nBob | 9\n",
		"| A | B |\n| C | D |\n",
		"Pattern | Meaning\n`a | b` | inline code\nx \\| y | escaped pipe\n",
		"Name | Formula\nf | α²\n",
		"Name | Score\nAlice | 10\n\nA | B\nC | D\n",
		"α + β = γ\n\nName | Score\nAlice | 10\n",
		"> Name | Score\n> Alice | 10\n",
		"- Name | Score\n- Alice | 10\n",
		"kM· | ω | err (%)\na | b | c\n",
		"| A | B |\n| :--- | ---: |\n| C | D\n",
		"Name | Score\nAlice | 10\nBob | 9\nEve | 42 | extra\n",
		"`a|b` | `c|d`\n`e|f` | `g|h`\n",
		"Name | Score\r\nAlice | 10\r\n",
		"| A | B |\n| --- | --- |\n| C | D |\n",
	}

	for _, src := range tests {
		t.Run(src, func(t *testing.T) {
			want := tablefix.Preprocess([]byte(src))
			snapshot := buildSnapshot(t, src)
			if snapshot.FullResetOnly {
				t.Fatal("test case unexpectedly produced a full-reset snapshot")
			}
			got := Apply([]byte(src), CollectTablePatches(snapshot))
			if !bytes.Equal(want, got) {
				t.Errorf("snapshot table repair diverged from Preprocess:\nwant %q\ngot  %q", want, got)
			}
		})
	}
}

// TestCollectTablePatchesSeparatorRowFirst documents that a leading
// separator-style row is treated as its own paragraph by the block renderer,
// while the data rows that follow form a repairable table block. The snapshot
// therefore repairs the data rows (matching the rendered output) even though
// the old whole-document Preprocess pass grouped the separator into the same
// run and skipped the repair entirely.
func TestCollectTablePatchesSeparatorRowFirst(t *testing.T) {
	src := "| --- | --- |\nA | B\nC | D\n"
	snapshot := buildSnapshot(t, src)

	patches := CollectTablePatches(snapshot)
	if len(patches) != 1 {
		t.Fatalf("expected 1 table patch for data rows, got %d", len(patches))
	}
	if patches[0].OldText != "A | B\nC | D\n" {
		t.Errorf("patch should cover only the data rows, got %q", patches[0].OldText)
	}
	if !strings.Contains(patches[0].NewText, "| --- | --- |") {
		t.Errorf("patch should insert a separator, got %q", patches[0].NewText)
	}

	all, tableCount, _, err := CollectDocumentPatches([]byte(src), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if tableCount == 0 {
		t.Fatal("expected a table repair candidate")
	}
	rewritten := string(Apply([]byte(src), all))
	if rewritten != "| --- | --- |\n| A | B |\n| --- | --- |\n| C | D |\n" {
		t.Errorf("unexpected rewritten source: %q", rewritten)
	}
}

// TestCollectTablePatchesSkipsPipeContentThatIsNotATable pins the behavior
// for pipe-bearing content that the block renderer does not treat as a table:
// display math ($$...$$ and \[...\]) and ATX headings. The old whole-document
// Preprocess pass over-repaired these into tables even though the rendered
// output shows math/headings; the snapshot-driven path correctly leaves them
// untouched so persisted fixes match the rendered document.
func TestCollectTablePatchesSkipsPipeContentThatIsNotATable(t *testing.T) {
	tests := []string{
		"$$ a | b $$\nc | d\n",
		"\\[ a | b \\]\nc | d\n",
		"## A | B\n## C | D\n",
	}
	for _, src := range tests {
		t.Run(src, func(t *testing.T) {
			snapshot := buildSnapshot(t, src)
			if patches := CollectTablePatches(snapshot); len(patches) != 0 {
				t.Fatalf("expected no table patches, got %d: %+v", len(patches), patches)
			}
			all, tableCount, _, err := CollectDocumentPatches([]byte(src), snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if tableCount != 0 {
				t.Fatalf("expected tableCount 0, got %d", tableCount)
			}
			if len(all) != 0 {
				t.Fatalf("expected no document patches, got %d: %+v", len(all), all)
			}
		})
	}
}

// TestCollectDocumentPatchesFullResetRepairsEveryTable guards the full-reset
// fallback: when a reference definition forces whole-document rendering, the
// snapshot has no per-block repairs, so table patches are recomputed with the
// whole-document Preprocess pass and every table in the document must still
// be repaired.
func TestCollectDocumentPatchesFullResetRepairsEveryTable(t *testing.T) {
	src := "Name | Score\nAlice | 10\n\n[docs]: https://example.com\n\nAnother | Table\nRow | Here\n"
	snapshot := buildSnapshot(t, src)
	if !snapshot.FullResetOnly {
		t.Fatal("expected reference definition to force full-reset snapshot")
	}

	patches, tableCount, _, err := CollectDocumentPatches([]byte(src), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if tableCount < 2 {
		t.Fatalf("expected repairs for both tables, tableCount=%d", tableCount)
	}
	rewritten := string(Apply([]byte(src), patches))
	if strings.Count(rewritten, "| --- |") < 2 {
		t.Fatalf("rewritten source missing separators for both tables: %q", rewritten)
	}
	if !strings.Contains(rewritten, "[docs]: https://example.com") {
		t.Fatalf("reference definition should be preserved: %q", rewritten)
	}
}

func buildSnapshot(t *testing.T, src string) document.DocumentSnapshot {
	t.Helper()
	md := markdown.NewRenderer()
	snapshot, err := document.BuildSnapshot(md, []byte(src), document.DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
