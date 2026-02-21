#!/usr/bin/env bash
# Interoperability test runner for GoBFD <-> FRR and GoBFD <-> BIRD3.
#
# This script builds the container images, starts the test stack,
# verifies BFD sessions reach Up state, tests failure detection,
# and cleans up.
#
# Usage:
#   ./test/interop/run.sh
#
# Prerequisites:
#   - podman and podman-compose installed
#   - Access to quay.io/frrouting/frr:10.2.5
#   - Access to docker.io/debian:trixie-slim (for BIRD3 build)
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

# Track test results.
TESTS_TOTAL=0
TESTS_PASSED=0
TESTS_FAILED=0

assert_pass() {
    TESTS_TOTAL=$((TESTS_TOTAL + 1))
    if "$@"; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

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

info "starting interop test stack"
${DC} up -d

info "waiting for containers to start (10s)"
sleep 10

# Verify all containers are running.
for svc in gobfd frr bird3; do
    if ! ${DC} ps | grep -q "${svc}-interop"; then
        fail "container ${svc}-interop is not running"
        ${DC} logs "${svc}" 2>&1 | tail -20
        exit 1
    fi
done

info "all containers are running"

# ---------------------------------------------------------------------------
# Helper: check FRR BFD peer status
# ---------------------------------------------------------------------------

frr_bfd_peer_status() {
    local peer_ip="$1"
    ${DC} exec -T frr vtysh -c "show bfd peers json" 2>/dev/null \
        | python3 -c "
import sys, json
data = json.load(sys.stdin)
for peer in data:
    if peer.get('peer') == '${peer_ip}':
        print(peer.get('status', 'unknown'))
        sys.exit(0)
print('not-found')
" 2>/dev/null || echo "error"
}

# ---------------------------------------------------------------------------
# Helper: check BIRD3 BFD session status
# ---------------------------------------------------------------------------

bird3_bfd_session_status() {
    local peer_ip="$1"
    # BIRD3 birdc command to show BFD sessions.
    ${DC} exec -T bird3 birdc "show bfd sessions" 2>/dev/null \
        | grep -F "${peer_ip}" \
        | awk '{print $NF}' \
        || echo "not-found"
}

# ---------------------------------------------------------------------------
# Helper: check GoBFD session status via logs
# ---------------------------------------------------------------------------

gobfd_session_up() {
    local peer_ip="$1"
    ${DC} logs gobfd 2>&1 | grep -q "session state changed.*new_state=Up.*${peer_ip}" \
        && echo "up" || echo "not-up"
}

# ---------------------------------------------------------------------------
# Test 1: BFD three-way handshake — GoBFD <-> FRR
# ---------------------------------------------------------------------------

test_frr_handshake() {
    info "test 1: BFD handshake GoBFD <-> FRR"

    # Wait up to 30 seconds for the BFD session to come up.
    local max_wait=30
    local interval=2
    local waited=0

    while [ "${waited}" -lt "${max_wait}" ]; do
        local status
        status="$(frr_bfd_peer_status "172.20.0.10")"
        if [ "${status}" = "up" ]; then
            pass "FRR BFD session with gobfd (172.20.0.10) is Up"
            return 0
        fi
        sleep "${interval}"
        waited=$((waited + interval))
    done

    fail "FRR BFD session did not reach Up state within ${max_wait}s (status: ${status:-unknown})"
    info "FRR BFD peers:"
    ${DC} exec -T frr vtysh -c "show bfd peers" 2>&1 || true
    info "GoBFD logs (last 30 lines):"
    ${DC} logs --tail 30 gobfd 2>&1 || true
    return 1
}

# ---------------------------------------------------------------------------
# Test 2: BFD three-way handshake — GoBFD <-> BIRD3
# ---------------------------------------------------------------------------

test_bird3_handshake() {
    info "test 2: BFD handshake GoBFD <-> BIRD3"

    local max_wait=30
    local interval=2
    local waited=0

    while [ "${waited}" -lt "${max_wait}" ]; do
        local status
        status="$(bird3_bfd_session_status "172.20.0.10")"
        if echo "${status}" | grep -qi "up"; then
            pass "BIRD3 BFD session with gobfd (172.20.0.10) is Up"
            return 0
        fi
        sleep "${interval}"
        waited=$((waited + interval))
    done

    fail "BIRD3 BFD session did not reach Up state within ${max_wait}s (status: ${status:-unknown})"
    info "BIRD3 BFD sessions:"
    ${DC} exec -T bird3 birdc "show bfd sessions" 2>&1 || true
    info "GoBFD logs (last 30 lines):"
    ${DC} logs --tail 30 gobfd 2>&1 || true
    return 1
}

# ---------------------------------------------------------------------------
# Test 3: Detection timeout — stop FRR, verify gobfd detects Down
# ---------------------------------------------------------------------------

test_frr_detection_timeout() {
    info "test 3: detection timeout — stop FRR"

    # Stop the FRR container to simulate peer failure.
    ${DC} stop frr

    # Wait for detection time + margin.
    # Detection time = detect_mult * interval = 3 * 300ms = 900ms.
    # Wait 5 seconds to be safe with jitter and timer alignment.
    sleep 5

    # Check GoBFD logs for the Down transition.
    if ${DC} logs gobfd 2>&1 | grep -q "session state changed.*new_state=Down"; then
        pass "GoBFD detected FRR peer failure (session transitioned to Down)"
    else
        fail "GoBFD did not detect FRR peer failure"
        info "GoBFD logs (last 30 lines):"
        ${DC} logs --tail 30 gobfd 2>&1 || true
        return 1
    fi

    # Restart FRR for subsequent tests.
    ${DC} start frr
    sleep 5

    return 0
}

# ---------------------------------------------------------------------------
# Test 4: Graceful shutdown — stop GoBFD, verify FRR detects AdminDown
# ---------------------------------------------------------------------------

test_gobfd_graceful_shutdown() {
    info "test 4: graceful shutdown — stop GoBFD"

    # Wait for sessions to re-establish after Test 3.
    local max_wait=30
    local interval=2
    local waited=0

    while [ "${waited}" -lt "${max_wait}" ]; do
        local status
        status="$(frr_bfd_peer_status "172.20.0.10")"
        if [ "${status}" = "up" ]; then
            break
        fi
        sleep "${interval}"
        waited=$((waited + interval))
    done

    # Send SIGTERM to gobfd for graceful shutdown (AdminDown).
    ${DC} stop gobfd

    # Wait for FRR to detect the session going down.
    sleep 5

    local status
    status="$(frr_bfd_peer_status "172.20.0.10")"
    if [ "${status}" = "down" ] || [ "${status}" = "not-found" ]; then
        pass "FRR detected GoBFD graceful shutdown"
    else
        fail "FRR did not detect GoBFD shutdown (status: ${status})"
        info "FRR BFD peers:"
        ${DC} exec -T frr vtysh -c "show bfd peers" 2>&1 || true
        return 1
    fi

    return 0
}

# ---------------------------------------------------------------------------
# Run all tests
# ---------------------------------------------------------------------------

echo ""
echo "========================================="
echo "  GoBFD Interoperability Tests"
echo "  FRR 10.2.5 + BIRD3 (Debian Trixie)"
echo "========================================="
echo ""

assert_pass test_frr_handshake
assert_pass test_bird3_handshake
assert_pass test_frr_detection_timeout
assert_pass test_gobfd_graceful_shutdown

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

echo ""
echo "========================================="
echo "  Results: ${TESTS_PASSED}/${TESTS_TOTAL} passed, ${TESTS_FAILED} failed"
echo "========================================="
echo ""

if [ "${TESTS_FAILED}" -gt 0 ]; then
    exit 1
fi

exit 0
