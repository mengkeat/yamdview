package session_test

import (
	"errors"
	"testing"

	"github.com/mengkeat/yamdview/internal/annotation"
	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/session"
)

func annotationSnapshot(blockID, source string, start, line int) document.DocumentSnapshot {
	return document.DocumentSnapshot{Blocks: []document.Block{{
		ID: blockID, Source: source, SourceStart: start, SourceEnd: start + len(source),
		StartLine: line, EndLine: line,
	}}}
}

func newAnchoredSession(t *testing.T) (*session.Session, annotation.Annotation) {
	t.Helper()
	s, err := session.New("id", "title", "prompt", nil, []byte("before selected after"), annotationSnapshot("block-1", "before selected after", 10, 3))
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateAnnotation(annotation.Annotation{Kind: annotation.KindComment, BlockID: "block-1", Quote: "selected", Comment: "note"})
	if err != nil {
		t.Fatal(err)
	}
	return s, created
}

func TestCreateAnnotationResolvesAndReturnsCopies(t *testing.T) {
	s, created := newAnchoredSession(t)
	if created.ID == "" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("server fields were not assigned: %#v", created)
	}
	if created.Status != annotation.StatusActive || created.SourceSpan == nil || created.SourceSpan.StartByte != 17 {
		t.Fatalf("created anchor = %#v", created)
	}
	created.SourceSpan.Text = "changed"
	created.Comment = "mutated copy"
	got, err := s.GetAnnotation(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Comment != "note" || got.SourceSpan == nil || got.SourceSpan.Text != "selected" {
		t.Fatalf("stored annotation was exposed through a copy: %#v", got)
	}
	list := s.ListAnnotations()
	list[0].Comment = "mutated list"
	if got, _ := s.GetAnnotation(created.ID); got.Comment != "note" {
		t.Fatal("list returned mutable session state")
	}
}

func TestAnnotationUpdateAndDelete(t *testing.T) {
	s, created := newAnchoredSession(t)
	updated, err := s.UpdateAnnotation(created.ID, annotation.Annotation{Comment: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Comment != "updated" || updated.SourceSpan == nil || updated.Status != annotation.StatusActive {
		t.Fatalf("updated annotation = %#v", updated)
	}
	if err := s.DeleteAnnotation(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAnnotation(created.ID); !errors.Is(err, session.ErrAnnotationNotFound) {
		t.Fatalf("get deleted annotation error = %v", err)
	}
}

func TestCreateAnnotationValidation(t *testing.T) {
	s := newTestSession(t)
	tests := []annotation.Annotation{
		{Kind: annotation.Kind("unknown"), BlockID: "block", Quote: "text"},
		{Kind: annotation.KindComment, Quote: "text"},
		{Kind: annotation.KindComment, BlockID: "block"},
		{Kind: annotation.KindSuggestion, BlockID: "block", Quote: "text"},
		{Kind: annotation.KindComment, BlockID: "block", Quote: "text", SuggestedReplacement: "replacement"},
	}
	for _, input := range tests {
		if _, err := s.CreateAnnotation(input); !errors.Is(err, session.ErrInvalidAnnotation) {
			t.Errorf("input %#v error = %v, want ErrInvalidAnnotation", input, err)
		}
	}
}

func TestAnnotationReanchorsAndRetainsOutdated(t *testing.T) {
	s, err := session.New("id", "title", "prompt", nil, []byte("keep me\nremove me"), annotationSnapshot("old", "keep me\nremove me", 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	moved, err := s.CreateAnnotation(annotation.Annotation{ID: "moved", Kind: annotation.KindComment, BlockID: "old", Quote: "keep me"})
	if err != nil {
		t.Fatal(err)
	}
	outdated, err := s.CreateAnnotation(annotation.Annotation{ID: "outdated", Kind: annotation.KindComment, BlockID: "old", Quote: "remove me"})
	if err != nil {
		t.Fatal(err)
	}
	next := annotationSnapshot("new", "keep me", 40, 8)
	if err := s.UpdateSnapshot([]byte("prefix\nkeep me"), next); err != nil {
		t.Fatal(err)
	}
	gotMoved, _ := s.GetAnnotation(moved.ID)
	gotOutdated, _ := s.GetAnnotation(outdated.ID)
	if gotMoved.Status != annotation.StatusActive || gotMoved.BlockID != "new" || gotMoved.SourceSpan == nil || gotMoved.SourceSpan.StartByte != 40 {
		t.Fatalf("moved annotation = %#v", gotMoved)
	}
	if gotOutdated.Status != annotation.StatusOutdated || gotOutdated.SourceSpan != nil {
		t.Fatalf("outdated annotation = %#v", gotOutdated)
	}
}

func TestTerminalSessionRejectsAnnotationMutations(t *testing.T) {
	s := newTestSession(t)
	if err := s.Submit("approve", "done"); err != nil {
		t.Fatal(err)
	}
	input := annotation.Annotation{Kind: annotation.KindComment, BlockID: "block", Quote: "text"}
	if _, err := s.CreateAnnotation(input); !errors.Is(err, session.ErrTerminalSessionMutation) {
		t.Fatalf("create terminal error = %v", err)
	}
	if err := s.UpdateSnapshot(nil, document.DocumentSnapshot{}); !errors.Is(err, session.ErrTerminalSessionMutation) {
		t.Fatalf("snapshot terminal error = %v", err)
	}
}
