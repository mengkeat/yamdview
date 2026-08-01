package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// openaiProvider builds a provider pointing at the test server with a short
// client timeout so cancellation tests do not stall.
func openaiProvider(t *testing.T, server *httptest.Server, cfg OpenAIConfig) *OpenAI {
	t.Helper()
	cfg.BaseURL = server.URL
	if cfg.Timeout == 0 {
		cfg.Timeout = 200 * time.Millisecond
	}
	return NewOpenAI(cfg)
}

func readReqBody(t *testing.T, r *http.Request) openAIRequest {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body openAIRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return body
}

func TestOpenAICompleteSuccess(t *testing.T) {
	var gotReq openAIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("unexpected content-type %q", ct)
		}
		gotReq = readReqBody(t, r)
		_ = json.NewEncoder(w).Encode(openAIResponse{
			Model: "fake-model",
			Choices: []openAIChoice{{
				Message:      openAIMessage{Role: "assistant", Content: "the fix"},
				FinishReason: "stop",
			}},
			Usage: &openAIUsage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7},
		})
	}))
	defer server.Close()

	prov := openaiProvider(t, server, OpenAIConfig{Name: "fake", Model: "fake-model"})
	resp, err := prov.Complete(context.Background(), Request{
		Kind:         KindMathFix,
		SystemPrompt: "you are a repair tool",
		UserPrompt:   "fix x^2",
		Temperature:  0.1,
		MaxTokens:    100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "the fix" {
		t.Errorf("got text %q", resp.Text)
	}
	if resp.Model != "fake-model" {
		t.Errorf("got model %q", resp.Model)
	}
	if resp.Finish != FinishStop {
		t.Errorf("got finish %q", resp.Finish)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Errorf("got total tokens %d", resp.Usage.TotalTokens)
	}
	if len(gotReq.Messages) != 2 || gotReq.Messages[0].Role != "system" || gotReq.Messages[1].Content != "fix x^2" {
		t.Errorf("unexpected messages: %+v", gotReq.Messages)
	}
	if gotReq.Model != "fake-model" || gotReq.MaxTokens != 100 || gotReq.Temperature != 0.1 {
		t.Errorf("unexpected request: %+v", gotReq)
	}
}

func TestOpenAIHTTPErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	prov := openaiProvider(t, server, OpenAIConfig{Name: "fake", Model: "m"})
	_, err := prov.Complete(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "http 500") {
		t.Fatalf("expected http 500 error, got %v", err)
	}
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("expected wrapped ErrProvider, got %v", err)
	}
}

func TestOpenAIMalformedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer server.Close()

	prov := openaiProvider(t, server, OpenAIConfig{Name: "fake", Model: "m"})
	_, err := prov.Complete(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestOpenAINoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(openAIResponse{Choices: nil})
	}))
	defer server.Close()

	prov := openaiProvider(t, server, OpenAIConfig{Name: "fake", Model: "m"})
	_, err := prov.Complete(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("expected no-choices error, got %v", err)
	}
}

func TestOpenAIMissingBaseURL(t *testing.T) {
	prov := NewOpenAI(OpenAIConfig{Name: "fake", Model: "m"})
	if _, err := prov.Complete(context.Background(), Request{}); !errors.Is(err, ErrProvider) {
		t.Fatalf("expected ErrProvider, got %v", err)
	}
}

func TestOpenAIMissingModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	prov := openaiProvider(t, server, OpenAIConfig{Name: "fake"})
	if _, err := prov.Complete(context.Background(), Request{}); err == nil {
		t.Fatal("expected error for missing model")
	}
}

func TestOpenAIAPIKeyFromEnv(t *testing.T) {
	t.Setenv("TEST_OPENAI_KEY", "secret-key")
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(openAIResponse{Choices: []openAIChoice{{}}})
	}))
	defer server.Close()

	prov := openaiProvider(t, server, OpenAIConfig{Name: "fake", Model: "m", APIKeyEnv: "TEST_OPENAI_KEY"})
	if _, err := prov.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth != "Bearer secret-key" {
		t.Errorf("expected bearer auth, got %q", auth)
	}
}

func TestOpenAINoAuthWhenNoKey(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(openAIResponse{Choices: []openAIChoice{{}}})
	}))
	defer server.Close()

	prov := openaiProvider(t, server, OpenAIConfig{Name: "fake", Model: "m"})
	if _, err := prov.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth != "" {
		t.Errorf("expected no auth header, got %q", auth)
	}
}

func TestOpenAITimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(openAIResponse{Choices: []openAIChoice{{}}})
	}))
	defer server.Close()

	cfg := OpenAIConfig{Name: "slow", Model: "m", Timeout: 10 * time.Millisecond}
	prov := openaiProvider(t, server, cfg)
	_, err := prov.Complete(context.Background(), Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestOpenAICancellation(t *testing.T) {
	var mu sync.Mutex
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		close(started)
		mu.Unlock()
		time.Sleep(500 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(openAIResponse{Choices: []openAIChoice{{}}})
	}))
	defer server.Close()

	prov := openaiProvider(t, server, OpenAIConfig{Name: "cancellable", Model: "m", Timeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := prov.Complete(ctx, Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected Canceled, got %v", err)
	}
}

func TestOpenAIDefaultsApplied(t *testing.T) {
	var gotReq openAIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = readReqBody(t, r)
		_ = json.NewEncoder(w).Encode(openAIResponse{Choices: []openAIChoice{{}}})
	}))
	defer server.Close()

	prov := openaiProvider(t, server, OpenAIConfig{Name: "fake", Model: "m", Temperature: 0.3, MaxTokens: 256})
	if _, err := prov.Complete(context.Background(), Request{UserPrompt: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotReq.Temperature != 0.3 || gotReq.MaxTokens != 256 {
		t.Errorf("defaults not applied: %+v", gotReq)
	}
	// With a system prompt only one message plus user; here no system prompt so one user message.
	if len(gotReq.Messages) != 1 || gotReq.Messages[0].Content != "x" {
		t.Errorf("unexpected messages: %+v", gotReq.Messages)
	}
}

func TestMapFinishReason(t *testing.T) {
	tests := []struct {
		in   string
		want FinishReason
	}{
		{"stop", FinishStop},
		{"STOP", FinishStop},
		{"length", FinishLength},
		{"content_filter", FinishContentFilter},
		{"tool_calls", FinishToolCall},
		{"function_call", FinishToolCall},
		{"", FinishUnknown},
		{"nonsense", FinishUnknown},
	}
	for _, tt := range tests {
		if got := mapFinishReason(tt.in); got != tt.want {
			t.Errorf("mapFinishReason(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
