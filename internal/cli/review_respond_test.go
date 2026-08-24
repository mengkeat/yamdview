package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/llm"
)

func writeTempLLMConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llm-config.json")
	if err := os.WriteFile(path, []byte(`{"providers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseRespondDefaultOff(t *testing.T) {
	cfg, err := Parse([]string{string(ModeReview), writeTempMD(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Review.Respond.Mode != llm.ModeOff {
		t.Errorf("expected default respond ModeOff, got %q", cfg.Review.Respond.Mode)
	}
	if cfg.Review.Respond.ProviderName != "" || cfg.Review.Respond.Model != "" {
		t.Errorf("expected empty respond provider/model, got %+v", cfg.Review.Respond)
	}
}

func TestParseRespondAskWithoutProviderErrors(t *testing.T) {
	for _, mode := range []string{"ask", "auto"} {
		_, err := Parse([]string{string(ModeReview), "--respond-llm", mode, writeTempMD(t)})
		if err == nil || !strings.Contains(err.Error(), "requires --respond-provider") {
			t.Fatalf("--respond-llm %s: expected provider-required error, got %v", mode, err)
		}
	}
}

func TestParseRespondOnWithoutConfigErrors(t *testing.T) {
	_, err := Parse([]string{
		string(ModeReview), "--respond-llm", "ask", "--respond-provider", "p", writeTempMD(t),
	})
	if err == nil || !strings.Contains(err.Error(), "requires --llm-config") {
		t.Fatalf("expected --llm-config-required error, got %v", err)
	}
}

func TestParseRespondProviderAndModel(t *testing.T) {
	configPath := writeTempLLMConfig(t)
	cfg, err := Parse([]string{
		string(ModeReview),
		"--respond-llm", "auto",
		"--respond-provider", "hosted",
		"--respond-model", "model-x",
		"--llm-config", configPath,
		writeTempMD(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Review.Respond.Mode != llm.ModeAuto {
		t.Errorf("got respond mode %q", cfg.Review.Respond.Mode)
	}
	if cfg.Review.Respond.ProviderName != "hosted" {
		t.Errorf("got respond provider %q", cfg.Review.Respond.ProviderName)
	}
	if cfg.Review.Respond.Model != "model-x" {
		t.Errorf("got respond model %q", cfg.Review.Respond.Model)
	}
	if cfg.Review.Respond.ConfigPath != configPath {
		t.Errorf("got respond config path %q", cfg.Review.Respond.ConfigPath)
	}
	if cfg.Review.Respond.Timeout != 0 {
		t.Errorf("expected zero respond timeout by default, got %s", cfg.Review.Respond.Timeout)
	}
}

func TestParseRespondRejectsInvalidMode(t *testing.T) {
	_, err := Parse([]string{
		string(ModeReview), "--respond-llm", "yolo", "--respond-provider", "p",
		writeTempMD(t),
	})
	if err == nil || !strings.Contains(err.Error(), "--respond-llm:") {
		t.Fatalf("expected --respond-llm error, got %v", err)
	}
}

func TestParseRespondTimeoutFlagRespected(t *testing.T) {
	// The review timeout flag must stay independent of reformulation.
	cfg, err := Parse([]string{
		string(ModeReview),
		"--timeout", "5s",
		writeTempMD(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Review.Timeout != 5*time.Second {
		t.Errorf("got review timeout %s", cfg.Review.Timeout)
	}
	if cfg.Review.Respond.Timeout != 0 {
		t.Errorf("review timeout leaked into respond settings: %s", cfg.Review.Respond.Timeout)
	}
}

func TestParseViewModeDoesNotAcceptRespondFlags(t *testing.T) {
	// Respond flags are registered only in review mode; view-mode invocations
	// reject them exactly like the other review-only flags.
	_, err := Parse([]string{"--respond-llm", "auto", writeTempMD(t)})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("expected undefined-flag error in view mode, got %v", err)
	}
}
