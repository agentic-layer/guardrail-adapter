# Guardrail Adapter

The guardrail adapter is a middleware service that connects API gateways with guardrail providers via Envoy's External Processing (ext_proc) API. It inspects MCP tool-call traffic and applies content moderation, PII masking, or policy enforcement before the request reaches the upstream tool server.

📖 **Documentation:** https://docs.agentic-layer.ai/guardrail-adapter/

## Development

### Prerequisites

- **Go** 1.24+
- **Docker** (with Docker Compose)
- **make**
- **curl**, and optionally [grpcurl](https://github.com/fullstorydev/grpcurl)
- For the Envoy Gateway E2E path: a local Kubernetes cluster, [Tilt](https://tilt.dev/) v0.33+, **helm**, **kubectl**, **jq**

**Tip:** [mise](https://mise.jdx.dev/) pins every tool to the version this repo uses. Run `mise install`.

### Build and run

```shell
# Build the binary
make build
# Run directly with default settings
make run
```

The adapter listens on `:9001` (gRPC ext_proc) and `:8080` (HTTP health). See the [reference docs](https://docs.agentic-layer.ai/guardrail-adapter/reference.html) for flags, environment variables, and the static config file schema.

### Test

```shell
make lint       # linting (use `make lint-fix` to auto-fix)
make fmt        # format
make vet        # go vet
make test       # unit tests with coverage
```

### Verify with end-to-end tests

Two E2E paths exercise the adapter against real services.

**Docker Compose** — adapter + Presidio + echo MCP server, no Kubernetes required:

```shell
# Start the stack
docker compose up -d
# Run the test script
./test/e2e.sh
# Tear down
docker compose down
```

**Envoy Gateway via Tilt** — full Kubernetes Gateway API wiring (kind, Docker Desktop, Colima, ...):

```shell
# Install Envoy Gateway, Presidio, echo-mcp-server, the adapter, and the
# Gateway / HTTPRoute / EnvoyExtensionPolicy / EnvoyPatchPolicy manifests
tilt up
# Run the test script (sends an MCP tools/call with a PII payload and asserts
# the adapter masked the email)
./test/e2e-gateway.sh
```

## Contributing

See the [Contribution Guide](https://github.com/agentic-layer/guardrail-adapter?tab=contributing-ov-file).
