package llmapp

import (
	"context"
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/llm"
	"github.com/mengkeat/yamdview/internal/markdown"
)

func buildSnapshot(t *testing.T, src string) (document.DocumentSnapshot, []byte) {
	t.Helper()
	md := markdown.NewRenderer()
	snap, err := document.BuildSnapshot(md, []byte(src), document.DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	return snap, []byte(src)
}

func findTableBlock(t *testing.T, snap document.DocumentSnapshot) document.Block {
	t.Helper()
	for _, b := range snap.Blocks {
		if b.Kind == document.BlockTable || strings.Contains(b.HTML, "table.ambiguous") || hasTrigger(b.Diagnostics) {
			return b
		}
	}
	for _, b := range snap.Blocks {
		if strings.Contains(b.Source, "|") || strings.Contains(b.Source, "Name") {
			return b
		}
	}
	t.Fatalf("no candidate block found in snapshot: %+v", snap.Blocks)
	return document.Block{}
}

func hasTrigger(diags []document.Diagnostic) bool {
	for _, d := range diags {
		if _, ok := triggerKinds[d.Code]; ok {
			return true
		}
	}
	return false
}

// ambiguousTableSource returns a table-like block tablefix classifies as
// ambiguous (uneven columns) and therefore leaves unrepaired with a
// table.ambiguous diagnostic. That diagnostic is the LLM repair trigger.
func ambiguousTableSource() string {
	return "a | b | c | d\ne | f\n"
}

func TestRepairNilProviderIsNoOp(t *testing.T) {
	snap, src := buildSnapshot(t, ambiguousTableSource())
	res := Repair(context.Background(), markdown.NewRenderer(), nil, snap, src)
	if res.Applied != 0 || res.Rejected != 0 {
		t.Errorf("expected no-op, got applied=%d rejected=%d", res.Applied, res.Rejected)
	}
}

func TestRepairAcceptsValidTableFix(t *testing.T) {
	snap, src := buildSnapshot(t, ambiguousTableSource())
	block := findTableBlock(t, snap)

	mock := llm.NewMock("mock")
	mock.Queue(llm.MockText(`{"replacement_markdown":"| h1 | h2 |\n| --- | --- |\n| a | b |\n","confidence":0.9}`))

	res := Repair(context.Background(), markdown.NewRenderer(), mock, snap, src)
	if res.Applied != 1 {
		t.Fatalf("expected 1 applied, got %d (diags: %+v)", res.Applied, res.Diagnostics)
	}
	fixed := res.Snapshot.Blocks[indexOf(snap.Blocks, block.ID)]
	if !strings.Contains(fixed.HTML, "<table>") {
		t.Errorf("expected repaired block to render a table, got:\n%s", fixed.HTML)
	}
	if !containsCode(fixed.Diagnostics, llm.CodeAccepted) {
		t.Errorf("expected accepted diagnostic on block, got %+v", fixed.Diagnostics)
	}
}

func TestRepairLeavesBlockUnchangedOnRejection(t *testing.T) {
	snap, src := buildSnapshot(t, ambiguousTableSource())
	block := findTableBlock(t, snap)
	originalHTML := block.HTML
	originalDiags := len(block.Diagnostics)

	mock := llm.NewMock("mock")
	// Response with an unrelated link → semantic validation rejects it.
	mock.Queue(llm.MockText(`{"replacement_markdown":"| h |\n| --- |\n| a | see [x](http://y) |\n","confidence":0.9}`))

	res := Repair(context.Background(), markdown.NewRenderer(), mock, snap, src)
	if res.Applied != 0 {
		t.Fatalf("expected 0 applied, got %d", res.Applied)
	}
	if res.Rejected < 1 {
		t.Fatalf("expected at least 1 rejected, got %d", res.Rejected)
	}
	fixed := res.Snapshot.Blocks[indexOf(snap.Blocks, block.ID)]
	if fixed.HTML != originalHTML {
		t.Errorf("rejected repair changed block HTML:\nwas: %s\nnow: %s", originalHTML, fixed.HTML)
	}
	if len(fixed.Diagnostics) <= originalDiags {
		t.Errorf("expected rejection diagnostic appended, got %+v", fixed.Diagnostics)
	}
	if !containsCode(res.Diagnostics, llm.CodeRejected) {
		t.Errorf("expected a rejection diagnostic in flat list, got %+v", res.Diagnostics)
	}
}

func TestRepairLowConfidenceRejected(t *testing.T) {
	snap, src := buildSnapshot(t, ambiguousTableSource())
	mock := llm.NewMock("mock")
	mock.Queue(llm.MockText(`{"replacement_markdown":"| h1 | h2 |\n| --- | --- |\n| a | b |\n","confidence":0.1}`))

	res := Repair(context.Background(), markdown.NewRenderer(), mock, snap, src)
	if res.Applied != 0 {
		t.Fatalf("expected 0 applied for low confidence, got %d", res.Applied)
	}
}

func TestRepairSkipsBlocksWithoutTrigger(t *testing.T) {
	snap, src := buildSnapshot(t, "# A normal heading\n\nSome prose paragraph.\n")
	mock := llm.NewMock("mock")
	mock.Queue(llm.MockText(`{"replacement_markdown":"$x$","confidence":0.9}`))

	res := Repair(context.Background(), markdown.NewRenderer(), mock, snap, src)
	if res.Applied != 0 || res.Rejected != 0 {
		t.Errorf("expected no candidates, got applied=%d rejected=%d", res.Applied, res.Rejected)
	}
	if len(mock.Calls()) != 0 {
		t.Errorf("expected no provider calls, got %d", len(mock.Calls()))
	}
}

func TestRepairFullResetOnlyIsNoOp(t *testing.T) {
	md := markdown.NewRenderer()
	src := []byte("Text with [ref][ref].\n\n[ref]: https://example.com\n")
	snap, err := document.BuildSnapshot(md, src, document.DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.FullResetOnly {
		t.Fatal("expected full-reset snapshot")
	}
	mock := llm.NewMock("mock")
	res := Repair(context.Background(), md, mock, snap, src)
	if res.Applied != 0 || res.Rejected != 0 {
		t.Errorf("expected no-op for full-reset, got applied=%d", res.Applied)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

func indexOf(blocks []document.Block, id string) int {
	for i, b := range blocks {
		if b.ID == id {
			return i
		}
	}
	return 0
}

func containsCode(diags []document.Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}
