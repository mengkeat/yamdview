package llm

import (
	"strings"
	"testing"
	"time"
)

func TestParseRespondMode(t *testing.T) {
	tests := []struct {
		in   string
		want Mode
		err  bool
	}{
		{"", ModeOff, false},
		{"off", ModeOff, false},
		{"ask", ModeAsk, false},
		{"AUTO", ModeAuto, false},
		{"bogus", "", true},
	}
	for _, tt := range tests {
		got, err := ParseRespondMode(tt.in)
		if tt.err {
			if err == nil {
				t.Errorf("ParseRespondMode(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRespondMode(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("ParseRespondMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRespondSettingsHasProviderSelection(t *testing.T) {
	var s RespondSettings
	if s.HasProviderSelection() {
		t.Error("zero settings should not select a provider")
	}
	s.ProviderName = "zai"
	if !s.HasProviderSelection() {
		t.Error("named provider should select a provider")
	}
}

func respondTestConfig() Config {
	return Config{Providers: map[string]ProviderConfig{
		"zai":        {Type: ProviderTypeOpenAI, BaseURL: "https://api.z.ai/api/paas/v4", Model: "glm-4.7", APIKeyEnv: "ZAI_API_KEY"},
		"openrouter": {Type: ProviderTypeOpenAI, BaseURL: "https://openrouter.ai/api/v1", Model: "anthropic/claude-sonnet-4", Models: []string{"anthropic/claude-sonnet-4", "z-ai/glm-4.7"}, APIKeyEnv: "OPENROUTER_API_KEY"},
		"openai":     {Type: ProviderTypeOpenAI, BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini", APIKeyEnv: "OPENAI_API_KEY"},
		"local":      {Type: ProviderTypeOpenAI, BaseURL: OllamaBaseURL, Model: "qwen"},
		"script":     {Type: ProviderTypeCommand, Command: []string{"./scripts/respond"}},
	}}
}

func TestResolveRespondProviderOff(t *testing.T) {
	p, model, err := ResolveRespondProvider(respondTestConfig(), RespondSettings{Mode: ModeOff})
	if p != nil || model != "" || err != nil {
		t.Fatalf("expected (nil, \"\", nil), got (%v, %q, %v)", p, model, err)
	}
}

func TestResolveRespondProviderRequiresProvider(t *testing.T) {
	_, _, err := ResolveRespondProvider(respondTestConfig(), RespondSettings{Mode: ModeAsk})
	if err == nil || !strings.Contains(err.Error(), "--respond-provider") {
		t.Fatalf("expected --respond-provider error, got %v", err)
	}
}

func TestResolveRespondProviderUnknownProvider(t *testing.T) {
	_, _, err := ResolveRespondProvider(respondTestConfig(), RespondSettings{Mode: ModeAsk, ProviderName: "nope"})
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("expected unknown-provider error, got %v", err)
	}
}

func TestResolveRespondProviderMissingModel(t *testing.T) {
	cfg := Config{Providers: map[string]ProviderConfig{
		"bare": {Type: ProviderTypeOpenAI, BaseURL: "http://x/v1"},
	}}
	_, _, err := ResolveRespondProvider(cfg, RespondSettings{Mode: ModeAsk, ProviderName: "bare"})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("expected missing-model error, got %v", err)
	}
	// CLI model fills the gap.
	p, model, err := ResolveRespondProvider(cfg, RespondSettings{Mode: ModeAsk, ProviderName: "bare", Model: "m1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "m1" {
		t.Errorf("resolved model = %q, want m1", model)
	}
	if o := p.(*OpenAI); o.cfg.Model != "m1" {
		t.Errorf("provider model = %q, want m1", o.cfg.Model)
	}
}

func TestResolveRespondProviderCLIModelOverridesConfigured(t *testing.T) {
	cfg := respondTestConfig()
	t.Setenv("ZAI_API_KEY", "test-key")
	p, model, err := ResolveRespondProvider(cfg, RespondSettings{Mode: ModeAsk, ProviderName: "zai", Model: "glm-4.6"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "glm-4.6" {
		t.Errorf("resolved model = %q, want glm-4.6", model)
	}
	o := p.(*OpenAI)
	if o.cfg.Model != "glm-4.6" {
		t.Errorf("provider model = %q, want glm-4.6", o.cfg.Model)
	}
	if o.Name() != "zai" || o.cfg.BaseURL != cfg.Providers["zai"].BaseURL {
		t.Errorf("unexpected provider identity: %+v", o.cfg)
	}
	// Original provider config is untouched.
	if cfg.Providers["zai"].Model != "glm-4.7" {
		t.Error("config model was mutated by override")
	}
}

func TestResolveRespondProviderUsesConfiguredDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	_, model, err := ResolveRespondProvider(respondTestConfig(), RespondSettings{Mode: ModeAuto, ProviderName: "openai"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "gpt-4o-mini" {
		t.Errorf("resolved model = %q, want gpt-4o-mini", model)
	}
}

func TestResolveRespondProviderModelsListOnlyNeedsExplicitModel(t *testing.T) {
	// A provider configured with only a models list has no default model, so
	// resolution errors until a model is picked (e.g. from ModelChoices).
	cfg := Config{Providers: map[string]ProviderConfig{
		"openrouter": {Type: ProviderTypeOpenAI, BaseURL: "https://openrouter.ai/api/v1", Models: []string{"anthropic/claude-sonnet-4", "z-ai/glm-4.7"}, APIKeyEnv: "OPENROUTER_API_KEY"},
	}}
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	if _, _, err := ResolveRespondProvider(cfg, RespondSettings{Mode: ModeAsk, ProviderName: "openrouter"}); err == nil {
		t.Fatal("expected missing-model error for models-list-only provider")
	}
	pick := cfg.Providers["openrouter"].ModelChoices()[0]
	p, model, err := ResolveRespondProvider(cfg, RespondSettings{Mode: ModeAsk, ProviderName: "openrouter", Model: pick})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != pick {
		t.Errorf("resolved model = %q, want %q", model, pick)
	}
	if o := p.(*OpenAI); o.cfg.Model != pick {
		t.Errorf("provider model = %q, want %q", o.cfg.Model, pick)
	}
	if cfg.Providers["openrouter"].Model != "" {
		t.Error("config was mutated by override")
	}
}

func TestResolveRespondProviderNamedEntries(t *testing.T) {
	cfg := respondTestConfig()
	tests := []struct {
		name  string
		env   string
		model string
	}{
		{"zai", "ZAI_API_KEY", "glm-4.7"},
		{"openrouter", "OPENROUTER_API_KEY", "anthropic/claude-sonnet-4"},
		{"openai", "OPENAI_API_KEY", "gpt-4o-mini"},
		{"local", "", "qwen"},
	}
	for _, tt := range tests {
		if tt.env != "" {
			t.Setenv(tt.env, "test-key")
		}
		p, model, err := ResolveRespondProvider(cfg, RespondSettings{Mode: ModeAsk, ProviderName: tt.name})
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
			continue
		}
		if model != tt.model {
			t.Errorf("%s: resolved model = %q, want %q", tt.name, model, tt.model)
		}
		if _, ok := p.(*OpenAI); !ok {
			t.Errorf("%s: expected *OpenAI, got %T", tt.name, p)
		}
	}
}

func TestRequireAPIKeyEnv(t *testing.T) {
	pc := ProviderConfig{Type: ProviderTypeOpenAI, APIKeyEnv: "RESPOND_TEST_KEY"}

	// Unset fails and names the variable without echoing contents.
	if err := RequireAPIKeyEnv(pc); err == nil || !strings.Contains(err.Error(), "RESPOND_TEST_KEY") {
		t.Fatalf("expected error naming RESPOND_TEST_KEY, got %v", err)
	} else if strings.Contains(strings.ToLower(err.Error()), "test-value") {
		t.Errorf("error must never echo env contents: %v", err)
	}

	// Empty value also fails.
	t.Setenv("RESPOND_TEST_KEY", "")
	if err := RequireAPIKeyEnv(pc); err == nil || !strings.Contains(err.Error(), "RESPOND_TEST_KEY") {
		t.Fatalf("expected error for empty env value, got %v", err)
	}

	// Set passes.
	t.Setenv("RESPOND_TEST_KEY", "test-value")
	if err := RequireAPIKeyEnv(pc); err != nil {
		t.Errorf("unexpected error with env set: %v", err)
	}

	// Keyless local providers pass.
	keyless := ProviderConfig{Type: ProviderTypeOpenAI, BaseURL: OllamaBaseURL, Model: "qwen"}
	if err := RequireAPIKeyEnv(keyless); err != nil {
		t.Errorf("keyless provider should pass, got %v", err)
	}

	// Explicit api_key makes the env var unnecessary.
	withKey := pc
	withKey.APIKey = "explicit"
	if err := RequireAPIKeyEnv(withKey); err != nil {
		t.Errorf("explicit key should pass, got %v", err)
	}

	// Command providers pass regardless.
	cmd := ProviderConfig{Type: ProviderTypeCommand, Command: []string{"./fix"}}
	if err := RequireAPIKeyEnv(cmd); err != nil {
		t.Errorf("command provider should pass, got %v", err)
	}
}

func TestResolveRespondProviderCredentialSafety(t *testing.T) {
	cfg := Config{Providers: map[string]ProviderConfig{
		"hosted": {Type: ProviderTypeOpenAI, BaseURL: "https://api.example.com/v1", Model: "m", APIKeyEnv: "RESPOND_HOSTED_KEY"},
	}}

	// Missing credential fails before any endpoint could be called.
	if _, _, err := ResolveRespondProvider(cfg, RespondSettings{Mode: ModeAsk, ProviderName: "hosted"}); err == nil {
		t.Fatal("expected credential error for unset api_key_env")
	}

	t.Setenv("RESPOND_HOSTED_KEY", "test-key")
	p, _, err := ResolveRespondProvider(cfg, RespondSettings{Mode: ModeAsk, ProviderName: "hosted"})
	if err != nil {
		t.Fatalf("unexpected error with credential set: %v", err)
	}
	if _, ok := p.(*OpenAI); !ok {
		t.Errorf("expected *OpenAI, got %T", p)
	}
}

func TestResolveRespondProviderCommandIgnoresModelOverride(t *testing.T) {
	p, model, err := ResolveRespondProvider(
		respondTestConfig(),
		RespondSettings{Mode: ModeAsk, ProviderName: "script", Model: "some-model"},
	)
	if err != nil {
		t.Fatalf("command provider must not error on model override, got %v", err)
	}
	if _, ok := p.(*Command); !ok {
		t.Fatalf("expected *Command, got %T", p)
	}
	// Resolved model reports the CLI choice even though the command ignores it.
	if model != "some-model" {
		t.Errorf("resolved model = %q, want some-model", model)
	}
}

func TestRespondSettingsTimeoutCarried(t *testing.T) {
	s := RespondSettings{Mode: ModeAsk, Timeout: 45 * time.Second}
	if s.Timeout != 45*time.Second {
		t.Errorf("timeout = %v", s.Timeout)
	}
}
