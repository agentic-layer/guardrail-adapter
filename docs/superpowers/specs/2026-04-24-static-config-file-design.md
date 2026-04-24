# Static Config File Support

## Context

Today the guardrail-adapter derives its `GuardrailConfig` (provider + modes +
provider-specific settings) from one of two per-request sources:

1. Envoy `MetadataContext` (dynamic metadata), populated upstream via
   `filter_metadata`.
2. Fallback: `x-guardrail-*` request headers, currently injected by a Lua HTTP
   filter mounted through `EnvoyPatchPolicy`.

Both paths funnel into `metadata.ParseGuardrailConfig(map[string]string)` in
`internal/metadata/metadata.go`.

Routing route-level `filter_metadata` into the ext_proc server on Kubernetes
requires an experimental `EnvoyPatchPolicy` in Envoy Gateway, and even with
it, only the header-injection workaround reliably works. That is fragile:
patch paths are sensitive to Envoy Gateway version bumps, and the policy is
explicitly marked experimental.

This spec adds a third configuration source — a YAML file loaded once at
startup — for deployments that prefer static, per-pod configuration over
gateway-driven dynamic metadata.

## Goals

- Provide a static, file-based configuration path that works without any
  EnvoyPatchPolicy or Lua filter.
- Keep the existing dynamic metadata + header-fallback path untouched when
  the feature is not used.
- Fit a deployment model of **one adapter pod per guardrail+provider**, with
  a `ConfigMap` mounted at a well-known path.
- Fail fast on misconfiguration so broken setups surface as crash-looping
  pods, not silent passthrough.

## Non-goals

- Hot reload / file watching. Config is loaded once at startup. ConfigMap
  changes trigger a rolling restart via a checksum annotation.
- Multiple guardrails per pod. One pod = one config.
- A CLI flag for the file path. The env var is the sole interface.
- Precedence modes other than "static wins". When the env var is set, the
  dynamic metadata and header paths are bypassed entirely.
- Replacing the existing dynamic path. Both coexist; the mode is selected at
  startup by the presence of the env var.

## Design

### Mode selection

- Env var `GUARDRAIL_CONFIG_FILE` unset or empty → **dynamic mode** (current
  behavior: metadata → header fallback).
- Env var set to a path → **static mode**. File is loaded in `main()`,
  validated, and injected into the server. Any error fails startup.

### Request-time flow

In `internal/extproc/server.go`, `handleRequestHeaders` gets a short-circuit:

```
if s.staticConfig != nil:
    state.config = s.staticConfig
else:
    // existing: parseMetadata(req.MetadataContext), then
    //           parseGuardrailHeaders(req) as fallback
```

`handleRequestBody` / `handleResponseBody` are unchanged — they already
branch on `state.config`.

### YAML schema

```yaml
provider: presidio-api       # required
modes:                       # required, non-empty list
  - pre_call
  - post_call
presidio:                    # required when provider == presidio-api
  endpoint: http://presidio-analyzer:3000   # required
  language: en               # optional
  score_thresholds:          # optional
    EMAIL_ADDRESS: 0.5
    PERSON: 0.8
  entity_actions:            # optional
    EMAIL_ADDRESS: mask
    PERSON: redact
```

The schema mirrors `metadata.GuardrailConfig` and `metadata.PresidioConfig`.
Unknown top-level or nested keys cause a startup failure (strict decoding)
to catch typos early.

### Components

**`internal/metadata/metadata.go`** — add:

- Internal YAML struct types (`guardrailConfigYAML`, `presidioConfigYAML`)
  with `yaml` tags mirroring the schema above.
- `LoadGuardrailConfigFile(path string) (*GuardrailConfig, error)`:
  1. Read file from disk.
  2. Decode YAML using `sigs.k8s.io/yaml` with strict unknown-field handling.
  3. Convert to `*GuardrailConfig`.
  4. Validate: non-empty `provider`, non-empty `modes`, only known modes
     (`pre_call`, `post_call`), and for `provider: presidio-api` a
     non-empty `presidio.endpoint`.

The loader returns the existing `*GuardrailConfig` domain type — no new
type leaks out of the package.

**`internal/extproc/server.go`** — modify:

- `NewServer(staticConfig *metadata.GuardrailConfig) *Server` — new
  parameter. `nil` preserves existing behavior.
- Add `staticConfig *metadata.GuardrailConfig` field on `Server`.
- Short-circuit in `handleRequestHeaders` when `staticConfig != nil`.

**`cmd/adapter/main.go`** — modify:

- Read `os.Getenv("GUARDRAIL_CONFIG_FILE")`.
- If non-empty: call `metadata.LoadGuardrailConfigFile(path)`; on error,
  `log.Fatalf`.
- Pass result (or `nil`) to `extproc.NewServer(...)`.
- Log which mode is active on startup:
  - `"static config loaded from <path>: provider=<p> modes=<m>"`, or
  - `"static config disabled; using dynamic metadata/headers"`.

**`go.mod`** — add `sigs.k8s.io/yaml`.

### Error handling

| Condition | Behavior |
|---|---|
| Env var unset/empty | Dynamic mode, startup log notes it. |
| Env var set, file missing or unreadable | `log.Fatalf` with path + underlying error. Pod crash-loops. |
| Env var set, YAML malformed or has unknown fields | `log.Fatalf` with decoder error. |
| Env var set, validation fails | `log.Fatalf` naming the failing field (e.g. `"presidio.endpoint is required when provider is presidio-api"`). |
| Env var set, load succeeds | Info log; adapter serves normally. |

