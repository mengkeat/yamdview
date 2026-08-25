package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMCP(t *testing.T) {
	cfg, err := Parse([]string{"mcp"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cfg.Mode != ModeMCP {
		t.Errorf("expected ModeMCP, got %q", cfg.Mode)
	}
	if cfg.Addr != "127.0.0.1:0" {
		t.Errorf("expected default addr, got %q", cfg.Addr)
	}
	if cfg.UnsafeBind {
		t.Error("expected UnsafeBind to be false by default")
	}
	if cfg.MarkdownPath != "" {
		t.Errorf("expected no markdown path in mcp mode, got %q", cfg.MarkdownPath)
	}
}

func TestParseMCPModeAlias(t *testing.T) {
	if MCPMode != ModeMCP {
		t.Fatalf("MCPMode = %q, want %q", MCPMode, ModeMCP)
	}
}

func TestParseMCPRejectsPositionalArgs(t *testing.T) {
	_, err := Parse([]string{"mcp", "extra.md"})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("expected ErrUsage, got %v", err)
	}
}

func TestParseMCPAddrFlag(t *testing.T) {
	cfg, err := Parse([]string{"mcp", "--addr", "localhost:8080"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cfg.Addr != "localhost:8080" {
		t.Errorf("expected addr %q, got %q", "localhost:8080", cfg.Addr)
	}
}

func TestParseMCPRejectsNonLoopbackAddr(t *testing.T) {
	_, err := Parse([]string{"mcp", "--addr", "0.0.0.0:8080"})
	if err == nil {
		t.Fatal("expected non-loopback addr to be refused")
	}
	if !strings.Contains(err.Error(), "--unsafe-bind") {
		t.Fatalf("expected error to mention --unsafe-bind, got %q", err)
	}
}

func TestParseMCPAllowsNonLoopbackAddrWithUnsafeBind(t *testing.T) {
	cfg, err := Parse([]string{"mcp", "--unsafe-bind", "--addr", "0.0.0.0:8080"})
	if err != nil {
		t.Fatalf("expected success with --unsafe-bind, got %v", err)
	}
	if !cfg.UnsafeBind {
		t.Error("expected UnsafeBind to be true")
	}
	if cfg.Addr != "0.0.0.0:8080" {
		t.Errorf("expected addr %q, got %q", "0.0.0.0:8080", cfg.Addr)
	}
}
