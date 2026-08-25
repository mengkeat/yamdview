package agentapi_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/agentapi"
	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/web"
)

// newTestAPIServer starts a real agent API server on a random loopback port.
func newTestAPIServer(t *testing.T) *agentapi.Server {
	t.Helper()
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := agentapi.New(assets, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// apiCall issues a bearer-authenticated JSON request; token may be "" or
// wrong to exercise the auth layer.
func apiCall(t *testing.T, method, url, token, body string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// createSession creates a session over the API and returns its ID and
// viewer URL.
func createSession(t *testing.T, srv *agentapi.Server, markdown string) (id, viewerURL string) {
	t.Helper()
	body := fmt.Sprintf(`{"markdown":%q, "title":"Plan", "prompt":"Please review", "choices":["approve","request_changes","comment"]}`, markdown)
	resp := apiCall(t, http.MethodPost, srv.URL()+"/api/v1/sessions", srv.Token(), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var info agentapi.SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !strings.HasPrefix(info.ID, "s-") {
		t.Fatalf("session id = %q, want s- prefix", info.ID)
	}
	if want := srv.URL() + "/sessions/" + info.ID + "/"; info.URL != want {
		t.Fatalf("session url = %q, want %q", info.URL, want)
	}
	return info.ID, info.URL
}

var (
	tokenPattern   = regexp.MustCompile(`data-session-token="([^"]+)"`)
	blockIDPattern = regexp.MustCompile(`id="(block-[^"]+)"`)
)

// pageToken fetches the viewer page and extracts its session token.
func pageToken(t *testing.T, viewerURL string) string {
	t.Helper()
	resp, err := http.Get(viewerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer page status = %d (%s)", resp.StatusCode, page)
	}
	match := tokenPattern.FindSubmatch(page)
	if match == nil {
		t.Fatalf("viewer page has no data-session-token attribute")
	}
	return string(match[1])
}

// firstBlockID returns the first md-block ID from the session snapshot.
func firstBlockID(t *testing.T, viewerURL string) string {
	t.Helper()
	resp, err := http.Get(viewerURL + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	snapshot, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	match := blockIDPattern.FindSubmatch(snapshot)
	if match == nil {
		t.Fatalf("snapshot has no block ids: %s", snapshot)
	}
	return string(match[1])
}

// browserPost posts to a viewer-relative session endpoint using the page token.
func browserPost(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(server.SessionTokenHeader, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// appendToSession posts one append request and decodes its result.
func appendToSession(t *testing.T, srv *agentapi.Server, id, body string) agentapi.AppendResult {
	t.Helper()
	resp := apiCall(t, http.MethodPost, srv.URL()+"/api/v1/sessions/"+id+"/append", srv.Token(), body)
	var result agentapi.AppendResult
	decodeJSONBody(t, resp, &result)
	return result
}

// subscribeEvents connects to the session's SSE stream and consumes the
// initial connected comment.
func subscribeEvents(t *testing.T, viewerURL string) *bufio.Reader {
	t.Helper()
	events, err := http.Get(viewerURL + "events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { events.Body.Close() })
	reader := bufio.NewReader(events.Body)
	if block, err := readSSEBlock(reader); err != nil || len(block) != 1 || block[0] != ": connected" {
		t.Fatalf("expected connected comment, got %v (err %v)", block, err)
	}
	return reader
}

// readEvent reads the next SSE event block and unmarshals its data line.
func readEvent(t *testing.T, reader *bufio.Reader, wantName string) map[string]any {
	t.Helper()
	block, err := readSSEBlock(reader)
	if err != nil {
		t.Fatalf("read %s event: %v", wantName, err)
	}
	if len(block) != 2 || block[0] != "event: "+wantName {
		t.Fatalf("expected %s event block, got %v", wantName, block)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block[1], "data: ")), &payload); err != nil {
		t.Fatalf("unmarshal %s payload: %v", wantName, err)
	}
	return payload
}

// readSSEBlock reads one newline-framed SSE block; an error means the stream
// ended.
func readSSEBlock(reader *bufio.Reader) ([]string, error) {
	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return lines, nil
		}
		lines = append(lines, line)
	}
}

func decodeJSONBody(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode %s response: %v", resp.Request.URL.Path, err)
	}
}

func TestSessionLifecycleEndToEnd(t *testing.T) {
	srv := newTestAPIServer(t)
	id, viewerURL := createSession(t, srv, "First paragraph.\n\nSecond paragraph.")

	// Viewer page is reachable at the returned URL without a bearer token.
	page, err := http.Get(viewerURL)
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(pageBody), "First paragraph.") {
		t.Fatalf("viewer page status = %d body = %s", page.StatusCode, pageBody)
	}
	token := pageToken(t, viewerURL)

	// Two appends: one streaming chunk, one that completes the stream.
	if result := appendToSession(t, srv, id, `{"markdown":"\n\nThird paragraph."}`); result.State != agentapi.StateStreaming {
		t.Fatalf("first append state = %q, want streaming", result.State)
	}
	if result := appendToSession(t, srv, id, `{"markdown":"\n\nFourth paragraph.","complete":true}`); result.State != agentapi.StateComplete {
		t.Fatalf("second append state = %q, want complete", result.State)
	}

	annotateAndSubmit(t, viewerURL, token)
	assertFeedbackPayload(t, srv, id, firstBlockID(t, viewerURL))
}

// annotateAndSubmit creates one annotation and submits the review through
// the browser endpoints.
func annotateAndSubmit(t *testing.T, viewerURL, token string) {
	t.Helper()
	blockID := firstBlockID(t, viewerURL)
	annotation := fmt.Sprintf(`{"kind":"comment","block_id":%q,"quote":"First paragraph.","start_line":1,"end_line":1,"comment":"Solid opening"}`, blockID)
	created := browserPost(t, viewerURL+"api/session/annotations", token, annotation)
	if createdBody, _ := io.ReadAll(created.Body); created.StatusCode != http.StatusCreated {
		t.Fatalf("annotation create status = %d (%s)", created.StatusCode, createdBody)
	}
	created.Body.Close()

	submitted := browserPost(t, viewerURL+"api/session/submit", token, `{"verdict":"approve","summary":"Looks good"}`)
	if submitBody, _ := io.ReadAll(submitted.Body); submitted.StatusCode != http.StatusOK {
		t.Fatalf("submit status = %d (%s)", submitted.StatusCode, submitBody)
	}
	submitted.Body.Close()
}

// assertFeedbackPayload long-polls nothing: the session is already terminal,
// so the endpoint must return the strict payload immediately.
func assertFeedbackPayload(t *testing.T, srv *agentapi.Server, id, blockID string) {
	t.Helper()
	resp := apiCall(t, http.MethodGet, srv.URL()+"/api/v1/sessions/"+id+"/feedback", srv.Token(), "")
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feedback status = %d (%s)", resp.StatusCode, raw)
	}
	payload, err := feedback.DecodeJSON(raw) // strict round-trip
	if err != nil {
		t.Fatalf("feedback payload does not round-trip: %v", err)
	}
	if payload.SessionID != id || payload.Title != "Plan" || payload.Prompt != "Please review" {
		t.Fatalf("unexpected payload header: %+v", payload)
	}
	if payload.Verdict != "approve" || payload.Summary != "Looks good" {
		t.Fatalf("unexpected verdict/summary: %+v", payload)
	}
	if len(payload.Comments) != 1 {
		t.Fatalf("comments = %+v, want exactly one", payload.Comments)
	}
	comment := payload.Comments[0]
	if comment.BlockID != blockID || comment.Quote != "First paragraph." || comment.Comment != "Solid opening" {
		t.Fatalf("unexpected comment: %+v", comment)
	}
}

func TestSessionURLRedirectsWithoutTrailingSlash(t *testing.T) {
	srv := newTestAPIServer(t)
	id, _ := createSession(t, srv, "Hello.")

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL() + "/sessions/" + id)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/sessions/"+id+"/" {
		t.Fatalf("Location = %q, want /sessions/%s/", loc, id)
	}
}

