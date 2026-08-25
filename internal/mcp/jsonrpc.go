package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// JSON-RPC 2.0 error codes used by this server.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// serverName is the server identity reported by initialize.
const serverName = "yamdview"

// initializeInstructions is the human-facing usage hint returned by
// initialize; MCP clients may surface it to the agent.
const initializeInstructions = "present_markdown streams a Markdown document to a browser review session (leave complete unset while streaming; set complete=true when the document is finished). await_feedback waits for the human's feedback, optionally marking the stream complete first. request_review presents, completes, and waits in one call."

// rpcMessage is one decoded request or notification line.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error makes rpcError usable as a Go error for logging and propagation.
func (e *rpcError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// rpcResponse is one encoded response line. Success responses always carry a
// Result struct (never an empty map, which omitempty would drop); error
// responses carry Error instead.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// initializeResult is the MCP initialize result.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverIdentity     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

type serverCapabilities struct {
	Tools toolsCapability `json:"tools"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type serverIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// toolsListResult is the MCP tools/list result.
type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

// handleLine processes one input line: blank lines are skipped, malformed
// JSON gets a -32700 response with a null id, notifications produce no
// output, and requests are dispatched by method with the id echoed back.
func (s *Server) handleLine(ctx context.Context, line []byte) error {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil
	}
	var msg rpcMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return s.writeResponse(nil, nil, &rpcError{Code: codeParseError, Message: "parse error: malformed JSON"})
	}
	if len(msg.ID) == 0 {
		// Notification (e.g. notifications/initialized, notifications/cancelled):
		// never answered.
		s.logf("notification %q ignored", msg.Method)
		return nil
	}
	result, rpcErr := s.dispatch(ctx, msg)
	if rpcErr != nil {
		return s.writeResponse(msg.ID, nil, rpcErr)
	}
	return s.writeResponse(msg.ID, result, nil)
}

// dispatch resolves one request to its result or JSON-RPC error.
func (s *Server) dispatch(ctx context.Context, msg rpcMessage) (any, *rpcError) {
	switch msg.Method {
	case "initialize":
		return initializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    serverCapabilities{Tools: toolsCapability{ListChanged: false}},
			ServerInfo:      serverIdentity{Name: serverName, Version: ServerVersion},
			Instructions:    initializeInstructions,
		}, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return toolsListResult{Tools: toolDefinitions()}, nil
	case "tools/call":
		return s.callTool(ctx, msg.Params)
	case "":
		return nil, &rpcError{Code: codeMethodNotFound, Message: "request has no method"}
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method not found: %s", msg.Method)}
	}
}

// writeResponse encodes one response line and flushes it so clients (and
// tests) see it immediately.
func (s *Server) writeResponse(id json.RawMessage, result any, rpcErr *rpcError) error {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}
	if err := s.enc.Encode(resp); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if err := s.out.Flush(); err != nil {
		return fmt.Errorf("flush response: %w", err)
	}
	return nil
}
