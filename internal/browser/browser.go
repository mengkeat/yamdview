// Package browser opens a URL in the system default web browser.
package browser

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type opener struct {
	name string
	args []string
}

var linuxOpeners = []opener{
	{name: "xdg-open"},
	{name: "gio", args: []string{"open"}},
	{name: "sensible-browser"},
}

var wslOpeners = []opener{
	{name: "wslview"},
	{name: "rundll32.exe", args: []string{"url.dll,FileProtocolHandler"}},
	{name: "cmd.exe", args: []string{"/C", "start", ""}},
}

const openWaitTimeout = 2 * time.Second

// Open opens the given URL in the system's default browser.
// It returns an error if the browser command cannot be determined or fails to start.
func Open(url string) error {
	return open(runtime.GOOS, url, exec.LookPath, isWSL(), runCommand)
}

// Command returns an *exec.Cmd that will open the given URL.
// It returns an error if the platform is unsupported or no opener is available.
func Command(url string) (*exec.Cmd, error) {
	return command(runtime.GOOS, url, exec.LookPath, isWSL())
}

func open(goos, url string, lookPath func(string) (string, error), isWSL bool, run func(*exec.Cmd) error) error {
	cmds, err := commands(goos, url, lookPath, isWSL)
	if err != nil {
		return err
	}

	errs := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		if err := run(cmd); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", strings.Join(cmd.Args, " "), err))
		}
	}

	return fmt.Errorf("could not open browser: %s", strings.Join(errs, "; "))
}

func runCommand(cmd *exec.Cmd) error {
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		if msg := strings.TrimSpace(output.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	case <-time.After(openWaitTimeout):
		// Some opener commands stay attached to the browser process. If the
		// command is still running after a short grace period, assume launch
		// succeeded rather than blocking yamdview startup.
		return nil
	}
}

func command(goos, url string, lookPath func(string) (string, error), isWSL bool) (*exec.Cmd, error) {
	cmds, err := commands(goos, url, lookPath, isWSL)
	if err != nil {
		return nil, err
	}
	return cmds[0], nil
}

func commands(goos, url string, lookPath func(string) (string, error), isWSL bool) ([]*exec.Cmd, error) {
	switch goos {
	case "linux":
		return linuxCommands(url, lookPath, isWSL)
	case "darwin":
		return []*exec.Cmd{exec.Command("open", url)}, nil
	case "windows":
		return []*exec.Cmd{exec.Command("rundll32", "url.dll,FileProtocolHandler", url)}, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", goos)
	}
}

func linuxCommands(url string, lookPath func(string) (string, error), isWSL bool) ([]*exec.Cmd, error) {
	openers := linuxOpeners
	if isWSL {
		openers = append(append([]opener{}, wslOpeners...), linuxOpeners...)
	}

	cmds := make([]*exec.Cmd, 0, len(openers))
	tried := make([]string, 0, len(openers))
	for _, opener := range openers {
		path, err := lookPath(opener.name)
		if err != nil {
			tried = append(tried, opener.name)
			continue
		}

		args := append([]string{}, opener.args...)
		args = append(args, url)
		cmds = append(cmds, exec.Command(path, args...))
	}

	if len(cmds) > 0 {
		return cmds, nil
	}

	return nil, fmt.Errorf("no browser opener found (tried %s)", strings.Join(tried, ", "))
}

func isWSL() bool {
	return fileHasWSLMarker("/proc/sys/kernel/osrelease") || fileHasWSLMarker("/proc/version")
}

func fileHasWSLMarker(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && hasWSLMarker(string(data))
}

func hasWSLMarker(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "microsoft") || strings.Contains(value, "wsl")
}
