package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewMockName(t *testing.T) {
	m := NewMock("test")
	if got := m.Name(); got != "test" {
		t.Fatalf("Name() = %q, want %q", got, "test")
	}
}

func TestMockDefaultResponse(t *testing.T) {
	m := NewMock("test")
	resp, err := m.Complete(context.Background(), Request{Kind: KindMathFix})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "" {
		t.Fatalf("expected empty default text, got %q", resp.Text)
	}
	if resp.Finish != FinishStop {
		t.Fatalf("expected FinishStop, got %q", resp.Finish)
	}
}

func TestMockQueuedResponsesInOrder(t *testing.T) {
	m := NewMock("test")
	m.Queue(MockText("first"), MockText("second"))

	for _, want := range []string{"first", "second"} {
		resp, err := m.Complete(context.Background(), Request{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != want {
			t.Fatalf("got %q, want %q", resp.Text, want)
		}
	}

	// The queue is exhausted; the last response repeats.
	resp, err := m.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "second" {
		t.Fatalf("expected last response to repeat, got %q", resp.Text)
	}
}

func TestMockRecordsCalls(t *testing.T) {
	m := NewMock("test")
	req := Request{Kind: KindTableFix, UserPrompt: "fix this table", Temperature: 0.1}
	if _, err := m.Complete(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := m.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Kind != KindTableFix {
		t.Errorf("got kind %q, want %q", calls[0].Kind, KindTableFix)
	}
	if calls[0].UserPrompt != "fix this table" {
		t.Errorf("got prompt %q", calls[0].UserPrompt)
	}
}

func TestMockSetFuncUsedWhenQueueEmpty(t *testing.T) {
	m := NewMock("test")
	m.SetFunc(func(r Request) (Response, error) {
		return Response{Text: "from-func-" + string(r.Kind), Finish: FinishStop}, nil
	})

	resp, err := m.Complete(context.Background(), Request{Kind: KindMathFix})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "from-func-math_fix" {
		t.Fatalf("got %q", resp.Text)
	}
}

func TestMockQueuePreferredOverFunc(t *testing.T) {
	m := NewMock("test")
	m.SetFunc(func(r Request) (Response, error) {
		return Response{Text: "from-func"}, nil
	})
	m.Queue(MockText("queued"))

	resp, err := m.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "queued" {
		t.Fatalf("expected queued response first, got %q", resp.Text)
	}
}

func TestMockPropagatesError(t *testing.T) {
	customErr := errors.New("boom")
	m := NewMock("test")
	m.Queue(MockResponse{Err: customErr})

	if _, err := m.Complete(context.Background(), Request{}); !errors.Is(err, customErr) {
		t.Fatalf("expected custom error, got %v", err)
	}
}

func TestMockHonorsCancelledContext(t *testing.T) {
	m := NewMock("test")
	m.SetDelay(50 * time.Millisecond)
	m.Queue(MockText("late"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := m.Complete(ctx, Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestMockCompletesAfterDelay(t *testing.T) {
	m := NewMock("test")
	m.SetDelay(5 * time.Millisecond)
	m.Queue(MockText("ok"))

	resp, err := m.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("got %q", resp.Text)
	}
}

func TestSourceSpanMatches(t *testing.T) {
	src := []byte("hello world")
	span := SourceSpan{StartByte: 6, EndByte: 11, Text: "world"}

	if !span.Matches(src) {
		t.Fatal("expected span to match")
	}

	edited := []byte("hello WORLD")
	if span.Matches(edited) {
		t.Fatal("expected span to be stale after edit")
	}

	outOfRange := SourceSpan{StartByte: 0, EndByte: 100, Text: ""}
	if outOfRange.Matches(src) {
		t.Fatal("expected out-of-range span not to match")
	}
}
