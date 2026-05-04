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
	"github.com/agentic-layer/guardrail-adapter/internal/sse"
)

// Server implements the Envoy ExternalProcessor service.
type Server struct {
	extprocv3.UnimplementedExternalProcessorServer
	protocolRegistry *protocol.Registry
	staticConfig     *metadata.GuardrailConfig
	logger           *slog.Logger
	maxBodySize      int64
}

// streamState holds per-stream state for processing requests.
type streamState struct {
	config           *metadata.GuardrailConfig
	parser           protocol.Parser
	parserAttempted  bool
	skipResponseBody bool
	requestMetadata  map[string]interface{}

	// streaming
	requestBuf       []byte
	requestBuffered  bool // path decided: true = buffer-until-EOS, false = pass-through
	requestPathSet   bool // whether path has been decided yet for the request side
	requestAborted   bool // set after first oversize ImmediateResponse; silences later chunks
	responseBuf      []byte
	responseBuffered bool
	responsePathSet  bool
	responseAborted  bool         // set after first oversize ImmediateResponse; silences later chunks
	sseDec           *sse.Decoder // non-nil iff frame-aware SSE path is active
	sseEmitted       bool         // true once any downstream byte has been sent on SSE path
}

// NewServer creates a new ext_proc server. maxBodySize caps the per-direction
// buffer in bytes for streams that take the buffer-until-EOS inspection
// path; bodies that exceed it are rejected with an ImmediateResponse. A
// non-positive value disables the cap (intended for tests only).
func NewServer(logger *slog.Logger, staticConfig *metadata.GuardrailConfig, maxBodySize int64) *Server {
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
		maxBodySize:      maxBodySize,
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

// handleResponseHeaders inspects the upstream response. Two decisions:
//  1. Non-2xx responses are typically plain-text/HTML, not JSON-RPC; skip
//     response-body inspection (today's skip_non_2xx workaround).
//  2. text/event-stream responses are inspected via the frame-aware SSE
//     path when post_call inspection is configured and a parser was
//     sniffed from the request side; otherwise they pass through
//     chunk-by-chunk like any other body.
func (s *Server) handleResponseHeaders(hdrs *extprocv3.HttpHeaders, state *streamState) *extprocv3.ProcessingResponse {
	if httpStatus, ok := readStatus(hdrs); ok && (httpStatus < 200 || httpStatus >= 300) {
		state.skipResponseBody = true
	}

	if isSSE(hdrs) && state.config != nil && s.modeEnabled(state.config, metadata.ModePostCall) && state.parser != nil && !state.skipResponseBody {
		state.sseDec = sse.NewDecoder(int(s.maxBodySize))
		s.logger.Debug("SSE post_call inspection: frame-aware path active",
			slog.Int64("max_event_size", s.maxBodySize),
		)
	}

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{},
		},
	}
}