func TestFeedbackLongPoll(t *testing.T) {
	srv := newTestAPIServer(t)
	id, viewerURL := createSession(t, srv, "Draft one.")
	token := pageToken(t, viewerURL)

	// Complete the stream so the browser can submit later.
	complete := apiCall(t, http.MethodPost, srv.URL()+"/api/v1/sessions/"+id+"/complete", srv.Token(), "")
	complete.Body.Close()

	// Short wait on an open session expires promptly with 408 + JSON error.
	start := time.Now()
	resp := apiCall(t, http.MethodGet, srv.URL()+"/api/v1/sessions/"+id+"/feedback?wait=50ms", srv.Token(), "")
	var errBody struct {
		Error string `json:"error"`
	}
	decodeJSONBody(t, resp, &errBody)
	if resp.StatusCode != http.StatusRequestTimeout || errBody.Error == "" {
		t.Fatalf("status = %d, body = %+v, want 408 with JSON error", resp.StatusCode, errBody)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("wait=50ms took %s to expire", elapsed)
	}

	// Submit from another goroutine; the long poll must return promptly.
	go func() {
		time.Sleep(150 * time.Millisecond)
		submitted := browserPost(t, viewerURL+"api/session/submit", token, `{"verdict":"comment","summary":"noted"}`)
		submitted.Body.Close()
	}()

	start = time.Now()
	resp = apiCall(t, http.MethodGet, srv.URL()+"/api/v1/sessions/"+id+"/feedback?wait=5s", srv.Token(), "")
	var payload feedback.Payload
	decodeJSONBody(t, resp, &payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("long poll did not return promptly on submit (%s)", elapsed)
	}
	if payload.Verdict != "comment" || payload.SessionID != id {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestBearerAuthEnforcedOnAllRoutes(t *testing.T) {
	srv := newTestAPIServer(t)
	id, viewerURL := createSession(t, srv, "Authed doc.")
	deleteID, _ := createSession(t, srv, "Delete me.")

	cases := []struct {
		method, path string
		body         string
	}{
		{http.MethodPost, "/api/v1/sessions", `{"markdown":"x"}`},
		{http.MethodPost, "/api/v1/sessions/" + id + "/append", `{"markdown":"y"}`},
		{http.MethodPost, "/api/v1/sessions/" + id + "/complete", "{}"},
		{http.MethodPut, "/api/v1/sessions/" + id + "/document", `{"markdown":"z"}`},
		{http.MethodGet, "/api/v1/sessions/" + id + "/feedback", ""},
		{http.MethodDelete, "/api/v1/sessions/" + deleteID, ""},
	}
	for _, auth := range []string{"missing", "wrong"} {
		for _, tc := range cases {
			t.Run(auth+" "+tc.method+" "+tc.path, func(t *testing.T) {
				token := ""
				if auth == "wrong" {
					token = "deadbeef"
				}
				resp := apiCall(t, tc.method, srv.URL()+tc.path, token, tc.body)
				var errBody struct {
					Error string `json:"error"`
				}
				decodeJSONBody(t, resp, &errBody)
				if resp.StatusCode != http.StatusUnauthorized || errBody.Error == "" {
					t.Fatalf("status = %d body = %+v, want 401 with JSON error", resp.StatusCode, errBody)
				}
			})
		}
	}

	// Correct token: every route answers (not necessarily 2xx — feedback on
	// an open session without wait is 408 — but never 401).
	paths := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/v1/sessions", `{"markdown":"x"}`},
		{http.MethodPost, "/api/v1/sessions/" + id + "/append", `{"markdown":"y"}`},
		{http.MethodPost, "/api/v1/sessions/" + id + "/complete", "{}"},
		{http.MethodPut, "/api/v1/sessions/" + id + "/document", `{"markdown":"z"}`},
		{http.MethodGet, "/api/v1/sessions/" + id + "/feedback", ""},
	}
	for _, tc := range paths {
		resp := apiCall(t, tc.method, srv.URL()+tc.path, srv.Token(), tc.body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("%s %s: unexpected 401 with correct token", tc.method, tc.path)
		}
	}
	deleted := apiCall(t, http.MethodDelete, srv.URL()+"/api/v1/sessions/"+deleteID, srv.Token(), "")
	deleted.Body.Close()
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", deleted.StatusCode)
	}

	// Viewer pages are intentionally not bearer-gated; the per-session token
	// protects mutations.
	page, err := http.Get(viewerURL)
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("viewer page status = %d, want 200 without bearer", page.StatusCode)
	}
}

