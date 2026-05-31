// Package browser opens a URL in the system default web browser.
package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Open opens the given URL in the system's default browser.
// It returns an error if the browser command cannot be determined or fails to start.
func Open(url string) error {
	cmd, err := Command(url)
	if err != nil {
		return err
	}
	return cmd.Start()
}

// Command returns an *exec.Cmd that will open the given URL.
// It returns an error if the platform is unsupported.
func Command(url string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url), nil
	case "darwin":
		return exec.Command("open", url), nil
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
