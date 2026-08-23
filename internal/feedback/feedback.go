// Package feedback renders versioned review feedback for agent consumers.
package feedback

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// CurrentVersion is the version of the feedback payload emitted by this
// package.
const CurrentVersion = 1

// Format is a supported feedback output format.
type Format string

const (
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
)

// Timing records the lifecycle timestamps and elapsed time for a review.
type Timing struct {
	OpenedAt    time.Time `json:"opened_at"`
	SubmittedAt time.Time `json:"submitted_at"`
	DurationMS  int64     `json:"duration_ms"`
}

// Payload is the versioned review feedback contract.
//
// Comments is reserved for the annotation schema introduced in Phase 12. It
// is emitted as an empty array in Phase 11 and non-empty values are rejected.
type Payload struct {
	Version   int    `json:"yamdview_feedback_version"`
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Prompt    string `json:"prompt"`
	Verdict   string `json:"verdict"`
	Summary   string `json:"summary"`
	Comments  []any  `json:"comments"`
	Timing    Timing `json:"timing"`
}

// Feedback and ReviewFeedback are descriptive aliases for Payload.
type (
	Feedback       = Payload
	ReviewFeedback = Payload
)

// Validate checks that payload fields supported by this phase are valid.
func (p Payload) Validate() error {
	if p.Version != CurrentVersion {
		return fmt.Errorf("unsupported feedback version %d (want %d)", p.Version, CurrentVersion)
	}
	if len(p.Comments) != 0 {
		return errors.New("feedback comments are not supported yet")
	}
	return nil
}

// ValidateFormat reports whether format is supported by the feedback output
// contract.
func ValidateFormat(format string) error {
	switch Format(format) {
	case FormatJSON, FormatMarkdown:
		return nil
	default:
		return fmt.Errorf("unsupported feedback format %q (want json or markdown)", format)
	}
}

// ParseFormat validates format and returns its typed representation.
func ParseFormat(format string) (Format, error) {
	if err := ValidateFormat(format); err != nil {
		return "", err
	}
	return Format(format), nil
}

// RenderJSON returns deterministic, indented JSON followed by a newline.
func RenderJSON(payload Payload) (string, error) {
	if err := payload.Validate(); err != nil {
		return "", err
	}

	// Keep the empty Phase 11 comments collection stable even when callers use
	// the zero value for the slice.
	payload.Comments = []any{}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal feedback JSON: %w", err)
	}
	return string(data) + "\n", nil
}

// RenderMarkdown returns the deterministic prose representation described by
// the feedback output contract. Annotation comments are intentionally not
// rendered until Phase 12.
func RenderMarkdown(payload Payload) (string, error) {
	if err := payload.Validate(); err != nil {
		return "", err
	}

	heading := "## Review feedback"
	if payload.Title != "" {
		heading += ": " + payload.Title
	}

	lines := []string{
		heading,
		"",
		"**Verdict:** " + strings.ReplaceAll(payload.Verdict, "_", " "),
		"",
	}
	if payload.Summary != "" {
		lines = append(lines, payload.Summary, "")
	}
	return strings.Join(lines, "\n"), nil
}

// Render renders payload using a supported output format.
func Render(payload Payload, format string) (string, error) {
	if err := ValidateFormat(format); err != nil {
		return "", err
	}
	switch Format(format) {
	case FormatJSON:
		return RenderJSON(payload)
	case FormatMarkdown:
		return RenderMarkdown(payload)
	default:
		// ValidateFormat above makes this unreachable, but keeps this function
		// safe if the format switch is extended later.
		return "", fmt.Errorf("unsupported feedback format %q", format)
	}
}

// DecodeJSON strictly decodes one supported feedback payload. Unknown fields
// and trailing JSON values are rejected so versioned output remains explicit.
func DecodeJSON(data []byte) (Payload, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return Payload{}, fmt.Errorf("decode feedback JSON: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Payload{}, errors.New("decode feedback JSON: trailing value")
		}
		return Payload{}, fmt.Errorf("decode feedback JSON: trailing value: %w", err)
	}
	if err := payload.Validate(); err != nil {
		return Payload{}, err
	}
	if payload.Comments == nil {
		payload.Comments = []any{}
	}
	return payload, nil
}
