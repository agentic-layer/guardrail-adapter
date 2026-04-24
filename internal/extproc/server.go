package extproc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/agentic-layer/guardrail-adapter/internal/metadata"
	"github.com/agentic-layer/guardrail-adapter/internal/protocol"
	"github.com/agentic-layer/guardrail-adapter/internal/protocol/mcpparser"
	"github.com/agentic-layer/guardrail-adapter/internal/provider"
	"github.com/agentic-layer/guardrail-adapter/internal/provider/presidio"
)

// Server implements the Envoy ExternalProcessor service.
type Server struct {
	extprocv3.UnimplementedExternalProcessorServer
	protocolRegistry *protocol.Registry
}

// streamState holds per-stream state for processing requests.
type streamState struct {
	config *metadata.GuardrailConfig
	// requestMetadata stores metadata from request processing (for use in response processing)
	requestMetadata map[string]interface{}
}

// NewServer creates a new ext_proc server.
func NewServer() *Server {
	// Initialize protocol registry with MCP parser
	registry := protocol.NewRegistry(
		mcpparser.NewMCPParser(),
	)

	return &Server{
		protocolRegistry: registry,
	}
}

// Process implements the bidirectional streaming RPC for external processing.
// It processes requests with guardrail inspection based on metadata configuration.
func (s *Server) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	ctx := stream.Context()
	state := &streamState{
		requestMetadata: make(map[string]interface{}),
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Receive the next request from Envoy
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			log.Printf("error receiving request: %v", err)
			return status.Errorf(codes.Unknown, "failed to receive request: %v", err)
		}

		// Generate response based on request type
		resp := s.handleRequest(ctx, req, state)

		// Send the response back to Envoy
		if err := stream.Send(resp); err != nil {
			log.Printf("error sending response: %v", err)
			return status.Errorf(codes.Unknown, "failed to send response: %v", err)
		}
	}
}

// handleRequest processes each request type and applies guardrail logic.
func (s *Server) handleRequest(ctx context.Context, req *extprocv3.ProcessingRequest, state *streamState) *extprocv3.ProcessingResponse {
	switch v := req.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:
		return s.handleRequestHeaders(req, state)
	case *extprocv3.ProcessingRequest_RequestBody:
		return s.handleRequestBody(ctx, v.RequestBody, state)
	case *extprocv3.ProcessingRequest_ResponseHeaders:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extprocv3.HeadersResponse{},
			},
		}
	case *extprocv3.ProcessingRequest_ResponseBody:
		return s.handleResponseBody(ctx, v.ResponseBody, state)
	case *extprocv3.ProcessingRequest_RequestTrailers:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestTrailers{
				RequestTrailers: &extprocv3.TrailersResponse{},
			},
		}
	case *extprocv3.ProcessingRequest_ResponseTrailers:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseTrailers{
				ResponseTrailers: &extprocv3.TrailersResponse{},
			},
		}
	default:
		log.Printf("unknown request type: %T", v)
		return &extprocv3.ProcessingResponse{}
	}
}

// handleRequestHeaders parses guardrail configuration from metadata or request headers.
func (s *Server) handleRequestHeaders(req *extprocv3.ProcessingRequest, state *streamState) *extprocv3.ProcessingResponse {
	// Primary: parse config from metadata_context (populated when Envoy forwards dynamic metadata)
	if req.MetadataContext != nil {
		config, err := s.parseMetadata(req.MetadataContext)
		if err != nil {
			log.Printf("error parsing guardrail metadata: %v", err)
		} else {
			state.config = config
		}
	}

	// Fallback: read config from x-guardrail-* request headers injected by the gateway's
	// Lua HTTP filter via EnvoyPatchPolicy so they are visible to ext_proc.
	if state.config == nil {
		if hdrs := req.GetRequestHeaders(); hdrs != nil {
			config, err := s.parseGuardrailHeaders(hdrs)
			if err != nil {
				log.Printf("error parsing guardrail headers: %v", err)
			} else {
				state.config = config
			}
		}
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{},
		},
	}
}

