package presidio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentic-layer/guardrail-adapter/internal/provider"
)

func TestPresidioProvider_Inspect(t *testing.T) {
	tests := []struct {
		name           string
		config         Config
		text           string
		mockResponse   []recognizerResult
		mockStatusCode int
		expectedAction provider.Action
		expectedMasked string
		expectedReason string
		expectError    bool
	}{
		{
			name: "no entities detected - allow",
			config: Config{
				Language: "en",
			},
			text:           "This is a safe text",
			mockResponse:   []recognizerResult{},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionAllow,
		},
		{
			name: "PERSON entity below threshold - allow",
			config: Config{
				Language: "en",
				ScoreThresholds: map[string]float64{
					"PERSON": 0.8,
				},
			},
			text: "John Doe",
			mockResponse: []recognizerResult{
				{EntityType: "PERSON", Start: 0, End: 8, Score: 0.7},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionAllow,
		},
		{
			name: "PERSON entity above threshold - mask",
			config: Config{
				Language: "en",
				ScoreThresholds: map[string]float64{
					"PERSON": 0.5,
				},
				EntityActions: map[string]string{
					"PERSON": "MASK",
				},
			},
			text: "John Doe works here",
			mockResponse: []recognizerResult{
				{EntityType: "PERSON", Start: 0, End: 8, Score: 0.9},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionMask,
			expectedMasked: "<PERSON> works here",
		},
		{
			name: "CREDIT_CARD detected - block",
			config: Config{
				Language: "en",
				EntityActions: map[string]string{
					"CREDIT_CARD": "BLOCK",
				},
			},
			text: "Card number 4111-1111-1111-1111",
			mockResponse: []recognizerResult{
				{EntityType: "CREDIT_CARD", Start: 12, End: 31, Score: 0.95},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionBlock,
			expectedReason: "Detected blocked entities: CREDIT_CARD",
		},
		{
			name: "Multiple entities - BLOCK takes precedence",
			config: Config{
				Language: "en",
				EntityActions: map[string]string{
					"PERSON":      "MASK",
					"CREDIT_CARD": "BLOCK",
				},
			},
			text: "John Doe has card 4111-1111-1111-1111",
			mockResponse: []recognizerResult{
				{EntityType: "PERSON", Start: 0, End: 8, Score: 0.9},
				{EntityType: "CREDIT_CARD", Start: 18, End: 37, Score: 0.95},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionBlock,
			expectedReason: "Detected blocked entities: CREDIT_CARD",
		},
		{
			name: "Multiple MASK entities",
			config: Config{
				Language: "en",
				EntityActions: map[string]string{
					"PERSON": "MASK",
					"EMAIL":  "MASK",
				},
			},
			text: "Contact John Doe at john@example.com",
			mockResponse: []recognizerResult{
				{EntityType: "PERSON", Start: 8, End: 16, Score: 0.9},
				{EntityType: "EMAIL", Start: 20, End: 36, Score: 0.95},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionMask,
			expectedMasked: "Contact <PERSON> at <EMAIL>",
		},
		{
			name: "ALL threshold applies to unspecified entity",
			config: Config{
				Language: "en",
				ScoreThresholds: map[string]float64{
					"ALL": 0.8,
				},
				EntityActions: map[string]string{
					"PHONE_NUMBER": "MASK",
				},
			},
			text: "Call 555-1234",
			mockResponse: []recognizerResult{
				{EntityType: "PHONE_NUMBER", Start: 5, End: 13, Score: 0.7},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionAllow, // Below ALL threshold
		},
		{
			name: "Per-entity threshold overrides ALL",
			config: Config{
				Language: "en",
				ScoreThresholds: map[string]float64{
					"ALL":    0.5,
					"PERSON": 0.9,
				},
				EntityActions: map[string]string{
					"PERSON": "MASK",
				},
			},
			text: "John Doe",
			mockResponse: []recognizerResult{
				{EntityType: "PERSON", Start: 0, End: 8, Score: 0.7},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionAllow, // Below PERSON threshold
		},
		{
			name: "ALL action applies to unspecified entity",
			config: Config{
				Language: "en",
				EntityActions: map[string]string{
					"ALL": "BLOCK",
				},
			},
			text: "Some PII",
			mockResponse: []recognizerResult{
				{EntityType: "UNKNOWN_ENTITY", Start: 0, End: 8, Score: 0.9},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionBlock,
			expectedReason: "Detected blocked entities: UNKNOWN_ENTITY",
		},
		{
			name: "Presidio error - non-200 status",
			config: Config{
				Language: "en",
			},
			text:           "Some text",
			mockStatusCode: http.StatusInternalServerError,
			expectError:    true,
		},
		{
			name: "Unicode text masking",
			config: Config{
				Language: "en",
				EntityActions: map[string]string{
					"PERSON": "MASK",
				},
			},
			text: "Contact 李明 today",
			mockResponse: []recognizerResult{
				{EntityType: "PERSON", Start: 8, End: 10, Score: 0.9},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionMask,
			expectedMasked: "Contact <PERSON> today",
		},
		{
			name: "EntityActions filters requested entities",
			config: Config{
				Language: "en",
				EntityActions: map[string]string{
					"PERSON": "MASK",
					"EMAIL":  "BLOCK",
				},
			},
			text: "Test",
			mockResponse: []recognizerResult{
				{EntityType: "PERSON", Start: 0, End: 4, Score: 0.9},
			},
			mockStatusCode: http.StatusOK,
			expectedAction: provider.ActionMask,
			expectedMasked: "<PERSON>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock Presidio server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Handle /analyze endpoint
				if r.URL.Path == "/analyze" {
					// Verify request
					if r.Method != "POST" {
						t.Errorf("Expected POST request, got %s", r.Method)
					}

					// Verify request body
					var reqBody analyzeRequest
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Errorf("Failed to decode request body: %v", err)
					}
					if reqBody.Text != tt.text {
						t.Errorf("Expected text %q, got %q", tt.text, reqBody.Text)
					}
					if reqBody.Language != tt.config.Language {
						t.Errorf("Expected language %q, got %q", tt.config.Language, reqBody.Language)
					}

					// If EntityActions is set, verify entities filter
					if len(tt.config.EntityActions) > 0 {
						expectedEntities := make(map[string]bool)
						for entity := range tt.config.EntityActions {
							if entity != "ALL" {
								expectedEntities[entity] = true
							}
						}
						if len(expectedEntities) > 0 {
							if len(reqBody.Entities) == 0 {
								t.Error("Expected entities to be set in request")
							}
							for _, entity := range reqBody.Entities {
								if !expectedEntities[entity] {
									t.Errorf("Unexpected entity in request: %s", entity)
								}
							}
						}
					}

					// Send mock response
					w.WriteHeader(tt.mockStatusCode)
					if tt.mockStatusCode == http.StatusOK {
						_ = json.NewEncoder(w).Encode(tt.mockResponse)
					}
					return
				}

				// Handle /anonymize endpoint
				if r.URL.Path == "/anonymize" {
					if r.Method != "POST" {
						t.Errorf("Expected POST request, got %s", r.Method)
					}

					var reqBody anonymizeRequest
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Errorf("Failed to decode anonymize request body: %v", err)
					}

					// Mock anonymize response - replace entities with <ENTITY_TYPE>
					anonymizedText := reqBody.Text
					// Sort results by start position in descending order to replace from end to start
					results := make([]recognizerResult, len(reqBody.AnalyzerResults))
					copy(results, reqBody.AnalyzerResults)
					for i := len(results) - 1; i >= 0; i-- {
						for j := i - 1; j >= 0; j-- {
							if results[j].Start < results[i].Start {
								results[j], results[i] = results[i], results[j]
							}
						}
					}

					// Convert to runes for proper Unicode handling
					runes := []rune(anonymizedText)
					for _, result := range results {
						placeholder := "<" + result.EntityType + ">"
						runes = append(runes[:result.Start], append([]rune(placeholder), runes[result.End:]...)...)
					}
					anonymizedText = string(runes)

					resp := anonymizeResponse{
						Text: anonymizedText,
					}
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(resp)
					return
				}

				t.Errorf("Unexpected path: %s", r.URL.Path)
			}))
			defer server.Close()

			// Configure provider with mock server
			tt.config.Endpoint = server.URL
			provider := New(tt.config)

			// Execute test
			result, err := provider.Inspect(context.Background(), tt.text)

			// Verify error
			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Verify result
			if result.Action != tt.expectedAction {
				t.Errorf("Expected action %s, got %s", tt.expectedAction, result.Action)
			}
			if tt.expectedMasked != "" && result.MaskedText != tt.expectedMasked {
				t.Errorf("Expected masked text %q, got %q", tt.expectedMasked, result.MaskedText)
			}
			if tt.expectedReason != "" && result.Reason != tt.expectedReason {
				t.Errorf("Expected reason %q, got %q", tt.expectedReason, result.Reason)
			}
		})
	}
}

func TestPresidioProvider_filterByThreshold(t *testing.T) {
	tests := []struct {
		name             string
		thresholds       map[string]float64
		results          []recognizerResult
		expectedFiltered int
	}{
		{
			name:       "no thresholds - all pass",
			thresholds: map[string]float64{},
			results: []recognizerResult{
				{EntityType: "PERSON", Score: 0.5},
				{EntityType: "EMAIL", Score: 0.3},
			},
			expectedFiltered: 2,
		},
		{
			name: "ALL threshold filters low scores",
			thresholds: map[string]float64{
				"ALL": 0.7,
			},
			results: []recognizerResult{
				{EntityType: "PERSON", Score: 0.8},
				{EntityType: "EMAIL", Score: 0.5},
			},
			expectedFiltered: 1,
		},
		{
			name: "per-entity threshold overrides ALL",
			thresholds: map[string]float64{
				"ALL":    0.5,
				"PERSON": 0.9,
			},
			results: []recognizerResult{
				{EntityType: "PERSON", Score: 0.8},
				{EntityType: "EMAIL", Score: 0.6},
			},
			expectedFiltered: 1, // Only EMAIL passes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{
				config: Config{
					ScoreThresholds: tt.thresholds,
				},
			}
			filtered := p.filterByThreshold(tt.results)
			if len(filtered) != tt.expectedFiltered {
				t.Errorf("Expected %d filtered results, got %d", tt.expectedFiltered, len(filtered))
			}
		})
	}
}

func TestPresidioProvider_determineAction(t *testing.T) {
	tests := []struct {
		name           string
		text           string
		entityActions  map[string]string
		results        []recognizerResult
		expectedAction provider.Action
	}{
		{
			name:           "no results - allow",
			text:           "test text",
			entityActions:  map[string]string{},
			results:        []recognizerResult{},
			expectedAction: provider.ActionAllow,
		},
		{
			name: "BLOCK entity present - block",
			text: "some data here",
			entityActions: map[string]string{
				"CREDIT_CARD": "BLOCK",
			},
			results: []recognizerResult{
				{EntityType: "CREDIT_CARD", Start: 0, End: 10},
			},
			expectedAction: provider.ActionBlock,
		},
		{
			name: "MASK entity present - mask",
			text: "some data here",
			entityActions: map[string]string{
				"PERSON": "MASK",
			},
			results: []recognizerResult{
				{EntityType: "PERSON", Start: 0, End: 10},
			},
			expectedAction: provider.ActionMask,
		},
		{
			name: "Both BLOCK and MASK - BLOCK takes precedence",
			text: "some data here with more text",
			entityActions: map[string]string{
				"PERSON":      "MASK",
				"CREDIT_CARD": "BLOCK",
			},
			results: []recognizerResult{
				{EntityType: "PERSON", Start: 0, End: 10},
				{EntityType: "CREDIT_CARD", Start: 15, End: 24},
			},
			expectedAction: provider.ActionBlock,
		},
		{
			name: "Entity with no action configured - allow",
			text: "some data here",
			entityActions: map[string]string{
				"PERSON": "MASK",
			},
			results: []recognizerResult{
				{EntityType: "UNKNOWN", Start: 0, End: 10},
			},
			expectedAction: provider.ActionAllow,
		},
		{
			name: "ALL action applies to unconfigured entity",
			text: "some data here",
			entityActions: map[string]string{
				"ALL": "MASK",
			},
			results: []recognizerResult{
				{EntityType: "UNKNOWN", Start: 0, End: 10},
			},
			expectedAction: provider.ActionMask,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock Presidio server for anonymize endpoint
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/anonymize" {
					var reqBody anonymizeRequest
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Errorf("Failed to decode anonymize request: %v", err)
					}

					// Simple mock - replace entities with <ENTITY_TYPE>
					anonymizedText := reqBody.Text
					runes := []rune(anonymizedText)
					// Sort results by start position in descending order
					results := make([]recognizerResult, len(reqBody.AnalyzerResults))
					copy(results, reqBody.AnalyzerResults)
					for i := len(results) - 1; i >= 0; i-- {
						for j := i - 1; j >= 0; j-- {
							if results[j].Start < results[i].Start {
								results[j], results[i] = results[i], results[j]
							}
						}
					}
					for _, result := range results {
						placeholder := "<" + result.EntityType + ">"
						runes = append(runes[:result.Start], append([]rune(placeholder), runes[result.End:]...)...)
					}

					resp := anonymizeResponse{
						Text: string(runes),
					}
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(resp)
				}
			}))
			defer server.Close()

			p := &Provider{
				config: Config{
					Endpoint:      server.URL,
					EntityActions: tt.entityActions,
				},
				httpClient: &http.Client{},
			}
			result := p.determineAction(context.Background(), tt.text, tt.results)
			if result.Action != tt.expectedAction {
				t.Errorf("Expected action %s, got %s", tt.expectedAction, result.Action)
			}
		})
	}
}

