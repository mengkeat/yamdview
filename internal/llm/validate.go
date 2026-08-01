package llm

import (
	"fmt"
	"strings"
)

// ValidationOptions configures the semantic validation of a decoded
// [RepairResponse] against the candidate span it repairs.
type ValidationOptions struct {
	// Kind selects the validation gates: math adds TeX and changed-span
	// locality checks; table adds table parseability and cell preservation.
	Kind RequestKind
	// Span is the exact source text being repaired, used for locality and
	// content-preservation checks. It may be empty to skip those gates.
	Span string
	// MinConfidence is the minimum model-reported confidence to accept.
	// DefaultMinConfidence is used when zero.
	MinConfidence float64
	// MaxExpansion bounds how much larger the replacement may be than the
	// original span, as a ratio (e.g. 4.0 allows a 4x expansion). A zero value
	// uses DefaultMaxExpansion. The check is skipped for very short spans.
	MaxExpansion float64
}

// Defaults applied when ValidationOptions leaves a field at its zero value.
const (
	DefaultMinConfidence = 0.6
	DefaultMaxExpansion  = 5.0
	// minSpanForExpansion is the span length below which the expansion-ratio
	// gate is skipped, because ratios are too noisy for tiny spans.
	minSpanForExpansion = 12
)

// ValidateResponse runs every applicable validation gate against a decoded
// response and returns one [Diagnostic] per failure. An empty slice means the
// response is safe to accept. The caller decides how to surface the failures.
func ValidateResponse(resp RepairResponse, opts ValidationOptions) []Diagnostic {
	minConf := opts.MinConfidence
	if minConf == 0 {
		minConf = DefaultMinConfidence
	}
	maxExp := opts.MaxExpansion
	if maxExp == 0 {
		maxExp = DefaultMaxExpansion
	}

	var diags []Diagnostic

	if resp.Confidence < 0 || resp.Confidence > 1 {
		diags = append(diags, rejectf("confidence %g is outside [0, 1]", resp.Confidence))
	} else if resp.Confidence < minConf {
		diags = append(diags, rejectf("confidence %g below threshold %g", resp.Confidence, minConf))
	}

	replacement := resp.ReplacementMarkdown
	if diags2 := checkForbiddenAdditions(opts.Span, replacement); diags2 != nil {
		diags = append(diags, diags2...)
	}
	if d := checkExpansion(opts.Span, replacement, maxExp); d != nil {
		diags = append(diags, *d)
	}

	switch opts.Kind {
	case KindMathFix:
		diags = append(diags, validateMath(resp, opts.Span)...)
	case KindTableFix:
		diags = append(diags, validateTable(resp, opts.Span)...)
	default:
		diags = append(diags, rejectf("unknown validation kind %q", opts.Kind))
	}

	return diags
}

// checkForbiddenAdditions rejects new markdown constructs an unrelated rewrite
// would introduce: images, links, and raw HTML tags that were absent from the
// original span.
func checkForbiddenAdditions(span, replacement string) []Diagnostic {
	var diags []Diagnostic
	if !containsImage(span) && containsImage(replacement) {
		diags = append(diags, reject("replacement introduces an image not present in the source"))
	}
	if !containsLink(span) && containsLink(replacement) {
		diags = append(diags, reject("replacement introduces a link not present in the source"))
	}
	if !containsRawHTML(span) && containsRawHTML(replacement) {
		diags = append(diags, reject("replacement introduces raw HTML not present in the source"))
	}
	return diags
}

// checkExpansion rejects replacements that grow or shrink far beyond the
// original span. Short spans skip the ratio gate.
func checkExpansion(span, replacement string, maxExp float64) *Diagnostic {
	spanLen := len(strings.TrimSpace(span))
	if spanLen < minSpanForExpansion {
		return nil
	}
	repLen := len(strings.TrimSpace(replacement))
	if repLen == 0 {
		return ptr(reject("replacement is empty"))
	}
	if ratio := float64(repLen) / float64(spanLen); ratio > maxExp {
		return ptr(rejectf("replacement is %.1fx the original span (max %.1fx)", ratio, maxExp))
	}
	if ratio := float64(spanLen) / float64(repLen); spanLen > minSpanForExpansion*4 && ratio > maxExp {
		return ptr(rejectf("replacement shrank to %.0f%% of the original span", 100.0/maxExp))
	}
	return nil
}

