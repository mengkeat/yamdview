package server_test

import (
	"bufio"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/session"
	"github.com/mengkeat/yamdview/web"
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

func TestBroadcastPatchesSendsSSEAndUpdatesSnapshot(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", `<section class="md-block" id="old"><p>Original</p></section>`))
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

	updated := template.HTML(`<section class="md-block" id="new"><p>Updated</p></section>`)
	err = srv.BroadcastPatches(updated, []document.PatchOp{{
		Op:   document.OpReplace,
		ID:   "old",
		HTML: string(updated),
	}})
	if err != nil {
		t.Fatal(err)
	}

	block := readSSEBlock(t, reader)
	if len(block) != 2 {
		t.Fatalf("expected patch event and data, got %v", block)
	}
	if block[0] != "event: patch" {
		t.Fatalf("expected patch event, got %q", block[0])
	}
	if !strings.HasPrefix(block[1], "data: ") {
		t.Fatalf("expected data line, got %q", block[1])
	}

	var payload struct {
		Ops []document.PatchOp `json:"ops"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block[1], "data: ")), &payload); err != nil {
		t.Fatalf("unmarshal patch payload: %v", err)
	}
	if len(payload.Ops) != 1 || payload.Ops[0].Op != document.OpReplace || payload.Ops[0].ID != "old" {
		t.Fatalf("unexpected patch payload: %+v", payload)
	}
	if !strings.Contains(payload.Ops[0].HTML, "Updated") {
		t.Fatalf("patch HTML missing update: %+v", payload.Ops[0])
	}

	snapshot, err := http.Get(srv.URL() + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(snapshot.Body)
	snapshot.Body.Close()
	if string(body) != string(updated) {
		t.Fatalf("expected updated snapshot, got %q", body)
	}
}

func TestBroadcastPatchesWithNoOpsOnlyUpdatesSnapshot(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Original</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	srv.Start()

	updated := template.HTML("<p>Updated</p>")
	if err := srv.BroadcastPatches(updated, nil); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL() + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != string(updated) {
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

func TestExportStandaloneViewportOverrides(t *testing.T) {
	tests := []struct {
		view    string
		measure string
	}{
		{"phone", "22rem"},
		{"tablet", "40rem"},
		{"laptop", "52rem"},
		{"desktop", "62rem"},
	}

	for _, tt := range tests {
		t.Run(tt.view, func(t *testing.T) {
			out, err := server.ExportStandalone(testAssets, testPageData("X", "<p>x</p>"), tt.view)
			if err != nil {
				t.Fatal(err)
			}
			want := "--measure:" + tt.measure
			if !strings.Contains(out, want) {
				t.Errorf("export for %q missing %q", tt.view, want)
			}
			if !strings.Contains(out, "yamdview export") {
				t.Errorf("export for %q missing override comment", tt.view)
			}
		})
	}
}

func TestExportStandaloneNoViewportHasNoOverride(t *testing.T) {
	out, err := server.ExportStandalone(testAssets, testPageData("X", "<p>x</p>"), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "yamdview export") {
		t.Error("export without viewport should not contain override comment")
	}
	if strings.Contains(out, "!important") {
		t.Error("export without viewport should not contain !important")
	}
}

func TestExportStandaloneAutoPopulatesCSSAndJS(t *testing.T) {
	// Pass PageData with empty CSS and JS; ExportStandalone fills them from assets.
	data := server.PageData{Title: "Auto", Content: "<p>auto</p>"}
	out, err := server.ExportStandalone(testAssets, data, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "body { margin: 0; }") {
		t.Error("export did not auto-populate CSS from assets")
	}
	// The JS field won't appear because testAssets.IndexHTML lacks {{.JS}},
	// but the function should not error.
	if !strings.Contains(out, "auto") {
		t.Error("export missing content after auto-populate")
	}
}

func TestExportStandalonePreservesExplicitCSS(t *testing.T) {
	// Explicit CSS should be used, not overwritten by assets.
	data := server.PageData{
		Title:   "Explicit",
		Content: "<p>explicit</p>",
		CSS:     "/* custom css */",
	}
	out, err := server.ExportStandalone(testAssets, data, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/* custom css */") {
		t.Error("export lost explicit CSS")
	}
	if !strings.Contains(out, "yamdview export") {
		t.Error("export missing override comment after explicit CSS")
	}
	if !strings.Contains(out, "--measure:22rem") {
		t.Error("export missing phone viewport override after explicit CSS")
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

func TestClientErrorRejectsGET(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Hello</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.Start()

	resp, err := http.Get(srv.URL() + "client-error")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", resp.StatusCode)
	}
}

func TestClientErrorAcceptsPOST(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Hello</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.Start()

	body := `[{"block_id":"b1","kind":"math","message":"bad tex","tex":"\\bad"}]`
	resp, err := http.Post(srv.URL()+"client-error", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for POST, got %d", resp.StatusCode)
	}
}

func TestClientErrorHandler(t *testing.T) {
	var received server.ClientError
	srv, err := server.New(
		"127.0.0.1:0", testAssets, testPageData("Test", "<p>Hello</p>"),
		server.WithClientErrorHandler(func(ce server.ClientError) {
			received = ce
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.Start()

	body := `[{"block_id":"b2","kind":"math","message":"parse fail","tex":"$$bad"}]`
	resp, err := http.Post(srv.URL()+"client-error", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if received.BlockID != "b2" {
		t.Errorf("expected block_id b2, got %q", received.BlockID)
	}
	if received.Kind != "math" {
		t.Errorf("expected kind math, got %q", received.Kind)
	}
	if received.Message != "parse fail" {
		t.Errorf("expected message parse fail, got %q", received.Message)
	}
}

func TestKatexStaticServing(t *testing.T) {
	// Create a minimal FS with one file.
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Hello</p>"))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Without KaTeX FS, /katex/ should 404.
	srv.Start()

	resp, err := http.Get(srv.URL() + "katex/katex.min.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 without katex FS, got %d", resp.StatusCode)
	}
}

func newReviewTestServer(t *testing.T) (*server.Server, *session.Session) {
	t.Helper()
	review, err := session.New("review-1", "Review this", "What do you think?", []string{"approve", "changes"}, []byte("# source"), document.DocumentSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New("127.0.0.1:0", assets, testPageData("Document", "<p>Content</p>"), server.WithSession(review))
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	t.Cleanup(func() { srv.Close() })
	return srv, review
}

func TestReviewSessionMetadataAndPage(t *testing.T) {
	srv, review := newReviewTestServer(t)

	page, err := http.Get(srv.URL())
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(pageBody), `id="review-session"`) || !strings.Contains(string(pageBody), review.Token) {
		t.Fatalf("review page is missing banner or injected token")
	}
	for _, want := range []string{"Review this", "What do you think?", "data-verdict=\"approve\"", "Summary", "Submit"} {
		if !strings.Contains(string(pageBody), want) {
			t.Errorf("review page missing %q", want)
		}
	}

	resp, err := http.Get(srv.URL() + "api/session")
	if err != nil {
		t.Fatal(err)
	}
	apiBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(apiBody, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["id"] != "review-1" || metadata["state"] != "open" {
		t.Fatalf("unexpected session metadata: %#v", metadata)
	}
	if _, exposed := metadata["token"]; exposed || strings.Contains(string(apiBody), review.Token) {
		t.Fatal("session API exposed the session token")
	}
}

func TestReviewSessionSubmitRequiresToken(t *testing.T) {
	srv, review := newReviewTestServer(t)

	for name, token := range map[string]string{"missing": "", "wrong": "not-the-token"} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, srv.URL()+"api/session/submit", strings.NewReader(`{"verdict":"approve","summary":"Looks good"}`))
			if err != nil {
				t.Fatal(err)
			}
			if token != "" {
				req.Header.Set(server.SessionTokenHeader, token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
		})
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL()+"api/session/submit", strings.NewReader(`{"verdict":"approve","summary":"Looks good"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(server.SessionTokenHeader, review.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("successful submit status = %d, want 200", resp.StatusCode)
	}
	metadata := review.Metadata()
	if metadata.State != session.Submitted || metadata.Verdict != "approve" || metadata.Summary != "Looks good" {
		t.Fatalf("session was not submitted: %+v", metadata)
	}
}

func TestReviewSessionSubmitValidatesMethodAndBody(t *testing.T) {
	srv, review := newReviewTestServer(t)

	get, err := http.Get(srv.URL() + "api/session/submit")
	if err != nil {
		t.Fatal(err)
	}
	get.Body.Close()
	if get.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET submit status = %d, want 405", get.StatusCode)
	}

	for _, body := range []string{"{", `{}`, `{"verdict":"approve"}`} {
		req, err := http.NewRequest(http.MethodPost, srv.URL()+"api/session/submit", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(server.SessionTokenHeader, review.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q status = %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestServerCloseClosesActiveSSEClients(t *testing.T) {
	srv, err := server.New("127.0.0.1:0", testAssets, testPageData("Test", "<p>Hello</p>"))
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()

	resp, err := http.Get(srv.URL() + "events")
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	readDone := make(chan struct{})
	go func() {
		_, _ = io.ReadAll(resp.Body)
		close(readDone)
	}()

	if err := srv.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("active SSE client remained open after server close")
	}
	_ = resp.Body.Close()
}
