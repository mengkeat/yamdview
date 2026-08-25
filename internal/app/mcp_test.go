package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/mcp"
	"github.com/mengkeat/yamdview/web"
)

// TestRunMCPReturnsAtStdinEOF checks that RunMCP shuts down cleanly when the
// protocol input reaches EOF and leaves the protocol output untouched.
func TestRunMCPReturnsAtStdinEOF(t *testing.T) {
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	protocol := &bytes.Buffer{}
	application := New(Config{
		Mode:    ModeMCP,
		Addr:    "127.0.0.1:0",
		Input:   strings.NewReader(""),
		Output:  protocol,
		Context: context.Background(),
	}, assets)

	done := make(chan error, 1)
	go func() { done <- application.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v at stdin EOF, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunMCP did not return at stdin EOF")
	}
	if protocol.Len() != 0 {
		t.Fatalf("protocol output not empty: %q", protocol.String())
	}
}

// TestRunMCPInitializeHandshake drives one initialize request through the
// app-level plumbing and checks the response line.
func TestRunMCPInitializeHandshake(t *testing.T) {
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	protocol := &bytes.Buffer{}
	application := New(Config{
		Mode:    ModeMCP,
		Addr:    "127.0.0.1:0",
		Input:   strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"),
		Output:  protocol,
		Context: context.Background(),
	}, assets)

	done := make(chan error, 1)
	go func() { done <- application.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunMCP did not return at stdin EOF")
	}

	lines := strings.Split(strings.TrimSpace(protocol.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("protocol output has %d lines, want 1: %q", len(lines), protocol.String())
	}
	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("decode response %q: %v", lines[0], err)
	}
	if resp.JSONRPC != "2.0" || resp.ID != 1 {
		t.Fatalf("response envelope = %+v", resp)
	}
	if resp.Result.ProtocolVersion != mcp.ProtocolVersion {
		t.Fatalf("protocolVersion = %q, want %q", resp.Result.ProtocolVersion, mcp.ProtocolVersion)
	}
	if resp.Result.ServerInfo.Name != "yamdview" {
		t.Fatalf("serverInfo.name = %q, want yamdview", resp.Result.ServerInfo.Name)
	}
}
