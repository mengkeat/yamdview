package browser

import (
	"runtime"
	"testing"
)

func TestCommandReturnsCmdForCurrentOS(t *testing.T) {
	cmd, err := Command("http://127.0.0.1:8080")
	if err != nil {
		// Only acceptable on truly unsupported platforms.
		if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			t.Fatalf("expected command for %s, got error: %v", runtime.GOOS, err)
		}
		t.Skipf("unsupported platform: %s", runtime.GOOS)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	if len(cmd.Args) < 2 {
		t.Fatalf("expected at least 2 args (cmd + url), got %v", cmd.Args)
	}
	// Last argument should be the URL.
	if cmd.Args[len(cmd.Args)-1] != "http://127.0.0.1:8080" {
		t.Errorf("expected URL as last arg, got %q", cmd.Args[len(cmd.Args)-1])
	}
}

func TestOpenDoesNotPanic(t *testing.T) {
	// Open may fail if no display is available (e.g. CI), but it should not panic.
	_ = Open("http://127.0.0.1:1")
}
