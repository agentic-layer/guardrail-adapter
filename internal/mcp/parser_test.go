package mcp

import (
	"encoding/json"
	"testing"
)

// TestExtractTexts_ToolsCallRequest tests text extraction from tools/call requests.
func TestExtractTexts_ToolsCallRequest(t *testing.T) {
	testCases := []struct {
		name          string
		body          string
		shouldInspect bool
		wantTexts     []TextExtraction
		wantError     bool
	}{
		{
			name: "simple_string_argument",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"method": "tools/call",
				"params": {
					"name": "search",
					"arguments": {
						"query": "test search"
					}
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "params.arguments.query", Value: "test search"},
			},
		},
		{
			name: "multiple_string_arguments",
			body: `{
				"jsonrpc": "2.0",
				"id": 2,
				"method": "tools/call",
				"params": {
					"name": "create_user",
					"arguments": {
						"name": "John Doe",
						"email": "john@example.com",
						"role": "admin"
					}
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "params.arguments.email", Value: "john@example.com"},
				{Path: "params.arguments.name", Value: "John Doe"},
				{Path: "params.arguments.role", Value: "admin"},
			},
		},
		{
			name: "nested_object_arguments",
			body: `{
				"jsonrpc": "2.0",
				"id": 3,
				"method": "tools/call",
				"params": {
					"name": "update_profile",
					"arguments": {
						"user": {
							"name": "Jane Smith",
							"address": {
								"street": "123 Main St",
								"city": "Boston"
							}
						}
					}
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "params.arguments.user.name", Value: "Jane Smith"},
				{Path: "params.arguments.user.address.street", Value: "123 Main St"},
				{Path: "params.arguments.user.address.city", Value: "Boston"},
			},
		},
		{
			name: "array_arguments",
			body: `{
				"jsonrpc": "2.0",
				"id": 4,
				"method": "tools/call",
				"params": {
					"name": "batch_process",
					"arguments": {
						"items": ["first item", "second item", "third item"]
					}
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "params.arguments.items[0]", Value: "first item"},
				{Path: "params.arguments.items[1]", Value: "second item"},
				{Path: "params.arguments.items[2]", Value: "third item"},
			},
		},
		{
			name: "mixed_types_only_strings_extracted",
			body: `{
				"jsonrpc": "2.0",
				"id": 5,
				"method": "tools/call",
				"params": {
					"name": "calculate",
					"arguments": {
						"operation": "add",
						"x": 10,
						"y": 20,
						"comment": "adding numbers"
					}
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "params.arguments.comment", Value: "adding numbers"},
				{Path: "params.arguments.operation", Value: "add"},
			},
		},
		{
			name: "empty_arguments",
			body: `{
				"jsonrpc": "2.0",
				"id": 6,
				"method": "tools/call",
				"params": {
					"name": "ping",
					"arguments": {}
				}
			}`,
			shouldInspect: true,
			wantTexts:     []TextExtraction{},
		},
		{
			name: "no_arguments",
			body: `{
				"jsonrpc": "2.0",
				"id": 7,
				"method": "tools/call",
				"params": {
					"name": "status"
				}
			}`,
			shouldInspect: true,
			wantTexts:     []TextExtraction{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ExtractTexts([]byte(tc.body))

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ExtractTexts() error = %v, want nil", err)
			}

			if result.ShouldInspect != tc.shouldInspect {
				t.Errorf("ShouldInspect = %v, want %v", result.ShouldInspect, tc.shouldInspect)
			}

			if len(result.Texts) != len(tc.wantTexts) {
				t.Errorf("got %d texts, want %d", len(result.Texts), len(tc.wantTexts))
			}

			// Convert to map for easier comparison (order may vary for map iteration)
			gotMap := make(map[string]string)
			for _, te := range result.Texts {
				gotMap[te.Path] = te.Value
			}

			for _, want := range tc.wantTexts {
				if got, ok := gotMap[want.Path]; !ok {
					t.Errorf("missing path %s", want.Path)
				} else if got != want.Value {
					t.Errorf("at path %s: got %q, want %q", want.Path, got, want.Value)
				}
			}
		})
	}
}

