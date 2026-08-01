package llm

import (
	"fmt"
	"time"
)

// Settings bundles the CLI/provider-selection flags into a single struct that
// the CLI, app, and resolver all share. The zero value describes a fully
// disabled LLM layer (ModeOff).
type Settings struct {
	// Mode controls whether repair is off, asks before applying, or applies
	// automatically. ModeOff means no provider is constructed.
	Mode Mode
	// ProviderName names a provider in the config file.
	ProviderName string
	// LocalProfile selects a local HTTP/command shortcut profile.
	LocalProfile LocalProfile
	// LocalURL overrides the profile's default base URL (and is required for
	// llama.cpp).
	LocalURL string
	// Model overrides the configured/profile model.
	Model string
	// ConfigPath is the optional path to a JSON provider config file.
	ConfigPath string
	// Timeout caps each repair call. Zero leaves the provider default.
	Timeout time.Duration
}

// HasProviderSelection reports whether Settings selects a concrete provider
// via a local profile or a named provider. Used by the CLI to reject enabling
// repair without any provider to call.
func (s Settings) HasProviderSelection() bool {
	return isHTTPProfile(s.LocalProfile) || s.ProviderName != ""
}

// ResolveProvider constructs the provider described by Settings, using cfg for
// named providers. It returns (nil, nil) when repair is off ([ModeOff]), so
// callers can treat a nil provider as "disabled" uniformly.
func ResolveProvider(cfg Config, s Settings) (Provider, error) {
	if s.Mode == ModeOff {
		return nil, nil
	}
	switch {
	case isHTTPProfile(s.LocalProfile):
		return NewLocalProvider(s.LocalProfile, s.LocalURL, s.Model)
	case s.LocalProfile == ProfileCommand:
		if s.ProviderName == "" {
			return nil, fmt.Errorf("--llm-local command requires --llm-provider naming a command provider in the config file")
		}
		return BuildProvider(cfg, s.ProviderName)
	case s.ProviderName != "":
		return BuildProvider(cfg, s.ProviderName)
	default:
		return nil, fmt.Errorf("llm mode is %q but no provider configured; use --llm-local or --llm-provider", s.Mode)
	}
}

// isHTTPProfile reports whether p is one of the OpenAI-compatible local
// profiles built directly from flags (no config needed).
func isHTTPProfile(p LocalProfile) bool {
	return p == ProfileOllama || p == ProfileLMStudio || p == ProfileLlamaCPP
}
