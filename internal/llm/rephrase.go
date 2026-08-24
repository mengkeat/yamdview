package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// RephraseAnnotation is a single review annotation fed into feedback
// reformulation. Quote is the exact source span the reviewer selected;
// StartLine/EndLine locate it in the document (1-based, inclusive); Comment is
// the reviewer's optional remark about the span.
type RephraseAnnotation struct {
	Quote     string
	StartLine int
	EndLine   int
	Comment   string
}

// RephraseInput carries everything the feedback-rephrasing prompt needs: the
// document under review, the original agent instruction, the verdict, the
// reviewer summary, and the individual annotations.
type RephraseInput struct {
	Title       string
	AgentPrompt string
	Verdict     string
	Summary     string
	Annotations []RephraseAnnotation
}

// RenderPrompt renders the user prompt for KindFeedbackRephrase from the
// input. It delegates to the shared template registry so the template stays
// editable without code changes.
func (in RephraseInput) RenderPrompt() (string, error) {
	return RenderPrompt(KindFeedbackRephrase, PromptData{
		Title:       in.Title,
		AgentPrompt: in.AgentPrompt,
		Verdict:     in.Verdict,
		Summary:     in.Summary,
		Annotations: in.Annotations,
	})
}

// RephraseResponse is the strict contract for feedback reformulation: the
// consolidated instruction text plus a confidence score in [0, 1].
type RephraseResponse struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// DecodeRephraseResponse extracts a single JSON object from raw model text and
// decodes it strictly into a RephraseResponse. It tolerates surrounding prose
// and ```json code fences, but rejects trailing data, unknown fields,
// structurally invalid JSON, and empty text values.
func DecodeRephraseResponse(raw []byte) (RephraseResponse, error) {
	extracted := ExtractJSONObject(string(raw))

	dec := json.NewDecoder(strings.NewReader(extracted))
	dec.DisallowUnknownFields()

	var resp RephraseResponse
	if err := dec.Decode(&resp); err != nil {
		return RephraseResponse{}, fmt.Errorf("%w: %s", ErrInvalidResponse, normalizeJSONErr(err))
	}
	// Exactly one JSON object: a second decode must hit EOF.
	var trailing struct{}
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return RephraseResponse{}, fmt.Errorf("%w: trailing data after JSON object", ErrInvalidResponse)
		}
		return RephraseResponse{}, fmt.Errorf("%w: invalid trailing data after JSON object: %s", ErrInvalidResponse, normalizeJSONErr(err))
	}
	if strings.TrimSpace(resp.Text) == "" {
		return RephraseResponse{}, fmt.Errorf("%w: text is empty", ErrInvalidResponse)
	}
	return resp, nil
}

// Validation thresholds for accepted reformulations. They are package-level
// defaults; ValidateRephrase uses them so callers get deterministic behavior
// without configuration.
const (
	// DefaultRephraseConfidenceThreshold is the minimum confidence a
	// reformulation must report to be accepted.
	DefaultRephraseConfidenceThreshold = 0.5
	// DefaultMaxRephraseRatio caps the reformulated text length relative to
	// the structured input size (title + prompt + verdict + summary +
	// quotes/comments, measured in bytes). Reformulation may consolidate and
	// connect the fragments, but an output many times larger than its input
	// indicates invented content rather than rephrasing.
	DefaultMaxRephraseRatio = 8.0
	// minRephraseCapBytes is the absolute floor for the computed length cap so
	// that degenerate tiny inputs still admit a minimal instruction.
	minRephraseCapBytes = 1
)

// ErrRephraseRejected is the sentinel base for semantic validation failures on
// a decoded RephraseResponse. Concrete causes are wrapped with %w.
var ErrRephraseRejected = errors.New("rephrase rejected")

