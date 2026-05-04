package extproc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/agentic-layer/guardrail-adapter/internal/metadata"
)

// mockProcessStream is a mock implementation of ExternalProcessor_ProcessServer.
type mockProcessStream struct {
	grpc.ServerStream
	ctx      context.Context
	requests []*extprocv3.ProcessingRequest
	sent     []*extprocv3.ProcessingResponse
	recvIdx  int
}

func (m *mockProcessStream) Context() context.Context {
	return m.ctx
}

func (m *mockProcessStream) Recv() (*extprocv3.ProcessingRequest, error) {
	if m.recvIdx >= len(m.requests) {
		return nil, io.EOF
	}
	req := m.requests[m.recvIdx]
	m.recvIdx++
	return req, nil
}

func (m *mockProcessStream) Send(resp *extprocv3.ProcessingResponse) error {
	m.sent = append(m.sent, resp)
	return nil
}

// TestPassthroughBehavior verifies that the server passes through all request types without modification.
func TestPassthroughBehavior(t *testing.T) {
	server := NewServer(nil, nil, 0)

	testCases := []struct {
		name     string
		request  *extprocv3.ProcessingRequest
		wantType string
	}{
		{
			name: "request_headers",
			request: &extprocv3.ProcessingRequest{
				Request: &extprocv3.ProcessingRequest_RequestHeaders{
					RequestHeaders: &extprocv3.HttpHeaders{},
				},
			},
			wantType: "RequestHeaders",
		},
		{
			name: "request_body",
			request: &extprocv3.ProcessingRequest{
				Request: &extprocv3.ProcessingRequest_RequestBody{
					RequestBody: &extprocv3.HttpBody{},
				},
			},
			wantType: "RequestBody",
		},
		{
			name: "response_headers",
			request: &extprocv3.ProcessingRequest{
				Request: &extprocv3.ProcessingRequest_ResponseHeaders{
					ResponseHeaders: &extprocv3.HttpHeaders{},
				},
			},
			wantType: "ResponseHeaders",
		},
		{
			name: "response_body",
			request: &extprocv3.ProcessingRequest{
				Request: &extprocv3.ProcessingRequest_ResponseBody{
					ResponseBody: &extprocv3.HttpBody{},
				},
			},
			wantType: "ResponseBody",
		},
		{
			name: "request_trailers",
			request: &extprocv3.ProcessingRequest{
				Request: &extprocv3.ProcessingRequest_RequestTrailers{
					RequestTrailers: &extprocv3.HttpTrailers{},
				},
			},
			wantType: "RequestTrailers",
		},
		{
			name: "response_trailers",
			request: &extprocv3.ProcessingRequest{
				Request: &extprocv3.ProcessingRequest_ResponseTrailers{
					ResponseTrailers: &extprocv3.HttpTrailers{},
				},
			},
			wantType: "ResponseTrailers",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stream := &mockProcessStream{
				ctx:      context.Background(),
				requests: []*extprocv3.ProcessingRequest{tc.request},
				sent:     []*extprocv3.ProcessingResponse{},
			}

			err := server.Process(stream)
			if err != nil {
				t.Fatalf("Process() error = %v, want nil", err)
			}

			if len(stream.sent) != 1 {
				t.Fatalf("sent %d responses, want 1", len(stream.sent))
			}

			resp := stream.sent[0]
			if resp.Response == nil {
				t.Fatal("response is nil")
			}

			// Verify the response type matches the request type
			switch tc.wantType {
			case "RequestHeaders":
				if _, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestHeaders); !ok {
					t.Errorf("expected RequestHeaders response, got %T", resp.Response)
				}
			case "RequestBody":
				if _, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody); !ok {
					t.Errorf("expected RequestBody response, got %T", resp.Response)
				}
			case "ResponseHeaders":
				if _, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseHeaders); !ok {
					t.Errorf("expected ResponseHeaders response, got %T", resp.Response)
				}
			case "ResponseBody":
				if _, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody); !ok {
					t.Errorf("expected ResponseBody response, got %T", resp.Response)
				}
			case "RequestTrailers":
				if _, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestTrailers); !ok {
					t.Errorf("expected RequestTrailers response, got %T", resp.Response)
				}
			case "ResponseTrailers":
				if _, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseTrailers); !ok {
					t.Errorf("expected ResponseTrailers response, got %T", resp.Response)
				}
			}
		})
	}
}

// TestProcessStreamError verifies error handling in the Process stream.
func TestProcessStreamError(t *testing.T) {
	server := NewServer(nil, nil, 0)

	t.Run("context_cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		stream := &mockProcessStream{
			ctx:      ctx,
			requests: []*extprocv3.ProcessingRequest{},
		}

		err := server.Process(stream)
		if err != context.Canceled {
			t.Errorf("expected context.Canceled error, got %v", err)
		}
	})

	t.Run("empty_stream", func(t *testing.T) {
		stream := &mockProcessStream{
			ctx:      context.Background(),
			requests: []*extprocv3.ProcessingRequest{},
		}

		err := server.Process(stream)
		if err != nil {
			t.Errorf("Process() error = %v, want nil", err)
		}
	})
}

// mockFailingStream simulates stream failures.
type mockFailingStream struct {
	grpc.ServerStream
	ctx       context.Context
	sendError error
}

func (m *mockFailingStream) Context() context.Context {
	return m.ctx
}

func (m *mockFailingStream) Recv() (*extprocv3.ProcessingRequest, error) {
	return &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{},
		},
	}, nil
}

func (m *mockFailingStream) Send(resp *extprocv3.ProcessingResponse) error {
	return m.sendError
}

func TestProcessSendError(t *testing.T) {
	server := NewServer(nil, nil, 0)

	stream := &mockFailingStream{
		ctx:       context.Background(),
		sendError: status.Error(codes.Internal, "send failed"),
	}

	err := server.Process(stream)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected status error, got %v", err)
	}

	if st.Code() != codes.Unknown {
		t.Errorf("expected code %v, got %v", codes.Unknown, st.Code())
	}
}

