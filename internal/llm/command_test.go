package llm

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeRunner is an injectable commandRunner for deterministic tests.
type fakeRunner struct {
	stdout     []byte
	err        error
	blockOnCtx bool
	lastStdin  []byte
	lastCfg    CommandConfig
	calls      int
}

func (f *fakeRunner) run(ctx context.Context, cfg CommandConfig, stdin []byte) ([]byte, error) {
	f.calls++
	f.lastStdin = stdin
	f.lastCfg = cfg
	if f.blockOnCtx {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.stdout, f.err
}

func TestCommandCompleteSuccess(t *testing.T) {
	envelope := `{"text":"hello","model":"fake-llm","finish":"stop",` +
		`"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	c := newCommandWithRunner(CommandConfig{Name: "fake", Command: []string{"prog"}}, &fakeRunner{stdout: []byte(envelope)})

	resp, err := c.Complete(context.Background(), Request{Kind: KindMathFix, UserPrompt: "fix"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hello" {
		t.Errorf("got text %q", resp.Text)
	}
	if resp.Model != "fake-llm" {
		t.Errorf("got model %q", resp.Model)
	}
	if resp.Finish != FinishStop {
		t.Errorf("got finish %q", resp.Finish)
	}
	if resp.Usage.TotalTokens != 5 {
		t.Errorf("got total tokens %d", resp.Usage.TotalTokens)
	}
}

func TestCommandCompleteRawTextFallback(t *testing.T) {
	c := newCommandWithRunner(CommandConfig{Name: "fake", Command: []string{"prog"}}, &fakeRunner{stdout: []byte("not json")})

	resp, err := c.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "not json" {
		t.Errorf("got text %q", resp.Text)
	}
	if resp.Finish != FinishUnknown {
		t.Errorf("expected FinishUnknown, got %q", resp.Finish)
	}
}

func TestCommandTimeout(t *testing.T) {
	runner := &fakeRunner{blockOnCtx: true}
	c := newCommandWithRunner(CommandConfig{
		Name:    "slow",
		Command: []string{"prog"},
		Timeout: 10 * time.Millisecond,
	}, runner)

	_, err := c.Complete(context.Background(), Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("expected wrapped ErrProvider, got %v", err)
	}
}

func TestCommandCancelled(t *testing.T) {
	runner := &fakeRunner{blockOnCtx: true}
	c := newCommandWithRunner(CommandConfig{
		Name:    "cancellable",
		Command: []string{"prog"},
		Timeout: time.Hour, // long; we cancel the parent instead
	}, runner)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	_, err := c.Complete(ctx, Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected Canceled, got %v", err)
	}
}

func TestCommandOutputSizeLimit(t *testing.T) {
	oversized := make([]byte, 100)
	c := newCommandWithRunner(CommandConfig{
		Name:     "verbose",
		Command:  []string{"prog"},
		MaxBytes: 50,
	}, &fakeRunner{stdout: oversized})

	_, err := c.Complete(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
}

func TestCommandEmptyCommand(t *testing.T) {
	c := newCommandWithRunner(CommandConfig{Name: "empty"}, &fakeRunner{})
	if _, err := c.Complete(context.Background(), Request{}); !errors.Is(err, ErrProvider) {
		t.Fatalf("expected ErrProvider, got %v", err)
	}
}

func TestCommandSendsRequestEnvelope(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"text":"ok"}`)}
	c := newCommandWithRunner(CommandConfig{Name: "fake", Command: []string{"prog"}}, runner)

	if _, err := c.Complete(context.Background(), Request{
		Kind:        KindTableFix,
		UserPrompt:  "repair",
		Temperature: 0.2,
		MaxTokens:   500,
		Metadata:    map[string]string{"block": "b1"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env commandRequest
	if err := json.Unmarshal(runner.lastStdin, &env); err != nil {
		t.Fatalf("stdin not valid envelope JSON: %v", err)
	}
	if env.Kind != "table_fix" || env.UserPrompt != "repair" || env.MaxTokens != 500 {
		t.Errorf("unexpected envelope: %+v", env)
	}
	if env.Metadata["block"] != "b1" {
		t.Errorf("metadata not forwarded: %+v", env.Metadata)
	}
}

func TestCommandNameFallback(t *testing.T) {
	c := newCommandWithRunner(CommandConfig{Command: []string{"prog"}}, &fakeRunner{})
	if got := c.Name(); got != "command" {
		t.Errorf("expected fallback name 'command', got %q", got)
	}
}

// TestCommandRealExecSmoke exercises the real os/exec runner end-to-end with a
// shell one-liner that prints a valid response envelope. Skipped when `sh` is
// not on PATH (e.g. some minimal Windows environments).
func TestCommandRealExecSmoke(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	script := `printf '%s\n' '{"text":"echoed","finish":"stop"}'`
	c := NewCommand(CommandConfig{Name: "sh-provider", Command: []string{"sh", "-c", script}})

	resp, err := c.Complete(context.Background(), Request{Kind: KindMathFix, UserPrompt: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "echoed" {
		t.Fatalf("expected text 'echoed', got %q", resp.Text)
	}
	if resp.Finish != FinishStop {
		t.Errorf("expected FinishStop, got %q", resp.Finish)
	}
}
