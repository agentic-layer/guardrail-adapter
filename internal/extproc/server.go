package extproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
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
	staticConfig     *metadata.GuardrailConfig
	logger           *slog.Logger
}

// streamState holds per-stream state for processing requests.
type streamState struct {
	config           *metadata.GuardrailConfig
	parser           protocol.Parser
	parserAttempted  bool
	skipResponseBody bool
	requestMetadata  map[string]interface{}
}

// NewServer creates a new ext_proc server. If staticConfig is non-nil, it is
// used for every request and dynamic metadata/headers are ignored. Pass nil
// to preserve the dynamic behavior. If logger is nil, slog.Default() is used.
func NewServer(logger *slog.Logger, staticConfig *metadata.GuardrailConfig) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	registry := protocol.NewRegistry(
		mcpparser.NewMCPParser(),
	)
	return &Server{
		protocolRegistry: registry,
		staticConfig:     staticConfig,
		logger:           logger,
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
			s.logger.Error("error receiving request", "error", err)
			return status.Errorf(codes.Unknown, "failed to receive request: %v", err)
		}

		// Generate response based on request type
		resp := s.handleRequest(ctx, req, state)

		// Send the response back to Envoy
		if err := stream.Send(resp); err != nil {
			s.logger.Error("error sending response", "error", err)
			return status.Errorf(codes.Unknown, "failed to send response: %v", err)
		}
	}
}

// handleRequest processes each request type and applies guardrail logic.
func (s *Server) handleRequest(ctx context.Context, req *extprocv3.ProcessingRequest, state *streamState) *extprocv3.ProcessingResponse {
	switch v := req.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:
		s.logger.Debug("received request headers")
		return s.handleRequestHeaders(req, state)
	case *extprocv3.ProcessingRequest_RequestBody:
		s.logger.Debug("received request body",
			slog.Int("body_size", len(v.RequestBody.GetBody())),
			slog.Bool("end_of_stream", v.RequestBody.GetEndOfStream()),
		)
		return s.handleRequestBody(ctx, v.RequestBody, state)
	case *extprocv3.ProcessingRequest_ResponseHeaders:
		s.logger.Debug("received response headers")
		return s.handleResponseHeaders(v.ResponseHeaders, state)
	case *extprocv3.ProcessingRequest_ResponseBody:
		s.logger.Debug("received response body",
			slog.Int("body_size", len(v.ResponseBody.GetBody())),
			slog.Bool("end_of_stream", v.ResponseBody.GetEndOfStream()),
		)
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
		s.logger.Warn("unknown request type", "type", fmt.Sprintf("%T", v))
		return &extprocv3.ProcessingResponse{}
	}
}

// handleRequestHeaders resolves the per-stream guardrail configuration.
// If the server was constructed with a static config, that value is used
// unconditionally. Otherwise the config is parsed from MetadataContext,
// falling back to x-guardrail-* request headers.
func (s *Server) handleRequestHeaders(req *extprocv3.ProcessingRequest, state *streamState) *extprocv3.ProcessingResponse {
	source := ""
	if s.staticConfig != nil {
		state.config = s.staticConfig
		source = "static"
	} else {
		// Primary: parse config from metadata_context (populated when Envoy forwards dynamic metadata)
		if req.MetadataContext != nil {
			config, err := s.parseMetadata(req.MetadataContext)
			if err != nil {
				s.logger.Warn("failed to parse guardrail metadata", "error", err)
			} else if config != nil {
				state.config = config
				source = "metadata"
			}
		}
		// Fallback: when metadata didn't yield a config, read from x-guardrail-* request
		// headers injected by the gateway's Lua HTTP filter via EnvoyPatchPolicy.
		// Envoy sends a non-nil but empty MetadataContext whenever the policy declares
		// accessibleNamespaces, so checking state.config is the only reliable signal.
		if state.config == nil {
			if hdrs := req.GetRequestHeaders(); hdrs != nil {
				config, err := s.parseGuardrailHeaders(hdrs)
				if err != nil {
					s.logger.Warn("failed to parse guardrail headers", "error", err)
				} else if config != nil {
					state.config = config
					source = "headers"
				}
			}
		}
	}

	if state.config != nil {
		s.logger.Debug("guardrail config resolved",
			slog.String("source", source),
			slog.String("provider", state.config.Provider),
			slog.Any("modes", state.config.Modes),
		)
	} else {
		s.logger.Debug("no guardrail config resolved, requests will passthrough")
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{},
		},
	}
}