// TestGuardrailIntegration tests the complete guardrail processing pipeline.
func TestGuardrailIntegration(t *testing.T) {
	// Setup mock Presidio server
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	testCases := []struct {
		name         string
		metadata     map[string]string
		requestBody  string
		wantBlocked  bool
		wantMasked   bool
		wantOriginal bool
	}{
		{
			name:     "passthrough_no_metadata",
			metadata: map[string]string{
				// No guardrail metadata
			},
			requestBody:  `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{"text":"hello world"}}}`,
			wantOriginal: true,
		},
		{
			name: "passthrough_non_tools_call",
			metadata: map[string]string{
				"guardrail.provider": "presidio-api",
				"guardrail.mode":     "pre_call",
			},
			requestBody:  `{"jsonrpc":"2.0","id":1,"method":"other/method","params":{"name":"test"}}`,
			wantOriginal: true,
		},
		{
			name: "block_with_ssn",
			metadata: map[string]string{
				"guardrail.provider":                  "presidio-api",
				"guardrail.mode":                      "pre_call",
				"guardrail.presidio.endpoint":         presidioServer.URL,
				"guardrail.presidio.language":         "en",
				"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
				"guardrail.presidio.entity_actions":   `{"US_SSN":"BLOCK"}`,
			},
			requestBody: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{"ssn":"123-45-6789"}}}`,
			wantBlocked: true,
		},
		{
			name: "mask_with_email",
			metadata: map[string]string{
				"guardrail.provider":                  "presidio-api",
				"guardrail.mode":                      "pre_call",
				"guardrail.presidio.endpoint":         presidioServer.URL,
				"guardrail.presidio.language":         "en",
				"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
				"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
			},
			requestBody: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{"email":"john@example.com"}}}`,
			wantMasked:  true,
		},
		{
			name: "allow_no_pii",
			metadata: map[string]string{
				"guardrail.provider":                  "presidio-api",
				"guardrail.mode":                      "pre_call",
				"guardrail.presidio.endpoint":         presidioServer.URL,
				"guardrail.presidio.language":         "en",
				"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
				"guardrail.presidio.entity_actions":   `{"ALL":"BLOCK"}`,
			},
			requestBody:  `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{"text":"hello world"}}}`,
			wantOriginal: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runGuardrailTestCase(t, tc)
		})
	}
}

// createMockPresidioServer creates a mock Presidio server for testing.
func createMockPresidioServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/analyze":
			handleMockAnalyzeRequest(t, w, r)
		case "/anonymize":
			handleMockAnonymizeRequest(t, w, r)
		}
	}))
}

// handleMockAnalyzeRequest handles /analyze requests for the mock server.
func handleMockAnalyzeRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("failed to decode analyze request: %v", err)
	}

	text := req["text"].(string)
	var results []interface{}

	// Detect PII in the text
	if contains(text, "john@example.com") {
		results = append(results, map[string]interface{}{
			"entity_type": "EMAIL_ADDRESS",
			"start":       findIndex(text, "john@example.com"),
			"end":         findIndex(text, "john@example.com") + len("john@example.com"),
			"score":       0.95,
		})
	}
	if contains(text, "123-45-6789") {
		results = append(results, map[string]interface{}{
			"entity_type": "US_SSN",
			"start":       findIndex(text, "123-45-6789"),
			"end":         findIndex(text, "123-45-6789") + len("123-45-6789"),
			"score":       0.95,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		t.Fatalf("failed to encode analyze response: %v", err)
	}
}

// handleMockAnonymizeRequest handles /anonymize requests for the mock server.
func handleMockAnonymizeRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("failed to decode anonymize request: %v", err)
	}

	text := req["text"].(string)
	results := req["analyzer_results"].([]interface{})

	// Replace detected entities with placeholders
	for _, result := range results {
		entityMap := result.(map[string]interface{})
		entityType := entityMap["entity_type"].(string)
		text = replaceAll(text, entityType)
	}

	// Build response items
	items := make([]interface{}, len(results))
	for i, result := range results {
		entityMap := result.(map[string]interface{})
		items[i] = map[string]interface{}{
			"start":       entityMap["start"],
			"end":         entityMap["end"],
			"entity_type": entityMap["entity_type"],
			"text":        "<" + entityMap["entity_type"].(string) + ">",
			"operator":    "replace",
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"text":  text,
		"items": items,
	}); err != nil {
		t.Fatalf("failed to encode anonymize response: %v", err)
	}
}

// runGuardrailTestCase runs a single guardrail test case.
func runGuardrailTestCase(t *testing.T, tc struct {
	name         string
	metadata     map[string]string
	requestBody  string
	wantBlocked  bool
	wantMasked   bool
	wantOriginal bool
}) {
	server := NewServer(nil, nil, 0)

	// Create metadata context
	var metadataCtx *corev3.Metadata
	if len(tc.metadata) > 0 {
		fields := make(map[string]*structpb.Value)
		for k, v := range tc.metadata {
			fields[k] = structpb.NewStringValue(v)
		}
		metadataCtx = &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				"envoy.filters.http.ext_proc": {
					Fields: fields,
				},
			},
		}
	}

	// Create stream with headers + body
	requests := []*extprocv3.ProcessingRequest{
		{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{},
			},
			MetadataContext: metadataCtx,
		},
		{
			Request: &extprocv3.ProcessingRequest_RequestBody{
				RequestBody: &extprocv3.HttpBody{
					Body:        []byte(tc.requestBody),
					EndOfStream: true,
				},
			},
			MetadataContext: metadataCtx,
		},
	}

	stream := &mockProcessStream{
		ctx:      context.Background(),
		requests: requests,
		sent:     []*extprocv3.ProcessingResponse{},
	}

	err := server.Process(stream)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if len(stream.sent) != 2 {
		t.Fatalf("sent %d responses, want 2", len(stream.sent))
	}

	// Check body response
	bodyResp := stream.sent[1]

	if tc.wantBlocked {
		verifyBlockedResponse(t, bodyResp)
	} else if tc.wantMasked {
		verifyMaskedResponse(t, bodyResp, tc.requestBody)
	} else if tc.wantOriginal {
		verifyPassthroughResponse(t, bodyResp, tc.requestBody)
	}
}