// isSSE reports whether the response carries a text/event-stream content type.
func isSSE(hdrs *extprocv3.HttpHeaders) bool {
	if hdrs.GetHeaders() == nil {
		return false
	}
	for _, h := range hdrs.GetHeaders().GetHeaders() {
		if !strings.EqualFold(h.Key, "content-type") {
			continue
		}
		val := h.Value
		if len(h.RawValue) > 0 {
			val = string(h.RawValue)
		}
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(val)), "text/event-stream")
	}
	return false
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
// In streamed mode, the function decides one of two paths on the first chunk:
//   - pass-through: echo each chunk as a StreamedResponse, no buffering.
//   - buffer-until-EOS: hold replies (body=nil) while accumulating, then run
//     parser+provider over the assembled buffer at EndOfStream and emit the
//     final reply with the assembled or mutated body.
//
// The path decision is recorded on streamState and stable for the rest of
// the request body chunks.
func (s *Server) handleRequestBody(ctx context.Context, body *extprocv3.HttpBody, state *streamState) *extprocv3.ProcessingResponse {
	chunkBody := body.GetBody()
	eos := body.GetEndOfStream()

	// Sniff a parser on the first non-empty chunk, regardless of mode.
	// SelectParser delegates to each parser's CanParse, which (today, for
	// MCP) requires complete JSON — partial chunks fail. To avoid locking a
	// stream into pass-through on a truncated first chunk, sniffParser
	// leaves parserAttempted=false on a NoParserMatchError against a
	// non-EOS chunk; the assembled buffer at EOS then gets a re-sniff.
	// The result feeds path selection here AND on the response side
	// (post_call-only configurations rely on this — see
	// docs/superpowers/specs/2026-04-30-shared-protocol-parser-design.md).
	if !state.parserAttempted && !state.requestPathSet && len(chunkBody) > 0 {
		s.sniffParser(ctx, chunkBody, state, eos)
	}

	// Decide the path on the first body event. If pre_call is configured
	// but the parser is still undecided (sniff inconclusive on a partial
	// chunk), keep tentatively buffering — we will re-sniff against the
	// assembled buffer at EOS.
	if !state.requestPathSet {
		preCallWanted := state.config != nil && s.modeEnabled(state.config, metadata.ModePreCall)
		state.requestBuffered = preCallWanted && (state.parser != nil || !state.parserAttempted)
		state.requestPathSet = true
		if !state.requestBuffered {
			var reason string
			switch {
			case state.config == nil:
				reason = "no_config"
			case !s.modeEnabled(state.config, metadata.ModePreCall):
				reason = "mode_disabled"
			case state.parser == nil:
				reason = "no_parser"
			}
			s.logger.Debug("request body passthrough", "reason", reason)
		}
	}

	// Path 1: pass-through. Echo each chunk verbatim.
	if !state.requestBuffered {
		return streamedRequestBodyResponse(chunkBody, eos, nil)
	}

	// Path 2: buffer-until-EOS.
	if state.requestAborted {
		return streamedRequestBodyResponse(nil, eos, nil)
	}
	state.requestBuf = append(state.requestBuf, chunkBody...)
	if s.maxBodySize > 0 && int64(len(state.requestBuf)) > s.maxBodySize {
		s.logger.Info("blocking oversized request body",
			slog.Int("size", len(state.requestBuf)),
			slog.Int64("max", s.maxBodySize),
		)
		state.requestAborted = true
		return s.createOversizeResponse(true, s.maxBodySize)
	}
	if !eos {
		return streamedRequestBodyResponse(nil, false, nil)
	}

	// EOS: if no parser has been identified yet, re-sniff against the
	// fully-assembled buffer (the partial-chunk sniff may have failed).
	// Skip the re-sniff for an empty buffer — there's nothing to inspect.
	if state.parser == nil && len(state.requestBuf) > 0 {
		s.sniffParser(ctx, state.requestBuf, state, true)
	}
	if state.parser == nil {
		s.logger.Debug("request body passthrough", "reason", "no_parser")
		return streamedRequestBodyResponse(state.requestBuf, true, nil)
	}

	// EOS: run parser+provider over the assembled buffer.
	return s.inspectAndEmitRequest(ctx, state)
}

// sniffParser attempts to identify a protocol parser for the given body bytes.
// On a NoParserMatchError encountered against a non-EOS chunk, the parser
// attempt is NOT marked as final — a later chunk (or the assembled buffer at
// EOS) can re-attempt. Any other outcome (match, non-match-on-EOS, or a
// non-NoParserMatchError) is final and locks state.parserAttempted=true.
func (s *Server) sniffParser(ctx context.Context, bodyBytes []byte, state *streamState, eos bool) {
	parser, err := s.protocolRegistry.SelectParser(ctx, bodyBytes, nil)
	if err != nil {
		var nm *protocol.NoParserMatchError
		if errors.As(err, &nm) {
			// On a partial chunk, defer the decision — there may be more bytes.
			if !eos {
				return
			}
			s.logger.Warn("no protocol parser matched request body",
				slog.Int("body_size", nm.BodySize),
				slog.String("prefix", nm.Prefix),
				slog.Any("reasons", nm.Reasons),
			)
		} else {
			s.logger.Warn("protocol parser selection failed", "error", err)
		}
		state.parser = nil
		state.parserAttempted = true
		return
	}
	state.parser = parser
	state.parserAttempted = true
	if parser != nil {
		s.logger.Debug("protocol parser selected",
			slog.String("parser", fmt.Sprintf("%T", parser)),
		)
	}
}

