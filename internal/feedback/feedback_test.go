package feedback_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/annotation"
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
		Comments:  []annotation.Annotation{},
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

func TestRenderRejectsInvalidCommentsAndVersion(t *testing.T) {
	payload := testPayload()
	payload.Comments = []annotation.Annotation{{Kind: annotation.Kind("unknown"), BlockID: "block", Quote: "quote"}}
	if _, err := feedback.RenderJSON(payload); err == nil {
		t.Fatal("RenderJSON accepted an invalid comment")
	}

	payload = testPayload()
	payload.Version = feedback.CurrentVersion + 1
	if _, err := feedback.RenderMarkdown(payload); err == nil {
		t.Fatal("RenderMarkdown accepted an unsupported version")
	}
}

func TestRenderJSONAnnotationsGoldenAndRoundTrip(t *testing.T) {
	payload := testPayload()
	createdAt := time.Date(2026, 6, 10, 12, 1, 2, 0, time.UTC)
	payload.Comments = []annotation.Annotation{
		{
			ID: "a-comment", Kind: annotation.KindComment, GroupID: "group-1",
			BlockID: "block-1", StartLine: 10, EndLine: 11, Quote: "selected text",
			Prefix: "before ", Suffix: " after",
			SourceSpan: &annotation.SourceSpan{StartByte: 1180, EndByte: 1193, Text: "secret source"},
			Comment:    "Please keep this wording.", CreatedAt: createdAt, Status: annotation.StatusActive,
		},
		{ID: "a-suggestion", Kind: annotation.KindSuggestion, BlockID: "block-2", StartLine: 20, EndLine: 20,
			Quote: "invalidate every key", Comment: "This is too broad.", SuggestedReplacement: "invalidate affected keys", CreatedAt: createdAt, Status: annotation.StatusActive},
		{ID: "a-question", Kind: annotation.KindQuestion, BlockID: "block-3", StartLine: 30, EndLine: 31,
			Quote: "why this dependency?", Comment: "Can we avoid it?", CreatedAt: createdAt, Status: annotation.StatusActive},
		{ID: "a-concern", Kind: annotation.KindConcern, BlockID: "block-4", StartLine: 40, EndLine: 40,
			Quote: "uncached request", Comment: "This may be expensive.", CreatedAt: createdAt, Status: annotation.StatusActive},
		{ID: "a-approval", Kind: annotation.KindApproval, BlockID: "block-5", StartLine: 50, EndLine: 52,
			Quote: "safe migration", CreatedAt: createdAt, Status: annotation.StatusActive},
	}

	got, err := feedback.RenderJSON(payload)
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
  "comments": [
    {
      "id": "a-comment",
      "kind": "comment",
      "group_id": "group-1",
      "block_id": "block-1",
      "start_line": 10,
      "end_line": 11,
      "quote": "selected text",
      "prefix": "before ",
      "suffix": " after",
      "source_span": {
        "start_byte": 1180,
        "end_byte": 1193
      },
      "comment": "Please keep this wording.",
      "created_at": "2026-06-10T12:01:02Z",
      "updated_at": "0001-01-01T00:00:00Z",
      "status": "active"
    },
    {
      "id": "a-suggestion",
      "kind": "suggestion",
      "block_id": "block-2",
      "start_line": 20,
      "end_line": 20,
      "quote": "invalidate every key",
      "comment": "This is too broad.",
      "suggested_replacement": "invalidate affected keys",
      "created_at": "2026-06-10T12:01:02Z",
      "updated_at": "0001-01-01T00:00:00Z",
      "status": "active"
    },
    {
      "id": "a-question",
      "kind": "question",
      "block_id": "block-3",
      "start_line": 30,
      "end_line": 31,
      "quote": "why this dependency?",
      "comment": "Can we avoid it?",
      "created_at": "2026-06-10T12:01:02Z",
      "updated_at": "0001-01-01T00:00:00Z",
      "status": "active"
    },
    {
      "id": "a-concern",
      "kind": "concern",
      "block_id": "block-4",
      "start_line": 40,
      "end_line": 40,
      "quote": "uncached request",
      "comment": "This may be expensive.",
      "created_at": "2026-06-10T12:01:02Z",
      "updated_at": "0001-01-01T00:00:00Z",
      "status": "active"
    },
    {
      "id": "a-approval",
      "kind": "approval",
      "block_id": "block-5",
      "start_line": 50,
      "end_line": 52,
      "quote": "safe migration",
      "created_at": "2026-06-10T12:01:02Z",
      "updated_at": "0001-01-01T00:00:00Z",
      "status": "active"
    }
  ],
  "timing": {
    "opened_at": "2026-06-10T12:00:00Z",
    "submitted_at": "2026-06-10T12:03:04.52Z",
    "duration_ms": 184520
  }
}
`
	if got != want {
		t.Fatalf("JSON differs from annotation golden fixture:\n got:\n%s\nwant:\n%s", got, want)
	}

	decoded, err := feedback.DecodeJSON([]byte(got))
	if err != nil {
		t.Fatalf("decode annotations: %v", err)
	}
	wantDecoded := payload
	wantDecoded.Comments[0].SourceSpan.Text = ""
	if !reflect.DeepEqual(decoded, wantDecoded) {
		t.Fatalf("decoded annotations differ:\n got: %#v\nwant: %#v", decoded.Comments, wantDecoded.Comments)
	}
	if strings.Contains(got, "secret source") || strings.Contains(got, `"text"`) {
		t.Fatal("JSON leaked SourceSpan.Text")
	}
}

func TestRenderMarkdownCommentsGolden(t *testing.T) {
	payload := testPayload()
	payload.Comments = []annotation.Annotation{
		{Kind: annotation.KindSuggestion, BlockID: "block-1", StartLine: 42, EndLine: 44,
			Quote: "invalidate the cache on every write", Comment: "Too aggressive - invalidate only the affected keys.",
			SuggestedReplacement: "invalidate only the affected cache keys on write", Status: annotation.StatusActive},
		{Kind: annotation.KindConcern, BlockID: "block-2", StartLine: 8, EndLine: 8,
			Quote: "uncached request", Comment: "This is now outdated.", Status: annotation.StatusOutdated},
	}

	got, err := feedback.RenderMarkdown(payload)
	if err != nil {
		t.Fatalf("render Markdown: %v", err)
	}
	want := `## Review feedback: Refactor plan

