package metadata

import (
	"testing"
)

// TestParseGuardrailConfig tests parsing guardrail configuration from metadata.
func TestParseGuardrailConfig(t *testing.T) {
	testCases := []struct {
		name         string
		fields       map[string]string
		wantNil      bool
		wantProvider string
		wantModes    []Mode
		wantError    bool
	}{
		{
			name: "presidio_full_config",
			fields: map[string]string{
				"guardrail.provider":                  "presidio-api",
				"guardrail.mode":                      "pre_call,post_call",
				"guardrail.presidio.endpoint":         "http://presidio.default.svc:80",
				"guardrail.presidio.language":         "en",
				"guardrail.presidio.score_thresholds": `{"ALL": 0.5}`,
				"guardrail.presidio.entity_actions":   `{"PERSON": "MASK", "CREDIT_CARD": "BLOCK"}`,
			},
			wantProvider: "presidio-api",
			wantModes:    []Mode{ModePreCall, ModePostCall},
		},
		{
			name: "presidio_minimal_config",
			fields: map[string]string{
				"guardrail.provider":          "presidio-api",
				"guardrail.presidio.endpoint": "http://presidio.default.svc:80",
				"guardrail.presidio.language": "en",
			},
			wantProvider: "presidio-api",
			wantModes:    []Mode{},
		},
		{
			name: "no_guardrail_config",
			fields: map[string]string{
				"some.other.key": "value",
			},
			wantNil: true,
		},
		{
			name:    "empty_fields",
			fields:  map[string]string{},
			wantNil: true,
		},
		{
			name: "provider_only",
			fields: map[string]string{
				"guardrail.provider": "presidio-api",
			},
			wantProvider: "presidio-api",
			wantModes:    []Mode{},
		},
		{
			name: "single_mode",
			fields: map[string]string{
				"guardrail.provider": "presidio-api",
				"guardrail.mode":     "pre_call",
			},
			wantProvider: "presidio-api",
			wantModes:    []Mode{ModePreCall},
		},
		{
			name: "mode_with_spaces",
			fields: map[string]string{
				"guardrail.provider": "presidio-api",
				"guardrail.mode":     " pre_call , post_call ",
			},
			wantProvider: "presidio-api",
			wantModes:    []Mode{ModePreCall, ModePostCall},
		},
		{
			name: "unknown_provider",
			fields: map[string]string{
				"guardrail.provider": "future-provider",
				"guardrail.mode":     "pre_call",
			},
			wantProvider: "future-provider",
			wantModes:    []Mode{ModePreCall},
		},
		{
			name: "invalid_score_thresholds_json",
			fields: map[string]string{
				"guardrail.provider":                  "presidio-api",
				"guardrail.presidio.score_thresholds": `{invalid json}`,
			},
			wantError: true,
		},
		{
			name: "invalid_entity_actions_json",
			fields: map[string]string{
				"guardrail.provider":                "presidio-api",
				"guardrail.presidio.entity_actions": `{invalid json}`,
			},
			wantError: true,
		},
		{
			name: "empty_provider",
			fields: map[string]string{
				"guardrail.provider": "",
			},
			wantNil: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := ParseGuardrailConfig(tc.fields)

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseGuardrailConfig() error = %v, want nil", err)
			}

			if tc.wantNil {
				if config != nil {
					t.Errorf("expected nil config, got %+v", config)
				}
				return
			}

			if config == nil {
				t.Fatal("expected non-nil config, got nil")
			}

			if config.Provider != tc.wantProvider {
				t.Errorf("Provider = %q, want %q", config.Provider, tc.wantProvider)
			}

			if len(config.Modes) != len(tc.wantModes) {
				t.Errorf("got %d modes, want %d", len(config.Modes), len(tc.wantModes))
			}

			for i, wantMode := range tc.wantModes {
				if i >= len(config.Modes) {
					t.Errorf("missing mode at index %d: %s", i, wantMode)
					continue
				}
				if config.Modes[i] != wantMode {
					t.Errorf("Modes[%d] = %q, want %q", i, config.Modes[i], wantMode)
				}
			}
		})
	}
}

