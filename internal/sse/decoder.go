// Package sse decodes and encodes Server-Sent Events for use by the
// guardrail adapter's frame-aware SSE inspection path. The decoder is
// chunk-fed: the caller hands in HTTP body bytes as they arrive and
// receives complete events as they finish. The encoder produces a
// canonical event byte sequence used when emitting mutated or
// adapter-synthesised events.
package sse

import "errors"

// ErrEventTooLarge is returned by Decoder.Write when the in-progress
// event's accumulated bytes exceed the configured per-event cap. The
// error is sticky: subsequent calls keep returning it.
var ErrEventTooLarge = errors.New("sse: event exceeds max size")

// Event is one fully-assembled SSE event.
type Event struct {
	// Raw is the exact source bytes for this event including the
	// trailing blank line. Pass-through emit uses Raw verbatim.
	Raw []byte
	// Name is the "event:" field value, defaulting to "message" when
	// the source omits it.
	Name string
	// ID is the "id:" field value, "" when the source omits it.
	ID string
	// Data is the joined "data:" field values, "\n"-separated, with no
	// trailing newline.
	Data []byte
}
