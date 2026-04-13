package metadata

import (
	"encoding/json"
	"fmt"
	"strings"
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
	Modes    []string
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

// parseModes parses the comma-separated mode string into a slice.
func parseModes(modeStr string) []string {
	if modeStr == "" {
		return []string{}
	}

	modes := []string{}
	for _, mode := range strings.Split(modeStr, ",") {
		mode = strings.TrimSpace(mode)
		if mode != "" {
			modes = append(modes, mode)
		}
	}
	return modes
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
