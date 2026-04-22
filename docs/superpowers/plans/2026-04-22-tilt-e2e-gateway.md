# Tilt-based E2E Test with Envoy Gateway — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a local Kubernetes-based E2E test that drives the guardrail-adapter through a real Envoy Gateway, mask-ing PII in an MCP `tools/call` request via ext_proc + route-level `filter_metadata`.

**Architecture:** Tilt orchestrates Envoy Gateway v1.7.2 (Helm chart, which bundles Gateway API CRDs) plus plain-K8s Deployments for Presidio, echo-mcp, and the locally-built guardrail-adapter image. An `EnvoyExtensionPolicy` wires the adapter as an ext_proc filter on an `HTTPRoute` that points at echo-mcp; an `EnvoyPatchPolicy` injects `filter_metadata["envoy.filters.http.ext_proc"]` onto the generated route so the adapter receives its Presidio configuration. A bash test script drives the flow end-to-end through the Gateway's port-forward.

**Tech Stack:** Tilt, Helm, kustomize, Envoy Gateway v1.7.2, Gateway API v1 (bundled with EG chart), Docker, kubectl, curl, jq.

**Spec:** `docs/superpowers/specs/2026-04-22-tilt-e2e-gateway-design.md`

**Preconditions (not part of any task):**
- A working local Kubernetes cluster is reachable through `kubectl` (kind, Docker Desktop k8s, Colima, etc.). The plan does not create the cluster.
- `tilt`, `kubectl`, `helm`, `docker`, `curl`, and `jq` are on PATH.

---

## File Structure

New:

```
Tiltfile                                           # Tilt entrypoint
deploy/local/
  kustomization.yaml                               # aggregates resources below
  namespaces.yaml                                  # guardrails, guardrail-providers
  envoy-gateway-values.yaml                        # Helm values (enables EnvoyPatchPolicy)
  presidio.yaml                                    # Deployment + Service
  echo-mcp.yaml                                    # Deployment + Service
  guardrail-adapter.yaml                           # Deployment + Service
  gateway.yaml                                     # GatewayClass + Gateway
  httproute.yaml                                   # HTTPRoute (/mcp → echo-mcp)
  reference-grant.yaml                             # default → guardrails Service
  ext-proc-policy.yaml                             # EnvoyExtensionPolicy
  patch-policy.yaml                                # EnvoyPatchPolicy (route metadata)
test/
  e2e-gateway.sh                                   # MCP-through-gateway test
docs/superpowers/plans/
  2026-04-22-tilt-e2e-gateway.md                   # this file
```

Modified:

```
README.md                                          # add "E2E via Envoy Gateway (Tilt)" section
```

Each file has one responsibility; policies are split so the generated Envoy config can be enabled incrementally (Tasks 6 → 7 → 8).

---

## Task 1: Scaffold Tiltfile and kustomize skeleton

**Files:**
- Create: `Tiltfile`
- Create: `deploy/local/kustomization.yaml`
- Create: `deploy/local/namespaces.yaml`

- [ ] **Step 1: Create `deploy/local/namespaces.yaml`**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: guardrails
---
apiVersion: v1
kind: Namespace
metadata:
  name: guardrail-providers
```

- [ ] **Step 2: Create `deploy/local/kustomization.yaml`**

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - namespaces.yaml
```

Later tasks append to the `resources` list; only `namespaces.yaml` is referenced here so this step remains applyable.

- [ ] **Step 3: Create `Tiltfile`**

```python
# -*- mode: Python -*-
update_settings(max_parallel_updates=5, k8s_upsert_timeout_secs=600)

k8s_yaml(kustomize('deploy/local'))
```

- [ ] **Step 4: Verify Tilt parses and applies**

Run: `tilt up --legacy=false` in a separate terminal (or `tilt ci` for a non-interactive one-shot). Then:

```bash
kubectl get ns guardrails guardrail-providers
```

Expected output: both namespaces listed with status `Active`.

- [ ] **Step 5: Commit**

```bash
git add Tiltfile deploy/local/kustomization.yaml deploy/local/namespaces.yaml
git commit -m "feat(e2e): scaffold Tiltfile and kustomize skeleton"
```

---

## Task 2: Install Envoy Gateway via Tilt

**Files:**
- Create: `deploy/local/envoy-gateway-values.yaml`
- Modify: `Tiltfile`

