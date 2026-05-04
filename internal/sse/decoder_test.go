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

func TestDecoderEventSplitAcrossChunks(t *testing.T) {
	d := NewDecoder(0)
	if events, err := d.Write([]byte("event: message\ndata: he")); err != nil || len(events) != 0 {
		t.Fatalf("first Write: events=%d err=%v, want 0 events nil", len(events), err)
	}
	events, err := d.Write([]byte("llo\n\n"))
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if !bytes.Equal(events[0].Data, []byte("hello")) {
		t.Errorf("Data = %q, want %q", events[0].Data, "hello")
	}
	if !bytes.Equal(events[0].Raw, []byte("event: message\ndata: hello\n\n")) {
		t.Errorf("Raw = %q, want full assembled event", events[0].Raw)
	}
}

func TestDecoderMultipleEventsInOneChunk(t *testing.T) {
	d := NewDecoder(0)
	chunk := []byte("data: one\n\ndata: two\n\n")
	events, err := d.Write(chunk)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if !bytes.Equal(events[0].Data, []byte("one")) || !bytes.Equal(events[1].Data, []byte("two")) {
		t.Errorf("Data = (%q, %q), want (%q, %q)",
			events[0].Data, events[1].Data, "one", "two")
	}
	if !bytes.Equal(events[0].Raw, []byte("data: one\n\n")) {
		t.Errorf("event 0 Raw = %q, want %q", events[0].Raw, "data: one\n\n")
	}
	if !bytes.Equal(events[1].Raw, []byte("data: two\n\n")) {
		t.Errorf("event 1 Raw = %q, want %q", events[1].Raw, "data: two\n\n")
	}
}

func TestDecoderMultiLineDataJoinedWithNewline(t *testing.T) {
	d := NewDecoder(0)
	chunk := []byte("data: line one\ndata: line two\n\n")
	events, err := d.Write(chunk)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if !bytes.Equal(events[0].Data, []byte("line one\nline two")) {
		t.Errorf("Data = %q, want %q", events[0].Data, "line one\nline two")
	}
}
