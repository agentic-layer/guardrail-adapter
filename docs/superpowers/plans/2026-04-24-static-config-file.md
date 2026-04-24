# Static Config File Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a YAML-file-based configuration path (enabled via `GUARDRAIL_CONFIG_FILE`) that bypasses the experimental `EnvoyPatchPolicy` workaround, loads once at startup, and fully replaces the dynamic metadata/header path when set.

**Architecture:** Three layers, built bottom-up. (1) A YAML loader in `internal/metadata` that produces the existing `*GuardrailConfig`. (2) A constructor-injected static config on `*Server` with a short-circuit in `handleRequestHeaders`. (3) Main wires the env var and fails fast. E2E coverage adds a static-mode variant for both Compose and Tilt/Envoy-Gateway paths.

**Tech Stack:** Go 1.24, `sigs.k8s.io/yaml`, Envoy ext_proc v3, Docker Compose, Tilt + Envoy Gateway, kustomize.

---

## File Structure

| Path | Change |
|---|---|
| `go.mod` / `go.sum` | Add `sigs.k8s.io/yaml` dep |
| `internal/metadata/metadata.go` | Add YAML loader + validation |
| `internal/metadata/metadata_test.go` | Add loader tests |
| `internal/extproc/server.go` | Accept static config in `NewServer`; short-circuit in `handleRequestHeaders` |
| `internal/extproc/server_test.go` | Update existing `NewServer()` calls to `NewServer(nil)`; add static-config tests |
| `cmd/adapter/main.go` | Read env var, load file, inject into `NewServer` |
| `test/static-config.yaml` | New sample config for Compose e2e |
| `test/e2e-static.sh` | New compose e2e script (no `metadata_context`) |
| `compose.static.yml` | New standalone compose file with bind-mount + env |
| `deploy/local-static/` | New kustomize overlay (configMapGenerator + adapter patch + delete patch-policy) |
| `test/e2e-gateway-static.sh` | New gateway e2e script |
| `Tiltfile` | Switchable overlay via env var or separate target |
| `.github/workflows/e2e.yml` | Two new jobs (compose-e2e-static, gateway-e2e-static) |
| `README.md` | Document static config mode |

---

## Task 1: (Merged into Task 2)

Originally this task added `sigs.k8s.io/yaml` via `go get; go mod tidy`. In
practice `go mod tidy` removes any module not imported by source code, so
adding the dep separately from its first use is a no-op. The dependency is
now added as part of Task 2, where the first import lives.

Skip this task and proceed to Task 2.

---

## Task 2: YAML loader — happy path (TDD)

**Files:**
- Modify: `internal/metadata/metadata.go`
- Modify: `internal/metadata/metadata_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/metadata/metadata_test.go`:

```go
func TestLoadGuardrailConfigFile_Presidio(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	yamlText := `provider: presidio-api
modes:
  - pre_call
  - post_call
presidio:
  endpoint: http://presidio:8000
  language: en
  score_thresholds:
    EMAIL_ADDRESS: 0.5
  entity_actions:
    EMAIL_ADDRESS: MASK
`
	if err := os.WriteFile(path, []byte(yamlText), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadGuardrailConfigFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	want := &GuardrailConfig{
		Provider: "presidio-api",
		Modes:    []Mode{ModePreCall, ModePostCall},
		Presidio: &PresidioConfig{
			Endpoint:        "http://presidio:8000",
			Language:        "en",
			ScoreThresholds: map[string]float64{"EMAIL_ADDRESS": 0.5},
			EntityActions:   map[string]string{"EMAIL_ADDRESS": "MASK"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
```

Also add imports at the top of `metadata_test.go` if missing: `"os"`, `"path/filepath"`, `"reflect"`.

- [ ] **Step 2: Run test — confirm it fails**

Run: `go test ./internal/metadata/... -run TestLoadGuardrailConfigFile_Presidio -v`
Expected: FAIL with `undefined: LoadGuardrailConfigFile`.

- [ ] **Step 3: Implement the loader**

Append to `internal/metadata/metadata.go`:

```go
import (
	// ... existing imports ...
	"os"

	"sigs.k8s.io/yaml"
)

// guardrailConfigYAML is the on-disk representation, decoded via sigs.k8s.io/yaml
// (which converts YAML -> JSON internally, so tags must be `json`).
type guardrailConfigYAML struct {
	Provider string              `json:"provider"`
	Modes    []string            `json:"modes"`
	Presidio *presidioConfigYAML `json:"presidio,omitempty"`
}

type presidioConfigYAML struct {
	Endpoint        string             `json:"endpoint"`
	Language        string             `json:"language,omitempty"`
	ScoreThresholds map[string]float64 `json:"score_thresholds,omitempty"`
	EntityActions   map[string]string  `json:"entity_actions,omitempty"`
}

// LoadGuardrailConfigFile reads and decodes a YAML config file, then validates it.
// Returns a *GuardrailConfig ready for the ext_proc server. Unknown fields cause
// an error so typos surface immediately.
func LoadGuardrailConfigFile(path string) (*GuardrailConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var raw guardrailConfigYAML
	if err := yaml.UnmarshalStrict(data, &raw); err != nil {
		return nil, fmt.Errorf("decode config yaml: %w", err)
	}

	cfg := &GuardrailConfig{
		Provider: raw.Provider,
		Modes:    make([]Mode, 0, len(raw.Modes)),
	}
	for _, m := range raw.Modes {
		cfg.Modes = append(cfg.Modes, Mode(m))
	}
	if raw.Presidio != nil {
		cfg.Presidio = &PresidioConfig{
			Endpoint:        raw.Presidio.Endpoint,
			Language:        raw.Presidio.Language,
			ScoreThresholds: raw.Presidio.ScoreThresholds,
			EntityActions:   raw.Presidio.EntityActions,
		}
	}
	return cfg, nil
}
```

Note: no validation yet — Task 3 adds it via failing tests.

- [ ] **Step 4: Add the `sigs.k8s.io/yaml` dependency**

Run: `go mod tidy`
Expected: `go.mod` gains `sigs.k8s.io/yaml` as a direct dependency (the
import added in Step 3 triggers the add); `go.sum` is updated.

Sandbox note: `go mod tidy` fetches modules, and on this machine the
Go toolchain's TLS path through the sandbox may fail with
`x509: OSStatus -26276`. If that happens, rerun outside the sandbox
(`dangerouslyDisableSandbox: true`) just for this command. All other
steps stay in-sandbox.

- [ ] **Step 5: Run test — confirm it passes**

Run: `go test ./internal/metadata/... -run TestLoadGuardrailConfigFile_Presidio -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/metadata/metadata.go internal/metadata/metadata_test.go
git commit -m "feat(metadata): add YAML config file loader"
```

---

## Task 3: YAML loader — validation (TDD)

**Files:**
- Modify: `internal/metadata/metadata.go`
- Modify: `internal/metadata/metadata_test.go`

- [ ] **Step 1: Write failing validation tests**

Append to `internal/metadata/metadata_test.go`:

```go
func TestLoadGuardrailConfigFile_ValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name:    "missing provider",
			yaml:    "modes: [pre_call]\n",
			wantSub: "provider is required",
		},
		{
			name:    "empty modes",
			yaml:    "provider: presidio-api\nmodes: []\npresidio:\n  endpoint: http://p:8000\n",
			wantSub: "modes is required",
		},
		{
			name: "unknown mode",
			yaml: "provider: presidio-api\nmodes: [mid_call]\npresidio:\n  endpoint: http://p:8000\n",
			wantSub: "unknown mode",
		},
		{
			name:    "presidio missing endpoint",
			yaml:    "provider: presidio-api\nmodes: [pre_call]\npresidio: {}\n",
			wantSub: "presidio.endpoint is required",
		},
		{
			name:    "presidio-api without presidio block",
			yaml:    "provider: presidio-api\nmodes: [pre_call]\n",
			wantSub: "presidio.endpoint is required",
		},
		{
			name:    "unknown top-level key",
			yaml:    "provider: presidio-api\nmodes: [pre_call]\nbogus: true\npresidio:\n  endpoint: http://p:8000\n",
			wantSub: "decode config yaml",
		},
		{
			name:    "modes wrong type",
			yaml:    "provider: presidio-api\nmodes: pre_call\npresidio:\n  endpoint: http://p:8000\n",
			wantSub: "decode config yaml",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := LoadGuardrailConfigFile(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestLoadGuardrailConfigFile_FileMissing(t *testing.T) {
	_, err := LoadGuardrailConfigFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "read config file") {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run tests — confirm they fail**

Run: `go test ./internal/metadata/... -run 'TestLoadGuardrailConfigFile_(ValidationErrors|FileMissing)' -v`
Expected: The decode-level cases may pass (unknown-key / wrong-type already caught by `UnmarshalStrict`), but the semantic cases (missing provider, empty modes, unknown mode, missing presidio.endpoint) must FAIL because no validation exists yet.

- [ ] **Step 3: Add validation**

Edit `internal/metadata/metadata.go`. Replace the end of `LoadGuardrailConfigFile` (just before `return cfg, nil`) with:

```go
	if err := validateGuardrailConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func validateGuardrailConfig(cfg *GuardrailConfig) error {
	if cfg.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if len(cfg.Modes) == 0 {
		return fmt.Errorf("modes is required and must be non-empty")
	}
	for _, m := range cfg.Modes {
		switch m {
		case ModePreCall, ModePostCall:
		default:
			return fmt.Errorf("unknown mode %q (allowed: pre_call, post_call)", m)
		}
	}
	if cfg.Provider == "presidio-api" {
		if cfg.Presidio == nil || cfg.Presidio.Endpoint == "" {
			return fmt.Errorf("presidio.endpoint is required when provider is presidio-api")
		}
	}
	return nil
}
```

- [ ] **Step 4: Run all metadata tests — confirm pass**

Run: `go test ./internal/metadata/... -v`
Expected: all tests PASS, including the existing flat-map `ParseGuardrailConfig` tests and the new loader tests.

- [ ] **Step 5: Commit**

```bash
git add internal/metadata/metadata.go internal/metadata/metadata_test.go
git commit -m "feat(metadata): validate loaded YAML config"
```

---

## Task 4: Inject static config into `*Server`

**Files:**
- Modify: `internal/extproc/server.go`
- Modify: `internal/extproc/server_test.go`
- Modify: `cmd/adapter/main.go`

- [ ] **Step 1: Write failing test — static config wins over MetadataContext**

Append to `internal/extproc/server_test.go`:

```go
func TestStaticConfigShortCircuitsMetadata(t *testing.T) {
	staticCfg := &metadata.GuardrailConfig{
		Provider: "presidio-api",
		Modes:    []metadata.Mode{metadata.ModePreCall},
		Presidio: &metadata.PresidioConfig{Endpoint: "http://static:8000"},
	}
	server := NewServer(staticCfg)

	// A MetadataContext that *would* configure a different provider if consulted.
	md, err := structpb.NewStruct(map[string]interface{}{
		"guardrail.provider":            "some-other-provider",
		"guardrail.mode":                "post_call",
		"guardrail.presidio.endpoint":   "http://dynamic:8000",
	})
	if err != nil {
		t.Fatalf("build metadata: %v", err)
	}

	state := &streamState{requestMetadata: make(map[string]interface{})}
	req := &extprocv3.ProcessingRequest{
		MetadataContext: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{"envoy.filters.http.ext_proc": md},
		},
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{},
		},
	}

	_ = server.handleRequestHeaders(req, state)

	if state.config == nil {
		t.Fatal("expected state.config to be set from static config, got nil")
	}
	if state.config.Provider != "presidio-api" {
		t.Errorf("provider = %q, want %q (static should win)", state.config.Provider, "presidio-api")
	}
	if state.config.Presidio == nil || state.config.Presidio.Endpoint != "http://static:8000" {
		t.Errorf("endpoint = %#v, want %q", state.config.Presidio, "http://static:8000")
	}
}

func TestStaticConfigWorksWithoutMetadataOrHeaders(t *testing.T) {
	staticCfg := &metadata.GuardrailConfig{
		Provider: "presidio-api",
		Modes:    []metadata.Mode{metadata.ModePreCall},
		Presidio: &metadata.PresidioConfig{Endpoint: "http://static:8000"},
	}
	server := NewServer(staticCfg)
	state := &streamState{requestMetadata: make(map[string]interface{})}
	req := &extprocv3.ProcessingRequest{
		Request: &extprocv3.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extprocv3.HttpHeaders{},
		},
	}
	_ = server.handleRequestHeaders(req, state)
	if state.config == nil || state.config.Provider != "presidio-api" {
		t.Fatalf("expected static config applied, got %#v", state.config)
	}
}
```

- [ ] **Step 2: Run test — confirm build fails**

Run: `go test ./internal/extproc/... -run 'TestStaticConfig' -v`
Expected: FAIL at compile time (`NewServer() takes no args` or equivalent).

- [ ] **Step 3: Change `NewServer` signature and add short-circuit**

Edit `internal/extproc/server.go`. Modify the `Server` struct and `NewServer`:

```go
// Server implements the Envoy ExternalProcessor service.
type Server struct {
	extprocv3.UnimplementedExternalProcessorServer
	protocolRegistry *protocol.Registry
	staticConfig     *metadata.GuardrailConfig
}