// ValidateRephrase enforces the semantic gates for a reformulated instruction:
//
//  1. Confidence must lie within [0, 1] and reach
//     DefaultRephraseConfidenceThreshold.
//  2. Length cap: Text must not exceed DefaultMaxRephraseRatio times the
//     input size in bytes, subject to the minRephraseCapBytes floor. A
//     non-empty Text shorter than the cap passes.
//  3. Verbatim-quote preservation: every annotation quote must appear in Text
//     either (a) verbatim as an exact substring, (b) via a line-range reference
//     such as "lines 42-44", "42-44", or "line 42", or (c) as a
//     whitespace-normalized substring. The first uncovered quote is reported
//     in the error.
func ValidateRephrase(resp RephraseResponse, input RephraseInput) error {
	return validateRephrase(resp, input, DefaultRephraseConfidenceThreshold, DefaultMaxRephraseRatio)
}

func validateRephrase(resp RephraseResponse, input RephraseInput, minConfidence, maxRatio float64) error {
	if resp.Confidence < 0 || resp.Confidence > 1 {
		return fmt.Errorf("%w: confidence %g outside [0, 1]", ErrRephraseRejected, resp.Confidence)
	}
	if resp.Confidence < minConfidence {
		return fmt.Errorf("%w: confidence %g below threshold %g", ErrRephraseRejected, resp.Confidence, minConfidence)
	}
	if len(resp.Text) < minRephraseCapBytes || strings.TrimSpace(resp.Text) == "" {
		return fmt.Errorf("%w: text is empty", ErrRephraseRejected)
	}
	if maxLen := rephraseCap(input, maxRatio); len(resp.Text) > maxLen {
		return fmt.Errorf("%w: text length %d exceeds cap %d (%gx input)", ErrRephraseRejected, len(resp.Text), maxLen, maxRatio)
	}
	for _, ann := range input.Annotations {
		if !quoteCovered(resp.Text, ann) {
			return fmt.Errorf("%w: annotation quote not covered (lines %d-%d): %q",
				ErrRephraseRejected, ann.StartLine, ann.EndLine, truncateForError(ann.Quote))
		}
	}
	return nil
}

// rephraseCap computes the maximum accepted output length: maxRatio times the
// input size, never below minRephraseCapBytes.
func rephraseCap(input RephraseInput, maxRatio float64) int {
	n := len(input.Title) + len(input.AgentPrompt) + len(input.Verdict) + len(input.Summary)
	for _, ann := range input.Annotations {
		n += len(ann.Quote) + len(ann.Comment)
	}
	cap := int(float64(n) * maxRatio)
	if cap < minRephraseCapBytes {
		return minRephraseCapBytes
	}
	return cap
}

// quoteCovered reports whether an annotation is anchored in text by one of the
// three accepted mechanisms: verbatim substring, line-range reference, or
// whitespace-normalized substring.
func quoteCovered(text string, ann RephraseAnnotation) bool {
	if strings.Contains(text, ann.Quote) {
		return true
	}
	if lineRangeReferenced(text, ann.StartLine, ann.EndLine) {
		return true
	}
	return strings.Contains(normalizeWhitespace(text), normalizeWhitespace(ann.Quote))
}

// lineRangeReferenced reports whether text references the given inclusive
// 1-based line range in one of the accepted spellings: "lines 42-44",
// "line 42" (single-line annotations), or the bare range "42-44".
func lineRangeReferenced(text string, start, end int) bool {
	if start <= 0 || end < start {
		return false
	}
	tokens := []string{
		fmt.Sprintf("lines %d-%d", start, end),
		fmt.Sprintf("%d-%d", start, end),
	}
	if start == end {
		tokens = append(tokens, fmt.Sprintf("line %d", start))
	}
	for _, tok := range tokens {
		if strings.Contains(text, tok) {
			return true
		}
	}
	return false
}

// normalizeWhitespace collapses every run of whitespace into a single space so
// that line wrapping inside quotes or output does not defeat matching.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateForError keeps error messages readable when a quote is long.
func truncateForError(s string) string {
	const maxLen = 80
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
