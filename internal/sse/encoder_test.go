package sse

import (
	"bytes"
	"testing"
)

func TestEncodeFullEvent(t *testing.T) {
	out := Encode("progress", "42", []byte(`{"x":1}`))
	want := []byte("event: progress\nid: 42\ndata: {\"x\":1}\n\n")
	if !bytes.Equal(out, want) {
		t.Errorf("Encode = %q, want %q", out, want)
	}
}

func TestEncodeOmitsDefaultName(t *testing.T) {
	out := Encode("message", "1", []byte("hi"))
	want := []byte("id: 1\ndata: hi\n\n")
	if !bytes.Equal(out, want) {
		t.Errorf("Encode = %q, want %q", out, want)
	}
}

func TestEncodeOmitsEmptyName(t *testing.T) {
	out := Encode("", "1", []byte("hi"))
	want := []byte("id: 1\ndata: hi\n\n")
	if !bytes.Equal(out, want) {
		t.Errorf("Encode = %q, want %q", out, want)
	}
}

func TestEncodeOmitsEmptyID(t *testing.T) {
	out := Encode("message", "", []byte("hi"))
	want := []byte("data: hi\n\n")
	if !bytes.Equal(out, want) {
		t.Errorf("Encode = %q, want %q", out, want)
	}
}

func TestEncodeMinimal(t *testing.T) {
	out := Encode("", "", []byte("hi"))
	want := []byte("data: hi\n\n")
	if !bytes.Equal(out, want) {
		t.Errorf("Encode = %q, want %q", out, want)
	}
}