// NewServer creates a new ext_proc server. If staticConfig is non-nil, it is
// used for every request and dynamic metadata/headers are ignored. Pass nil
// to preserve the dynamic behavior.
func NewServer(staticConfig *metadata.GuardrailConfig) *Server {
	registry := protocol.NewRegistry(
		mcpparser.NewMCPParser(),
	)
	return &Server{
		protocolRegistry: registry,
		staticConfig:     staticConfig,
	}
}
```

Edit `handleRequestHeaders` — add a short-circuit at the top:

```go
func (s *Server) handleRequestHeaders(req *extprocv3.ProcessingRequest, state *streamState) *extprocv3.ProcessingResponse {
	if s.staticConfig != nil {
		state.config = s.staticConfig
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{},
			},
		}
	}
	// ... existing body (parseMetadata, then parseGuardrailHeaders fallback) ...
}
```

- [ ] **Step 4: Update all existing `NewServer()` call sites**

Grep to locate:
```bash
grep -rn "NewServer()" internal/ cmd/
```

Update each occurrence:
- `cmd/adapter/main.go`: `extprocServer := extproc.NewServer()` → `extprocServer := extproc.NewServer(nil)` (temporary; Task 5 replaces `nil` with real wiring).
- `internal/extproc/server_test.go`: every `NewServer()` → `NewServer(nil)`.

- [ ] **Step 5: Run full test suite**

Run: `make test`
Expected: PASS across all packages. The new static-config tests pass; existing tests unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/extproc/server.go internal/extproc/server_test.go cmd/adapter/main.go
git commit -m "feat(extproc): accept static config via NewServer"
```

---

## Task 5: Wire `GUARDRAIL_CONFIG_FILE` in `main.go`

**Files:**
- Modify: `cmd/adapter/main.go`

- [ ] **Step 1: Add env-var-driven loader**

Edit `cmd/adapter/main.go`. Add imports:

```go
import (
	// ... existing ...
	"os"

	"github.com/agentic-layer/guardrail-adapter/internal/metadata"
)
```

Replace the `extprocServer := extproc.NewServer(nil)` line with:

```go
	// Static config path (optional). When set, the adapter ignores dynamic
	// metadata and x-guardrail-* headers entirely.
	cfgPath := os.Getenv("GUARDRAIL_CONFIG_FILE")
	var staticCfg *metadata.GuardrailConfig
	if cfgPath != "" {
		loaded, err := metadata.LoadGuardrailConfigFile(cfgPath)
		if err != nil {
			log.Fatalf("failed to load static config file %s: %v", cfgPath, err)
		}
		staticCfg = loaded
		log.Printf("static config loaded from %s: provider=%s modes=%v", cfgPath, loaded.Provider, loaded.Modes)
	} else {
		log.Printf("static config disabled; using dynamic metadata/headers")
	}

	extprocServer := extproc.NewServer(staticCfg)
```

- [ ] **Step 2: Build and smoke-test locally**

Build:
```bash
make build
```

Smoke-test startup log with no env var:
```bash
./bin/adapter --addr :19001 --health-addr :18080 &
PID=$!
sleep 1
kill $PID
```
Expected: log line `static config disabled; using dynamic metadata/headers`.

Smoke-test with a valid file:
```bash
cat >/tmp/cfg.yaml <<'EOF'
provider: presidio-api
modes:
  - pre_call
presidio:
  endpoint: http://presidio:8000
EOF
GUARDRAIL_CONFIG_FILE=/tmp/cfg.yaml ./bin/adapter --addr :19001 --health-addr :18080 &
PID=$!
sleep 1
kill $PID
```
Expected: log line `static config loaded from /tmp/cfg.yaml: provider=presidio-api modes=[pre_call]`.

Smoke-test with a missing file:
```bash
GUARDRAIL_CONFIG_FILE=/tmp/nope.yaml ./bin/adapter --addr :19001 --health-addr :18080
```
Expected: process exits non-zero with `failed to load static config file /tmp/nope.yaml: read config file: ...`.

- [ ] **Step 3: Commit**

```bash
git add cmd/adapter/main.go
git commit -m "feat: wire GUARDRAIL_CONFIG_FILE env var in main"
```

---

## Task 6: Compose static-mode E2E

**Files:**
- Create: `test/static-config.yaml`
- Create: `compose.static.yml`
- Create: `test/e2e-static.sh`

- [ ] **Step 1: Create the sample static config**

Create `test/static-config.yaml`:

```yaml
provider: presidio-api
modes:
  - pre_call
presidio:
  endpoint: http://presidio:8000
  language: en
  score_thresholds:
    ALL: 0.5
  entity_actions:
    EMAIL_ADDRESS: MASK
```

The values mirror the inline metadata currently embedded in `test/e2e.sh` (case-sensitive: `MASK` and `ALL` match the existing provider contract).

- [ ] **Step 2: Create `compose.static.yml`**

