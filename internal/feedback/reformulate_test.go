package feedback

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/annotation"
	"github.com/mengkeat/yamdview/internal/llm"
)

func requoteJSON(text string, confidence float64) string {
	return fmt.Sprintf(`{"text": %q, "confidence": %g}`, text, confidence)
}

func testAnnotations() []annotation.Annotation {
	return []annotation.Annotation{
		{
			ID:        "a1",
			Kind:      annotation.KindComment,
			BlockID:   "b1",
			StartLine: 10,
			EndLine:   12,
			Quote:     "first quoted span",
			Comment:   "tighten this",
		},
		{
			ID:        "a2",
			Kind:      annotation.KindSuggestion,
			BlockID:   "b2",
			StartLine: 20,
			EndLine:   20,
			Quote:     "second quote",
		},
	}
}

func testRequest() ReformulateRequest {
	return ReformulateRequest{
		Title:   "Review of doc",
		Prompt:  "Please review the draft.",
		Verdict: "needs_work",
		Summary: "Two spots need attention before merge.",
	}
}

func coveringText() string {
	return strings.Join([]string{
		"Fix the draft: tighten 'first quoted span' on lines 10-12 and revise the second quote at line 20.",
	}, "\n")
}

// successMock returns a mock whose dynamic responder echoes a valid JSON
// response quoting every requested annotation.
func successMock(name string) *llm.Mock {
	m := llm.NewMock(name)
	m.SetFunc(func(req llm.Request) (llm.Response, error) {
		return llm.Response{Text: requoteJSON(coveringText(), 0.9), Finish: llm.FinishStop}, nil
	})
	return m
}

func TestReformulateSuccess(t *testing.T) {
	mock := successMock("mock-provider")

	result, err := Reformulate(context.Background(), mock, "test-model", testRequest(), testAnnotations())
	if err != nil {
		t.Fatalf("Reformulate returned error: %v", err)
	}
	if !result.Applied {
		t.Fatalf("Applied = false, diagnostics %+v", result.Diagnostics)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", result.Diagnostics)
	}
	r := result.Reformulated
	if r == nil {
		t.Fatal("Reformulated is nil")
	}
	if r.Provider != "mock-provider" {
		t.Errorf("Provider = %q, want mock-provider", r.Provider)
	}
	if r.Model != "test-model" {
		t.Errorf("Model = %q, want test-model", r.Model)
	}
	if r.Text != coveringText() {
		t.Errorf("Text = %q, want %q", r.Text, coveringText())
	}
	if r.ApprovedByUser {
		t.Error("ApprovedByUser should default to false")
	}

	// Request audit: kind, model metadata, prompt contents.
	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d provider calls, want 1", len(calls))
	}
	req := calls[0]
	if req.Kind != llm.KindFeedbackRephrase {
		t.Errorf("request Kind = %q, want feedback_rephrase", req.Kind)
	}
	if req.Metadata["model"] != "test-model" {
		t.Errorf("Metadata[model] = %q, want test-model", req.Metadata["model"])
	}
	for _, want := range []string{"Review of doc", "needs_work", "Two spots need attention before merge.", "first quoted span", "second quote"} {
		if !strings.Contains(req.UserPrompt, want) {
			t.Errorf("UserPrompt missing %q", want)
		}
	}
}

func TestReformulatePrefersResponseModel(t *testing.T) {
	mock := llm.NewMock("mock-provider")
	mock.SetFunc(func(llm.Request) (llm.Response, error) {
		return llm.Response{
			Text:  requoteJSON(coveringText(), 0.9),
			Model: "resp-reported-model",
		}, nil
	})

	result, err := Reformulate(context.Background(), mock, "requested-model", testRequest(), testAnnotations())
	if err != nil {
		t.Fatalf("Reformulate returned error: %v", err)
	}
	if !result.Applied || result.Reformulated == nil {
		t.Fatalf("expected applied result, got %+v", result)
	}
	if result.Reformulated.Model != "resp-reported-model" {
		t.Errorf("Model = %q, want resp-reported-model", result.Reformulated.Model)
	}
}

