// Package session models the lifecycle of a document review session.
package session

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mengkeat/yamdview/internal/annotation"
	"github.com/mengkeat/yamdview/internal/document"
)

// State is the lifecycle state of a review session.
type State string

const (
	Open      State = "open"
	Submitted State = "submitted"
	Timeout   State = "timeout"
	Cancelled State = "cancelled"

	// State-prefixed aliases make the allowed values discoverable alongside the
	// State type while retaining the concise names above.
	StateOpen      = Open
	StateSubmitted = Submitted
	StateTimeout   = Timeout
	StateCancelled = Cancelled
)

// ErrInvalidTransition is returned when an already-terminal session is
// submitted, timed out, or cancelled.
var ErrInvalidTransition = errors.New("invalid session transition")

// Errors returned by annotation operations.
var (
	ErrInvalidAnnotation       = errors.New("invalid annotation")
	ErrAnnotationNotFound      = errors.New("annotation not found")
	ErrAnnotationExists        = errors.New("annotation already exists")
	ErrTerminalSessionMutation = errors.New("terminal session cannot be mutated")
)

const tokenBytes = 32

// Metadata is the safe, mutable state of a review session. It intentionally
// excludes the session token and frozen document contents.
type Metadata struct {
	ID          string
	Title       string
	Prompt      string
	Choices     []string
	State       State
	Verdict     string
	Summary     string
	Revision    int
	OpenedAt    time.Time
	SubmittedAt time.Time
}

// Session is one frozen document review and its lifecycle state.
//
// Source, Choices, and Snapshot are copied when the session is created, so
// later changes to the constructor's inputs do not change the review. State
// transitions and annotation operations are concurrency-safe; use CurrentState
// when reading State from concurrent code.
type Session struct {
	ID       string
	Title    string
	Prompt   string
	Choices  []string
	Source   []byte
	Snapshot document.DocumentSnapshot

	State    State
	Verdict  string
	Summary  string
	Revision int
	Token    string

	OpenedAt    time.Time
	SubmittedAt time.Time

	mu          sync.RWMutex
	annotations []annotation.Annotation
	done        chan struct{}
}

// Metadata returns a concurrency-safe copy of the session state suitable for
// displaying or serialising to a client. It never includes the session token.
func (s *Session) Metadata() Metadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Metadata{
		ID:          s.ID,
		Title:       s.Title,
		Prompt:      s.Prompt,
		Choices:     append([]string(nil), s.Choices...),
		State:       s.State,
		Verdict:     s.Verdict,
		Summary:     s.Summary,
		Revision:    s.Revision,
		OpenedAt:    s.OpenedAt,
		SubmittedAt: s.SubmittedAt,
	}
}

// TokenMatches reports whether token is this session's token. Comparison is
// constant-time so callers can safely use it for HTTP authentication.
func (s *Session) TokenMatches(token string) bool {
	s.mu.RLock()
	expected := s.Token
	s.mu.RUnlock()
	return subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1
}

// New creates an open review session with a cryptographically random token.
// The supplied source, choices, and document snapshot are copied.
func New(id, title, prompt string, choices []string, source []byte, snapshot document.DocumentSnapshot) (*Session, error) {
	token, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}

	return &Session{
		ID:       id,
		Title:    title,
		Prompt:   prompt,
		Choices:  append([]string(nil), choices...),
		Source:   append([]byte(nil), source...),
		Snapshot: document.CloneSnapshot(snapshot),
		State:    Open,
		Token:    token,
		OpenedAt: time.Now().UTC(),
		done:     make(chan struct{}),
	}, nil
}

// NewSession is an explicit alias for New.
func NewSession(id, title, prompt string, choices []string, source []byte, snapshot document.DocumentSnapshot) (*Session, error) {
	return New(id, title, prompt, choices, source, snapshot)
}

