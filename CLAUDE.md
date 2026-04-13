# CLAUDE.md - Guardrail Adapter Development Guide

## Project Overview

The **guardrail-adapter** is a middleware service that connects API gateways (like Envoy) with guardrail providers to enable real-time content moderation, safety checks, and policy enforcement for LLM applications. It implements Envoy's External Processing (ext_proc) protocol to intercept and process HTTP requests and responses.

### Purpose

- Bridge between Envoy gateways and guardrail providers
- Real-time request/response interception for LLM traffic
- Extensible architecture for multiple guardrail providers
- High-performance gRPC-based processing

## Essential Commands

### Building

```bash
# Build the adapter binary
make build

# Build output: bin/adapter
```

### Testing

```bash
# Run all tests with coverage
make test

# Coverage report: cover.out
```

### Code Quality

```bash
# Run linter
make lint

# Auto-fix linting issues
make lint-fix

# Format code
make fmt

# Run go vet
make vet
```

### Running

```bash
# Run from source
make run

# Run binary directly
./bin/adapter --addr :9001 --health-addr :8080
```

### Docker

```bash
# Build Docker image
make docker-build

# Build multi-platform (amd64, arm64)
make docker-buildx

# Run container
docker run -p 9001:9001 -p 8080:8080 ghcr.io/agentic-layer/guardrail-adapter:latest
```

## Architecture

### High-Level Components

1. **ext_proc Server** (`internal/extproc/`)
   - Implements Envoy's External Processing gRPC protocol
   - Handles bidirectional streaming for request/response interception
   - Currently operates in passthrough mode (returns empty responses)
   - Future: Will invoke guardrail evaluation logic

2. **MCP Parser** (planned)
   - Parse Model Context Protocol messages
   - Extract LLM prompts and completions
   - Validate message structure
   - Extract metadata for guardrail context

3. **Provider Interface** (planned)
   - Extensible interface for guardrail providers
   - Support for multiple providers (OpenAI Moderation, Azure Content Safety, etc.)
   - Provider-specific configuration
   - Result aggregation and decision logic

4. **Metadata Parser** (planned)
   - Extract user/session context from request headers
   - Parse custom metadata for policy decisions
   - Context enrichment for guardrail evaluation

### Request Flow

```
Envoy → ext_proc gRPC → Guardrail Adapter → Provider API
         ↑                                      ↓
         └──────────── Decision ←──────────────┘
```

1. Envoy sends request to adapter via ext_proc protocol
2. Adapter extracts MCP messages from request body
3. Adapter calls guardrail provider(s) with content
4. Provider returns safety/policy decision
5. Adapter sends response to Envoy (allow/block/modify)

### Current Implementation Status

**Implemented:**
- ✅ gRPC ext_proc server with bidirectional streaming
- ✅ Passthrough mode for all request types (headers, body, trailers)
- ✅ Health check HTTP endpoint
- ✅ Graceful shutdown handling
- ✅ Comprehensive unit tests

**Planned:**
- 🔲 MCP message parsing
- 🔲 Guardrail provider interface
- 🔲 Provider integrations (OpenAI, Azure, etc.)
- 🔲 Metadata extraction and enrichment
- 🔲 Configuration management
- 🔲 Metrics and observability

## File Structure

```
guardrail-adapter/
├── cmd/
│   └── adapter/
│       └── main.go              # Entry point, flag parsing, server initialization
├── internal/
│   └── extproc/
│       ├── server.go            # ext_proc gRPC server implementation
│       └── server_test.go       # Unit tests for server
├── .github/
│   └── workflows/
│       ├── lint.yml             # Linting workflow
│       ├── test.yml             # Testing workflow
│       └── publish.yml          # Docker publish workflow
├── .golangci.yml               # Linter configuration
├── .gitignore                  # Git ignore patterns
├── .dockerignore               # Docker ignore patterns
├── Dockerfile                  # Multi-stage Docker build
├── Makefile                    # Build automation
├── go.mod                      # Go module definition
├── go.sum                      # Go dependency checksums
├── README.md                   # User-facing documentation
└── CLAUDE.md                   # This file
```

### Key Files

- **`cmd/adapter/main.go`**: Server initialization, flag parsing, signal handling
- **`internal/extproc/server.go`**: Core ext_proc protocol implementation
- **`internal/extproc/server_test.go`**: Comprehensive tests for passthrough behavior
- **`Makefile`**: Build targets for development workflow
- **`Dockerfile`**: Multi-stage build for minimal production image

