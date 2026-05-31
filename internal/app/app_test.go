package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/watcher"
)

var testAssets = server.Assets{
	IndexHTML: `<!DOCTYPE html><html><head><title>{{.Title}}</title><style>{{.CSS}}</style></head><body><main id="document">{{.Content}}</main><script>{{.JS}}</script></body></html>`,
	ViewerCSS: "body { margin: 0; }",
	ViewerJS:  "// test js",
}

func TestReloadLoopBroadcastsResetForChangedMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("# Original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{MarkdownPath: path}, testAssets)
	initialContent, err := application.renderFile()
	if err != nil {
		t.Fatal(err)
	}

	srv, err := server.New("127.0.0.1:0", testAssets, server.PageData{
		Title:   path,
		Content: initialContent,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.Start()

	resp, err := http.Get(srv.URL() + "events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	readSSEBlock(t, reader) // connected comment

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := make(chan watcher.Event, 1)
	watchErrs := make(chan error)
	go application.reloadLoop(ctx, srv, changes, watchErrs)

	if err := os.WriteFile(path, []byte("# Updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes <- watcher.Event{Path: path}

	payload := readResetPayload(t, reader)
	if payload.Op != "reset" || !strings.Contains(payload.HTML, "Updated") {
		t.Fatalf("unexpected reset payload: %+v", payload)
	}

	snapshot, err := http.Get(srv.URL() + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(snapshot.Body)
	snapshot.Body.Close()
	if !strings.Contains(string(body), "Updated") {
		t.Fatalf("expected updated snapshot, got %q", body)
	}
}

func readResetPayload(t *testing.T, reader *bufio.Reader) struct {
	Op   string `json:"op"`
	HTML string `json:"html"`
} {
	t.Helper()

	block := readSSEBlock(t, reader)
	if len(block) != 2 || block[0] != "event: reset" || !strings.HasPrefix(block[1], "data: ") {
		t.Fatalf("expected reset SSE block, got %v", block)
	}

	var payload struct {
		Op   string `json:"op"`
		HTML string `json:"html"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block[1], "data: ")), &payload); err != nil {
		t.Fatalf("unmarshal reset payload: %v", err)
	}
	return payload
}

func readSSEBlock(t *testing.T, reader *bufio.Reader) []string {
	t.Helper()

	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE line: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return lines
		}
		lines = append(lines, line)
	}
}

func TestExportWritesStandaloneFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")

	if err := os.WriteFile(src, []byte("# Exported\n\nHello, world.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: src,
		Export:       dst,
		ExportView:   "tablet",
	}, testAssets)

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	out := string(data)
	if !strings.Contains(out, "Hello, world.") {
		t.Error("exported file missing content")
	}
	if !strings.Contains(out, "Exported") {
		t.Error("exported file missing title")
	}
	if !strings.Contains(out, "--measure:40rem") {
		t.Error("exported file missing tablet viewport override")
	}
	if !strings.Contains(out, testAssets.ViewerCSS) {
		t.Error("exported file missing CSS")
	}
	if !strings.Contains(out, testAssets.ViewerJS) {
		t.Error("exported file missing JS")
	}
}

func TestExportWithoutViewport(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")

	if err := os.WriteFile(src, []byte("# Plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: src,
		Export:       dst,
	}, testAssets)

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	out := string(data)
	if !strings.Contains(out, "Plain") {
		t.Error("exported file missing content")
	}
	// No export override comment should be present.
	if strings.Contains(out, "yamdview export") {
		t.Error("exported file should not contain viewport override comment")
	}
}
