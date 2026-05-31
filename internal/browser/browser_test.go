package browser

import (
	"errors"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCommandReturnsCmdForCurrentOS(t *testing.T) {
	cmd, err := Command("http://127.0.0.1:8080")
	if err != nil {
		if runtime.GOOS == "linux" && strings.Contains(err.Error(), "no browser opener found") {
			t.Skipf("no Linux browser opener available: %v", err)
		}
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

func TestCommandReturnsPlatformCommands(t *testing.T) {
	url := "http://127.0.0.1:8080"
	tests := []struct {
		name     string
		goos     string
		wantArgs []string
	}{
		{
			name:     "macOS",
			goos:     "darwin",
			wantArgs: []string{"open", url},
		},
		{
			name:     "Windows",
			goos:     "windows",
			wantArgs: []string{"rundll32", "url.dll,FileProtocolHandler", url},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := command(tt.goos, url, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(cmd.Args, tt.wantArgs) {
				t.Fatalf("expected args %v, got %v", tt.wantArgs, cmd.Args)
			}
		})
	}
}

func TestLinuxCommandUsesFirstAvailableOpener(t *testing.T) {
	url := "http://127.0.0.1:8080"
	tests := []struct {
		name      string
		available map[string]string
		wantArgs  []string
	}{
		{
			name: "prefers xdg-open",
			available: map[string]string{
				"xdg-open": "/bin/xdg-open",
				"gio":      "/bin/gio",
			},
			wantArgs: []string{"/bin/xdg-open", url},
		},
		{
			name: "falls back to gio open",
			available: map[string]string{
				"gio": "/bin/gio",
			},
			wantArgs: []string{"/bin/gio", "open", url},
		},
		{
			name: "falls back to sensible-browser",
			available: map[string]string{
				"sensible-browser": "/bin/sensible-browser",
			},
			wantArgs: []string{"/bin/sensible-browser", url},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := command("linux", url, fakeLookPath(tt.available), false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(cmd.Args, tt.wantArgs) {
				t.Fatalf("expected args %v, got %v", tt.wantArgs, cmd.Args)
			}
		})
	}
}

func TestLinuxCommandPrefersWSLOpenerWhenInWSL(t *testing.T) {
	url := "http://127.0.0.1:8080"
	cmd, err := command("linux", url, fakeLookPath(map[string]string{
		"rundll32.exe": "/mnt/c/Windows/System32/rundll32.exe",
		"xdg-open":     "/bin/xdg-open",
	}), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"/mnt/c/Windows/System32/rundll32.exe", "url.dll,FileProtocolHandler", url}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("expected args %v, got %v", wantArgs, cmd.Args)
	}
}

func TestLinuxCommandFallsBackToLinuxOpenersWhenWSLOpenersMissing(t *testing.T) {
	url := "http://127.0.0.1:8080"
	cmd, err := command("linux", url, fakeLookPath(map[string]string{
		"xdg-open": "/bin/xdg-open",
	}), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"/bin/xdg-open", url}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("expected args %v, got %v", wantArgs, cmd.Args)
	}
}

func TestOpenFallsBackWhenAvailableOpenerFails(t *testing.T) {
	url := "http://127.0.0.1:8080"
	runs := [][]string{}
	run := func(cmd *exec.Cmd) error {
		runs = append(runs, append([]string{}, cmd.Args...))
		if len(runs) == 1 {
			return errors.New("failed")
		}
		return nil
	}

	err := open("linux", url, fakeLookPath(map[string]string{
		"xdg-open": "/bin/xdg-open",
		"gio":      "/bin/gio",
	}), false, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantRuns := [][]string{
		{"/bin/xdg-open", url},
		{"/bin/gio", "open", url},
	}
	if !reflect.DeepEqual(runs, wantRuns) {
		t.Fatalf("expected runs %v, got %v", wantRuns, runs)
	}
}

func TestOpenErrorsWhenAllAvailableOpenersFail(t *testing.T) {
	err := open("linux", "http://127.0.0.1:8080", fakeLookPath(map[string]string{
		"xdg-open": "/bin/xdg-open",
	}), false, func(*exec.Cmd) error {
		return errors.New("failed")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "could not open browser") || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("expected opener failure error, got %v", err)
	}
}

func TestLinuxCommandErrorsWhenNoOpenerFound(t *testing.T) {
	_, err := command("linux", "http://127.0.0.1:8080", fakeLookPath(nil), false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no browser opener found") {
		t.Fatalf("expected no opener error, got %v", err)
	}
}

func TestUnsupportedPlatformErrors(t *testing.T) {
	_, err := command("plan9", "http://127.0.0.1:8080", nil, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported platform: plan9") {
		t.Fatalf("expected unsupported platform error, got %v", err)
	}
}

func TestHasWSLMarker(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "WSL2 osrelease", value: "6.6.114.1-microsoft-standard-WSL2", want: true},
		{name: "WSL uppercase", value: "Linux version WSL2", want: true},
		{name: "regular Linux", value: "6.8.0-31-generic", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasWSLMarker(tt.value); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func fakeLookPath(paths map[string]string) func(string) (string, error) {
	return func(file string) (string, error) {
		if path, ok := paths[file]; ok {
			return path, nil
		}
		return "", exec.ErrNotFound
	}
}