// NewWithAnnotations creates a session and adds the supplied annotations using
// the same validation, anchoring, and copy rules as CreateAnnotation.
func NewWithAnnotations(id, title, prompt string, choices []string, source []byte, snapshot document.DocumentSnapshot, annotations []annotation.Annotation) (*Session, error) {
	s, err := New(id, title, prompt, choices, source, snapshot)
	if err != nil {
		return nil, err
	}
	for _, item := range annotations {
		if _, err := s.CreateAnnotation(item); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// AnnotationSnapshot returns a deep copy of all annotations in insertion
// order. The returned slice and its SourceSpan values are independent of the
// session and may be safely modified by the caller.
func (s *Session) AnnotationSnapshot() []annotation.Annotation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneAnnotations(s.annotations)
}

// AnnotationsSnapshot is an explicit alias for AnnotationSnapshot.
func (s *Session) AnnotationsSnapshot() []annotation.Annotation {
	return s.AnnotationSnapshot()
}

// SnapshotAnnotations is an explicit alias for AnnotationSnapshot.
func (s *Session) SnapshotAnnotations() []annotation.Annotation {
	return s.AnnotationSnapshot()
}

// ListAnnotations returns all annotations in insertion order.
func (s *Session) ListAnnotations() []annotation.Annotation {
	return s.AnnotationSnapshot()
}

// Annotations returns all annotations in insertion order. It is retained as a
// concise accessor for handlers and callers that model annotations as a list.
func (s *Session) Annotations() []annotation.Annotation {
	return s.AnnotationSnapshot()
}

// GetAnnotation returns a copy of one annotation.
func (s *Session) GetAnnotation(id string) (annotation.Annotation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.annotations {
		if item.ID == id {
			return cloneAnnotation(item), nil
		}
	}
	return annotation.Annotation{}, fmt.Errorf("%w: %s", ErrAnnotationNotFound, id)
}

// FindAnnotation returns a copy of one annotation and whether it exists.
func (s *Session) FindAnnotation(id string) (annotation.Annotation, bool) {
	item, err := s.GetAnnotation(id)
	return item, err == nil
}

// CreateAnnotation validates and stores an annotation, resolving its quote
// against the current document snapshot. IDs and missing timestamps are
// assigned by the server. The returned annotation is a copy.
func (s *Session) CreateAnnotation(input annotation.Annotation) (annotation.Annotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureOpen(); err != nil {
		return annotation.Annotation{}, err
	}
	if err := validateAnnotation(input); err != nil {
		return annotation.Annotation{}, err
	}

	item := cloneAnnotation(input)
	if item.ID == "" {
		id, err := randomToken()
		if err != nil {
			return annotation.Annotation{}, fmt.Errorf("generate annotation ID: %w", err)
		}
		item.ID = "annotation-" + id[:16]
	}
	for _, existing := range s.annotations {
		if existing.ID == item.ID {
			return annotation.Annotation{}, fmt.Errorf("%w: %s", ErrAnnotationExists, item.ID)
		}
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	item = resolveAnnotation(item, s.Snapshot)
	s.annotations = append(s.annotations, item)
	return cloneAnnotation(item), nil
}

// UpdateAnnotation applies a partial update to an existing annotation. Empty
// anchor fields retain their current values, allowing PATCH handlers to update
// only the comment. The anchor is resolved again against the current snapshot.
func (s *Session) UpdateAnnotation(id string, input annotation.Annotation) (annotation.Annotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureOpen(); err != nil {
		return annotation.Annotation{}, err
	}
	index := -1
	for i := range s.annotations {
		if s.annotations[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return annotation.Annotation{}, fmt.Errorf("%w: %s", ErrAnnotationNotFound, id)
	}

	current := s.annotations[index]
	item := mergeAnnotationUpdate(id, current, input)
	if err := validateAnnotation(item); err != nil {
		return annotation.Annotation{}, err
	}
	item.CreatedAt = current.CreatedAt
	item.UpdatedAt = time.Now().UTC()
	item = resolveAnnotation(item, s.Snapshot)
	s.annotations[index] = item
	return cloneAnnotation(item), nil
}

// UpdateAnnotationRecord updates an annotation using its ID field. It is a
// convenience for callers that already hold a complete record.
func (s *Session) UpdateAnnotationRecord(input annotation.Annotation) (annotation.Annotation, error) {
	if input.ID == "" {
		return annotation.Annotation{}, fmt.Errorf("%w: annotation id is required", ErrInvalidAnnotation)
	}
	return s.UpdateAnnotation(input.ID, input)
}

// DeleteAnnotation removes an annotation by ID.
func (s *Session) DeleteAnnotation(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureOpen(); err != nil {
		return err
	}
	for i := range s.annotations {
		if s.annotations[i].ID == id {
			copy(s.annotations[i:], s.annotations[i+1:])
			s.annotations = s.annotations[:len(s.annotations)-1]
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrAnnotationNotFound, id)
}

// UpdateSnapshot replaces the document source and snapshot, then re-anchors
// every annotation. Annotations that cannot be resolved are retained with an
// outdated status rather than being discarded.
func (s *Session) UpdateSnapshot(source []byte, snapshot document.DocumentSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureOpen(); err != nil {
		return err
	}
	s.Source = append([]byte(nil), source...)
	s.Snapshot = document.CloneSnapshot(snapshot)
	s.annotations = cloneAnnotations(annotation.ReanchorAll(s.annotations, s.Snapshot))
	return nil
}

// UpdateDocument is an explicit alias for UpdateSnapshot.
func (s *Session) UpdateDocument(source []byte, snapshot document.DocumentSnapshot) error {
	return s.UpdateSnapshot(source, snapshot)
}

// Submit records the review and moves the session to submitted.
func (s *Session) Submit(verdict, summary string) error {
	return s.finish(Submitted, verdict, summary)
}

// Timeout marks an open session as timed out.
func (s *Session) Timeout() error {
	return s.finish(Timeout, "timeout", "")
}

// Cancel marks an open session as cancelled.
func (s *Session) Cancel() error {
	return s.finish(Cancelled, "cancelled", "")
}

// CurrentState returns the session's state safely for concurrent callers.
func (s *Session) CurrentState() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.State
}

// Done returns a channel that is closed once the session reaches a terminal
// state. It is safe to select on this channel from multiple goroutines.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// Wait blocks until the session reaches a terminal state or ctx is cancelled.
// Context cancellation does not change the session state; call Cancel when the
// cancellation should end the review itself.
func (s *Session) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}

	select {
	case <-s.done:
		return nil
	default:
	}

	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) finish(next State, verdict, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.State != Open {
		return fmt.Errorf("%w: session is %s", ErrInvalidTransition, s.State)
	}

	s.State = next
	s.Verdict = verdict
	s.Summary = summary
	s.SubmittedAt = time.Now().UTC()
	close(s.done)
	return nil
}

func (s *Session) ensureOpen() error {
	if s.State != Open {
		return fmt.Errorf("%w: session is %s", ErrTerminalSessionMutation, s.State)
	}
	return nil
}

func validateAnnotation(item annotation.Annotation) error {
	if err := item.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAnnotation, err)
	}
	if item.Kind == annotation.KindSuggestion && strings.TrimSpace(item.SuggestedReplacement) == "" {
		return fmt.Errorf("%w: suggestion suggested_replacement is required", ErrInvalidAnnotation)
	}
	if item.Kind != annotation.KindSuggestion && strings.TrimSpace(item.SuggestedReplacement) != "" {
		return fmt.Errorf("%w: suggested_replacement is only valid for suggestions", ErrInvalidAnnotation)
	}
	return nil
}

