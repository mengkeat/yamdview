package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/mcp"
	"github.com/mengkeat/yamdview/web"
)

// wireError is one decoded JSON-RPC error object.
type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// wireResponse is one decoded stdout line.
type wireResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *wireError      `json:"error"`
}

// harness drives one MCP server over io.Pipe pairs with strict
// request/response alternation.
type harness struct {
	srv  *mcp.Server
	inW  io.WriteCloser
	out  *bufio.Reader
	done <-chan error
}

// startMCP boots a server on a random loopback port and consumes its stdout
// for the duration of the test.
func startMCP(t *testing.T) *harness {
	t.Helper()
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv, err := mcp.New(assets, "127.0.0.1:0", inR, outW)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	h := &harness{srv: srv, inW: inW, out: bufio.NewReader(outR), done: done}
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after cancellation")
		}
		_ = outR.Close()
		_ = outW.Close()
	})
	return h
}

// send writes one protocol line.
func (h *harness) send(t *testing.T, line string) {
	t.Helper()
	if _, err := fmt.Fprint(h.inW, line+"\n"); err != nil {
		t.Fatalf("send %s: %v", line, err)
	}
}

// read reads and decodes one response line.
func (h *harness) read(t *testing.T) wireResponse {
	t.Helper()
	line, err := h.out.ReadString('\n')
	if err != nil {
		t.Fatalf("read response line: %v", err)
	}
	var resp wireResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("response jsonrpc = %q, want 2.0 (%s)", resp.JSONRPC, line)
	}
	return resp
}

// call sends one request line and returns its response.
func (h *harness) call(t *testing.T, line string) wireResponse {
	t.Helper()
	h.send(t, line)
	return h.read(t)
}

// requestLine builds a bare request with no params.
func requestLine(t *testing.T, id any, method string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

// toolCallLine builds a tools/call request; arguments must be a JSON object
// or null.
func toolCallLine(t *testing.T, id any, tool string, arguments map[string]any) string {
	t.Helper()
	args, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": tool, "arguments": json.RawMessage(args)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

// assertID checks that a response echoes the request id verbatim.
func assertID(t *testing.T, resp wireResponse, want string) {
	t.Helper()
	if got := strings.TrimSpace(string(resp.ID)); got != want {
		t.Fatalf("response id = %s, want %s", got, want)
	}
}

// unmarshalResult decodes a success result into target.
func unmarshalResult(t *testing.T, resp wireResponse, target any) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if err := json.Unmarshal(resp.Result, target); err != nil {
		t.Fatalf("decode result %s: %v", resp.Result, err)
	}
}

// toolText decodes a successful text tool result and unmarshals its text
// into the target type.
func toolText[T any](t *testing.T, resp wireResponse) T {
	t.Helper()
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	unmarshalResult(t, resp, &res)
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("tool result content = %+v, want one text block", res.Content)
	}
	if res.IsError {
		t.Fatalf("tool result unexpectedly isError: %s", res.Content[0].Text)
	}
	var v T
	if err := json.Unmarshal([]byte(res.Content[0].Text), &v); err != nil {
		t.Fatalf("decode tool text %q: %v", res.Content[0].Text, err)
	}
	return v
}

// toolErrorText asserts a tool result is isError and returns its text.
func toolErrorText(t *testing.T, resp wireResponse) string {
	t.Helper()
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	unmarshalResult(t, resp, &res)
	if !res.IsError {
		t.Fatalf("tool result unexpectedly succeeded: %+v", res)
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("tool error content = %+v, want one text block", res.Content)
	}
	return res.Content[0].Text
}

// presentEcho mirrors the compact JSON returned by present_markdown.
type presentEcho struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	State     string `json:"state"`
}

// TestGoldenTranscript walks a scripted MCP session over stdio in order:
// initialize, a notification that must produce no output, tools/list, a
// present_markdown call, a scripted human submission collected via
// await_feedback, ping, an unknown method, and a malformed line.
func TestGoldenTranscript(t *testing.T) {
	h := startMCP(t)
	checkServerAccessors(t, h)
	checkInitialize(t, h)
	checkNotificationIsSilent(t, h)
	checkToolsList(t, h)
	sessionID := checkPresentMarkdown(t, h)
	checkAwaitFeedbackAfterSubmission(t, h, sessionID)
	checkTrailingProtocolErrors(t, h)
}

// checkServerAccessors asserts the loopback API surface the server exposes
// for stderr logging.
func checkServerAccessors(t *testing.T, h *harness) {
	t.Helper()
	if h.srv.Token() == "" {
		t.Fatal("server token is empty")
	}
	if !strings.HasPrefix(h.srv.URL(), "http://127.0.0.1:") {
		t.Fatalf("server URL is not loopback: %q", h.srv.URL())
	}
}

// checkInitialize asserts the initialize result contract.
func checkInitialize(t *testing.T, h *harness) {
	t.Helper()
	resp := h.call(t, requestLine(t, 1, "initialize"))
	assertID(t, resp, "1")
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	unmarshalResult(t, resp, &init)
	if init.ProtocolVersion != mcp.ProtocolVersion {
		t.Fatalf("protocolVersion = %q, want %q", init.ProtocolVersion, mcp.ProtocolVersion)
	}
	if init.Capabilities.Tools.ListChanged {
		t.Fatal("tools.listChanged = true, want false")
	}
	if init.ServerInfo.Name != "yamdview" || init.ServerInfo.Version == "" {
		t.Fatalf("serverInfo = %+v, want yamdview with a version", init.ServerInfo)
	}
}

