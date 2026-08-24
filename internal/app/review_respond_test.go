package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/internal/llm"
	"github.com/mengkeat/yamdview/web"
)

// captureLog redirects the standard logger into a buffer for the duration of
// the test so warnings emitted by the reformulation wiring can be asserted.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	var buf syncBuffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buf
}

// syncBuffer is a mutex-guarded buffer safe for concurrent writers such as
// the review goroutine and the test reading it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// writeLLMConfig writes a provider config file usable by the respond wiring.
func writeLLMConfig(t *testing.T, providersJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llm-config.json")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"providers":{%s}}`, providersJSON)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type reviewOutcome struct {
	status ReviewExitStatus
	err    error
}

// startRespondReview starts a review with the given respond settings. When
// stubResolve is non-nil it replaces App.resolveRespond (the provider-
// construction seam) so tests can inject mock providers.
func startRespondReview(t *testing.T, respond llm.RespondSettings, stubResolve func(cfg llm.Config, s llm.RespondSettings) (llm.Provider, string, error)) (*App, *bytes.Buffer, chan reviewOutcome) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(source, []byte("# Respond\n\nOriginal text.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	application := New(Config{
		Mode: ModeReview, MarkdownPath: source, Addr: "127.0.0.1:0", NoOpen: true,
		Output: &output,
		Review: ReviewConfig{Title: "Respond review", Prompt: "Check this", Format: feedback.FormatJSON, Respond: respond},
	}, assets)
	if stubResolve != nil {
		application.resolveRespond = stubResolve
	}
	result := make(chan reviewOutcome, 1)
	go func() {
		status, err := application.RunReview()
		result <- reviewOutcome{status, err}
	}()
	waitReviewURL(t, application)
	return application, &output, result
}

