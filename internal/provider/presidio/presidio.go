package presidio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/agentic-layer/guardrail-adapter/internal/provider"
)

// Config holds the configuration for the Presidio provider.
type Config struct {
	// Endpoint is the base URL of the Presidio Analyzer service.
	Endpoint string
	// Language is the language of the text to analyze (e.g., "en").
	Language string
	// ScoreThresholds maps entity types to minimum confidence scores.
	// Entities below the threshold are filtered out.
	// Use "ALL" as a catch-all default for entity types not explicitly configured.
	ScoreThresholds map[string]float64
	// EntityActions maps entity types to actions (ALLOW, MASK, or BLOCK).
	// Use "ALL" as a catch-all default for entity types not explicitly configured.
	EntityActions map[string]string
}

// Provider is the Presidio implementation of the GuardrailProvider interface.
type Provider struct {
	config     Config
	httpClient *http.Client
}

// analyzeRequest represents the request to Presidio Analyzer API.
type analyzeRequest struct {
	Text     string   `json:"text"`
	Language string   `json:"language"`
	Entities []string `json:"entities,omitempty"`
}

// recognizerResult represents a single PII detection result from Presidio.
type recognizerResult struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

// anonymizeRequest represents the request to Presidio Anonymizer API.
type anonymizeRequest struct {
	Text            string                      `json:"text"`
	AnalyzerResults []recognizerResult          `json:"analyzer_results"`
	Anonymizers     map[string]anonymizerConfig `json:"anonymizers,omitempty"`
}

// anonymizerConfig represents the configuration for a specific anonymizer.
type anonymizerConfig struct {
	Type string `json:"type"`
}

// anonymizeResponse represents the response from Presidio Anonymizer API.
type anonymizeResponse struct {
	Text  string                  `json:"text"`
	Items []anonymizeResponseItem `json:"items"`
}

// anonymizeResponseItem represents an item in the anonymizer response.
type anonymizeResponseItem struct {
	Start      int    `json:"start"`
	End        int    `json:"end"`
	EntityType string `json:"entity_type"`
	Text       string `json:"text"`
	Operator   string `json:"operator"`
}

// deanonymizeRequest represents the request to Presidio Deanonymizer API.
type deanonymizeRequest struct {
	Text          string                        `json:"text"`
	Entities      []deanonymizeEntity           `json:"entities"`
	Deanonymizers map[string]deanonymizerConfig `json:"deanonymizers,omitempty"`
}

// deanonymizeEntity represents an entity to be deanonymized.
type deanonymizeEntity struct {
	Start      int    `json:"start"`
	End        int    `json:"end"`
	EntityType string `json:"entity_type"`
	Text       string `json:"text"`
	Operator   string `json:"operator"`
}

// deanonymizerConfig represents the configuration for a specific deanonymizer.
type deanonymizerConfig struct {
	Type string `json:"type"`
}

// deanonymizeResponse represents the response from Presidio Deanonymizer API.
type deanonymizeResponse struct {
	Text  string                    `json:"text"`
	Items []deanonymizeResponseItem `json:"items"`
}

// deanonymizeResponseItem represents an item in the deanonymizer response.
type deanonymizeResponseItem struct {
	Start      int    `json:"start"`
	End        int    `json:"end"`
	EntityType string `json:"entity_type"`
	Text       string `json:"text"`
	Operator   string `json:"operator"`
}

// New creates a new Presidio provider with the given configuration.
func New(config Config) *Provider {
	return &Provider{
		config:     config,
		httpClient: &http.Client{},
	}
}