// verifyBlockedResponse verifies that the response is an ImmediateResponse with 403.
func verifyBlockedResponse(t *testing.T, bodyResp *extprocv3.ProcessingResponse) {
	immResp, ok := bodyResp.Response.(*extprocv3.ProcessingResponse_ImmediateResponse)
	if !ok {
		t.Errorf("expected ImmediateResponse, got %T", bodyResp.Response)
		return
	}

	if immResp.ImmediateResponse.Status.Code != 403 {
		t.Errorf("expected status code 403, got %d", immResp.ImmediateResponse.Status.Code)
	}

	// Verify error body contains GUARDRAIL_VIOLATION
	var errorBody map[string]interface{}
	if err := json.Unmarshal(immResp.ImmediateResponse.Body, &errorBody); err == nil {
		if errorObj, ok := errorBody["error"].(map[string]interface{}); ok {
			if code, ok := errorObj["code"].(string); !ok || code != "GUARDRAIL_VIOLATION" {
				t.Errorf("expected error code GUARDRAIL_VIOLATION, got %v", code)
			}
		}
	}
}

// verifyMaskedResponse verifies the EOS reply carries a StreamedResponse with
// a body that differs from the original.
func verifyMaskedResponse(t *testing.T, bodyResp *extprocv3.ProcessingResponse, originalBody string) {
	rb, ok := bodyResp.Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Errorf("response = %T, want *ProcessingResponse_RequestBody", bodyResp.Response)
		return
	}
	if rb.RequestBody.Response == nil || rb.RequestBody.Response.BodyMutation == nil {
		t.Error("expected BodyMutation in response")
		return
	}
	sr, ok := rb.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse)
	if !ok {
		t.Errorf("mutation = %T, want *BodyMutation_StreamedResponse", rb.RequestBody.Response.BodyMutation.Mutation)
		return
	}
	if !sr.StreamedResponse.EndOfStream {
		t.Error("EndOfStream = false, want true on the masked reply")
	}
	if len(sr.StreamedResponse.Body) == 0 {
		t.Error("expected non-empty mutated body")
	}
	if string(sr.StreamedResponse.Body) == originalBody {
		t.Error("expected mutated body to differ from original")
	}
}

// verifyPassthroughResponse verifies the EOS reply carries a StreamedResponse
// whose body equals the original (pass-through path) or a held empty reply
// (buffer-until-EOS path with no replacements).
func verifyPassthroughResponse(t *testing.T, bodyResp *extprocv3.ProcessingResponse, originalBody string) {
	rb, ok := bodyResp.Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Errorf("response = %T, want *ProcessingResponse_RequestBody", bodyResp.Response)
		return
	}
	if rb.RequestBody.Response == nil || rb.RequestBody.Response.BodyMutation == nil {
		t.Error("expected StreamedResponse mutation in passthrough reply")
		return
	}
	sr, ok := rb.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse)
	if !ok {
		t.Errorf("mutation = %T, want *BodyMutation_StreamedResponse", rb.RequestBody.Response.BodyMutation.Mutation)
		return
	}
	if string(sr.StreamedResponse.Body) != originalBody {
		t.Errorf("passthrough body = %q, want %q", sr.StreamedResponse.Body, originalBody)
	}
	if !sr.StreamedResponse.EndOfStream {
		t.Error("EndOfStream = false, want true")
	}
}

// Helper functions for the test
func contains(s, substr string) bool {
	return findIndex(s, substr) != -1
}

func findIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func replaceAll(s, entityType string) string {
	placeholder := "<" + entityType + ">"
	// Simple replacement logic for test
	switch entityType {
	case "EMAIL_ADDRESS":
		s = replaceSubstring(s, "john@example.com", placeholder)
	case "US_SSN":
		s = replaceSubstring(s, "123-45-6789", placeholder)
	}
	return s
}

func replaceSubstring(s, old, new string) string {
	idx := findIndex(s, old)
	if idx == -1 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

func TestStaticConfigShortCircuitsMetadata(t *testing.T) {
	const testProvider = "presidio-api"
	staticCfg := &metadata.GuardrailConfig{
		Provider: testProvider,
		Modes:    []metadata.Mode{metadata.ModePreCall},
		Presidio: &metadata.PresidioConfig{Endpoint: "http://static:8000"},
	}
	server := NewServer(nil, staticCfg, 0)

	// A MetadataContext that *would* configure a different provider if consulted.
	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":          "some-other-provider",
		"guardrail.mode":              "post_call",
		"guardrail.presidio.endpoint": "http://dynamic:8000",
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}

	state := &streamState{requestMetadata: make(map[string]interface{})}
	req := &extprocv3.ProcessingRequest{
		MetadataContext: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md},
		},
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{
				Headers: &corev3.HeaderMap{
					Headers: []*corev3.HeaderValue{
						{Key: "x-guardrail-provider", Value: "header-provider"},
						{Key: "x-guardrail-mode", Value: "post_call"},
						{Key: "x-guardrail-presidio-endpoint", Value: "http://from-header:8000"},
					},
				},
			},
		},
	}

	_ = server.handleRequestHeaders(req, state)

	if state.config == nil {
		t.Fatal("expected state.config to be set from static config, got nil")
	}
	if state.config.Provider != testProvider {
		t.Errorf("provider = %q, want %q (static should win over both metadata and headers)", state.config.Provider, testProvider)
	}
	if state.config.Presidio == nil || state.config.Presidio.Endpoint != "http://static:8000" {
		t.Errorf("endpoint = %#v, want %q", state.config.Presidio, "http://static:8000")
	}
}

func TestStaticConfigWorksWithoutMetadataOrHeaders(t *testing.T) {
	const testProvider = "presidio-api"
	staticCfg := &metadata.GuardrailConfig{
		Provider: testProvider,
		Modes:    []metadata.Mode{metadata.ModePreCall},
		Presidio: &metadata.PresidioConfig{Endpoint: "http://static:8000"},
	}
	server := NewServer(nil, staticCfg, 0)
	state := &streamState{requestMetadata: make(map[string]interface{})}
	req := &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{},
		},
	}
	_ = server.handleRequestHeaders(req, state)
	if state.config == nil || state.config.Provider != testProvider {
		t.Fatalf("expected static config applied, got %#v", state.config)
	}
}

