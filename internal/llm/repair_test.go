package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// mathSpan builds a repair request for a math span over src.
func mathSpan(src string) RepairRequest {
	return RepairRequest{
		Kind:      KindMathFix,
		Span:      SourceSpan{StartByte: 0, EndByte: len(src), Text: src},
		BlockKind: "paragraph",
	}
}

func TestRepairMathAccepted(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockText(`{"replacement_markdown":"$E = mc^{2}$","tex":"E = mc^{2}","confidence":0.9}`))

	src := []byte("E = mc^2")
	cand, err := Repair(context.Background(), m, mathSpan("E = mc^2"), src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cand.Accepted {
		t.Fatalf("expected acceptance, got diagnostics: %+v", cand.Diagnostics)
	}
	if cand.Replacement != "$E = mc^{2}$" {
		t.Errorf("replacement = %q", cand.Replacement)
	}
	if cand.TeX != "E = mc^{2}" {
		t.Errorf("tex = %q", cand.TeX)
	}
	if cand.ProviderName != "mock" {
		t.Errorf("provider = %q", cand.ProviderName)
	}
	if cand.PromptHash == "" || cand.ResponseHash == "" {
		t.Error("expected non-empty hashes")
	}
	if !hasCode(cand.Diagnostics, CodeAccepted) {
		t.Errorf("expected %s diagnostic", CodeAccepted)
	}
}

func TestRepairTableAccepted(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockText(`{"replacement_markdown":"| a | b |\n| --- | --- |\n| 1 | 2 |\n","confidence":0.85}`))

	src := []byte("a | b\n1 | 2")
	req := RepairRequest{
		Kind:      KindTableFix,
		Span:      SourceSpan{StartByte: 0, EndByte: len(src), Text: string(src)},
		BlockKind: "table",
	}
	cand, err := Repair(context.Background(), m, req, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cand.Accepted {
		t.Fatalf("expected acceptance, got: %+v", cand.Diagnostics)
	}
}

func TestRepairComplexMathFixes(t *testing.T) {
	tests := []struct {
		name   string
		span   string
		resp   string
		wantTX string
	}{
		{
			"derivative",
			"d^2 x / dt^2 = 0",
			`{"replacement_markdown":"$\\frac{d^{2}x}{dt^{2}} = 0$","tex":"\\frac{d^{2}x}{dt^{2}} = 0","confidence":0.9}`,
			`\frac{d^{2}x}{dt^{2}} = 0`,
		},
		{
			"symbolic fraction",
			"(a+b)/(c+d)",
			`{"replacement_markdown":"$\\frac{a+b}{c+d}$","tex":"\\frac{a+b}{c+d}","confidence":0.9}`,
			`\frac{a+b}{c+d}`,
		},
		{
			"multiline equation",
			"F = ma\nF = dp/dt",
			`{"replacement_markdown":"$$\nF = ma \\\\\nF = \\frac{dp}{dt}\n$$","tex":"F = ma \\\\ F = \\frac{dp}{dt}","confidence":0.8}`,
			`F = ma`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMock("mock")
			m.Queue(MockText(tt.resp))
			cand, err := Repair(context.Background(), m, mathSpan(tt.span), []byte(tt.span))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !cand.Accepted {
				t.Fatalf("expected acceptance for %s, got: %+v", tt.name, cand.Diagnostics)
			}
			if !strings.Contains(cand.TeX, tt.wantTX) {
				t.Errorf("tex = %q, want to contain %q", cand.TeX, tt.wantTX)
			}
		})
	}
}

func TestRepairRejectsInvalidJSON(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockText("totally not json"))
	cand, err := Repair(context.Background(), m, mathSpan("x"), []byte("x"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cand.Accepted {
		t.Fatal("expected rejection")
	}
	if !hasCode(cand.Diagnostics, CodeRejected) {
		t.Errorf("expected %s, got %+v", CodeRejected, cand.Diagnostics)
	}
}

func TestRepairRejectsUnknownFields(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockText(`{"replacement_markdown":"$x$","confidence":0.9,"bogus":1}`))
	cand, _ := Repair(context.Background(), m, mathSpan("x"), []byte("x"))
	if cand.Accepted {
		t.Fatal("expected rejection for unknown fields")
	}
	if !hasCode(cand.Diagnostics, CodeRejected) {
		t.Errorf("expected %s, got %+v", CodeRejected, cand.Diagnostics)
	}
}