// handleRequestBody processes the request body with guardrail inspection.
func (s *Server) handleRequestBody(ctx context.Context, body *extprocv3.HttpBody, state *streamState) *extprocv3.ProcessingResponse {
	// If no config is set or pre_call mode is not enabled, passthrough
	if state.config == nil || !s.modeEnabled(state.config, metadata.ModePreCall) {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Select appropriate protocol parser
	parser := s.protocolRegistry.SelectParser(ctx, body.Body, nil)
	if parser == nil {
		// No parser available, passthrough
		log.Printf("no protocol parser available for request body")
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Parse request and extract texts
	texts, shouldInspect, err := parser.ParseRequest(ctx, body.Body)
	if err != nil {
		log.Printf("error parsing request: %v", err)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// If not inspectable or no texts to inspect, passthrough
	if !shouldInspect || len(texts) == 0 {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Create provider instance
	prov, err := s.createProvider(state.config)
	if err != nil {
		log.Printf("error creating provider: %v", err)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Process each text with the provider
	replacements := make(map[string]string)
	for _, text := range texts {
		result, err := prov.ProcessRequest(ctx, text.Value)
		if err != nil {
			// BLOCK action - return ImmediateResponse with 403
			return s.createBlockResponse(err.Error())
		}

		// If text was modified, store replacement
		if result.Text != text.Value {
			replacements[text.Path] = result.Text
			// Store metadata for response processing
			if result.ResponseMetadata != nil {
				state.requestMetadata[text.Path] = result.ResponseMetadata
			}
		}
	}

	// If there are replacements, apply MASK action
	if len(replacements) > 0 {
		modifiedBody, err := parser.ReplaceTexts(ctx, body.Body, replacements)
		if err != nil {
			log.Printf("error replacing texts: %v", err)
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestBody{
					RequestBody: &extprocv3.BodyResponse{},
				},
			}
		}

		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation: contentLengthMutation(len(modifiedBody)),
						BodyMutation: &extprocv3.BodyMutation{
							Mutation: &extprocv3.BodyMutation_Body{
								Body: modifiedBody,
							},
						},
					},
				},
			},
		}
	}

	// ALLOW action - passthrough
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{},
		},
	}
}

// contentLengthMutation returns a HeaderMutation that removes the content-length
// header. Required when mutating the HTTP body so Envoy recomputes the length
// correctly rather than rejecting with
// "mismatch_between_content_length_and_the_length_of_the_mutated_body".
func contentLengthMutation(_ int) *extprocv3.HeaderMutation {
	return &extprocv3.HeaderMutation{
		RemoveHeaders: []string{"content-length"},
	}
}

// handleResponseBody processes the response body with guardrail inspection.
func (s *Server) handleResponseBody(ctx context.Context, body *extprocv3.HttpBody, state *streamState) *extprocv3.ProcessingResponse {
	// If no config is set or post_call mode is not enabled, passthrough
	if state.config == nil || !s.modeEnabled(state.config, metadata.ModePostCall) {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Select appropriate protocol parser
	parser := s.protocolRegistry.SelectParser(ctx, body.Body, nil)
	if parser == nil {
		// No parser available, passthrough
		log.Printf("no protocol parser available for response body")
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Parse response and extract texts
	texts, shouldInspect, err := parser.ParseResponse(ctx, body.Body)
	if err != nil {
		log.Printf("error parsing response: %v", err)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// If not inspectable or no texts to inspect, passthrough
	if !shouldInspect || len(texts) == 0 {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Create provider instance
	prov, err := s.createProvider(state.config)
	if err != nil {
		log.Printf("error creating provider: %v", err)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Process each text with the provider
	// If we have request metadata, use ProcessResponse to deanonymize
	// Otherwise, use ProcessRequest for direct inspection
	replacements := make(map[string]string)
	for _, text := range texts {
		var processedText string
		var err error

		if reqMeta, ok := state.requestMetadata[text.Path]; ok {
			// Deanonymize using request metadata
			processedText, err = prov.ProcessResponse(ctx, text.Value, reqMeta)
		} else {
			// Direct inspection
			result, err := prov.ProcessRequest(ctx, text.Value)
			if err != nil {
				// BLOCK action in response - return ImmediateResponse with 403
				return s.createBlockResponse(err.Error())
			}
			processedText = result.Text
		}

		if err != nil {
			log.Printf("error processing response text: %v", err)
			continue
		}

		// If text was modified, store replacement
		if processedText != text.Value {
			replacements[text.Path] = processedText
		}
	}

	// If there are replacements, apply MASK action
	if len(replacements) > 0 {
		modifiedBody, err := parser.ReplaceTexts(ctx, body.Body, replacements)
		if err != nil {
			log.Printf("error replacing texts: %v", err)
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ResponseBody{
					ResponseBody: &extprocv3.BodyResponse{},
				},
			}
		}

		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation: contentLengthMutation(len(modifiedBody)),
						BodyMutation: &extprocv3.BodyMutation{
							Mutation: &extprocv3.BodyMutation_Body{
								Body: modifiedBody,
							},
						},
					},
				},
			},
		}
	}

	// ALLOW action - passthrough
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{},
		},
	}
}

