package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultOpenAITimeout caps a single OpenAI-compatible call when neither the
// request context nor the config supplies a shorter deadline.
const DefaultOpenAITimeout = 30 * time.Second

// OpenAIConfig describes an OpenAI-compatible chat completions endpoint.
type OpenAIConfig struct {
	// Name is the provider name surfaced in diagnostics.
	Name string
	// BaseURL is the API root ending in "/v1", e.g.
	// "http://127.0.0.1:11434/v1" for Ollama.
	BaseURL string
	// Model is the model id sent in the request.
	Model string
	// APIKeyEnv names the environment variable holding the API key. Empty
	// means no authentication header is sent (suitable for local servers).
	APIKeyEnv string
	// APIKey is an explicit key that takes precedence over APIKeyEnv.
	APIKey string
	// Timeout caps each call. Zero uses DefaultOpenAITimeout.
	Timeout time.Duration
	// MaxTokens is used when a Request does not set MaxTokens. Zero leaves
	// the provider default.
	MaxTokens int
	// Temperature is used when a Request does not set Temperature.
	Temperature float64
}

// OpenAI is a [Provider] for any OpenAI-compatible /v1/chat/completions
// endpoint. It uses only the standard library so no provider SDK is tied into
// the binary.
type OpenAI struct {
	cfg    OpenAIConfig
	client *http.Client
}

// NewOpenAI creates an OpenAI-compatible provider with a default HTTP client.
func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultOpenAITimeout
	}
	return &OpenAI{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

// Name implements [Provider].
func (o *OpenAI) Name() string {
	if o.cfg.Name != "" {
		return o.cfg.Name
	}
	return "openai-compatible"
}

// Complete implements [Provider].
func (o *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	if strings.TrimSpace(o.cfg.BaseURL) == "" {
		return Response{}, fmt.Errorf("%w: openai provider %q has no base url", ErrProvider, o.Name())
	}
	if strings.TrimSpace(o.cfg.Model) == "" {
		return Response{}, fmt.Errorf("%w: openai provider %q has no model", ErrProvider, o.Name())
	}

	body, err := json.Marshal(o.buildPayload(req))
	if err != nil {
		return Response{}, fmt.Errorf("%w: marshal request: %w", ErrProvider, err)
	}

	url := strings.TrimRight(o.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("%w: build request: %w", ErrProvider, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := o.apiKey(); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return Response{}, o.wrapHTTPErr(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenAIResponseBytes))
	if err != nil {
		return Response{}, fmt.Errorf("%w: read response: %w", ErrProvider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("%w: http %d: %s", ErrProvider, resp.StatusCode, truncate(string(raw), 200))
	}

	parsed, err := parseOpenAIResponse(raw)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	parsed.Model = effectiveModel(parsed.Model, o.cfg.Model)
	return parsed, nil
}

// maxOpenAIResponseBytes caps the response body read into memory.
const maxOpenAIResponseBytes = 4 << 20 // 4 MiB

// buildPayload assembles the chat completions request body from config and the
// per-call Request, applying config defaults for temperature and max tokens.
func (o *OpenAI) buildPayload(req Request) openAIRequest {
	temp := req.Temperature
	if temp == 0 {
		temp = o.cfg.Temperature
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = o.cfg.MaxTokens
	}
	messages := make([]openAIMessage, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: req.UserPrompt})
	return openAIRequest{
		Model:       o.cfg.Model,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   maxTokens,
		Stream:      false,
	}
}

// apiKey resolves the key from the explicit value then the configured env var.
func (o *OpenAI) apiKey() string {
	if o.cfg.APIKey != "" {
		return o.cfg.APIKey
	}
	if o.cfg.APIKeyEnv != "" {
		return os.Getenv(o.cfg.APIKeyEnv)
	}
	return ""
}

// wrapHTTPErr maps HTTP transport errors to provider errors, preserving
// context-cancellation categories.
func (o *OpenAI) wrapHTTPErr(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: timed out: %w", ErrProvider, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: cancelled: %w", ErrProvider, context.Canceled)
	default:
		return fmt.Errorf("%w: request failed: %v", ErrProvider, err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func effectiveModel(reported, fallback string) string {
	if strings.TrimSpace(reported) != "" {
		return reported
	}
	return fallback
}

// ── OpenAI request/response types ──────────────────────────────────────────

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// parseOpenAIResponse decodes an OpenAI chat completions response body.
func parseOpenAIResponse(raw []byte) (Response, error) {
	var body openAIResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if len(body.Choices) == 0 {
		return Response{Raw: raw}, fmt.Errorf("response has no choices")
	}
	out := Response{
		Text:   body.Choices[0].Message.Content,
		Model:  body.Model,
		Finish: mapFinishReason(body.Choices[0].FinishReason),
		Raw:    raw,
	}
	if body.Usage != nil {
		out.Usage = Usage{
			PromptTokens:     body.Usage.PromptTokens,
			CompletionTokens: body.Usage.CompletionTokens,
			TotalTokens:      body.Usage.TotalTokens,
		}
	}
	return out, nil
}

// mapFinishReason converts an OpenAI finish_reason string to a FinishReason.
func mapFinishReason(reason string) FinishReason {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop":
		return FinishStop
	case "length":
		return FinishLength
	case "content_filter":
		return FinishContentFilter
	case "tool_calls", "function_call":
		return FinishToolCall
	case "":
		return FinishUnknown
	default:
		return FinishUnknown
	}
}
