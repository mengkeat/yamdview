// Package agentapi provides the long-running multi-session HTTP API used by
// agents (yamdview serve --api) and, in-process, by the MCP server.
//
// A Manager owns any number of streaming review sessions. Each session gets
// its own viewer page mounted under /sessions/<id>/ on the shared API server.
// The HTTP layer adds bearer-token-authenticated /api/v1 endpoints for
// creating sessions, streaming Markdown, long-polling feedback, and deleting
// sessions.
package agentapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/yuin/goldmark"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/markdown"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/session"
	"github.com/mengkeat/yamdview/web"
)

// Stream states reported by Append and Complete results.
const (
	StateStreaming = "streaming"
	StateComplete  = "complete"
)

// Errors returned by Manager operations.
var (
	// ErrSessionNotFound reports an unknown (or deleted) session ID.
	ErrSessionNotFound = errors.New("session not found")
)

// CreateOptions configures one new review session.
type CreateOptions struct {
	Title   string
	Prompt  string
	Choices []string
}

// SessionInfo identifies one created session: its API ID and the viewer URL
// a human can open in a browser.
type SessionInfo struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// AppendResult reports the outcome of an Append or ReplaceDocument call.
type AppendResult struct {
	// State is the stream state after the call: "streaming" while the agent
	// may keep appending, "complete" once MarkComplete has unlocked it.
	State string `json:"state"`
	// OpsApplied is the number of block-level patch operations produced by
	// the diff (0 when the diff fell back to a full reset).
	OpsApplied int `json:"ops_applied"`
	// Reset reports whether the change was broadcast as a full-document
	// reset instead of incremental patches.
	Reset bool `json:"reset"`
}

// managedSession couples one review session with its private viewer server
// and the document state needed for incremental diffing.
type managedSession struct {
	sess *session.Session
	srv  *server.Server

	// mu guards source and snapshot, which track the document as appended.
	// The session object itself is concurrency-safe.
	mu       sync.Mutex
	source   []byte
	snapshot document.DocumentSnapshot
}

// markComplete unlocks annotations for a streaming session and notifies
// connected viewer pages via the "session" SSE event. It is idempotent.
func (ms *managedSession) markComplete() {
	if !ms.sess.IsStreaming() {
		return
	}
	ms.sess.MarkComplete()
	_ = ms.srv.BroadcastSessionState(string(ms.sess.CurrentState()), false)
}

// Manager owns a set of review sessions. It is usable directly (the MCP
// server drives it in-process) and from the HTTP API handlers.
type Manager struct {
	assets  server.Assets
	baseURL string // scheme + host of the shared listener, no trailing slash
	md      goldmark.Markdown

	mu       sync.RWMutex
	sessions map[string]*managedSession
}

// NewManager creates an empty Manager. baseURL is the scheme and host of the
// listener the viewer pages are served from (e.g. "http://127.0.0.1:8080");
// per-session URLs are derived from it.
func NewManager(assets server.Assets, baseURL string) *Manager {
	return &Manager{
		assets:   assets,
		baseURL:  baseURL,
		md:       markdown.NewRenderer(),
		sessions: make(map[string]*managedSession),
	}
}

// Create builds a new streaming session from the initial Markdown. The
// session starts with annotations locked; call Append with complete=true or
// Complete to unlock them.
func (m *Manager) Create(markdown []byte, opts CreateOptions) (SessionInfo, error) {
	snapshot, err := document.BuildSnapshot(m.md, markdown, document.DocumentSnapshot{})
	if err != nil {
		return SessionInfo{}, fmt.Errorf("render markdown: %w", err)
	}
	id, err := newSessionID()
	if err != nil {
		return SessionInfo{}, fmt.Errorf("generate session id: %w", err)
	}
	sess, err := session.NewStreaming(id, opts.Title, opts.Prompt, opts.Choices, markdown, snapshot)
	if err != nil {
		return SessionInfo{}, fmt.Errorf("create session: %w", err)
	}
	srv, err := server.NewHandler(m.assets,
		server.PageDataFromAssets(m.assets, opts.Title, template.HTML(snapshot.HTML)),
		server.WithSession(sess),
		server.WithKatexFS(web.KatexFS()))
	if err != nil {
		return SessionInfo{}, fmt.Errorf("create viewer: %w", err)
	}

	m.mu.Lock()
	m.sessions[id] = &managedSession{
		sess:     sess,
		srv:      srv,
		source:   append([]byte(nil), markdown...),
		snapshot: document.CloneSnapshot(snapshot),
	}
	m.mu.Unlock()

	return SessionInfo{ID: id, URL: m.baseURL + "/sessions/" + id + "/"}, nil
}