// TestExtractTexts_ToolsCallResponse tests text extraction from tool call responses.
func TestExtractTexts_ToolsCallResponse(t *testing.T) {
	testCases := []struct {
		name          string
		body          string
		shouldInspect bool
		wantTexts     []TextExtraction
		wantError     bool
	}{
		{
			name: "single_text_content",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"result": {
					"content": [
						{
							"type": "text",
							"text": "Search results: Found 5 items"
						}
					]
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "result.content[0].text", Value: "Search results: Found 5 items"},
			},
		},
		{
			name: "multiple_text_content",
			body: `{
				"jsonrpc": "2.0",
				"id": 2,
				"result": {
					"content": [
						{
							"type": "text",
							"text": "First response"
						},
						{
							"type": "text",
							"text": "Second response"
						},
						{
							"type": "text",
							"text": "Third response"
						}
					]
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "result.content[0].text", Value: "First response"},
				{Path: "result.content[1].text", Value: "Second response"},
				{Path: "result.content[2].text", Value: "Third response"},
			},
		},
		{
			name: "mixed_content_types",
			body: `{
				"jsonrpc": "2.0",
				"id": 3,
				"result": {
					"content": [
						{
							"type": "text",
							"text": "Text content"
						},
						{
							"type": "image",
							"data": "base64data"
						},
						{
							"type": "text",
							"text": "More text"
						}
					]
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "result.content[0].text", Value: "Text content"},
				{Path: "result.content[2].text", Value: "More text"},
			},
		},
		{
			name: "empty_content_array",
			body: `{
				"jsonrpc": "2.0",
				"id": 4,
				"result": {
					"content": []
				}
			}`,
			shouldInspect: false,
			wantTexts:     []TextExtraction{},
		},
		{
			name: "error_response",
			body: `{
				"jsonrpc": "2.0",
				"id": 5,
				"error": {
					"code": -32600,
					"message": "Invalid Request"
				}
			}`,
			shouldInspect: false,
			wantTexts:     []TextExtraction{},
		},
		{
			name: "empty_text_fields_ignored",
			body: `{
				"jsonrpc": "2.0",
				"id": 6,
				"result": {
					"content": [
						{
							"type": "text",
							"text": ""
						},
						{
							"type": "text",
							"text": "Valid text"
						}
					]
				}
			}`,
			shouldInspect: true,
			wantTexts: []TextExtraction{
				{Path: "result.content[1].text", Value: "Valid text"},
			},
		},
		{
			name: "non_tool_result_structure",
			body: `{
				"jsonrpc": "2.0",
				"id": 7,
				"result": {
					"status": "ok",
					"data": "some data"
				}
			}`,
			shouldInspect: false,
			wantTexts:     []TextExtraction{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ExtractTexts([]byte(tc.body))

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ExtractTexts() error = %v, want nil", err)
			}

			if result.ShouldInspect != tc.shouldInspect {
				t.Errorf("ShouldInspect = %v, want %v", result.ShouldInspect, tc.shouldInspect)
			}

			if len(result.Texts) != len(tc.wantTexts) {
				t.Errorf("got %d texts, want %d", len(result.Texts), len(tc.wantTexts))
			}

			for i, want := range tc.wantTexts {
				if i >= len(result.Texts) {
					t.Errorf("missing text at index %d", i)
					continue
				}
				got := result.Texts[i]
				if got.Path != want.Path {
					t.Errorf("text[%d].Path = %q, want %q", i, got.Path, want.Path)
				}
				if got.Value != want.Value {
					t.Errorf("text[%d].Value = %q, want %q", i, got.Value, want.Value)
				}
			}
		})
	}
}

// TestExtractTexts_OtherMethods tests that other MCP methods return ShouldInspect=false.
func TestExtractTexts_OtherMethods(t *testing.T) {
	testCases := []struct {
		name   string
		body   string
		method string
	}{
		{
			name:   "tools_list",
			method: "tools/list",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"method": "tools/list"
			}`,
		},
		{
			name:   "initialize",
			method: "initialize",
			body: `{
				"jsonrpc": "2.0",
				"id": 2,
				"method": "initialize",
				"params": {
					"protocolVersion": "1.0",
					"clientInfo": {
						"name": "test-client",
						"version": "1.0.0"
					}
				}
			}`,
		},
		{
			name:   "ping",
			method: "ping",
			body: `{
				"jsonrpc": "2.0",
				"id": 3,
				"method": "ping"
			}`,
		},
		{
			name:   "resources_list",
			method: "resources/list",
			body: `{
				"jsonrpc": "2.0",
				"id": 4,
				"method": "resources/list"
			}`,
		},
		{
			name:   "prompts_list",
			method: "prompts/list",
			body: `{
				"jsonrpc": "2.0",
				"id": 5,
				"method": "prompts/list"
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ExtractTexts([]byte(tc.body))

			if err != nil {
				t.Fatalf("ExtractTexts() error = %v, want nil", err)
			}

			if result.ShouldInspect {
				t.Errorf("ShouldInspect = true for method %s, want false", tc.method)
			}

			if len(result.Texts) != 0 {
				t.Errorf("got %d texts for non-inspectable method, want 0", len(result.Texts))
			}
		})
	}
}