- [ ] **Step 1: Create `deploy/local/envoy-gateway-values.yaml`**

```yaml
config:
  envoyGateway:
    extensionApis:
      enableEnvoyPatchPolicy: true
```

- [ ] **Step 2: Add helm_resource block to `Tiltfile`**

Insert at the top (after `update_settings`):

```python
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
```

- [ ] **Step 3: Verify Envoy Gateway is installed**

After `tilt up` reconciles, run:

```bash
kubectl -n envoy-gateway-system get deploy envoy-gateway
kubectl get crd gateways.gateway.networking.k8s.io
kubectl get crd envoypatchpolicies.gateway.envoyproxy.io
```

Expected: all three resources exist and the deployment is `Available`.

- [ ] **Step 4: Verify EnvoyPatchPolicy flag is enabled**

```bash
kubectl -n envoy-gateway-system get configmap envoy-gateway-config -o yaml | grep -A2 extensionApis
```

Expected: `enableEnvoyPatchPolicy: true` is present.

- [ ] **Step 5: Commit**

```bash
git add deploy/local/envoy-gateway-values.yaml Tiltfile
git commit -m "feat(e2e): install Envoy Gateway v1.7.2 via Tilt"
```

---

## Task 3: Deploy Presidio

**Files:**
- Create: `deploy/local/presidio.yaml`
- Modify: `deploy/local/kustomization.yaml`

- [ ] **Step 1: Create `deploy/local/presidio.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: presidio
  namespace: guardrail-providers
  labels:
    app: presidio
spec:
  replicas: 1
  selector:
    matchLabels:
      app: presidio
  template:
    metadata:
      labels:
        app: presidio
    spec:
      automountServiceAccountToken: false
      containers:
        - name: presidio
          image: ghcr.io/agentic-layer/presidio:0.3.1
          ports:
            - containerPort: 8000
          readinessProbe:
            httpGet:
              path: /health
              port: 8000
            initialDelaySeconds: 30
            periodSeconds: 10
            failureThreshold: 12
          livenessProbe:
            httpGet:
              path: /health
              port: 8000
            initialDelaySeconds: 60
            periodSeconds: 15
            failureThreshold: 3
          resources:
            requests:
              cpu: 200m
              memory: 512Mi
            limits:
              cpu: "1"
              memory: 2Gi
---
apiVersion: v1
kind: Service
metadata:
  name: presidio
  namespace: guardrail-providers
spec:
  selector:
    app: presidio
  ports:
    - port: 80
      targetPort: 8000
```

- [ ] **Step 2: Append to `deploy/local/kustomization.yaml`**

The file should now read:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - namespaces.yaml
  - presidio.yaml
```

- [ ] **Step 3: Verify Presidio is reachable**

After Tilt reconciles:

```bash
kubectl -n guardrail-providers rollout status deploy/presidio --timeout=180s
kubectl -n guardrail-providers run curl-presidio --rm -i --restart=Never \
  --image=curlimages/curl:8.8.0 -- \
  curl -sS -o /dev/null -w '%{http_code}\n' http://presidio/health
```

Expected: `200`.

- [ ] **Step 4: Commit**

```bash
git add deploy/local/presidio.yaml deploy/local/kustomization.yaml
git commit -m "feat(e2e): deploy Presidio analyzer+anonymizer"
```

---

## Task 4: Deploy echo-mcp

**Files:**
- Create: `deploy/local/echo-mcp.yaml`
- Modify: `deploy/local/kustomization.yaml`

- [ ] **Step 1: Create `deploy/local/echo-mcp.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo-mcp
  namespace: default
  labels:
    app: echo-mcp
spec:
  replicas: 1
  selector:
    matchLabels:
      app: echo-mcp
  template:
    metadata:
      labels:
        app: echo-mcp
    spec:
      automountServiceAccountToken: false
      containers:
        - name: echo-mcp
          image: ghcr.io/agentic-layer/echo-mcp-server:0.3.0
          ports:
            - containerPort: 8000
          resources:
            requests:
              cpu: 50m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 512Mi
---
apiVersion: v1
kind: Service
metadata:
  name: echo-mcp
  namespace: default
spec:
  selector:
    app: echo-mcp
  ports:
    - port: 8000
      targetPort: 8000
