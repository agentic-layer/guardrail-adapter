package extproc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	server := NewServer(nil)

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
	server := NewServer(nil)

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
	server := NewServer(nil)

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
				"guardrail.presidio.score_thresholds": `{"ALL":0.5}`,
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
				"guardrail.presidio.score_thresholds": `{"ALL":0.5}`,
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
				"guardrail.presidio.score_thresholds": `{"ALL":0.5}`,
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
	server := NewServer(nil)

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
					Body: []byte(tc.requestBody),
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
		verifyPassthroughResponse(t, bodyResp)
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

// verifyMaskedResponse verifies that the response contains a BodyMutation.
func verifyMaskedResponse(t *testing.T, bodyResp *extprocv3.ProcessingResponse, originalBody string) {
	bodyRespTyped, ok := bodyResp.Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Errorf("expected RequestBody response, got %T", bodyResp.Response)
		return
	}

	if bodyRespTyped.RequestBody.Response == nil || bodyRespTyped.RequestBody.Response.BodyMutation == nil {
		t.Error("expected BodyMutation in response")
		return
	}

	mutatedBody := bodyRespTyped.RequestBody.Response.BodyMutation.GetBody()
	if len(mutatedBody) == 0 {
		t.Error("expected non-empty mutated body")
	}

	// Verify the body is different from original
	if string(mutatedBody) == originalBody {
		t.Error("expected mutated body to be different from original")
	}
}

// verifyPassthroughResponse verifies that the response is a passthrough (no mutation).
func verifyPassthroughResponse(t *testing.T, bodyResp *extprocv3.ProcessingResponse) {
	bodyRespTyped, ok := bodyResp.Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		t.Errorf("expected RequestBody response, got %T", bodyResp.Response)
		return
	}

	if bodyRespTyped.RequestBody.Response != nil && bodyRespTyped.RequestBody.Response.BodyMutation != nil {
		t.Error("expected passthrough (no BodyMutation), but got mutation")
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
	staticCfg := &metadata.GuardrailConfig{
		Provider: "presidio-api",
		Modes:    []metadata.Mode{metadata.ModePreCall},
		Presidio: &metadata.PresidioConfig{Endpoint: "http://static:8000"},
	}
	server := NewServer(staticCfg)

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
	if state.config.Provider != "presidio-api" {
		t.Errorf("provider = %q, want %q (static should win over both metadata and headers)", state.config.Provider, "presidio-api")
	}
	if state.config.Presidio == nil || state.config.Presidio.Endpoint != "http://static:8000" {
		t.Errorf("endpoint = %#v, want %q", state.config.Presidio, "http://static:8000")
	}
}

func TestStaticConfigWorksWithoutMetadataOrHeaders(t *testing.T) {
	staticCfg := &metadata.GuardrailConfig{
		Provider: "presidio-api",
		Modes:    []metadata.Mode{metadata.ModePreCall},
		Presidio: &metadata.PresidioConfig{Endpoint: "http://static:8000"},
	}
	server := NewServer(staticCfg)
	state := &streamState{requestMetadata: make(map[string]interface{})}
	req := &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{},
		},
	}
	_ = server.handleRequestHeaders(req, state)
	if state.config == nil || state.config.Provider != "presidio-api" {
		t.Fatalf("expected static config applied, got %#v", state.config)
	}
}

// TestHeaderFallbackConfig tests the parseGuardrailHeaders function for extracting
// guardrail configuration from x-guardrail-* HTTP request headers.
func TestHeaderFallbackConfig(t *testing.T) {
	const testProvider = "presidio-api"
	server := NewServer(nil)

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
				"x-guardrail-presidio-score-thresholds": `{"ALL":0.5}`,
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
				"x-guardrail-presidio-score-thresholds": `{"ALL":0.5}`,
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

// TestHeaderFallbackInRequestFlow tests that header-fallback config is used when
// MetadataContext is nil and that body mutations include the content-length removal.
func TestHeaderFallbackInRequestFlow(t *testing.T) {
	// Setup mock Presidio server
	presidioServer := createMockPresidioServer(t)
	defer presidioServer.Close()

	server := NewServer(nil)

	// Create request headers with guardrail config
	headerValues := []*corev3.HeaderValue{
		{Key: "x-guardrail-provider", Value: "presidio-api"},
		{Key: "x-guardrail-mode", Value: "pre_call"},
		{Key: "x-guardrail-presidio-endpoint", Value: presidioServer.URL},
		{Key: "x-guardrail-presidio-language", Value: "en"},
		{Key: "x-guardrail-presidio-score-thresholds", Value: `{"ALL":0.5}`},
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
					Body: []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{"email":"john@example.com"}}}`),
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
	mutatedBody := bodyResp.RequestBody.Response.BodyMutation.GetBody()
	if string(mutatedBody) == string(requests[1].GetRequestBody().Body) {
		t.Error("expected body to be mutated, but it matches original")
	}
}
