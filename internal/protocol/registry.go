package protocol

import "context"

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
// Returns nil if no parser can handle the body.
func (r *Registry) SelectParser(ctx context.Context, body []byte, metadata map[string]string) Parser {
	for _, parser := range r.parsers {
		if parser.CanParse(ctx, body, metadata) {
			return parser
		}
	}
	return nil
}

// AddParser adds a new parser to the registry.
func (r *Registry) AddParser(parser Parser) {
	r.parsers = append(r.parsers, parser)
}
