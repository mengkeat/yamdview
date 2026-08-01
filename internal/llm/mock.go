package llm

import (
	"context"
	"sync"
	"time"
)

// MockResponse pairs a canned [Response] with an optional error, used to
// exercise validation paths without a live provider.
type MockResponse struct {
	Response Response
	Err      error
}

// MockText is a convenience constructor for a successful text response.
func MockText(text string) MockResponse {
	return MockResponse{Response: Response{Text: text, Finish: FinishStop}}
}

// Mock is an in-memory [Provider] for tests. It returns queued responses in
// FIFO order, falling back to a dynamic responder when the queue is empty.
// Every call is recorded so tests can assert on request contents.
type Mock struct {
	name  string
	mu    sync.Mutex
	queue []MockResponse
	fn    func(Request) (Response, error)
	delay time.Duration
	calls []Request
}

// NewMock creates a mock provider with the given name.
func NewMock(name string) *Mock {
	return &Mock{name: name}
}

// Queue appends canned responses. Responses are returned in FIFO order; once
// the queue is exhausted the last response repeats so a single canned reply
// can satisfy an unbounded number of calls.
func (m *Mock) Queue(responses ...MockResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = append(m.queue, responses...)
}

// SetFunc installs a dynamic responder used whenever the queue is empty.
func (m *Mock) SetFunc(fn func(Request) (Response, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fn = fn
}

// SetDelay pauses for d before responding, so tests can exercise context
// cancellation and deadlines against a slow provider.
func (m *Mock) SetDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = d
}

// Calls returns a copy of the recorded requests in call order.
func (m *Mock) Calls() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Request, len(m.calls))
	copy(out, m.calls)
	return out
}

// Name implements [Provider].
func (m *Mock) Name() string { return m.name }

// Complete implements [Provider]. It honors the context: if the context is
// cancelled during the configured delay it returns the context error.
func (m *Mock) Complete(ctx context.Context, req Request) (Response, error) {
	m.mu.Lock()
	delay := m.delay
	m.calls = append(m.calls, req)

	var selected MockResponse
	switch {
	case len(m.queue) > 0:
		selected = m.queue[0]
		if len(m.queue) > 1 {
			m.queue = m.queue[1:]
		}
	case m.fn != nil:
		// Release the lock while the dynamic responder runs so a slow function
		// does not block Calls().
		fn := m.fn
		m.mu.Unlock()
		return fn(req)
	default:
		selected = MockResponse{Response: Response{Finish: FinishStop}}
	}
	m.mu.Unlock()

	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-timer.C:
		}
	}
	return selected.Response, selected.Err
}
