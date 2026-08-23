package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNoProvider is returned by Repair when no provider is available.
var ErrNoProvider = errors.New("no llm provider configured")

// RepairRequest describes a single narrow repair attempt against one candidate
// span. The orchestrator renders a prompt, calls the provider, strictly decodes
// the response, and runs every semantic gate before the result may be applied.
type RepairRequest struct {
	// Kind selects the prompt template and validation gates (math or table).
	Kind RequestKind
	// Span locates the candidate within the source. Its Text is checked against
	// the live source after the provider returns to detect staleness.
	Span SourceSpan
	// BlockKind is the document block kind the candidate came from.
	BlockKind string
	// Context is optional surrounding text for disambiguation.
	Context string
	// Diagnostics are the deterministic diagnostics that triggered fallback.
	Diagnostics []string
	// HeuristicTeX is an existing heuristic TeX candidate (math only).
	HeuristicTeX string
	// MinConfidence overrides the default confidence threshold. Zero uses the
	// validator default.
	MinConfidence float64
	// MaxExpansion overrides the default replacement size ratio. Zero uses the
	// validator default.
	MaxExpansion float64
	// MaxTokens caps the provider response length. Zero leaves the provider
	// default.
	MaxTokens int
	// Metadata is opaque per-call context forwarded to the provider.
	Metadata map[string]string
}

// Repair executes one repair attempt end to end and returns an audit-ready
// candidate. It never applies the result; the caller decides whether to use
// [LLMCandidate.Replacement].
//
// The returned error is non-nil only for caller mistakes (nil provider,
// unsupported kind, prompt rendering failure). Every provider outcome —
// timeout, cancellation, malformed output, validation rejection, staleness —
// is a normal result reported via [LLMCandidate.Diagnostics] with the
// appropriate code, and Accepted set accordingly.
//
// currentSource is the live source at apply time; if the candidate span no
// longer matches it (the document was edited during the call), the result is
// marked stale ([CodeStale]).
func Repair(ctx context.Context, p Provider, req RepairRequest, currentSource []byte) (LLMCandidate, error) { //nolint:nilerr // provider failures are normal results
	cand := LLMCandidate{ProviderName: "none", SourceSpan: req.Span, CreatedAt: time.Now()}
	if p == nil {
		return cand, ErrNoProvider
	}
	cand.ProviderName = p.Name()

	if req.Kind != KindMathFix && req.Kind != KindTableFix {
		return cand, fmt.Errorf("unsupported repair kind %q", req.Kind)
	}

	userPrompt, err := RenderPrompt(req.Kind, PromptData{
		Kind:         req.Kind,
		BlockKind:    req.BlockKind,
		SourceSpan:   req.Span.Text,
		Context:      req.Context,
		Diagnostics:  req.Diagnostics,
		HeuristicTeX: req.HeuristicTeX,
	})
	if err != nil {
		return cand, err
	}
	cand.PromptHash = hashShort(userPrompt)

	resp, err := p.Complete(ctx, Request{
		Kind:         req.Kind,
		SystemPrompt: SystemPrompt(req.Kind),
		UserPrompt:   userPrompt,
		Temperature:  0.2,
		MaxTokens:    req.MaxTokens,
		Metadata:     req.Metadata,
	})
	if err != nil {
		// Provider failures are normal repair outcomes, not caller errors.
		cand.Diagnostics = []Diagnostic{classifyProviderErr(err)}
		return cand, nil
	}
	cand.ResponseHash = hashShort(resp.Text)
	cand.Model = resp.Model

	// Staleness: the document may have changed while the call was in flight.
	if !req.Span.Matches(currentSource) {
		cand.Diagnostics = []Diagnostic{{
			Severity: SeverityInfo,
			Code:     CodeStale,
			Message:  "llm repair arrived after the source changed; result ignored",
		}}
		return cand, nil
	}

	decoded, err := DecodeRepairResponse([]byte(resp.Text))
	if err != nil {
		cand.Diagnostics = []Diagnostic{{
			Severity: SeverityWarning,
			Code:     CodeRejected,
			Message:  "invalid llm response: " + err.Error(),
		}}
		return cand, nil
	}

	failures := ValidateResponse(decoded, ValidationOptions{
		Kind:          req.Kind,
		Span:          req.Span.Text,
		MinConfidence: req.MinConfidence,
		MaxExpansion:  req.MaxExpansion,
	})
	if len(failures) > 0 {
		cand.Diagnostics = failures
		return cand, nil
	}

	cand.Accepted = true
	cand.Replacement = decoded.ReplacementMarkdown
	cand.TeX = decoded.TeX
	cand.Confidence = decoded.Confidence
	cand.Explanation = decoded.Explanation
	cand.Diagnostics = []Diagnostic{{
		Severity: SeverityInfo,
		Code:     CodeAccepted,
		Message:  fmt.Sprintf("llm repair accepted (confidence %.2f, provider %s)", decoded.Confidence, p.Name()),
	}}
	return cand, nil
}

// classifyProviderErr maps a provider error to a diagnostic with the right
// stable code: timeouts, cancellation (treated as stale), and other failures.
func classifyProviderErr(err error) Diagnostic {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return Diagnostic{Severity: SeverityWarning, Code: CodeTimeout, Message: "llm call timed out"}
	case errors.Is(err, context.Canceled):
		return Diagnostic{Severity: SeverityInfo, Code: CodeStale, Message: "llm call cancelled; result is no longer relevant"}
	default:
		return Diagnostic{Severity: SeverityError, Code: CodeFailed, Message: "llm call failed: " + err.Error()}
	}
}

// hashShort returns a short hex hash of s for debug provenance without logging
// the full content by default.
func hashShort(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
