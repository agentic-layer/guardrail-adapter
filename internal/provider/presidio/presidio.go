package presidio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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

// AnalyzeRequest represents the request to Presidio Analyzer API.
type analyzeRequest struct {
	Text     string   `json:"text"`
	Language string   `json:"language"`
	Entities []string `json:"entities,omitempty"`
}

// RecognizerResult represents a single PII detection result from Presidio.
type recognizerResult struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

// New creates a new Presidio provider with the given configuration.
func New(config Config) *Provider {
	return &Provider{
		config:     config,
		httpClient: &http.Client{},
	}
}

// Inspect analyzes the provided text using Presidio and returns the action to take.
func (p *Provider) Inspect(ctx context.Context, text string) (*provider.Result, error) {
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
	return p.determineAction(text, filteredResults), nil
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
// BLOCK takes precedence over MASK - if any entity is BLOCK, reject the entire request.
func (p *Provider) determineAction(text string, results []recognizerResult) *provider.Result {
	if len(results) == 0 {
		// No entities detected, allow the request
		return &provider.Result{
			Action: provider.ActionAllow,
		}
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
		return &provider.Result{
			Action: provider.ActionBlock,
			Reason: fmt.Sprintf("Detected blocked entities: %s", strings.Join(uniqueStrings(blockEntities), ", ")),
		}
	}

	// If there are MASK entities, apply masking
	if len(maskEntities) > 0 {
		maskedText := p.maskText(text, maskEntities)
		return &provider.Result{
			Action:     provider.ActionMask,
			MaskedText: maskedText,
		}
	}

	// No BLOCK or MASK entities, allow the request
	return &provider.Result{
		Action: provider.ActionAllow,
	}
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

// maskText replaces detected PII with <ENTITY_TYPE> placeholders.
func (p *Provider) maskText(text string, results []recognizerResult) string {
	// Sort results by start position in descending order to replace from end to start
	// This avoids invalidating positions as we replace text
	sorted := make([]recognizerResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start > sorted[j].Start
	})

	// Convert string to rune slice for proper Unicode handling
	runes := []rune(text)

	// Replace each entity with its placeholder
	for _, result := range sorted {
		placeholder := fmt.Sprintf("<%s>", result.EntityType)
		// Replace the entity text with the placeholder
		runes = append(runes[:result.Start], append([]rune(placeholder), runes[result.End:]...)...)
	}

	return string(runes)
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
