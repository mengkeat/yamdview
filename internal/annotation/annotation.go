// Package annotation models review annotations and resolves rendered text back
// to source ranges without making ambiguous guesses.
package annotation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mengkeat/yamdview/internal/document"
)

// Kind identifies the user's intent for an annotation.
type Kind string

const (
	KindComment    Kind = "comment"
	KindSuggestion Kind = "suggestion"
	KindQuestion   Kind = "question"
	KindConcern    Kind = "concern"
	KindApproval   Kind = "approval"

	// Short aliases mirror the values used by the feedback contract.
	Comment    = KindComment
	Suggestion = KindSuggestion
	Question   = KindQuestion
	Concern    = KindConcern
	Approval   = KindApproval
)

// Status describes whether an annotation still has a source anchor.
type Status string

const (
	StatusActive   Status = "active"
	StatusOutdated Status = "outdated"

	Active   = StatusActive
	Outdated = StatusOutdated
)

// SourceSpan is a byte range in the document source. EndByte is exclusive.
// Text is retained in memory as a staleness check, but is omitted from the
// feedback wire format because Quote is the user-visible anchor.
type SourceSpan struct {
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	Text      string `json:"-"`
}

// Matches reports whether the span still identifies the same source text.
func (s SourceSpan) Matches(source []byte) bool {
	return s.StartByte >= 0 && s.EndByte >= s.StartByte &&
		s.EndByte <= len(source) && string(source[s.StartByte:s.EndByte]) == s.Text
}

// Annotation is a review note anchored to one rendered document block.
type Annotation struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	GroupID   string `json:"group_id,omitempty"`
	BlockID   string `json:"block_id"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`

	Quote  string `json:"quote"`
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`

	SourceSpan           *SourceSpan `json:"source_span,omitempty"`
	Comment              string      `json:"comment,omitempty"`
	SuggestedReplacement string      `json:"suggested_replacement,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Status    Status    `json:"status,omitempty"`
}

// Validate checks the stable, user-facing annotation fields.
func (a Annotation) Validate() error {
	switch a.Kind {
	case KindComment, KindSuggestion, KindQuestion, KindConcern, KindApproval:
	default:
		return fmt.Errorf("unsupported annotation kind %q", a.Kind)
	}
	if a.BlockID == "" {
		return errors.New("annotation block_id is required")
	}
	if strings.TrimSpace(a.Quote) == "" {
		return errors.New("annotation quote is required")
	}
	if a.Status != "" && a.Status != StatusActive && a.Status != StatusOutdated {
		return fmt.Errorf("unsupported annotation status %q", a.Status)
	}
	return nil
}