// postSession posts a JSON body to a session API endpoint with the review token.
func postSession(t *testing.T, url, path, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Yamdview-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodePayload(t *testing.T, data []byte) feedback.Payload {
	t.Helper()
	payload, err := feedback.DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestReviewRespondOffLeavesEndpointDisabled(t *testing.T) {
	application, _, result := startRespondReview(t, llm.RespondSettings{}, nil)
	review := application.ReviewSession()

	resp := postSession(t, application.ReviewURL(), "api/session/reformulate", review.Token, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reformulate status = %d, want 404 when respond is off", resp.StatusCode)
	}

	resp2 := postSession(t, application.ReviewURL(), "api/session/submit", review.Token, `{"verdict":"approve","summary":"ok"}`)
	resp2.Body.Close()
	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
}

func TestReviewRespondAskFlowEndToEnd(t *testing.T) {
	configPath := writeLLMConfig(t, `"hosted":{"type":"openai-compatible","base_url":"http://127.0.0.1:9/v1","model":"cfg-model"}`)
	mock := llm.NewMock("respond-mock")
	stub := func(cfg llm.Config, s llm.RespondSettings) (llm.Provider, string, error) {
		if s.ProviderName != "hosted" {
			t.Errorf("provider name = %q, want hosted", s.ProviderName)
		}
		return mock, "stub-model", nil
	}
	settings := llm.RespondSettings{
		Mode: llm.ModeAsk, ProviderName: "hosted",
		Model: "cli-model", ConfigPath: configPath,
	}
	application, output, result := startRespondReview(t, settings, stub)
	url := application.ReviewURL()
	review := application.ReviewSession()

	// The served page advertises the respond configuration.
	page, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(page.Body)
	page.Body.Close()
	for _, want := range []string{
		`data-respond-mode="ask"`,
		`data-respond-provider="respond-mock"`,
		`data-respond-model="stub-model"`,
	} {
		if !strings.Contains(string(pageBody), want) {
			t.Fatalf("review page missing %q:\n%s", want, pageBody)
		}
	}

	// Create one annotation so the rephrase input has a quote to cover.
	blockID := ""
	for _, block := range review.Snapshot.Blocks {
		if strings.Contains(block.Source, "Original text.") {
			blockID = block.ID
			break
		}
	}
	createBody, err := json.Marshal(map[string]string{
		"kind": "comment", "block_id": blockID, "quote": "Original text.", "comment": "tighten",
	})
	if err != nil {
		t.Fatal(err)
	}
	createResp := postSession(t, url, "api/session/annotations", review.Token, string(createBody))
	if createResp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(createResp.Body)
		t.Fatalf("annotation create status = %d: %s", createResp.StatusCode, data)
	}
	createResp.Body.Close()

	// Preview: the mock quotes the annotation verbatim so validation accepts.
	mock.Queue(llm.MockText(`{"text":"Fix Original text. as noted","confidence":0.9}`))
	previewResp := postSession(t, url, "api/session/reformulate", review.Token, `{}`)
	defer previewResp.Body.Close()
	if previewResp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(previewResp.Body)
		t.Fatalf("reformulate status = %d: %s", previewResp.StatusCode, data)
	}
	var preview struct {
		Applied      bool `json:"applied"`
		Reformulated *struct {
			Text           string `json:"text"`
			Provider       string `json:"provider"`
			ApprovedByUser bool   `json:"approved_by_user"`
		} `json:"reformulated"`
	}
	if err := json.NewDecoder(previewResp.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Applied || preview.Reformulated == nil {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if preview.Reformulated.ApprovedByUser {
		t.Error("preview must not be approved before submission")
	}

	submitResp := postSession(t, url, "api/session/submit", review.Token,
		`{"verdict":"approve","summary":"done","use_reformulated":true}`)
	submitResp.Body.Close()
	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
	payload := decodePayload(t, output.Bytes())
	if payload.Reformulated == nil {
		t.Fatal("payload missing reformulated section")
	}
	if !payload.Reformulated.ApprovedByUser {
		t.Errorf("approved_by_user = false, want true")
	}
	if payload.Reformulated.Text != "Fix Original text. as noted" {
		t.Errorf("reformulated text = %q", payload.Reformulated.Text)
	}
	if len(payload.Comments) != 1 {
		t.Errorf("raw comments lost: %#v", payload.Comments)
	}
}

func TestReviewRespondAskWithoutPreviewKeepsRawFeedback(t *testing.T) {
	configPath := writeLLMConfig(t, `"hosted":{"type":"openai-compatible","base_url":"http://127.0.0.1:9/v1","model":"cfg-model"}`)
	mock := llm.NewMock("respond-mock")
	stub := func(llm.Config, llm.RespondSettings) (llm.Provider, string, error) { return mock, "stub-model", nil }
	settings := llm.RespondSettings{Mode: llm.ModeAsk, ProviderName: "hosted", ConfigPath: configPath}
	application, output, result := startRespondReview(t, settings, stub)

	resp := postSession(t, application.ReviewURL(), "api/session/submit", application.ReviewSession().Token,
		`{"verdict":"approve","summary":"plain"}`)
	resp.Body.Close()
	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
	payload := decodePayload(t, output.Bytes())
	if payload.Reformulated != nil {
		t.Errorf("unexpected reformulated section: %+v", payload.Reformulated)
	}
	if calls := len(mock.Calls()); calls != 0 {
		t.Errorf("ask mode must not call the provider without a preview request, got %d calls", calls)
	}
}

func TestReviewRespondAutoReformulatesOnceUnapproved(t *testing.T) {
	configPath := writeLLMConfig(t, `"hosted":{"type":"openai-compatible","base_url":"http://127.0.0.1:9/v1","model":"cfg-model"}`)
	mock := llm.NewMock("respond-mock")
	mock.Queue(llm.MockText(`{"text":"Consolidated instruction.","confidence":0.9}`))
	stub := func(llm.Config, llm.RespondSettings) (llm.Provider, string, error) { return mock, "auto-model", nil }
	settings := llm.RespondSettings{Mode: llm.ModeAuto, ProviderName: "hosted", ConfigPath: configPath}
	application, output, result := startRespondReview(t, settings, stub)

	resp := postSession(t, application.ReviewURL(), "api/session/submit", application.ReviewSession().Token,
		`{"verdict":"approve","summary":"done"}`)
	resp.Body.Close()
	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
	payload := decodePayload(t, output.Bytes())
	if payload.Reformulated == nil {
		t.Fatal("auto mode payload missing reformulated section")
	}
	if payload.Reformulated.ApprovedByUser {
		t.Error("auto-mode reformulation must stay unapproved")
	}
	if payload.Reformulated.Text != "Consolidated instruction." {
		t.Errorf("reformulated text = %q", payload.Reformulated.Text)
	}
	if calls := len(mock.Calls()); calls != 1 {
		t.Errorf("provider called %d times, want exactly 1", calls)
	}
}

func TestReviewRespondProviderFailureFallsBackCleanly(t *testing.T) {
	configPath := writeLLMConfig(t, `"hosted":{"type":"openai-compatible","base_url":"http://127.0.0.1:9/v1","model":"cfg-model"}`)
	mock := llm.NewMock("slow-respond")
	mock.SetDelay(500 * time.Millisecond)
	stub := func(llm.Config, llm.RespondSettings) (llm.Provider, string, error) { return mock, "slow-model", nil }
	settings := llm.RespondSettings{
		Mode: llm.ModeAuto, ProviderName: "hosted", ConfigPath: configPath,
		Timeout: 20 * time.Millisecond,
	}
	warnings := captureLog(t)
	application, output, result := startRespondReview(t, settings, stub)

	resp := postSession(t, application.ReviewURL(), "api/session/submit", application.ReviewSession().Token,
		`{"verdict":"approve","summary":"done"}`)
	resp.Body.Close()
	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
	payload := decodePayload(t, output.Bytes())
	if payload.Reformulated != nil {
		t.Errorf("timed-out reformulation must not reach the payload: %+v", payload.Reformulated)
	}
	if !strings.Contains(warnings.String(), "respond llm") {
		t.Errorf("expected a respond llm diagnostic warning, got:\n%s", warnings.String())
	}
}

func TestReviewRespondMissingDefaultModelDisablesGracefully(t *testing.T) {
	// The provider lists models but configures none and the CLI passes no
	// override, so resolution fails; the review must complete without the
	// reformulate endpoint and log a warning.
	configPath := writeLLMConfig(t, `"hosted":{"type":"openai-compatible","base_url":"http://127.0.0.1:9/v1","models":["m-a","m-b"],"api_key":"test-key"}`)
	settings := llm.RespondSettings{Mode: llm.ModeAsk, ProviderName: "hosted", ConfigPath: configPath}
	warnings := captureLog(t)
	application, output, result := startRespondReview(t, settings, nil)
	review := application.ReviewSession()

	resp := postSession(t, application.ReviewURL(), "api/session/reformulate", review.Token, `{}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reformulate status = %d, want 404 after failed resolution", resp.StatusCode)
	}
	if !strings.Contains(warnings.String(), "respond llm disabled") {
		t.Errorf("expected disable warning, got:\n%s", warnings.String())
	}

	submitResp := postSession(t, application.ReviewURL(), "api/session/submit", review.Token,
		`{"verdict":"approve","summary":"done"}`)
	submitResp.Body.Close()
	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
	payload := decodePayload(t, output.Bytes())
	if payload.Reformulated != nil {
		t.Errorf("unexpected reformulated section: %+v", payload.Reformulated)
	}
}

func TestReviewRespondExplicitModelResolvesAndWires(t *testing.T) {
	// Same config as above, but an explicit model override resolves cleanly
	// and wires the endpoint (a tokenless POST reaches the handler: 403).
	configPath := writeLLMConfig(t, `"hosted":{"type":"openai-compatible","base_url":"http://127.0.0.1:9/v1","models":["m-a","m-b"],"api_key":"test-key"}`)
	settings := llm.RespondSettings{Mode: llm.ModeAsk, ProviderName: "hosted", Model: "m-b", ConfigPath: configPath}
	application, _, result := startRespondReview(t, settings, nil)
	review := application.ReviewSession()

	resp := postSession(t, application.ReviewURL(), "api/session/reformulate", "", `{}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tokenless reformulate status = %d, want 403 (endpoint wired)", resp.StatusCode)
	}

	cancel := postSession(t, application.ReviewURL(), "api/session/submit", review.Token,
		`{"verdict":"approve","summary":"done"}`)
	cancel.Body.Close()
	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
}

func TestReviewRespondMissingCredentialEnvDisablesWithWarning(t *testing.T) {
	t.Setenv("TEST_MISSING_KEY_XYZ", "")
	os.Unsetenv("TEST_MISSING_KEY_XYZ")
	configPath := writeLLMConfig(t, `"hosted":{"type":"openai-compatible","base_url":"http://127.0.0.1:9/v1","model":"cfg-model","api_key_env":"TEST_MISSING_KEY_XYZ"}`)
	settings := llm.RespondSettings{Mode: llm.ModeAuto, ProviderName: "hosted", ConfigPath: configPath}
	warnings := captureLog(t)
	application, output, result := startRespondReview(t, settings, nil)

	resp := postSession(t, application.ReviewURL(), "api/session/submit", application.ReviewSession().Token,
		`{"verdict":"approve","summary":"done"}`)
	resp.Body.Close()
	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
	if !strings.Contains(warnings.String(), "TEST_MISSING_KEY_XYZ") {
		t.Errorf("warning should name the missing env var, got:\n%s", warnings.String())
	}
	payload := decodePayload(t, output.Bytes())
	if payload.Reformulated != nil {
		t.Errorf("disabled respond must not produce reformulated output: %+v", payload.Reformulated)
	}
}
