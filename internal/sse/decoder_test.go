package sse

import (
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
