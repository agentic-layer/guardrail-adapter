package sse

import (
	"bytes"
	"errors"
	"testing"
)

func TestEventZeroValue(t *testing.T) {
	var ev Event
	if ev.Name != "" || ev.ID != "" || ev.Data != nil || ev.Raw != nil {
		t.Errorf("zero Event = %+v, want zero", ev)
	}
}

func TestErrEventTooLargeIsSentinel(t *testing.T) {
	if !errors.Is(ErrEventTooLarge, ErrEventTooLarge) {
		t.Error("ErrEventTooLarge does not match itself via errors.Is")
	}
}

func TestDecoderSingleEventOneChunk(t *testing.T) {
	d := NewDecoder(0)
	chunk := []byte("event: message\ndata: hello\n\n")
	events, err := d.Write(chunk)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Name != "message" {
		t.Errorf("Name = %q, want %q", ev.Name, "message")
	}
	if !bytes.Equal(ev.Data, []byte("hello")) {
		t.Errorf("Data = %q, want %q", ev.Data, "hello")
	}
	if !bytes.Equal(ev.Raw, chunk) {
		t.Errorf("Raw = %q, want %q", ev.Raw, chunk)
	}
	if d.Pending() != 0 {
		t.Errorf("Pending() = %d, want 0", d.Pending())
	}
}
