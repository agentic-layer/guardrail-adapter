package provider

import "context"

// Result represents the result of processing text.
type Result struct {
	// Text is the processed text (either original or masked).
	Text string
	// ResponseMetadata stores provider-specific metadata needed to process responses.
	// For example, this may contain data needed to restore masked text.
	// Only populated when text was modified.
	ResponseMetadata interface{}
}

// GuardrailProvider is the interface for inspecting text and applying guardrails.
type GuardrailProvider interface {
	// ProcessRequest analyzes the provided text and returns the processed result.
	// If the request should be blocked, returns an error with the reason.
	// Otherwise, returns the text (either original or masked) and any metadata needed for ProcessResponse.
	ProcessRequest(ctx context.Context, text string) (*Result, error)
	// ProcessResponse restores processed text using metadata from ProcessRequest.
	// For example, this may restore masked text to its original form.
	ProcessResponse(ctx context.Context, processedText string, metadata interface{}) (string, error)
}
