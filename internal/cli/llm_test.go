package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/llm"
)

func writeTempMD(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseLLMDefaultOff(t *testing.T) {
	cfg, err := Parse([]string{writeTempMD(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Mode != llm.ModeOff {
		t.Errorf("expected default ModeOff, got %q", cfg.LLM.Mode)
	}
}

func TestParseLLMAskRequiresProvider(t *testing.T) {
	_, err := Parse([]string{"--llm", "ask", writeTempMD(t)})
	if err == nil || !strings.Contains(err.Error(), "requires --llm-local or --llm-provider") {
		t.Fatalf("expected provider-required error, got %v", err)
	}
}

func TestParseLLMAutoWithLocalProfile(t *testing.T) {
	cfg, err := Parse([]string{"--llm", "auto", "--llm-local", "ollama", "--llm-model", "qwen", writeTempMD(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Mode != llm.ModeAuto {
		t.Errorf("got mode %q", cfg.LLM.Mode)
	}
	if cfg.LLM.LocalProfile != llm.ProfileOllama {
		t.Errorf("got profile %q", cfg.LLM.LocalProfile)
	}
	if cfg.LLM.Model != "qwen" {
		t.Errorf("got model %q", cfg.LLM.Model)
	}
	if !cfg.LLM.HasProviderSelection() {
		t.Error("expected provider selection")
	}
}

func TestParseLLMWithProviderName(t *testing.T) {
	cfg, err := Parse([]string{"--llm", "ask", "--llm-provider", "my-script", writeTempMD(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.ProviderName != "my-script" {
		t.Errorf("got provider %q", cfg.LLM.ProviderName)
	}
}

func TestParseLLMLocalURLAndTimeout(t *testing.T) {
	cfg, err := Parse([]string{
		"--llm", "ask",
		"--llm-local", "llama.cpp",
		"--llm-local-url", "http://127.0.0.1:8080/v1",
		"--llm-timeout", "15s",
		writeTempMD(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.LocalURL != "http://127.0.0.1:8080/v1" {
		t.Errorf("got local url %q", cfg.LLM.LocalURL)
	}
	if cfg.LLM.Timeout != 15*time.Second {
		t.Errorf("got timeout %s", cfg.LLM.Timeout)
	}
}

func TestParseLLMRejectsInvalidMode(t *testing.T) {
	_, err := Parse([]string{"--llm", "yolo", "--llm-local", "ollama", writeTempMD(t)})
	if err == nil || !strings.Contains(err.Error(), "--llm:") {
		t.Fatalf("expected --llm error, got %v", err)
	}
}

func TestParseLLMRejectsInvalidProfile(t *testing.T) {
	_, err := Parse([]string{"--llm", "ask", "--llm-local", "grok", writeTempMD(t)})
	if err == nil || !strings.Contains(err.Error(), "--llm-local:") {
		t.Fatalf("expected --llm-local error, got %v", err)
	}
}

func TestParseLLMConfigPathMustExist(t *testing.T) {
	_, err := Parse([]string{
		"--llm", "ask", "--llm-provider", "p",
		"--llm-config", filepath.Join(t.TempDir(), "missing.json"),
		writeTempMD(t),
	})
	if err == nil || !strings.Contains(err.Error(), "llm config") {
		t.Fatalf("expected llm config error, got %v", err)
	}
}

func TestParseLLMConfigRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := Parse([]string{"--llm", "ask", "--llm-provider", "p", "--llm-config", dir, writeTempMD(t)})
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestParseLLMConfigAcceptsFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"providers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse([]string{"--llm", "ask", "--llm-provider", "p", "--llm-config", cfgPath, writeTempMD(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.ConfigPath != cfgPath {
		t.Errorf("got config path %q", cfg.LLM.ConfigPath)
	}
}
