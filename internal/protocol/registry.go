package protocol

import (
	"context"
	"fmt"
	"strings"
)

// Registry manages protocol parsers and selects the appropriate one based on content.
type Registry struct {
	parsers []Parser
}

// NewRegistry creates a new protocol parser registry.
func NewRegistry(parsers ...Parser) *Registry {
	return &Registry{
		parsers: parsers,
	}
}

// NoParserMatchError is returned by Registry.SelectParser when no registered
// parser matched the body. It carries diagnostic detail the caller can log.
type NoParserMatchError struct {
	BodySize int
	Prefix   string   // already sanitized via Preview
	Reasons  []string // one entry per parser that returned an error from CanParse
}

func (e *NoParserMatchError) Error() string {
	return fmt.Sprintf("no parser matched body (size=%d, prefix=%q, reasons=[%s])",
		e.BodySize, e.Prefix, strings.Join(e.Reasons, "; "))
}

// SelectParser selects the appropriate parser for the given body.
// On match, returns (parser, nil). On no match, returns (nil, *NoParserMatchError)
// carrying the body size, sanitized prefix, and per-parser rejection reasons.
func (r *Registry) SelectParser(ctx context.Context, body []byte, metadata map[string]string) (Parser, error) {
	var reasons []string
	for _, parser := range r.parsers {
		ok, err := parser.CanParse(ctx, body, metadata)
		if ok {
			return parser, nil
		}
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%T: %v", parser, err))
		}
	}
	return nil, &NoParserMatchError{
		BodySize: len(body),
		Prefix:   Preview(body, 64),
		Reasons:  reasons,
	}
}

// AddParser adds a new parser to the registry.
func (r *Registry) AddParser(parser Parser) {
	r.parsers = append(r.parsers, parser)
}

// Preview returns up to n bytes from body, replacing non-printable
// bytes (outside 0x20-0x7e) with '.' so the result is safe to log.
func Preview(body []byte, n int) string {
	if n <= 0 {
		return ""
	}
	if len(body) < n {
		n = len(body)
	}
	out := make([]byte, n)
	for i, b := range body[:n] {
		if b < 0x20 || b > 0x7e {
			out[i] = '.'
		} else {
			out[i] = b
		}
	}
	return string(out)
}