// TestHeadersFallbackWhenMetadataPresentButEmpty reproduces the production scenario
// where Envoy sends a non-nil MetadataContext (because the EnvoyExtensionPolicy
// declares accessibleNamespaces) that contains no guardrail.* fields. The adapter
// must still fall back to the x-guardrail-* headers injected by the Lua filter.
func TestHeadersFallbackWhenMetadataPresentButEmpty(t *testing.T) {
	const testProvider = "presidio-api"
	server := NewServer(nil, nil, 0)

	emptyMD, err := structpb.NewStruct(map[string]interface{}{})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}

	state := &streamState{requestMetadata: make(map[string]interface{})}
	req := &extprocv3.ProcessingRequest{
		MetadataContext: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": emptyMD},
		},
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{
				Headers: &corev3.HeaderMap{
					Headers: []*corev3.HeaderValue{
						{Key: "x-guardrail-provider", Value: testProvider},
						{Key: "x-guardrail-mode", Value: "pre_call"},
						{Key: "x-guardrail-presidio-endpoint", Value: "http://presidio:80"},
					},
				},
			},
		},
	}

	_ = server.handleRequestHeaders(req, state)

	if state.config == nil {
		t.Fatal("expected state.config from header fallback, got nil")
	}
	if state.config.Provider != testProvider {
		t.Errorf("provider = %q, want %q", state.config.Provider, testProvider)
	}
	if state.config.Presidio == nil || state.config.Presidio.Endpoint != "http://presidio:80" {
		t.Errorf("presidio = %#v, want endpoint http://presidio:80", state.config.Presidio)
	}
}

// TestHeaderFallbackConfig tests the parseGuardrailHeaders function for extracting
// guardrail configuration from x-guardrail-* HTTP request headers.
func TestHeaderFallbackConfig(t *testing.T) {
	const testProvider = "presidio-api"
	server := NewServer(nil, nil, 0)

	testCases := []struct {
		name       string
		headers    map[string]string
		wantConfig bool
		wantError  bool
		validate   func(*testing.T, *metadata.GuardrailConfig)
	}{
		{
			name:       "no_headers",
			headers:    map[string]string{},
			wantConfig: false,
			wantError:  false,
		},
		{
			name: "complete_presidio_config",
			headers: map[string]string{
				"x-guardrail-provider":                  testProvider,
				"x-guardrail-mode":                      "pre_call",
				"x-guardrail-presidio-endpoint":         "http://presidio:80",
				"x-guardrail-presidio-language":         "en",
				"x-guardrail-presidio-score-thresholds": `{"ALL":"0.5"}`,
				"x-guardrail-presidio-entity-actions":   `{"EMAIL_ADDRESS":"MASK"}`,
			},
			wantConfig: true,
			wantError:  false,
			validate: func(t *testing.T, cfg *metadata.GuardrailConfig) {
				if cfg.Provider != testProvider {
					t.Errorf("expected provider '%s', got %s", testProvider, cfg.Provider)
				}
				if len(cfg.Modes) != 1 || cfg.Modes[0] != metadata.ModePreCall {
					t.Errorf("expected mode pre_call, got %v", cfg.Modes)
				}
				if cfg.Presidio == nil {
					t.Fatal("expected presidio config, got nil")
				}
				if cfg.Presidio.Endpoint != "http://presidio:80" {
					t.Errorf("expected endpoint 'http://presidio:80', got %s", cfg.Presidio.Endpoint)
				}
				if cfg.Presidio.Language != "en" {
					t.Errorf("expected language 'en', got %s", cfg.Presidio.Language)
				}
			},
		},
		{
			name: "multiple_modes",
			headers: map[string]string{
				"x-guardrail-provider":                  testProvider,
				"x-guardrail-mode":                      "pre_call,post_call",
				"x-guardrail-presidio-endpoint":         "http://presidio:80",
				"x-guardrail-presidio-language":         "en",
				"x-guardrail-presidio-score-thresholds": `{"ALL":"0.5"}`,
				"x-guardrail-presidio-entity-actions":   `{"EMAIL_ADDRESS":"MASK"}`,
			},
			wantConfig: true,
			wantError:  false,
			validate: func(t *testing.T, cfg *metadata.GuardrailConfig) {
				if len(cfg.Modes) != 2 {
					t.Errorf("expected 2 modes, got %d", len(cfg.Modes))
				}
			},
		},
		{
			name: "case_insensitive_headers",
			headers: map[string]string{
				"X-Guardrail-Provider":          testProvider,
				"X-GUARDRAIL-MODE":              "pre_call",
				"x-GuardRail-Presidio-Endpoint": "http://presidio:80",
				"X-GUARDRAIL-PRESIDIO-LANGUAGE": "en",
			},
			wantConfig: true,
			wantError:  false,
			validate: func(t *testing.T, cfg *metadata.GuardrailConfig) {
				if cfg.Provider != testProvider {
					t.Errorf("expected provider '%s', got %s", testProvider, cfg.Provider)
				}
			},
		},
		{
			name: "partial_presidio_config",
			headers: map[string]string{
				"x-guardrail-provider": testProvider,
				"x-guardrail-mode":     "pre_call",
				// Missing endpoint and other fields - still valid, just incomplete
			},
			wantConfig: true,
			wantError:  false,
			validate: func(t *testing.T, cfg *metadata.GuardrailConfig) {
				if cfg.Provider != testProvider {
					t.Errorf("expected provider '%s', got %s", testProvider, cfg.Provider)
				}
				if cfg.Presidio == nil {
					t.Fatal("expected presidio config, got nil")
				}
				if cfg.Presidio.Endpoint != "" {
					t.Errorf("expected empty endpoint, got %s", cfg.Presidio.Endpoint)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Build HttpHeaders from test headers
			headers := &extprocv3.HttpHeaders{}
			if len(tc.headers) > 0 {
				hdrs := make([]*corev3.HeaderValue, 0, len(tc.headers))
				for k, v := range tc.headers {
					hdrs = append(hdrs, &corev3.HeaderValue{
						Key:   k,
						Value: v,
					})
				}
				headers.Headers = &corev3.HeaderMap{
					Headers: hdrs,
				}
			}

			config, err := server.parseGuardrailHeaders(headers)

			if tc.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantConfig && config == nil {
				t.Fatal("expected config, got nil")
			}

			if !tc.wantConfig && config != nil {
				t.Fatalf("expected no config, got %+v", config)
			}

			if tc.validate != nil && config != nil {
				tc.validate(t, config)
			}
		})
	}
}

// TestEmptyRequestBodySkipsParserSelection verifies that an empty request body
// is passed through without invoking the protocol registry, and that the
// subsequent response is also passed through because no parser was identified
// for the stream.
func TestEmptyRequestBodySkipsParserSelection(t *testing.T) {
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	server := NewServer(nil, nil, 0)

	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "pre_call,post_call",
		"guardrail.presidio.endpoint":         presidioServer.URL,
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
		"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	metadataCtx := &corev3.Metadata{
		FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md},
	}

	requests := []*extprocv3.ProcessingRequest{
		{
			Request:         &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}},
			MetadataContext: metadataCtx,
		},
		{
			Request:         &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: nil, EndOfStream: true}},
			MetadataContext: metadataCtx,
		},
		{
			Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{
				Body:        []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"john@example.com"}]}}`),
				EndOfStream: true,
			}},
			MetadataContext: metadataCtx,
		},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if len(stream.sent) != 3 {
		t.Fatalf("sent %d responses, want 3", len(stream.sent))
	}

	reqBody, ok := stream.sent[1].Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Fatalf("expected RequestBody response, got %T", stream.sent[1].Response)
	}
	sr := reqBody.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if len(sr.Body) != 0 || !sr.EndOfStream {
		t.Errorf("empty request body passthrough: body=%q EOS=%v, want empty + EOS=true", sr.Body, sr.EndOfStream)
	}

	const expectedRespBody = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"john@example.com"}]}}`
	respBody, ok := stream.sent[2].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	if !ok {
		t.Fatalf("expected ResponseBody response, got %T", stream.sent[2].Response)
	}
	sr = respBody.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if string(sr.Body) != expectedRespBody || !sr.EndOfStream {
		t.Errorf("response passthrough: body=%q EOS=%v, want %q EOS=true", sr.Body, sr.EndOfStream, expectedRespBody)
	}
}