**Verdict:** request changes

Mostly good. Two issues around the cache layer.

### Comments

1. **Suggestion** (lines 42-44):
   > invalidate the cache on every write

   Too aggressive - invalidate only the affected keys.
   Suggested replacement: ` + "`invalidate only the affected cache keys on write`" + `

2. **Concern** (lines 8-8) **(outdated)**:
   > uncached request

   This is now outdated.
`
	if got != want {
		t.Fatalf("Markdown differs from annotation golden fixture:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestValidateRejectsEveryInvalidAnnotation(t *testing.T) {
	tests := []annotation.Annotation{
		{Kind: annotation.Kind("unknown"), BlockID: "block", Quote: "quote"},
		{Kind: annotation.KindComment, Quote: "quote"},
		{Kind: annotation.KindComment, BlockID: "block"},
		{Kind: annotation.KindComment, BlockID: "block", Quote: "quote", Status: annotation.Status("unknown")},
	}
	for i, comment := range tests {
		payload := testPayload()
		payload.Comments = []annotation.Annotation{comment}
		if err := payload.Validate(); err == nil {
			t.Errorf("invalid comment %d was accepted: %#v", i, comment)
		}
		if _, err := feedback.RenderJSON(payload); err == nil {
			t.Errorf("RenderJSON accepted invalid comment %d", i)
		}
	}
}

func TestDecodeJSONRejectsInvalidNestedComment(t *testing.T) {
	input := strings.Replace(feedbackJSONWithComment(), `"kind": "comment"`, `"kind": "unknown"`, 1)
	if _, err := feedback.DecodeJSON([]byte(input)); err == nil {
		t.Fatal("DecodeJSON accepted an invalid annotation kind")
	}

	input = strings.Replace(feedbackJSONWithComment(), `"quote": "quote"`, `"quote": "quote", "extra": true`, 1)
	if _, err := feedback.DecodeJSON([]byte(input)); err == nil {
		t.Fatal("DecodeJSON accepted an unknown annotation field")
	}
}

func feedbackJSONWithComment() string {
	payload := testPayload()
	payload.Comments = []annotation.Annotation{{Kind: annotation.KindComment, BlockID: "block", Quote: "quote"}}
	encoded, err := feedback.RenderJSON(payload)
	if err != nil {
		panic(err)
	}
	return encoded
}
