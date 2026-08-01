// Package fixer defines the safe persistence model for heuristic fixes.
//
// Heuristic repairs (table normalization, Unicode math conversion) currently
// affect only the rendered snapshot. This package provides the structures and
// validation needed to optionally write those repairs back to the original
// Markdown file in a controlled, atomic, auditable way.
package fixer

import (
	"errors"
	"fmt"
	"strings"
)

// WriteMode controls whether validated SourcePatches are persisted to disk.
type WriteMode string

const (
	// WriteModeNever applies fixes to the rendered snapshot only. The source
	// file is never modified. This is the safe default.
	WriteModeNever WriteMode = "never"
	// WriteModeBackup writes a timestamped backup of the original file
	// before applying fixes in place.
	WriteModeBackup WriteMode = "backup"
	// WriteModeInPlace atomically rewrites the original file in place. No
	// backup is created; the user is expected to have their own version
	// control or backup strategy.
	WriteModeInPlace WriteMode = "in-place"
)

// ParseWriteMode converts a CLI string into a WriteMode. Empty input maps to
// WriteModeNever; unknown values are rejected so misconfiguration is loud.
func ParseWriteMode(s string) (WriteMode, error) {
	switch WriteMode(strings.ToLower(strings.TrimSpace(s))) {
	case "", WriteModeNever:
		return WriteModeNever, nil
	case WriteModeBackup:
		return WriteModeBackup, nil
	case WriteModeInPlace:
		return WriteModeInPlace, nil
	default:
		return "", fmt.Errorf("unknown mode %q; valid values: never, backup, in-place", s)
	}
}

// PatchSource identifies the origin of a SourcePatch.
type PatchSource string

const (
	SourceHeuristicTable PatchSource = "heuristic_table"
	SourceHeuristicMath  PatchSource = "heuristic_math"
	SourceHeuristicBoth  PatchSource = "heuristic_table_math"
	SourceLLMTable       PatchSource = "llm_table"
	SourceLLMMath        PatchSource = "llm_math"
)

// SourcePatch is a single local text replacement to apply to the source file.
// It always carries enough information to validate that the source file is in
// the expected state before the replacement is committed.
type SourcePatch struct {
	StartByte  int
	EndByte    int
	OldText    string
	NewText    string
	Reason     string
	Confidence float64
	Source     PatchSource
	// BlockID is an optional stable identifier of the originating block, used
	// for diagnostics and reporting.
	BlockID string
}

// Errors returned by patch validation.
var (
	ErrPatchOutOfRange      = errors.New("patch offsets are out of range for source")
	ErrPatchOldTextMismatch = errors.New("patch OldText does not match the source at the recorded offsets")
	ErrPatchEmptyOld        = errors.New("patch has empty OldText and zero-length offsets")
	ErrPatchOverlap         = errors.New("patches overlap")
)

// ValidateAgainstSource confirms that the patch is well-formed and that the
// current file contents still match the recorded OldText at [StartByte:EndByte].
func (p SourcePatch) ValidateAgainstSource(src []byte) error {
	if p.StartByte < 0 || p.EndByte < p.StartByte || p.EndByte > len(src) {
		return fmt.Errorf("%w: [%d:%d] for source length %d", ErrPatchOutOfRange, p.StartByte, p.EndByte, len(src))
	}
	actual := string(src[p.StartByte:p.EndByte])
	if actual != p.OldText {
		return fmt.Errorf("%w: expected %q, got %q at [%d:%d]", ErrPatchOldTextMismatch, p.OldText, actual, p.StartByte, p.EndByte)
	}
	return nil
}

// ValidatePatches ensures the patch set has no internal overlaps and that
// each patch is well-formed in the context of the provided source.
func ValidatePatches(src []byte, patches []SourcePatch) error {
	if len(patches) == 0 {
		return nil
	}
	sorted := make([]SourcePatch, len(patches))
	copy(sorted, patches)
	sorted = SortPatches(sorted)
	for i, p := range sorted {
		if p.StartByte == p.EndByte && p.OldText == "" {
			return fmt.Errorf("%w at index %d", ErrPatchEmptyOld, i)
		}
		if err := p.ValidateAgainstSource(src); err != nil {
			return fmt.Errorf("patch %d: %w", i, err)
		}
		if i > 0 {
			prev := sorted[i-1]
			if p.StartByte < prev.EndByte {
				return fmt.Errorf("%w: [%d:%d] overlaps [%d:%d]", ErrPatchOverlap, p.StartByte, p.EndByte, prev.StartByte, prev.EndByte)
			}
		}
	}
	return nil
}