// validateMath runs the math-specific gates: TeX sanity and changed-span
// locality.
func validateMath(resp RepairResponse, span string) []Diagnostic {
	var diags []Diagnostic
	if resp.TeX != "" {
		if err := ValidateTeX(resp.TeX); err != nil {
			diags = append(diags, reject(err.Error()))
		}
	}
	for i, cs := range resp.ChangedSpans {
		if cs.Old == "" {
			continue
		}
		if !strings.Contains(span, cs.Old) {
			diags = append(diags, rejectf("changed_spans[%d].old not found in source span", i))
		}
	}
	return diags
}

// validateTable runs the table-specific gates: parseability, no stray prose,
// and approximate cell-content preservation.
func validateTable(resp RepairResponse, span string) []Diagnostic {
	var diags []Diagnostic
	src := []byte(resp.ReplacementMarkdown)
	if !HasTable(src) {
		diags = append(diags, reject("replacement does not parse as a table"))
		return diags
	}
	if !IsTableOnly(src) {
		diags = append(diags, reject("replacement contains prose or blocks outside the table"))
	}
	if d := checkCellPreservation(span, src); d != nil {
		diags = append(diags, *d)
	}
	if d := checkColumnPlausibility(span, src); d != nil {
		diags = append(diags, *d)
	}
	return diags
}

// checkCellPreservation requires every non-empty original cell text to appear
// somewhere in the repaired table (normalized to lowercase, whitespace-folded).
func checkCellPreservation(span string, replacement []byte) *Diagnostic {
	original := tableCellSet([]byte(span))
	if len(original) == 0 {
		return nil
	}
	repaired := tableCellSet(replacement)
	for cell := range original {
		if !repaired[cell] {
			return ptr(rejectf("original cell content %q is missing from the repaired table", cell))
		}
	}
	return nil
}

// checkColumnPlausibility rejects tables whose column count changed by more
// than a factor of two relative to the original, which usually indicates a
// structural rewrite rather than a repair.
func checkColumnPlausibility(span string, replacement []byte) *Diagnostic {
	_, oldCols := TableDimensions([]byte(span))
	_, newCols := TableDimensions(replacement)
	if oldCols == 0 || newCols == 0 {
		return nil
	}
	ratio := float64(newCols) / float64(oldCols)
	if ratio > 2 || ratio < 0.5 {
		return ptr(rejectf("column count changed from %d to %d", oldCols, newCols))
	}
	return nil
}

// tableCellSet returns the set of normalized non-empty cell texts for the first
// table in src. Normalization folds whitespace and lowercases so that repairs
// to whitespace and capitalization do not count as content loss.
func tableCellSet(src []byte) map[string]bool {
	cells := TableCellTexts(src)
	set := make(map[string]bool, len(cells))
	for _, c := range cells {
		normalized := strings.ToLower(strings.Join(strings.Fields(c), " "))
		if normalized == "" {
			continue
		}
		set[normalized] = true
	}
	return set
}

// ── markdown construct detectors ────────────────────────────────────────────

// containsImage reports the presence of image syntax ![...](...).
func containsImage(s string) bool {
	return strings.Contains(s, "![")
}

// containsLink reports the presence of inline link syntax [...](...). Image
// syntax is also [...](...) but starts with !, so exclude images first.
func containsLink(s string) bool {
	s = strings.ReplaceAll(s, "![", "") // strip image starts
	return strings.Contains(s, "](")
}

// containsRawHTML reports the presence of a likely HTML tag: <letter ...>.
func containsRawHTML(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '<' && (isASCIILetterByte(s[i+1]) || s[i+1] == '/') {
			return true
		}
	}
	return false
}

// ── diagnostic helpers ──────────────────────────────────────────────────────

func reject(message string) Diagnostic {
	return Diagnostic{Severity: SeverityWarning, Code: CodeRejected, Message: message}
}

func rejectf(format string, args ...any) Diagnostic {
	return reject(fmt.Sprintf(format, args...))
}

func validationErrorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func ptr(d Diagnostic) *Diagnostic { return &d }