Create `compose.static.yml`:

```yaml
services:
  adapter:
    build:
      context: .
      dockerfile: Dockerfile
    ports:
      - "9001:9001"
    environment:
      - LOG_LEVEL=info
      - GUARDRAIL_CONFIG_FILE=/etc/guardrail/config.yaml
    volumes:
      - ./test/static-config.yaml:/etc/guardrail/config.yaml:ro
    command: ["--addr", ":9001", "--health-addr", ":8080"]
    depends_on:
      - presidio

  presidio:
    image: ghcr.io/agentic-layer/presidio:0.3.0
    ports:
      - "8000:8000"
    healthcheck:
      test: ["CMD", ".venv/bin/python", "-c", "import requests; requests.get('http://localhost:8000/health').raise_for_status()"]
      interval: 1s
      timeout: 3s
      retries: 10
      start_period: 10s
```

Standalone file (not an overlay) — keeps `compose.yml` untouched for the dynamic-mode test and avoids Compose's environment-merge quirks.

- [ ] **Step 3: Create `test/e2e-static.sh`**

Create `test/e2e-static.sh` (chmod +x):

```bash
#!/bin/bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

ADAPTER_GRPC_ADDR="localhost:9001"

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

if ! command -v grpcurl &>/dev/null; then
    log_error "grpcurl is required: https://github.com/fullstorydev/grpcurl"
    exit 1
fi
if ! command -v jq &>/dev/null; then
    log_error "jq is required"
    exit 1
fi

log_info "Test: ext_proc Server Connectivity (static config mode)"
services=$(grpcurl -plaintext "${ADAPTER_GRPC_ADDR}" list 2>&1)
if ! echo "$services" | grep -q "envoy.service.ext_proc.v3.ExternalProcessor"; then
    log_error "✗ ext_proc service not found"
    log_error "Available: $services"
    exit 1
fi
log_info "✓ ext_proc service registered"

log_info "Test: Presidio PII masking via static config (no metadata_context)"

mcp_payload='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"send","arguments":{"message":"My email is john@example.com"}}}'
mcp_payload_b64=$(printf '%s' "$mcp_payload" | base64 | tr -d '\n')

# Note: no metadata_context. The adapter must read its config from the mounted
# YAML file and still mask the email.
stream=$(cat <<EOF
{"request_headers":{}}
{"request_body":{"body":"${mcp_payload_b64}","end_of_stream":true}}
EOF
)

if ! response=$(printf '%s' "$stream" | grpcurl -plaintext -d @ "${ADAPTER_GRPC_ADDR}" envoy.service.ext_proc.v3.ExternalProcessor/Process 2>&1); then
    log_error "✗ grpcurl failed"
    log_error "$response"
    exit 1
fi

masked_body_b64=$(echo "$response" | jq -sr '[.[] | .requestBody.response.bodyMutation.body? // empty | select(. != "")] | .[0] // ""')

if [ -z "$masked_body_b64" ]; then
    log_error "✗ Adapter did not return a body mutation — PII was not masked"
    log_error "Response: $response"
    exit 1
fi

masked_body=$(echo "$masked_body_b64" | base64 -d)
log_info "Masked body: $masked_body"

if echo "$masked_body" | grep -q "john@example.com"; then
    log_error "✗ Raw email leaked in masked body"
    exit 1
fi
if ! echo "$masked_body" | grep -q "<EMAIL_ADDRESS>"; then
    log_error "✗ <EMAIL_ADDRESS> token not present in masked body"
    exit 1
fi

log_info "✓ static-config PII masking works"
```

- [ ] **Step 4: Make the script executable**

Run: `chmod +x test/e2e-static.sh`

- [ ] **Step 5: Run the static-mode E2E locally**

```bash
docker compose -f compose.static.yml up --build --wait
./test/e2e-static.sh
docker compose -f compose.static.yml down -v
```

Expected: `✓ static-config PII masking works`.

If it fails, inspect logs with `docker compose -f compose.static.yml logs adapter`.

- [ ] **Step 6: Commit**

```bash
git add test/static-config.yaml compose.static.yml test/e2e-static.sh
git commit -m "test(e2e): add static-config Compose E2E variant"
```

---

## Task 7: Tilt / Envoy-Gateway static-mode E2E — overlay

**Files:**
- Create: `deploy/local-static/kustomization.yaml`
- Create: `deploy/local-static/config.yaml`
- Create: `deploy/local-static/adapter-patch.yaml`
- Create: `deploy/local-static/delete-patch-policy.yaml`

