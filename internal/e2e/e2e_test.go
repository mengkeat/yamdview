// Package e2e contains gated end-to-end tests that drive a real browser
// against the live viewer server over SSE. The tests are skipped unless
// YAMDVIEW_E2E=1 is set (and also skip, rather than fail, when no usable
// browser or node runtime is available), so plain `go test ./...` stays
// fast and hermetic.
package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/markdown"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/web"
)

const envEnabled = "YAMDVIEW_E2E"

const (
	paragraphCount    = 120
	insertedCount     = 8
	minBlocks         = paragraphCount + 1 // heading + paragraphs
	scrollTolerancePx = 2.0
	startTimeout      = 60 * time.Second
	patchTimeout      = 30 * time.Second
)

// driverConfig is the JSON configuration passed to driver.js.
type driverConfig struct {
	URL            string `json:"url"`
	ExecutablePath string `json:"executablePath"`
	MinBlocks      int    `json:"minBlocks"`
}

type readyMessage struct {
	Phase        string  `json:"phase"`
	RefID        string  `json:"refId"`
	TopBefore    float64 `json:"topBefore"`
	BlocksBefore int     `json:"blocksBefore"`
}

type doneMessage struct {
	Phase           string   `json:"phase"`
	TopAfter        *float64 `json:"topAfter"`
	SentinelKept    bool     `json:"sentinelKept"`
	BlocksAfter     int      `json:"blocksAfter"`
	ConsoleWarnings []string `json:"consoleWarnings"`
	Message         string   `json:"message"`
}

func skipUnlessEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv(envEnabled) != "1" {
		t.Skipf("set %s=1 to run gated browser tests", envEnabled)
	}
}

// findBrowser locates a Chrome/Chromium binary: YAMDVIEW_E2E_BROWSER first,
// then the Playwright browser cache, then common system install paths.
func findBrowser(t *testing.T) (string, bool) {
	t.Helper()

	if p := os.Getenv("YAMDVIEW_E2E_BROWSER"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		return "", false
	}

	if home, err := os.UserHomeDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(
			home, ".cache", "ms-playwright", "chromium-*", "chrome-linux*", "chrome"))
		sort.Strings(matches)
		if len(matches) > 0 {
			return matches[len(matches)-1], true
		}
	}

	for _, p := range []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/snap/bin/chromium",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}

	return "", false
}

// findNode locates a node runtime via PATH, then the default nvm layout.
func findNode(t *testing.T) (string, bool) {
	t.Helper()

	if p, err := exec.LookPath("node"); err == nil {
		return p, true
	}
	if home, err := os.UserHomeDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(
			home, ".nvm", "versions", "node", "*", "bin", "node"))
		sort.Strings(matches)
		if len(matches) > 0 {
			return matches[len(matches)-1], true
		}
	}
	return "", false
}

// nodeModulesPath returns the directory holding puppeteer-core for NODE_PATH.
func nodeModulesPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("YAMDVIEW_E2E_NODE_PATH"); p != "" {
		return p
	}
	// Default npx cache location where puppeteer-core is installed on this
	// machine; override with YAMDVIEW_E2E_NODE_PATH elsewhere.
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".npm", "_npx", "668c188756b835f3", "node_modules")
}

// writeMarkdown generates a scrolling document: one heading plus many
// unique paragraphs. It must not contain reference-style definitions,
// which force the viewer into full-reset mode.
func writeMarkdown(t *testing.T, dir string) (string, []byte) {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("# E2E Scroll Preservation\n\n")
	for i := 1; i <= paragraphCount; i++ {
		fmt.Fprintf(&buf,
			"Paragraph %d: the quick brown fox jumps over the lazy dog while the viewer keeps its scroll anchor steady across live block patches.\n\n",
			i)
	}
	src := buf.Bytes()

	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}
	return path, src
}

// withPrepended returns src with new paragraphs inserted above everything,
// so the patch shifts every existing block down while the reader is
// scrolled into the middle of the page.
func withPrepended(src []byte) []byte {
	var buf bytes.Buffer
	for i := 1; i <= insertedCount; i++ {
		fmt.Fprintf(&buf,
			"Inserted paragraph %d: this block was added above the viewport by the e2e scroll test.\n\n",
			i)
	}
	buf.Write(src)
	return buf.Bytes()
}

// startDriver launches node driver.js and returns its stdin pipe plus a
// channel of decoded stdout JSON messages.
func startDriver(t *testing.T, nodePath, nodeModules, cfgPath string) (io.WriteCloser, <-chan map[string]any, func()) {
	t.Helper()

	driverPath := filepath.Join("driver.js")
	cmd := exec.Command(nodePath, driverPath, cfgPath)
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodeModules)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("driver stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("driver stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start driver: %v", err)
	}

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	cleanup := func() {
		stdin.Close() // unblocks waitForGo if the driver is still waiting
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			<-exited
		}
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("driver stderr:\n%s", stderr.String())
		}
	}

	return stdin, decodeMessages(t, lines), cleanup
}

// decodeMessages converts raw driver stdout lines into decoded JSON maps.
func decodeMessages(t *testing.T, lines <-chan string) <-chan map[string]any {
	t.Helper()
	msgs := make(chan map[string]any, 16)
	go func() {
		defer close(msgs)
		for line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Errorf("driver emitted non-JSON line %q: %v", line, err)
				continue
			}
			msgs <- m
		}
	}()
	return msgs
}

