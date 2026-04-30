package protocol

import (
	"context"
	"fmt"
	"log"
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

// SelectParser selects the appropriate parser for the given body.
// Returns nil if no parser can handle the body. When no parser matches,
// logs an info line with the body size, a sanitized prefix, and each
// parser's rejection reason for diagnostics.
func (r *Registry) SelectParser(ctx context.Context, body []byte, metadata map[string]string) Parser {
	var reasons []string
	for _, parser := range r.parsers {
		ok, err := parser.CanParse(ctx, body, metadata)
		if ok {
			return parser
		}
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%T: %v", parser, err))
		}
	}
	log.Printf("protocol: no parser matched body (size=%d, prefix=%q, reasons=[%s])",
		len(body), preview(body, 64), strings.Join(reasons, "; "))
	return nil
}

// AddParser adds a new parser to the registry.
func (r *Registry) AddParser(parser Parser) {
	r.parsers = append(r.parsers, parser)
}

// preview returns up to n bytes from body, replacing non-printable
// bytes (outside 0x20-0x7e) with '.' so the result is safe to log.
func preview(body []byte, n int) string {
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