func mergeAnnotationUpdate(id string, current, input annotation.Annotation) annotation.Annotation {
	item := cloneAnnotation(input)
	item.ID = id
	if item.Kind == "" {
		item.Kind = current.Kind
	}
	if item.BlockID == "" {
		item.BlockID = current.BlockID
	}
	if item.Quote == "" {
		item.Quote = current.Quote
	}
	if item.Prefix == "" {
		item.Prefix = current.Prefix
	}
	if item.Suffix == "" {
		item.Suffix = current.Suffix
	}
	if item.Comment == "" {
		item.Comment = current.Comment
	}
	if item.SuggestedReplacement == "" && item.Kind == current.Kind {
		item.SuggestedReplacement = current.SuggestedReplacement
	}
	if item.GroupID == "" {
		item.GroupID = current.GroupID
	}
	if item.StartLine == 0 {
		item.StartLine = current.StartLine
	}
	if item.EndLine == 0 {
		item.EndLine = current.EndLine
	}
	return item
}

func resolveAnnotation(item annotation.Annotation, snapshot document.DocumentSnapshot) annotation.Annotation {
	item.SourceSpan = annotation.Resolve(snapshot, item)
	if item.SourceSpan == nil {
		item.Status = annotation.StatusOutdated
		return item
	}
	item.Status = annotation.StatusActive
	for _, block := range snapshot.Blocks {
		if block.ID == item.BlockID {
			item.StartLine = block.StartLine
			item.EndLine = block.EndLine
			break
		}
	}
	return item
}

func cloneAnnotation(item annotation.Annotation) annotation.Annotation {
	clone := item
	if item.SourceSpan != nil {
		span := *item.SourceSpan
		clone.SourceSpan = &span
	}
	return clone
}

func cloneAnnotations(items []annotation.Annotation) []annotation.Annotation {
	if items == nil {
		return []annotation.Annotation{}
	}
	clones := make([]annotation.Annotation, len(items))
	for i, item := range items {
		clones[i] = cloneAnnotation(item)
	}
	return clones
}

func randomToken() (string, error) {
	var token [tokenBytes]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}
