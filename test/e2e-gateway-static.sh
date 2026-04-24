#!/bin/bash
# Mirrors test/e2e-gateway.sh but exercises the static-config overlay:
# adapter reads its config from a mounted ConfigMap, no EnvoyPatchPolicy /
# Lua header injection is applied.
set -euo pipefail

exec "$(dirname "$0")/e2e-gateway.sh" "$@"
