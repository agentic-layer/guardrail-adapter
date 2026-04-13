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
	// AnonymizationMetadata stores metadata needed to deanonymize masked text.
	// Only populated when Action == ActionMask.
	AnonymizationMetadata interface{}
}

// GuardrailProvider is the interface for inspecting text and applying guardrails.
type GuardrailProvider interface {
	// Inspect analyzes the provided text and returns the action to take.
	Inspect(ctx context.Context, text string) (*Result, error)
	// Deanonymize restores masked text to its original form using anonymization metadata.
	Deanonymize(ctx context.Context, maskedText string, metadata interface{}) (string, error)
}
