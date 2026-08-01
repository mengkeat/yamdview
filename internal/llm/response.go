package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrInvalidResponse is the sentinel base for response decoding failures.
// Callers wrap the concrete cause with %w so the category stays detectable.
var ErrInvalidResponse = errors.New("invalid llm response")

// ChangedSpan is the LLM's audit trail for a single local edit. Each entry
// records the original text it claims to have replaced and the new text. It is
// used to reject broad rewrites that stray from the candidate span.
type ChangedSpan struct {
	Old    string `json:"old"`
	New    string `json:"new"`
	Reason string `json:"reason,omitempty"`
}

// RepairResponse is the strict contract for a single math or table repair.
// Math responses additionally populate TeX and ChangedSpans; table responses
// leave them empty. Fields mirror the documented JSON schema so that strict
// decoding with DisallowUnknownFields rejects anything else.
type RepairResponse struct {
	ReplacementMarkdown string        `json:"replacement_markdown"`
	TeX                 string        `json:"tex,omitempty"`
	ChangedSpans        []ChangedSpan `json:"changed_spans,omitempty"`
	Explanation         string        `json:"explanation,omitempty"`
	Confidence          float64       `json:"confidence"`
}

// DecodeRepairResponse extracts a single JSON object from raw model text and
// decodes it strictly into a RepairResponse. It tolerates surrounding prose
// and ```json code fences, but it rejects trailing data, unknown fields,
// structurally invalid JSON, and empty replacement_markdown values.
func DecodeRepairResponse(raw []byte) (RepairResponse, error) {
	extracted := ExtractJSONObject(string(raw))

	dec := json.NewDecoder(strings.NewReader(extracted))
	dec.DisallowUnknownFields()

	var resp RepairResponse
	if err := dec.Decode(&resp); err != nil {
		return RepairResponse{}, fmt.Errorf("%w: %s", ErrInvalidResponse, normalizeJSONErr(err))
	}
	// Exactly one JSON object: a second decode must hit EOF.
	var trailing struct{}
	if err := dec.Decode(&trailing); err != nil && !errors.Is(err, io.EOF) {
		return RepairResponse{}, fmt.Errorf("%w: trailing data after JSON object", ErrInvalidResponse)
	}
	if strings.TrimSpace(resp.ReplacementMarkdown) == "" {
		return RepairResponse{}, fmt.Errorf("%w: replacement_markdown is empty", ErrInvalidResponse)
	}
	return resp, nil
}

// ExtractJSONObject locates the first JSON object in s, stripping a leading
// ```json code fence and any surrounding prose. It returns the substring from
// the first '{' to the matching last '}', which is sufficient for a single
// well-formed top-level object. If no object can be located it returns s
// unchanged so that the strict decoder surfaces a clear error.
func ExtractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = strings.TrimSpace(s[nl+1:])
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = strings.TrimSpace(s[:idx])
		}
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return s
	}
	return s[start : end+1]
}

// normalizeJSONErr shortens common json errors into readable messages without
// leaking internal type details.
func normalizeJSONErr(err error) string {
	msg := err.Error()
	// Drop the long "json: cannot unmarshal ..." prefix noise for field errors.
	msg = strings.TrimPrefix(msg, "json: ")
	return msg
}
