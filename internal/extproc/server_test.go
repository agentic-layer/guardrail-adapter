package extproc

import (
	"context"
	"io"
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	server := NewServer()

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
	server := NewServer()

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
	server := NewServer()

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
