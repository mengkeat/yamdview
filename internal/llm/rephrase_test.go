package llm

import (
	"strings"
	"testing"
)

func rephraseTestInput() RephraseInput {
	return RephraseInput{
		Title:       "Spec Draft",
		AgentPrompt: "Rewrite the introduction section for clarity.",
		Verdict:     "needs_changes",
		Summary:     "Two spans need attention before this draft ships.",
		Annotations: []RephraseAnnotation{
			{Quote: "the quick brown fox", StartLine: 12, EndLine: 14, Comment: "typo in adjective"},
			{Quote: "x² ≥ 0", StartLine: 20, EndLine: 20, Comment: ""},
		},
	}
}

func TestRenderRephrasePrompt(t *testing.T) {
	in := rephraseTestInput()
	out, err := in.RenderPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checks := []string{
		"Spec Draft",
		"Rewrite the introduction",
		"needs_changes",
		"Two spans need attention",
		"the quick brown fox",
		"x² ≥ 0",
		"lines 12-14",
		`"text"`,
		"confidence",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("rendered prompt missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderRephrasePromptWithoutAnnotations(t *testing.T) {
	in := RephraseInput{Title: "T", AgentPrompt: "P", Verdict: "approved", Summary: "S"}
	out, err := in.RenderPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Annotations (each") {
		t.Errorf("expected annotation section omitted:\n%s", out)
	}
}

func TestDecodeRephraseResponse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		text    string
		conf    float64
		wantErr bool
	}{
		{name: "valid json", raw: `{"text":"Fix the typo.","confidence":0.9}`, text: "Fix the typo.", conf: 0.9},
		{
			name: "fenced with prose",
			raw:  "Here you go:\n```json\n{\"text\":\"Rewrite it.\",\"confidence\":0.7}\n```\nDone.",
			text: "Rewrite it.", conf: 0.7,
		},
		{name: "unknown field", raw: `{"text":"a","confidence":0.5,"extra":1}`, wantErr: true},
		{name: "trailing data", raw: `{"text":"a","confidence":0.5} {"b":1}`, wantErr: true},
		{name: "empty text", raw: `{"text":"","confidence":0.9}`, wantErr: true},
		{name: "whitespace text", raw: `{"text":"   ","confidence":0.9}`, wantErr: true},
		{name: "malformed json", raw: `{text: nope`, wantErr: true},
		{name: "no json at all", raw: "just prose", wantErr: true},
		{name: "missing text field", raw: `{"confidence":0.5}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := DecodeRephraseResponse([]byte(tt.raw))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", resp)
				}
				if !strings.Contains(err.Error(), ErrInvalidResponse.Error()) {
					t.Errorf("error should wrap ErrInvalidResponse: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Text != tt.text || resp.Confidence != tt.conf {
				t.Errorf("got %+v, want text=%q conf=%g", resp, tt.text, tt.conf)
			}
		})
	}
}

func TestValidateRephraseConfidenceBounds(t *testing.T) {
	in := RephraseInput{Summary: "summary only"}
	tests := []struct {
		name    string
		conf    float64
		wantErr bool
	}{
		{name: "at threshold", conf: DefaultRephraseConfidenceThreshold},
		{name: "above threshold", conf: 0.99},
		{name: "below threshold", conf: 0.49, wantErr: true},
		{name: "zero", conf: 0, wantErr: true},
		{name: "negative", conf: -0.1, wantErr: true},
		{name: "above one", conf: 1.1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRephrase(RephraseResponse{Text: "ok", Confidence: tt.conf}, in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRephraseLengthCap(t *testing.T) {
	in := RephraseInput{Summary: strings.Repeat("s", 100)} // cap = 800
	if err := ValidateRephrase(RephraseResponse{Text: strings.Repeat("t", 801), Confidence: 1}, in); err == nil {
		t.Fatal("expected rejection when text exceeds length cap")
	} else if !strings.Contains(err.Error(), ErrRephraseRejected.Error()) {
		t.Errorf("error should wrap ErrRephraseRejected: %v", err)
	}
	long := strings.Repeat("t", 800)
	if err := ValidateRephrase(RephraseResponse{Text: long[:799] + " ", Confidence: 1}, in); err != nil {
		t.Fatalf("text within cap should pass: %v", err)
	}
}

func TestValidateRephraseQuoteCoverage(t *testing.T) {
	base := RephraseInput{
		Title:       "Doc",
		AgentPrompt: "Polish the text.",
		Verdict:     "needs_changes",
		Summary:     "Fix issues.",
		Annotations: []RephraseAnnotation{
			{Quote: "the quick brown fox", StartLine: 12, EndLine: 14},
		},
	}

	tests := []struct {
		name   string
		mutate func(*RephraseInput)
		text   string
		pass   bool
	}{
		{
			name: "verbatim quote accepted",
			text: "Rewrite the sentence containing \"the quick brown fox\" on lines 12-14.",
			pass: true,
		},
		{
			name: "line-range reference accepted",
			text: "Rewrite lines 12-14 to fix the pacing.",
			pass: true,
		},
		{
			name: "bare range token accepted",
			text: "Rewrite 12-14 to fix the pacing.",
			pass: true,
		},
		{
			name: "single-line line reference accepted",
			mutate: func(in *RephraseInput) {
				in.Annotations = []RephraseAnnotation{{Quote: "x² ≥ 0", StartLine: 20, EndLine: 20}}
			},
			text: "Check that line 20 stays valid TeX-adjacent prose.",
			pass: true,
		},
		{
			name: "normalized whitespace match accepted",
			text: "Keep \"the\nquick\tbrown fox\" exactly as written.",
			pass: true,
		},
		{
			name: "dropped quote rejected",
			text: "Rewrite the animal sentence to improve pacing.",
			pass: false,
		},
		{
			name: "wrong line range rejected",
			text: "Rewrite lines 30-32 to fix the pacing.",
			pass: false,
		},
		{
			name: "truncated quote without range rejected",
			text: "Keep \"quick brown\" as is.",
			pass: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			resp := RephraseResponse{Text: tt.text, Confidence: 0.9}
			err := ValidateRephrase(resp, in)
			if tt.pass && err != nil {
				t.Fatalf("expected acceptance, got: %v", err)
			}
			if !tt.pass && err == nil {
				t.Fatal("expected rejection")
			}
			if !tt.pass && !strings.Contains(err.Error(), "annotation quote not covered") {
				t.Errorf("rejection should name the uncovered quote: %v", err)
			}
		})
	}
}

func TestValidateRephraseNoAnnotationsAccepted(t *testing.T) {
	in := RephraseInput{Title: "T", AgentPrompt: "P", Verdict: "approved", Summary: "All good."}
	resp := RephraseResponse{Text: "The instruction is ready to use as written.", Confidence: 0.8}
	if err := ValidateRephrase(resp, in); err != nil {
		t.Fatalf("summary-only reformulation should pass: %v", err)
	}
}

func TestSystemPromptFeedbackRephrase(t *testing.T) {
	got := SystemPrompt(KindFeedbackRephrase)
	if !strings.Contains(got, "JSON only") {
		t.Errorf("system prompt missing JSON instruction: %q", got)
	}
}
