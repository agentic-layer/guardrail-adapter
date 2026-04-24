package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// Mode represents when guardrail inspection should occur.
type Mode string

const (
	// ModePreCall inspects request bodies before they are sent upstream.
	ModePreCall Mode = "pre_call"
	// ModePostCall inspects response bodies before they are sent downstream.
	ModePostCall Mode = "post_call"
)

const (
	// Common metadata key prefixes
	keyPrefix   = "guardrail."
	keyProvider = "guardrail.provider"
	keyMode     = "guardrail.mode"

	// Presidio-specific key prefixes
	presidioPrefix          = "guardrail.presidio."
	presidioEndpoint        = "guardrail.presidio.endpoint"
	presidioLanguage        = "guardrail.presidio.language"
	presidioScoreThresholds = "guardrail.presidio.score_thresholds"
	presidioEntityActions   = "guardrail.presidio.entity_actions"
)

// GuardrailConfig represents the parsed guardrail configuration.
type GuardrailConfig struct {
	Provider string
	Modes    []Mode
	Presidio *PresidioConfig
}

// PresidioConfig contains Presidio-specific configuration.
type PresidioConfig struct {
	Endpoint        string
	Language        string
	ScoreThresholds map[string]float64
	EntityActions   map[string]string
}

// ParseGuardrailConfig parses guardrail configuration from ext_proc metadata.
// Returns nil if no guardrail.provider key is present (passthrough mode).
func ParseGuardrailConfig(fields map[string]string) (*GuardrailConfig, error) {
	// Check if guardrail configuration is present
	provider, ok := fields[keyProvider]
	if !ok || provider == "" {
		return nil, nil
	}

	config := &GuardrailConfig{
		Provider: provider,
		Modes:    parseModes(fields[keyMode]),
	}

	// Parse provider-specific configuration
	switch provider {
	case "presidio-api":
		presidioConfig, err := parsePresidioConfig(fields)
		if err != nil {
			return nil, fmt.Errorf("failed to parse presidio config: %w", err)
		}
		config.Presidio = presidioConfig
	default:
		// Unknown provider - config is still valid, just has no provider-specific fields
		// This allows for future extensibility
	}

	return config, nil
}

// parseModes parses the comma-separated mode string into a slice of Mode types.
func parseModes(modeStr string) []Mode {
	if modeStr == "" {
		return []Mode{}
	}

	modes := []Mode{}
	for _, mode := range strings.Split(modeStr, ",") {
		mode = strings.TrimSpace(mode)
		if mode != "" {
			modes = append(modes, Mode(mode))
		}
	}
	return modes
}

// guardrailConfigYAML is the on-disk representation, decoded via sigs.k8s.io/yaml
// (which converts YAML -> JSON internally, so tags must be `json`).
type guardrailConfigYAML struct {
	Provider string              `json:"provider"`
	Modes    []string            `json:"modes"`
	Presidio *presidioConfigYAML `json:"presidio,omitempty"`
}

type presidioConfigYAML struct {
	Endpoint        string             `json:"endpoint"`
	Language        string             `json:"language,omitempty"`
	ScoreThresholds map[string]float64 `json:"score_thresholds,omitempty"`
	EntityActions   map[string]string  `json:"entity_actions,omitempty"`
}

// LoadGuardrailConfigFile reads and decodes a YAML config file, then validates it.
// Returns a *GuardrailConfig ready for the ext_proc server. Unknown fields cause
// an error so typos surface immediately.
func LoadGuardrailConfigFile(path string) (*GuardrailConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var raw guardrailConfigYAML
	if err := yaml.UnmarshalStrict(data, &raw); err != nil {
		return nil, fmt.Errorf("decode config yaml: %w", err)
	}

	cfg := &GuardrailConfig{
		Provider: raw.Provider,
		Modes:    make([]Mode, 0, len(raw.Modes)),
	}
	for _, m := range raw.Modes {
		cfg.Modes = append(cfg.Modes, Mode(m))
	}
	if raw.Presidio != nil {
		cfg.Presidio = &PresidioConfig{
			Endpoint:        raw.Presidio.Endpoint,
			Language:        raw.Presidio.Language,
			ScoreThresholds: raw.Presidio.ScoreThresholds,
			EntityActions:   raw.Presidio.EntityActions,
		}
	}
	return cfg, nil
}

// parsePresidioConfig extracts Presidio-specific configuration from metadata fields.
func parsePresidioConfig(fields map[string]string) (*PresidioConfig, error) {
	config := &PresidioConfig{
		Endpoint: fields[presidioEndpoint],
		Language: fields[presidioLanguage],
	}

	// Parse score thresholds (JSON object)
	if thresholdsStr := fields[presidioScoreThresholds]; thresholdsStr != "" {
		var thresholds map[string]float64
		if err := json.Unmarshal([]byte(thresholdsStr), &thresholds); err != nil {
			return nil, fmt.Errorf("failed to parse score_thresholds JSON: %w", err)
		}
		config.ScoreThresholds = thresholds
	}

	// Parse entity actions (JSON object)
	if actionsStr := fields[presidioEntityActions]; actionsStr != "" {
		var actions map[string]string
		if err := json.Unmarshal([]byte(actionsStr), &actions); err != nil {
			return nil, fmt.Errorf("failed to parse entity_actions JSON: %w", err)
		}
		config.EntityActions = actions
	}

	return config, nil
}