// waitMessage reads driver messages until one with the wanted phase arrives
// or the deadline expires. Non-matching phases are reported as failures.
func waitMessage(t *testing.T, msgs <-chan map[string]any, phase string, timeout time.Duration) map[string]any {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatalf("driver exited before emitting %q message", phase)
			}
			got, _ := m["phase"].(string)
			switch got {
			case phase:
				return m
			case "error":
				t.Fatalf("driver error: %v", m["message"])
			default:
				t.Fatalf("unexpected driver phase %q (want %q)", got, phase)
			}
		case <-timer.C:
			t.Fatalf("timed out after %s waiting for driver %q message", timeout, phase)
		}
	}
}

// TestE2EScrollPreservationOnInsertAboveViewport verifies the live-patch
// path end to end: with the page scrolled into the middle of a long
// document, inserting paragraphs above the viewport must keep the visible
// content visually stable (scroll preservation) without replacing the DOM
// (no full-document reset).
func TestE2EScrollPreservationOnInsertAboveViewport(t *testing.T) {
	skipUnlessEnabled(t)

	browserPath, ok := findBrowser(t)
	if !ok {
		t.Skip("no Chrome/Chromium binary found (set YAMDVIEW_E2E_BROWSER)")
	}
	nodePath, ok := findNode(t)
	if !ok {
		t.Skip("node runtime not found in PATH or ~/.nvm")
	}
	nodeModules := nodeModulesPath(t)
	if _, err := os.Stat(filepath.Join(nodeModules, "puppeteer-core")); err != nil {
		t.Skipf("puppeteer-core not found under %s (set YAMDVIEW_E2E_NODE_PATH)", nodeModules)
	}

	dir := t.TempDir()
	mdPath, src := writeMarkdown(t, dir)

	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatalf("load assets: %v", err)
	}

	renderer := markdown.NewRenderer()
	snapshot, err := document.BuildSnapshot(renderer, src, document.DocumentSnapshot{})
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	srv, err := server.New("127.0.0.1:0", assets, server.PageData{
		Title:   "e2e scroll preservation",
		Content: template.HTML(snapshot.HTML),
	}, server.WithKatexFS(web.KatexFS()))
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer srv.Close()
	srv.Start()

	cfgPath := filepath.Join(dir, "driver-config.json")
	cfg, err := json.Marshal(driverConfig{
		URL:            srv.URL(),
		ExecutablePath: browserPath,
		MinBlocks:      minBlocks,
	})
	if err != nil {
		t.Fatalf("marshal driver config: %v", err)
	}
	if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
		t.Fatalf("write driver config: %v", err)
	}

	stdin, msgs, cleanup := startDriver(t, nodePath, nodeModules, cfgPath)
	defer cleanup()

	ready := waitMessage(t, msgs, "ready", startTimeout)
	refID, _ := ready["refId"].(string)
	topBefore, _ := ready["topBefore"].(float64)
	blocksBefore, _ := ready["blocksBefore"].(float64)
	if refID == "" || blocksBefore < float64(minBlocks) {
		t.Fatalf("bad ready message: %v", ready)
	}

	// Rewrite the file above the viewport's content and broadcast the
	// resulting incremental patches over SSE.
	nextSrc := withPrepended(src)
	if err := os.WriteFile(mdPath, nextSrc, 0o644); err != nil {
		t.Fatalf("rewrite markdown: %v", err)
	}
	next, err := document.BuildSnapshot(renderer, nextSrc, snapshot)
	if err != nil {
		t.Fatalf("build next snapshot: %v", err)
	}
	diff := document.Diff(snapshot, next)
	if diff.Reset {
		t.Fatal("expected incremental patches but diff produced a full reset")
	}
	if len(diff.Ops) == 0 {
		t.Fatal("expected non-empty patch op list")
	}
	if err := srv.BroadcastPatches(template.HTML(diff.Snapshot.HTML), diff.Ops); err != nil {
		t.Fatalf("broadcast patches: %v", err)
	}

	if _, err := io.WriteString(stdin, "go\n"); err != nil {
		t.Fatalf("signal driver: %v", err)
	}

	done := waitMessage(t, msgs, "done", patchTimeout)
	blocksAfter, _ := done["blocksAfter"].(float64)
	if want := blocksBefore + insertedCount; blocksAfter != want {
		t.Errorf("block count after patch = %d, want %d", int(blocksAfter), int(want))
	}

	// (a) Scroll preservation: the reference block must sit at the same
	// viewport-relative position after the insert above it.
	if topAfter, ok := done["topAfter"].(float64); ok && topAfter >= 0 {
		delta := topAfter - topBefore
		if delta < -scrollTolerancePx || delta > scrollTolerancePx {
			t.Errorf("reference block viewport-relative top moved by %.2fpx (%.2f -> %.2f), tolerance ±%.0fpx",
				delta, topBefore, topAfter, scrollTolerancePx)
		}
	} else {
		t.Error("reference block missing from DOM after patch")
	}

	// (b) No full reset: the pre-edit DOM node must still be the same node.
	// A reset replaces #document's innerHTML, which destroys the original
	// element and its JS expando sentinel property.
	sentinelKept, _ := done["sentinelKept"].(bool)
	if !sentinelKept {
		t.Error("pre-edit DOM node identity lost: full document reset occurred")
	}
	warnings, _ := done["consoleWarnings"].([]any)
	for _, w := range warnings {
		if s, ok := w.(string); ok && strings.Contains(s, "falling back to snapshot reset") {
			t.Errorf("viewer logged snapshot-reset fallback: %s", s)
		}
	}
}
