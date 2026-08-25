package mcp_test

import (
	"strings"
	"testing"
)

// TestUnknownMethodsAre32601 covers unknown and missing methods.
func TestUnknownMethodsAre32601(t *testing.T) {
	h := startMCP(t)

	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/get"}`,
		`{"jsonrpc":"2.0","id":3}`,
	} {
		resp := h.call(t, line)
		if resp.Error == nil {
			t.Fatalf("request %s succeeded, want -32601", line)
		}
		if resp.Error.Code != -32601 {
			t.Fatalf("error code = %d, want -32601 (%s)", resp.Error.Code, line)
		}
	}
}

// TestStringIDEcho checks that non-numeric ids are echoed back verbatim.
func TestStringIDEcho(t *testing.T) {
	h := startMCP(t)
	resp := h.call(t, `{"jsonrpc":"2.0","id":"req-abc","method":"ping"}`)
	assertID(t, resp, `"req-abc"`)
}

// TestMalformedLinesAre32700 covers structurally broken input lines.
func TestMalformedLinesAre32700(t *testing.T) {
	h := startMCP(t)

	for _, line := range []string{
		`{"jsonrpc":`,
		`not json at all`,
		`[1,2,3]`,
	} {
		resp := h.call(t, line)
		if resp.Error == nil {
			t.Fatalf("line %q succeeded, want -32700", line)
		}
		if resp.Error.Code != -32700 {
			t.Fatalf("error code = %d, want -32700 (%q)", resp.Error.Code, line)
		}
		if got := strings.TrimSpace(string(resp.ID)); got != "null" {
			t.Fatalf("parse error id = %s, want null", got)
		}
	}
}

// TestNotificationsProduceNoOutput verifies that both initialized and
// cancelled notifications are silently absorbed.
func TestNotificationsProduceNoOutput(t *testing.T) {
	h := startMCP(t)

	h.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9}}`)

	// The next response must belong to the ping that follows: neither
	// notification may have produced an output line.
	resp := h.call(t, `{"jsonrpc":"2.0","id":42,"method":"ping"}`)
	assertID(t, resp, "42")
}

// TestInvalidToolsCallParamsAre32602 covers protocol-level tools/call
// problems, which are JSON-RPC errors rather than isError results.
func TestInvalidToolsCallParamsAre32602(t *testing.T) {
	h := startMCP(t)

	cases := []struct {
		name string
		line string
	}{
		{"missing name", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":{}}}`},
		{"unknown tool", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}`},
		{"params not object", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":[1,2]}`},
		{"unknown params field", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ping","bogus":1}}`},
		{"bad argument type", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"present_markdown","arguments":{"markdown":5}}}`},
		{"unknown argument field", `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"present_markdown","arguments":{"bogus":true}}}`},
		{"missing session_id", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"await_feedback","arguments":{}}}`},
		{"negative timeout", `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"await_feedback","arguments":{"session_id":"s-x","timeout_seconds":-1}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.call(t, tc.line)
			if resp.Error == nil {
				t.Fatalf("request succeeded, want -32602")
			}
			if resp.Error.Code != -32602 {
				t.Fatalf("error code = %d, want -32602", resp.Error.Code)
			}
			if resp.Error.Message == "" {
				t.Fatal("error message is empty")
			}
		})
	}
}
