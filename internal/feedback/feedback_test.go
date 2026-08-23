package feedback_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/feedback"
)

func testPayload() feedback.Payload {
	return feedback.Payload{
		Version:   feedback.CurrentVersion,
		SessionID: "s-20260610-3f9a",
		Title:     "Refactor plan",
		Prompt:    "Please review this plan.",
		Verdict:   "request_changes",
		Summary:   "Mostly good. Two issues around the cache layer.",
		Comments:  []any{},
		Timing: feedback.Timing{
			OpenedAt:    time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
			SubmittedAt: time.Date(2026, 6, 10, 12, 3, 4, 520000000, time.UTC),
			DurationMS:  184520,
		},
	}
}

func TestRenderJSONGolden(t *testing.T) {
	got, err := feedback.RenderJSON(testPayload())
	if err != nil {
		t.Fatalf("render JSON: %v", err)
	}

	want := `{
  "yamdview_feedback_version": 1,
  "session_id": "s-20260610-3f9a",
  "title": "Refactor plan",
  "prompt": "Please review this plan.",
  "verdict": "request_changes",
  "summary": "Mostly good. Two issues around the cache layer.",
  "comments": [],
  "timing": {
    "opened_at": "2026-06-10T12:00:00Z",
    "submitted_at": "2026-06-10T12:03:04.52Z",
    "duration_ms": 184520
  }
}
`
	if got != want {
		t.Fatalf("JSON differs from golden fixture:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMarkdownGolden(t *testing.T) {
	got, err := feedback.RenderMarkdown(testPayload())
	if err != nil {
		t.Fatalf("render Markdown: %v", err)
	}

	want := `## Review feedback: Refactor plan

**Verdict:** request changes

Mostly good. Two issues around the cache layer.
`
	if got != want {
		t.Fatalf("Markdown differs from golden fixture:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderingIsDeterministic(t *testing.T) {
	payload := testPayload()
	jsonFirst, err := feedback.RenderJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	markdownFirst, err := feedback.RenderMarkdown(payload)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		jsonNext, err := feedback.RenderJSON(payload)
		if err != nil {
			t.Fatal(err)
		}
		markdownNext, err := feedback.RenderMarkdown(payload)
		if err != nil {
			t.Fatal(err)
		}
		if jsonNext != jsonFirst || markdownNext != markdownFirst {
			t.Fatal("rendering changed between identical calls")
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	payload := testPayload()
	encoded, err := feedback.RenderJSON(payload)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := feedback.DecodeJSON([]byte(encoded))
	if err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if !reflect.DeepEqual(decoded, payload) {
		t.Fatalf("decoded payload differs:\n got: %#v\nwant: %#v", decoded, payload)
	}

	var standardDecoded feedback.Payload
	if err := json.Unmarshal([]byte(encoded), &standardDecoded); err != nil {
		t.Fatalf("standard JSON decode: %v", err)
	}
	if standardDecoded.SessionID != payload.SessionID || standardDecoded.Timing.DurationMS != payload.Timing.DurationMS {
		t.Fatalf("standard decode lost fields: %#v", standardDecoded)
	}
}

func TestDecodeJSONRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	encoded, err := feedback.RenderJSON(testPayload())
	if err != nil {
		t.Fatal(err)
	}

	unknown := strings.TrimSuffix(encoded, "\n")
	unknown = strings.TrimSuffix(unknown, "}") + `, "extra": true}`
	if _, err := feedback.DecodeJSON([]byte(unknown)); err == nil {
		t.Fatal("DecodeJSON accepted an unknown field")
	}

	if _, err := feedback.DecodeJSON([]byte(encoded + encoded)); err == nil {
		t.Fatal("DecodeJSON accepted a trailing value")
	}
}

func TestValidateFormat(t *testing.T) {
	for _, format := range []string{"json", "markdown"} {
		if err := feedback.ValidateFormat(format); err != nil {
			t.Errorf("ValidateFormat(%q): %v", format, err)
		}
		parsed, err := feedback.ParseFormat(format)
		if err != nil || string(parsed) != format {
			t.Errorf("ParseFormat(%q) = %q, %v", format, parsed, err)
		}
	}
	for _, format := range []string{"", "yaml", "JSON"} {
		if err := feedback.ValidateFormat(format); err == nil {
			t.Errorf("ValidateFormat(%q) accepted unsupported format", format)
		}
	}
}

func TestRenderRejectsUnsupportedPhase12CommentsAndVersion(t *testing.T) {
	payload := testPayload()
	payload.Comments = []any{"annotation"}
	if _, err := feedback.RenderJSON(payload); err == nil {
		t.Fatal("RenderJSON accepted Phase 12 comments")
	}

	payload = testPayload()
	payload.Version = feedback.CurrentVersion + 1
	if _, err := feedback.RenderMarkdown(payload); err == nil {
		t.Fatal("RenderMarkdown accepted an unsupported version")
	}
}