// Append appends chunk to the session's Markdown source and broadcasts the
// incremental diff to connected viewers. With complete=true the stream is
// also marked complete, unlocking annotation mutations.
func (m *Manager) Append(id string, chunk []byte, complete bool) (AppendResult, error) {
	ms, ok := m.lookup(id)
	if !ok {
		return AppendResult{}, ErrSessionNotFound
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	next := make([]byte, 0, len(ms.source)+len(chunk))
	next = append(next, ms.source...)
	next = append(next, chunk...)
	return m.applySource(ms, next, complete)
}

// ReplaceDocument swaps the session's Markdown for a new document and
// broadcasts the diff, like Append but without preserving the old source.
// Revision semantics arrive in Phase 16; the snapshot is simply replaced.
func (m *Manager) ReplaceDocument(id string, markdown []byte) (AppendResult, error) {
	ms, ok := m.lookup(id)
	if !ok {
		return AppendResult{}, ErrSessionNotFound
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	return m.applySource(ms, append([]byte(nil), markdown...), false)
}

// applySource re-renders next against the tracked snapshot, applies the diff
// to the session, and broadcasts it. Callers must hold ms.mu.
func (m *Manager) applySource(ms *managedSession, next []byte, complete bool) (AppendResult, error) {
	snapshot, err := document.BuildSnapshot(m.md, next, ms.snapshot)
	if err != nil {
		return AppendResult{}, fmt.Errorf("render markdown: %w", err)
	}
	diff := document.Diff(ms.snapshot, snapshot)

	if err := ms.sess.UpdateSnapshot(next, diff.Snapshot); err != nil {
		return AppendResult{}, fmt.Errorf("update session document: %w", err)
	}
	ms.source = next
	ms.snapshot = diff.Snapshot

	content := template.HTML(diff.Snapshot.HTML)
	if diff.Reset {
		if err := ms.srv.BroadcastReset(content); err != nil {
			return AppendResult{}, fmt.Errorf("broadcast reset: %w", err)
		}
	} else if err := ms.srv.BroadcastPatches(content, diff.Ops); err != nil {
		return AppendResult{}, fmt.Errorf("broadcast patches: %w", err)
	}

	if complete {
		ms.markComplete()
	}

	result := AppendResult{OpsApplied: len(diff.Ops), Reset: diff.Reset, State: StateStreaming}
	if !ms.sess.IsStreaming() {
		result.State = StateComplete
	}
	return result, nil
}

// Complete marks a streaming session's document finished, unlocking
// annotation mutations and submissions. It is idempotent and safe on any
// session state.
func (m *Manager) Complete(id string) error {
	ms, ok := m.lookup(id)
	if !ok {
		return ErrSessionNotFound
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.markComplete()
	return nil
}

// Feedback builds the versioned feedback payload for a session. It does not
// wait; use WaitFeedback to block for a terminal state first.
func (m *Manager) Feedback(id string) (feedback.Payload, error) {
	ms, ok := m.lookup(id)
	if !ok {
		return feedback.Payload{}, ErrSessionNotFound
	}
	return ms.sess.FeedbackPayload(), nil
}

// WaitFeedback blocks until the session reaches a terminal state, ctx is
// done, or the session is deleted. It returns nil once the session is
// terminal and Feedback will return the final payload.
func (m *Manager) WaitFeedback(ctx context.Context, id string) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	ms, ok := m.lookup(id)
	if !ok {
		return ErrSessionNotFound
	}

	select {
	case <-ms.sess.Done():
	case <-ctx.Done():
		return ctx.Err()
	}

	// The session may have been deleted (and cancelled) while waiting.
	if _, ok := m.lookup(id); !ok {
		return ErrSessionNotFound
	}
	return nil
}

// Delete removes a session, disconnects its viewers, and wakes anyone
// blocked in WaitFeedback for it (the session is cancelled first so its
// Done channel closes).
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	ms, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}

	if err := ms.sess.Cancel(); err != nil && !errors.Is(err, session.ErrInvalidTransition) {
		return fmt.Errorf("cancel session: %w", err)
	}
	return ms.srv.Close()
}

// Get returns the underlying review session.
func (m *Manager) Get(id string) (*session.Session, bool) {
	ms, ok := m.lookup(id)
	if !ok {
		return nil, false
	}
	return ms.sess, true
}

// ViewerHandler returns the per-session viewer HTTP handler that the API
// server mounts under /sessions/<id>/.
func (m *Manager) ViewerHandler(id string) (http.Handler, bool) {
	ms, ok := m.lookup(id)
	if !ok {
		return nil, false
	}
	return ms.srv.Handler(), true
}

func (m *Manager) lookup(id string) (*managedSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ms, ok := m.sessions[id]
	return ms, ok
}

// IDs returns a snapshot of the live session IDs. It is used internally for
// shutdown cleanup and by in-process integrations (and their tests) that
// need to discover sessions created through the Manager.
func (m *Manager) IDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

// newSessionID mints an app-style session ID: s-<unixnano>-<hex>.
func newSessionID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("s-%d-%s", time.Now().UTC().UnixNano(), hex.EncodeToString(random[:])), nil
}
