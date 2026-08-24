package feedback

import (
	"context"
	"errors"
	"strings"

	"github.com/mengkeat/yamdview/internal/annotation"
	"github.com/mengkeat/yamdview/internal/llm"
)

// maxDiagMessage caps diagnostic message length. Provider errors may embed
// truncated HTTP bodies; diagnostics must stay short and never carry prompt
// or response content.
const maxDiagMessage = 200

// ReformulateRequest carries the review-session data fed into feedback
// reformulation. Prompt is the agent's original prompt/banner text.
type ReformulateRequest struct {
	Title   string
	Prompt  string
	Verdict string
	Summary string
}

// ReformulateResult is the outcome of one reformulation attempt. On success
// Applied is true and Reformulated carries the validated text with
// ApprovedByUser left false; callers/UI set approval later. Every failure
// mode (provider error, malformed response, rejected validation) is a silent
// fallback: Applied stays false, Diagnostics explains why, and no error is
// returned so the caller can fall back to raw feedback without special-case
// handling. The caller decides whether to log.
type ReformulateResult struct {
	Applied      bool
	Reformulated *Reformulated
	Diagnostics  []llm.Diagnostic
}

// Reformulate consolidates review annotations into a single reformulated
// instruction using the given provider and model. It builds the rephrase
// prompt from req and annotations, calls the provider, strictly decodes the
// response, and validates it against the semantic gates (confidence,
// length cap, verbatim-quote coverage). Annotations with empty quotes are
// skipped defensively.
//
// Reformulate never panics on bad input and returns a nil error even for a
// nil provider; such conditions surface as an Applied=false result carrying a
// diagnostic instead. The returned error is always nil and exists only to
// keep the signature forward-compatible.
func Reformulate(ctx context.Context, p llm.Provider, model string, req ReformulateRequest, annotations []annotation.Annotation) (ReformulateResult, error) {
	if p == nil {
		return fallback(llm.Diagnostic{
			Severity: llm.SeverityError,
			Code:     llm.CodeFailed,
			Message:  "reformulation skipped: no provider configured",
		}), nil
	}

	input := buildRephraseInput(req, annotations)

	prompt, err := input.RenderPrompt()
	if err != nil {
		return fallback(llm.Diagnostic{
			Severity: llm.SeverityError,
			Code:     llm.CodeFailed,
			Message:  sanitizeMessage("render rephrase prompt: " + err.Error()),
		}), nil
	}

	resp, err := p.Complete(ctx, llm.Request{
		Kind:         llm.KindFeedbackRephrase,
		SystemPrompt: llm.SystemPrompt(llm.KindFeedbackRephrase),
		UserPrompt:   prompt,
		Metadata: map[string]string{
			"provider": p.Name(),
			"model":    model,
		},
	})
	if err != nil {
		return fallback(classifyProviderErr(ctx, err)), nil
	}

	rephrase, err := llm.DecodeRephraseResponse([]byte(resp.Text))
	if err != nil {
		return fallback(llm.Diagnostic{
			Severity: llm.SeverityError,
			Code:     llm.CodeFailed,
			Message:  sanitizeMessage("decode rephrase response: " + err.Error()),
		}), nil
	}

	if err := llm.ValidateRephrase(rephrase, input); err != nil {
		return fallback(llm.Diagnostic{
			Severity: llm.SeverityWarning,
			Code:     llm.CodeRejected,
			Message:  sanitizeMessage(err.Error()),
		}), nil
	}

	resultModel := model
	if resp.Model != "" {
		resultModel = resp.Model
	}
	return ReformulateResult{
		Applied: true,
		Reformulated: &Reformulated{
			Provider:       p.Name(),
			Model:          resultModel,
			Text:           rephrase.Text,
			ApprovedByUser: false,
		},
	}, nil
}

// buildRephraseInput maps the request and annotations onto the llm-layer
// input, skipping annotations whose quote is blank.
func buildRephraseInput(req ReformulateRequest, annotations []annotation.Annotation) llm.RephraseInput {
	input := llm.RephraseInput{
		Title:       req.Title,
		AgentPrompt: req.Prompt,
		Verdict:     req.Verdict,
		Summary:     req.Summary,
	}
	for _, ann := range annotations {
		if strings.TrimSpace(ann.Quote) == "" {
			continue
		}
		input.Annotations = append(input.Annotations, llm.RephraseAnnotation{
			Quote:     ann.Quote,
			StartLine: ann.StartLine,
			EndLine:   ann.EndLine,
			Comment:   ann.Comment,
		})
	}
	return input
}

// classifyProviderErr maps a provider call failure onto a diagnostic. Context
// cancellation and deadlines become timeouts; anything else is a generic
// provider failure.
func classifyProviderErr(ctx context.Context, err error) llm.Diagnostic {
	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && ctx.Err() != nil) {
		return llm.Diagnostic{
			Severity: llm.SeverityWarning,
			Code:     llm.CodeTimeout,
			Message:  "reformulation call timed out or was cancelled",
		}
	}
	return llm.Diagnostic{
		Severity: llm.SeverityError,
		Code:     llm.CodeFailed,
		Message:  sanitizeMessage("reformulation call failed: " + err.Error()),
	}
}

// fallback builds the silent-fallback result for the given diagnostic.
func fallback(diag llm.Diagnostic) ReformulateResult {
	return ReformulateResult{Diagnostics: []llm.Diagnostic{diag}}
}

// sanitizeMessage collapses newlines and caps length so provider error text
// (which may include truncated HTTP bodies) stays out of logs at full size.
func sanitizeMessage(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > maxDiagMessage {
		runes := []rune(msg)
		if len(runes) <= maxDiagMessage {
			return msg[:maxDiagMessage]
		}
		msg = string(runes[:maxDiagMessage]) + "…"
	}
	return msg
}
