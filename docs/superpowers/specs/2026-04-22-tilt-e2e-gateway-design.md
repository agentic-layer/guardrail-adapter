# Tilt-based E2E Test with Envoy Gateway

**Date:** 2026-04-22
**Status:** Design
**Area:** Local development / E2E testing

## Goal

Validate the guardrail-adapter end-to-end against a real Envoy data plane, driving PII-masking behavior through a Kubernetes Gateway API stack. A developer should be able to run `tilt up` followed by `./test/e2e-gateway.sh` and see the adapter mask PII inside an MCP tool call that transits an Envoy Gateway.

This is strictly a local-development and CI-smoke artifact. It does not replace the existing `compose.yml` / `test/e2e.sh` path, which exercises the adapter's gRPC interface directly.

## Non-goals

- Creating or managing the local Kubernetes cluster. Tilt consumes whatever `kubectl` context is active (kind, Docker Desktop, Colima, etc.).
- Full production Envoy Gateway hardening. Security-relevant caveats are documented below but not addressed.
- Adding the agentic-layer CRDs (`ToolGateway`, `GuardrailProvider`, `Guard`, `ToolServer`). Only upstream Gateway API + Envoy Gateway types are used.
- Response-side (post_call) masking in the first cut. Tracked as follow-up work.

## Architecture

```
[test/e2e-gateway.sh]
       │ HTTP POST /mcp (JSON-RPC MCP over streamable-HTTP)
       ▼
┌─────────────────────────────────────────────┐
│ Envoy data plane (auto-created by EG)       │ ns: envoy-gateway-system
│  Deployment/Service: envoy-default-eg       │ (port-forward: 10000 → 80)
│                                             │
│  Filter chain (per-HTTPRoute):              │
│    …                                        │
│    envoy.filters.http.ext_proc              │───┐
│    router                                   │   │
└─────────────────────────┬───────────────────┘   │
                          │                       │ ext_proc gRPC
                          │                       │ metadata_context.filter_metadata
                          │                       │  ["envoy.filters.http.ext_proc"]
                          │                       ▼
                          │             ┌───────────────────────┐       HTTP
                          │             │ guardrail-adapter     │──────────────▶┌────────────────┐
                          │             │  ns: guardrails       │               │ presidio       │
                          │             │  :9001 ext_proc (gRPC)│◀──────────────│  analyzer +    │
                          │             │  :8080 health (HTTP)  │  masked text  │  anonymizer    │
                          │             └──────────┬────────────┘               │  ns: guardrail-│
                          │                        │ returns body_mutation      │  providers :80 │
                          │                        │ (masked MCP payload)       └────────────────┘
                          │◀───────────────────────┘
                          │ forwards masked request to upstream
                          ▼
               ┌──────────────────────┐
               │ echo-mcp-server      │  ns: default
               │  FastMCP streamable- │  echo(message) tool returns input verbatim
               │  HTTP on :8000       │
               └──────────────────────┘
```

### Request flow

1. Test script POSTs `initialize` → response header `Mcp-Session-Id: <uuid>`.
2. Test script POSTs `notifications/initialized` with that session id.
3. Test script POSTs `tools/call` for `echo` with `arguments.message = "My email is john@example.com"`.
4. Envoy's ext_proc filter buffers the request body and calls the adapter via gRPC, attaching `metadata_context.filter_metadata["envoy.filters.http.ext_proc"]` populated from the HTTPRoute's xDS route metadata (set via `EnvoyPatchPolicy`).
5. Adapter parses the MCP JSON-RPC envelope, extracts `params.arguments.message`, calls Presidio to mask `EMAIL_ADDRESS`, returns a `body_mutation` replacing the request body.
6. Envoy forwards the masked request to `echo-mcp:8000`.
7. `echo` returns the masked message verbatim (FastMCP `echo(message)` echoes input).
8. Client receives the response; assertions pass when it contains `<EMAIL_ADDRESS>` and not `john@example.com`.

## Components

### New files

```
Tiltfile
deploy/local/
  kustomization.yaml
  namespaces.yaml                    # guardrails, guardrail-providers
  envoy-gateway-values.yaml          # Helm values file (enables EnvoyPatchPolicy)
  echo-mcp.yaml                      # Deployment + Service, default ns
  presidio.yaml                      # Deployment + Service, guardrail-providers ns
  guardrail-adapter.yaml             # Deployment + Service, guardrails ns
  gateway.yaml                       # GatewayClass + Gateway
  httproute.yaml                     # HTTPRoute: /mcp → echo-mcp:8000
  ext-proc-policy.yaml               # EnvoyExtensionPolicy → guardrail-adapter:9001
  patch-policy.yaml                  # EnvoyPatchPolicy injecting route filter_metadata
  reference-grant.yaml               # cross-namespace ext_proc backendRef
test/
  e2e-gateway.sh                     # new MCP-through-gateway test
```

### Unchanged

