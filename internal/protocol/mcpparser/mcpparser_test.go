package mcpparser

import (
	"context"
	"strings"
	"testing"
)

func TestCanParse(t *testing.T) {
	testCases := []struct {
		name          string
		body          string
		wantOK        bool
		wantErrSubstr string // substring expected in error when wantOK=false
	}{
		{
			name:   "valid_mcp_request",
			body:   `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"query":"x"}}}`,
			wantOK: true,
		},
		{
			name:          "response_shape_rejected",
			body:          `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`,
			wantOK:        false,
			wantErrSubstr: "not an MCP request",
		},
		{
			name:          "invalid_json",
			body:          `{not json`,
			wantOK:        false,
			wantErrSubstr: "not an MCP request",
		},
		{
			name:          "valid_json_but_not_jsonrpc",
			body:          `{"hello":"world"}`,
			wantOK:        false,
			wantErrSubstr: "not an MCP request",
		},
		{
			name:          "request_missing_jsonrpc_field",
			body:          `{"id":1,"method":"tools/call","params":{}}`,
			wantOK:        false,
			wantErrSubstr: "missing or invalid jsonrpc field",
		},
	}

	p := NewMCPParser()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := p.CanParse(context.Background(), []byte(tc.body), nil)
			if ok != tc.wantOK {
				t.Errorf("CanParse() ok = %v, want %v (err=%v)", ok, tc.wantOK, err)
			}
			if tc.wantOK && err != nil {
				t.Errorf("CanParse() unexpected error on match: %v", err)
			}
			if !tc.wantOK {
				if err == nil {
					t.Fatalf("CanParse() returned nil error on rejection")
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Errorf("CanParse() err = %q, want substring %q", err.Error(), tc.wantErrSubstr)
				}
			}
		})
	}
}
