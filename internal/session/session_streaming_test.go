package session_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/annotation"
	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/session"
)

func newStreamingSession(t *testing.T) *session.Session {
	t.Helper()
	s, err := session.NewStreaming("stream-1", "Streaming", "Prompt", nil, []byte("first paragraph"), annotationSnapshot("block-1", "first paragraph", 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStreamingSessionLocksMutationsUntilComplete(t *testing.T) {
	s := newStreamingSession(t)
	if !s.IsStreaming() {
		t.Fatal("NewStreaming session should report streaming")
	}

	if _, err := s.CreateAnnotation(annotation.Annotation{Kind: annotation.KindComment, BlockID: "block-1", Quote: "first", Comment: "note"}); !errors.Is(err, session.ErrSessionStreaming) {
		t.Fatalf("CreateAnnotation while streaming err = %v, want ErrSessionStreaming", err)
	}
	if err := s.Submit("approve", "ok"); !errors.Is(err, session.ErrSessionStreaming) {
		t.Fatalf("Submit while streaming err = %v, want ErrSessionStreaming", err)
	}

	// Document updates stay available while streaming so appends keep flowing.
	if err := s.UpdateSnapshot([]byte("first paragraph\n\nsecond"), annotationSnapshot("block-1", "first paragraph", 0, 1)); err != nil {
		t.Fatalf("UpdateSnapshot while streaming: %v", err)
	}

	s.MarkComplete()
	if s.IsStreaming() {
		t.Fatal("MarkComplete should clear the streaming flag")
	}
	created, err := s.CreateAnnotation(annotation.Annotation{Kind: annotation.KindComment, BlockID: "block-1", Quote: "first", Comment: "note"})
	if err != nil {
		t.Fatalf("CreateAnnotation after MarkComplete: %v", err)
	}
	if err := s.DeleteAnnotation(created.ID); err != nil {
		t.Fatalf("DeleteAnnotation after MarkComplete: %v", err)
	}
	if err := s.Submit("approve", "ok"); err != nil {
		t.Fatalf("Submit after MarkComplete: %v", err)
	}
}

func TestMarkCompleteIsIdempotentAndSafeOnPlainSessions(t *testing.T) {
	s := newTestSession(t)
	if s.IsStreaming() {
		t.Fatal("plain session.New must not be streaming")
	}
	s.MarkComplete()
	s.MarkComplete()
	if s.IsStreaming() {
		t.Fatal("MarkComplete on a non-streaming session should be a no-op")
	}
}

func TestFeedbackPayloadMirrorsSessionState(t *testing.T) {
	s, created := newAnchoredSession(t)
	if _, err := s.CreateAnnotation(annotation.Annotation{Kind: annotation.KindConcern, BlockID: "block-1", Quote: "selected", Comment: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit("request_changes", "two notes"); err != nil {
		t.Fatal(err)
	}

	payload := s.FeedbackPayload()
	if payload.Version != feedback.CurrentVersion || payload.SessionID != s.ID || payload.Title != "title" || payload.Prompt != "prompt" {
		t.Fatalf("unexpected payload header: %+v", payload)
	}
	if payload.Verdict != "request_changes" || payload.Summary != "two notes" {
		t.Fatalf("unexpected verdict/summary: %+v", payload)
	}
	if len(payload.Comments) != 2 || payload.Comments[0].ID != created.ID {
		t.Fatalf("unexpected comments: %+v", payload.Comments)
	}
	if payload.Timing.OpenedAt.IsZero() || payload.Timing.SubmittedAt.IsZero() || payload.Timing.DurationMS < 0 {
		t.Fatalf("unexpected timing: %+v", payload.Timing)
	}
	if _, err := feedback.RenderJSON(payload); err != nil {
		t.Fatalf("payload does not render: %v", err)
	}
}

func TestFeedbackPayloadReformulatedIncluded(t *testing.T) {
	s := newTestSession(t)
	s.SetReformulated(&feedback.Reformulated{Provider: "p", Model: "m", Text: "t", ApprovedByUser: true})
	if err := s.Submit("approve", ""); err != nil {
		t.Fatal(err)
	}
	payload := s.FeedbackPayload()
	if payload.Reformulated == nil || !payload.Reformulated.ApprovedByUser {
		t.Fatalf("reformulation missing from payload: %+v", payload.Reformulated)
	}
}

func TestFeedbackPayloadNegativeDurationClamped(t *testing.T) {
	s := newTestSession(t)
	s.OpenedAt = time.Now().Add(time.Hour) // future timestamp edge case
	if err := s.Submit("approve", ""); err != nil {
		t.Fatal(err)
	}
	if got := s.FeedbackPayload().Timing.DurationMS; got != 0 {
		t.Fatalf("duration = %d, want 0 for negative elapsed time", got)
	}
}
