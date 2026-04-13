package provider

import "context"

// Action represents the action to take after inspection.
type Action string

const (
	// ActionAllow allows the request to proceed without modification.
	ActionAllow Action = "ALLOW"
	// ActionMask replaces sensitive data with placeholders.
	ActionMask Action = "MASK"
	// ActionBlock rejects the request entirely.
	ActionBlock Action = "BLOCK"
)

// Result represents the result of inspecting text.
type Result struct {
	// Action is the action to take (ALLOW, MASK, or BLOCK).
	Action Action
	// MaskedText is populated when Action == ActionMask, containing the text with masked entities.
	MaskedText string
	// Reason is a human-readable reason for blocking, populated when Action == ActionBlock.
	Reason string
	// ResponseMetadata stores provider-specific metadata needed to process responses.
	// For example, this may contain data needed to restore masked text.
	// Only populated when Action == ActionMask.
	ResponseMetadata interface{}
}

// GuardrailProvider is the interface for inspecting text and applying guardrails.
type GuardrailProvider interface {
	// ProcessRequest analyzes the provided text and returns the action to take.
	ProcessRequest(ctx context.Context, text string) (*Result, error)
	// ProcessResponse restores processed text using metadata from ProcessRequest.
	// For example, this may restore masked text to its original form.
	ProcessResponse(ctx context.Context, processedText string, metadata interface{}) (string, error)
}