func TestReformulateDroppedQuoteRejected(t *testing.T) {
	dropping := strings.Join([]string{
		"Tighten 'first quoted span' on lines 10-12; the rest looks fine.",
	}, "\n")
	mock := llm.NewMock("mock-provider")
	mock.SetFunc(func(llm.Request) (llm.Response, error) {
		return llm.Response{Text: requoteJSON(dropping, 0.9)}, nil
	})

	result, err := Reformulate(context.Background(), mock, "test-model", testRequest(), testAnnotations())
	if err != nil {
		t.Fatalf("Reformulate returned error: %v", err)
	}
	if result.Applied || result.Reformulated != nil {
		t.Fatalf("expected silent fallback, got %+v", result)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", result.Diagnostics)
	}
	diag := result.Diagnostics[0]
	if diag.Code != llm.CodeRejected {
		t.Errorf("Code = %q, want %q", diag.Code, llm.CodeRejected)
	}
	missing := "second quote"
	if !strings.Contains(diag.Message, missing) {
		t.Errorf("diagnostic message %q does not mention missing quote %q", diag.Message, missing)
	}
}

func TestReformulateExceedsLengthCap(t *testing.T) {
	huge := strings.Repeat("word ", 400)
	mock := llm.NewMock("mock-provider")
	mock.SetFunc(func(llm.Request) (llm.Response, error) {
		return llm.Response{Text: requoteJSON(huge, 0.9)}, nil
	})

	// Tiny input keeps the computed cap far below the huge response.
	req := ReformulateRequest{Title: "t", Prompt: "p", Verdict: "v", Summary: "s"}
	anns := []annotation.Annotation{{Kind: annotation.KindComment, BlockID: "b", Quote: "q"}}

	result, err := Reformulate(context.Background(), mock, "test-model", req, anns)
	if err != nil {
		t.Fatalf("Reformulate returned error: %v", err)
	}
	if result.Applied {
		t.Fatalf("expected rejection, got %+v", result)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != llm.CodeRejected {
		t.Fatalf("expected CodeRejected diagnostic, got %+v", result.Diagnostics)
	}
	if !strings.Contains(result.Diagnostics[0].Message, "exceeds cap") {
		t.Errorf("message %q should mention the length cap", result.Diagnostics[0].Message)
	}
}

func TestReformulateLowConfidence(t *testing.T) {
	mock := llm.NewMock("mock-provider")
	mock.SetFunc(func(llm.Request) (llm.Response, error) {
		return llm.Response{Text: requoteJSON(coveringText(), 0.2)}, nil
	})

	result, _ := Reformulate(context.Background(), mock, "test-model", testRequest(), testAnnotations())
	if result.Applied {
		t.Fatalf("expected rejection, got %+v", result)
	}
	if d := onlyDiag(t, result); d.Code != llm.CodeRejected {
		t.Errorf("Code = %q, want %q", d.Code, llm.CodeRejected)
	}
}

func TestReformulateInvalidJSON(t *testing.T) {
	mock := llm.NewMock("mock-provider")
	mock.SetFunc(func(llm.Request) (llm.Response, error) {
		return llm.Response{Text: "not json at all"}, nil
	})

	result, _ := Reformulate(context.Background(), mock, "test-model", testRequest(), testAnnotations())
	if result.Applied {
		t.Fatalf("expected fallback, got %+v", result)
	}
	if d := onlyDiag(t, result); d.Code != llm.CodeFailed {
		t.Errorf("Code = %q, want %q", d.Code, llm.CodeFailed)
	}
}

func TestReformulateUnknownField(t *testing.T) {
	mock := llm.NewMock("mock-provider")
	mock.SetFunc(func(llm.Request) (llm.Response, error) {
		return llm.Response{Text: `{"text": "ok", "confidence": 0.9, "extra": true}`}, nil
	})

	result, _ := Reformulate(context.Background(), mock, "test-model", testRequest(), testAnnotations())
	if result.Applied {
		t.Fatalf("expected fallback, got %+v", result)
	}
	if d := onlyDiag(t, result); d.Code != llm.CodeFailed {
		t.Errorf("Code = %q, want %q", d.Code, llm.CodeFailed)
	}
}

func TestReformulateTimeout(t *testing.T) {
	mock := llm.NewMock("mock-provider")
	mock.SetDelay(10 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := Reformulate(ctx, mock, "test-model", testRequest(), testAnnotations())
	if err != nil {
		t.Fatalf("Reformulate returned error: %v", err)
	}
	if result.Applied || result.Reformulated != nil {
		t.Fatalf("expected clean fallback, got %+v", result)
	}
	if d := onlyDiag(t, result); d.Code != llm.CodeTimeout {
		t.Errorf("Code = %q, want %q", d.Code, llm.CodeTimeout)
	}
}

func TestReformulateProviderHardError(t *testing.T) {
	mock := llm.NewMock("mock-provider")
	mock.Queue(llm.MockResponse{Err: errors.New("500 oops " + strings.Repeat("body ", 100))})

	result, err := Reformulate(context.Background(), mock, "test-model", testRequest(), testAnnotations())
	if err != nil {
		t.Fatalf("Reformulate returned error: %v", err)
	}
	if result.Applied || result.Reformulated != nil {
		t.Fatalf("expected clean fallback, got %+v", result)
	}
	d := onlyDiag(t, result)
	if d.Code != llm.CodeFailed {
		t.Errorf("Code = %q, want %q", d.Code, llm.CodeFailed)
	}
	if len(d.Message) > maxDiagMessage+len("…") {
		t.Errorf("message too long (%d chars): %q", len(d.Message), d.Message)
	}
	if strings.Contains(d.Message, strings.Repeat("body ", 100)) {
		t.Error("diagnostic message leaked full provider body")
	}
}

func TestReformulateNoAnnotations(t *testing.T) {
	mock := llm.NewMock("mock-provider")
	text := "Polish the draft: two spots need attention before merge."
	mock.SetFunc(func(llm.Request) (llm.Response, error) {
		return llm.Response{Text: requoteJSON(text, 0.8)}, nil
	})

	result, err := Reformulate(context.Background(), mock, "test-model", testRequest(), nil)
	if err != nil {
		t.Fatalf("Reformulate returned error: %v", err)
	}
	if !result.Applied || result.Reformulated == nil {
		t.Fatalf("expected applied result, got %+v", result)
	}
	if result.Reformulated.Text != text {
		t.Errorf("Text = %q, want %q", result.Reformulated.Text, text)
	}
}

func TestReformulateNilProvider(t *testing.T) {
	result, err := Reformulate(context.Background(), nil, "test-model", testRequest(), testAnnotations())
	if err != nil {
		t.Fatalf("nil provider must not return an error, got %v", err)
	}
	if result.Applied {
		t.Fatalf("expected fallback, got %+v", result)
	}
	if d := onlyDiag(t, result); d.Code != llm.CodeFailed {
		t.Errorf("Code = %q, want %q", d.Code, llm.CodeFailed)
	}
}

func TestBuildRephraseInputSkipsEmptyQuotes(t *testing.T) {
	anns := []annotation.Annotation{
		{Kind: annotation.KindComment, BlockID: "b1", Quote: "", Comment: "orphan"},
		{Kind: annotation.KindComment, BlockID: "b2", Quote: "   \n\t "},
		{Kind: annotation.KindComment, BlockID: "b3", Quote: "real", StartLine: 3, EndLine: 4},
	}

	input := buildRephraseInput(testRequest(), anns)
	if len(input.Annotations) != 1 {
		t.Fatalf("got %d annotations, want 1: %+v", len(input.Annotations), input.Annotations)
	}
	got := input.Annotations[0]
	if got.Quote != "real" || got.StartLine != 3 || got.EndLine != 4 {
		t.Errorf("unexpected annotation %+v", got)
	}
}

// onlyDiag asserts the result carries exactly one diagnostic and returns it.
func onlyDiag(t *testing.T, result ReformulateResult) llm.Diagnostic {
	t.Helper()
	if len(result.Diagnostics) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %+v", result.Diagnostics)
	}
	return result.Diagnostics[0]
}
