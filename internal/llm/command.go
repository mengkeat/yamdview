package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Defaults for command provider safety.
const (
	DefaultCommandTimeout  = 30 * time.Second
	DefaultCommandMaxBytes = 1 << 20 // 1 MiB
)

// CommandConfig describes a local command provider.
type CommandConfig struct {
	// Name is the provider name surfaced in diagnostics.
	Name string
	// Command is the program and its fixed arguments. Additional arguments
	// cannot be supplied per-call; the request is passed on stdin.
	Command []string
	// Timeout caps each call. Zero uses DefaultCommandTimeout.
	Timeout time.Duration
	// MaxBytes caps the accepted stdout size. Zero uses
	// DefaultCommandMaxBytes.
	MaxBytes int
}

// commandRunner executes the configured command with stdin and returns its
// raw stdout. The default implementation uses os/exec; tests inject a fake to
// exercise timeout and size limits deterministically.
type commandRunner interface {
	run(ctx context.Context, cfg CommandConfig, stdin []byte) ([]byte, error)
}

// Command is a [Provider] that delegates each completion to a local command.
// The request is sent as JSON on stdin; the command replies with JSON (or raw
// text) on stdout. Every call honors the supplied context and the configured
// timeout, and stdout is rejected when it exceeds MaxBytes.
type Command struct {
	cfg    CommandConfig
	runner commandRunner
}

// NewCommand creates a command provider using the real os/exec runner.
func NewCommand(cfg CommandConfig) *Command {
	return &Command{cfg: cfg, runner: execRunner{}}
}

// newCommandWithRunner creates a command provider with an injected runner,
// for tests.
func newCommandWithRunner(cfg CommandConfig, runner commandRunner) *Command {
	return &Command{cfg: cfg, runner: runner}
}

// Name implements [Provider].
func (c *Command) Name() string {
	if c.cfg.Name != "" {
		return c.cfg.Name
	}
	return "command"
}

// Complete implements [Provider].
func (c *Command) Complete(ctx context.Context, req Request) (Response, error) {
	if len(c.cfg.Command) == 0 {
		return Response{}, fmt.Errorf("%w: command provider %q has no command", ErrProvider, c.Name())
	}

	envelope, err := json.Marshal(commandRequest{
		Kind:           string(req.Kind),
		SystemPrompt:   req.SystemPrompt,
		UserPrompt:     req.UserPrompt,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		ResponseSchema: req.ResponseSchema,
		Metadata:       req.Metadata,
	})
	if err != nil {
		return Response{}, fmt.Errorf("%w: marshal command request: %w", ErrProvider, err)
	}

	callCtx := ctx
	timeout := c.cfg.Timeout
	if timeout == 0 {
		timeout = DefaultCommandTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	stdout, err := c.runner.run(callCtx, c.cfg, envelope)
	if err != nil {
		return Response{}, c.wrapRunErr(err)
	}

	maxBytes := c.cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultCommandMaxBytes
	}
	if len(stdout) > maxBytes {
		return Response{}, fmt.Errorf("%w: command output %d bytes exceeds limit %d", ErrProvider, len(stdout), maxBytes)
	}

	return parseCommandResponse(stdout), nil
}

// wrapRunErr maps runner failures to provider errors while preserving
// context-cancellation categories (DeadlineExceeded / Canceled) so callers can
// classify timeouts and cancellation.
func (c *Command) wrapRunErr(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: command timed out: %w", ErrProvider, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: command cancelled: %w", ErrProvider, context.Canceled)
	default:
		return fmt.Errorf("%w: command %q failed: %v", ErrProvider, c.cfg.Command[0], err)
	}
}

// parseCommandResponse decodes the command's stdout envelope. If stdout is not
// valid JSON it is treated as raw response text so simple scripts that print
// text directly remain usable.
func parseCommandResponse(stdout []byte) Response {
	var resp commandResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &resp); err != nil {
		return Response{Text: string(stdout), Finish: FinishUnknown, Raw: stdout}
	}
	out := Response{
		Text:   resp.Text,
		Model:  resp.Model,
		Finish: resp.Finish,
		Raw:    stdout,
	}
	if resp.Finish == "" {
		out.Finish = FinishUnknown
	}
	if resp.Usage != nil {
		out.Usage = Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}
	return out
}

// commandRequest is the JSON envelope sent to the command on stdin.
type commandRequest struct {
	Kind           string            `json:"kind"`
	SystemPrompt   string            `json:"system_prompt"`
	UserPrompt     string            `json:"user_prompt"`
	Temperature    float64           `json:"temperature,omitempty"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	ResponseSchema string            `json:"response_schema,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// commandResponse is the JSON envelope expected on stdout.
type commandResponse struct {
	Text   string        `json:"text"`
	Model  string        `json:"model,omitempty"`
	Finish FinishReason  `json:"finish,omitempty"`
	Usage  *commandUsage `json:"usage,omitempty"`
}

type commandUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// execRunner is the production [commandRunner] backed by os/exec.
type execRunner struct{}

func (execRunner) run(ctx context.Context, cfg CommandConfig, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)

	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		return nil, fmt.Errorf("%s", msg)
	}
	return out.Bytes(), nil
}
