package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mengkeat/yamdview/internal/agentapi"
	"github.com/mengkeat/yamdview/internal/browser"
	"github.com/mengkeat/yamdview/internal/feedback"
)

// Tool names exposed via tools/list and tools/call.
const (
	toolPresent       = "present_markdown"
	toolAwaitFeedback = "await_feedback"
	toolRequestReview = "request_review"
)

// maxMarkdownBytes caps documents accepted from inline content or a local
// file path, mirroring the agent API request body cap.
const maxMarkdownBytes = 10 << 20

// textContent is one MCP content block.
type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callToolResult is the MCP tools/call result shape: text content plus the
// isError flag distinguishing tool failures from protocol errors.
type callToolResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// toolDef is one tools/list entry.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// textResult wraps text as a successful tool result.
func textResult(text string) any {
	return callToolResult{Content: []textContent{{Type: "text", Text: text}}}
}

// errorResult wraps a formatted message as a failed tool result (MCP
// isError), which is distinct from a JSON-RPC protocol error.
func errorResult(format string, args ...any) any {
	return callToolResult{
		Content: []textContent{{Type: "text", Text: fmt.Sprintf(format, args...)}},
		IsError: true,
	}
}

// prop builds one JSON Schema property.
func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

// stringArrayProp builds a JSON Schema property for string arrays.
func stringArrayProp(description string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": description}
}

// presentProperties returns the shared present_markdown schema properties.
func presentProperties() map[string]any {
	return map[string]any{
		"markdown": prop("string", "Inline Markdown content (exactly one of markdown or path is required)"),
		"path":     prop("string", "Path to a local Markdown file (exactly one of markdown or path is required)"),
		"title":    prop("string", "Session title shown in the review viewer"),
		"prompt":   prop("string", "Question or request shown above the document"),
		"choices":  stringArrayProp("Quick verdict choices offered to the reviewer"),
		"complete": prop("boolean", "Mark the document complete immediately, unlocking annotations (default false)"),
		"open":     prop("boolean", "Open the viewer in the user's browser (default true)"),
	}
}

// reviewProperties returns the request_review schema properties: the
// present_markdown union minus complete (request_review always completes)
// plus a feedback wait timeout.
func reviewProperties() map[string]any {
	props := presentProperties()
	delete(props, "complete")
	props["timeout_seconds"] = prop("integer", "How long to wait for feedback; 0 (default) waits forever")
	return props
}

// toolDefinitions returns the tools/list entries in stable order.
func toolDefinitions() []toolDef {
	return []toolDef{
		{
			Name:        toolPresent,
			Description: "Present a Markdown document for human review in a browser session; returns {session_id, url, state} as JSON. The session starts in streaming mode with annotations locked; pass complete=true when the document is finished, or complete it later via await_feedback/request_review.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": presentProperties(),
			},
		},
		{
			Name:        toolAwaitFeedback,
			Description: "Wait for the human to submit feedback on a review session and return the versioned feedback payload as JSON. Optionally marks a still-streaming document complete first. With timeout_seconds=0 (default) it waits indefinitely; on timeout the session is still open, so call it again.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id":      prop("string", "Session ID returned by present_markdown"),
					"timeout_seconds": prop("integer", "How long to wait for feedback; 0 (default) waits forever"),
					"complete":        prop("boolean", "Mark a still-streaming document complete before waiting (default false)"),
				},
				"required": []string{"session_id"},
			},
		},
		{
			Name:        toolRequestReview,
			Description: "One-shot review convenience: present a Markdown document (always marked complete), open it in the browser, wait for the human's feedback, and return the versioned feedback payload as JSON.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": reviewProperties(),
			},
		},
	}
}

// presentParams is the argument object for present_markdown (and, embedded,
// request_review). Markdown is a pointer so an empty string still counts as
// provided; Open is a pointer so the default is true.
type presentParams struct {
	Markdown *string  `json:"markdown"`
	Path     string   `json:"path"`
	Title    string   `json:"title"`
	Prompt   string   `json:"prompt"`
	Choices  []string `json:"choices"`
	Complete bool     `json:"complete"`
	Open     *bool    `json:"open"`
}

