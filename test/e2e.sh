#!/bin/bash
set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
ADAPTER_GRPC_ADDR="localhost:9001"
ADAPTER_HTTP_ADDR="localhost:8080"
PRESIDIO_ANALYZER_ADDR="localhost:5001"
TIMEOUT=30

# Test results
TESTS_PASSED=0
TESTS_FAILED=0

# Helper functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

wait_for_service() {
    local service_name=$1
    local url=$2
    local max_attempts=$TIMEOUT
    local attempt=1

    log_info "Waiting for $service_name to be ready..."
    while [ $attempt -le $max_attempts ]; do
        if curl -sf "$url" > /dev/null 2>&1; then
            log_info "$service_name is ready!"
            return 0
        fi
        echo -n "."
        sleep 1
        ((attempt++))
    done

    log_error "$service_name did not become ready within ${TIMEOUT}s"
    return 1
}

test_http_health_check() {
    log_info "Test 1: HTTP Health Check"

    local response
    if response=$(curl -sf "http://${ADAPTER_HTTP_ADDR}/health" 2>&1); then
        if echo "$response" | grep -q "OK"; then
            log_info "✓ HTTP health check passed"
            ((TESTS_PASSED++))
            return 0
        else
            log_error "✗ HTTP health check returned unexpected response: $response"
            ((TESTS_FAILED++))
            return 1
        fi
    else
        log_error "✗ HTTP health check failed: $response"
        ((TESTS_FAILED++))
        return 1
    fi
}

test_grpc_health_check() {
    log_info "Test 2: gRPC Health Check"

    # Check if grpcurl is available
    if ! command -v grpcurl &> /dev/null; then
        log_warning "grpcurl not installed, skipping gRPC health check test"
        log_warning "To run this test, install grpcurl: https://github.com/fullstorydev/grpcurl"
        return 0
    fi

    local response
    if response=$(grpcurl -plaintext "${ADAPTER_GRPC_ADDR}" grpc.health.v1.Health/Check 2>&1); then
        if echo "$response" | grep -q "SERVING"; then
            log_info "✓ gRPC health check passed"
            ((TESTS_PASSED++))
            return 0
        else
            log_error "✗ gRPC health check returned unexpected response: $response"
            ((TESTS_FAILED++))
            return 1
        fi
    else
        log_error "✗ gRPC health check failed: $response"
        ((TESTS_FAILED++))
        return 1
    fi
}

test_presidio_analyzer_health() {
    log_info "Test 3: Presidio Analyzer Health Check"

    local response
    if response=$(curl -sf "http://${PRESIDIO_ANALYZER_ADDR}/health" 2>&1); then
        log_info "✓ Presidio Analyzer health check passed"
        ((TESTS_PASSED++))
        return 0
    else
        log_error "✗ Presidio Analyzer health check failed: $response"
        ((TESTS_FAILED++))
        return 1
    fi
}

test_presidio_analyzer_analyze() {
    log_info "Test 4: Presidio Analyzer PII Detection"

    local test_payload='{"text":"My email is test@example.com and my phone is 212-555-1234","language":"en"}'
    local response

    if response=$(curl -sf -X POST "http://${PRESIDIO_ANALYZER_ADDR}/analyze" \
        -H "Content-Type: application/json" \
        -d "$test_payload" 2>&1); then

        # Check if response contains expected entity types
        if echo "$response" | grep -q "EMAIL_ADDRESS" && echo "$response" | grep -q "PHONE_NUMBER"; then
            log_info "✓ Presidio Analyzer correctly detected PII entities"
            ((TESTS_PASSED++))
            return 0
        else
            log_error "✗ Presidio Analyzer did not detect expected PII entities"
            log_error "Response: $response"
            ((TESTS_FAILED++))
            return 1
        fi
    else
        log_error "✗ Presidio Analyzer analyze request failed: $response"
        ((TESTS_FAILED++))
        return 1
    fi
}

test_ext_proc_passthrough() {
    log_info "Test 5: ext_proc Server Connectivity"

    # Check if grpcurl is available
    if ! command -v grpcurl &> /dev/null; then
        log_warning "grpcurl not installed, skipping ext_proc passthrough test"
        log_warning "To run this test, install grpcurl: https://github.com/fullstorydev/grpcurl"
        return 0
    fi

    # List services to verify ext_proc is registered
    local response
    if response=$(grpcurl -plaintext "${ADAPTER_GRPC_ADDR}" list 2>&1); then
        if echo "$response" | grep -q "envoy.service.ext_proc.v3.ExternalProcessor"; then
            log_info "✓ ext_proc service is registered and accessible"
            ((TESTS_PASSED++))
            return 0
        else
            log_error "✗ ext_proc service not found in service list"
            log_error "Available services: $response"
            ((TESTS_FAILED++))
            return 1
        fi
    else
        log_error "✗ Failed to list gRPC services: $response"
        ((TESTS_FAILED++))
        return 1
    fi
}

# Main test execution
main() {
    log_info "Starting e2e tests for guardrail-adapter"
    log_info "========================================"

    # Wait for all services to be ready
    wait_for_service "Adapter HTTP" "http://${ADAPTER_HTTP_ADDR}/health" || exit 1
    wait_for_service "Presidio Analyzer" "http://${PRESIDIO_ANALYZER_ADDR}/health" || exit 1

    echo ""
    log_info "All services are ready, starting tests..."
    echo ""

    # Run tests
    test_http_health_check
    echo ""

    test_grpc_health_check
    echo ""

    test_presidio_analyzer_health
    echo ""

    test_presidio_analyzer_analyze
    echo ""

    test_ext_proc_passthrough
    echo ""

    # Print summary
    log_info "========================================"
    log_info "Test Summary:"
    log_info "  Passed: ${GREEN}${TESTS_PASSED}${NC}"
    if [ $TESTS_FAILED -gt 0 ]; then
        log_info "  Failed: ${RED}${TESTS_FAILED}${NC}"
    else
        log_info "  Failed: ${TESTS_FAILED}"
    fi
    log_info "========================================"

    if [ $TESTS_FAILED -gt 0 ]; then
        log_error "Some tests failed!"
        exit 1
    else
        log_info "All tests passed!"
        exit 0
    fi
}

# Run main function
main "$@"
