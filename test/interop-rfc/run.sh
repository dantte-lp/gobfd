#!/usr/bin/env bash
# RFC interop test runner for GoBFD.
#
# Tests three RFCs with FRR peers:
#   1. RFC 7419 — Common interval alignment (tshark packet analysis)
#   2. RFC 9384 — BGP Cease BFD-Down (log + BGP state inspection)
#   3. RFC 9468 — Unsolicited BFD (auto-session creation)
#
# Usage:
#   ./test/interop-rfc/run.sh
#
# Prerequisites:
#   - podman and podman-compose installed
#   - Access to required container images (FRR, GoBGP)
#
# Exit codes:
#   0 - all tests passed
#   1 - test failure

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/compose.yml"
DC="podman-compose -f ${COMPOSE_FILE}"

# Colors for test output (disabled if not a terminal).
if [ -t 1 ]; then
    GREEN='\033[0;32m'
    RED='\033[0;31m'
    YELLOW='\033[0;33m'
    NC='\033[0m'
else
    GREEN=''
    RED=''
    YELLOW=''
    NC=''
fi

pass() { echo -e "${GREEN}PASS${NC}: $1"; }
fail() { echo -e "${RED}FAIL${NC}: $1"; }
info() { echo -e "${YELLOW}INFO${NC}: $1"; }

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

cleanup() {
    info "cleaning up containers and network"
    ${DC} down --volumes --remove-orphans 2>/dev/null || true
}

trap cleanup EXIT

# ---------------------------------------------------------------------------
# Build & Start
# ---------------------------------------------------------------------------

info "building container images"
${DC} build --no-cache

info "starting RFC interop test stack"
${DC} up -d

info "waiting for containers to start (15s)"
sleep 15

# Verify critical containers are running.
for svc in gobfd-rfc tshark-rfc frr-rfc gobfd-rfc9384 gobgp-rfc frr-rfc-bgp frr-rfc-unsolicited echo-reflector; do
    if ! podman ps --format '{{.Names}}' | grep -q "${svc}-interop"; then
        fail "container ${svc}-interop is not running"
        podman logs "${svc}-interop" 2>&1 | tail -20 || true
        exit 1
    fi
done

info "all containers are running"

# ---------------------------------------------------------------------------
# Run Go interop tests
# ---------------------------------------------------------------------------

info "running Go RFC interop tests"

INTEROP_RFC_COMPOSE_FILE="${COMPOSE_FILE}" \
    go test -tags interop_rfc -v -count=1 -timeout 300s ./test/interop-rfc/
TEST_EXIT=$?

if [ "${TEST_EXIT}" -eq 0 ]; then
    pass "all RFC interop tests passed"
else
    fail "RFC interop tests failed (exit code: ${TEST_EXIT})"
fi

exit "${TEST_EXIT}"