// handleResponseHeaders inspects the upstream response status. Non-2xx
// responses (typically plain-text or HTML error pages) cannot be parsed as
// JSON-RPC, so we mark the stream to skip body inspection and avoid noisy
// parse errors on the response body.
func (s *Server) handleResponseHeaders(hdrs *extprocv3.HttpHeaders, state *streamState) *extprocv3.ProcessingResponse {
	if status, ok := readStatus(hdrs); ok && (status < 200 || status >= 300) {
		state.skipResponseBody = true
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{},
		},
	}
}

// readStatus extracts the HTTP :status pseudo-header as an int.
func readStatus(hdrs *extprocv3.HttpHeaders) (int, bool) {
	if hdrs.GetHeaders() == nil {
		return 0, false
	}
	for _, h := range hdrs.GetHeaders().GetHeaders() {
		if h.Key != ":status" {
			continue
		}
		val := h.Value
		if len(h.RawValue) > 0 {
			val = string(h.RawValue)
		}
		code, err := strconv.Atoi(val)
		if err != nil {
			return 0, false
		}
		return code, true
	}
	return 0, false
}

// handleRequestBody processes the request body with guardrail inspection.
func (s *Server) handleRequestBody(ctx context.Context, body *extprocv3.HttpBody, state *streamState) *extprocv3.ProcessingResponse {
	// Empty bodies (e.g. trailing chunks, GETs) carry no protocol signal.
	if len(body.Body) == 0 {
		s.logger.Debug("request body passthrough", "reason", "empty_body")
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Identify the protocol from the first non-empty request body. The result
	// (which may be nil) is shared with the response handler via streamState.
	if !state.parserAttempted {
		parser, err := s.protocolRegistry.SelectParser(ctx, body.Body, nil)
		state.parser = parser
		state.parserAttempted = true
		if err != nil {
			var nm *protocol.NoParserMatchError
			if errors.As(err, &nm) {
				s.logger.Warn("no protocol parser matched request body",
					slog.Int("body_size", nm.BodySize),
					slog.String("prefix", nm.Prefix),
					slog.Any("reasons", nm.Reasons),
				)
			} else {
				s.logger.Warn("protocol parser selection failed", "error", err)
			}
		} else if parser != nil {
			s.logger.Debug("protocol parser selected",
				slog.String("parser", fmt.Sprintf("%T", parser)),
			)
		}
	}

	// If pre_call inspection isn't requested, pass through without inspecting.
	if state.config == nil || !s.modeEnabled(state.config, metadata.ModePreCall) {
		reason := "mode_disabled"
		if state.config == nil {
			reason = "no_config"
		}
		s.logger.Debug("request body passthrough", "reason", reason)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// No parser available, passthrough (parser-selection error already logged).
	if state.parser == nil {
		s.logger.Debug("request body passthrough", "reason", "no_parser")
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Parse request and extract texts
	texts, shouldInspect, err := state.parser.ParseRequest(ctx, body.Body)
	if err != nil {
		s.logger.Warn("failed to parse request body", "error", err)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	s.logger.Debug("texts extracted from request", "count", len(texts))
	// If not inspectable or no texts to inspect, passthrough
	if !shouldInspect || len(texts) == 0 {
		reason := "no_texts"
		if !shouldInspect {
			reason = "not_inspectable"
		}
		s.logger.Debug("request body passthrough", "reason", reason)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Create provider instance
	prov, err := s.createProvider(state.config)
	if err != nil {
		s.logger.Warn("failed to create provider", "error", err)
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
		modifiedBody, err := state.parser.ReplaceTexts(ctx, body.Body, replacements)
		if err != nil {
			s.logger.Warn("failed to replace texts in request body", "error", err)
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestBody{
					RequestBody: &extprocv3.BodyResponse{},
				},
			}
		}
		s.logger.Info("masked request body",
			slog.Int("replacements", len(replacements)),
			slog.Int("before_size", len(body.Body)),
			slog.Int("after_size", len(modifiedBody)),
		)

		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation: contentLengthMutation(),
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
	s.logger.Debug("request body passthrough", "reason", "no_replacements")
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
func contentLengthMutation() *extprocv3.HeaderMutation {
	return &extprocv3.HeaderMutation{
		RemoveHeaders: []string{"content-length"},
	}
}

// streamedRequestBodyResponse constructs a ProcessingResponse for the request
// direction carrying a StreamedBodyResponse mutation. Use body=nil to "hold"
// (emit no bytes downstream while we accumulate); use a non-nil body to emit
// (either echoing the original chunk or sending the assembled+mutated body).
func streamedRequestBodyResponse(body []byte, endOfStream bool, headerMutation *extprocv3.HeaderMutation) *extprocv3.ProcessingResponse {
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation: &extprocv3.BodyMutation{
						Mutation: &extprocv3.BodyMutation_StreamedResponse{
							StreamedResponse: &extprocv3.StreamedBodyResponse{
								Body:        body,
								EndOfStream: endOfStream,
							},
						},
					},
				},
			},
		},
	}
}

// streamedResponseBodyResponse is the response-direction counterpart to
// streamedRequestBodyResponse. See its docstring for body=nil semantics.
func streamedResponseBodyResponse(body []byte, endOfStream bool, headerMutation *extprocv3.HeaderMutation) *extprocv3.ProcessingResponse {
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation: &extprocv3.BodyMutation{
						Mutation: &extprocv3.BodyMutation_StreamedResponse{
							StreamedResponse: &extprocv3.StreamedBodyResponse{
								Body:        body,
								EndOfStream: endOfStream,
							},
						},
					},
				},
			},
		},
	}
}

