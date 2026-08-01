package llm

import (
	"strings"
	"testing"
)

func TestRenderMathFixPrompt(t *testing.T) {
	out, err := RenderPrompt(KindMathFix, PromptData{
		BlockKind:    "paragraph",
		SourceSpan:   "F = ma",
		Context:      "Newton's second law states that F = ma.",
		Diagnostics:  []string{"math.unresolved: ASCII equation"},
		HeuristicTeX: "F = ma",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checks := []string{
		"F = ma",
		"paragraph",
		"Newton's second law",
		"math.unresolved",
		`"replacement_markdown"`,
		`"changed_spans"`,
		"confidence",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("rendered prompt missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderMathFixPromptOmitsEmptySections(t *testing.T) {
	out, err := RenderPrompt(KindMathFix, PromptData{
		BlockKind:  "paragraph",
		SourceSpan: "x²",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Heuristic TeX") {
		t.Errorf("expected heuristic tex section omitted:\n%s", out)
	}
	if strings.Contains(out, "Surrounding context") {
		t.Errorf("expected context section omitted:\n%s", out)
	}
	if strings.Contains(out, "triggered this fallback") {
		t.Errorf("expected diagnostics section omitted:\n%s", out)
	}
}

func TestRenderTableFixPrompt(t *testing.T) {
	out, err := RenderPrompt(KindTableFix, PromptData{
		BlockKind:  "table",
		SourceSpan: "Name | Value\nA | 1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Name | Value") {
		t.Errorf("missing source span:\n%s", out)
	}
	if !strings.Contains(out, `"replacement_markdown"`) {
		t.Errorf("missing schema:\n%s", out)
	}
	// Table prompt must not carry math-only fields.
	if strings.Contains(out, "changed_spans") {
		t.Errorf("table prompt should not mention changed_spans:\n%s", out)
	}
}

func TestRenderClassifyPrompt(t *testing.T) {
	out, err := RenderPrompt(KindClassifyCandidate, PromptData{
		SourceSpan: "F = ma",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"is_math"`) {
		t.Errorf("missing is_math field:\n%s", out)
	}
}

func TestRenderPromptUnknownKind(t *testing.T) {
	if _, err := RenderPrompt(RequestKind("bogus"), PromptData{}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestSystemPromptPerKind(t *testing.T) {
	for _, kind := range []RequestKind{KindMathFix, KindTableFix, KindClassifyCandidate} {
		if got := SystemPrompt(kind); !strings.Contains(got, "JSON only") {
			t.Errorf("system prompt for %q missing JSON instruction: %q", kind, got)
		}
	}
}
