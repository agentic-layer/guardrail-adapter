package mcpparser

import (
	"context"

	"github.com/agentic-layer/guardrail-adapter/internal/mcp"
	"github.com/agentic-layer/guardrail-adapter/internal/protocol"
)

// MCPParser implements the protocol.Parser interface for MCP (Model Context Protocol).
type MCPParser struct{}

// NewMCPParser creates a new MCP protocol parser.
func NewMCPParser() *MCPParser {
	return &MCPParser{}
}

// CanParse checks if this parser can handle the given body.
// MCP uses JSON-RPC 2.0 format, so we check for the "jsonrpc" field.
func (p *MCPParser) CanParse(ctx context.Context, body []byte, metadata map[string]string) bool {
	// Try to parse as MCP request/response
	_, err := mcp.ParseRequest(body)
	if err == nil {
		return true
	}
	_, err = mcp.ParseResponse(body)
	return err == nil
}

// ParseRequest parses an MCP request message and extracts text fields.
func (p *MCPParser) ParseRequest(ctx context.Context, body []byte) ([]protocol.TextExtraction, bool, error) {
	mcpReq, err := mcp.ParseRequest(body)
	if err != nil {
		return nil, false, err
	}

	// Only process tools/call methods
	if mcpReq.Method != "tools/call" {
		return nil, false, nil
	}

	params, err := mcp.ParseToolsCallParams(mcpReq)
	if err != nil {
		return nil, false, err
	}

	texts := mcp.ExtractTextsFromToolCallRequest(params)

	// Convert mcp.TextExtraction to protocol.TextExtraction
	result := make([]protocol.TextExtraction, len(texts))
	for i, t := range texts {
		result[i] = protocol.TextExtraction{
			Path:  t.Path,
			Value: t.Value,
		}
	}

	return result, len(result) > 0, nil
}

// ParseResponse parses an MCP response message and extracts text fields.
func (p *MCPParser) ParseResponse(ctx context.Context, body []byte) ([]protocol.TextExtraction, bool, error) {
	mcpResp, err := mcp.ParseResponse(body)
	if err != nil {
		return nil, false, err
	}

	// Skip error responses
	if mcpResp.Error != nil {
		return nil, false, nil
	}

	result, err := mcp.ParseToolCallResult(mcpResp)
	if err != nil {
		return nil, false, err
	}

	texts := mcp.ExtractTextsFromToolCallResponse(result)

	// Convert mcp.TextExtraction to protocol.TextExtraction
	extracted := make([]protocol.TextExtraction, len(texts))
	for i, t := range texts {
		extracted[i] = protocol.TextExtraction{
			Path:  t.Path,
			Value: t.Value,
		}
	}

	return extracted, len(extracted) > 0, nil
}

// ReplaceTexts replaces text at specified paths in the MCP message body.
func (p *MCPParser) ReplaceTexts(ctx context.Context, body []byte, replacements map[string]string) ([]byte, error) {
	return mcp.ReplaceTexts(body, replacements)
}