// inspectAndEmitRequest runs request-side inspection on state.requestBuf and
// emits the final EOS=true reply. Always returns a response — never nil.
func (s *Server) inspectAndEmitRequest(ctx context.Context, state *streamState) *extprocv3.ProcessingResponse {
	body := state.requestBuf

	texts, shouldInspect, err := state.parser.ParseRequest(ctx, body)
	if err != nil {
		s.logger.Warn("failed to parse request body", "error", err)
		return streamedRequestBodyResponse(body, true, nil)
	}
	s.logger.Debug("texts extracted from request", "count", len(texts))

	if !shouldInspect || len(texts) == 0 {
		reason := "no_texts"
		if !shouldInspect {
			reason = "not_inspectable"
		}
		s.logger.Debug("request body passthrough", "reason", reason)
		return streamedRequestBodyResponse(body, true, nil)
	}

	prov, err := s.createProvider(state.config)
	if err != nil {
		s.logger.Warn("failed to create provider", "error", err)
		return streamedRequestBodyResponse(body, true, nil)
	}

	replacements := make(map[string]string)
	for _, text := range texts {
		result, err := prov.ProcessRequest(ctx, text.Value)
		if err != nil {
			return s.createBlockResponse(err.Error())
		}
		if result.Text != text.Value {
			replacements[text.Path] = result.Text
			if result.ResponseMetadata != nil {
				state.requestMetadata[text.Path] = result.ResponseMetadata
			}
		}
	}

	if len(replacements) == 0 {
		s.logger.Debug("request body passthrough", "reason", "no_replacements")
		return streamedRequestBodyResponse(body, true, nil)
	}

	modifiedBody, err := state.parser.ReplaceTexts(ctx, body, replacements)
	if err != nil {
		s.logger.Warn("failed to replace texts in request body", "error", err)
		return streamedRequestBodyResponse(body, true, nil)
	}
	s.logger.Info("masked request body",
		slog.Int("replacements", len(replacements)),
		slog.Int("before_size", len(body)),
		slog.Int("after_size", len(modifiedBody)),
	)
	return streamedRequestBodyResponse(modifiedBody, true, contentLengthMutation())
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

// handleResponseBody mirrors handleRequestBody for the response direction.
func (s *Server) handleResponseBody(ctx context.Context, body *extprocv3.HttpBody, state *streamState) *extprocv3.ProcessingResponse {
	if state.sseDec != nil {
		return s.handleSSEResponseBody(ctx, body, state)
	}
	chunkBody := body.GetBody()
	eos := body.GetEndOfStream()

	if !state.responsePathSet {
		state.responseBuffered = state.config != nil &&
			s.modeEnabled(state.config, metadata.ModePostCall) &&
			state.parser != nil &&
			!state.skipResponseBody
		state.responsePathSet = true
		if !state.responseBuffered {
			var reason string
			switch {
			case state.config == nil:
				reason = "no_config"
			case !s.modeEnabled(state.config, metadata.ModePostCall):
				reason = "mode_disabled"
			case state.skipResponseBody:
				reason = "skip_non_2xx"
			case state.parser == nil:
				reason = "no_parser"
			}
			s.logger.Debug("response body passthrough", "reason", reason)
		}
	}

	if !state.responseBuffered {
		return streamedResponseBodyResponse(chunkBody, eos, nil)
	}

	if state.responseAborted {
		return streamedResponseBodyResponse(nil, eos, nil)
	}
	state.responseBuf = append(state.responseBuf, chunkBody...)
	if s.maxBodySize > 0 && int64(len(state.responseBuf)) > s.maxBodySize {
		s.logger.Info("blocking oversized response body",
			slog.Int("size", len(state.responseBuf)),
			slog.Int64("max", s.maxBodySize),
		)
		state.responseAborted = true
		return s.createOversizeResponse(false, s.maxBodySize)
	}
	if !eos {
		return streamedResponseBodyResponse(nil, false, nil)
	}

	return s.inspectAndEmitResponse(ctx, state)
}

// inspectAndEmitResponse runs response-side inspection on state.responseBuf
// and emits the final EOS=true reply. Always returns a response.
func (s *Server) inspectAndEmitResponse(ctx context.Context, state *streamState) *extprocv3.ProcessingResponse {
	body := state.responseBuf

	// Empty assembled buffer (e.g. 204-after-2xx, no-content terminal chunk)
	// has no JSON-RPC payload to parse — skip the parser and emit cleanly.
	if len(body) == 0 {
		s.logger.Debug("response body passthrough", "reason", "empty_body")
		return streamedResponseBodyResponse(body, true, nil)
	}

	texts, shouldInspect, err := state.parser.ParseResponse(ctx, body)
	if err != nil {
		s.logger.Warn("failed to parse response body",
			slog.Any("error", err),
			slog.String("body_prefix", protocol.Preview(body, 64)),
		)
		return streamedResponseBodyResponse(body, true, nil)
	}
	s.logger.Debug("texts extracted from response", "count", len(texts))

	if !shouldInspect || len(texts) == 0 {
		reason := "no_texts"
		if !shouldInspect {
			reason = "not_inspectable"
		}
		s.logger.Debug("response body passthrough", "reason", reason)
		return streamedResponseBodyResponse(body, true, nil)
	}

	prov, err := s.createProvider(state.config)
	if err != nil {
		s.logger.Warn("failed to create provider", "error", err)
		return streamedResponseBodyResponse(body, true, nil)
	}

	replacements := make(map[string]string)
	for _, text := range texts {
		var processedText string
		if reqMeta, ok := state.requestMetadata[text.Path]; ok {
			pt, perr := prov.ProcessResponse(ctx, text.Value, reqMeta)
			if perr != nil {
				s.logger.Warn("failed to process response text", "error", perr, "path", text.Path)
				continue
			}
			processedText = pt
		} else {
			result, perr := prov.ProcessRequest(ctx, text.Value)
			if perr != nil {
				return s.createBlockResponse(perr.Error())
			}
			processedText = result.Text
		}
		if processedText != text.Value {
			replacements[text.Path] = processedText
		}
	}

	if len(replacements) == 0 {
		s.logger.Debug("response body passthrough", "reason", "no_replacements")
		return streamedResponseBodyResponse(body, true, nil)
	}

	modifiedBody, err := state.parser.ReplaceTexts(ctx, body, replacements)
	if err != nil {
		s.logger.Warn("failed to replace texts in response body", "error", err)
		return streamedResponseBodyResponse(body, true, nil)
	}
	s.logger.Info("masked response body",
		slog.Int("replacements", len(replacements)),
		slog.Int("before_size", len(body)),
		slog.Int("after_size", len(modifiedBody)),
	)
	return streamedResponseBodyResponse(modifiedBody, true, contentLengthMutation())
}

// handleSSEResponseBody implements the frame-aware SSE inspection path.
// Each completed SSE event is run through processSSEEvent; the resulting
// bytes (verbatim, mutated, or error event) are written downstream as a
// StreamedResponse. Non-final chunks that don't complete an event reply
// with body=nil to hold downstream bytes until the next event boundary.
func (s *Server) handleSSEResponseBody(ctx context.Context, body *extprocv3.HttpBody, state *streamState) *extprocv3.ProcessingResponse {
	chunkBody := body.GetBody()
	eos := body.GetEndOfStream()

	if state.responseAborted {
		return streamedResponseBodyResponse(nil, eos, nil)
	}

	// Decoder.Write checks the per-event cap before scanning lines, so a
	// chunk that contains a complete event followed by oversize bytes
	// short-circuits with ErrEventTooLarge — the complete event's bytes
	// in this same chunk are dropped along with the oversize tail. The
	// stream still terminates cleanly with the error event + EOS below.
	events, err := state.sseDec.Write(chunkBody)
	if errors.Is(err, sse.ErrEventTooLarge) {
		return s.handleSSEOversize(state)
	}

	var out []byte
	for _, ev := range events {
		emit, blockReason := s.processSSEEvent(ctx, state, ev)
		if blockReason != "" {
			return s.handleSSEBlock(state, ev, blockReason, out)
		}
		out = append(out, emit...)
	}

	if eos {
		if final, _ := state.sseDec.Flush(); final != nil {
			emit, blockReason := s.processSSEEvent(ctx, state, *final)
			if blockReason != "" {
				return s.handleSSEBlock(state, *final, blockReason, out)
			}
			out = append(out, emit...)
		}
	}

	if len(out) > 0 {
		state.sseEmitted = true
	}
	return streamedResponseBodyResponse(out, eos, nil)
}

// processSSEEvent inspects one SSE event. Returns the bytes to emit
// (the original Raw if the event is non-inspectable or no replacements
// were made; canonically re-encoded bytes after mutation) and a
// non-empty blockReason when the provider rejects the event.
func (s *Server) processSSEEvent(ctx context.Context, state *streamState, ev sse.Event) ([]byte, string) {
	if len(ev.Data) == 0 {
		return ev.Raw, ""
	}

	texts, shouldInspect, err := state.parser.ParseResponse(ctx, ev.Data)
	if err != nil || !shouldInspect || len(texts) == 0 {
		// Notifications, server-initiated requests, and other non
		// tool-call shapes naturally land here.
		return ev.Raw, ""
	}

	prov, err := s.createProvider(state.config)
	if err != nil {
		s.logger.Warn("failed to create provider", "error", err)
		return ev.Raw, ""
	}

	replacements := make(map[string]string)
	for _, text := range texts {
		var processed string
		if reqMeta, ok := state.requestMetadata[text.Path]; ok {
			pt, perr := prov.ProcessResponse(ctx, text.Value, reqMeta)
			if perr != nil {
				s.logger.Warn("failed to process response text", "error", perr, "path", text.Path)
				continue
			}
			processed = pt
		} else {
			result, perr := prov.ProcessRequest(ctx, text.Value)
			if perr != nil {
				return nil, perr.Error()
			}
			processed = result.Text
		}
		if processed != text.Value {
			replacements[text.Path] = processed
		}
	}

	if len(replacements) == 0 {
		return ev.Raw, ""
	}

	mutated, err := state.parser.ReplaceTexts(ctx, ev.Data, replacements)
	if err != nil {
		s.logger.Warn("failed to replace texts in SSE event", "error", err)
		return ev.Raw, ""
	}
	s.logger.Info("masked SSE event",
		slog.Int("replacements", len(replacements)),
		slog.Int("event_size_before", len(ev.Raw)),
	)
	return sse.Encode(ev.Name, ev.ID, mutated), ""
}

// handleSSEBlock builds the response when a provider blocks an event.
// If no bytes have been emitted yet AND the current chunk has not yet
// emitted earlier events, swap to ImmediateResponse{403}. Otherwise
// append a JSON-RPC error event and EOS the stream.
func (s *Server) handleSSEBlock(state *streamState, ev sse.Event, reason string, prior []byte) *extprocv3.ProcessingResponse {
	s.logger.Info("blocking SSE event", "reason", reason)
	state.responseAborted = true
	if !state.sseEmitted && len(prior) == 0 {
		return s.createBlockResponse(reason)
	}
	errEvent := buildSSEErrorEvent(ev, "blocked by guardrail", -32000)
	out := append(prior, errEvent...)
	state.sseEmitted = true
	return streamedResponseBodyResponse(out, true, nil)
}

// handleSSEOversize builds the response when an SSE event exceeds the
// per-event byte cap. Same fork as handleSSEBlock but with 502 (no
// prior bytes) or a generic oversize error event.
func (s *Server) handleSSEOversize(state *streamState) *extprocv3.ProcessingResponse {
	s.logger.Info("blocking oversized SSE event",
		slog.Int("pending", state.sseDec.Pending()),
		slog.Int64("max", s.maxBodySize),
	)
	state.responseAborted = true
	if !state.sseEmitted {
		return s.createOversizeResponse(false, s.maxBodySize)
	}
	payload := []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32000,"message":"response body too large"}}`)
	errEvent := sse.Encode("message", "", payload)
	state.sseEmitted = true
	return streamedResponseBodyResponse(errEvent, true, nil)
}

// buildSSEErrorEvent constructs a JSON-RPC error event mirroring the
// offending event's id when parseable. message is the error.message;
// code is the JSON-RPC error code.
func buildSSEErrorEvent(srcEvent sse.Event, message string, code int) []byte {
	var msg struct {
		ID interface{} `json:"id"`
	}
	if len(srcEvent.Data) > 0 {
		_ = json.Unmarshal(srcEvent.Data, &msg)
	}
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      msg.ID,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	data, _ := json.Marshal(payload)
	return sse.Encode(srcEvent.Name, srcEvent.ID, data)
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

// createOversizeResponse builds an ImmediateResponse for bodies exceeding
// the configured max-body-size. requestSide=true returns 413, false returns
// 502 (so an unscanned response body never reaches the client).
func (s *Server) createOversizeResponse(requestSide bool, maxBytes int64) *extprocv3.ProcessingResponse {
	statusCode := typev3.StatusCode_BadGateway
	direction := "response"
	if requestSide {
		statusCode = typev3.StatusCode_PayloadTooLarge
		direction = "request"
	}
	body := fmt.Sprintf(`{"error":{"code":"GUARDRAIL_BODY_TOO_LARGE","message":"%s body exceeds guardrail max-body-size of %d bytes"}}`, direction, maxBytes)
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extprocv3.ImmediateResponse{
				Status:  &typev3.HttpStatus{Code: statusCode},
				Body:    []byte(body),
				Details: "body_too_large",
			},
		},
	}
}
