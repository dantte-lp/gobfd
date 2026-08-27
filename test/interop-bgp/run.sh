#!/usr/bin/env bash
# BGP+BFD full-cycle interoperability test runner.
#
# Tests the end-to-end BFD->BGP coupling: when BFD detects a forwarding
# failure, GoBGP disables the BGP peer; on recovery, the peer is re-enabled.
#
# Three scenarios:
#   1. GoBFD + GoBGP  <->  FRR (bgpd + bfdd)
#   2. GoBFD + GoBGP  <->  BIRD3 (BGP + bfd on)
#   3. GoBFD + GoBGP  <->  GoBFD + ExaBGP (BFD sidecar)
#
# Usage:
#   ./test/interop-bgp/run.sh
#
# Prerequisites:
#   - podman and podman compose installed
#   - Access to required container images (FRR, GoBGP, ExaBGP, BIRD3)
#
# Exit codes:
#   0 - all tests passed
#   1 - test failure

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/compose.yml"
DC="podman compose -f ${COMPOSE_FILE}"

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

info "starting BGP+BFD interop test stack"
${DC} up -d

info "waiting for containers to start (15s)"
sleep 15

# Verify critical containers are running.
for svc in gobfd-bgp gobgp frr-bgp bird3-bgp gobfd-exabgp exabgp; do
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

info "running Go BGP+BFD interop tests"

INTEROP_BGP_COMPOSE_FILE="${COMPOSE_FILE}" \
    go test -tags interop_bgp -v -count=1 -timeout 300s ./test/interop-bgp/
TEST_EXIT=$?

if [ "${TEST_EXIT}" -eq 0 ]; then
    pass "all BGP+BFD interop tests passed"
else
    fail "BGP+BFD interop tests failed (exit code: ${TEST_EXIT})"
fi

exit "${TEST_EXIT}"
