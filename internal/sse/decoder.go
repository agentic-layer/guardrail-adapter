// Package sse decodes and encodes Server-Sent Events for use by the
// guardrail adapter's frame-aware SSE inspection path. The decoder is
// chunk-fed: the caller hands in HTTP body bytes as they arrive and
// receives complete events as they finish. The encoder produces a
// canonical event byte sequence used when emitting mutated or
// adapter-synthesised events.
package sse

import (
	"bytes"
	"errors"
)

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

// Decoder reads chunked SSE bytes and yields complete events. Use Write
// to feed bytes and consume returned events; use Flush at end-of-stream
// to drain a trailing event without a closing blank line.
type Decoder struct {
	max  int      // per-event cap in bytes; <= 0 disables
	buf  []byte   // unscanned bytes (after the last newline scanned)
	raw  []byte   // raw bytes of the in-progress event so far
	name string   // "event:" value for the in-progress event
	id   string   // "id:" value for the in-progress event
	data [][]byte // accumulated "data:" payloads for the in-progress event
	seen bool     // any field or comment line observed in the in-progress event
	err  error    // sticky error (e.g., ErrEventTooLarge)
}

// NewDecoder returns a Decoder. max is the per-event byte budget; <= 0
// disables the cap (intended for tests).
func NewDecoder(max int) *Decoder { return &Decoder{max: max} }

// Pending reports the bytes held in the in-progress event (raw bytes
// plus unscanned tail).
func (d *Decoder) Pending() int { return len(d.raw) + len(d.buf) }

// Write appends chunk and returns events that completed within it.
func (d *Decoder) Write(chunk []byte) ([]Event, error) {
	if d.err != nil {
		return nil, d.err
	}
	d.buf = append(d.buf, chunk...)

	var events []Event
	for {
		idx := bytes.IndexByte(d.buf, '\n')
		if idx < 0 {
			break
		}
		// Check the per-event size cap before moving the line into raw.
		// This ensures we're checking the current in-progress event size,
		// not the entire unscanned buffer which may contain multiple events.
		if d.max > 0 && len(d.raw)+idx+1 > d.max {
			d.err = ErrEventTooLarge
			return nil, d.err
		}
		// Move bytes up to and including the '\n' from buf into raw.
		d.raw = append(d.raw, d.buf[:idx+1]...)
		line := d.buf[:idx]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		d.buf = d.buf[idx+1:]

		if len(line) == 0 {
			if d.seen {
				events = append(events, d.buildEvent())
			}
			d.reset()
			continue
		}
		d.processLine(line)
	}
	// After processing all complete lines, check if the in-progress event
	// (including the unscanned tail) exceeds the per-event cap. This catches
	// cases where a single event is being built across multiple chunks.
	if d.max > 0 && d.Pending() > d.max {
		d.err = ErrEventTooLarge
		return nil, d.err
	}
	return events, nil
}

func (d *Decoder) buildEvent() Event {
	rawCopy := make([]byte, len(d.raw))
	copy(rawCopy, d.raw)
	name := d.name
	if name == "" {
		name = "message"
	}
	var data []byte
	if len(d.data) > 0 {
		data = bytes.Join(d.data, []byte{'\n'})
	}
	return Event{Raw: rawCopy, Name: name, ID: d.id, Data: data}
}

func (d *Decoder) reset() {
	d.raw = nil
	d.name = ""
	d.id = ""
	d.data = nil
	d.seen = false
}

// processLine handles a single non-empty line. The line has had any
// trailing '\r' stripped.
func (d *Decoder) processLine(line []byte) {
	if line[0] == ':' {
		// Comment per the EventSource spec; ignored but counts as
		// activity so a stream of comments doesn't dispatch an empty
		// event when the blank line lands.
		d.seen = true
		return
	}
	colon := bytes.IndexByte(line, ':')
	var field, value []byte
	if colon < 0 {
		field = line
	} else {
		field = line[:colon]
		value = line[colon+1:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
	}
	d.seen = true
	switch string(field) {
	case "event":
		d.name = string(value)
	case "id":
		d.id = string(value)
	case "data":
		cp := make([]byte, len(value))
		copy(cp, value)
		d.data = append(d.data, cp)
	}
}

// Flush returns the in-progress event when end-of-stream arrives without
// a trailing blank line, or nil when the in-progress event is empty.
// Treats any unterminated trailing bytes as a final line.
func (d *Decoder) Flush() (*Event, error) {
	if d.err != nil {
		return nil, d.err
	}
	if len(d.buf) > 0 {
		// Move the unterminated tail into raw and process it as a line.
		d.raw = append(d.raw, d.buf...)
		line := d.buf
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		d.buf = nil
		if len(line) > 0 {
			d.processLine(line)
		}
	}
	if !d.seen {
		return nil, nil
	}
	ev := d.buildEvent()
	d.reset()
	return &ev, nil
}