// TestReplaceTexts tests text replacement functionality.
func TestReplaceTexts(t *testing.T) {
	testCases := []struct {
		name         string
		body         string
		replacements map[string]string
		wantBody     string
		wantError    bool
	}{
		{
			name: "replace_request_argument",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"method": "tools/call",
				"params": {
					"name": "search",
					"arguments": {
						"query": "sensitive data"
					}
				}
			}`,
			replacements: map[string]string{
				"params.arguments.query": "[REDACTED]",
			},
			wantBody: `{"id":1,"jsonrpc":"2.0","method":"tools/call","params":{"arguments":{"query":"[REDACTED]"},"name":"search"}}`,
		},
		{
			name: "replace_multiple_fields",
			body: `{
				"jsonrpc": "2.0",
				"id": 2,
				"method": "tools/call",
				"params": {
					"name": "create_user",
					"arguments": {
						"name": "John Doe",
						"email": "john@example.com",
						"ssn": "123-45-6789"
					}
				}
			}`,
			replacements: map[string]string{
				"params.arguments.email": "[EMAIL_REDACTED]",
				"params.arguments.ssn":   "[SSN_REDACTED]",
			},
			wantBody: `{"id":2,"jsonrpc":"2.0","method":"tools/call","params":{"arguments":{"email":"[EMAIL_REDACTED]","name":"John Doe","ssn":"[SSN_REDACTED]"},"name":"create_user"}}`,
		},
		{
			name: "replace_nested_field",
			body: `{
				"jsonrpc": "2.0",
				"id": 3,
				"method": "tools/call",
				"params": {
					"name": "update_profile",
					"arguments": {
						"user": {
							"name": "Jane Smith",
							"address": {
								"street": "123 Main St"
							}
						}
					}
				}
			}`,
			replacements: map[string]string{
				"params.arguments.user.address.street": "[ADDRESS_REDACTED]",
			},
			wantBody: `{"id":3,"jsonrpc":"2.0","method":"tools/call","params":{"arguments":{"user":{"address":{"street":"[ADDRESS_REDACTED]"},"name":"Jane Smith"}},"name":"update_profile"}}`,
		},
		{
			name: "replace_array_element",
			body: `{
				"jsonrpc": "2.0",
				"id": 4,
				"result": {
					"content": [
						{
							"type": "text",
							"text": "First response"
						},
						{
							"type": "text",
							"text": "Second response"
						}
					]
				}
			}`,
			replacements: map[string]string{
				"result.content[0].text": "[REDACTED_1]",
				"result.content[1].text": "[REDACTED_2]",
			},
			wantBody: `{"id":4,"jsonrpc":"2.0","result":{"content":[{"text":"[REDACTED_1]","type":"text"},{"text":"[REDACTED_2]","type":"text"}]}}`,
		},
		{
			name: "invalid_json",
			body: `{invalid json}`,
			replacements: map[string]string{
				"params.query": "test",
			},
			wantError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ReplaceTexts([]byte(tc.body), tc.replacements)

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ReplaceTexts() error = %v, want nil", err)
			}

			// Parse both to compare as JSON objects (to ignore formatting differences)
			var gotJSON, wantJSON interface{}
			if err := json.Unmarshal(result, &gotJSON); err != nil {
				t.Fatalf("failed to parse result JSON: %v", err)
			}
			if err := json.Unmarshal([]byte(tc.wantBody), &wantJSON); err != nil {
				t.Fatalf("failed to parse expected JSON: %v", err)
			}

			gotBytes, _ := json.Marshal(gotJSON)
			wantBytes, _ := json.Marshal(wantJSON)

			if string(gotBytes) != string(wantBytes) {
				t.Errorf("ReplaceTexts() = %s, want %s", string(gotBytes), string(wantBytes))
			}
		})
	}
}

// TestExtractTexts_InvalidJSON tests handling of invalid JSON.
func TestExtractTexts_InvalidJSON(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{
			name: "malformed_json",
			body: `{invalid json`,
		},
		{
			name: "empty_string",
			body: ``,
		},
		{
			name: "non_json",
			body: `this is not json`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExtractTexts([]byte(tc.body))
			if err == nil {
				t.Fatal("expected error for invalid JSON, got nil")
			}
		})
	}
}

// TestParsePath tests the path parsing functionality.
func TestParsePath(t *testing.T) {
	testCases := []struct {
		path string
		want []string
	}{
		{
			path: "params.arguments.query",
			want: []string{"params", "arguments", "query"},
		},
		{
			path: "result.content[0].text",
			want: []string{"result", "content", "0", "text"},
		},
		{
			path: "data.items[2].nested.field",
			want: []string{"data", "items", "2", "nested", "field"},
		},
		{
			path: "simple",
			want: []string{"simple"},
		},
		{
			path: "array[10]",
			want: []string{"array", "10"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			got := parsePath(tc.path)

			if len(got) != len(tc.want) {
				t.Errorf("parsePath(%q) returned %d segments, want %d", tc.path, len(got), len(tc.want))
				return
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("parsePath(%q)[%d] = %q, want %q", tc.path, i, got[i], tc.want[i])
				}
			}
		})
	}
}