func TestPresidioProvider_Deanonymize(t *testing.T) {
	tests := []struct {
		name           string
		maskedText     string
		metadata       interface{}
		expectedResult string
		expectError    bool
	}{
		{
			name:       "successful deanonymization",
			maskedText: "<PERSON> works at <ORGANIZATION>",
			metadata: []anonymizeResponseItem{
				{Start: 0, End: 8, EntityType: "PERSON", Text: "John Doe", Operator: "replace"},
				{Start: 18, End: 32, EntityType: "ORGANIZATION", Text: "Acme Corp", Operator: "replace"},
			},
			expectedResult: "John Doe works at Acme Corp",
			expectError:    false,
		},
		{
			name:           "empty metadata - return as-is",
			maskedText:     "No masked content",
			metadata:       []anonymizeResponseItem{},
			expectedResult: "No masked content",
			expectError:    false,
		},
		{
			name:        "invalid metadata type",
			maskedText:  "some text",
			metadata:    "invalid",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock Presidio server for deanonymize endpoint
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/deanonymize" {
					var reqBody deanonymizeRequest
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Errorf("Failed to decode deanonymize request: %v", err)
					}

					// Mock deanonymize response - restore original text
					// For testing purposes, use the Text field from entities as the restoration value
					restoredText := reqBody.Text
					runes := []rune(restoredText)

					// Sort entities by start position in descending order
					entities := make([]deanonymizeEntity, len(reqBody.Entities))
					copy(entities, reqBody.Entities)
					for i := len(entities) - 1; i >= 0; i-- {
						for j := i - 1; j >= 0; j-- {
							if entities[j].Start < entities[i].Start {
								entities[j], entities[i] = entities[i], entities[j]
							}
						}
					}

					// Replace placeholders with original text
					for _, entity := range entities {
						originalText := entity.Text
						runes = append(runes[:entity.Start], append([]rune(originalText), runes[entity.End:]...)...)
					}

					resp := deanonymizeResponse{
						Text: string(runes),
					}
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(resp)
				}
			}))
			defer server.Close()

			p := &Provider{
				config: Config{
					Endpoint: server.URL,
				},
				httpClient: &http.Client{},
			}

			result, err := p.Deanonymize(context.Background(), tt.maskedText, tt.metadata)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result != tt.expectedResult {
				t.Errorf("Expected result %q, got %q", tt.expectedResult, result)
			}
		})
	}
}
