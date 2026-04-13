package mcp

import (
	"encoding/json"
	"testing"
)

// TestParseRequest tests parsing of JSON-RPC requests.
func TestParseRequest(t *testing.T) {
	testCases := []struct {
		name       string
		body       string
		wantError  bool
		wantMethod string
	}{
		{
			name: "valid_tools_call_request",
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
			wantMethod: "tools/call",
		},
		{
			name: "valid_tools_list_request",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"method": "tools/list"
			}`,
			wantMethod: "tools/list",
		},
		{
			name:      "invalid_json",
			body:      `{invalid json}`,
			wantError: true,
		},
		{
			name: "missing_method",
			body: `{
				"jsonrpc": "2.0",
				"id": 1
			}`,
			wantError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ParseRequest([]byte(tc.body))

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseRequest() error = %v, want nil", err)
			}

			if req.Method != tc.wantMethod {
				t.Errorf("Method = %q, want %q", req.Method, tc.wantMethod)
			}
		})
	}
}

// TestParseResponse tests parsing of JSON-RPC responses.
func TestParseResponse(t *testing.T) {
	testCases := []struct {
		name      string
		body      string
		wantError bool
		hasError  bool
		hasResult bool
	}{
		{
			name: "valid_success_response",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"result": {
					"content": [
						{
							"type": "text",
							"text": "Search results"
						}
					]
				}
			}`,
			hasResult: true,
		},
		{
			name: "valid_error_response",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"error": {
					"code": -32600,
					"message": "Invalid Request"
				}
			}`,
			hasError: true,
		},
		{
			name:      "invalid_json",
			body:      `{invalid json}`,
			wantError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := ParseResponse([]byte(tc.body))

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseResponse() error = %v, want nil", err)
			}

			if tc.hasError && resp.Error == nil {
				t.Error("expected error in response, got nil")
			}

			if tc.hasResult && resp.Result == nil {
				t.Error("expected result in response, got nil")
			}
		})
	}
}

// TestParseToolsCallParams tests parsing of tools/call request params.
func TestParseToolsCallParams(t *testing.T) {
	testCases := []struct {
		name      string
		body      string
		wantError bool
		wantName  string
	}{
		{
			name: "valid_tools_call",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"method": "tools/call",
				"params": {
					"name": "search",
					"arguments": {
						"query": "test"
					}
				}
			}`,
			wantName: "search",
		},
		{
			name: "not_tools_call",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"method": "tools/list"
			}`,
			wantError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ParseRequest([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseRequest() error = %v", err)
			}

			params, err := ParseToolsCallParams(req)

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseToolsCallParams() error = %v, want nil", err)
			}

			if params.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", params.Name, tc.wantName)
			}
		})
	}
}

// TestParseToolCallResult tests parsing of tool call response results.
func TestParseToolCallResult(t *testing.T) {
	testCases := []struct {
		name        string
		body        string
		wantError   bool
		wantContent int
	}{
		{
			name: "valid_result",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"result": {
					"content": [
						{
							"type": "text",
							"text": "Result text"
						}
					]
				}
			}`,
			wantContent: 1,
		},
		{
			name: "error_response",
			body: `{
				"jsonrpc": "2.0",
				"id": 1,
				"error": {
					"code": -32600,
					"message": "Invalid Request"
				}
			}`,
			wantError: true,
		},
		{
			name: "missing_result",
			body: `{
				"jsonrpc": "2.0",
				"id": 1
			}`,
			wantError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := ParseResponse([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseResponse() error = %v", err)
			}

			result, err := ParseToolCallResult(resp)

			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseToolCallResult() error = %v, want nil", err)
			}

			if len(result.Content) != tc.wantContent {
				t.Errorf("Content length = %d, want %d", len(result.Content), tc.wantContent)
			}
		})
	}
}

// TestExtractTextsFromToolCallRequest tests text extraction from tool call requests.
func TestExtractTextsFromToolCallRequest(t *testing.T) {
	testCases := []struct {
		name      string
		body      string
		wantTexts []TextExtraction
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
			wantTexts: []TextExtraction{},
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
			wantTexts: []TextExtraction{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ParseRequest([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseRequest() error = %v", err)
			}

			params, err := ParseToolsCallParams(req)
			if err != nil {
				t.Fatalf("ParseToolsCallParams() error = %v", err)
			}

			texts := ExtractTextsFromToolCallRequest(params)

			if len(texts) != len(tc.wantTexts) {
				t.Errorf("got %d texts, want %d", len(texts), len(tc.wantTexts))
			}

			// Convert to map for easier comparison (order may vary for map iteration)
			gotMap := make(map[string]string)
			for _, te := range texts {
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

// TestExtractTextsFromToolCallResponse tests text extraction from tool call responses.
func TestExtractTextsFromToolCallResponse(t *testing.T) {
	testCases := []struct {
		name      string
		body      string
		wantTexts []TextExtraction
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
			wantTexts: []TextExtraction{},
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
			wantTexts: []TextExtraction{
				{Path: "result.content[1].text", Value: "Valid text"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := ParseResponse([]byte(tc.body))
			if err != nil {
				t.Fatalf("ParseResponse() error = %v", err)
			}

			result, err := ParseToolCallResult(resp)
			if err != nil {
				t.Fatalf("ParseToolCallResult() error = %v", err)
			}

			texts := ExtractTextsFromToolCallResponse(result)

			if len(texts) != len(tc.wantTexts) {
				t.Errorf("got %d texts, want %d", len(texts), len(tc.wantTexts))
			}

			for i, want := range tc.wantTexts {
				if i >= len(texts) {
					t.Errorf("missing text at index %d", i)
					continue
				}
				got := texts[i]
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
