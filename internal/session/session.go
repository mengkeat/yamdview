// Package session models the lifecycle of a document review session.
package session

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

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
// transitions are concurrency-safe; use CurrentState when reading State from
// concurrent code.
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

	mu   sync.RWMutex
	done chan struct{}
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

func randomToken() (string, error) {
	var token [tokenBytes]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}
