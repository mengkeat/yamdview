package fixer

import (
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/markdown"
)

func TestCollectTablePatchesFromMalformedTable(t *testing.T) {
	src := "Name | Score\nAlice | 10\nBob | 9\n"
	snapshot := buildSnapshot(t, src)
	patches := CollectTablePatches(snapshot)

	if len(patches) != 1 {
		t.Fatalf("expected 1 table patch, got %d", len(patches))
	}
	p := patches[0]
	if p.Source != SourceHeuristicTable {
		t.Errorf("Source = %q, want %q", p.Source, SourceHeuristicTable)
	}
	if p.OldText != src {
		t.Errorf("OldText = %q, want %q", p.OldText, src)
	}
	if !strings.Contains(p.NewText, "| --- |") {
		t.Errorf("NewText missing repaired separator: %q", p.NewText)
	}
	if p.StartByte != 0 || p.EndByte != len(src) {
		t.Errorf("patch offsets [%d:%d], want [0:%d]", p.StartByte, p.EndByte, len(src))
	}
}

func TestCollectTablePatchesSkipsUnchangedBlocks(t *testing.T) {
	src := "A paragraph without tables.\n\nAnother paragraph.\n"
	snapshot := buildSnapshot(t, src)
	if patches := CollectTablePatches(snapshot); len(patches) != 0 {
		t.Fatalf("expected no table patches, got %d", len(patches))
	}
}

func TestCollectTablePatchesSkipsAmbiguousTables(t *testing.T) {
	// Inconsistent column counts → ambiguous; no destructive repair.
	src := "Name | Score | Note\nAlice | 10\nBob | 9 | ok | extra\n"
	snapshot := buildSnapshot(t, src)
	if patches := CollectTablePatches(snapshot); len(patches) != 0 {
		t.Fatalf("ambiguous table should not produce a patch, got %d", len(patches))
	}
}

func TestCollectMathPatchesForUnicodeEquations(t *testing.T) {
	src := "For all x in R, x^2 >= 0 with αᵢ = βᵢ + γᵢ\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) == 0 {
		t.Fatalf("expected at least one math patch for unicode equation input")
	}
	for _, p := range patches {
		if p.Source != SourceHeuristicMath {
			t.Errorf("patch Source = %q, want %q", p.Source, SourceHeuristicMath)
		}
		if p.StartByte < 0 || p.EndByte > len(src) {
			t.Errorf("patch offsets out of range: [%d:%d]", p.StartByte, p.EndByte)
		}
		if p.OldText != string(src[p.StartByte:p.EndByte]) {
			t.Errorf("OldText does not match recorded offsets: %q vs %q", p.OldText, src[p.StartByte:p.EndByte])
		}
		if !strings.Contains(p.NewText, "$") {
			t.Errorf("NewText should contain math delimiters: %q", p.NewText)
		}
	}
}

func TestCollectMathPatchesEmptyForPlainProse(t *testing.T) {
	src := "Just a regular paragraph with no math.\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 0 {
		t.Fatalf("expected no patches for plain prose, got %d", len(patches))
	}
}

func TestCollectMathPatchesLeavesFencesAlone(t *testing.T) {
	src := "```\nalpha + beta = gamma\n```\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 0 {
		t.Fatalf("fenced code block should not be patched: %+v", patches)
	}
}

func TestCollectMathPatchesHandlesMultipleParagraphs(t *testing.T) {
	src := "α + β = γ\n\nPlain prose paragraph.\n\nα² + β² = γ²\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) < 2 {
		t.Fatalf("expected at least 2 math patches across paragraphs, got %d", len(patches))
	}
	// Patches must not overlap.
	if err := ValidatePatches([]byte(src), patches); err != nil {
		t.Fatalf("math patches failed validation: %v", err)
	}
}

func TestMathPatchesRoundTrip(t *testing.T) {
	src := "α + β = γ\n\nPlain prose.\n"
	patches, err := CollectMathPatches([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) == 0 {
		t.Skip("no math patches produced for this input")
	}
	rewritten := Apply([]byte(src), patches)
	if string(rewritten) == src {
		t.Fatalf("expected rewritten source to differ from original")
	}
	// Re-applying the collector should be idempotent: no more patches.
	patches2, err := CollectMathPatches(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if len(patches2) != 0 {
		t.Errorf("expected idempotent patches, got %d additional patches", len(patches2))
	}
}

func buildSnapshot(t *testing.T, src string) document.DocumentSnapshot {
	t.Helper()
	md := markdown.NewRenderer()
	snapshot, err := document.BuildSnapshot(md, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