// ProcessRequest analyzes the provided text using Presidio and returns the action to take.
func (p *Provider) ProcessRequest(ctx context.Context, text string) (*provider.Result, error) {
	// Prepare the analyze request
	reqBody := analyzeRequest{
		Text:     text,
		Language: p.config.Language,
	}

	// If EntityActions is set, only request those entity types from Presidio
	if len(p.config.EntityActions) > 0 {
		entities := make([]string, 0, len(p.config.EntityActions))
		for entityType := range p.config.EntityActions {
			if entityType != "ALL" {
				entities = append(entities, entityType)
			}
		}
		if len(entities) > 0 {
			reqBody.Entities = entities
		}
	}

	// Marshal request
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := strings.TrimSuffix(p.config.Endpoint, "/") + "/analyze"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to Presidio: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("presidio returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var results []recognizerResult
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Filter results by score thresholds
	filteredResults := p.filterByThreshold(results)

	// Determine action based on entity actions
	return p.determineAction(ctx, text, filteredResults)
}

// filterByThreshold filters out entities that don't meet the configured score threshold.
func (p *Provider) filterByThreshold(results []recognizerResult) []recognizerResult {
	if len(p.config.ScoreThresholds) == 0 {
		return results
	}

	filtered := make([]recognizerResult, 0)
	for _, result := range results {
		threshold := p.getThreshold(result.EntityType)
		if result.Score >= threshold {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

// getThreshold returns the threshold for the given entity type.
// Falls back to "ALL" if the specific entity type is not configured.
func (p *Provider) getThreshold(entityType string) float64 {
	if threshold, ok := p.config.ScoreThresholds[entityType]; ok {
		return threshold
	}
	if threshold, ok := p.config.ScoreThresholds["ALL"]; ok {
		return threshold
	}
	return 0.0 // No threshold configured
}

// determineAction determines the action to take based on the detected entities.
// Returns an error if the request should be blocked.
// Otherwise returns the processed text (original or masked) and metadata.
func (p *Provider) determineAction(ctx context.Context, text string, results []recognizerResult) (*provider.Result, error) {
	if len(results) == 0 {
		// No entities detected, return original text
		return &provider.Result{
			Text: text,
		}, nil
	}

	// Check for BLOCK entities first (BLOCK takes precedence)
	blockEntities := make([]string, 0)
	maskEntities := make([]recognizerResult, 0)

	for _, result := range results {
		action := p.getAction(result.EntityType)
		switch action {
		case "BLOCK":
			blockEntities = append(blockEntities, result.EntityType)
		case "MASK":
			maskEntities = append(maskEntities, result)
		}
	}

	// If any BLOCK entity is detected, reject the entire request
	if len(blockEntities) > 0 {
		return nil, fmt.Errorf("detected blocked entities: %s", strings.Join(uniqueStrings(blockEntities), ", "))
	}

	// If there are MASK entities, apply masking using Presidio anonymize endpoint
	if len(maskEntities) > 0 {
		maskedText, anonymizeItems, err := p.anonymizeText(ctx, text, maskEntities)
		if err != nil {
			// If anonymization fails, return an error
			return nil, fmt.Errorf("failed to anonymize text: %w", err)
		}
		return &provider.Result{
			Text:             maskedText,
			ResponseMetadata: anonymizeItems,
		}, nil
	}

	// No BLOCK or MASK entities, return original text
	return &provider.Result{
		Text: text,
	}, nil
}

// getAction returns the action for the given entity type.
// Falls back to "ALL" if the specific entity type is not configured.
// Returns "ALLOW" as the default if nothing is configured.
func (p *Provider) getAction(entityType string) string {
	if action, ok := p.config.EntityActions[entityType]; ok {
		return action
	}
	if action, ok := p.config.EntityActions["ALL"]; ok {
		return action
	}
	return "ALLOW"
}

// anonymizeText uses Presidio's /anonymize endpoint to mask detected PII.
// Returns the masked text and anonymization items needed for deanonymization.
func (p *Provider) anonymizeText(ctx context.Context, text string, results []recognizerResult) (string, []anonymizeResponseItem, error) {
	// Build anonymizers config - use "replace" operator to replace with entity type
	anonymizers := make(map[string]anonymizerConfig)
	for _, result := range results {
		if _, exists := anonymizers[result.EntityType]; !exists {
			anonymizers[result.EntityType] = anonymizerConfig{
				Type: "replace",
			}
		}
	}

	// Prepare the anonymize request
	reqBody := anonymizeRequest{
		Text:            text,
		AnalyzerResults: results,
		Anonymizers:     anonymizers,
	}

	// Marshal request
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal anonymize request: %w", err)
	}

	// Create HTTP request
	url := strings.TrimSuffix(p.config.Endpoint, "/") + "/anonymize"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create anonymize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("failed to send request to Presidio anonymizer: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read anonymize response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("presidio anonymizer returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var anonymizeResp anonymizeResponse
	if err := json.Unmarshal(body, &anonymizeResp); err != nil {
		return "", nil, fmt.Errorf("failed to parse anonymize response: %w", err)
	}

	return anonymizeResp.Text, anonymizeResp.Items, nil
}

// ProcessResponse restores masked text to its original form using Presidio's /deanonymize endpoint.
func (p *Provider) ProcessResponse(ctx context.Context, maskedText string, metadata interface{}) (string, error) {
	// Convert metadata to anonymize items
	items, ok := metadata.([]anonymizeResponseItem)
	if !ok {
		return "", fmt.Errorf("invalid anonymization metadata type")
	}

	if len(items) == 0 {
		// No entities to deanonymize, return text as-is
		return maskedText, nil
	}

	// Convert anonymize items to deanonymize entities
	entities := make([]deanonymizeEntity, len(items))
	for i, item := range items {
		//nolint:staticcheck // Cannot use type conversion as field names differ between types
		entities[i] = deanonymizeEntity{
			Start:      item.Start,
			End:        item.End,
			EntityType: item.EntityType,
			Text:       item.Text,
			Operator:   item.Operator,
		}
	}

	// Prepare the deanonymize request
	reqBody := deanonymizeRequest{
		Text:     maskedText,
		Entities: entities,
	}

	// Marshal request
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal deanonymize request: %w", err)
	}

	// Create HTTP request
	url := strings.TrimSuffix(p.config.Endpoint, "/") + "/deanonymize"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create deanonymize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request to Presidio deanonymizer: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read deanonymize response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("presidio deanonymizer returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var deanonymizeResp deanonymizeResponse
	if err := json.Unmarshal(body, &deanonymizeResp); err != nil {
		return "", fmt.Errorf("failed to parse deanonymize response: %w", err)
	}

	return deanonymizeResp.Text, nil
}

// uniqueStrings returns a deduplicated slice of strings.
func uniqueStrings(strs []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
