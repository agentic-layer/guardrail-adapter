#!/bin/bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

ADAPTER_GRPC_ADDR="localhost:9001"
PRESIDIO_ENDPOINT="http://presidio:8000"

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

if ! command -v grpcurl &> /dev/null; then
    log_error "grpcurl is required but not installed: https://github.com/fullstorydev/grpcurl"
    exit 1
fi

if ! command -v jq &> /dev/null; then
    log_error "jq is required but not installed"
    exit 1
fi

log_info "Test: ext_proc Server Connectivity"
services=$(grpcurl -plaintext "${ADAPTER_GRPC_ADDR}" list 2>&1)
if ! echo "$services" | grep -q "envoy.service.ext_proc.v3.ExternalProcessor"; then
    log_error "✗ ext_proc service not found in service list"
    log_error "Available services: $services"
    exit 1
fi
log_info "✓ ext_proc service is registered"

log_info "Test: Presidio PII masking via ext_proc"

mcp_payload='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"send","arguments":{"message":"My email is john@example.com"}}}'
mcp_payload_b64=$(printf '%s' "$mcp_payload" | base64 | tr -d '\n')

stream=$(cat <<EOF
{"request_headers":{},"metadata_context":{"filter_metadata":{"envoy.filters.http.ext_proc":{"guardrail.provider":"presidio-api","guardrail.mode":"pre_call","guardrail.presidio.endpoint":"${PRESIDIO_ENDPOINT}","guardrail.presidio.language":"en","guardrail.presidio.score_thresholds":"{\"ALL\":0.5}","guardrail.presidio.entity_actions":"{\"EMAIL_ADDRESS\":\"MASK\"}"}}}}
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
masked_message=$(echo "$masked_body" | jq -r '.params.arguments.message')

if [[ "$masked_message" == *"john@example.com"* ]]; then
    log_error "✗ Masked message still contains the original email address"
    log_error "Message: $masked_message"
    exit 1
fi

if [[ "$masked_message" != *"<EMAIL_ADDRESS>"* ]]; then
    log_error "✗ Masked message does not contain <EMAIL_ADDRESS> placeholder"
    log_error "Message: $masked_message"
    exit 1
fi

log_info "✓ Presidio detected and masked the email address"
log_info "  Masked message: $masked_message"

log_info "All tests passed!"