// handleResponseBody processes the response body with guardrail inspection.
func (s *Server) handleResponseBody(ctx context.Context, body *extprocv3.HttpBody, state *streamState) *extprocv3.ProcessingResponse {
	// Empty bodies (end-of-stream chunks, no-content responses) carry no payload to inspect.
	if len(body.Body) == 0 {
		s.logger.Debug("response body passthrough", "reason", "empty_body")
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Non-2xx upstream responses are typically plain-text or HTML error pages
	// rather than JSON-RPC. Skip parsing entirely.
	if state.skipResponseBody {
		s.logger.Debug("response body passthrough", "reason", "skip_non_2xx")
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// If no config is set or post_call mode is not enabled, passthrough
	if state.config == nil || !s.modeEnabled(state.config, metadata.ModePostCall) {
		reason := "mode_disabled"
		if state.config == nil {
			reason = "no_config"
		}
		s.logger.Debug("response body passthrough", "reason", reason)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Reuse the parser identified from the request body. If the request body
	// was empty or no parser matched, the protocol is unknown and we passthrough.
	if state.parser == nil {
		s.logger.Debug("response body passthrough", "reason", "no_parser")
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Parse response and extract texts
	texts, shouldInspect, err := state.parser.ParseResponse(ctx, body.Body)
	if err != nil {
		s.logger.Warn("failed to parse response body",
			slog.Any("error", err),
			slog.String("body_prefix", protocol.Preview(body.Body, 64)),
		)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	s.logger.Debug("texts extracted from response", "count", len(texts))
	// If not inspectable or no texts to inspect, passthrough
	if !shouldInspect || len(texts) == 0 {
		reason := "no_texts"
		if !shouldInspect {
			reason = "not_inspectable"
		}
		s.logger.Debug("response body passthrough", "reason", reason)
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
	}

	// Create provider instance
	prov, err := s.createProvider(state.config)
	if err != nil {
		s.logger.Warn("failed to create provider", "error", err)
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
			s.logger.Warn("failed to process response text", "error", err, "path", text.Path)
			continue
		}

		// If text was modified, store replacement
		if processedText != text.Value {
			replacements[text.Path] = processedText
		}
	}

	// If there are replacements, apply MASK action
	if len(replacements) > 0 {
		modifiedBody, err := state.parser.ReplaceTexts(ctx, body.Body, replacements)
		if err != nil {
			s.logger.Warn("failed to replace texts in response body", "error", err)
			return &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ResponseBody{
					ResponseBody: &extprocv3.BodyResponse{},
				},
			}
		}
		s.logger.Info("masked response body",
			slog.Int("replacements", len(replacements)),
			slog.Int("before_size", len(body.Body)),
			slog.Int("after_size", len(modifiedBody)),
		)

		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation: contentLengthMutation(),
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
	s.logger.Debug("response body passthrough", "reason", "no_replacements")
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
	s.logger.Info("blocking request", "reason", reason)
	errorBody := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    "GUARDRAIL_VIOLATION",
			"message": reason,
		},
	}

	bodyBytes, err := json.Marshal(errorBody)
	if err != nil {
		s.logger.Error("failed to marshal block-response body", "error", err)
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
