package extproc

import (
	"io"
	"log"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements the Envoy ExternalProcessor service.
type Server struct {
	extprocv3.UnimplementedExternalProcessorServer
}

// NewServer creates a new ext_proc server.
func NewServer() *Server {
	return &Server{}
}

// Process implements the bidirectional streaming RPC for external processing.
// It passes all traffic through without inspection (passthrough mode).
func (s *Server) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	ctx := stream.Context()

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

		// Generate passthrough response based on request type
		resp := s.handleRequest(req)

		// Send the response back to Envoy
		if err := stream.Send(resp); err != nil {
			log.Printf("error sending response: %v", err)
			return status.Errorf(codes.Unknown, "failed to send response: %v", err)
		}
	}
}

// handleRequest generates a passthrough response for each request type.
func (s *Server) handleRequest(req *extprocv3.ProcessingRequest) *extprocv3.ProcessingResponse {
	switch v := req.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{},
			},
		}
	case *extprocv3.ProcessingRequest_RequestBody:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}
	case *extprocv3.ProcessingRequest_ResponseHeaders:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extprocv3.HeadersResponse{},
			},
		}
	case *extprocv3.ProcessingRequest_ResponseBody:
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}
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
