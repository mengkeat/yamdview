package llm

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// RespondSettings bundles the CLI flags that control feedback reformulation
// (Phase 13): whether it runs, and which configured provider/model to call.
// The zero value describes a fully disabled reformulation layer (ModeOff).
type RespondSettings struct {
	// Mode controls whether reformulation is off, asks before running, or
	// applies automatically. ModeOff means no provider is constructed.
	Mode Mode
	// ProviderName names a provider in the config file.
	ProviderName string
	// Model overrides the provider's configured default model.
	Model string
	// ConfigPath is the optional path to a JSON provider config file.
	ConfigPath string
	// Timeout caps each reformulation call. Zero leaves the provider default.
	Timeout time.Duration
}

// HasProviderSelection reports whether RespondSettings names a concrete
// provider. Used by the CLI to reject enabling reformulation without any
// provider to call.
func (s RespondSettings) HasProviderSelection() bool {
	return s.ProviderName != ""
}

// ParseRespondMode converts a CLI string to a Mode for the reformulation
// flags. It accepts exactly the same values as [ParseMode].
func ParseRespondMode(s string) (Mode, error) {
	m, err := ParseMode(s)
	if err != nil {
		return "", fmt.Errorf("invalid respond mode: %w", err)
	}
	return m, nil
}

// modelOverridable is implemented by providers whose model can be swapped for
// a copy of themselves. Command providers deliberately do not implement it:
// their model choice lives inside the external command, so a CLI model flag
// cannot change what they run.
type modelOverridable interface {
	withModel(model string) Provider
}

// ResolveRespondProvider constructs the provider described by RespondSettings,
// using cfg for named providers, and reports the resolved model. It returns
// (nil, "", nil) when reformulation is off ([ModeOff]), so callers can treat a
// nil provider as "disabled" uniformly.
//
// Resolved model precedence: an explicit CLI model (s.Model) overrides the
// provider's configured pc.Model; if both are empty, openai-compatible
// providers error because they need a concrete model id. Command providers may
// have an empty resolved model — their behavior is fully defined by their
// command — and never receive a model override; the CLI model, when set, is
// only echoed back as the resolved model for reporting.
func ResolveRespondProvider(cfg Config, s RespondSettings) (Provider, string, error) {
	if s.Mode == ModeOff {
		return nil, "", nil
	}
	if s.ProviderName == "" {
		return nil, "", fmt.Errorf("--respond-mode %q requires --respond-provider naming a provider in the config file", s.Mode)
	}
	pc, ok := cfg.Providers[s.ProviderName]
	if !ok {
		return nil, "", fmt.Errorf("--respond-provider %q not found in llm config", s.ProviderName)
	}

	resolved := pc.Model
	if s.Model != "" {
		resolved = s.Model
	}

	switch pc.Type {
	case ProviderTypeOpenAI:
		if strings.TrimSpace(resolved) == "" {
			return nil, "", fmt.Errorf("llm provider %q has no model: add \"model\" or \"models\" to its config entry or pass --respond-model", s.ProviderName)
		}
	case ProviderTypeCommand:
		// No model requirement; see ResolveRespondProvider docs.
	default:
		return nil, "", fmt.Errorf("--respond-provider %q has unknown type %q", s.ProviderName, pc.Type)
	}

	if err := RequireAPIKeyEnv(pc); err != nil {
		return nil, "", err
	}

	p, err := BuildProvider(cfg, s.ProviderName)
	if err != nil {
		return nil, "", err
	}
	if pc.Type == ProviderTypeOpenAI && s.Model != "" && s.Model != pc.Model {
		if mo, ok := p.(modelOverridable); ok {
			p = mo.withModel(s.Model)
		}
	}
	return p, resolved, nil
}

// RequireAPIKeyEnv verifies up front that an OpenAI-compatible provider's
// credential environment variable is set, so the reformulation path fails
// safely before calling hosted endpoints. It returns nil when no env var is
// required (keyless local servers), when an explicit api_key is configured, or
// for non-openai-compatible types. Error messages name the missing variable
// only; environment contents are never echoed back.
func RequireAPIKeyEnv(pc ProviderConfig) error {
	if pc.Type != ProviderTypeOpenAI || pc.APIKeyEnv == "" || pc.APIKey != "" {
		return nil
	}
	if v, ok := os.LookupEnv(pc.APIKeyEnv); !ok || strings.TrimSpace(v) == "" {
		return fmt.Errorf("llm provider type %q requires environment variable %s to be set with an API key", pc.Type, pc.APIKeyEnv)
	}
	return nil
}
