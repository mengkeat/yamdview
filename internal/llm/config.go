package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Mode controls whether LLM repair is active and how it prompts the user.
type Mode string

const (
	// ModeOff disables LLM repair entirely; no provider is constructed.
	ModeOff Mode = "off"
	// ModeAsk prompts the user (CLI/browser) before applying an LLM repair.
	ModeAsk Mode = "ask"
	// ModeAuto applies validated LLM repairs automatically without prompting.
	ModeAuto Mode = "auto"
)

// ParseMode converts a CLI string to a Mode. Empty input maps to ModeOff.
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "", ModeOff:
		return ModeOff, nil
	case ModeAsk:
		return ModeAsk, nil
	case ModeAuto:
		return ModeAuto, nil
	default:
		return "", fmt.Errorf("unknown llm mode %q; valid values: off, ask, auto", s)
	}
}

// ProviderType identifies a configured provider implementation.
type ProviderType string

const (
	// ProviderTypeOpenAI is an OpenAI-compatible HTTP endpoint.
	ProviderTypeOpenAI ProviderType = "openai-compatible"
	// ProviderTypeCommand is a local command provider.
	ProviderTypeCommand ProviderType = "command"
)

// duration is a JSON-decodable time.Duration that accepts both duration
// strings ("20s") and numbers (interpreted as seconds).
type duration time.Duration

func (d *duration) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if strings.TrimSpace(s) == "" {
			return nil
		}
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("duration %q: %w", s, err)
		}
		*d = duration(parsed)
		return nil
	}
	var seconds float64
	if err := json.Unmarshal(data, &seconds); err != nil {
		return err
	}
	*d = duration(time.Duration(seconds * float64(time.Second)))
	return nil
}

// Std returns the duration as a time.Duration.
func (d duration) Std() time.Duration { return time.Duration(d) }

// ProviderConfig is the definition of one named provider in a config file.
type ProviderConfig struct {
	Type        ProviderType `json:"type"`
	BaseURL     string       `json:"base_url,omitempty"`
	Model       string       `json:"model,omitempty"`
	APIKeyEnv   string       `json:"api_key_env,omitempty"`
	APIKey      string       `json:"api_key,omitempty"`
	Timeout     duration     `json:"timeout,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
	Command     []string     `json:"command,omitempty"`
	MaxBytes    int          `json:"max_bytes,omitempty"`
}

// Config is the top-level LLM provider configuration document.
type Config struct {
	Providers map[string]ProviderConfig `json:"providers"`
}

// ParseConfig strictly decodes an LLM provider config document. Unknown fields
// are rejected so misconfiguration is loud.
func ParseConfig(data []byte) (Config, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse llm config: %w", err)
	}
	return cfg, nil
}

// ParseConfigFile reads and parses a config file from path.
func ParseConfigFile(path string, read func(string) ([]byte, error)) (Config, error) {
	data, err := read(path)
	if err != nil {
		return Config{}, fmt.Errorf("read llm config %s: %w", path, err)
	}
	return ParseConfig(data)
}

// BuildProvider constructs the named provider from a config. It validates the
// provider type and required fields, returning an error before constructing an
// unusable provider.
func BuildProvider(cfg Config, name string) (Provider, error) {
	pc, ok := cfg.Providers[name]
	if !ok {
		return nil, fmt.Errorf("llm provider %q not found in config", name)
	}
	switch pc.Type {
	case ProviderTypeOpenAI:
		return NewOpenAI(OpenAIConfig{
			Name:        name,
			BaseURL:     pc.BaseURL,
			Model:       pc.Model,
			APIKeyEnv:   pc.APIKeyEnv,
			APIKey:      pc.APIKey,
			Timeout:     pc.Timeout.Std(),
			MaxTokens:   pc.MaxTokens,
			Temperature: pc.Temperature,
		}), nil
	case ProviderTypeCommand:
		if len(pc.Command) == 0 {
			return nil, fmt.Errorf("llm provider %q is type command but has no command", name)
		}
		return NewCommand(CommandConfig{
			Name:     name,
			Command:  pc.Command,
			Timeout:  pc.Timeout.Std(),
			MaxBytes: pc.MaxBytes,
		}), nil
	default:
		return nil, fmt.Errorf("llm provider %q has unknown type %q", name, pc.Type)
	}
}
