package llm

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeRepairResponseValid(t *testing.T) {
	raw := []byte(`{"replacement_markdown":"$x^{2}$","tex":"x^{2}","confidence":0.9}`)
	resp, err := DecodeRepairResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ReplacementMarkdown != "$x^{2}$" {
		t.Errorf("got replacement %q", resp.ReplacementMarkdown)
	}
	if resp.TeX != "x^{2}" {
		t.Errorf("got tex %q", resp.TeX)
	}
	if resp.Confidence != 0.9 {
		t.Errorf("got confidence %g", resp.Confidence)
	}
}

func TestDecodeRepairResponseStripsCodeFence(t *testing.T) {
	raw := []byte("Here is the fix:\n```json\n{\"replacement_markdown\":\"| a |\\n| --- |\",\"confidence\":0.8}\n```\n")
	resp, err := DecodeRepairResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ReplacementMarkdown != "| a |\n| --- |" {
		t.Errorf("got replacement %q", resp.ReplacementMarkdown)
	}
}

func TestDecodeRepairResponseUnknownFieldRejected(t *testing.T) {
	raw := []byte(`{"replacement_markdown":"x","confidence":0.5,"bogus":1}`)
	if _, err := DecodeRepairResponse(raw); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("expected ErrInvalidResponse, got %v", err)
	}
}

func TestDecodeRepairResponseTrailingDataRejected(t *testing.T) {
	raw := []byte(`{"replacement_markdown":"x","confidence":0.5}{"extra":1}`)
	if _, err := DecodeRepairResponse(raw); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("expected ErrInvalidResponse for trailing data, got %v", err)
	}
}

func TestDecodeRepairResponseMalformedJSONRejected(t *testing.T) {
	raw := []byte(`{"replacement_markdown":"x", confidence: 0.5}`)
	if _, err := DecodeRepairResponse(raw); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("expected ErrInvalidResponse, got %v", err)
	}
}

func TestDecodeRepairResponseEmptyReplacementRejected(t *testing.T) {
	raw := []byte(`{"replacement_markdown":"   ","confidence":0.5}`)
	if _, err := DecodeRepairResponse(raw); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("expected ErrInvalidResponse for empty replacement, got %v", err)
	}
}