// TestParsePresidioConfig tests parsing of Presidio-specific configuration.
func TestParsePresidioConfig(t *testing.T) {
	testCases := []struct {
		name                string
		fields              map[string]string
		wantEndpoint        string
		wantLanguage        string
		wantScoreThresholds map[string]float64
		wantEntityActions   map[string]string
		wantError           bool
	}{
		{
			name: "full_config",
			fields: map[string]string{
				"guardrail.presidio.endpoint":         "http://presidio.default.svc:80",
				"guardrail.presidio.language":         "en",
				"guardrail.presidio.score_thresholds": `{"ALL": 0.5, "PERSON": 0.7}`,
				"guardrail.presidio.entity_actions":   `{"PERSON": "MASK", "CREDIT_CARD": "BLOCK"}`,
			},
			wantEndpoint: "http://presidio.default.svc:80",
			wantLanguage: "en",
			wantScoreThresholds: map[string]float64{
				"ALL":    0.5,
				"PERSON": 0.7,
			},
			wantEntityActions: map[string]string{
				"PERSON":      "MASK",
				"CREDIT_CARD": "BLOCK",
			},
		},
		{
			name: "minimal_config",
			fields: map[string]string{
				"guardrail.presidio.endpoint": "http://presidio.default.svc:80",
				"guardrail.presidio.language": "en",
			},
			wantEndpoint: "http://presidio.default.svc:80",
			wantLanguage: "en",
		},
		{
			name: "empty_json_fields",
			fields: map[string]string{
				"guardrail.presidio.endpoint":         "http://presidio.default.svc:80",
				"guardrail.presidio.language":         "fr",
				"guardrail.presidio.score_thresholds": "",
				"guardrail.presidio.entity_actions":   "",
			},
			wantEndpoint: "http://presidio.default.svc:80",
			wantLanguage: "fr",
		},
		{
			name: "only_score_thresholds",
			fields: map[string]string{
				"guardrail.presidio.score_thresholds": `{"ALL": 0.8}`,
			},
			wantScoreThresholds: map[string]float64{
				"ALL": 0.8,
			},
		},
		{
			name: "only_entity_actions",
			fields: map[string]string{
				"guardrail.presidio.entity_actions": `{"EMAIL": "MASK"}`,
			},
			wantEntityActions: map[string]string{
				"EMAIL": "MASK",
			},
		},
		{
			name: "invalid_score_thresholds_json",
			fields: map[string]string{
				"guardrail.presidio.score_thresholds": `{invalid}`,
			},
			wantError: true,
		},
		{
			name: "invalid_entity_actions_json",
			fields: map[string]string{
				"guardrail.presidio.entity_actions": `{invalid}`,
			},
			wantError: true,
		},
		{
			name: "score_thresholds_wrong_type",
			fields: map[string]string{
				"guardrail.presidio.score_thresholds": `{"ALL": "not_a_number"}`,
			},
			wantError: true,
		},
		{
			name:   "empty_fields",
			fields: map[string]string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := parsePresidioConfig(tc.fields)

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("parsePresidioConfig() error = %v, want nil", err)
			}

			if config == nil {
				t.Fatal("expected non-nil config, got nil")
			}

			if config.Endpoint != tc.wantEndpoint {
				t.Errorf("Endpoint = %q, want %q", config.Endpoint, tc.wantEndpoint)
			}

			if config.Language != tc.wantLanguage {
				t.Errorf("Language = %q, want %q", config.Language, tc.wantLanguage)
			}

			if tc.wantScoreThresholds != nil {
				if config.ScoreThresholds == nil {
					t.Error("expected non-nil ScoreThresholds, got nil")
				} else {
					for key, wantValue := range tc.wantScoreThresholds {
						if gotValue, ok := config.ScoreThresholds[key]; !ok {
							t.Errorf("missing ScoreThresholds[%s]", key)
						} else if gotValue != wantValue {
							t.Errorf("ScoreThresholds[%s] = %f, want %f", key, gotValue, wantValue)
						}
					}
					// Check no extra keys
					for key := range config.ScoreThresholds {
						if _, ok := tc.wantScoreThresholds[key]; !ok {
							t.Errorf("unexpected ScoreThresholds[%s] = %f", key, config.ScoreThresholds[key])
						}
					}
				}
			}

			if tc.wantEntityActions != nil {
				if config.EntityActions == nil {
					t.Error("expected non-nil EntityActions, got nil")
				} else {
					for key, wantValue := range tc.wantEntityActions {
						if gotValue, ok := config.EntityActions[key]; !ok {
							t.Errorf("missing EntityActions[%s]", key)
						} else if gotValue != wantValue {
							t.Errorf("EntityActions[%s] = %q, want %q", key, gotValue, wantValue)
						}
					}
					// Check no extra keys
					for key := range config.EntityActions {
						if _, ok := tc.wantEntityActions[key]; !ok {
							t.Errorf("unexpected EntityActions[%s] = %q", key, config.EntityActions[key])
						}
					}
				}
			}
		})
	}
}