func TestAppendStreamsPatchesOverSSE(t *testing.T) {
	srv := newTestAPIServer(t)
	id, viewerURL := createSession(t, srv, "First paragraph.\n\nSecond paragraph.")
	// Subscribe before appending so the patch event is captured.
	reader := subscribeEvents(t, viewerURL)

	result := appendToSession(t, srv, id, `{"markdown":"\n\nThird paragraph."}`)
	if result.State != agentapi.StateStreaming || result.Reset || result.OpsApplied == 0 {
		t.Fatalf("append result = %+v, want streaming incremental patches", result)
	}
	payload := readEvent(t, reader, "patch")
	foundInsert := false
	for _, rawOp := range payload["ops"].([]any) {
		op := rawOp.(map[string]any)
		if op["op"] != "insert_after" {
			t.Fatalf("unexpected op %v in streamed patch", op["op"])
		}
		if html, _ := op["html"].(string); strings.Contains(html, "Third paragraph.") {
			foundInsert = true
		}
	}
	if !foundInsert {
		t.Fatalf("no insert_after op carried the appended paragraph: %v", payload["ops"])
	}

	// No reset may follow the patch.
	next := watchNextSSEBlock(reader)
	select {
	case block, ok := <-next:
		if ok && len(block) == 2 && block[0] == "event: reset" {
			t.Fatalf("append produced a reset event: %v", block)
		}
	case <-time.After(300 * time.Millisecond):
		// No further events: expected.
	}

	snapshot, err := http.Get(viewerURL + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(snapshot.Body)
	snapshot.Body.Close()
	if !strings.Contains(string(body), "Third paragraph.") {
		t.Fatalf("snapshot missing appended text: %s", body)
	}
}

// watchNextSSEBlock returns a channel carrying the next SSE block from
// reader, closed when the stream ends.
func watchNextSSEBlock(reader *bufio.Reader) chan []string {
	next := make(chan []string, 1)
	go func() {
		block, err := readSSEBlock(reader)
		if err != nil {
			close(next)
			return
		}
		next <- block
	}()
	return next
}

func TestCompleteEmitsSessionEvent(t *testing.T) {
	srv := newTestAPIServer(t)
	id, viewerURL := createSession(t, srv, "Lifecycle doc.")
	reader := subscribeEvents(t, viewerURL)
	next := watchNextSSEBlock(reader)

	// Completing the stream emits the small "session" lifecycle event.
	appendToSession(t, srv, id, `{"markdown":"","complete":true}`)
	select {
	case block, ok := <-next:
		if !ok {
			t.Fatal("session event never arrived after complete")
		}
		if len(block) != 2 || block[0] != "event: session" {
			t.Fatalf("expected session event block, got %v", block)
		}
		var state struct {
			Streaming bool `json:"streaming"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(block[1], "data: ")), &state); err != nil {
			t.Fatalf("unmarshal session payload: %v", err)
		}
		if state.Streaming {
			t.Fatal("session event should report streaming=false")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session event after complete")
	}
}

func TestAnnotationsGatedWhileStreaming(t *testing.T) {
	srv := newTestAPIServer(t)
	id, viewerURL := createSession(t, srv, "Streaming draft.")
	token := pageToken(t, viewerURL)
	blockID := firstBlockID(t, viewerURL)

	annotation := fmt.Sprintf(`{"kind":"comment","block_id":%q,"quote":"Streaming","comment":"too early"}`, blockID)
	resp := browserPost(t, viewerURL+"api/session/annotations", token, annotation)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "streaming") {
		t.Fatalf("streaming annotation status = %d (%s), want 409 streaming", resp.StatusCode, body)
	}

	submit := browserPost(t, viewerURL+"api/session/submit", token, `{"verdict":"approve","summary":"no"}`)
	submit.Body.Close()
	if submit.StatusCode != http.StatusConflict {
		t.Fatalf("streaming submit status = %d, want 409", submit.StatusCode)
	}

	complete := apiCall(t, http.MethodPost, srv.URL()+"/api/v1/sessions/"+id+"/complete", srv.Token(), "")
	completeBody, _ := io.ReadAll(complete.Body)
	complete.Body.Close()
	if complete.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d (%s)", complete.StatusCode, completeBody)
	}

	resp = browserPost(t, viewerURL+"api/session/annotations", token, annotation)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("post-complete annotation status = %d, want 201", resp.StatusCode)
	}
}

func TestDeleteSessionRemovesViewerAndAPI(t *testing.T) {
	srv := newTestAPIServer(t)
	id, viewerURL := createSession(t, srv, "Temporary.")

	resp := apiCall(t, http.MethodDelete, srv.URL()+"/api/v1/sessions/"+id, srv.Token(), "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp.StatusCode)
	}

	page, err := http.Get(viewerURL)
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.StatusCode != http.StatusNotFound {
		t.Fatalf("viewer page after delete = %d, want 404", page.StatusCode)
	}

	feedbackResp := apiCall(t, http.MethodGet, srv.URL()+"/api/v1/sessions/"+id+"/feedback", srv.Token(), "")
	feedbackResp.Body.Close()
	if feedbackResp.StatusCode != http.StatusNotFound {
		t.Fatalf("feedback after delete = %d, want 404", feedbackResp.StatusCode)
	}

	again := apiCall(t, http.MethodDelete, srv.URL()+"/api/v1/sessions/"+id, srv.Token(), "")
	again.Body.Close()
	if again.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", again.StatusCode)
	}
}

func TestReplaceDocumentUpdatesSnapshot(t *testing.T) {
	srv := newTestAPIServer(t)
	id, viewerURL := createSession(t, srv, "Old content one.\n\nOld content two.")

	resp := apiCall(t, http.MethodPut, srv.URL()+"/api/v1/sessions/"+id+"/document", srv.Token(), `{"markdown":"# Fresh heading\n\nFresh paragraph."}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replace status = %d, want 200", resp.StatusCode)
	}

	snapshot, err := http.Get(viewerURL + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(snapshot.Body)
	snapshot.Body.Close()
	if !strings.Contains(string(body), "Fresh heading") || strings.Contains(string(body), "Old content") {
		t.Fatalf("snapshot not replaced: %s", body)
	}
}

func TestCreateValidatesBody(t *testing.T) {
	srv := newTestAPIServer(t)

	for name, body := range map[string]string{
		"missing markdown": `{}`,
		"unknown field":    `{"markdown":"x","bogus":1}`,
		"trailing value":   `{"markdown":"x"} {}`,
		"markdown not str": `{"markdown":42}`,
		"not an object":    `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := apiCall(t, http.MethodPost, srv.URL()+"/api/v1/sessions", srv.Token(), body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestFeedbackValidatesWaitParameter(t *testing.T) {
	srv := newTestAPIServer(t)
	id, _ := createSession(t, srv, "Wait validation.")

	resp := apiCall(t, http.MethodGet, srv.URL()+"/api/v1/sessions/"+id+"/feedback?wait=bogus", srv.Token(), "")
	var errBody struct {
		Error string `json:"error"`
	}
	decodeJSONBody(t, resp, &errBody)
	if resp.StatusCode != http.StatusBadRequest || errBody.Error == "" {
		t.Fatalf("status = %d body = %+v, want 400 with JSON error", resp.StatusCode, errBody)
	}
}

func TestUnknownSessionIs404(t *testing.T) {
	srv := newTestAPIServer(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/sessions/nope/append"},
		{http.MethodPost, "/api/v1/sessions/nope/complete"},
		{http.MethodPut, "/api/v1/sessions/nope/document"},
		{http.MethodGet, "/api/v1/sessions/nope/feedback"},
		{http.MethodDelete, "/api/v1/sessions/nope"},
	} {
		resp := apiCall(t, tc.method, srv.URL()+tc.path, srv.Token(), `{"markdown":"x"}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", tc.method, tc.path, resp.StatusCode)
		}
	}

	page, err := http.Get(srv.URL() + "/sessions/nope/")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown viewer page status = %d, want 404", page.StatusCode)
	}
}