func TestValidateTeX(t *testing.T) {
	tests := []struct {
		name string
		tex  string
		ok   bool
	}{
		{"empty", "", true},
		{"balanced braces", `\frac{1}{2}`, true},
		{"escaped braces", `\{x\}`, true},
		{"balanced leftright", `\left(x\right)`, true},
		{"unbalanced braces", `\frac{1}{2`, false},
		{"unbalanced closing brace", `{x}}`, false},
		{"unbalanced leftright", `\left(x)`, false},
		{"control char", "x\x00y", false},
		{"dangling backslash", `x\`, false},
		{"leftarrow not counted as left", `\leftarrow`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTeX(tt.tex)
			if tt.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("expected failure, got ok")
			}
		})
	}
}

func TestHasTableAndCellTexts(t *testing.T) {
	src := []byte("| Name | Score |\n| --- | --- |\n| Alice | 10 |\n")
	if !HasTable(src) {
		t.Fatal("expected table")
	}
	if !IsTableOnly(src) {
		t.Fatal("expected table-only document")
	}
	cells := TableCellTexts(src)
	want := []string{"Name", "Score", "Alice", "10"}
	if len(cells) != len(want) {
		t.Fatalf("got cells %v, want %v", cells, want)
	}
	for i, w := range want {
		if cells[i] != w {
			t.Errorf("cell[%d] = %q, want %q", i, cells[i], w)
		}
	}
	rows, cols := TableDimensions(src)
	if rows != 2 || cols != 2 {
		t.Errorf("dims = (%d,%d), want (2,2)", rows, cols)
	}
}

func TestIsTableOnlyRejectsStrayProse(t *testing.T) {
	src := []byte("| a | b |\n| --- | --- |\n| 1 | 2 |\n\nSome stray prose.\n")
	if IsTableOnly(src) {
		t.Fatal("expected table+prose to be rejected")
	}
}

func TestValidateResponseAcceptsValidMath(t *testing.T) {
	resp := RepairResponse{
		ReplacementMarkdown: "$x^{2} + y^{2} = z^{2}$",
		TeX:                 "x^{2} + y^{2} = z^{2}",
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindMathFix, Span: "x² + y² = z²"})
	if len(diags) != 0 {
		t.Fatalf("expected acceptance, got %v", diags)
	}
}

func TestValidateResponseRejectsLowConfidence(t *testing.T) {
	resp := RepairResponse{ReplacementMarkdown: "$x$", Confidence: 0.2}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindMathFix, Span: "x²"})
	if !hasCode(diags, CodeRejected) || !anyContains(diags, "confidence") {
		t.Fatalf("expected confidence rejection, got %v", diags)
	}
}

func TestValidateResponseRejectsOutOfRangeConfidence(t *testing.T) {
	resp := RepairResponse{ReplacementMarkdown: "$x$", Confidence: 1.5}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindMathFix, Span: "x"})
	if !anyContains(diags, "outside [0, 1]") {
		t.Fatalf("expected range rejection, got %v", diags)
	}
}

func TestValidateResponseRejectsBadTeX(t *testing.T) {
	resp := RepairResponse{
		ReplacementMarkdown: "$\\frac{1}{2$",
		TeX:                 `\frac{1}{2`,
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindMathFix, Span: "frac"})
	if !anyContains(diags, "brace") {
		t.Fatalf("expected TeX rejection, got %v", diags)
	}
}

func TestValidateResponseRejectsImageInjection(t *testing.T) {
	resp := RepairResponse{
		ReplacementMarkdown: "$x$ ![img](http://evil/a.png)",
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindMathFix, Span: "x"})
	if !anyContains(diags, "image") {
		t.Fatalf("expected image-injection rejection, got %v", diags)
	}
}

func TestValidateResponseRejectsLinkInjection(t *testing.T) {
	resp := RepairResponse{
		ReplacementMarkdown: "see [more](http://evil) x",
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindMathFix, Span: "see x"})
	if !anyContains(diags, "link") {
		t.Fatalf("expected link-injection rejection, got %v", diags)
	}
}

func TestValidateResponseRejectsHugeExpansion(t *testing.T) {
	span := "the quick brown fox jumps" // > minSpanForExpansion
	resp := RepairResponse{
		ReplacementMarkdown: strings.Repeat("a ", 200),
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindMathFix, Span: span, MaxExpansion: 4})
	if !anyContains(diags, "x the original") {
		t.Fatalf("expected expansion rejection, got %v", diags)
	}
}

func TestValidateResponseRejectsChangedSpanNotInSource(t *testing.T) {
	resp := RepairResponse{
		ReplacementMarkdown: "$a$",
		Confidence:          0.9,
		ChangedSpans:        []ChangedSpan{{Old: "not-in-span", New: "a"}},
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindMathFix, Span: "alpha"})
	if !anyContains(diags, "changed_spans") {
		t.Fatalf("expected changed-span locality rejection, got %v", diags)
	}
}

func TestValidateResponseAcceptsValidTable(t *testing.T) {
	span := "Name | Score\nAlice | 10\nBob | 9"
	resp := RepairResponse{
		ReplacementMarkdown: "| Name | Score |\n| --- | --- |\n| Alice | 10 |\n| Bob | 9 |\n",
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindTableFix, Span: span})
	if len(diags) != 0 {
		t.Fatalf("expected acceptance, got %v", diags)
	}
}

func TestValidateResponseRejectsNonTableReplacement(t *testing.T) {
	resp := RepairResponse{
		ReplacementMarkdown: "just some prose",
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindTableFix, Span: "a | b"})
	if !anyContains(diags, "does not parse as a table") {
		t.Fatalf("expected non-table rejection, got %v", diags)
	}
}

func TestValidateResponseRejectsTableWithDroppedCell(t *testing.T) {
	span := "| Name | Score |\n| --- | --- |\n| Alice | 10 |\n"
	resp := RepairResponse{
		ReplacementMarkdown: "| Name | Score |\n| --- | --- |\n| Alice | |\n",
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindTableFix, Span: span})
	if !anyContains(diags, "missing from the repaired table") {
		t.Fatalf("expected cell-preservation rejection, got %v", diags)
	}
}

func TestValidateResponseRejectsImplausibleColumnChange(t *testing.T) {
	span := "| a | b | c | d |\n| --- | --- | --- | --- |\n| 1 | 2 | 3 | 4 |\n"
	resp := RepairResponse{
		ReplacementMarkdown: "| a |\n| --- |\n| 1 |\n",
		Confidence:          0.9,
	}
	diags := ValidateResponse(resp, ValidationOptions{Kind: KindTableFix, Span: span})
	if !anyContains(diags, "column count changed") {
		t.Fatalf("expected column-plausibility rejection, got %v", diags)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

func hasCode(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func anyContains(diags []Diagnostic, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}
