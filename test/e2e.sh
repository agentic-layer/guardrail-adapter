#!/bin/bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

ADAPTER_GRPC_ADDR="localhost:9001"

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

log_info "Test: ext_proc Server Connectivity"

response=$(grpcurl -plaintext "${ADAPTER_GRPC_ADDR}" list 2>&1)
if echo "$response" | grep -q "envoy.service.ext_proc.v3.ExternalProcessor"; then
    log_info "✓ ext_proc service is registered and accessible"
    exit 0
else
    log_error "✗ ext_proc service not found in service list"
    log_error "Available services: $response"
    exit 1
fi
