# CLAUDE.md - Guardrail Adapter Development Guide

## Project Overview

Middleware service connecting API gateways (Envoy) with guardrail providers for real-time content moderation and policy enforcement for LLM applications using Envoy's ext_proc protocol.

## Essential Commands

```bash
# Build
make build              # Output: bin/adapter

# Test
make test               # Output: cover.out

# Quality
make lint               # Run golangci-lint
make lint-fix           # Auto-fix issues
make fmt                # Format code
make vet                # Run go vet

# Run
make run                # From source
./bin/adapter --addr :9001 --health-addr :8080

# Docker
make docker-build       # Build image
make docker-buildx      # Multi-platform build (amd64, arm64)
```

## Architecture

**Main Components:**
1. **ext_proc Server** (`internal/extproc/`) - Envoy External Processing gRPC protocol, currently passthrough mode
2. **MCP Parser** (planned) - Parse Model Context Protocol messages
3. **Provider Interface** (planned) - Extensible interface for guardrail providers

**Configuration:**
- `--addr` :9001 (gRPC ext_proc server)
- `--health-addr` :8080 (HTTP health check)

## File Structure

```
cmd/adapter/main.go              # Entry point, server initialization
internal/extproc/
  server.go                      # ext_proc protocol implementation
  server_test.go                 # Unit tests
.github/workflows/               # CI/CD (lint, test, publish)
.golangci.yml                    # Linter configuration
Makefile                         # Build automation
```