// SelectionPiece is the part of a browser selection belonging to one block.
type SelectionPiece struct {
	BlockID   string `json:"block_id"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Quote     string `json:"quote"`
	Prefix    string `json:"prefix,omitempty"`
	Suffix    string `json:"suffix,omitempty"`
}

// QuotePiece is a descriptive alias for SelectionPiece.
type QuotePiece = SelectionPiece

// SplitSelection creates one comment annotation per block in a selection.
// Multi-block selections receive one deterministic shared group ID. A single
// piece deliberately has no group ID, since it is not a group.
func SplitSelection(pieces []SelectionPiece) ([]Annotation, error) {
	if len(pieces) == 0 {
		return []Annotation{}, nil
	}
	for i, piece := range pieces {
		if piece.BlockID == "" {
			return nil, fmt.Errorf("selection piece %d has no block_id", i)
		}
		if strings.TrimSpace(piece.Quote) == "" {
			return nil, fmt.Errorf("selection piece %d has no quote", i)
		}
	}

	groupID := ""
	if len(pieces) > 1 {
		groupID = groupIDFor(pieces)
	}
	annotations := make([]Annotation, len(pieces))
	for i, piece := range pieces {
		annotations[i] = Annotation{
			Kind:      KindComment,
			GroupID:   groupID,
			BlockID:   piece.BlockID,
			StartLine: piece.StartLine,
			EndLine:   piece.EndLine,
			Quote:     piece.Quote,
			Prefix:    piece.Prefix,
			Suffix:    piece.Suffix,
			Status:    StatusActive,
		}
	}
	return annotations, nil
}

// GroupIDForPieces returns the stable group ID used for a multi-block
// Split is a concise alias for SplitSelection.
func Split(pieces []SelectionPiece) ([]Annotation, error) {
	return SplitSelection(pieces)
}

// GroupIDForPieces returns the stable group ID used for a multi-block
// selection. It returns an empty string for zero or one piece.
func GroupIDForPieces(pieces []SelectionPiece) string {
	if len(pieces) <= 1 {
		return ""
	}
	return groupIDFor(pieces)
}

func groupIDFor(pieces []SelectionPiece) string {
	var b strings.Builder
	for _, piece := range pieces {
		fmt.Fprintf(&b, "%d:%s%d:%s%d:%s%d:%s", len(piece.BlockID), piece.BlockID,
			len(piece.Quote), piece.Quote, len(piece.Prefix), piece.Prefix,
			len(piece.Suffix), piece.Suffix)
	}
	h := sha256.Sum256([]byte(b.String()))
	return "group-" + hex.EncodeToString(h[:])[:16]
}

// Resolve resolves an annotation's quote in its anchored block. It returns
// nil when the block is absent, the quote is absent, or the match is not
// unique.
func Resolve(snapshot document.DocumentSnapshot, annotation Annotation) *SourceSpan {
	block, ok := findBlock(snapshot, annotation.BlockID)
	if !ok {
		return nil
	}
	return ResolveInBlock(block, annotation.Quote, annotation.Prefix, annotation.Suffix)
}

// ResolveAnnotation is an explicit alias for Resolve.
func ResolveAnnotation(snapshot document.DocumentSnapshot, annotation Annotation) *SourceSpan {
	return Resolve(snapshot, annotation)
}

// ResolveSourceSpan is a descriptive alias for Resolve.
func ResolveSourceSpan(snapshot document.DocumentSnapshot, annotation Annotation) *SourceSpan {
	return Resolve(snapshot, annotation)
}

// ResolveQuote resolves a complete text-quote anchor in a snapshot.
func ResolveQuote(snapshot document.DocumentSnapshot, annotation Annotation) *SourceSpan {
	return Resolve(snapshot, annotation)
}

// ResolveTextQuote resolves a text quote when the caller has the anchor fields
// separately rather than as an Annotation.
func ResolveTextQuote(snapshot document.DocumentSnapshot, blockID, quote, prefix, suffix string) *SourceSpan {
	block, ok := findBlock(snapshot, blockID)
	if !ok {
		return nil
	}
	return ResolveInBlock(block, quote, prefix, suffix)
}

// ResolveInBlock resolves quote against one block's original source. Matching
// is attempted from most trustworthy to least: exact source, source with
// whitespace normalised, then a conservative Markdown-text projection. Each
// stage must produce exactly one candidate; ambiguity is never resolved by
// choosing the first result.
func ResolveInBlock(block document.Block, quote, prefix, suffix string) *SourceSpan {
	if strings.TrimSpace(quote) == "" || block.Source == "" {
		return nil
	}

	if candidates := exactCandidates(block.Source, quote); len(candidates) > 0 {
		candidates = filterContext(candidates, block.Source, prefix, suffix)
		if len(candidates) == 1 {
			return spanFor(block, candidates[0].start, candidates[0].end)
		}
		if len(candidates) > 1 {
			return nil
		}
	}

	if candidates := normalizedCandidates(block.Source, quote); len(candidates) > 0 {
		candidates = filterMappedContext(candidates, prefix, suffix)
		if len(candidates) == 1 {
			return spanFor(block, candidates[0].start, candidates[0].end)
		}
		if len(candidates) > 1 {
			return nil
		}
	}

	projected := markdownText(block.Source)
	if candidates := normalizedMappedCandidates(projected, quote); len(candidates) > 0 {
		candidates = filterMappedContext(candidates, prefix, suffix)
		if len(candidates) == 1 {
			return spanFor(block, candidates[0].start, candidates[0].end)
		}
	}
	return nil
}

// Reanchor updates one annotation against a new snapshot. If the original
// block no longer exists, a quote is accepted from another block only when it
// resolves uniquely across the entire snapshot. This permits safe block
// renumbering while preventing a repeated quote from being guessed.
func Reanchor(annotation Annotation, snapshot document.DocumentSnapshot) Annotation {
	updated := annotation
	if block, ok := findBlock(snapshot, annotation.BlockID); ok {
		if span := ResolveInBlock(block, annotation.Quote, annotation.Prefix, annotation.Suffix); span != nil {
			return anchored(updated, block, span)
		}
		updated.SourceSpan = nil
		updated.Status = StatusOutdated
		return updated
	}

	var found *SourceSpan
	var foundBlock document.Block
	for _, block := range snapshot.Blocks {
		span := ResolveInBlock(block, annotation.Quote, annotation.Prefix, annotation.Suffix)
		if span == nil {
			continue
		}
		if found != nil {
			updated.SourceSpan = nil
			updated.Status = StatusOutdated
			return updated
		}
		found, foundBlock = span, block
	}
	if found == nil {
		updated.SourceSpan = nil
		updated.Status = StatusOutdated
		return updated
	}
	return anchored(updated, foundBlock, found)
}

// ReanchorAll applies Reanchor without changing annotation order.
func ReanchorAll(annotations []Annotation, snapshot document.DocumentSnapshot) []Annotation {
	updated := make([]Annotation, len(annotations))
	for i, annotation := range annotations {
		updated[i] = Reanchor(annotation, snapshot)
	}
	return updated
}

// ReanchorAnnotations is an explicit alias for ReanchorAll.
func ReanchorAnnotations(annotations []Annotation, snapshot document.DocumentSnapshot) []Annotation {
	return ReanchorAll(annotations, snapshot)
}

// ReanchorAnnotation is an explicit singular alias for Reanchor.
func ReanchorAnnotation(annotation Annotation, snapshot document.DocumentSnapshot) Annotation {
	return Reanchor(annotation, snapshot)
}

func anchored(annotation Annotation, block document.Block, span *SourceSpan) Annotation {
	annotation.BlockID = block.ID
	annotation.StartLine = block.StartLine
	annotation.EndLine = block.EndLine
	annotation.SourceSpan = span
	annotation.Status = StatusActive
	return annotation
}

func findBlock(snapshot document.DocumentSnapshot, id string) (document.Block, bool) {
	for _, block := range snapshot.Blocks {
		if block.ID == id {
			return block, true
		}
	}
	return document.Block{}, false
}

type candidate struct {
	start int
	end   int
	// contextStart and contextEnd are offsets in projected, which may differ
	// from source offsets when syntax or whitespace was removed.
	contextStart int
	contextEnd   int
	// projected is the text used for context matching. For source candidates it
	// is the source itself; for rendered candidates it is the projection.
	projected string
}

func exactCandidates(source, quote string) []candidate {
	var result []candidate
	for at := 0; ; {
		i := strings.Index(source[at:], quote)
		if i < 0 {
			break
		}
		i += at
		result = append(result, candidate{start: i, end: i + len(quote), contextStart: i, contextEnd: i + len(quote), projected: source})
		at = i + 1
		if at > len(source) {
			break
		}
	}
	return result
}

func normalizedCandidates(source, quote string) []candidate {
	return normalizedMappedCandidates(mappedSource(source), quote)
}

type mappedRune struct {
	r     rune
	start int
	end   int
}

type mappedText struct {
	text  string
	items []mappedRune
}

func mappedSource(source string) mappedText {
	var out mappedText
	for i := 0; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if unicode.IsSpace(r) {
			start := i
			end := i + size
			for end < len(source) {
				next, nextSize := utf8.DecodeRuneInString(source[end:])
				if !unicode.IsSpace(next) {
					break
				}
				end += nextSize
			}
			out.items = append(out.items, mappedRune{r: ' ', start: start, end: end})
			i = end
			continue
		}
		out.items = append(out.items, mappedRune{r: r, start: i, end: i + size})
		i += size
	}
	out.text = mappedItemsText(out.items)
	return out
}

func mappedItemsText(items []mappedRune) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteRune(item.r)
	}
	return b.String()
}

func normalizedMappedCandidates(source mappedText, quote string) []candidate {
	query := normalizeQuote(quote)
	if query == "" || source.text == "" {
		return nil
	}
	var result []candidate
	for at := 0; ; {
		i := strings.Index(source.text[at:], query)
		if i < 0 {
			break
		}
		i += at
		end := i + len(query)
		startItem, endItem, ok := mappedRange(source.items, i, end)
		if ok {
			result = append(result, candidate{
				start: startItem.start, end: endItem.end,
				contextStart: i, contextEnd: end, projected: source.text,
			})
		}
		at = i + 1
		if at > len(source.text) {
			break
		}
	}
	return result
}

func mappedRange(items []mappedRune, start, end int) (mappedRune, mappedRune, bool) {
	position := 0
	first, last := -1, -1
	for i, item := range items {
		size := utf8.RuneLen(item.r)
		if position < end && position+size > start {
			if first < 0 {
				first = i
			}
			last = i
		}
		position += size
		if position >= end {
			break
		}
	}
	if first < 0 || last < 0 {
		return mappedRune{}, mappedRune{}, false
	}
	return items[first], items[last], true
}

func filterContext(candidates []candidate, source, prefix, suffix string) []candidate {
	for i := range candidates {
		candidates[i].projected = source
		candidates[i].contextStart = candidates[i].start
		candidates[i].contextEnd = candidates[i].end
	}
	return filterMappedContext(candidates, prefix, suffix)
}

func filterMappedContext(candidates []candidate, prefix, suffix string) []candidate {
	if prefix == "" && suffix == "" {
		return candidates
	}
	result := candidates[:0]
	for _, candidate := range candidates {
		if contextMatches(candidate.projected, candidate.contextStart, candidate.contextEnd, prefix, suffix) {
			result = append(result, candidate)
		}
	}
	// Context is only disambiguating evidence. If it was captured from a
	// transformed renderer and cannot be represented in source, keep the
	// original candidates rather than making a false negative for a unique
	// quote. It is used only when it actually narrows the set.
	if len(result) == 0 {
		return candidates
	}
	return result
}

func contextMatches(text string, start, end int, prefix, suffix string) bool {
	before := text[:start]
	after := text[end:]
	prefix = normalizeQuote(prefix)
	suffix = normalizeQuote(suffix)
	if prefix != "" && !strings.HasSuffix(normalizeQuote(before), prefix) {
		return false
	}
	if suffix != "" && !strings.HasPrefix(normalizeQuote(after), suffix) {
		return false
	}
	return true
}

func normalizeQuote(value string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func spanFor(block document.Block, start, end int) *SourceSpan {
	absoluteStart := block.SourceStart + start
	absoluteEnd := block.SourceStart + end
	return &SourceSpan{StartByte: absoluteStart, EndByte: absoluteEnd, Text: block.Source[start:end]}
}

// markdownText produces a source-to-text projection for the syntax that most
// commonly disappears between Markdown and a rendered selection. It keeps a
// byte mapping for every retained rune. Deliberately unsupported or complex
// constructs fall through to source matching instead of being interpreted
// heuristically.
func markdownText(source string) mappedText {
	masked := make([]bool, len(source))
	maskLinePrefixes(source, masked)
	maskLinksAndTags(source, masked)
	maskEmphasis(source, masked)

	items := make([]mappedRune, 0, utf8.RuneCountInString(source))
	for i := 0; i < len(source); {
		r, size := utf8.DecodeRuneInString(source[i:])
		if !masked[i] {
			items = append(items, mappedRune{r: r, start: i, end: i + size})
		}
		i += size
	}
	mapped := mappedText{items: items}
	mapped.text = mappedItemsText(items)
	return mappedSourceFromItems(mapped)
}

func mappedSourceFromItems(source mappedText) mappedText {
	// Rendered selections collapse runs of source whitespace in the same way
	// as the normalisation stage. Rebuild the map while retaining source bounds.
	var out mappedText
	for i := 0; i < len(source.items); {
		item := source.items[i]
		if unicode.IsSpace(item.r) {
			start, end := item.start, item.end
			for i+1 < len(source.items) && unicode.IsSpace(source.items[i+1].r) {
				i++
				end = source.items[i].end
			}
			out.items = append(out.items, mappedRune{r: ' ', start: start, end: end})
		} else {
			out.items = append(out.items, item)
		}
		i++
	}
	out.text = mappedItemsText(out.items)
	return out
}

func maskLinePrefixes(source string, masked []bool) {
	lineStart := 0
	for lineStart < len(source) {
		lineEnd := strings.IndexByte(source[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source)
		} else {
			lineEnd += lineStart
		}
		left := lineStart
		for left < lineEnd && (source[left] == ' ' || source[left] == '\t') {
			left++
		}
		trimmed := source[left:lineEnd]
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			for i := lineStart; i < lineEnd; i++ {
				masked[i] = true
			}
		} else {
			prefixEnd := left
			switch {
			case strings.HasPrefix(trimmed, "> "):
				prefixEnd = left + 2
			case len(trimmed) >= 2 && (trimmed[0] == '#' || trimmed[0] == '-' || trimmed[0] == '+' || trimmed[0] == '*') && (trimmed[1] == ' ' || trimmed[1] == '\t'):
				prefixEnd = left + 2
			default:
				j := 0
				for j < len(trimmed) && trimmed[j] >= '0' && trimmed[j] <= '9' {
					j++
				}
				if j > 0 && j+1 < len(trimmed) && (trimmed[j] == '.' || trimmed[j] == ')') && (trimmed[j+1] == ' ' || trimmed[j+1] == '\t') {
					prefixEnd = left + j + 2
				}
			}
			for i := left; i < prefixEnd && i < lineEnd; i++ {
				masked[i] = true
			}
		}
		if lineEnd == len(source) {
			break
		}
		lineStart = lineEnd + 1
	}
}

func maskLinksAndTags(source string, masked []bool) {
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case '\\':
			if i+1 < len(source) && strings.ContainsRune(`\\`+"`*{}[]()#+-.!_>", rune(source[i+1])) {
				masked[i] = true
			}
		case '!':
			if i+1 < len(source) && source[i+1] == '[' {
				masked[i] = true
			}
		case '[':
			masked[i] = true
		case ']':
			masked[i] = true
			if i+1 < len(source) && source[i+1] == '(' {
				end := strings.IndexByte(source[i+2:], ')')
				if end >= 0 {
					for j := i + 1; j <= i+2+end; j++ {
						masked[j] = true
					}
				}
			}
		case '<':
			if i+1 < len(source) && (unicode.IsLetter(rune(source[i+1])) || source[i+1] == '/' || source[i+1] == '!') {
				if end := strings.IndexByte(source[i:], '>'); end >= 0 {
					for j := i; j <= i+end; j++ {
						masked[j] = true
					}
				}
			}
		}
	}
}

func maskEmphasis(source string, masked []bool) {
	for _, marker := range []byte{'*', '_', '~'} {
		for i := 0; i < len(source); {
			if source[i] != marker {
				i++
				continue
			}
			start := i
			for i < len(source) && source[i] == marker {
				i++
			}
			run := i - start
			if start > 0 && source[start-1] == '\\' {
				continue
			}
			for j := i; j < len(source); {
				if source[j] != marker {
					j++
					continue
				}
				end := j
				for end < len(source) && source[end] == marker {
					end++
				}
				if end-j >= run && strings.TrimSpace(source[i:j]) != "" {
					for k := start; k < i; k++ {
						masked[k] = true
					}
					for k := j; k < j+run; k++ {
						masked[k] = true
					}
					break
				}
				j = end
			}
		}
	}
}