`compose.yml` and `test/e2e.sh` remain. They cover the adapter-in-isolation gRPC path and are faster to iterate on than the full k8s stack.

### Versions

- **Envoy Gateway:** `v1.7.2` (latest stable as of 2026-04-22).
- **Gateway API CRDs:** bundled with the Envoy Gateway Helm chart (standard channel).
- **Presidio image:** `ghcr.io/agentic-layer/presidio:0.3.1`.
- **echo-mcp-server image:** `ghcr.io/agentic-layer/echo-mcp-server:0.3.0`.
- **guardrail-adapter image:** built locally by Tilt from the repo `Dockerfile`; tag `guardrail-adapter-local:tilt`.

## Tiltfile

```python
# -*- mode: Python -*-
update_settings(max_parallel_updates=5, k8s_upsert_timeout_secs=600)

load('ext://helm_resource', 'helm_resource')

helm_resource(
    'envoy-gateway',
    'oci://docker.io/envoyproxy/gateway-helm',
    namespace='envoy-gateway-system',
    flags=[
        '--version=v1.7.2',
        '--create-namespace',
        '--values=deploy/local/envoy-gateway-values.yaml',
    ],
    labels=['gateway'],
)

docker_build('guardrail-adapter-local', '.', dockerfile='Dockerfile')

k8s_yaml(kustomize('deploy/local'))

k8s_resource('envoy-default-eg',
             port_forwards='10000:80',
             labels=['gateway'],
             resource_deps=['envoy-gateway'])

k8s_resource('echo-mcp', labels=['mcp'])
k8s_resource('presidio', labels=['guardrails'])
k8s_resource('guardrail-adapter', labels=['guardrails'])
```

Notes:

- `envoy-default-eg` is the auto-generated data-plane Deployment/Service name for a `Gateway` named `eg` in namespace `default` (stable convention in Envoy Gateway v1.7.x). Renaming the Gateway requires updating this string.
- `docker_build('guardrail-adapter-local', ...)` does not enable live-update. Dockerfile builds from source each cycle; a future iteration can add live-update (sync + in-container `go build`) once we settle on a layout.

## Manifests — key details

### `envoy-gateway-values.yaml`

```yaml
config:
  envoyGateway:
    extensionApis:
      enableEnvoyPatchPolicy: true
```

### `gateway.yaml`

Standard `GatewayClass` using the `gateway.envoyproxy.io/gatewayclass-controller` controller, and a `Gateway` named `eg` in `default` with a single HTTP listener on port 80 accepting `HTTPRoute` from the same namespace.

### `httproute.yaml`

`HTTPRoute` named `echo-mcp` in the `default` namespace with `parentRefs: [eg]`, path prefix `/mcp`, backendRef `echo-mcp:8000`. The policy manifests below reference this name.

### `ext-proc-policy.yaml`

`EnvoyExtensionPolicy` targeting the HTTPRoute:

```yaml
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: echo-mcp
  extProc:
    - backendRefs:
        - group: ""
          kind: Service
          name: guardrail-adapter
          namespace: guardrails
          port: 9001
      processingMode:
        request:
          body: Buffered
      messageTimeout: 5s
      failOpen: false
      metadataOptions:
        forwardingNamespaces:
          untyped:
            - envoy.filters.http.ext_proc
```

### `patch-policy.yaml`

`EnvoyPatchPolicy` of type `JSONPatch` targeting the Gateway, adding `metadata.filter_metadata["envoy.filters.http.ext_proc"]` to the generated RouteConfiguration entry for the HTTPRoute. Payload matches the existing `e2e.sh` contract:

```yaml
guardrail.provider: "presidio-api"
guardrail.mode: "pre_call"
guardrail.presidio.endpoint: "http://presidio.guardrail-providers:80"
guardrail.presidio.language: "en"
guardrail.presidio.score_thresholds: "{\"ALL\":0.5}"
guardrail.presidio.entity_actions: "{\"EMAIL_ADDRESS\":\"MASK\"}"
```

Concrete starting form (exact `name:` and JSON pointer path are confirmed against the live xDS dump during implementation — see tooling note below):

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyPatchPolicy
metadata:
  name: guardrail-route-metadata
  namespace: default
spec:
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: eg
  type: JSONPatch
  jsonPatches:
    - type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration"
      name: default/eg/http        # verify against xDS dump
      operation:
        op: add
        path: "/virtual_hosts/0/routes/0/metadata"
        value:
          filter_metadata:
            envoy.filters.http.ext_proc:
              guardrail.provider: "presidio-api"
              guardrail.mode: "pre_call"
              guardrail.presidio.endpoint: "http://presidio.guardrail-providers:80"
              guardrail.presidio.language: "en"
              guardrail.presidio.score_thresholds: "{\"ALL\":0.5}"
              guardrail.presidio.entity_actions: "{\"EMAIL_ADDRESS\":\"MASK\"}"
