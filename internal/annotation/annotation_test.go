package annotation_test

import (
	"testing"

	"github.com/mengkeat/yamdview/internal/annotation"
	"github.com/mengkeat/yamdview/internal/document"
)

func snapshot(blocks ...document.Block) document.DocumentSnapshot {
	return document.DocumentSnapshot{Blocks: blocks}
}

func block(id, source string, start, line int) document.Block {
	return document.Block{ID: id, Source: source, SourceStart: start, SourceEnd: start + len(source), StartLine: line, EndLine: line}
}

func TestResolveUniqueExactAndNormalized(t *testing.T) {
	b := block("b1", "before\nselected text\nafter", 10, 4)
	s := snapshot(b)

	span := annotation.Resolve(s, annotation.Annotation{BlockID: "b1", Quote: "selected text"})
	if span == nil || span.StartByte != 17 || span.EndByte != 30 {
		t.Fatalf("exact span = %#v", span)
	}

	span = annotation.Resolve(s, annotation.Annotation{BlockID: "b1", Quote: "selected   text"})
	if span == nil || span.StartByte != 17 || span.EndByte != 30 {
		t.Fatalf("normalized span = %#v", span)
	}
}

func TestResolveAmbiguousUsesContextOrFails(t *testing.T) {
	b := block("b1", "same one; same two", 0, 1)
	s := snapshot(b)
	if got := annotation.Resolve(s, annotation.Annotation{BlockID: "b1", Quote: "same"}); got != nil {
		t.Fatalf("ambiguous quote resolved to %#v", got)
	}

	got := annotation.Resolve(s, annotation.Annotation{BlockID: "b1", Quote: "same", Suffix: " two"})
	if got == nil || got.StartByte != 10 || got.EndByte != 14 {
		t.Fatalf("context-resolved quote = %#v", got)
	}
}

func TestResolveRenderedMarkdownText(t *testing.T) {
	b := block("b1", "A **bold** [link](https://example.test) text", 5, 2)
	got := annotation.Resolve(snapshot(b), annotation.Annotation{BlockID: "b1", Quote: "bold link"})
	if got == nil || got.StartByte != 9 || got.EndByte != 21 {
		t.Fatalf("rendered bold span = %#v", got)
	}

	if got := annotation.Resolve(snapshot(b), annotation.Annotation{BlockID: "b1", Quote: "missing"}); got != nil {
		t.Fatalf("failed quote resolved to %#v", got)
	}
}

func TestSplitSelectionSharesOnlyMultiBlockGroup(t *testing.T) {
	pieces := []annotation.SelectionPiece{
		{BlockID: "b1", Quote: "first"},
		{BlockID: "b2", Quote: "second"},
	}
	got, err := annotation.SplitSelection(pieces)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].GroupID == "" || got[0].GroupID != got[1].GroupID {
		t.Fatalf("group IDs = %#v", got)
	}
	one, err := annotation.SplitSelection(pieces[:1])
	if err != nil || len(one) != 1 || one[0].GroupID != "" {
		t.Fatalf("single-piece group = %#v, err=%v", one, err)
	}
}

func TestReanchorPreservesUniqueAndMarksOutdated(t *testing.T) {
	old := annotation.Annotation{
		ID: "a1", BlockID: "old", Quote: "keep me", Status: annotation.StatusActive,
		SourceSpan: &annotation.SourceSpan{StartByte: 6, EndByte: 13, Text: "keep me"},
	}
	newSnapshot := snapshot(block("new", "keep me", 40, 8))
	got := annotation.Reanchor(old, newSnapshot)
	if got.Status != annotation.StatusActive || got.BlockID != "new" || got.SourceSpan == nil || got.SourceSpan.StartByte != 40 {
		t.Fatalf("reanchored annotation = %#v", got)
	}

	outdated := annotation.Reanchor(old, snapshot(block("old", "changed", 0, 1)))
	if outdated.Status != annotation.StatusOutdated || outdated.SourceSpan != nil {
		t.Fatalf("outdated annotation = %#v", outdated)
	}
}