// TestEmptyResponseBodyPassesThrough verifies that an empty response body is
// passed through without invoking the parser even when a parser was identified
// from the request.
func TestEmptyResponseBodyPassesThrough(t *testing.T) {
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	server := NewServer(nil, nil, 0)

	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "post_call",
		"guardrail.presidio.endpoint":         presidioServer.URL,
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
		"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	metadataCtx := &corev3.Metadata{
		FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md},
	}

	requests := []*extprocv3.ProcessingRequest{
		{
			Request:         &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}},
			MetadataContext: metadataCtx,
		},
		{
			Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{
				Body:        []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"q":"hi"}}}`),
				EndOfStream: true,
			}},
			MetadataContext: metadataCtx,
		},
		{
			Request:         &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: nil, EndOfStream: true}},
			MetadataContext: metadataCtx,
		},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if len(stream.sent) != 3 {
		t.Fatalf("sent %d responses, want 3", len(stream.sent))
	}
	respBody, ok := stream.sent[2].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	if !ok {
		t.Fatalf("expected ResponseBody response, got %T", stream.sent[2].Response)
	}
	sr := respBody.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if len(sr.Body) != 0 || !sr.EndOfStream {
		t.Errorf("empty response passthrough: body=%q EOS=%v, want empty + EOS=true", sr.Body, sr.EndOfStream)
	}
}

// TestPostCallOnlyParserSharedFromRequest verifies that even when pre_call mode
// is disabled, the parser is selected during request body handling and reused
// to inspect the response body.
func TestPostCallOnlyParserSharedFromRequest(t *testing.T) {
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	server := NewServer(nil, nil, 0)

	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "post_call",
		"guardrail.presidio.endpoint":         presidioServer.URL,
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
		"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	metadataCtx := &corev3.Metadata{
		FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md},
	}

	requestBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"q":"hi"}}}`)
	responseBody := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"contact john@example.com"}]}}`)

	requests := []*extprocv3.ProcessingRequest{
		{
			Request:         &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}},
			MetadataContext: metadataCtx,
		},
		{
			Request:         &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: requestBody, EndOfStream: true}},
			MetadataContext: metadataCtx,
		},
		{
			Request:         &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: responseBody, EndOfStream: true}},
			MetadataContext: metadataCtx,
		},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if len(stream.sent) != 3 {
		t.Fatalf("sent %d responses, want 3", len(stream.sent))
	}

	// Request body should be passthrough (pre_call disabled).
	reqBody, ok := stream.sent[1].Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Fatalf("expected RequestBody response, got %T", stream.sent[1].Response)
	}
	sr := reqBody.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if string(sr.Body) != string(requestBody) || !sr.EndOfStream {
		t.Errorf("request body passthrough: body=%q EOS=%v, want %q EOS=true", sr.Body, sr.EndOfStream, requestBody)
	}

	// Response body should be MASKED — proves the parser was identified from
	// the request and reused to extract texts from the response.
	respBody, ok := stream.sent[2].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	if !ok {
		t.Fatalf("expected ResponseBody response, got %T", stream.sent[2].Response)
	}
	sr2, ok := respBody.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse)
	if !ok {
		t.Fatalf("mutation = %T, want *BodyMutation_StreamedResponse", respBody.ResponseBody.Response.BodyMutation.Mutation)
	}
	if !sr2.StreamedResponse.EndOfStream {
		t.Error("EndOfStream = false, want true on masked EOS reply")
	}
	if len(sr2.StreamedResponse.Body) == 0 {
		t.Error("expected non-empty mutated response body")
	}
	if string(sr2.StreamedResponse.Body) == string(responseBody) {
		t.Error("expected mutated response body to differ from original")
	}
}

// TestHeaderFallbackInRequestFlow tests that header-fallback config is used when
// MetadataContext is nil and that body mutations include the content-length removal.
func TestHeaderFallbackInRequestFlow(t *testing.T) {
	// Setup mock Presidio server
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	server := NewServer(nil, nil, 0)

	// Create request headers with guardrail config
	headerValues := []*corev3.HeaderValue{
		{Key: "x-guardrail-provider", Value: "presidio-api"},
		{Key: "x-guardrail-mode", Value: "pre_call"},
		{Key: "x-guardrail-presidio-endpoint", Value: presidioServer.URL},
		{Key: "x-guardrail-presidio-language", Value: "en"},
		{Key: "x-guardrail-presidio-score-thresholds", Value: `{"ALL":"0.5"}`},
		{Key: "x-guardrail-presidio-entity-actions", Value: `{"EMAIL_ADDRESS":"MASK"}`},
	}

	requests := []*extprocv3.ProcessingRequest{
		{
			Request: &extprocv3.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extprocv3.HttpHeaders{
					Headers: &corev3.HeaderMap{
						Headers: headerValues,
					},
				},
			},
			// No MetadataContext - force header fallback
			MetadataContext: nil,
		},
		{
			Request: &extprocv3.ProcessingRequest_RequestBody{
				RequestBody: &extprocv3.HttpBody{
					Body:        []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{"email":"john@example.com"}}}`),
					EndOfStream: true,
				},
			},
			MetadataContext: nil,
		},
	}

	stream := &mockProcessStream{
		ctx:      context.Background(),
		requests: requests,
		sent:     []*extprocv3.ProcessingResponse{},
	}

	err := server.Process(stream)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if len(stream.sent) != 2 {
		t.Fatalf("sent %d responses, want 2", len(stream.sent))
	}

	// Check that body was mutated
	bodyResp, ok := stream.sent[1].Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Fatalf("expected RequestBody response, got %T", stream.sent[1].Response)
	}

	if bodyResp.RequestBody.Response == nil || bodyResp.RequestBody.Response.BodyMutation == nil {
		t.Fatal("expected BodyMutation in response")
	}

	// Verify HeaderMutation removes content-length
	if bodyResp.RequestBody.Response.HeaderMutation == nil {
		t.Fatal("expected HeaderMutation in response")
	}

	removedHeaders := bodyResp.RequestBody.Response.HeaderMutation.RemoveHeaders
	found := false
	for _, h := range removedHeaders {
		if h == "content-length" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected content-length to be removed, but it was not in RemoveHeaders")
	}

	// Verify body was actually modified
	sr, ok := bodyResp.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse)
	if !ok {
		t.Fatalf("mutation = %T, want *BodyMutation_StreamedResponse", bodyResp.RequestBody.Response.BodyMutation.Mutation)
	}
	mutatedBody := sr.StreamedResponse.Body
	if string(mutatedBody) == string(requests[1].GetRequestBody().Body) {
		t.Error("expected body to be mutated, but it matches original")
	}
	if !sr.StreamedResponse.EndOfStream {
		t.Error("EndOfStream = false, want true on EOS reply")
	}
}