// TestParseModes tests the mode parsing functionality.
func TestParseModes(t *testing.T) {
	testCases := []struct {
		name      string
		modeStr   string
		wantModes []Mode
	}{
		{
			name:      "single_mode",
			modeStr:   "pre_call",
			wantModes: []Mode{ModePreCall},
		},
		{
			name:      "two_modes",
			modeStr:   "pre_call,post_call",
			wantModes: []Mode{ModePreCall, ModePostCall},
		},
		{
			name:      "modes_with_spaces",
			modeStr:   " pre_call , post_call ",
			wantModes: []Mode{ModePreCall, ModePostCall},
		},
		{
			name:      "three_modes",
			modeStr:   "pre_call,post_call,streaming",
			wantModes: []Mode{ModePreCall, ModePostCall, "streaming"},
		},
		{
			name:      "empty_string",
			modeStr:   "",
			wantModes: []Mode{},
		},
		{
			name:      "only_spaces",
			modeStr:   "  ",
			wantModes: []Mode{},
		},
		{
			name:      "trailing_comma",
			modeStr:   "pre_call,post_call,",
			wantModes: []Mode{ModePreCall, ModePostCall},
		},
		{
			name:      "leading_comma",
			modeStr:   ",pre_call,post_call",
			wantModes: []Mode{ModePreCall, ModePostCall},
		},
		{
			name:      "multiple_consecutive_commas",
			modeStr:   "pre_call,,post_call",
			wantModes: []Mode{ModePreCall, ModePostCall},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			modes := parseModes(tc.modeStr)

			if len(modes) != len(tc.wantModes) {
				t.Errorf("got %d modes, want %d", len(modes), len(tc.wantModes))
				t.Logf("got: %v", modes)
				t.Logf("want: %v", tc.wantModes)
				return
			}

			for i, wantMode := range tc.wantModes {
				if modes[i] != wantMode {
					t.Errorf("modes[%d] = %q, want %q", i, modes[i], wantMode)
				}
			}
		})
	}
}

// TestPresidioConfigFields tests that Presidio config correctly extracts all fields.
func TestPresidioConfigFields(t *testing.T) {
	fields := map[string]string{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "pre_call,post_call",
		"guardrail.presidio.endpoint":         "http://presidio.default.svc:80",
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL": 0.5, "PERSON": 0.7, "EMAIL": 0.9}`,
		"guardrail.presidio.entity_actions":   `{"PERSON": "MASK", "CREDIT_CARD": "BLOCK", "EMAIL": "MASK"}`,
	}

	config, err := ParseGuardrailConfig(fields)
	if err != nil {
		t.Fatalf("ParseGuardrailConfig() error = %v", err)
	}

	if config == nil {
		t.Fatal("expected non-nil config")
	}

	if config.Presidio == nil {
		t.Fatal("expected non-nil Presidio config")
	}

	// Verify all fields
	if config.Presidio.Endpoint != "http://presidio.default.svc:80" {
		t.Errorf("Endpoint = %q, want %q", config.Presidio.Endpoint, "http://presidio.default.svc:80")
	}

	if config.Presidio.Language != "en" {
		t.Errorf("Language = %q, want %q", config.Presidio.Language, "en")
	}

	expectedThresholds := map[string]float64{
		"ALL":    0.5,
		"PERSON": 0.7,
		"EMAIL":  0.9,
	}

	if len(config.Presidio.ScoreThresholds) != len(expectedThresholds) {
		t.Errorf("got %d score thresholds, want %d", len(config.Presidio.ScoreThresholds), len(expectedThresholds))
	}

	for entity, expectedScore := range expectedThresholds {
		if score, ok := config.Presidio.ScoreThresholds[entity]; !ok {
			t.Errorf("missing score threshold for %s", entity)
		} else if score != expectedScore {
			t.Errorf("ScoreThresholds[%s] = %f, want %f", entity, score, expectedScore)
		}
	}

	expectedActions := map[string]string{
		"PERSON":      "MASK",
		"CREDIT_CARD": "BLOCK",
		"EMAIL":       "MASK",
	}

	if len(config.Presidio.EntityActions) != len(expectedActions) {
		t.Errorf("got %d entity actions, want %d", len(config.Presidio.EntityActions), len(expectedActions))
	}

	for entity, expectedAction := range expectedActions {
		if action, ok := config.Presidio.EntityActions[entity]; !ok {
			t.Errorf("missing entity action for %s", entity)
		} else if action != expectedAction {
			t.Errorf("EntityActions[%s] = %q, want %q", entity, action, expectedAction)
		}
	}
}