Runtime failures are not possible — `s.staticConfig` is set once at startup
and only read from.

### Deployment (Kubernetes)

Each guardrail+provider pair becomes its own Deployment:

- A `ConfigMap` containing the YAML under a key such as `config.yaml`.
- Volume + `volumeMount` at e.g. `/etc/guardrail/`.
- Container env: `GUARDRAIL_CONFIG_FILE=/etc/guardrail/config.yaml`.
- Pod-template annotation keyed on a hash of the ConfigMap so edits roll
  the Deployment.

A sample manifest goes under `deploy/local/` (or a new sibling directory)
to pair with the existing kustomize overlays.

### Boundaries

- `metadata` remains the sole producer of `*GuardrailConfig`. Both the
  existing flat-map path and the new YAML path live there.
- `extproc` depends on `metadata` (already does) and receives the config
  via constructor injection. No env/fs access inside the package.
- `cmd/adapter/main.go` is the only place that touches env vars and the
  filesystem for config.

## Testing

### Unit tests

`internal/metadata/metadata_test.go` — extend with
`TestLoadGuardrailConfigFile`:

- Golden case: valid Presidio YAML decodes to the expected `*GuardrailConfig`.
- Validation failures: empty `provider`; empty `modes`; unknown mode;
  missing `presidio.endpoint` for `presidio-api`; unknown top-level key;
  unknown key inside `presidio`.
- File errors: nonexistent path; malformed YAML; wrong type for a field
  (e.g. `modes` as a string instead of a list).
- Round-trip equivalence: a YAML config and the flat-map form of the same
  logical config both parse to equal `*GuardrailConfig` values. Confirms
  the YAML path doesn't drift semantically from the dynamic path.

`internal/extproc/server_test.go` — extend:

- `NewServer(nil)` preserves all current test behavior.
- `NewServer(&staticCfg)` short-circuits header parsing: a request with
  `MetadataContext` populated **and** conflicting `x-guardrail-*` headers
  is still processed with `staticCfg`.
- `NewServer(&staticCfg)` works with neither metadata nor headers present.

### E2E (required, both paths)

Both existing E2E paths gain a static-config variant, kept alongside the
existing dynamic-mode tests so non-regression stays covered. The test
assertion (Presidio masking the `EMAIL_ADDRESS` in an MCP `tools/call`
echo payload) is unchanged — only the configuration delivery mechanism
changes.

**Docker Compose path**

Today `test/e2e.sh` talks to the adapter directly via `grpcurl` and
injects config inline via `metadata_context`. The static variant:

- New `test/e2e-static.sh`: same assertion, but the streamed request has
  **no** `metadata_context` block. The adapter must pick up the config
  from the mounted file.
- New `compose.static.yml` (sibling to `compose.yml`): adds a bind-mount
  of `test/static-config.yaml` into the adapter container at
  `/etc/guardrail/config.yaml` and sets
  `GUARDRAIL_CONFIG_FILE=/etc/guardrail/config.yaml`. Existing
  `compose.yml` is left untouched so the dynamic-mode test keeps working.
- New `test/static-config.yaml`: YAML schema instance pointing at the
  compose `presidio` service, mirroring the inline metadata currently
  embedded in `test/e2e.sh`.

**Tilt / Envoy Gateway path**

Today `test/e2e-gateway.sh` goes through Envoy Gateway with the
`EnvoyPatchPolicy` + Lua filter injecting `x-guardrail-*` headers. The
static variant proves the adapter works without that workaround:

- New kustomize overlay under `deploy/local-static/` (sibling to
  `deploy/local/`): reuses the common manifests but substitutes a
  `ConfigMap` + volume mount for the adapter, and **omits** the
  `patch-policy.yaml`.
- Adapter Deployment (in the overlay) mounts the `ConfigMap` at
  `/etc/guardrail/` and sets `GUARDRAIL_CONFIG_FILE=/etc/guardrail/config.yaml`.
  Pod template gets a checksum annotation on the ConfigMap so edits
  roll the Deployment.
- New `test/e2e-gateway-static.sh`: same Envoy-path assertion as
  `e2e-gateway.sh`, but targets the overlay above.
- `Tiltfile` gains a second mode (flag or separate target) that loads
  the `deploy/local-static/` overlay.

CI wiring mirrors the existing compose-e2e and gateway-e2e GitHub
workflows: each static-variant script gets its own job so failures are
distinguishable from dynamic-mode regressions.

### Observability

No structured change beyond startup logs. Existing request-path logging is
unchanged; in static mode, the per-stream log output is identical to the
dynamic mode's happy path.

## Migration / compatibility

- Backward compatible: existing users with no env var see no change.
- Users running the gateway+PatchPolicy setup can migrate pod-by-pod:
  deploy a new instance with `GUARDRAIL_CONFIG_FILE`, route traffic to
  it, decommission the old one. No shared state between the two modes.

## Open questions

None remaining from brainstorming. All decisions — precedence (static
wins), format (YAML), reload (startup only), source (env var only),
failure mode (fail-fast), schema shape (structured) — resolved.
