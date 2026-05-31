package server_test

import (
	"bufio"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/server"
)

var testAssets = server.Assets{
	IndexHTML: `<!DOCTYPE html><html><head><title>{{.Title}}</title><style>{{.CSS}}</style></head><body>{{.Content}}</body></html>`,
	ViewerCSS: "body { margin: 0; }",
	ViewerJS:  "// test js",
}

func testPageData(title, content string) server.PageData {
	return server.PageData{
		Title:   title,
		Content: template.HTML(content),
		CSS:     template.CSS(testAssets.ViewerCSS),
		JS:      template.JS(testAssets.ViewerJS),
	}
}

func TestNewServerListen(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Hello</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	addr := srv.Addr()
	if addr == "" {
		t.Fatal("expected non-empty address")
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("expected localhost address, got %s", addr)
	}
}

func TestServerURL(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Hello</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	url := srv.URL()
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("expected http URL, got %s", url)
	}
}

func TestServerIndexReturnsHTML(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test Doc", "<p>Hello World</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	srv.Start()

	resp, err := http.Get(srv.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "Hello World") {
		t.Errorf("response missing content, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "Test Doc") {
		t.Errorf("response missing title, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "body { margin: 0; }") {
		t.Errorf("response missing CSS, got: %s", bodyStr)
	}
}

func TestServerSnapshotReturnsContent(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Snapshot Content</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	srv.Start()

	resp, err := http.Get(srv.URL() + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	bodyStr := string(body)
	if bodyStr != "<p>Snapshot Content</p>" {
		t.Errorf("snapshot returned %q", bodyStr)
	}
}

func TestServerNotFound(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Hello</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	srv.Start()

	resp, err := http.Get(srv.URL() + "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSetContentUpdatesSnapshot(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Original</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	srv.Start()

	// Check original content.
	resp, err := http.Get(srv.URL() + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "<p>Original</p>" {
		t.Fatalf("expected original content, got %q", body)
	}

	// Update content.
	srv.SetContent("<p>Updated</p>")

	// Check updated content.
	resp, err = http.Get(srv.URL() + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "<p>Updated</p>" {
		t.Fatalf("expected updated content, got %q", body)
	}
}

func TestBroadcastResetSendsSSEAndUpdatesSnapshot(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Original</p>"))
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
	connected := readSSEBlock(t, reader)
	if len(connected) != 1 || connected[0] != ": connected" {
		t.Fatalf("expected connected comment, got %v", connected)
	}

	if err := srv.BroadcastReset("<p>Updated</p>"); err != nil {
		t.Fatal(err)
	}

	block := readSSEBlock(t, reader)
	if len(block) != 2 {
		t.Fatalf("expected reset event and data, got %v", block)
	}
	if block[0] != "event: reset" {
		t.Fatalf("expected reset event, got %q", block[0])
	}
	if !strings.HasPrefix(block[1], "data: ") {
		t.Fatalf("expected data line, got %q", block[1])
	}

	var payload struct {
		Op   string `json:"op"`
		HTML string `json:"html"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block[1], "data: ")), &payload); err != nil {
		t.Fatalf("unmarshal reset payload: %v", err)
	}
	if payload.Op != "reset" || payload.HTML != "<p>Updated</p>" {
		t.Fatalf("unexpected payload: %+v", payload)
	}

	snapshot, err := http.Get(srv.URL() + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(snapshot.Body)
	snapshot.Body.Close()
	if string(body) != "<p>Updated</p>" {
		t.Fatalf("expected updated snapshot, got %q", body)
	}
}

func TestRenderPage(t *testing.T) {
	out, err := server.RenderPage(testAssets, testPageData("Rendered", "<p>Rendered Content</p>"))
	if err != nil {
		t.Fatal(err)
	}

	bodyStr := string(out)
	if !strings.Contains(bodyStr, "Rendered Content") {
		t.Errorf("rendered page missing content: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "Rendered") {
		t.Errorf("rendered page missing title: %s", bodyStr)
	}
}

func TestServerRejectsInvalidAddr(t *testing.T) {
	_, err := server.New("256.256.256.256:99999", testAssets, server.PageData{})
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestValidExportView(t *testing.T) {
	if server.ValidExportView("phone") != true {
		t.Error("expected phone to be valid")
	}
	if server.ValidExportView("tablet") != true {
		t.Error("expected tablet to be valid")
	}
	if server.ValidExportView("laptop") != true {
		t.Error("expected laptop to be valid")
	}
	if server.ValidExportView("desktop") != true {
		t.Error("expected desktop to be valid")
	}
	if server.ValidExportView("watch") != false {
		t.Error("expected watch to be invalid")
	}
	if server.ValidExportView("") != false {
		t.Error("expected empty string to be invalid")
	}
}

func TestExportStandaloneContainsContent(t *testing.T) {
	out, err := server.ExportStandalone(testAssets, testPageData("Export", "<p>Exported</p>"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Exported") {
		t.Error("export missing content")
	}
	if !strings.Contains(out, "Export") {
		t.Error("export missing title")
	}
	if !strings.Contains(out, "body { margin: 0; }") {
		t.Error("export missing CSS")
	}
}

func TestExportStandaloneRejectsUnknownView(t *testing.T) {
	_, err := server.ExportStandalone(testAssets, testPageData("X", "<p>x</p>"), "watch")
	if err == nil {
		t.Fatal("expected error for unknown view")
	}
}

func TestExportStandaloneInjectsViewportOverride(t *testing.T) {
	out, err := server.ExportStandalone(testAssets, testPageData("X", "<p>x</p>"), "tablet")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--measure:40rem") {
		t.Error("export missing tablet viewport override")
	}
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