```

- [ ] **Step 2: Append `- echo-mcp.yaml` to `deploy/local/kustomization.yaml` resources**

- [ ] **Step 3: Verify echo-mcp responds to an MCP initialize**

```bash
kubectl -n default rollout status deploy/echo-mcp --timeout=120s
kubectl -n default port-forward svc/echo-mcp 18000:8000 >/tmp/pf-echo.log 2>&1 &
PF_PID=$!
sleep 2
curl -sS -i -X POST http://localhost:18000/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"check","version":"0.1"}}}' \
  | head -20
kill $PF_PID
```

Expected: response contains an `Mcp-Session-Id:` header and a JSON-RPC / SSE body announcing the server.

- [ ] **Step 4: Commit**

```bash
git add deploy/local/echo-mcp.yaml deploy/local/kustomization.yaml
git commit -m "feat(e2e): deploy echo-mcp-server"
```

---

## Task 5: Deploy the guardrail-adapter with a locally-built image

**Files:**
- Create: `deploy/local/guardrail-adapter.yaml`
- Modify: `deploy/local/kustomization.yaml`
- Modify: `Tiltfile`

- [ ] **Step 1: Create `deploy/local/guardrail-adapter.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: guardrail-adapter
  namespace: guardrails
  labels:
    app: guardrail-adapter
spec:
  replicas: 1
  selector:
    matchLabels:
      app: guardrail-adapter
  template:
    metadata:
      labels:
        app: guardrail-adapter
    spec:
      automountServiceAccountToken: false
      containers:
        - name: guardrail-adapter
          image: guardrail-adapter-local
          imagePullPolicy: IfNotPresent
          args:
            - --addr=:9001
            - --health-addr=:8080
          ports:
            - name: ext-proc
              containerPort: 9001
            - name: health
              containerPort: 8080
          env:
            - name: LOG_LEVEL
              value: info
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 3
            periodSeconds: 5
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 15
            periodSeconds: 10
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 512Mi
---
apiVersion: v1
kind: Service
metadata:
  name: guardrail-adapter
  namespace: guardrails
spec:
  selector:
    app: guardrail-adapter
  ports:
    - name: ext-proc
      port: 9001
      targetPort: 9001
      appProtocol: kubernetes.io/h2c
```

Notes:
- The image tag is left off — `docker_build` in Tilt will assign it automatically and rewrite the manifest's image ref to the freshly-built image.
- `appProtocol: kubernetes.io/h2c` tells Envoy Gateway to talk gRPC (HTTP/2 cleartext) to the adapter.

- [ ] **Step 2: Append `- guardrail-adapter.yaml` to `deploy/local/kustomization.yaml`**

- [ ] **Step 3: Add `docker_build` to `Tiltfile`**

Insert above the `k8s_yaml(kustomize(...))` line:

```python
docker_build('guardrail-adapter-local', '.', dockerfile='Dockerfile')
```

- [ ] **Step 4: Verify the adapter's health endpoint**

```bash
kubectl -n guardrails rollout status deploy/guardrail-adapter --timeout=120s
kubectl -n guardrails port-forward svc/guardrail-adapter 18080:8080 >/tmp/pf-adapter.log 2>&1 &
PF_PID=$!
sleep 2
curl -sS http://localhost:18080/health
kill $PF_PID
```

Expected: `OK` (or whatever the existing `/health` handler returns).

- [ ] **Step 5: Commit**

```bash
git add deploy/local/guardrail-adapter.yaml deploy/local/kustomization.yaml Tiltfile
git commit -m "feat(e2e): deploy guardrail-adapter with Tilt-built image"
```

---

## Task 6: Gateway, HTTPRoute, and gateway port-forward

**Files:**
- Create: `deploy/local/gateway.yaml`
- Create: `deploy/local/httproute.yaml`
- Modify: `deploy/local/kustomization.yaml`
- Modify: `Tiltfile`

- [ ] **Step 1: Create `deploy/local/gateway.yaml`**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: eg
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: eg
  namespace: default
spec:
  gatewayClassName: eg
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: Same
```

- [ ] **Step 2: Create `deploy/local/httproute.yaml`**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: echo-mcp
  namespace: default
spec:
  parentRefs:
    - name: eg
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /mcp
      backendRefs:
        - name: echo-mcp
          port: 8000
