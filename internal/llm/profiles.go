package llm

import (
	"fmt"
	"strings"
)

// LocalProfile is a named shortcut for a common local LLM endpoint.
type LocalProfile string

const (
	// ProfileOllama targets an Ollama OpenAI-compatible endpoint.
	ProfileOllama LocalProfile = "ollama"
	// ProfileLMStudio targets an LM Studio OpenAI-compatible endpoint.
	ProfileLMStudio LocalProfile = "lm-studio"
	// ProfileLlamaCPP targets a user-supplied llama.cpp OpenAI-compatible URL.
	ProfileLlamaCPP LocalProfile = "llama.cpp"
	// ProfileCommand selects a command provider resolved from config; it is
	// not handled by NewLocalProvider, which only builds HTTP providers.
	ProfileCommand LocalProfile = "command"
)

// Default base URLs for the local OpenAI-compatible profiles. They bind to the
// loopback interface only.
const (
	OllamaBaseURL   = "http://127.0.0.1:11434/v1"
	LMStudioBaseURL = "http://127.0.0.1:1234/v1"
)

// ParseLocalProfile converts a CLI string to a LocalProfile.
func ParseLocalProfile(s string) (LocalProfile, error) {
	switch LocalProfile(strings.ToLower(strings.TrimSpace(s))) {
	case ProfileOllama:
		return ProfileOllama, nil
	case ProfileLMStudio:
		return ProfileLMStudio, nil
	case ProfileLlamaCPP:
		return ProfileLlamaCPP, nil
	case ProfileCommand:
		return ProfileCommand, nil
	default:
		return "", fmt.Errorf("unknown local profile %q; valid values: ollama, lm-studio, llama.cpp, command", s)
	}
}

// NewLocalProvider builds an OpenAI-compatible provider for one of the local
// HTTP profiles. localURL overrides the profile's default base URL (and is
// required for llama.cpp). The command profile is config-resolved and returns
// an error here; callers should build it via [BuildProvider] instead.
func NewLocalProvider(profile LocalProfile, localURL, model string) (Provider, error) {
	switch profile {
	case ProfileOllama, ProfileLMStudio:
		base := localURL
		if base == "" {
			if profile == ProfileOllama {
				base = OllamaBaseURL
			} else {
				base = LMStudioBaseURL
			}
		}
		return NewOpenAI(OpenAIConfig{
			Name:    string(profile),
			BaseURL: base,
			Model:   model,
		}), nil
	case ProfileLlamaCPP:
		if strings.TrimSpace(localURL) == "" {
			return nil, fmt.Errorf("local profile %q requires a base url via --llm-local-url", profile)
		}
		return NewOpenAI(OpenAIConfig{
			Name:    "llama.cpp",
			BaseURL: localURL,
			Model:   model,
		}), nil
	case ProfileCommand:
		return nil, fmt.Errorf("local profile %q must be resolved via --llm-provider and config", profile)
	default:
		return nil, fmt.Errorf("unknown local profile %q", profile)
	}
}
