package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPreview(t *testing.T) {
	testCases := []struct {
		name string
		body []byte
		n    int
		want string
	}{
		{name: "empty", body: []byte{}, n: 64, want: ""},
		{name: "shorter_than_n", body: []byte("hello"), n: 64, want: "hello"},
		{name: "longer_than_n_truncates", body: []byte("abcdefghij"), n: 4, want: "abcd"},
		{name: "non_printable_replaced", body: []byte{'a', 0x00, 'b', 0x1f, 'c', 0x7f, 'd'}, n: 64, want: "a.b.c.d"},
		{name: "unicode_replaced", body: []byte("a\xc3\xa9b"), n: 64, want: "a..b"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := Preview(tc.body, tc.n)
			if got != tc.want {
				t.Errorf("Preview() = %q, want %q", got, tc.want)
			}
		})
	}
}

// fakeParser is a test stub for the Parser interface.
type fakeParser struct {
	name    string
	matches bool
	reason  string
}

func (f *fakeParser) CanParse(_ context.Context, _ []byte, _ map[string]string) (bool, error) {
	if f.matches {
		return true, nil
	}
	if f.reason == "" {
		return false, nil
	}
	return false, errors.New(f.reason)
}

func (f *fakeParser) ParseRequest(_ context.Context, _ []byte) ([]TextExtraction, bool, error) {
	return nil, false, nil
}
func (f *fakeParser) ParseResponse(_ context.Context, _ []byte) ([]TextExtraction, bool, error) {
	return nil, false, nil
}
func (f *fakeParser) ReplaceTexts(_ context.Context, body []byte, _ map[string]string) ([]byte, error) {
	return body, nil
}

func TestSelectParser_Match(t *testing.T) {
	a := &fakeParser{name: "a", matches: false, reason: "nope-a"}
	b := &fakeParser{name: "b", matches: true}

	reg := NewRegistry(a, b)
	got, err := reg.SelectParser(context.Background(), []byte("hello"), nil)

	if err != nil {
		t.Fatalf("SelectParser() error = %v, want nil", err)
	}
	if got != b {
		t.Fatalf("SelectParser() = %v, want b", got)
	}
}

func TestSelectParser_NoMatch_ReturnsError(t *testing.T) {
	a := &fakeParser{name: "a", matches: false, reason: "nope-a"}
	b := &fakeParser{name: "b", matches: false, reason: "nope-b"}

	reg := NewRegistry(a, b)
	got, err := reg.SelectParser(context.Background(), []byte("hello\x00world"), nil)

	if got != nil {
		t.Fatalf("SelectParser() parser = %v, want nil", got)
	}
	var nm *NoParserMatchError
	if !errors.As(err, &nm) {
		t.Fatalf("SelectParser() err = %v, want *NoParserMatchError", err)
	}
	if nm.BodySize != 11 {
		t.Errorf("BodySize = %d, want 11", nm.BodySize)
	}
	if nm.Prefix != "hello.world" {
		t.Errorf("Prefix = %q, want %q", nm.Prefix, "hello.world")
	}
	joined := strings.Join(nm.Reasons, "|")
	for _, want := range []string{"nope-a", "nope-b"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Reasons missing %q; got %v", want, nm.Reasons)
		}
	}
	// Error string still includes the diagnostic for callers that just log err.
	if !strings.Contains(err.Error(), "no parser matched") {
		t.Errorf("Error() = %q, missing summary", err.Error())
	}
}
