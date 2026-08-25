package server_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/session"
	"github.com/mengkeat/yamdview/web"
)

// newStreamingTestServer builds a streaming review session and mounts its
// handler under /sessions/<id>/ on a throwaway outer server, mirroring how
// the agent API embeds per-session viewers. It returns the session and its
// base URL.
func newStreamingTestServer(t *testing.T) (*session.Session, string) {
	t.Helper()
	snapshot := document.DocumentSnapshot{Blocks: []document.Block{
		{ID: "b1", Source: "first paragraph", SourceStart: 0, SourceEnd: 15, StartLine: 1, EndLine: 1},
	}}
	review, err := session.NewStreaming("review-stream", "Review", "Prompt", nil, []byte("first paragraph"), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	// Real embedded assets: the mounted page must expose the session token
	// and use page-relative endpoint paths.
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := server.NewHandler(assets, server.PageDataFromAssets(assets, "Document", "<p>first paragraph</p>"), server.WithSession(review))
	if err != nil {
		t.Fatal(err)
	}

	const idPrefix = "/sessions/review-stream"
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, idPrefix)
		if rest == "" {
			http.Redirect(w, r, idPrefix+"/", http.StatusMovedPermanently)
			return
		}
		if !strings.HasPrefix(rest, "/") {
			http.NotFound(w, r)
			return
		}
		http.StripPrefix(idPrefix, viewer.Handler()).ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		outer.Close()
		_ = viewer.Close()
	})
	return review, outer.URL + idPrefix + "/"
}

func TestNewHandlerMountsViewerUnderPrefix(t *testing.T) {
	review, base := newStreamingTestServer(t)
	review.MarkComplete()

	page, err := http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	pageBody := readAllAndClose(t, page)
	if !strings.Contains(pageBody, review.Token) {
		t.Fatalf("mounted page missing session token: %s", pageBody)
	}
	if !strings.Contains(pageBody, `data-session-token="`+review.Token+`"`) {
		t.Fatal("mounted page missing data-session-token attribute")
	}

	snapshot, err := http.Get(base + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if body := readAllAndClose(t, snapshot); !strings.Contains(body, "first paragraph") {
		t.Fatalf("mounted snapshot missing content: %s", body)
	}

	// Annotation create via the mounted browser endpoint.
	req, _ := http.NewRequest(http.MethodPost, base+"api/session/annotations", strings.NewReader(`{"kind":"comment","block_id":"b1","quote":"first","comment":"note"}`))
	req.Header.Set(server.SessionTokenHeader, review.Token)
	created, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if body := readAllAndClose(t, created); created.StatusCode != http.StatusCreated {
		t.Fatalf("mounted annotation create status = %d (%s)", created.StatusCode, body)
	}

	submit, _ := http.NewRequest(http.MethodPost, base+"api/session/submit", strings.NewReader(`{"verdict":"approve","summary":"ok"}`))
	submit.Header.Set(server.SessionTokenHeader, review.Token)
	resp, err := http.DefaultClient.Do(submit)
	if err != nil {
		t.Fatal(err)
	}
	if body := readAllAndClose(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("mounted submit status = %d (%s)", resp.StatusCode, body)
	}
	if review.CurrentState() != session.Submitted {
		t.Fatalf("session state = %s, want submitted", review.CurrentState())
	}
}

func TestNewHandlerListenerlessGuards(t *testing.T) {
	viewer, err := server.NewHandler(testAssets, testPageData("Doc", "<p>x</p>"))
	if err != nil {
		t.Fatal(err)
	}
	if viewer.Handler() == nil {
		t.Fatal("Handler() must return a non-nil handler")
	}
	if viewer.Addr() != "" || viewer.URL() != "" {
		t.Fatalf("listenerless Addr/URL should be empty, got %q / %q", viewer.Addr(), viewer.URL())
	}
	viewer.Start() // must be a safe no-op
	if err := viewer.Serve(); err == nil {
		t.Fatal("Serve on listenerless server should fail")
	}
	if err := viewer.Close(); err != nil {
		t.Fatalf("Close on listenerless server: %v", err)
	}
	if err := viewer.Close(); err != nil {
		t.Fatalf("second Close should be a no-op: %v", err)
	}
}

func TestStreamingSessionGatesBrowserMutations(t *testing.T) {
	review, base := newStreamingTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, base+"api/session/annotations", strings.NewReader(`{"kind":"comment","block_id":"b1","quote":"first","comment":"note"}`))
	req.Header.Set(server.SessionTokenHeader, review.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if body := readAllAndClose(t, resp); resp.StatusCode != http.StatusConflict || !strings.Contains(body, "streaming") {
		t.Fatalf("streaming annotation status = %d (%s), want 409 streaming", resp.StatusCode, body)
	}

	submit, _ := http.NewRequest(http.MethodPost, base+"api/session/submit", strings.NewReader(`{"verdict":"approve","summary":"ok"}`))
	submit.Header.Set(server.SessionTokenHeader, review.Token)
	resp, err = http.DefaultClient.Do(submit)
	if err != nil {
		t.Fatal(err)
	}
	if body := readAllAndClose(t, resp); resp.StatusCode != http.StatusConflict || !strings.Contains(body, "streaming") {
		t.Fatalf("streaming submit status = %d (%s), want 409 streaming", resp.StatusCode, body)
	}
}

func TestSessionStateEventOnSubmit(t *testing.T) {
	review, base := newStreamingTestServer(t)
	review.MarkComplete()

	events, err := http.Get(base + "events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	reader := bufio.NewReader(events.Body)
	readSSEBlock(t, reader) // connected comment

	submit, _ := http.NewRequest(http.MethodPost, base+"api/session/submit", strings.NewReader(`{"verdict":"approve","summary":"ok"}`))
	submit.Header.Set(server.SessionTokenHeader, review.Token)
	resp, err := http.DefaultClient.Do(submit)
	if err != nil {
		t.Fatal(err)
	}
	readAllAndClose(t, resp)

	block := readSSEBlock(t, reader)
	if len(block) != 2 || block[0] != "event: session" {
		t.Fatalf("expected session event block, got %v", block)
	}
	var payload struct {
		State     string `json:"state"`
		Streaming bool   `json:"streaming"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block[1], "data: ")), &payload); err != nil {
		t.Fatalf("unmarshal session payload: %v", err)
	}
	if payload.State != "submitted" || payload.Streaming {
		t.Fatalf("unexpected session payload: %+v", payload)
	}
}

func readAllAndClose(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var buf strings.Builder
	if _, err := bufio.NewReader(resp.Body).WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
