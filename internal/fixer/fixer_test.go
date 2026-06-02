package fixer

import (
	"errors"
	"strings"
	"testing"
)

func TestParseWriteMode(t *testing.T) {
	tests := []struct {
		in   string
		want WriteMode
	}{
		{"", WriteModeNever},
		{"never", WriteModeNever},
		{"NEVER", WriteModeNever},
		{"  backup  ", WriteModeBackup},
		{"in-place", WriteModeInPlace},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseWriteMode(tt.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseWriteModeRejectsUnknown(t *testing.T) {
	_, err := ParseWriteMode("always")
	if err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("expected unknown mode error, got %v", err)
	}
}

func TestSourcePatchValidateAgainstSource(t *testing.T) {
	src := []byte("hello world")
	p := SourcePatch{StartByte: 6, EndByte: 11, OldText: "world"}
	if err := p.ValidateAgainstSource(src); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestSourcePatchRejectsStaleSource(t *testing.T) {
	src := []byte("hello WORLD")
	p := SourcePatch{StartByte: 6, EndByte: 11, OldText: "world"}
	err := p.ValidateAgainstSource(src)
	if !errors.Is(err, ErrPatchOldTextMismatch) {
		t.Fatalf("expected ErrPatchOldTextMismatch, got %v", err)
	}
}

func TestSourcePatchRejectsOutOfRange(t *testing.T) {
	src := []byte("abc")
	p := SourcePatch{StartByte: 0, EndByte: 5, OldText: "abcdef"}
	err := p.ValidateAgainstSource(src)
	if !errors.Is(err, ErrPatchOutOfRange) {
		t.Fatalf("expected ErrPatchOutOfRange, got %v", err)
	}
}

func TestSourcePatchRejectsNegativeOffset(t *testing.T) {
	src := []byte("abc")
	p := SourcePatch{StartByte: -1, EndByte: 1, OldText: "a"}
	err := p.ValidateAgainstSource(src)
	if !errors.Is(err, ErrPatchOutOfRange) {
		t.Fatalf("expected ErrPatchOutOfRange, got %v", err)
	}
}

func TestValidatePatchesRejectsOverlap(t *testing.T) {
	src := []byte("hello world")
	patches := []SourcePatch{
		{StartByte: 0, EndByte: 5, OldText: "hello"},
		{StartByte: 3, EndByte: 8, OldText: "lo wo"},
	}
	err := ValidatePatches(src, patches)
	if !errors.Is(err, ErrPatchOverlap) {
		t.Fatalf("expected ErrPatchOverlap, got %v", err)
	}
}

func TestValidatePatchesAcceptsAdjacent(t *testing.T) {
	src := []byte("hello world")
	patches := []SourcePatch{
		{StartByte: 0, EndByte: 5, OldText: "hello"},
		{StartByte: 5, EndByte: 11, OldText: " world"},
	}
	if err := ValidatePatches(src, patches); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestValidatePatchesRejectsEmptyOldText(t *testing.T) {
	src := []byte("hello world")
	patches := []SourcePatch{
		{StartByte: 0, EndByte: 0, OldText: ""},
	}
	err := ValidatePatches(src, patches)
	if !errors.Is(err, ErrPatchEmptyOld) {
		t.Fatalf("expected ErrPatchEmptyOld, got %v", err)
	}
}

func TestValidatePatchesAcceptsEmptySet(t *testing.T) {
	if err := ValidatePatches([]byte("abc"), nil); err != nil {
		t.Fatalf("expected nil error for empty set, got %v", err)
	}
}

func TestValidatePatchesSortsUnordered(t *testing.T) {
	src := []byte("hello world")
	patches := []SourcePatch{
		{StartByte: 6, EndByte: 11, OldText: "world"},
		{StartByte: 0, EndByte: 5, OldText: "hello"},
	}
	if err := ValidatePatches(src, patches); err != nil {
		t.Fatalf("expected unordered patches to be accepted after sort, got %v", err)
	}
}