// checkNotificationIsSilent proves notifications/initialized produces no
// output: the next response belongs to the ping that follows it.
func checkNotificationIsSilent(t *testing.T, h *harness) {
	t.Helper()
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp := h.call(t, requestLine(t, 2, "ping"))
	assertID(t, resp, "2")
	if got := strings.TrimSpace(string(resp.Result)); got != "{}" {
		t.Fatalf("ping result = %s, want {}", got)
	}
}

// checkToolsList asserts the advertised tool set and schemas.
func checkToolsList(t *testing.T, h *harness) {
	t.Helper()
	resp := h.call(t, requestLine(t, 3, "tools/list"))
	assertID(t, resp, "3")
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type     string   `json:"type"`
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	unmarshalResult(t, resp, &list)
	wantNames := []string{"present_markdown", "await_feedback", "request_review"}
	if len(list.Tools) != len(wantNames) {
		t.Fatalf("tools/list has %d tools, want %d", len(list.Tools), len(wantNames))
	}
	for i, want := range wantNames {
		got := list.Tools[i]
		if got.Name != want {
			t.Fatalf("tool %d name = %q, want %q", i, got.Name, want)
		}
		if got.Description == "" {
			t.Fatalf("tool %q has empty description", got.Name)
		}
		if got.InputSchema.Type != "object" {
			t.Fatalf("tool %q inputSchema.type = %q, want object", got.Name, got.InputSchema.Type)
		}
	}
	if req := list.Tools[1].InputSchema.Required; len(req) != 1 || req[0] != "session_id" {
		t.Fatalf("await_feedback required = %v, want [session_id]", req)
	}
}

// checkPresentMarkdown presents inline markdown (staying streaming) and
// returns the session id.
func checkPresentMarkdown(t *testing.T, h *harness) string {
	t.Helper()
	resp := h.call(t, toolCallLine(t, 4, "present_markdown", map[string]any{
		"markdown": "# Title\n\nHello world.",
		"title":    "Plan",
		"prompt":   "Please review",
		"choices":  []string{"approve", "request_changes", "comment"},
		"open":     false,
	}))
	assertID(t, resp, "4")
	present := toolText[presentEcho](t, resp)
	if !strings.HasPrefix(present.SessionID, "s-") {
		t.Fatalf("session id = %q, want s- prefix", present.SessionID)
	}
	if want := h.srv.URL() + "/sessions/" + present.SessionID + "/"; present.URL != want {
		t.Fatalf("session url = %q, want %q", present.URL, want)
	}
	if present.State != "streaming" {
		t.Fatalf("state = %q, want streaming", present.State)
	}
	return present.SessionID
}

// checkAwaitFeedbackAfterSubmission scripts the human side (complete the
// stream, submit a verdict) and asserts await_feedback returns the payload.
func checkAwaitFeedbackAfterSubmission(t *testing.T, h *harness, sessionID string) {
	t.Helper()
	mgr := h.srv.Manager()
	if err := mgr.Complete(sessionID); err != nil {
		t.Fatalf("complete session: %v", err)
	}
	sess, ok := mgr.Get(sessionID)
	if !ok {
		t.Fatal("session missing from manager")
	}
	if err := sess.Submit("approve", "ship it"); err != nil {
		t.Fatalf("submit session: %v", err)
	}

	resp := h.call(t, toolCallLine(t, 5, "await_feedback", map[string]any{
		"session_id": sessionID,
	}))
	assertID(t, resp, "5")
	payload := toolText[feedback.Payload](t, resp)
	if payload.Version != feedback.CurrentVersion {
		t.Fatalf("payload version = %d, want %d", payload.Version, feedback.CurrentVersion)
	}
	if payload.SessionID != sessionID {
		t.Fatalf("payload session id = %q, want %q", payload.SessionID, sessionID)
	}
	if payload.Verdict != "approve" || payload.Summary != "ship it" {
		t.Fatalf("payload verdict/summary = %q/%q", payload.Verdict, payload.Summary)
	}
}

// checkTrailingProtocolErrors covers ping after tool traffic, an unknown
// method, and a malformed line.
func checkTrailingProtocolErrors(t *testing.T, h *harness) {
	t.Helper()
	resp := h.call(t, requestLine(t, 6, "ping"))
	assertID(t, resp, "6")

	resp = h.call(t, requestLine(t, 7, "resources/list"))
	assertID(t, resp, "7")
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("unknown method error = %+v, want -32601", resp.Error)
	}

	h.send(t, `{"jsonrpc":`)
	resp = h.read(t)
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("malformed line error = %+v, want -32700", resp.Error)
	}
	if got := strings.TrimSpace(string(resp.ID)); got != "null" {
		t.Fatalf("parse error id = %s, want null", got)
	}
}

// TestServeReturnsAtStdinEOF checks that EOF on the protocol input is a
// clean shutdown.
func TestServeReturnsAtStdinEOF(t *testing.T) {
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	out := &strings.Builder{}
	srv, err := mcp.New(assets, "127.0.0.1:0", inR, out)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background()) }()
	if err := inW.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v at EOF, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return at stdin EOF")
	}
}

// TestServeReturnsOnContextCancel checks that cancelling the context
// unblocks Serve even while stdin would block forever.
func TestServeReturnsOnContextCancel(t *testing.T) {
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	defer inW.Close() // unblocks the reader goroutine after the test
	out := &strings.Builder{}
	srv, err := mcp.New(assets, "127.0.0.1:0", inR, out)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v on cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}