func TestStreamedRequestBodyResponseShape(t *testing.T) {
	resp := streamedRequestBodyResponse([]byte("hello"), true, nil)
	rb, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Fatalf("response = %T, want *ProcessingResponse_RequestBody", resp.Response)
	}
	mut, ok := rb.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse)
	if !ok {
		t.Fatalf("mutation = %T, want *BodyMutation_StreamedResponse", rb.RequestBody.Response.BodyMutation.Mutation)
	}
	if string(mut.StreamedResponse.Body) != "hello" {
		t.Errorf("body = %q, want %q", mut.StreamedResponse.Body, "hello")
	}
	if !mut.StreamedResponse.EndOfStream {
		t.Error("EndOfStream = false, want true")
	}
}

func TestStreamedResponseBodyResponseShape(t *testing.T) {
	resp := streamedResponseBodyResponse([]byte("world"), false, nil)
	rb, ok := resp.Response.(*extprocv3.ProcessingResponse_ResponseBody)
	if !ok {
		t.Fatalf("response = %T, want *ProcessingResponse_ResponseBody", resp.Response)
	}
	mut, ok := rb.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse)
	if !ok {
		t.Fatalf("mutation = %T, want *BodyMutation_StreamedResponse", rb.ResponseBody.Response.BodyMutation.Mutation)
	}
	if string(mut.StreamedResponse.Body) != "world" {
		t.Errorf("body = %q, want %q", mut.StreamedResponse.Body, "world")
	}
	if mut.StreamedResponse.EndOfStream {
		t.Error("EndOfStream = true, want false")
	}
}

