package document

import (
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/markdown"
)

func TestBuildSnapshotSegmentsAndWrapsBlocks(t *testing.T) {
	md := markdown.NewRenderer()
	snapshot, err := BuildSnapshot(md, []byte("# Title\n\nFirst paragraph.\n\n- one\n- two\n"))
	if err != nil {
		t.Fatal(err)
	}

	if snapshot.FullResetOnly {
		t.Fatalf("did not expect full reset fallback: %s", snapshot.FallbackReason)
	}
	if len(snapshot.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(snapshot.Blocks))
	}

	wantKinds := []BlockKind{BlockHeading, BlockParagraph, BlockList}
	for i, want := range wantKinds {
		if snapshot.Blocks[i].Kind != want {
			t.Errorf("block %d kind = %q, want %q", i, snapshot.Blocks[i].Kind, want)
		}
		if snapshot.Blocks[i].ID == "" {
			t.Errorf("block %d missing stable ID", i)
		}
	}

	if !strings.Contains(snapshot.HTML, `class="md-block"`) {
		t.Fatalf("snapshot HTML missing block wrapper:\n%s", snapshot.HTML)
	}
	if !strings.Contains(snapshot.HTML, `<h1>Title</h1>`) {
		t.Fatalf("snapshot HTML missing heading render:\n%s", snapshot.HTML)
	}
	if !strings.Contains(snapshot.HTML, `<li>two</li>`) {
		t.Fatalf("snapshot HTML missing list render:\n%s", snapshot.HTML)
	}
}

func TestBuildSnapshotRepairsMalformedTableBlock(t *testing.T) {
	snapshot := mustSnapshot(t, "Name | Score\nAlice | 10\nBob | 9\n")

	if len(snapshot.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(snapshot.Blocks))
	}
	block := snapshot.Blocks[0]
	if block.Kind != BlockTable {
		t.Fatalf("block kind = %q, want %q", block.Kind, BlockTable)
	}
	if block.Source != "Name | Score\nAlice | 10\nBob | 9\n" {
		t.Fatalf("source should remain original, got %q", block.Source)
	}
	if !strings.Contains(block.HTML, "<table>") || !strings.Contains(block.HTML, "<td>10</td>") {
		t.Fatalf("malformed table was not repaired for rendering:\n%s", block.HTML)
	}
	if len(block.Diagnostics) != 0 {
		t.Fatalf("obvious table should not produce diagnostics: %+v", block.Diagnostics)
	}
}

func TestBuildSnapshotDiagnosesAmbiguousTableBlock(t *testing.T) {
	snapshot := mustSnapshot(t, "Name | Score | Note\nAlice | 10\nBob | 9 | ok | extra\n")

	if len(snapshot.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(snapshot.Blocks))
	}
	block := snapshot.Blocks[0]
	if block.Kind != BlockTable {
		t.Fatalf("block kind = %q, want %q", block.Kind, BlockTable)
	}
	if strings.Contains(block.HTML, "<table>") {
		t.Fatalf("ambiguous table should not be repaired:\n%s", block.HTML)
	}
	if len(block.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", block.Diagnostics)
	}
	diag := block.Diagnostics[0]
	if diag.Code != "table.ambiguous" || diag.BlockID != block.ID || diag.StartLine != 1 || diag.EndLine != 3 {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if !strings.Contains(block.WrappedHTML(), "diagnostic-code") || !strings.Contains(block.WrappedHTML(), "table.ambiguous") {
		t.Fatalf("wrapped HTML missing diagnostic badge:\n%s", block.WrappedHTML())
	}
}

