package protocol

import (
	"bytes"
	"context"
	"errors"
	"log"
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
			got := preview(tc.body, tc.n)
			if got != tc.want {
				t.Errorf("preview() = %q, want %q", got, tc.want)
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

	var logBuf bytes.Buffer
	prevOutput := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevOutput) })

	reg := NewRegistry(a, b)
	got := reg.SelectParser(context.Background(), []byte("hello"), nil)

	if got != b {
		t.Fatalf("SelectParser() = %v, want b", got)
	}
	if logBuf.Len() != 0 {
		t.Errorf("expected no log output on match, got: %s", logBuf.String())
	}
}

func TestSelectParser_NoMatch_LogsReasons(t *testing.T) {
	a := &fakeParser{name: "a", matches: false, reason: "nope-a"}
	b := &fakeParser{name: "b", matches: false, reason: "nope-b"}

	var logBuf bytes.Buffer
	prevOutput := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevOutput) })

	reg := NewRegistry(a, b)
	got := reg.SelectParser(context.Background(), []byte("hello\x00world"), nil)

	if got != nil {
		t.Fatalf("SelectParser() = %v, want nil", got)
	}
	out := logBuf.String()
	for _, want := range []string{"no parser matched", "size=11", `prefix="hello.world"`, "nope-a", "nope-b"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q; got: %s", want, out)
		}
	}
}
