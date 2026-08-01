// Package llmapp connects the vendor-neutral [llm] repair pipeline to the
// document snapshot model. It scans a snapshot for blocks carrying
// diagnostics that warrant LLM fallback, runs a narrow repair through a
// provider, and applies only validated results to the rendered snapshot.
//
// Rejected, stale, timed-out, or failed repairs never mutate the snapshot;
// they are reported as diagnostics so the original rendering is preserved.
package llmapp

import (
	"context"

	"github.com/yuin/goldmark"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/llm"
	"github.com/mengkeat/yamdview/internal/markdown"
)

// Diagnostic codes that should trigger an LLM repair attempt, mapped to the
// repair kind they request.
var triggerKinds = map[string]llm.RequestKind{
	"table.ambiguous": llm.KindTableFix,
	"table.unfixable": llm.KindTableFix,
	"math.unresolved": llm.KindMathFix,
}

// Result is the outcome of a repair pass over one snapshot.
type Result struct {
	// Snapshot is a copy of the input snapshot with accepted repairs applied
	// (re-rendered block HTML and updated diagnostics).
	Snapshot document.DocumentSnapshot
	// Diagnostics is a flat list of every LLM diagnostic produced, in document
	// form, for CLI/browser surfacing.
	Diagnostics []document.Diagnostic
	// Applied and Rejected count accepted and not-accepted candidates.
	Applied  int
	Rejected int
}

// Repair runs the LLM repair pass over snap. It is a no-op when p is nil (LLM
// disabled) or when snap is a full-reset-only document with no block list.
//
// src is the live source used for staleness checks: if a block's source
// changed between snapshot build and repair completion, that candidate is
// reported stale and left untouched.
func Repair(ctx context.Context, md goldmark.Markdown, p llm.Provider, snap document.DocumentSnapshot, src []byte) Result {
	out := Result{Snapshot: cloneSnapshot(snap)}
	if p == nil || out.Snapshot.FullResetOnly || len(out.Snapshot.Blocks) == 0 {
		return out
	}

	for i := range out.Snapshot.Blocks {
		block := &out.Snapshot.Blocks[i]
		kind, trigger := repairKind(block.Diagnostics)
		if !trigger {
			continue
		}

		cand, err := llm.Repair(ctx, p, llm.RepairRequest{
			Kind:      kind,
			Span:      llm.SourceSpan{StartByte: block.SourceStart, EndByte: block.SourceEnd, Text: block.Source},
			BlockKind: string(block.Kind),
		}, src)
		if err != nil {
			// Only caller mistakes (nil provider/kind) reach here; the
			// provider path reports outcomes via diagnostics. Skip defensively.
			continue
		}

		out.Diagnostics = append(out.Diagnostics, toDocumentDiagnostics(cand.Diagnostics, block)...)

		if !cand.Accepted {
			// Keep the original block; record the rejection diagnostic on it
			// so the badge is visible, but do not change its HTML.
			block.Diagnostics = append(block.Diagnostics, toDocumentDiagnostics(cand.Diagnostics, block)...)
			out.Rejected++
			continue
		}

		rendered, err := markdown.Render(md, []byte(cand.Replacement))
		if err != nil {
			rej := document.Diagnostic{
				Severity:  "warning",
				Code:      llm.CodeRejected,
				Message:   "llm replacement did not re-render: " + err.Error(),
				BlockID:   block.ID,
				StartLine: block.StartLine,
				EndLine:   block.EndLine,
			}
			out.Diagnostics = append(out.Diagnostics, rej)
			block.Diagnostics = append(block.Diagnostics, rej)
			out.Rejected++
			continue
		}

		block.HTML = rendered
		block.Normalized = cand.Replacement
		// Replace the trigger diagnostics with the acceptance badge so the
		// block no longer advertises itself as unfixable.
		block.Diagnostics = toDocumentDiagnostics(cand.Diagnostics, block)
		out.Applied++
	}

	out.Snapshot.HTML = document.RenderBlocks(out.Snapshot.Blocks)
	return out
}

// repairKind reports whether a block's diagnostics include a trigger code and
// returns the repair kind to use.
func repairKind(diags []document.Diagnostic) (llm.RequestKind, bool) {
	for _, d := range diags {
		if kind, ok := triggerKinds[d.Code]; ok {
			return kind, true
		}
	}
	return "", false
}

// toDocumentDiagnostics converts llm diagnostics to document diagnostics,
// attaching the block id and line range.
func toDocumentDiagnostics(diags []llm.Diagnostic, block *document.Block) []document.Diagnostic {
	out := make([]document.Diagnostic, 0, len(diags))
	for _, d := range diags {
		severity := d.Severity
		if severity == "" {
			severity = "warning"
		}
		out = append(out, document.Diagnostic{
			Severity:  severity,
			Code:      d.Code,
			Message:   d.Message,
			BlockID:   block.ID,
			StartLine: block.StartLine,
			EndLine:   block.EndLine,
		})
	}
	return out
}

// cloneSnapshot returns a deep-enough copy of snap: the blocks slice and each
// block's diagnostics slice are copied so repairs never mutate the caller's
// snapshot. String fields are immutable and need not be copied.
func cloneSnapshot(s document.DocumentSnapshot) document.DocumentSnapshot {
	out := s
	out.Blocks = make([]document.Block, len(s.Blocks))
	for i := range s.Blocks {
		out.Blocks[i] = s.Blocks[i]
		out.Blocks[i].Diagnostics = append([]document.Diagnostic(nil), s.Blocks[i].Diagnostics...)
	}
	return out
}
