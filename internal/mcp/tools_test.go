package mcp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/feedback"
)

// TestRequestReviewRoundTrip presents a document through request_review and
// submits the underlying session concurrently, asserting the tool returns
// the full feedback payload for the one-shot flow.
func TestRequestReviewRoundTrip(t *testing.T) {
	h := startMCP(t)
	mgr := h.srv.Manager()

	submitted := make(chan string, 1)
	go func() {
		defer close(submitted)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			ids := mgr.IDs()
			if len(ids) == 1 {
				id := ids[0]
				// Complete is idempotent and races safely with the tool's
				// own call; submission requires a non-streaming document.
				if err := mgr.Complete(id); err != nil {
					continue
				}
				if sess, ok := mgr.Get(id); ok {
					if err := sess.Submit("request_changes", "tighten wording"); err == nil {
						submitted <- id
						return
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	resp := h.call(t, toolCallLine(t, 1, "request_review", map[string]any{
		"markdown":        "# Draft\n\nNeeds words.",
		"title":           "Draft",
		"open":            false,
		"timeout_seconds": 10,
	}))
	payload := toolText[feedback.Payload](t, resp)
	if payload.Verdict != "request_changes" {
		t.Fatalf("verdict = %q, want request_changes", payload.Verdict)
	}
	if payload.Summary != "tighten wording" {
		t.Fatalf("summary = %q, want %q", payload.Summary, "tighten wording")
	}
	if !strings.HasPrefix(payload.SessionID, "s-") {
		t.Fatalf("session id = %q, want s- prefix", payload.SessionID)
	}
	select {
	case id, ok := <-submitted:
		if ok && id != payload.SessionID {
			t.Fatalf("submitted session %q differs from payload session %q", id, payload.SessionID)
		}
	default:
		// The channel is only for cross-checking; its absence is not fatal.
	}
}

// TestAwaitFeedbackTimeout presents a streaming session and waits with a
// short timeout plus complete=true, asserting the tool reports a still-open
// session as isError and that the convenience complete took effect.
func TestAwaitFeedbackTimeout(t *testing.T) {
	h := startMCP(t)

	resp := h.call(t, toolCallLine(t, 1, "present_markdown", map[string]any{
		"markdown": "# Waiting\n\nNobody will review this.",
		"open":     false,
	}))
	present := toolText[presentEcho](t, resp)

	start := time.Now()
	resp = h.call(t, toolCallLine(t, 2, "await_feedback", map[string]any{
		"session_id":      present.SessionID,
		"timeout_seconds": 1,
		"complete":        true,
	}))
	text := toolErrorText(t, resp)
	if !strings.Contains(text, "still open") {
		t.Fatalf("timeout text = %q, want it to mention the still-open session", text)
	}
	if !strings.Contains(text, present.SessionID) {
		t.Fatalf("timeout text = %q, want it to name session %q", text, present.SessionID)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Fatalf("await_feedback returned too early (%s), want it to honor the timeout", elapsed)
	}

	// The complete convenience must have unlocked the session.
	sess, ok := h.srv.Manager().Get(present.SessionID)
	if !ok {
		t.Fatal("session missing from manager")
	}
	if sess.IsStreaming() {
		t.Fatal("session still streaming after await_feedback complete=true")
	}
}

// TestAwaitFeedbackUnknownSession asserts an unknown session id is a tool
// error, not a protocol error.
func TestAwaitFeedbackUnknownSession(t *testing.T) {
	h := startMCP(t)
	resp := h.call(t, toolCallLine(t, 1, "await_feedback", map[string]any{
		"session_id": "s-does-not-exist",
	}))
	if text := toolErrorText(t, resp); !strings.Contains(text, "session not found") {
		t.Fatalf("text = %q, want session not found", text)
	}
}

// TestPresentValidation covers the markdown|path union and path failures.
func TestPresentValidation(t *testing.T) {
	h := startMCP(t)

	cases := []struct {
		name      string
		arguments map[string]any
		want      string
	}{
		{
			name:      "neither markdown nor path",
			arguments: map[string]any{"open": false},
			want:      "exactly one",
		},
		{
			name: "both markdown and path",
			arguments: map[string]any{
				"markdown": "# x",
				"path":     "/tmp/x.md",
				"open":     false,
			},
			want: "exactly one",
		},
		{
			name: "missing path",
			arguments: map[string]any{
				"path": filepath.Join(t.TempDir(), "does-not-exist.md"),
				"open": false,
			},
			want: "does-not-exist.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.call(t, toolCallLine(t, 1, "present_markdown", tc.arguments))
			if text := toolErrorText(t, resp); !strings.Contains(text, tc.want) {
				t.Fatalf("text = %q, want it to contain %q", text, tc.want)
			}
		})
	}
}

// TestPresentFromPath presents a local file and checks the resulting
// session content matches the file.
func TestPresentFromPath(t *testing.T) {
	h := startMCP(t)
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("# From disk\n\nFile body."), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := h.call(t, toolCallLine(t, 1, "present_markdown", map[string]any{
		"path":  path,
		"title": "From disk",
		"open":  false,
	}))
	present := toolText[presentEcho](t, resp)
	if present.State != "streaming" {
		t.Fatalf("state = %q, want streaming", present.State)
	}
	sess, ok := h.srv.Manager().Get(present.SessionID)
	if !ok {
		t.Fatal("session missing from manager")
	}
	if string(sess.Source) != "# From disk\n\nFile body." {
		t.Fatalf("session source = %q", sess.Source)
	}
	if sess.Title != "From disk" {
		t.Fatalf("session title = %q, want From disk", sess.Title)
	}
}

// TestPresentCompleteFlag checks complete=true unlocks the session and is
// reported in the result state.
func TestPresentCompleteFlag(t *testing.T) {
	h := startMCP(t)
	resp := h.call(t, toolCallLine(t, 1, "present_markdown", map[string]any{
		"markdown": "# Done\n\nFinished document.",
		"complete": true,
		"open":     false,
	}))
	present := toolText[presentEcho](t, resp)
	if present.State != "complete" {
		t.Fatalf("state = %q, want complete", present.State)
	}
	sess, ok := h.srv.Manager().Get(present.SessionID)
	if !ok {
		t.Fatal("session missing from manager")
	}
	if sess.IsStreaming() {
		t.Fatal("session still streaming after complete=true")
	}
}
