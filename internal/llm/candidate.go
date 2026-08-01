package llm

import "time"

// SourceSpan locates a candidate text range within a document for repair. It
// carries enough information to detect staleness: the [Text] must still match
// the live source at [StartByte:EndByte] when a repair result is applied.
type SourceSpan struct {
	// StartByte and EndByte are the byte offsets within the source document.
	StartByte int
	EndByte   int
	// Text is the exact source slice covered by the span, kept verbatim so
	// staleness can be checked by string equality against the live source.
	Text string
}

// Matches reports whether the span still describes the given source. A span is
// stale when the source has been edited since the repair request was issued.
func (s SourceSpan) Matches(src []byte) bool {
	if s.StartByte < 0 || s.EndByte < s.StartByte || s.EndByte > len(src) {
		return false
	}
	return string(src[s.StartByte:s.EndByte]) == s.Text
}

// Severity levels used by llm diagnostics. They mirror the project-wide
// diagnostic severities so callers can map them without translation.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Diagnostic code prefixes emitted by the LLM layer. Each code is stable and
// documented so users and tests can rely on the exact strings.
const (
	// CodeAccepted reports that an LLM repair was validated and applied to the
	// rendered snapshot.
	CodeAccepted = "llm.accepted"
	// CodeRejected reports that an LLM repair was rejected by validation. The
	// message carries the specific reason.
	CodeRejected = "llm.rejected"
	// CodeStale reports that an LLM repair arrived after the source changed,
	// so the result could not be applied safely.
	CodeStale = "llm.stale"
	// CodeTimeout reports that an LLM call exceeded its deadline.
	CodeTimeout = "llm.timeout"
	// CodeFailed reports that an LLM call failed before producing a usable
	// response (network error, provider error, malformed body).
	CodeFailed = "llm.failed"
)

// Diagnostic is a single LLM-layer warning or error. It is intentionally a
// local type so the llm package never imports higher-level packages; callers
// convert it to the project's shared diagnostic type when surfacing.
type Diagnostic struct {
	Severity string
	Code     string
	Message  string
}

// LLMCandidate is the validated, audit-ready outcome of a single repair
// attempt. The zero value is not a valid accepted candidate; callers should
// consult [Diagnostic] codes to distinguish acceptance from rejection.
//
// It records enough provenance (provider, model, prompt/response hashes) to
// support diagnostics and future "write fixes" workflows without logging the
// private source content by default.
type LLMCandidate struct {
	// ProviderName and Model identify the backend that produced the candidate.
	ProviderName string
	Model        string
	// PromptHash and ResponseHash are short hashes of the rendered prompt and
	// raw response text, used for debug logs without exposing full content.
	PromptHash   string
	ResponseHash string
	// SourceSpan is the document range the candidate repairs.
	SourceSpan SourceSpan
	// Replacement is the local Markdown replacement to use in the rendered
	// snapshot or source patch.
	Replacement string
	// TeX is the principal TeX expression for math repairs, when the response
	// supplied one. It is kept separate from Replacement so TeX-specific
	// validation can run against it.
	TeX string
	// Confidence is the model-reported confidence in [0, 1].
	Confidence float64
	// Explanation is the model's human-readable reason for the change.
	Explanation string
	// Diagnostics carries acceptance or rejection diagnostics.
	Diagnostics []Diagnostic
	// Accepted reports whether the candidate passed every validation gate and
	// may be applied. Rejected candidates carry one or more [CodeRejected]
	// diagnostics explaining why.
	Accepted bool
	// CreatedAt is when the candidate was validated, for ordering and
	// staleness checks relative to subsequent source edits.
	CreatedAt time.Time
}