// openBrowser reports whether the viewer should be opened in the user's
// browser; absent defaults to true.
func (p *presentParams) openBrowser() bool { return p.Open == nil || *p.Open }

// awaitParams is the argument object for await_feedback.
type awaitParams struct {
	SessionID      string `json:"session_id"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Complete       bool   `json:"complete"`
}

// reviewParams is the argument object for request_review: the
// present_markdown arguments plus a feedback wait timeout. The embedded
// Complete field is ignored; request_review always completes.
type reviewParams struct {
	presentParams
	TimeoutSeconds int `json:"timeout_seconds"`
}

// callTool validates a tools/call request and dispatches to the tool
// implementation. Protocol-level problems (unknown tool, invalid arguments)
// are JSON-RPC -32602 errors; tool-level failures are isError results.
func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(params) == 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: `tools/call requires params: {"name": ..., "arguments": {...}}`}
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&call); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("invalid tools/call params: %v", err)}
	}
	if call.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: `tools/call requires a tool "name"`}
	}

	var (
		result any
		rerr   *rpcError
	)
	switch call.Name {
	case toolPresent:
		result, rerr = s.toolPresent(call.Arguments)
	case toolAwaitFeedback:
		result, rerr = s.toolAwait(ctx, call.Arguments)
	case toolRequestReview:
		result, rerr = s.toolReview(ctx, call.Arguments)
	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", call.Name)}
	}
	if rerr != nil {
		return nil, rerr
	}
	return result, nil
}

// decodeArgs strictly decodes a tool arguments object; unknown fields and
// trailing values are rejected so agent typos surface instead of being
// silently ignored. Empty or null arguments are allowed; semantic validation
// happens in the tool.
func decodeArgs(args json.RawMessage, target any) *rpcError {
	if len(args) == 0 || string(bytes.TrimSpace(args)) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("invalid arguments: %v", err)}
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return &rpcError{Code: codeInvalidParams, Message: "arguments must be a single JSON object"}
	}
	return nil
}

// toolPresent implements present_markdown: create a streaming session from
// inline Markdown or a local file, optionally mark it complete, optionally
// open the browser, and report {session_id, url, state} as compact JSON.
func (s *Server) toolPresent(args json.RawMessage) (any, *rpcError) {
	var p presentParams
	if rerr := decodeArgs(args, &p); rerr != nil {
		return nil, rerr
	}
	md, err := readDocument(p.Markdown, p.Path)
	if err != nil {
		return errorResult("%v", err), nil
	}
	info, err := s.manager.Create(md, agentapi.CreateOptions{Title: p.Title, Prompt: p.Prompt, Choices: p.Choices})
	if err != nil {
		return errorResult("could not create session: %v", err), nil
	}
	state := agentapi.StateStreaming
	if p.Complete {
		if err := s.manager.Complete(info.ID); err != nil {
			return errorResult("could not complete session: %v", err), nil
		}
		state = agentapi.StateComplete
	}
	s.maybeOpenBrowser(info.URL, p.openBrowser())
	return textResult(compactJSON(presentResult{SessionID: info.ID, URL: info.URL, State: state})), nil
}

// toolAwait implements await_feedback: optionally complete the stream, then
// wait for the session to become terminal and return the feedback payload.
func (s *Server) toolAwait(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p awaitParams
	if rerr := decodeArgs(args, &p); rerr != nil {
		return nil, rerr
	}
	if p.SessionID == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: `await_feedback requires "session_id"`}
	}
	if p.TimeoutSeconds < 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: `"timeout_seconds" must be zero or positive`}
	}
	if p.Complete {
		if err := s.manager.Complete(p.SessionID); err != nil {
			if errors.Is(err, agentapi.ErrSessionNotFound) {
				return errorResult("session not found: %s", p.SessionID), nil
			}
			return errorResult("could not complete session: %v", err), nil
		}
	}
	return s.waitAndRenderFeedback(ctx, p.SessionID, p.TimeoutSeconds)
}

// toolReview implements request_review: present the document (always marked
// complete), open the browser, wait for feedback, and return the payload.
func (s *Server) toolReview(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p reviewParams
	if rerr := decodeArgs(args, &p); rerr != nil {
		return nil, rerr
	}
	if p.TimeoutSeconds < 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: `"timeout_seconds" must be zero or positive`}
	}
	md, err := readDocument(p.Markdown, p.Path)
	if err != nil {
		return errorResult("%v", err), nil
	}
	info, err := s.manager.Create(md, agentapi.CreateOptions{Title: p.Title, Prompt: p.Prompt, Choices: p.Choices})
	if err != nil {
		return errorResult("could not create session: %v", err), nil
	}
	if err := s.manager.Complete(info.ID); err != nil {
		return errorResult("could not complete session: %v", err), nil
	}
	s.maybeOpenBrowser(info.URL, p.openBrowser())
	return s.waitAndRenderFeedback(ctx, info.ID, p.TimeoutSeconds)
}

// waitAndRenderFeedback blocks until the session is terminal (bounded by
// timeoutSeconds; 0 waits forever), then renders the feedback payload. A
// timeout or unknown session is a tool error result, not a protocol error.
func (s *Server) waitAndRenderFeedback(ctx context.Context, id string, timeoutSeconds int) (any, *rpcError) {
	waitCtx := ctx
	if timeoutSeconds > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		defer cancel()
	}
	switch err := s.manager.WaitFeedback(waitCtx, id); {
	case err == nil:
	case errors.Is(err, context.DeadlineExceeded):
		return errorResult("feedback not ready: session %s is still open; call await_feedback (or request_review) again to keep waiting", id), nil
	case errors.Is(err, agentapi.ErrSessionNotFound):
		return errorResult("session not found: %s", id), nil
	case errors.Is(err, context.Canceled):
		return errorResult("server is shutting down; session %s is still open", id), nil
	default:
		return errorResult("could not wait for feedback: %v", err), nil
	}
	payload, err := s.manager.Feedback(id)
	if err != nil {
		return errorResult("could not build feedback: %v", err), nil
	}
	text, err := feedback.RenderJSON(payload)
	if err != nil {
		return errorResult("could not render feedback: %v", err), nil
	}
	return textResult(text), nil
}

// maybeOpenBrowser opens url when enabled; failures are logged, not fatal.
func (s *Server) maybeOpenBrowser(url string, enabled bool) {
	if !enabled {
		return
	}
	if err := browser.Open(url); err != nil {
		s.logf("warning: could not open browser: %v", err)
	}
}

// presentResult is the compact JSON reported by present_markdown.
type presentResult struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	State     string `json:"state"`
}

// compactJSON marshals v without indentation. Callers pass fixed result
// structs, so failure is impossible; a failure still degrades to a visible
// error value rather than an empty tool result.
func compactJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(data)
}

// readDocument resolves the markdown|path union: exactly one source must be
// set. Path-based documents are read locally and capped at maxMarkdownBytes.
func readDocument(inline *string, path string) ([]byte, error) {
	hasInline := inline != nil
	hasPath := path != ""
	if hasInline == hasPath {
		return nil, errors.New(`provide exactly one of "markdown" (inline content) or "path" (local file)`)
	}
	if hasInline {
		return []byte(*inline), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("access path %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory: %s", path)
	}
	if info.Size() > maxMarkdownBytes {
		return nil, fmt.Errorf("file %s is too large: %d bytes (max %d)", path, info.Size(), maxMarkdownBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxMarkdownBytes {
		return nil, fmt.Errorf("file %s is too large: %d bytes (max %d)", path, len(data), maxMarkdownBytes)
	}
	return data, nil
}
