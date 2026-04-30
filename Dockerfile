# Build stage
FROM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

# Copy go.mod and go.sum first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o adapter ./cmd/adapter

# Runtime stage - using distroless for minimal container
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the binary from builder
COPY --from=builder /build/adapter /adapter

# Use non-root user (nonroot user in distroless is UID 65532)
USER 65532:65532

# Expose default ports
EXPOSE 9001 8080

# Run the adapter
ENTRYPOINT ["/adapter"]