The overlay uses `configMapGenerator` so kustomize appends a content-hash
suffix to the ConfigMap name. When the YAML payload changes, the generated
name changes, kustomize rewrites the volume reference in the adapter
Deployment, and the Pod is re-created automatically — no manual checksum
annotation required.

Endpoint note: the in-cluster Presidio service lives at
`presidio.guardrail-providers` on port **80** (service-port 80 forwards to
container-port 8000). Confirmed against `deploy/local/presidio.yaml` and
`deploy/local/patch-policy.yaml`. The `EnvoyPatchPolicy` to delete is named
`guardrail-route-metadata` in namespace `default` (confirmed against
`deploy/local/patch-policy.yaml`).

- [ ] **Step 1: Create the YAML payload that becomes the ConfigMap**

Create `deploy/local-static/config.yaml`:

```yaml
provider: presidio-api
modes:
  - pre_call
presidio:
  endpoint: http://presidio.guardrail-providers:80
  language: en
  score_thresholds:
    ALL: 0.5
  entity_actions:
    EMAIL_ADDRESS: MASK
```

This file is the raw YAML payload — no `kind: ConfigMap` wrapper. Kustomize
will generate the ConfigMap around it in Step 4.

- [ ] **Step 2: Create the Deployment strategic-merge patch**

Create `deploy/local-static/adapter-patch.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: guardrail-adapter
  namespace: guardrails
spec:
  template:
    spec:
      containers:
        - name: guardrail-adapter
          env:
            - name: LOG_LEVEL
              value: info
            - name: GUARDRAIL_CONFIG_FILE
              value: /etc/guardrail/config.yaml
          volumeMounts:
            - name: guardrail-config
              mountPath: /etc/guardrail
              readOnly: true
      volumes:
        - name: guardrail-config
          configMap:
            name: guardrail-adapter-config
```

The name `guardrail-adapter-config` is the *generator base name*; kustomize
rewrites it to the hashed name at render time.

- [ ] **Step 3: Create the delete patch for `EnvoyPatchPolicy`**

Create `deploy/local-static/delete-patch-policy.yaml`:

```yaml
$patch: delete
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyPatchPolicy
metadata:
  name: guardrail-route-metadata
  namespace: default
```

- [ ] **Step 4: Create the overlay kustomization**

Create `deploy/local-static/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ../local

configMapGenerator:
  - name: guardrail-adapter-config
    namespace: guardrails
    files:
      - config.yaml

patches:
  - path: adapter-patch.yaml
    target:
      kind: Deployment
      name: guardrail-adapter
  - path: delete-patch-policy.yaml
    target:
      kind: EnvoyPatchPolicy
      name: guardrail-route-metadata
```

- [ ] **Step 5: Validate the overlay renders**

Run: `kubectl kustomize deploy/local-static/ > /tmp/rendered-static.yaml`

Inspect `/tmp/rendered-static.yaml` and verify:
- A `ConfigMap` named `guardrail-adapter-config-<hash>` is present in
  namespace `guardrails` with the YAML payload under key `config.yaml`.
- The `guardrail-adapter` Deployment has the volume mount, the
  `GUARDRAIL_CONFIG_FILE` env var, and a volume whose `configMap.name`
  references the **hashed** name (kustomize rewrote it automatically).
- **No** `EnvoyPatchPolicy` resource named `guardrail-route-metadata` is
  present.

If any expectation fails, adjust the patch files and re-render.

- [ ] **Step 6: Commit**

```bash
git add deploy/local-static/
git commit -m "feat(deploy): add static-config kustomize overlay"
```

---

## Task 8: Tiltfile switch + gateway E2E script

**Files:**
- Modify: `Tiltfile`
- Create: `test/e2e-gateway-static.sh`

- [ ] **Step 1: Make `Tiltfile` overlay-switchable via env var**

Edit `Tiltfile`. Replace:

```python
k8s_yaml(kustomize('deploy/local'))
```

with:

```python
overlay = os.environ.get('GUARDRAIL_ADAPTER_OVERLAY', 'deploy/local')
k8s_yaml(kustomize(overlay))
```

Also guard the `guardrail-route-metadata:envoypatchpolicy:default` entry in the `gateway-config` grouping so Tilt doesn't complain when the overlay deletes it:

```python
gateway_objects = [
    'eg:gatewayclass',
    'eg:gateway:default',
    'echo-mcp:httproute:default',
    'allow-default-to-guardrail-adapter:referencegrant:guardrails',
    'guardrail-extproc:envoyextensionpolicy:default',
]
if overlay == 'deploy/local':
    gateway_objects.append('guardrail-route-metadata:envoypatchpolicy:default')

k8s_resource(
    new_name='gateway-config',
    objects=gateway_objects,
    resource_deps=['envoy-gateway'],
    labels=['gateway'],
)
```