func TestDiffReplacesOneChangedParagraph(t *testing.T) {
	oldSnapshot := mustSnapshot(t, "# Title\n\nOriginal paragraph.\n\nTail paragraph.\n")
	newSnapshot := mustSnapshot(t, "# Title\n\nUpdated paragraph.\n\nTail paragraph.\n")

	result := Diff(oldSnapshot, newSnapshot)
	if result.Reset {
		t.Fatal("did not expect reset for one paragraph change")
	}
	if len(result.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d: %+v", len(result.Ops), result.Ops)
	}

	op := result.Ops[0]
	if op.Op != OpReplace {
		t.Fatalf("expected replace op, got %+v", op)
	}
	if op.ID != oldSnapshot.Blocks[1].ID {
		t.Fatalf("replace target ID = %q, want %q", op.ID, oldSnapshot.Blocks[1].ID)
	}
	if !strings.Contains(op.HTML, "Updated paragraph") {
		t.Fatalf("replace HTML missing updated paragraph:\n%s", op.HTML)
	}
	if strings.Contains(op.HTML, "Tail paragraph") {
		t.Fatalf("replace HTML should contain only the changed block:\n%s", op.HTML)
	}

	if result.Snapshot.Blocks[0].ID != oldSnapshot.Blocks[0].ID {
		t.Error("unchanged heading should retain its ID")
	}
	if result.Snapshot.Blocks[2].ID != oldSnapshot.Blocks[2].ID {
		t.Error("unchanged tail paragraph should retain its ID")
	}
}

func TestDiffInsertsHeadingWithoutReset(t *testing.T) {
	oldSnapshot := mustSnapshot(t, "# Title\n\nParagraph.\n")
	newSnapshot := mustSnapshot(t, "# Title\n\n## Inserted\n\nParagraph.\n")

	result := Diff(oldSnapshot, newSnapshot)
	if result.Reset {
		t.Fatal("did not expect reset for inserted heading")
	}
	if len(result.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d: %+v", len(result.Ops), result.Ops)
	}

	op := result.Ops[0]
	if op.Op != OpInsertAfter {
		t.Fatalf("expected insert_after op, got %+v", op)
	}
	if op.After != oldSnapshot.Blocks[0].ID {
		t.Fatalf("insert anchor = %q, want %q", op.After, oldSnapshot.Blocks[0].ID)
	}
	if !strings.Contains(op.HTML, "Inserted") || !strings.Contains(op.HTML, `class="md-block"`) {
		t.Fatalf("insert HTML missing wrapped heading:\n%s", op.HTML)
	}
	if result.Snapshot.Blocks[2].ID != oldSnapshot.Blocks[1].ID {
		t.Error("unchanged paragraph should retain its ID after insertion")
	}
}

func TestDiffDeletesBlockWithoutReset(t *testing.T) {
	oldSnapshot := mustSnapshot(t, "# Title\n\nRemove me.\n\nKeep me.\n")
	newSnapshot := mustSnapshot(t, "# Title\n\nKeep me.\n")

	result := Diff(oldSnapshot, newSnapshot)
	if result.Reset {
		t.Fatal("did not expect reset for deleted paragraph")
	}
	if len(result.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d: %+v", len(result.Ops), result.Ops)
	}
	if result.Ops[0].Op != OpDelete || result.Ops[0].ID != oldSnapshot.Blocks[1].ID {
		t.Fatalf("unexpected delete op: %+v", result.Ops[0])
	}
}

func TestReferenceDefinitionsUseFullResetFallback(t *testing.T) {
	snapshot := mustSnapshot(t, "See [the docs][docs].\n\n[docs]: https://example.com\n")
	if !snapshot.FullResetOnly {
		t.Fatal("expected full reset fallback for reference definition")
	}
	if snapshot.HTML == "" || !strings.Contains(snapshot.HTML, "https://example.com") {
		t.Fatalf("fallback snapshot should still render full HTML:\n%s", snapshot.HTML)
	}

	updated := mustSnapshot(t, "See [the docs][docs].\n\n[docs]: https://example.org\n")
	result := Diff(snapshot, updated)
	if !result.Reset {
		t.Fatal("expected reset diff when fallback snapshots are involved")
	}
}

func mustSnapshot(t *testing.T, source string) DocumentSnapshot {
	t.Helper()
	snapshot, err := BuildSnapshot(markdown.NewRenderer(), []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