```

**Tooling note for implementation:** use `egctl config envoy-proxy route -n envoy-gateway-system <envoy-pod>` to dump the live RouteConfiguration, then set the patch `name` and `path` to match the actual entry for our HTTPRoute. If the index-based path (`/routes/0`) is fragile, switch to a JSONPatch `test` + `add` pair keyed on the route's `name` field.

### `reference-grant.yaml`

Standard `gateway.networking.k8s.io/v1beta1 ReferenceGrant` in `guardrails` ns, allowing `EnvoyExtensionPolicy` in `default` to reference `Service guardrail-adapter`.

### `guardrail-adapter.yaml`, `presidio.yaml`, `echo-mcp.yaml`

- `guardrail-adapter` Deployment uses `image: guardrail-adapter-local:tilt` with `imagePullPolicy: IfNotPresent`, exposes ports `9001` (gRPC) and `8080` (HTTP health), readiness/liveness against `/health`.
- `presidio` adapted from the reference sample (`ghcr.io/agentic-layer/presidio:0.3.1`).
- `echo-mcp` Deployment runs `ghcr.io/agentic-layer/echo-mcp-server:0.3.0` with no extra env (the default `echo` tool is what the test uses).

## Test script — `test/e2e-gateway.sh`

```bash
#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:10000}"
MCP_ENDPOINT="${GATEWAY_URL}/mcp"
PII_EMAIL="john@example.com"
PII_MESSAGE="My email is ${PII_EMAIL}"
EXPECTED_TOKEN="<EMAIL_ADDRESS>"

# 1. initialize — capture Mcp-Session-Id
INIT_RESP=$(curl -sS -i -X POST "$MCP_ENDPOINT" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
        "protocolVersion":"2024-11-05",
        "capabilities":{},
        "clientInfo":{"name":"e2e-gateway-test","version":"0.1"}}}')

SESSION_ID=$(echo "$INIT_RESP" | awk 'tolower($1)=="mcp-session-id:" {print $2}' | tr -d '\r\n')
[ -n "$SESSION_ID" ] || { echo "no session id"; exit 1; }

# 2. initialized notification
curl -sS -X POST "$MCP_ENDPOINT" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'

# 3. tools/call echo with PII
RESP=$(curl -sS -X POST "$MCP_ENDPOINT" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d "$(jq -cn --arg m "$PII_MESSAGE" \
        '{jsonrpc:"2.0",id:2,method:"tools/call",
          params:{name:"echo",arguments:{message:$m}}}')")

# 4. assertions — handles both direct JSON and SSE (data: ...) framing
BODY=$(echo "$RESP" | sed -n 's/^data: //p' | tr -d '\r')
[ -n "$BODY" ] || BODY="$RESP"

if echo "$BODY" | grep -q "$PII_EMAIL"; then
  echo "FAIL: raw email leaked to client"; echo "$BODY"; exit 1
fi
if ! echo "$BODY" | grep -q "$EXPECTED_TOKEN"; then
  echo "FAIL: expected $EXPECTED_TOKEN in response"; echo "$BODY"; exit 1
fi
echo "PASS: echo returned masked message"
```

Requires only `curl` and `jq` (already a dep of `e2e.sh`).

## Acceptance

- `tilt up` brings all resources to green within ~2 minutes on a warm cache.
- `./test/e2e-gateway.sh` exits 0 against a running stack.
- Response body contains `<EMAIL_ADDRESS>` and does not contain `john@example.com`.

## Caveats

- **EnvoyPatchPolicy is experimental** in Envoy Gateway v1.7.2 and explicitly documented as unstable across versions. Upstream also notes it may enable a "complete security compromise" since arbitrary xDS can be injected — acceptable for local test, call out loudly in any production reuse.
- **Version pin matters.** The Tiltfile pins `v1.7.2`; patch policy payloads may need adjustment on minor/major bumps.
- **Cluster precondition.** The developer must already have a working local cluster. README update will mention a minimal kind config that exposes the required ports (optional; port-forward works without it).
- **Envoy Gateway auto-naming.** `envoy-default-eg` is stable for `Gateway default/eg` in v1.7.x. A Gateway rename requires the Tiltfile to be updated.

## Follow-up work (not in this PR)

1. **Header-fallback config in adapter.** Accept guardrail settings via request headers (e.g., `x-guardrail-provider`, `x-guardrail-presidio-*`, or a single JSON-encoded `x-guardrail-config`) as a fallback when `metadata_context` is absent. Keeps metadata as the primary contract; adds a Gateway-API-native path.
2. **Dedicated header-path E2E test.** Second manifest bundle and script that uses `HTTPRoute.filters.requestHeaderModifier` to inject config per route — no `EnvoyPatchPolicy`, no experimental-API dependency.
3. **post_call masking flow.** Extend `EnvoyExtensionPolicy.extProc.processingMode.response.body: Buffered`; add response-side assertions to a variant of `e2e-gateway.sh`.
4. **Docker-build live-update.** Sync `internal/` and `cmd/` into the adapter container and rebuild in-place, instead of rebuilding the full image each cycle.