// TestRequestBodyBufferedUntilEOSMasks verifies that when an MCP request body
// arrives in multiple streamed chunks, the adapter holds replies until
// EndOfStream and emits the assembled+mutated body on the EOS chunk.
func TestRequestBodyBufferedUntilEOSMasks(t *testing.T) {
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	server := NewServer(nil, nil, 0)

	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "pre_call",
		"guardrail.presidio.endpoint":         presidioServer.URL,
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
		"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	metadataCtx := &corev3.Metadata{
		FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md},
	}

	full := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"email":"john@example.com"}}}`
	chunk1 := []byte(full[:40])
	chunk2 := []byte(full[40:])

	requests := []*extprocv3.ProcessingRequest{
		{
			Request:         &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}},
			MetadataContext: metadataCtx,
		},
		{
			Request:         &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: chunk1, EndOfStream: false}},
			MetadataContext: metadataCtx,
		},
		{
			Request:         &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: chunk2, EndOfStream: true}},
			MetadataContext: metadataCtx,
		},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(stream.sent) != 3 {
		t.Fatalf("sent %d responses, want 3", len(stream.sent))
	}

	// First body chunk: held — body=nil, EOS=false.
	first := stream.sent[1].Response.(*extprocv3.ProcessingResponse_RequestBody)
	firstSR := first.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if len(firstSR.Body) != 0 || firstSR.EndOfStream {
		t.Errorf("intermediate reply: body=%q EOS=%v, want empty body and EOS=false", firstSR.Body, firstSR.EndOfStream)
	}

	// EOS chunk: assembled + mutated body, EOS=true.
	last := stream.sent[2].Response.(*extprocv3.ProcessingResponse_RequestBody)
	lastSR := last.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if !lastSR.EndOfStream {
		t.Error("EOS reply: EndOfStream = false, want true")
	}
	if len(lastSR.Body) == 0 {
		t.Fatal("EOS reply: body is empty, want assembled+mutated body")
	}
	if string(lastSR.Body) == full {
		t.Error("EOS reply: body matches original; expected masked")
	}
}

// TestRequestBodyMultiChunkPassthroughEchoes verifies that without a guardrail
// configured, multi-chunk request bodies are echoed chunk-by-chunk with no
// buffering.
func TestRequestBodyMultiChunkPassthroughEchoes(t *testing.T) {
	server := NewServer(nil, nil, 0)

	chunk1 := []byte(`{"jsonrpc":"2.0",`)
	chunk2 := []byte(`"id":1,"method":"ping"}`)

	requests := []*extprocv3.ProcessingRequest{
		{Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}}},
		{Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: chunk1, EndOfStream: false}}},
		{Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: chunk2, EndOfStream: true}}},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	first := stream.sent[1].Response.(*extprocv3.ProcessingResponse_RequestBody)
	firstSR := first.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if string(firstSR.Body) != string(chunk1) || firstSR.EndOfStream {
		t.Errorf("chunk 1 reply: body=%q EOS=%v, want %q EOS=false", firstSR.Body, firstSR.EndOfStream, chunk1)
	}

	last := stream.sent[2].Response.(*extprocv3.ProcessingResponse_RequestBody)
	lastSR := last.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if string(lastSR.Body) != string(chunk2) || !lastSR.EndOfStream {
		t.Errorf("chunk 2 reply: body=%q EOS=%v, want %q EOS=true", lastSR.Body, lastSR.EndOfStream, chunk2)
	}
}

// TestResponseBodyBufferedUntilEOSMasks exercises post_call inspection on a
// streamed response body delivered in multiple chunks.
func TestResponseBodyBufferedUntilEOSMasks(t *testing.T) {
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	server := NewServer(nil, nil, 0)

	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "post_call",
		"guardrail.presidio.endpoint":         presidioServer.URL,
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
		"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	metadataCtx := &corev3.Metadata{
		FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md},
	}

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"q":"hi"}}}`)
	full := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"contact john@example.com"}]}}`
	respChunk1 := []byte(full[:50])
	respChunk2 := []byte(full[50:])

	requests := []*extprocv3.ProcessingRequest{
		{Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: reqBody, EndOfStream: true}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extprocv3.HttpHeaders{}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: respChunk1, EndOfStream: false}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: respChunk2, EndOfStream: true}}, MetadataContext: metadataCtx},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(stream.sent) != 5 {
		t.Fatalf("sent %d responses, want 5", len(stream.sent))
	}

	// Intermediate response chunk: held.
	mid := stream.sent[3].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	midSR := mid.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if len(midSR.Body) != 0 || midSR.EndOfStream {
		t.Errorf("intermediate reply: body=%q EOS=%v, want empty + EOS=false", midSR.Body, midSR.EndOfStream)
	}

	// EOS reply: assembled + mutated.
	end := stream.sent[4].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	endSR := end.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if !endSR.EndOfStream {
		t.Error("EOS reply: EndOfStream = false, want true")
	}
	if len(endSR.Body) == 0 {
		t.Fatal("EOS reply: body is empty, want assembled+mutated body")
	}
	if string(endSR.Body) == full {
		t.Error("EOS reply: body matches original; expected masked")
	}
}

// TestResponseBodyMultiChunkPassthroughEchoes verifies that without
// inspection configured, response chunks are echoed without buffering.
func TestResponseBodyMultiChunkPassthroughEchoes(t *testing.T) {
	server := NewServer(nil, nil, 0)

	chunk1 := []byte(`{"jsonrpc":"2.0",`)
	chunk2 := []byte(`"id":1,"result":"ok"}`)

	requests := []*extprocv3.ProcessingRequest{
		{Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}}},
		{Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extprocv3.HttpHeaders{}}},
		{Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: chunk1, EndOfStream: false}}},
		{Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: chunk2, EndOfStream: true}}},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	first := stream.sent[2].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	firstSR := first.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if string(firstSR.Body) != string(chunk1) || firstSR.EndOfStream {
		t.Errorf("chunk 1 reply: body=%q EOS=%v, want %q EOS=false", firstSR.Body, firstSR.EndOfStream, chunk1)
	}

	last := stream.sent[3].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	lastSR := last.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if string(lastSR.Body) != string(chunk2) || !lastSR.EndOfStream {
		t.Errorf("chunk 2 reply: body=%q EOS=%v, want %q EOS=true", lastSR.Body, lastSR.EndOfStream, chunk2)
	}
}

// TestSSEResponseInspectionForwardsNonInspectableEvents verifies that
// with post_call inspection configured, a text/event-stream response
// runs through the frame-aware path: each event is forwarded verbatim
// when its data: payload is not a tool-call response. No
// ImmediateResponse is emitted.
func TestSSEResponseInspectionForwardsNonInspectableEvents(t *testing.T) {
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	server := NewServer(nil, nil, 0)

	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "post_call",
		"guardrail.presidio.endpoint":         presidioServer.URL,
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
		"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	metadataCtx := &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md}}

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"q":"hi"}}}`)
	// A notification — has no result, parser.ParseResponse returns no
	// extractions, so it must pass through verbatim.
	notif := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":50}}\n\n")

	requests := []*extprocv3.ProcessingRequest{
		{Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: reqBody, EndOfStream: true}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extprocv3.HttpHeaders{
			Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
				{Key: ":status", Value: "200"},
				{Key: "content-type", Value: "text/event-stream"},
			}},
		}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: notif, EndOfStream: true}}, MetadataContext: metadataCtx},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	for i, sent := range stream.sent {
		if _, ok := sent.Response.(*extprocv3.ProcessingResponse_ImmediateResponse); ok {
			t.Fatalf("sent[%d] is ImmediateResponse, want frame-aware passthrough", i)
		}
	}
	bodyResp := stream.sent[3].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	sr := bodyResp.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if !bytes.Equal(sr.Body, notif) {
		t.Errorf("body = %q, want %q (verbatim)", sr.Body, notif)
	}
	if !sr.EndOfStream {
		t.Error("EndOfStream = false, want true")
	}
}

