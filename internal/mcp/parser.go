package mcp

import (
	"encoding/json"
	"fmt"
)

// TextExtraction represents a text field extracted from the message with its JSON path.
type TextExtraction struct {
	Path  string // JSON path to the text field (e.g., "params.arguments.query")
	Value string // The extracted text value
}

// Request represents a parsed JSON-RPC 2.0 request message.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a parsed JSON-RPC 2.0 response message.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error represents a JSON-RPC 2.0 error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ToolsCallParams represents the params for a tools/call request.
type ToolsCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// ToolCallResult represents the result of a tools/call response.
type ToolCallResult struct {
	Content []ContentItem `json:"content,omitempty"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem represents an item in the content array.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ParseRequest parses a JSON-RPC 2.0 request from raw bytes.
func ParseRequest(body []byte) (*Request, error) {
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to parse JSON-RPC request: %w", err)
	}
	if req.Method == "" {
		return nil, fmt.Errorf("invalid JSON-RPC request: missing method")
	}
	return &req, nil
}

// ParseResponse parses a JSON-RPC 2.0 response from raw bytes.
func ParseResponse(body []byte) (*Response, error) {
	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON-RPC response: %w", err)
	}
	return &resp, nil
}

// ParseToolsCallParams parses the params of a tools/call request.
func ParseToolsCallParams(req *Request) (*ToolsCallParams, error) {
	if req.Method != "tools/call" {
		return nil, fmt.Errorf("not a tools/call request: method is %s", req.Method)
	}
	var params ToolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("failed to parse tools/call params: %w", err)
	}
	return &params, nil
}

// ParseToolCallResult parses the result of a tools/call response.
func ParseToolCallResult(resp *Response) (*ToolCallResult, error) {
	if resp.Error != nil {
		return nil, fmt.Errorf("response contains error: %s", resp.Error.Message)
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("response missing result")
	}
	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tool call result: %w", err)
	}
	return &result, nil
}

// ExtractTextsFromToolCallRequest extracts all string values from a tools/call request's arguments.
func ExtractTextsFromToolCallRequest(params *ToolsCallParams) []TextExtraction {
	texts := []TextExtraction{}
	extractStringsFromMap("params.arguments", params.Arguments, &texts)
	return texts
}

// ExtractTextsFromToolCallResponse extracts text fields from a tools/call response's content array.
func ExtractTextsFromToolCallResponse(result *ToolCallResult) []TextExtraction {
	texts := []TextExtraction{}
	for i, item := range result.Content {
		if item.Type == "text" && item.Text != "" {
			texts = append(texts, TextExtraction{
				Path:  fmt.Sprintf("result.content[%d].text", i),
				Value: item.Text,
			})
		}
	}
	return texts
}

// extractStringsFromMap recursively extracts string values from a map.
func extractStringsFromMap(basePath string, m map[string]interface{}, texts *[]TextExtraction) {
	for key, value := range m {
		path := basePath + "." + key
		extractStringsFromValue(path, value, texts)
	}
}

// extractStringsFromValue extracts strings from any JSON value type.
func extractStringsFromValue(path string, value interface{}, texts *[]TextExtraction) {
	switch v := value.(type) {
	case string:
		if v != "" {
			*texts = append(*texts, TextExtraction{
				Path:  path,
				Value: v,
			})
		}
	case map[string]interface{}:
		extractStringsFromMap(path, v, texts)
	case []interface{}:
		for i, item := range v {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			extractStringsFromValue(itemPath, item, texts)
		}
	}
}

// ReplaceTexts replaces text at specified JSON paths in the message body.
// This is used for MASK actions in guardrails.
func ReplaceTexts(body []byte, replacements map[string]string) ([]byte, error) {
	// Parse the message
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Apply replacements
	for path, newValue := range replacements {
		if err := setValueAtPath(raw, path, newValue); err != nil {
			return nil, fmt.Errorf("failed to replace at path %s: %w", path, err)
		}
	}

	// Marshal back to JSON
	result, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return result, nil
}

// setValueAtPath sets a value at the specified JSON path.
func setValueAtPath(obj map[string]interface{}, path string, value string) error {
	// Parse the path and navigate to the target location
	return setValueAtPathRecursive(obj, parsePath(path), value)
}

// parsePath splits a JSON path into segments.
// For example: "params.arguments.query" -> ["params", "arguments", "query"]
// "result.content[0].text" -> ["result", "content", "0", "text"]
func parsePath(path string) []string {
	segments := []string{}
	current := ""

	for i := 0; i < len(path); i++ {
		ch := path[i]
		switch ch {
		case '.':
			if current != "" {
				segments = append(segments, current)
				current = ""
			}
		case '[':
			if current != "" {
				segments = append(segments, current)
				current = ""
			}
			// Find the closing bracket
			j := i + 1
			for j < len(path) && path[j] != ']' {
				j++
			}
			if j < len(path) {
				segments = append(segments, path[i+1:j])
				i = j
			}
		default:
			current += string(ch)
		}
	}

	if current != "" {
		segments = append(segments, current)
	}

	return segments
}

// setValueAtPathRecursive recursively navigates and sets the value.
func setValueAtPathRecursive(obj interface{}, segments []string, value string) error {
	if len(segments) == 0 {
		return fmt.Errorf("empty path")
	}

	if len(segments) == 1 {
		// Base case: set the value
		switch o := obj.(type) {
		case map[string]interface{}:
			o[segments[0]] = value
			return nil
		default:
			return fmt.Errorf("cannot set value on non-object at final segment")
		}
	}

	// Recursive case: navigate deeper
	segment := segments[0]
	remaining := segments[1:]

	switch o := obj.(type) {
	case map[string]interface{}:
		if next, ok := o[segment]; ok {
			return setValueAtPathRecursive(next, remaining, value)
		}
		return fmt.Errorf("path segment %s not found", segment)
	case []interface{}:
		// Parse segment as array index
		var idx int
		if _, err := fmt.Sscanf(segment, "%d", &idx); err != nil {
			return fmt.Errorf("invalid array index: %s", segment)
		}
		if idx < 0 || idx >= len(o) {
			return fmt.Errorf("array index out of bounds: %d", idx)
		}
		return setValueAtPathRecursive(o[idx], remaining, value)
	default:
		return fmt.Errorf("cannot navigate through non-object/array at segment %s", segment)
	}
}