func TestRepairRejectsLowConfidence(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockText(`{"replacement_markdown":"$x$","confidence":0.1}`))
	cand, _ := Repair(context.Background(), m, mathSpan("x"), []byte("x"))
	if cand.Accepted {
		t.Fatal("expected rejection for low confidence")
	}
}

func TestRepairRejectsUnrelatedContent(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockText(`{"replacement_markdown":"$x$ and see [more](http://x)","confidence":0.9}`))
	cand, _ := Repair(context.Background(), m, mathSpan("x"), []byte("x"))
	if cand.Accepted {
		t.Fatal("expected rejection for link injection")
	}
}

func TestRepairRejectsBadTeX(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockText(`{"replacement_markdown":"$\\frac{1}{2$","tex":"\\frac{1}{2","confidence":0.9}`))
	cand, _ := Repair(context.Background(), m, mathSpan("frac"), []byte("frac"))
	if cand.Accepted {
		t.Fatal("expected rejection for bad TeX")
	}
}

func TestRepairTimeout(t *testing.T) {
	m := NewMock("mock")
	m.SetDelay(100 * time.Millisecond)
	m.Queue(MockText(`{"replacement_markdown":"$x$","confidence":0.9}`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	cand, err := Repair(ctx, m, mathSpan("x"), []byte("x"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cand.Accepted {
		t.Fatal("expected non-acceptance on timeout")
	}
	if !hasCode(cand.Diagnostics, CodeTimeout) {
		t.Errorf("expected %s, got %+v", CodeTimeout, cand.Diagnostics)
	}
}

func TestRepairStaleOnSourceChange(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockText(`{"replacement_markdown":"$x^{2}$","tex":"x^{2}","confidence":0.9}`))

	// The live source no longer matches the span captured in the request:
	// the user edited the file while the call was in flight.
	cand, err := Repair(context.Background(), m, mathSpan("x^2"), []byte("EDITED"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cand.Accepted {
		t.Fatal("expected staleness, not acceptance")
	}
	if !hasCode(cand.Diagnostics, CodeStale) {
		t.Errorf("expected %s, got %+v", CodeStale, cand.Diagnostics)
	}
}

func TestRepairCancelled(t *testing.T) {
	m := NewMock("mock")
	m.SetDelay(100 * time.Millisecond)
	m.Queue(MockText(`{"replacement_markdown":"$x$","confidence":0.9}`))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	cand, _ := Repair(ctx, m, mathSpan("x"), []byte("x"))
	if !hasCode(cand.Diagnostics, CodeStale) {
		t.Errorf("expected %s for cancellation, got %+v", CodeStale, cand.Diagnostics)
	}
}

func TestRepairFailed(t *testing.T) {
	m := NewMock("mock")
	m.Queue(MockResponse{Err: errors.New("network down")})

	cand, err := Repair(context.Background(), m, mathSpan("x"), []byte("x"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cand.Accepted {
		t.Fatal("expected non-acceptance on failure")
	}
	if !hasCode(cand.Diagnostics, CodeFailed) {
		t.Errorf("expected %s, got %+v", CodeFailed, cand.Diagnostics)
	}
}

func TestRepairNoProvider(t *testing.T) {
	if _, err := Repair(context.Background(), nil, mathSpan("x"), []byte("x")); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

func TestRepairUnsupportedKind(t *testing.T) {
	m := NewMock("mock")
	req := RepairRequest{Kind: KindClassifyCandidate, Span: SourceSpan{Text: "x"}}
	if _, err := Repair(context.Background(), m, req, []byte("x")); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}
