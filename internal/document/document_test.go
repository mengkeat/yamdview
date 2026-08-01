package document

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/markdown"
)

func TestBuildSnapshotSegmentsAndWrapsBlocks(t *testing.T) {
	md := markdown.NewRenderer()
	snapshot, err := BuildSnapshot(md, []byte("# Title\n\nFirst paragraph.\n\n- one\n- two\n"), DocumentSnapshot{})
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

func TestDiffFallsBackToResetForLargeBlockMatrix(t *testing.T) {
	oldBlocks := make([]Block, 501)
	newBlocks := make([]Block, 501)
	for i := range oldBlocks {
		oldBlocks[i] = Block{ID: blockID(i, BlockParagraph, "old"), Kind: BlockParagraph, HTML: "<p>old</p>\n"}
		newBlocks[i] = Block{ID: blockID(i, BlockParagraph, "new"), Kind: BlockParagraph, HTML: "<p>new</p>\n"}
	}

	result := Diff(DocumentSnapshot{Blocks: oldBlocks}, DocumentSnapshot{Blocks: newBlocks})
	if !result.Reset {
		t.Fatal("expected reset when diff matrix exceeds cap")
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
	snapshot, err := BuildSnapshot(markdown.NewRenderer(), []byte(source), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestBuildSnapshotReusesIdenticalBlocks(t *testing.T) {
	src := "# Title\n\nFirst paragraph.\n\n- one\n- two\n\nTail.\n"
	md := markdown.NewRenderer()

	first, err := BuildSnapshot(md, []byte(src), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildSnapshot(md, []byte(src), first)
	if err != nil {
		t.Fatal(err)
	}

	if len(second.Blocks) != len(first.Blocks) {
		t.Fatalf("block count changed: %d → %d", len(first.Blocks), len(second.Blocks))
	}
	for i := range first.Blocks {
		if second.Blocks[i].ID != first.Blocks[i].ID {
			t.Errorf("block %d ID changed on rebuild: %q → %q", i, first.Blocks[i].ID, second.Blocks[i].ID)
		}
		if second.Blocks[i].HTML != first.Blocks[i].HTML {
			t.Errorf("block %d HTML changed on rebuild", i)
		}
	}
}

func TestBuildSnapshotReusesUnchangedBlocksWhenOneChanges(t *testing.T) {
	md := markdown.NewRenderer()
	prevSrc := "# Title\n\nOriginal paragraph.\n\n- one\n- two\n\nTail.\n"
	nextSrc := "# Title\n\nUpdated paragraph.\n\n- one\n- two\n\nTail.\n"

	prev, err := BuildSnapshot(md, []byte(prevSrc), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := BuildSnapshot(md, []byte(nextSrc), prev)
	if err != nil {
		t.Fatal(err)
	}

	if len(next.Blocks) != len(prev.Blocks) {
		t.Fatalf("expected same block count, got %d → %d", len(prev.Blocks), len(next.Blocks))
	}
	for _, i := range []int{0, 2, 3} {
		if next.Blocks[i].ID != prev.Blocks[i].ID {
			t.Errorf("unchanged block %d should reuse its ID: %q → %q", i, prev.Blocks[i].ID, next.Blocks[i].ID)
		}
		if next.Blocks[i].HTML != prev.Blocks[i].HTML {
			t.Errorf("unchanged block %d HTML changed", i)
		}
	}
	if next.Blocks[1].HTML == prev.Blocks[1].HTML {
		t.Error("changed block should have new HTML")
	}
	if !strings.Contains(next.Blocks[1].HTML, "Updated paragraph") {
		t.Errorf("changed block HTML missing update:\n%s", next.Blocks[1].HTML)
	}
}

func TestBuildSnapshotCachedEqualsFreshForMixedDocument(t *testing.T) {
	src := "# Heading\n\n" +
		"A paragraph with `code` and **bold**.\n\n" +
		"Name | Score\nAlice | 10\nBob | 9\n\n" +
		"Name | Score | Note\nAlice | 10\nBob | 9 | ok | extra\n\n" +
		"$$\nE = mc^2\n$$\n\n" +
		"```go\npackage main\n```\n\n" +
		"```math\n\\int x dx\n```\n\n" +
		"- item one\n- item two\n\n" +
		"> quoted\n\n" +
		"---\n"
	md := markdown.NewRenderer()

	fresh, err := BuildSnapshot(md, []byte(src), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Blocks) != 10 {
		t.Fatalf("expected 10 blocks, got %d", len(fresh.Blocks))
	}
	cached, err := BuildSnapshot(md, []byte(src), fresh)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotsEqual(t, fresh, cached)
}

func TestBuildSnapshotCachedEqualsFreshForManyBlocks(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&sb, "Paragraph number %d with unique text %d.\n\n", i, i)
	}
	src := sb.String()
	md := markdown.NewRenderer()

	fresh, err := BuildSnapshot(md, []byte(src), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := BuildSnapshot(md, []byte(src), fresh)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotsEqual(t, fresh, cached)
}

func TestBuildSnapshotCachedEqualsFreshForCRLF(t *testing.T) {
	src := "# Title\r\n\r\nFirst paragraph.\r\n\r\nName | Score\r\nAlice | 10\r\nBob | 9\r\n\r\n$$\r\nE = mc^2\r\n$$\r\n"
	md := markdown.NewRenderer()

	fresh, err := BuildSnapshot(md, []byte(src), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := BuildSnapshot(md, []byte(src), fresh)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotsEqual(t, fresh, cached)
}

// blockID includes the block ordinal, so a shifted block that keeps its ID
// across an insertion is observable proof that its render was reused rather
// than recomputed with a new index.
func TestBuildSnapshotKeepsIDsWhenBlockInsertedAbove(t *testing.T) {
	md := markdown.NewRenderer()
	prevSrc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph.\n\nThird paragraph.\n"
	nextSrc := "## Inserted\n\n# Heading\n\nFirst paragraph.\n\nSecond paragraph.\n\nThird paragraph.\n"

	prev, err := BuildSnapshot(md, []byte(prevSrc), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := BuildSnapshot(md, []byte(nextSrc), prev)
	if err != nil {
		t.Fatal(err)
	}

	if len(next.Blocks) != len(prev.Blocks)+1 {
		t.Fatalf("expected one more block, got %d → %d", len(prev.Blocks), len(next.Blocks))
	}
	for i := 0; i < len(prev.Blocks); i++ {
		if next.Blocks[i+1].ID != prev.Blocks[i].ID {
			t.Errorf("block %d shifted by insertion lost its ID: %q → %q", i, prev.Blocks[i].ID, next.Blocks[i+1].ID)
		}
		if next.Blocks[i+1].HTML != prev.Blocks[i].HTML {
			t.Errorf("block %d HTML changed after insertion", i)
		}
	}
	if !strings.Contains(next.Blocks[0].HTML, "Inserted") {
		t.Errorf("inserted block missing content:\n%s", next.Blocks[0].HTML)
	}
}

func TestBuildSnapshotKeepsIDsAfterMultiBlockInsertion(t *testing.T) {
	md := markdown.NewRenderer()
	prevSrc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph.\n\nThird paragraph.\n"
	nextSrc := "## A\n\n## B\n\n## C\n\n## D\n\n# Heading\n\nFirst paragraph.\n\nSecond paragraph.\n\nThird paragraph.\n"

	prev, err := BuildSnapshot(md, []byte(prevSrc), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := BuildSnapshot(md, []byte(nextSrc), prev)
	if err != nil {
		t.Fatal(err)
	}

	if len(next.Blocks) != len(prev.Blocks)+4 {
		t.Fatalf("expected 4 more blocks, got %d → %d", len(prev.Blocks), len(next.Blocks))
	}
	for i := 0; i < len(prev.Blocks); i++ {
		if next.Blocks[i+4].ID != prev.Blocks[i].ID {
			t.Errorf("block %d shifted by 4 lost its ID: %q → %q", i, prev.Blocks[i].ID, next.Blocks[i+4].ID)
		}
	}
}

func TestBuildSnapshotKeepsIDsWhenBlockDeletedAbove(t *testing.T) {
	md := markdown.NewRenderer()
	prevSrc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph.\n\nThird paragraph.\n"
	nextSrc := "# Heading\n\nSecond paragraph.\n\nThird paragraph.\n"

	prev, err := BuildSnapshot(md, []byte(prevSrc), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := BuildSnapshot(md, []byte(nextSrc), prev)
	if err != nil {
		t.Fatal(err)
	}

	if len(next.Blocks) != len(prev.Blocks)-1 {
		t.Fatalf("expected one fewer block, got %d → %d", len(prev.Blocks), len(next.Blocks))
	}
	for i := 1; i < len(next.Blocks); i++ {
		if next.Blocks[i].ID != prev.Blocks[i+1].ID {
			t.Errorf("block %d shifted up lost its ID: %q → %q", i, prev.Blocks[i+1].ID, next.Blocks[i].ID)
		}
	}
}

// Repeated identical blocks plus a shift can make a fresh ordinal-based ID
// collide with a reused ID; the snapshot must still contain only unique IDs.
func TestBuildSnapshotCachedKeepsIDsUniqueWithRepeatedBlocks(t *testing.T) {
	md := markdown.NewRenderer()
	prevSrc := "intro\n\ndup\n\ndup\n\ndup\n\noutro\n"
	nextSrc := "dup\n\ndup\n\ndup\n\ndup\n\noutro\n"

	prev, err := BuildSnapshot(md, []byte(prevSrc), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := BuildSnapshot(md, []byte(nextSrc), prev)
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool, len(next.Blocks))
	for i, b := range next.Blocks {
		if seen[b.ID] {
			t.Fatalf("duplicate block ID %q at block %d", b.ID, i)
		}
		seen[b.ID] = true
	}
}

func TestBuildSnapshotFullResetInteractsWithPrev(t *testing.T) {
	md := markdown.NewRenderer()
	normalSrc := "# Title\n\nParagraph.\n"
	refSrc := "See [docs][docs].\n\n[docs]: https://example.com\n"

	normal, err := BuildSnapshot(md, []byte(normalSrc), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}

	// A full-reset document must render fresh regardless of prev.
	ref, err := BuildSnapshot(md, []byte(refSrc), normal)
	if err != nil {
		t.Fatal(err)
	}
	if !ref.FullResetOnly {
		t.Fatal("expected full reset fallback for reference definitions")
	}
	if !strings.Contains(ref.HTML, "https://example.com") {
		t.Fatalf("fallback snapshot should render full HTML:\n%s", ref.HTML)
	}

	// Building a normal document with a full-reset prev (which carries no
	// blocks) must render fresh and match a no-prev build.
	rebuilt, err := BuildSnapshot(md, []byte(normalSrc), ref)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.FullResetOnly {
		t.Fatal("normal document must not be full-reset")
	}
	assertSnapshotsEqual(t, normal, rebuilt)
}

func TestDiffWithCachedNextMatchesFreshDiff(t *testing.T) {
	md := markdown.NewRenderer()
	cases := []struct {
		name   string
		oldSrc string
		newSrc string
	}{
		{
			name:   "one block replaced",
			oldSrc: "# Title\n\nOriginal paragraph.\n\n- one\n- two\n\nTail.\n",
			newSrc: "# Title\n\nUpdated paragraph.\n\n- one\n- two\n\nTail.\n",
		},
		{
			name:   "block inserted at top",
			oldSrc: "# Title\n\nOriginal paragraph.\n\nTail.\n",
			newSrc: "## Inserted\n\n# Title\n\nOriginal paragraph.\n\nTail.\n",
		},
		{
			name:   "block deleted from middle",
			oldSrc: "# Title\n\nOriginal paragraph.\n\nMiddle paragraph.\n\nTail.\n",
			newSrc: "# Title\n\nOriginal paragraph.\n\nTail.\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old, err := BuildSnapshot(md, []byte(tc.oldSrc), DocumentSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			cachedNext, err := BuildSnapshot(md, []byte(tc.newSrc), old)
			if err != nil {
				t.Fatal(err)
			}
			freshNext, err := BuildSnapshot(md, []byte(tc.newSrc), DocumentSnapshot{})
			if err != nil {
				t.Fatal(err)
			}

			cached := Diff(old, cachedNext)
			fresh := Diff(old, freshNext)

			if cached.Reset != fresh.Reset {
				t.Fatalf("reset mismatch: cached=%v fresh=%v", cached.Reset, fresh.Reset)
			}
			if len(cached.Ops) != len(fresh.Ops) {
				t.Fatalf("ops mismatch: cached=%+v fresh=%+v", cached.Ops, fresh.Ops)
			}
			for i := range cached.Ops {
				if cached.Ops[i] != fresh.Ops[i] {
					t.Errorf("op %d mismatch:\ncached %+v\nfresh  %+v", i, cached.Ops[i], fresh.Ops[i])
				}
			}
			assertSnapshotsEqual(t, fresh.Snapshot, cached.Snapshot)
		})
	}
}

func TestBuildSnapshotCachedEqualsFreshForSingleBlock(t *testing.T) {
	md := markdown.NewRenderer()
	src := "Name | Score\nAlice | 10\n"

	fresh, err := BuildSnapshot(md, []byte(src), DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := BuildSnapshot(md, []byte(src), fresh)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotsEqual(t, fresh, cached)
}

// assertSnapshotsEqual verifies two snapshots are identical in every field
// that influences rendered output, Diff matching, or fix persistence.
func assertSnapshotsEqual(t *testing.T, want, got DocumentSnapshot) {
	t.Helper()
	if want.FullResetOnly != got.FullResetOnly || want.FallbackReason != got.FallbackReason {
		t.Errorf("fallback mismatch: want %+v got %+v", want, got)
	}
	if want.HTML != got.HTML {
		t.Errorf("snapshot HTML mismatch:\n--- want ---\n%s\n--- got ---\n%s", want.HTML, got.HTML)
	}
	if len(want.Blocks) != len(got.Blocks) {
		t.Fatalf("block count mismatch: %d vs %d", len(want.Blocks), len(got.Blocks))
	}
	for i := range want.Blocks {
		w, g := want.Blocks[i], got.Blocks[i]
		if w.ID != g.ID || w.Kind != g.Kind || w.SourceStart != g.SourceStart || w.SourceEnd != g.SourceEnd ||
			w.StartLine != g.StartLine || w.EndLine != g.EndLine || w.Source != g.Source ||
			w.Normalized != g.Normalized || w.HTML != g.HTML {
			t.Errorf("block %d mismatch:\nwant %+v\ngot  %+v", i, w, g)
		}
		if !sameDiagnostics(w.Diagnostics, g.Diagnostics) {
			t.Errorf("block %d diagnostics mismatch:\nwant %+v\ngot  %+v", i, w.Diagnostics, g.Diagnostics)
		}
	}
}
