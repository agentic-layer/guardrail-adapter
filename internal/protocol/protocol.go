package protocol

import "context"

// TextExtraction represents a text field extracted from a message with its path.
type TextExtraction struct {
	Path  string // Path to the text field (e.g., "params.arguments.query")
	Value string // The extracted text value
}

// Parser is the interface for parsing different protocol messages.
type Parser interface {
	// CanParse reports whether this parser can handle the given body.
	// When the parser cannot handle the body, the returned error explains why
	// (used for diagnostics when no parser matches). When the parser matches,
	// the error is nil.
	CanParse(ctx context.Context, body []byte, metadata map[string]string) (bool, error)

	// ParseRequest parses a request message and extracts text fields.
	// Returns the extracted texts and whether the request should be inspected.
	ParseRequest(ctx context.Context, body []byte) ([]TextExtraction, bool, error)

	// ParseResponse parses a response message and extracts text fields.
	// Returns the extracted texts and whether the response should be inspected.
	ParseResponse(ctx context.Context, body []byte) ([]TextExtraction, bool, error)

	// ReplaceTexts replaces text at specified paths in the message body.
	// This is used for MASK actions in guardrails.
	ReplaceTexts(ctx context.Context, body []byte, replacements map[string]string) ([]byte, error)
}
