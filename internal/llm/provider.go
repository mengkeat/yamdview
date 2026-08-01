// Package llm provides an optional repair backend for blocks that
// deterministic heuristics cannot confidently fix (ambiguous ASCII equations,
// complex derivatives, malformed tables, and so on).
//
// The package is deliberately vendor-neutral: every concrete provider
// (OpenAI-compatible HTTP endpoints, local command providers, in-memory mocks)
// implements the small [Provider] interface, and every provider output is run
// through project-owned validation before it can affect rendering or source
// files. The layer proposes; yamdview validates and decides.
//
// The package depends only on the Go standard library so that providers stay
// interchangeable and no SDK is hard-wired into the binary.
package llm

import (
	"context"
	"errors"
)

// RequestKind identifies the purpose of an LLM call. Each kind selects a
// prompt template and the response struct used for strict decoding.
type RequestKind string

const (
	// KindMathFix asks the model to convert a small local math candidate span
	// into Markdown with valid TeX delimiters.
	KindMathFix RequestKind = "math_fix"
	// KindTableFix asks the model to repair a single malformed Markdown table.
	KindTableFix RequestKind = "table_fix"
	// KindClassifyCandidate asks the model to classify whether a span is math.
	KindClassifyCandidate RequestKind = "classify"
	// KindFeedbackRephrase asks the model to consolidate review feedback into a
	// single instruction. Reserved for the agent review mode (later phases).
	KindFeedbackRephrase RequestKind = "feedback_rephrase"
)

// Provider is a single LLM backend. Implementations must honor the supplied
// context: cancellation must abort an in-flight call and return promptly, so
// that shutting down the viewer or editing the document again can invalidate a
// pending repair.
type Provider interface {
	// Name is a stable identifier for diagnostics and audit records.
	Name() string
	// Complete performs a single completion request. The returned Response.Text
	// is the raw model text; callers are responsible for strict decoding and
	// semantic validation before trusting any of it.
	Complete(ctx context.Context, req Request) (Response, error)
}

// Request describes a single completion call. Fields are provider-agnostic;
// concrete providers translate them into their native request format.
type Request struct {
	// Kind selects the prompt template and response contract.
	Kind RequestKind
	// SystemPrompt is the provider's system/instruction message.
	SystemPrompt string
	// UserPrompt is the provider's user message, already rendered from a
	// template with the candidate span and diagnostics substituted in.
	UserPrompt string
	// Temperature biases the provider toward more or less deterministic
	// output. Zero leaves the provider default in place.
	Temperature float64
	// MaxTokens caps the response length. Zero leaves the provider default.
	MaxTokens int
	// ResponseSchema is a human-readable description of the required JSON
	// shape. It is included verbatim in the prompt; providers that support a
	// native structured-output mode may also derive constraints from it.
	ResponseSchema string
	// Metadata carries opaque per-call context (block id, request hash) for
	// diagnostics. Providers must not interpret its contents.
	Metadata map[string]string
}

// Usage reports token accounting when the provider surfaces it. All-zero
// values are valid when a provider does not report usage.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// FinishReason describes why a completion stopped.
type FinishReason string

const (
	// FinishStop means the model produced a complete response.
	FinishStop FinishReason = "stop"
	// FinishLength means the response was truncated by the token limit.
	FinishLength FinishReason = "length"
	// FinishContentFilter means the provider blocked the response.
	FinishContentFilter FinishReason = "content_filter"
	// FinishToolCall means the model requested a tool call.
	FinishToolCall FinishReason = "tool_call"
	// FinishUnknown means the provider did not report a finish reason.
	FinishUnknown FinishReason = "unknown"
)

// Response is the raw, untrusted result of a completion call.
type Response struct {
	// Text is the raw model output (before any JSON decoding).
	Text string
	// Raw is the provider's full response body, kept for debug hashing.
	Raw []byte
	// Usage reports token usage when available.
	Usage Usage
	// Model is the concrete model name reported by the provider.
	Model string
	// Finish describes why the completion stopped.
	Finish FinishReason
}

// ErrProvider is the sentinel base for provider call failures. Concrete
// providers wrap domain-specific errors (timeouts, oversized output, HTTP
// errors) with [errors.Join] or %w so callers can detect categories.
var ErrProvider = errors.New("llm provider error")