// parseGuardrailHeaders reads guardrail config from x-guardrail-* HTTP request headers.
// This is a fallback for when metadata_context is absent (e.g. route metadata not in dynamic store).
func (s *Server) parseGuardrailHeaders(hdrs *extprocv3.HttpHeaders) (*metadata.GuardrailConfig, error) {
	if hdrs.GetHeaders() == nil {
		return nil, nil
	}
	fields := make(map[string]string)
	for _, h := range hdrs.GetHeaders().GetHeaders() {
		val := h.Value
		if len(h.RawValue) > 0 {
			val = string(h.RawValue)
		}
		switch strings.ToLower(h.Key) {
		case "x-guardrail-provider":
			fields["guardrail.provider"] = val
		case "x-guardrail-mode":
			fields["guardrail.mode"] = val
		case "x-guardrail-presidio-endpoint":
			fields["guardrail.presidio.endpoint"] = val
		case "x-guardrail-presidio-language":
			fields["guardrail.presidio.language"] = val
		case "x-guardrail-presidio-score-thresholds":
			fields["guardrail.presidio.score_thresholds"] = val
		case "x-guardrail-presidio-entity-actions":
			fields["guardrail.presidio.entity_actions"] = val
		}
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return metadata.ParseGuardrailConfig(fields)
}

// parseMetadata extracts guardrail configuration from ext_proc metadata.
func (s *Server) parseMetadata(metadataCtx *corev3.Metadata) (*metadata.GuardrailConfig, error) {
	if metadataCtx == nil || metadataCtx.FilterMetadata == nil {
		return nil, nil
	}

	// Extract metadata from a namespace (e.g., "envoy.filters.http.ext_proc")
	// For now, we'll look in the default namespace
	var metadataStruct *structpb.Struct
	for _, v := range metadataCtx.FilterMetadata {
		metadataStruct = v
		break // Use the first available namespace
	}

	if metadataStruct == nil {
		return nil, nil
	}

	// Convert structpb.Struct to map[string]string
	fields := make(map[string]string)
	for key, value := range metadataStruct.Fields {
		if strVal := value.GetStringValue(); strVal != "" {
			fields[key] = strVal
		}
	}

	return metadata.ParseGuardrailConfig(fields)
}

// createProvider creates a guardrail provider instance from the configuration.
func (s *Server) createProvider(config *metadata.GuardrailConfig) (provider.GuardrailProvider, error) {
	if config == nil {
		return nil, fmt.Errorf("no configuration provided")
	}

	switch config.Provider {
	case "presidio-api":
		if config.Presidio == nil {
			return nil, fmt.Errorf("presidio configuration missing")
		}
		return presidio.New(presidio.Config{
			Endpoint:        config.Presidio.Endpoint,
			Language:        config.Presidio.Language,
			ScoreThresholds: config.Presidio.ScoreThresholds,
			EntityActions:   config.Presidio.EntityActions,
		}), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", config.Provider)
	}
}

// modeEnabled checks if the given mode is enabled in the configuration.
func (s *Server) modeEnabled(config *metadata.GuardrailConfig, mode metadata.Mode) bool {
	if config == nil {
		return false
	}
	for _, m := range config.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// createBlockResponse creates an ImmediateResponse with 403 status and JSON error body.
func (s *Server) createBlockResponse(reason string) *extprocv3.ProcessingResponse {
	errorBody := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    "GUARDRAIL_VIOLATION",
			"message": reason,
		},
	}

	bodyBytes, err := json.Marshal(errorBody)
	if err != nil {
		log.Printf("error marshaling error body: %v", err)
		bodyBytes = []byte(`{"error":{"code":"GUARDRAIL_VIOLATION","message":"Request blocked by guardrail"}}`)
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extprocv3.ImmediateResponse{
				Status: &typev3.HttpStatus{
					Code: typev3.StatusCode_Forbidden,
				},
				Body:    bodyBytes,
				Details: reason,
			},
		},
	}
}