## Testing Strategy

### Unit Tests

Located in `internal/extproc/server_test.go`:

- **Passthrough Tests**: Verify correct response types for each request type
  - Request headers
  - Request body
  - Response headers
  - Response body
  - Request trailers
  - Response trailers

- **Error Handling Tests**:
  - Context cancellation
  - Empty streams
  - Send errors

### Running Tests

```bash
# Run all tests
make test

# Run specific package
go test ./internal/extproc -v

# Run with coverage
go test ./... -coverprofile=cover.out
go tool cover -html=cover.out
```

### Test Coverage

Current coverage: **83.3%** for `internal/extproc`

Target: **>80%** for all packages

## Configuration

### Command-Line Flags

- `--addr`: gRPC server address (default: `:9001`)
  - Format: `host:port` or `:port`
  - Example: `--addr 0.0.0.0:9001`

- `--health-addr`: HTTP health check server address (default: `:8080`)
  - Format: `host:port` or `:port`
  - Example: `--health-addr :8080`

### Environment Variables

Currently none. Future: configuration for providers, policies, etc.

### Health Checks

HTTP endpoint: `GET /health`
- Returns: `200 OK` with body `OK\n`
- Used by Kubernetes liveness/readiness probes

gRPC health check service:
- Implements `grpc.health.v1.Health/Check`
- Status: `SERVING`

## Development Workflow

### Making Changes

1. Create a feature branch
2. Make code changes
3. Run formatting: `make fmt`
4. Run tests: `make test`
5. Run linting: `make lint-fix`
6. Build: `make build`
7. Test manually: `./bin/adapter`
8. Submit PR

### Adding New Features

When adding guardrail logic:

1. Define provider interface in `internal/provider/`
2. Implement MCP parsing in `internal/mcp/`
3. Update `internal/extproc/server.go` to call providers
4. Add tests for new functionality
5. Update documentation

### Debugging

```bash
# Run with verbose logging (future)
./bin/adapter --log-level debug

# Test with envoy locally
# (see envoy config examples in docs/)

# Use grpcurl for manual testing
grpcurl -plaintext localhost:9001 list
```

## CI/CD

### GitHub Actions Workflows

1. **Lint** (`.github/workflows/lint.yml`)
   - Triggers: PR, push to main/renovate branches
   - Runs: golangci-lint v2.11.4
   - Go version: from go.mod

2. **Test** (`.github/workflows/test.yml`)
   - Triggers: PR, push to main/renovate branches
   - Runs: `make test`
   - Includes: go mod tidy check

3. **Publish** (`.github/workflows/publish.yml`)
   - Triggers: push to main, tags (v*.*.*), PR, workflow_dispatch
   - Builds: Multi-platform Docker images (linux/amd64, linux/arm64)
   - Publishes: ghcr.io/agentic-layer/guardrail-adapter
   - Tags: semver, branch, PR, latest

### Release Process

1. Create version tag: `git tag v0.1.0`
2. Push tag: `git push origin v0.1.0`
3. GitHub Actions builds and publishes Docker image
4. Image available at: `ghcr.io/agentic-layer/guardrail-adapter:v0.1.0`

## Links

- [Design Specification](https://github.com/agentic-layer/guardrail-adapter/issues) - Detailed design and architecture
- [Implementation Plan](https://github.com/agentic-layer/guardrail-adapter/issues) - Development roadmap
- [Envoy ext_proc Protocol](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/ext_proc_filter) - External processing documentation
- [Model Context Protocol (MCP)](https://spec.modelcontextprotocol.io/) - Message format specification

## Common Tasks

### Add a new linter rule

Edit `.golangci.yml` and add to the `enable` list under `linters`.

### Update Go version

1. Update `go.mod`: `go mod edit -go=1.24`
2. Update `Dockerfile`: change `FROM golang:1.24`
3. Run: `go mod tidy`

### Add a new Makefile target

Add to `Makefile` following the pattern:

```makefile
.PHONY: my-target
my-target: dependencies ## Description shown in help
	command to run
```

### Troubleshooting

**Issue: Tests fail with gRPC errors**
- Solution: Check mock implementations in test files
- Ensure context is not cancelled prematurely

**Issue: Docker build fails**
- Solution: Check Dockerfile.cross is removed from previous build
- Run: `docker buildx rm guardrail-adapter-builder`

**Issue: Lint failures**
- Solution: Run `make lint-fix` to auto-fix
- Check `.golangci.yml` for rule exclusions if needed