```

- [ ] **Step 3: Append both files to `deploy/local/kustomization.yaml` resources**

Order: `gateway.yaml` before `httproute.yaml`.

- [ ] **Step 4: Add port-forward for the auto-created data-plane service to `Tiltfile`**

Append at the bottom of `Tiltfile`:

```python
k8s_resource('envoy-default-eg',
             port_forwards='10000:80',
             labels=['gateway'],
             resource_deps=['envoy-gateway'])

k8s_resource('echo-mcp', labels=['mcp'])
k8s_resource('presidio', labels=['guardrails'])
k8s_resource('guardrail-adapter', labels=['guardrails'])
```

- [ ] **Step 5: Verify gateway routes `/mcp` to echo-mcp (no ext_proc yet)**

```bash
kubectl -n envoy-gateway-system get deploy envoy-default-eg
kubectl -n default get httproute echo-mcp -o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}'
```

Expected: `True` for the `Accepted` condition.

Then drive a request through the gateway (Tilt is port-forwarding `10000:80`):

```bash
curl -sS -i -X POST http://localhost:10000/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"check","version":"0.1"}}}' \
  | head -20
```

Expected: same response shape as in Task 4 (status 200 with `Mcp-Session-Id`), but now routed through Envoy.

- [ ] **Step 6: Commit**

```bash
git add deploy/local/gateway.yaml deploy/local/httproute.yaml deploy/local/kustomization.yaml Tiltfile
git commit -m "feat(e2e): add Gateway + HTTPRoute for /mcp → echo-mcp"
```

---

## Task 7: Wire ext_proc via EnvoyExtensionPolicy + ReferenceGrant

**Files:**
- Create: `deploy/local/reference-grant.yaml`
- Create: `deploy/local/ext-proc-policy.yaml`
- Modify: `deploy/local/kustomization.yaml`

- [ ] **Step 1: Create `deploy/local/reference-grant.yaml`**

```yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: allow-default-to-guardrail-adapter
  namespace: guardrails
spec:
  from:
    - group: gateway.envoyproxy.io
      kind: EnvoyExtensionPolicy
      namespace: default
  to:
    - group: ""
      kind: Service
      name: guardrail-adapter
```

- [ ] **Step 2: Create `deploy/local/ext-proc-policy.yaml`**

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyExtensionPolicy
metadata:
  name: guardrail-extproc
  namespace: default
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

- [ ] **Step 3: Append both files to `deploy/local/kustomization.yaml` resources**

Order: `reference-grant.yaml` before `ext-proc-policy.yaml`.

- [ ] **Step 4: Verify the policy is Accepted and ext_proc is exercised**

```bash
kubectl -n default get envoyextensionpolicy guardrail-extproc \
  -o jsonpath='{.status.ancestors[0].conditions[?(@.type=="Accepted")].status}'
```

Expected: `True`.

Then drive a `tools/call` through the gateway and check adapter logs:

```bash
kubectl -n guardrails logs deploy/guardrail-adapter --tail=0 -f >/tmp/adapter.log 2>&1 &
LOG_PID=$!

# re-run init + call
INIT=$(curl -sS -i -X POST http://localhost:10000/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c","version":"0.1"}}}')
SID=$(echo "$INIT" | awk 'tolower($1)=="mcp-session-id:" {print $2}' | tr -d '\r\n')
curl -sS -X POST http://localhost:10000/mcp \
  -H "Mcp-Session-Id: $SID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
curl -sS -X POST http://localhost:10000/mcp \
  -H "Mcp-Session-Id: $SID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello"}}}'

sleep 1; kill $LOG_PID
grep -i 'process\|ext_proc\|received' /tmp/adapter.log
```

Expected: adapter logs show incoming ext_proc calls (the adapter is currently pass-through when no metadata is attached, so the response still contains `"hello"` verbatim — that's fine at this stage).

- [ ] **Step 5: Commit**

```bash
git add deploy/local/reference-grant.yaml deploy/local/ext-proc-policy.yaml deploy/local/kustomization.yaml
git commit -m "feat(e2e): wire guardrail-adapter as ext_proc on echo-mcp route"
```

---

## Task 8: Inject guardrail configuration via EnvoyPatchPolicy

**Files:**
- Create: `deploy/local/patch-policy.yaml`
- Modify: `deploy/local/kustomization.yaml`

- [ ] **Step 1: Discover the generated RouteConfiguration name**

Run:

```bash
EG_POD=$(kubectl -n envoy-gateway-system get pod -o name | grep '^pod/envoy-default-eg-' | head -1 | cut -d/ -f2)
kubectl -n envoy-gateway-system exec "$EG_POD" -c envoy -- \
  curl -sS http://localhost:19000/config_dump?include_eds | \
  jq '.configs[] | select(."@type" | test("RoutesConfigDump")) | .dynamic_route_configs[] | .route_config.name'