- [ ] **Step 2: Create `test/e2e-gateway-static.sh`**

Create `test/e2e-gateway-static.sh` (chmod +x). It is identical to `test/e2e-gateway.sh` — same assertion (the Envoy proxy is fronting everything and it doesn't know whether the adapter got its config from a file or a header).

```bash
#!/bin/bash
# Mirrors test/e2e-gateway.sh but exercises the static-config overlay:
# adapter reads its config from a mounted ConfigMap, no EnvoyPatchPolicy /
# Lua header injection is applied.
set -euo pipefail

exec "$(dirname "$0")/e2e-gateway.sh" "$@"
```

Rationale: the gateway-level assertion is unchanged; the overlay is what makes it a different test. Keeping the script a thin wrapper avoids duplicating the grpcurl/curl plumbing.

- [ ] **Step 3: Make the script executable**

Run: `chmod +x test/e2e-gateway-static.sh`

- [ ] **Step 4: Local verification (optional but recommended)**

In one shell:
```bash
GUARDRAIL_ADAPTER_OVERLAY=deploy/local-static tilt up
```

Wait for all resources green. Verify in another shell:
```bash
kubectl -n guardrails get configmap guardrail-adapter-config
kubectl -n guardrails describe deploy guardrail-adapter | grep -A2 GUARDRAIL_CONFIG_FILE
kubectl -n default get envoypatchpolicy 2>/dev/null || echo "no patch policies (expected)"
```

Run the e2e script after port-forward is up:
```bash
./test/e2e-gateway-static.sh
```

Expected: `✓ ...` success lines identical to the dynamic-mode path.

Tear down: `tilt down`.

- [ ] **Step 5: Commit**

```bash
git add Tiltfile test/e2e-gateway-static.sh
git commit -m "test(e2e): add static-config gateway E2E via Tiltfile overlay switch"
```

---

## Task 9: CI workflows

**Files:**
- Modify: `.github/workflows/e2e.yml`

- [ ] **Step 1: Add `compose-e2e-static` job**

Edit `.github/workflows/e2e.yml`. Add a new job sibling to `e2e` and `gateway-e2e`:

```yaml
  compose-e2e-static:
    name: End-to-End Tests (Static Config, Compose)
    runs-on: ubuntu-latest
    permissions:
      contents: 'read'

    steps:
      - name: Clone the code
        uses: actions/checkout@v6

      - name: Setup mise
        uses: jdx/mise-action@v2
        with:
          install: true

      - name: Install grpcurl
        run: |
          GRPCURL_VERSION=1.9.1
          curl -sSL "https://github.com/fullstorydev/grpcurl/releases/download/v${GRPCURL_VERSION}/grpcurl_${GRPCURL_VERSION}_linux_x86_64.tar.gz" | \
            sudo tar -xz -C /usr/local/bin grpcurl
          grpcurl --version

      - name: Start services with Docker Compose (static config)
        run: docker compose -f compose.static.yml up --build --wait

      - name: Run static-config e2e tests
        run: ./test/e2e-static.sh

      - name: Show logs on failure
        if: failure()
        run: |
          echo "=== Docker Compose Status ==="
          docker compose -f compose.static.yml ps
          echo ""
          echo "=== Logs ==="
          docker compose -f compose.static.yml logs

      - name: Cleanup
        if: always()
        run: docker compose -f compose.static.yml down -v
```

- [ ] **Step 2: Add `gateway-e2e-static` job**

Append another job (copy `gateway-e2e` and adapt):

```yaml
  gateway-e2e-static:
    name: End-to-End Tests (Static Config, Envoy Gateway)
    runs-on: ubuntu-latest
    permissions:
      contents: 'read'

    steps:
      - name: Clone the code
        uses: actions/checkout@v6

      - name: Setup mise
        uses: jdx/mise-action@v2
        with:
          install: true

      - name: Create kind cluster
        uses: helm/kind-action@v1
        with:
          cluster_name: guardrail-e2e-static

      - name: Bring up stack with Tilt (static overlay)
        env:
          GUARDRAIL_ADAPTER_OVERLAY: deploy/local-static
        run: tilt ci --timeout 15m

      - name: Port-forward Envoy data plane
        run: |
          GATEWAY_SVC=$(kubectl -n envoy-gateway-system get svc \
            -l gateway.envoyproxy.io/owning-gateway-name=eg \
            -o jsonpath='{.items[0].metadata.name}')
          kubectl -n envoy-gateway-system port-forward \
            "svc/$GATEWAY_SVC" 10000:80 >/tmp/port-forward.log 2>&1 &
          echo $! > /tmp/port-forward.pid
          for i in {1..30}; do
            if nc -z localhost 10000; then
              echo "port-forward ready on :10000"
              exit 0
            fi
            sleep 1
          done
          echo "port-forward did not become ready" >&2
          cat /tmp/port-forward.log >&2
          exit 1

      - name: Run gateway static-config e2e tests
        run: ./test/e2e-gateway-static.sh

      - name: Show logs on failure
        if: failure()
        run: |
          echo "=== Pods ==="
          kubectl get pods -A
          echo ""
          echo "=== Recent events ==="
          kubectl get events -A --sort-by=.lastTimestamp | tail -50
          echo ""
          echo "=== guardrail-adapter logs ==="
          kubectl -n guardrails logs -l app=guardrail-adapter --tail=200 || true
          echo ""
          echo "=== guardrail-adapter configmap ==="
          kubectl -n guardrails get configmap guardrail-adapter-config -o yaml || true
          echo ""
          echo "=== envoy data plane logs ==="
          kubectl -n envoy-gateway-system logs \
            -l gateway.envoyproxy.io/owning-gateway-name=eg \
            --all-containers --tail=200 || true
          echo ""
          echo "=== port-forward log ==="
          cat /tmp/port-forward.log 2>/dev/null || true

      - name: Cleanup
        if: always()
        run: |
          if [ -f /tmp/port-forward.pid ]; then
            kill "$(cat /tmp/port-forward.pid)" 2>/dev/null || true
          fi
```

- [ ] **Step 3: Lint the workflow file**

Run (if `actionlint` is installed): `actionlint .github/workflows/e2e.yml`
Otherwise visually diff the new jobs against the existing `e2e` and `gateway-e2e` to confirm the indentation is right.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/e2e.yml
git commit -m "ci(e2e): add static-config Compose and Gateway E2E jobs"
```

---

## Task 10: README update

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add a "Static configuration" section**

Insert a new section after the existing `### Configuration Flags` subsection in `README.md`:

```markdown
### Static Configuration (ConfigMap-friendly)

The adapter can alternatively be configured via a YAML file loaded once at
startup, bypassing the dynamic metadata / `EnvoyPatchPolicy` workaround.
This is the recommended path for Kubernetes deployments where one pod
serves a single guardrail+provider combination.

Set `GUARDRAIL_CONFIG_FILE` to the path of a YAML file with the following
schema:

```yaml
provider: presidio-api       # required
modes:                       # required, non-empty list: pre_call | post_call
  - pre_call
  - post_call
presidio:                    # required when provider: presidio-api
  endpoint: http://presidio-analyzer:3000
  language: en               # optional
  score_thresholds:          # optional
    EMAIL_ADDRESS: 0.5
  entity_actions:            # optional
    EMAIL_ADDRESS: MASK
```

When `GUARDRAIL_CONFIG_FILE` is set:
- the file is loaded and validated at startup; any error exits non-zero,
- dynamic metadata (`MetadataContext`) and `x-guardrail-*` headers are
  ignored,
- reloading requires restarting the pod (e.g. via a ConfigMap checksum
  annotation on the Pod template).

When `GUARDRAIL_CONFIG_FILE` is unset or empty, the adapter falls back to
the dynamic metadata path (with `x-guardrail-*` header fallback) described
above.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: document static configuration mode"
```

---

## Task 11: Final verification

- [ ] **Step 1: Full local test suite**

```bash
make lint
make test
make build
```
Expected: all green.

- [ ] **Step 2: Run dynamic-mode Compose E2E (regression check)**

```bash
docker compose up --build --wait
./test/e2e.sh
docker compose down -v
```
Expected: pass (nothing changed for the dynamic path).

- [ ] **Step 3: Run static-mode Compose E2E**

```bash
docker compose -f compose.static.yml up --build --wait
./test/e2e-static.sh
docker compose -f compose.static.yml down -v
```
Expected: pass.

- [ ] **Step 4: (Optional) Run both Tilt E2Es**

Dynamic:
```bash
tilt up   # in one shell
./test/e2e-gateway.sh   # in another, after port-forward
tilt down
```

Static:
```bash
GUARDRAIL_ADAPTER_OVERLAY=deploy/local-static tilt up
./test/e2e-gateway-static.sh
tilt down
```
Expected: both pass.

- [ ] **Step 5: Review the full diff**

```bash
git log --oneline main..HEAD
git diff main..HEAD --stat
```

Confirm the set of commits matches the task list and there are no
surprises.
