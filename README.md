# guardrail-adapter

The guardrail adapter connects gateways with guardrail providers using Envoy's External Processing (ext_proc) API.

## Overview

The guardrail-adapter acts as a bridge between API gateways (like Envoy) and various guardrail providers, enabling real-time content moderation, safety checks, and policy enforcement for LLM applications. It implements Envoy's External Processing protocol to intercept and process requests and responses.

### Architecture

The adapter consists of three main components:

1. **ext_proc Server**: Implements Envoy's External Processing gRPC protocol to handle request/response interception
2. **MCP Parser**: Parses and validates Model Context Protocol (MCP) messages for guardrail evaluation
3. **Provider Interface**: Extensible interface for integrating with various guardrail providers

The adapter operates in passthrough mode by default, allowing traffic to flow while infrastructure is established. Future implementations will add actual guardrail evaluation logic.

## Building and Running

### Prerequisites

- Go 1.24 or later
- Docker (optional, for containerized deployment)
- Make

### Build from Source

```bash
# Build the binary
make build

# The binary will be in bin/adapter
./bin/adapter --help
```

### Run Tests

```bash
# Run all tests with coverage
make test

# Run linting
make lint

# Auto-fix linting issues
make lint-fix

# Format code
make fmt

# Run go vet
make vet
```

### Run the Adapter

```bash
# Run directly with default settings
make run

# Or run the binary with custom flags
./bin/adapter --addr :9001 --health-addr :8080
```

### Configuration Flags

- `--addr`: Address for the gRPC ext_proc server (default: `:9001`)
- `--health-addr`: Address for the HTTP health check server (default: `:8080`)

## Docker Usage

### Build Docker Image

```bash
# Build with default tag
make docker-build

# Build with custom tag
make docker-build IMG=ghcr.io/agentic-layer/guardrail-adapter:v0.1.0
```

### Build Multi-Platform Image

```bash
# Build and push for linux/amd64 and linux/arm64
make docker-buildx IMG=ghcr.io/agentic-layer/guardrail-adapter:v0.1.0
```

### Run with Docker

```bash
# Pull and run the latest image
docker run -p 9001:9001 -p 8080:8080 ghcr.io/agentic-layer/guardrail-adapter:latest

# Run with custom configuration
docker run -p 9001:9001 -p 8080:8080 \
  ghcr.io/agentic-layer/guardrail-adapter:latest \
  --addr :9001 --health-addr :8080
```

### Health Checks

The adapter exposes a health check endpoint on the HTTP server:

```bash
curl http://localhost:8080/health
# Returns: OK
```

## Development

### Project Structure

```
.
├── cmd/
│   └── adapter/          # Main entry point
│       └── main.go
├── internal/
│   └── extproc/          # ext_proc server implementation
│       ├── server.go
│       └── server_test.go
├── .github/
│   └── workflows/        # CI/CD workflows
├── Dockerfile            # Multi-stage Docker build
├── Makefile             # Build automation
├── go.mod               # Go module definition
└── README.md            # This file
```

### Testing Strategy

The project includes unit tests for all core functionality:

- **ext_proc tests**: Validate passthrough behavior for all request types
- **Stream handling**: Test error conditions and context cancellation
- **Integration tests**: Future tests will validate end-to-end guardrail processing

### Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests: `make test`
5. Run linting: `make lint-fix`
6. Submit a pull request

## CI/CD

The project uses GitHub Actions for continuous integration:

- **Lint**: Runs on all PRs and pushes to main/renovate branches
- **Test**: Executes test suite with coverage reporting
- **Publish**: Builds and publishes multi-platform Docker images to GitHub Container Registry

Images are automatically published on:
- Push to main branch (tagged as `latest` and branch name)
- Git tags matching `v*.*.*` (tagged with semver patterns)
- Pull requests (tagged with PR number)

## License

See [LICENSE](LICENSE) for details.

## Links

- [Design Specification](https://github.com/agentic-layer/guardrail-adapter/issues) - Detailed design and architecture
- [Implementation Plan](https://github.com/agentic-layer/guardrail-adapter/issues) - Development roadmap
