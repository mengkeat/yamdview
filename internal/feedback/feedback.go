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

	"github.com/mengkeat/yamdview/internal/annotation"
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

// Reformulated records the LLM-reformulated consolidation of the review
// feedback, including which provider/model produced it and whether the user
// approved the reformulation.
type Reformulated struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Text           string `json:"text"`
	ApprovedByUser bool   `json:"approved_by_user"`
}

// Payload is the versioned review feedback contract.
type Payload struct {
	Version      int                     `json:"yamdview_feedback_version"`
	SessionID    string                  `json:"session_id"`
	Title        string                  `json:"title"`
	Prompt       string                  `json:"prompt"`
	Verdict      string                  `json:"verdict"`
	Summary      string                  `json:"summary"`
	Comments     []annotation.Annotation `json:"comments"`
	Reformulated *Reformulated           `json:"reformulated,omitempty"`
	Timing       Timing                  `json:"timing"`
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
	for i, comment := range p.Comments {
		if err := comment.Validate(); err != nil {
			return fmt.Errorf("invalid feedback comment %d: %w", i, err)
		}
	}
	if p.Reformulated != nil {
		r := p.Reformulated
		if r.Provider == "" {
			return errors.New("invalid feedback reformulated: provider must not be empty")
		}
		if r.Model == "" {
			return errors.New("invalid feedback reformulated: model must not be empty")
		}
		if r.Text == "" {
			return errors.New("invalid feedback reformulated: text must not be empty")
		}
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

	// Keep the empty comments collection stable even when callers use the zero
	// value for the slice.
	if payload.Comments == nil {
		payload.Comments = []annotation.Annotation{}
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal feedback JSON: %w", err)
	}
	return string(data) + "\n", nil
}

// RenderMarkdown returns the deterministic prose representation described by
// the feedback output contract.
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
	if len(payload.Comments) > 0 {
		lines = append(lines, "### Comments", "")
		for i, comment := range payload.Comments {
			itemHeading := fmt.Sprintf("%d. **%s** (lines %d-%d)", i+1, titleKind(comment.Kind), comment.StartLine, comment.EndLine)
			if comment.Status == annotation.StatusOutdated {
				itemHeading += " **(outdated)**"
			}
			itemHeading += ":"
			lines = append(lines, itemHeading)
			for _, quoteLine := range strings.Split(comment.Quote, "\n") {
				lines = append(lines, "   > "+quoteLine)
			}
			if comment.Comment != "" || comment.SuggestedReplacement != "" {
				lines = append(lines, "")
			}
			if comment.Comment != "" {
				lines = append(lines, indentComment(comment.Comment)...)
			}
			if comment.SuggestedReplacement != "" {
				lines = append(lines, "   Suggested replacement: `"+comment.SuggestedReplacement+"`")
			}
			lines = append(lines, "")
		}
	}
	// Reformulated section format (deterministic; rendered only when
	// payload.Reformulated is non-nil, appended after Comments or after the
	// summary when no comments are present):
	//
	//   ### Consolidated instruction
	//   (<provider>/<model>, approved by user: yes|no)
	//   <blank line>
	//   <text, one output line per source line>
	if r := payload.Reformulated; r != nil {
		approval := "no"
		if r.ApprovedByUser {
			approval = "yes"
		}
		lines = append(
			lines,
			"### Consolidated instruction",
			fmt.Sprintf("(%s/%s, approved by user: %s)", r.Provider, r.Model, approval),
			"",
		)
		lines = append(lines, strings.Split(r.Text, "\n")...)
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n"), nil
}

func titleKind(kind annotation.Kind) string {
	if kind == "" {
		return ""
	}
	return strings.ToUpper(string(kind[:1])) + string(kind[1:])
}

func indentComment(comment string) []string {
	lines := strings.Split(comment, "\n")
	for i := range lines {
		lines[i] = "   " + lines[i]
	}
	return lines
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
		payload.Comments = []annotation.Annotation{}
	}
	return payload, nil
}