// TestSSEResponsePassesThroughWhenNotInspecting verifies that without
// post_call inspection configured, a text/event-stream response is echoed
// chunk-by-chunk and no ImmediateResponse is emitted.
func TestSSEResponsePassesThroughWhenNotInspecting(t *testing.T) {
	server := NewServer(nil, nil, 0)

	chunk := []byte("event: message\ndata: hello\n\n")

	requests := []*extprocv3.ProcessingRequest{
		{Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}}},
		{Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extprocv3.HttpHeaders{
			Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
				{Key: ":status", Value: "200"},
				{Key: "content-type", Value: "text/event-stream"},
			}},
		}}},
		{Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: chunk, EndOfStream: true}}},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	for i, sent := range stream.sent {
		if _, ok := sent.Response.(*extprocv3.ProcessingResponse_ImmediateResponse); ok {
			t.Fatalf("sent[%d] is ImmediateResponse, want passthrough", i)
		}
	}
	bodyResp := stream.sent[2].Response.(*extprocv3.ProcessingResponse_ResponseBody)
	sr := bodyResp.ResponseBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if string(sr.Body) != string(chunk) || !sr.EndOfStream {
		t.Errorf("chunk reply: body=%q EOS=%v, want %q EOS=true", sr.Body, sr.EndOfStream, chunk)
	}
}

// TestRequestBodyOversizeFailsClosed verifies that a request body exceeding
// max-body-size in the inspection path is rejected with 413.
func TestRequestBodyOversizeFailsClosed(t *testing.T) {
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	const maxSize = 64
	server := NewServer(nil, nil, maxSize)

	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "pre_call",
		"guardrail.presidio.endpoint":         presidioServer.URL,
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
		"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	metadataCtx := &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md}}

	// 200 bytes of valid-looking JSON-RPC well over the 64-byte cap.
	full := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"text":"` + strings.Repeat("a", 100) + `"}}}`

	requests := []*extprocv3.ProcessingRequest{
		{Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: []byte(full), EndOfStream: true}}, MetadataContext: metadataCtx},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(stream.sent) != 2 {
		t.Fatalf("sent %d responses, want 2", len(stream.sent))
	}

	imm, ok := stream.sent[1].Response.(*extprocv3.ProcessingResponse_ImmediateResponse)
	if !ok {
		t.Fatalf("response = %T, want *ProcessingResponse_ImmediateResponse", stream.sent[1].Response)
	}
	if imm.ImmediateResponse.Status.Code != 413 {
		t.Errorf("status = %d, want 413", imm.ImmediateResponse.Status.Code)
	}
}

// TestRequestBodyOversizePassthroughUnaffected verifies the cap does not
// apply to the pass-through path (no inspection configured).
func TestRequestBodyOversizePassthroughUnaffected(t *testing.T) {
	const maxSize = 64
	server := NewServer(nil, nil, maxSize)

	full := strings.Repeat("a", 200)

	requests := []*extprocv3.ProcessingRequest{
		{Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}}},
		{Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: []byte(full), EndOfStream: true}}},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if _, ok := stream.sent[1].Response.(*extprocv3.ProcessingResponse_ImmediateResponse); ok {
		t.Fatal("got ImmediateResponse on pass-through path; expected echo")
	}
	rb, ok := stream.sent[1].Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Fatalf("response = %T, want *ProcessingResponse_RequestBody", stream.sent[1].Response)
	}
	sr := rb.RequestBody.Response.BodyMutation.Mutation.(*extprocv3.BodyMutation_StreamedResponse).StreamedResponse
	if string(sr.Body) != full || !sr.EndOfStream {
		t.Errorf("pass-through reply: body=%q EOS=%v, want %q EOS=true", sr.Body, sr.EndOfStream, full)
	}
}

// TestResponseBodyOversizeFailsClosed verifies a 502 reply when the
// response body exceeds max-body-size in the inspection path.
func TestResponseBodyOversizeFailsClosed(t *testing.T) {
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	const maxSize = 64
	server := NewServer(nil, nil, maxSize)

	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":                  "presidio-api",
		"guardrail.mode":                      "post_call",
		"guardrail.presidio.endpoint":         presidioServer.URL,
		"guardrail.presidio.language":         "en",
		"guardrail.presidio.score_thresholds": `{"ALL":"0.5"}`,
		"guardrail.presidio.entity_actions":   `{"EMAIL_ADDRESS":"MASK"}`,
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}
	metadataCtx := &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md}}

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"q":"hi"}}}`)
	respBody := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"` + strings.Repeat("a", 100) + `"}]}}`

	requests := []*extprocv3.ProcessingRequest{
		{Request: &extprocv3.ProcessingRequest_RequestHeaders{RequestHeaders: &extprocv3.HttpHeaders{}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_RequestBody{RequestBody: &extprocv3.HttpBody{Body: reqBody, EndOfStream: true}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extprocv3.HttpHeaders{}}, MetadataContext: metadataCtx},
		{Request: &extprocv3.ProcessingRequest_ResponseBody{ResponseBody: &extprocv3.HttpBody{Body: []byte(respBody), EndOfStream: true}}, MetadataContext: metadataCtx},
	}

	stream := &mockProcessStream{ctx: context.Background(), requests: requests, sent: []*extprocv3.ProcessingResponse{}}
	if err := server.Process(stream); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(stream.sent) != 4 {
		t.Fatalf("sent %d responses, want 4", len(stream.sent))
	}
	imm, ok := stream.sent[3].Response.(*extprocv3.ProcessingResponse_ImmediateResponse)
	if !ok {
		t.Fatalf("response = %T, want *ProcessingResponse_ImmediateResponse", stream.sent[3].Response)
	}
	if imm.ImmediateResponse.Status.Code != 502 {
		t.Errorf("status = %d, want 502", imm.ImmediateResponse.Status.Code)
	}
}
