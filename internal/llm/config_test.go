package llm

import (
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		in   string
		want Mode
		err  bool
	}{
		{"", ModeOff, false},
		{"off", ModeOff, false},
		{"ASK", ModeAsk, false},
		{"auto", ModeAuto, false},
		{"bogus", "", true},
	}
	for _, tt := range tests {
		got, err := ParseMode(tt.in)
		if tt.err {
			if err == nil {
				t.Errorf("ParseMode(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("ParseMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseConfigValid(t *testing.T) {
	doc := `{
  "providers": {
    "local": {
      "type": "openai-compatible",
      "base_url": "http://127.0.0.1:11434/v1",
      "model": "qwen",
      "timeout": "20s",
      "max_tokens": 1200,
      "temperature": 0.2
    },
    "script": {
      "type": "command",
      "command": ["./scripts/fix"],
      "timeout": 15,
      "max_bytes": 200000
    }
  }
}`
	cfg, err := ParseConfig([]byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(cfg.Providers))
	}
	local := cfg.Providers["local"]
	if local.Type != ProviderTypeOpenAI || local.Model != "qwen" {
		t.Errorf("unexpected local provider: %+v", local)
	}
	if local.Timeout.Std().String() != "20s" {
		t.Errorf("timeout = %v, want 20s", local.Timeout.Std())
	}
	if local.MaxTokens != 1200 {
		t.Errorf("max_tokens = %d", local.MaxTokens)
	}
	script := cfg.Providers["script"]
	if script.Type != ProviderTypeCommand || len(script.Command) != 1 {
		t.Errorf("unexpected script provider: %+v", script)
	}
	if script.Timeout.Std() != 15_000_000_000 {
		t.Errorf("numeric timeout = %v nanos, want 15s", script.Timeout.Std())
	}
}

func TestParseConfigRejectsUnknownFields(t *testing.T) {
	doc := `{"providers":{"x":{"type":"openai-compatible","bogus":1}}}`
	if _, err := ParseConfig([]byte(doc)); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestParseConfigRejectsBadJSON(t *testing.T) {
	if _, err := ParseConfig([]byte(`{not json`)); err == nil {
		t.Fatal("expected error for malformed json")
	}
}

func TestParseConfigFile(t *testing.T) {
	doc := `{"providers":{"x":{"type":"openai-compatible","model":"m","base_url":"http://x/v1"}}}`
	cfg, err := ParseConfigFile("cfg.json", func(string) ([]byte, error) { return []byte(doc), nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.Providers["x"]; !ok {
		t.Fatal("expected provider x")
	}

	if _, err := ParseConfigFile("missing.json", func(string) ([]byte, error) { return nil, errRead }); err == nil {
		t.Fatal("expected error on read failure")
	}
}

func TestBuildProvider(t *testing.T) {
	cfg := Config{Providers: map[string]ProviderConfig{
		"http":    {Type: ProviderTypeOpenAI, BaseURL: "http://x/v1", Model: "m"},
		"cmd":     {Type: ProviderTypeCommand, Command: []string{"./fix"}},
		"badcmd":  {Type: ProviderTypeCommand},
		"unknown": {Type: ProviderType("weird")},
	}}

	if _, err := BuildProvider(cfg, "missing"); err == nil {
		t.Fatal("expected error for missing provider")
	}
	if p, err := BuildProvider(cfg, "http"); err != nil {
		t.Fatalf("http: %v", err)
	} else if _, ok := p.(*OpenAI); !ok {
		t.Fatalf("expected *OpenAI, got %T", p)
	}
	if p, err := BuildProvider(cfg, "cmd"); err != nil {
		t.Fatalf("cmd: %v", err)
	} else if _, ok := p.(*Command); !ok {
		t.Fatalf("expected *Command, got %T", p)
	}
	if _, err := BuildProvider(cfg, "badcmd"); err == nil {
		t.Fatal("expected error for command without command")
	}
	if _, err := BuildProvider(cfg, "unknown"); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestParseLocalProfile(t *testing.T) {
	for _, s := range []string{"ollama", "lm-studio", "llama.cpp", "command", "OLLAMA"} {
		if _, err := ParseLocalProfile(s); err != nil {
			t.Errorf("ParseLocalProfile(%q): %v", s, err)
		}
	}
	if _, err := ParseLocalProfile("grok"); err == nil {
		t.Fatal("expected error for unknown profile")
	}
}

func TestNewLocalProviderOllama(t *testing.T) {
	p, err := NewLocalProvider(ProfileOllama, "", "qwen")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o, ok := p.(*OpenAI)
	if !ok {
		t.Fatalf("expected *OpenAI, got %T", p)
	}
	if o.cfg.BaseURL != OllamaBaseURL {
		t.Errorf("base url = %q, want %q", o.cfg.BaseURL, OllamaBaseURL)
	}
	if o.cfg.Model != "qwen" {
		t.Errorf("model = %q", o.cfg.Model)
	}
}

func TestNewLocalProviderLMStudioOverridesURL(t *testing.T) {
	p, err := NewLocalProvider(ProfileLMStudio, "http://127.0.0.1:9999/v1", "m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o := p.(*OpenAI)
	if o.cfg.BaseURL != "http://127.0.0.1:9999/v1" {
		t.Errorf("base url = %q", o.cfg.BaseURL)
	}
}

func TestNewLocalProviderLlamaCPPRequiresURL(t *testing.T) {
	if _, err := NewLocalProvider(ProfileLlamaCPP, "", "m"); err == nil {
		t.Fatal("expected error for missing url")
	}
	p, err := NewLocalProvider(ProfileLlamaCPP, "http://127.0.0.1:8080/v1", "m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.(*OpenAI).cfg.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Error("unexpected base url")
	}
}

func TestNewLocalProviderCommandErrors(t *testing.T) {
	_, err := NewLocalProvider(ProfileCommand, "", "")
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("expected config-resolution error, got %v", err)
	}
}

var errRead = &readErr{}

type readErr struct{}

func (*readErr) Error() string { return "read failed" }