```

Expected output like: `"default/eg/http"`.

Record this value; Step 2 uses it as `name:`. Also note the full route name (next to `/virtual_hosts/0/routes/0/name`) — Step 4 uses it if the index-based path fails:

```bash
kubectl -n envoy-gateway-system exec "$EG_POD" -c envoy -- \
  curl -sS http://localhost:19000/config_dump?include_eds | \
  jq '.configs[] | select(."@type" | test("RoutesConfigDump")) | .dynamic_route_configs[] | .route_config.virtual_hosts[0].routes[0].name'
```

- [ ] **Step 2: Create `deploy/local/patch-policy.yaml`**

Use the `name` from Step 1 (expected: `default/eg/http`):

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
      name: default/eg/http
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

- [ ] **Step 3: Append `- patch-policy.yaml` to `deploy/local/kustomization.yaml` resources**

- [ ] **Step 4: Verify the patch is Programmed**

```bash
kubectl -n default get envoypatchpolicy guardrail-route-metadata \
  -o jsonpath='{.status.ancestors[0].conditions[?(@.type=="Programmed")].status}'
```

Expected: `True`.

If status is `False`, re-run the `config_dump` inspection from Step 1 and adjust the JSONPatch `path` — envoy-gateway may have emitted more than one virtual host or route, in which case `/virtual_hosts/0/routes/0/metadata` is wrong. Use a `test`-then-`add` pair keyed on the route's `name` field as a more robust alternative, for example:

```yaml
jsonPatches:
  - type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration"
    name: default/eg/http
    operation:
      op: test
      path: "/virtual_hosts/0/routes/0/name"
      value: default/eg/http/echo-mcp/rule/0/match/0/*
  - type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration"
    name: default/eg/http
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

Look up the actual route `name:` value in the `config_dump` output if needed.

- [ ] **Step 5: Verify filter_metadata is visible in the dumped config**

```bash
kubectl -n envoy-gateway-system exec "$EG_POD" -c envoy -- \
  curl -sS http://localhost:19000/config_dump?include_eds | \
  jq '.. | .metadata? // empty | select(.filter_metadata."envoy.filters.http.ext_proc")'
```

Expected: the injected `guardrail.*` keys are printed.

- [ ] **Step 6: Commit**

```bash
git add deploy/local/patch-policy.yaml deploy/local/kustomization.yaml
git commit -m "feat(e2e): inject guardrail metadata onto route via EnvoyPatchPolicy"
```

---

## Task 9: End-to-end test script

**Files:**
- Create: `test/e2e-gateway.sh`

- [ ] **Step 1: Create `test/e2e-gateway.sh`**

```bash
#!/bin/bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'
log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

GATEWAY_URL="${GATEWAY_URL:-http://localhost:10000}"
MCP_ENDPOINT="${GATEWAY_URL}/mcp"
PII_EMAIL="john@example.com"
PII_MESSAGE="My email is ${PII_EMAIL}"
EXPECTED_TOKEN="<EMAIL_ADDRESS>"

command -v curl >/dev/null || { log_error "curl is required"; exit 1; }
command -v jq >/dev/null || { log_error "jq is required"; exit 1; }

log_info "Test: MCP initialize through Envoy Gateway"
INIT_RESP=$(curl -sS -i -X POST "$MCP_ENDPOINT" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
        "protocolVersion":"2024-11-05",
        "capabilities":{},
        "clientInfo":{"name":"e2e-gateway-test","version":"0.1"}}}')

SESSION_ID=$(echo "$INIT_RESP" | awk 'tolower($1)=="mcp-session-id:" {print $2}' | tr -d '\r\n')
if [ -z "$SESSION_ID" ]; then
    log_error "✗ No Mcp-Session-Id in initialize response"
    log_error "$INIT_RESP"
    exit 1
fi
log_info "✓ Session established: $SESSION_ID"

log_info "Test: notifications/initialized"
curl -sS -X POST "$MCP_ENDPOINT" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null

log_info "Test: tools/call echo with PII payload"
REQ_BODY=$(jq -cn --arg m "$PII_MESSAGE" \
  '{jsonrpc:"2.0",id:2,method:"tools/call",
    params:{name:"echo",arguments:{message:$m}}}')

RESP=$(curl -sS -X POST "$MCP_ENDPOINT" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d "$REQ_BODY")

BODY=$(echo "$RESP" | sed -n 's/^data: //p' | tr -d '\r')
[ -n "$BODY" ] || BODY="$RESP"

if echo "$BODY" | grep -q "$PII_EMAIL"; then
    log_error "✗ Raw email leaked in response"
    log_error "Response: $BODY"
    exit 1
fi

if ! echo "$BODY" | grep -q "$EXPECTED_TOKEN"; then
    log_error "✗ Response is missing the masked token $EXPECTED_TOKEN"
    log_error "Response: $BODY"
    exit 1
fi

log_info "✓ Response contains $EXPECTED_TOKEN and no raw PII"
log_info "All tests passed!"
```

- [ ] **Step 2: Make script executable**

```bash
chmod +x test/e2e-gateway.sh
```

- [ ] **Step 3: Run it end-to-end**

Ensure `tilt up` is running and all resources are green. Then:

```bash
./test/e2e-gateway.sh
```

Expected output:

```
[INFO] Test: MCP initialize through Envoy Gateway
[INFO] ✓ Session established: <uuid>
[INFO] Test: notifications/initialized
[INFO] Test: tools/call echo with PII payload
[INFO] ✓ Response contains <EMAIL_ADDRESS> and no raw PII
[INFO] All tests passed!
```

Exit code: `0`.

- [ ] **Step 4: If it fails, inspect adapter logs**

```bash
kubectl -n guardrails logs deploy/guardrail-adapter --tail=100
```

Common failures and remedies:
- "presidio connection refused" → Presidio pod still initializing. Re-run after `kubectl -n guardrail-providers rollout status deploy/presidio`.
- No masking visible in response → verify Task 8, Step 5 output; the patch may not have landed.
- HTTP 503 from gateway → adapter not ready; check `deploy/local/guardrail-adapter.yaml` readiness probe.

- [ ] **Step 5: Commit**

```bash
git add test/e2e-gateway.sh
git commit -m "test(e2e): add gateway-level MCP PII masking test"
```

---

## Task 10: Document the flow in README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add a new section after the existing E2E docs**

Append under `### End-to-End Testing`:

~~~markdown
#### E2E via Envoy Gateway (Tilt)

An additional end-to-end flow exercises the adapter through a real Envoy
Gateway in Kubernetes, driven by Tilt. Prerequisites:

- A local Kubernetes cluster (kind, Docker Desktop k8s, Colima, …) set as
  your current `kubectl` context.
- `tilt`, `kubectl`, `helm`, `docker`, `curl`, and `jq` on PATH.

Bring the stack up:

```bash
tilt up
```

Wait for all resources to go green in the Tilt UI (or use `tilt ci` for a
one-shot apply). Then run:

```bash
./test/e2e-gateway.sh
```

The script sends an MCP `tools/call` through the gateway to `echo-mcp`
with PII in the tool arguments and asserts that the response has been
masked via Presidio by the guardrail-adapter.

To tear down:

```bash
tilt down
```
~~~

- [ ] **Step 2: Verify the change renders**

Quick sanity check:

```bash
grep -A5 "E2E via Envoy Gateway" README.md
```

Expected: the new section is present.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document Tilt-based E2E flow with Envoy Gateway"
```

---

## Acceptance

- `tilt up` reconciles all resources green on a clean local cluster.
- `./test/e2e-gateway.sh` exits `0` and prints `All tests passed!`.
- Response body contains `<EMAIL_ADDRESS>` and does not contain the raw `john@example.com`.
- `tilt down` cleans up all created namespaces (except pre-existing ones).

## Follow-up work (tracked in the spec, not in this plan)

1. Add header-fallback configuration to the adapter and a dedicated header-path E2E test (no `EnvoyPatchPolicy`).
2. Extend the flow to post_call masking and add response-side assertions.
3. Introduce Tilt live-update for the adapter (`sync` + in-container `go build`).
