package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestParseServeAPI(t *testing.T) {
	cfg, err := Parse([]string{"serve", "--api"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cfg.Mode != ModeServe {
		t.Errorf("expected ModeServe, got %q", cfg.Mode)
	}
	if !cfg.API {
		t.Error("expected API to be true")
	}
	if cfg.Addr != "127.0.0.1:0" {
		t.Errorf("expected default addr, got %q", cfg.Addr)
	}
	if cfg.UnsafeBind {
		t.Error("expected UnsafeBind to be false by default")
	}
	if cfg.MarkdownPath != "" {
		t.Errorf("expected no markdown path in serve mode, got %q", cfg.MarkdownPath)
	}
}

func TestParseServeWithoutAPIFails(t *testing.T) {
	_, err := Parse([]string{"serve"})
	if err == nil {
		t.Fatal("expected an error for serve without --api")
	}
	if !strings.Contains(err.Error(), "--api") {
		t.Fatalf("expected error to mention --api, got %q", err)
	}
}

func TestParseServeRejectsPositionalArgs(t *testing.T) {
	_, err := Parse([]string{"serve", "--api", "extra.md"})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("expected ErrUsage, got %v", err)
	}
}

func TestParseServeAddrFlag(t *testing.T) {
	cfg, err := Parse([]string{"serve", "--api", "--addr", "localhost:8080"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cfg.Addr != "localhost:8080" {
		t.Errorf("expected addr %q, got %q", "localhost:8080", cfg.Addr)
	}
}

func TestParseServeUnsafeBindFlag(t *testing.T) {
	cfg, err := Parse([]string{"serve", "--api", "--unsafe-bind"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !cfg.UnsafeBind {
		t.Error("expected UnsafeBind to be true")
	}
}

func TestParseServeRejectsNonLoopbackAddr(t *testing.T) {
	_, err := Parse([]string{"serve", "--api", "--addr", "0.0.0.0:8080"})
	if err == nil {
		t.Fatal("expected non-loopback addr to be refused")
	}
	if !strings.Contains(err.Error(), "--unsafe-bind") {
		t.Fatalf("expected error to mention --unsafe-bind, got %q", err)
	}
}

func TestParseServeAllowsNonLoopbackAddrWithUnsafeBind(t *testing.T) {
	cfg, err := Parse([]string{"serve", "--api", "--unsafe-bind", "--addr", "0.0.0.0:8080"})
	if err != nil {
		t.Fatalf("expected success with --unsafe-bind, got %v", err)
	}
	if cfg.Addr != "0.0.0.0:8080" {
		t.Errorf("expected addr %q, got %q", "0.0.0.0:8080", cfg.Addr)
	}
}

func TestValidateLoopbackBind(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		allowed bool
	}{
		{"ipv4 loopback", "127.0.0.1:0", true},
		{"ipv4 loopback any 127/8", "127.200.1.1:8080", true},
		{"localhost", "localhost:8080", true},
		{"ipv6 loopback", "[::1]:9000", true},
		{"empty host binds all interfaces", ":8080", false},
		{"wildcard ipv4", "0.0.0.0:8080", false},
		{"private ip", "192.168.0.1:8080", false},
		{"public ip", "8.8.8.8:8080", false},
		{"wildcard ipv6", "[::]:8080", false},
		{"arbitrary hostname", "example.com:8080", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLoopbackBind(tc.addr, false)
			if tc.allowed {
				if err != nil {
					t.Fatalf("expected %q to be allowed, got %v", tc.addr, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected %q to be refused", tc.addr)
			}
			if !strings.Contains(err.Error(), "--unsafe-bind") {
				t.Fatalf("expected refusal to mention --unsafe-bind, got %q", err)
			}
			if err := ValidateLoopbackBind(tc.addr, true); err != nil {
				t.Fatalf("expected %q to be allowed with unsafe bind, got %v", tc.addr, err)
			}
		})
	}
}
