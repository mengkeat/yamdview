package session_test

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/session"
)

func newTestSession(t *testing.T) *session.Session {
	t.Helper()
	s, err := session.New("review-1", "A review", "Please review", []string{"approve", "changes"}, []byte("# frozen"), document.DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewSessionTokenIsRandomAndHexEncoded(t *testing.T) {
	first := newTestSession(t)
	second := newTestSession(t)

	if len(first.Token) != 64 {
		t.Fatalf("token length = %d, want 64", len(first.Token))
	}
	if _, err := hex.DecodeString(first.Token); err != nil {
		t.Fatalf("token is not hex encoded: %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("two sessions received the same token")
	}
}

func TestNewSessionCopiesFrozenInputs(t *testing.T) {
	choices := []string{"approve"}
	source := []byte("original")
	snapshot := document.DocumentSnapshot{
		Blocks: []document.Block{{Source: "original", Diagnostics: []document.Diagnostic{{Message: "warning"}}}},
	}
	s, err := session.New("id", "title", "prompt", choices, source, snapshot)
	if err != nil {
		t.Fatal(err)
	}

	choices[0] = "changed"
	source[0] = 'X'
	snapshot.Blocks[0].Source = "changed"
	snapshot.Blocks[0].Diagnostics[0].Message = "changed"

	if s.Choices[0] != "approve" || string(s.Source) != "original" {
		t.Fatalf("session inputs were not copied: choices=%v source=%q", s.Choices, s.Source)
	}
	if s.Snapshot.Blocks[0].Source != "original" || s.Snapshot.Blocks[0].Diagnostics[0].Message != "warning" {
		t.Fatalf("snapshot was not copied: %+v", s.Snapshot)
	}
}

func TestSessionLegalAndIllegalTransitions(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*session.Session) error
		want  session.State
	}{
		{name: "submit", apply: func(s *session.Session) error { return s.Submit("approve", "looks good") }, want: session.Submitted},
		{name: "timeout", apply: func(s *session.Session) error { return s.Timeout() }, want: session.Timeout},
		{name: "cancel", apply: func(s *session.Session) error { return s.Cancel() }, want: session.Cancelled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSession(t)
			if err := tt.apply(s); err != nil {
				t.Fatalf("legal transition failed: %v", err)
			}
			if got := s.CurrentState(); got != tt.want {
				t.Fatalf("state = %q, want %q", got, tt.want)
			}
			if err := s.Cancel(); !errors.Is(err, session.ErrInvalidTransition) {
				t.Fatalf("illegal transition error = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestOnlyOneConcurrentTransitionSucceeds(t *testing.T) {
	s := newTestSession(t)
	results := make(chan error, 3)
	var wg sync.WaitGroup
	wg.Add(3)

	go func() { defer wg.Done(); results <- s.Submit("approve", "done") }()
	go func() { defer wg.Done(); results <- s.Timeout() }()
	go func() { defer wg.Done(); results <- s.Cancel() }()
	wg.Wait()
	close(results)

	var successes int
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, session.ErrInvalidTransition) {
			t.Fatalf("unexpected transition error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful transitions = %d, want 1", successes)
	}
	if got := s.CurrentState(); got == session.Open {
		t.Fatal("session remained open after a successful terminal transition")
	}
}

func TestTerminalNotificationAndWait(t *testing.T) {
	s := newTestSession(t)
	waitDone := make(chan error, 1)
	go func() { waitDone <- s.Wait(context.Background()) }()

	if err := s.Submit("approve", "done"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Done was not closed after submission")
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("Wait returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after submission")
	}
}

func TestTimeoutAndCancelSemantics(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		s := newTestSession(t)
		if err := s.Timeout(); err != nil {
			t.Fatal(err)
		}
		if s.CurrentState() != session.Timeout || s.SubmittedAt.IsZero() {
			t.Fatalf("timeout state/timestamp = %q/%v", s.CurrentState(), s.SubmittedAt)
		}
		if err := s.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		s := newTestSession(t)
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
		if s.CurrentState() != session.Cancelled || s.SubmittedAt.IsZero() {
			t.Fatalf("cancel state/timestamp = %q/%v", s.CurrentState(), s.SubmittedAt)
		}
		if err := s.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("context cancellation does not cancel session", func(t *testing.T) {
		s := newTestSession(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Wait(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait error = %v, want context.Canceled", err)
		}
		if s.CurrentState() != session.Open {
			t.Fatalf("state = %q, want open", s.CurrentState())
		}
		if err := s.Cancel(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReformulatedStorageCopiesAndTerminalState(t *testing.T) {
	s := newTestSession(t)

	if got := s.ReformulatedResult(); got != nil {
		t.Fatalf("ReformulatedResult on fresh session = %#v, want nil", got)
	}

	stored := &feedback.Reformulated{Provider: "mock", Model: "m1", Text: "rewritten"}
	s.SetReformulated(stored)

	got := s.ReformulatedResult()
	if got == nil || *got != *stored {
		t.Fatalf("ReformulatedResult = %#v, want %+v", got, stored)
	}

	// Mutating the returned copy must not affect what the session holds.
	got.Text = "tampered"
	got.ApprovedByUser = true
	if again := s.ReformulatedResult(); again.Text != "rewritten" || again.ApprovedByUser {
		t.Fatalf("stored result was mutated via returned copy: %#v", again)
	}

	// Mutating the value passed to SetReformulated must not leak in either.
	stored.Model = "changed"
	if again := s.ReformulatedResult(); again.Model != "m1" {
		t.Fatalf("session aliased caller's struct: %#v", again)
	}

	s.SetReformulated(nil)
	if got := s.ReformulatedResult(); got != nil {
		t.Fatalf("SetReformulated(nil) did not clear storage: %#v", got)
	}

	// Storage must survive a terminal transition: the app assembles the final
	// payload (including approval) after Submit.
	if err := s.Submit("approve", "fine"); err != nil {
		t.Fatal(err)
	}
	final := &feedback.Reformulated{Provider: "mock", Model: "m1", Text: "final", ApprovedByUser: true}
	s.SetReformulated(final)
	if got := s.ReformulatedResult(); got == nil || !got.ApprovedByUser || got.Text != "final" {
		t.Fatalf("reformulated not stored after terminal state: %#v", got)
	}
}
