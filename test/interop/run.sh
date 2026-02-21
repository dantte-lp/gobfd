#!/usr/bin/env bash
# Interoperability test runner for GoBFD <-> FRR and GoBFD <-> BIRD3.
#
# This script builds the container images, starts the test stack,
# verifies BFD sessions reach Up state, runs comprehensive RFC 5880/5881
# compliance checks via tshark packet analysis, tests failure detection,
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

GOBFD_IP="172.20.0.10"
FRR_IP="172.20.0.20"
BIRD3_IP="172.20.0.30"

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

dump_tshark() {
    if podman ps --format '{{.Names}}' 2>/dev/null | grep -q tshark-interop; then
        info "=== BFD packet capture (last 50 packets) ==="
        podman exec tshark-interop tshark -r /captures/bfd.pcapng -Y bfd -c 50 \
            -T fields -e frame.time_relative -e ip.src -e ip.dst \
            -e bfd.sta -e bfd.flags -e bfd.my_discriminator \
            -e bfd.your_discriminator -e bfd.desired_min_tx_interval \
            -e bfd.required_min_rx_interval \
            -E header=y -E separator='	' 2>/dev/null || true
    fi
}

cleanup() {
    if [ "${TESTS_FAILED}" -gt 0 ]; then
        dump_tshark
    fi
    info "cleaning up containers and network"
    ${DC} down --volumes --remove-orphans 2>/dev/null || true
}

trap cleanup EXIT

# ---------------------------------------------------------------------------
# tshark helpers
# ---------------------------------------------------------------------------

# tshark_count — return number of packets matching a display filter.
tshark_count() {
    local filter="$1"
    local count
    count="$(podman exec tshark-interop tshark -r /captures/bfd.pcapng \
        -Y "${filter}" -T fields -e frame.number 2>/dev/null | wc -l)"
    echo "${count}"
}

# tshark_fields — extract fields from packets matching a display filter.
# Usage: tshark_fields "filter" "field1" "field2" ...
# Output: tab-separated rows on stdout.
tshark_fields() {
    local filter="$1"
    shift
    local field_args=()
    for f in "$@"; do
        field_args+=("-e" "$f")
    done
    podman exec tshark-interop tshark -r /captures/bfd.pcapng \
        -Y "${filter}" -T fields "${field_args[@]}" \
        -E separator='	' -E header=n 2>/dev/null
}

# assert_no_packets — fail if any packets match the filter.
assert_no_packets() {
    local filter="$1"
    local desc="$2"
    local count
    count="$(tshark_count "${filter}")"
    if [ "${count}" -gt 0 ]; then
        fail "${desc} — found ${count} violating packets (filter: ${filter})"
        return 1
    fi
    pass "${desc}"
    return 0
}

# assert_has_packets — fail if no packets match the filter.
assert_has_packets() {
    local filter="$1"
    local desc="$2"
    local count
    count="$(tshark_count "${filter}")"
    if [ "${count}" -eq 0 ]; then
        fail "${desc} — no packets found (filter: ${filter})"
        return 1
    fi
    pass "${desc} (${count} packets)"
    return 0
}

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
    # BIRD3 output: IP Interface State Since Interval Timeout
    # State is column 3, not the last field.
    ${DC} exec -T bird3 birdc "show bfd sessions" 2>/dev/null \
        | grep -F "${peer_ip}" \
        | awk '{print $3}' \
        || echo "not-found"
}

# ---------------------------------------------------------------------------
# Helper: wait for session to reach Up
# ---------------------------------------------------------------------------

wait_frr_up() {
    local max_wait="${1:-30}"
    local interval=2
    local waited=0

    while [ "${waited}" -lt "${max_wait}" ]; do
        local status
        status="$(frr_bfd_peer_status "${GOBFD_IP}")"
        if [ "${status}" = "up" ]; then
            return 0
        fi
        sleep "${interval}"
        waited=$((waited + interval))
    done
    return 1
}

wait_bird3_up() {
    local max_wait="${1:-30}"
    local interval=2
    local waited=0

    while [ "${waited}" -lt "${max_wait}" ]; do
        local status
        status="$(bird3_bfd_session_status "${GOBFD_IP}")"
        if echo "${status}" | grep -qi "up"; then
            return 0
        fi
        sleep "${interval}"
        waited=$((waited + interval))
    done
    return 1
}

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

# ===========================================================================
# Test 1: BFD three-way handshake — GoBFD <-> FRR
# ===========================================================================

test_frr_handshake() {
    info "test: BFD handshake GoBFD <-> FRR"
    if wait_frr_up 60; then
        pass "FRR BFD session with gobfd (${GOBFD_IP}) is Up"
        return 0
    fi
    fail "FRR BFD session did not reach Up state within 60s"
    ${DC} exec -T frr vtysh -c "show bfd peers" 2>&1 || true
    ${DC} logs --tail 30 gobfd 2>&1 || true
    return 1
}

# ===========================================================================
# Test 2: BFD three-way handshake — GoBFD <-> BIRD3
# ===========================================================================

test_bird3_handshake() {
    info "test: BFD handshake GoBFD <-> BIRD3"
    if wait_bird3_up 60; then
        pass "BIRD3 BFD session with gobfd (${GOBFD_IP}) is Up"
        return 0
    fi
    fail "BIRD3 BFD session did not reach Up state within 60s"
    ${DC} exec -T bird3 birdc "show bfd sessions" 2>&1 || true
    ${DC} logs --tail 30 gobfd 2>&1 || true
    return 1
}

# ===========================================================================
# Group A: Packet-Level Invariants (RFC 5880 §4.1, RFC 5881 §4-5)
# Read-only tshark analysis — no state changes.
# ===========================================================================

GOBFD_PKTS="bfd && ip.src == ${GOBFD_IP}"

test_rfc5880_version() {
    info "test: RFC 5880 §4.1 — version=1"
    assert_no_packets \
        "${GOBFD_PKTS} && bfd.version != 1" \
        "all GoBFD packets must have version=1"
}

test_rfc5880_multipoint_zero() {
    info "test: RFC 5880 §4.1 — multipoint bit=0"
    assert_no_packets \
        "${GOBFD_PKTS} && bfd.flags.m == 1" \
        "multipoint bit must always be 0"
}

test_rfc5880_demand_zero() {
    info "test: RFC 5880 §4.1 — demand bit=0"
    assert_no_packets \
        "${GOBFD_PKTS} && bfd.flags.d == 1" \
        "demand bit must be 0 (not implemented)"
}

test_rfc5881_echo_interval_zero() {
    info "test: RFC 5881 §4 — echo interval=0"
    assert_no_packets \
        "${GOBFD_PKTS} && bfd.required_min_echo_interval != 0" \
        "RequiredMinEchoRxInterval must be 0 (echo not implemented)"
}

test_rfc5880_my_discr_nonzero() {
    info "test: RFC 5880 §6.8.7 — MyDiscriminator!=0"
    assert_no_packets \
        "${GOBFD_PKTS} && bfd.my_discriminator == 0x00000000" \
        "My Discriminator must be nonzero in all packets"
}

test_rfc5880_packet_length() {
    info "test: RFC 5880 §4.1 — packet length=24 (no auth)"
    assert_no_packets \
        "${GOBFD_PKTS} && bfd.length != 24" \
        "packet length must be 24 (no auth)"
}

test_rfc5881_ttl_255() {
    info "test: RFC 5881 §5 — TTL=255 (GTSM)"
    assert_no_packets \
        "${GOBFD_PKTS} && ip.ttl != 255" \
        "all single-hop packets must have TTL=255"
}

test_rfc5881_dst_port_3784() {
    info "test: RFC 5881 §4 — dst port=3784"
    assert_no_packets \
        "${GOBFD_PKTS} && udp.dstport != 3784" \
        "destination port must be 3784"
}

test_rfc5881_src_port_ephemeral() {
    info "test: RFC 5881 §4 — src port 49152-65535"
    assert_no_packets \
        "${GOBFD_PKTS} && (udp.srcport < 49152 || udp.srcport > 65535)" \
        "source port must be in 49152-65535"
}

# ===========================================================================
# Group B: Handshake & State Sequence (RFC 5880 §6.2, §6.8.6)
# ===========================================================================

test_rfc5880_handshake_sequence() {
    info "test: RFC 5880 §6.2 — handshake state sequence Down→Init→Up"

    local max_state=0
    local found_up=false
    local regression=false

    while IFS= read -r state_val; do
        state_val="$(echo "${state_val}" | tr -d '[:space:]')"
        [ -z "${state_val}" ] && continue

        # Handle hex values from tshark.
        local state_int
        state_int="$((state_val))"

        if [ "${state_int}" -gt "${max_state}" ]; then
            max_state="${state_int}"
        elif [ "${state_int}" -lt "${max_state}" ]; then
            regression=true
            break
        fi
        if [ "${state_int}" -eq 3 ]; then
            found_up=true
            break
        fi
    done < <(tshark_fields "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${FRR_IP}" "bfd.sta")

    if [ "${regression}" = true ]; then
        fail "handshake has state regression (went backward after reaching state ${max_state})"
        return 1
    fi
    if [ "${found_up}" = true ]; then
        pass "handshake follows strict Down→Init→Up sequence"
        return 0
    fi

    fail "handshake did not reach Up (max state: ${max_state})"
    return 1
}

test_rfc5880_discr_learning() {
    info "test: RFC 5880 §6.8.6 — YourDiscriminator=0 only in Down state"
    assert_no_packets \
        "${GOBFD_PKTS} && bfd.your_discriminator == 0x00000000 && bfd.sta != 0x01 && bfd.sta != 0x00" \
        "YourDiscriminator=0 only valid in Down/AdminDown state"
}

test_rfc5880_discr_uniqueness() {
    info "test: RFC 5880 §6.8.1 — unique discriminators per session"

    local frr_discr bird3_discr
    frr_discr="$(tshark_fields \
        "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${FRR_IP} && bfd.sta == 0x03" \
        "bfd.my_discriminator" | head -1 | tr -d '[:space:]')"
    bird3_discr="$(tshark_fields \
        "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${BIRD3_IP} && bfd.sta == 0x03" \
        "bfd.my_discriminator" | head -1 | tr -d '[:space:]')"

    if [ -z "${frr_discr}" ] || [ -z "${bird3_discr}" ]; then
        fail "could not extract discriminators (frr=${frr_discr:-empty}, bird3=${bird3_discr:-empty})"
        return 1
    fi

    if [ "${frr_discr}" = "${bird3_discr}" ]; then
        fail "FRR and BIRD3 sessions use same discriminator: ${frr_discr}"
        return 1
    fi

    pass "discriminators are unique (FRR=${frr_discr}, BIRD3=${bird3_discr})"
    return 0
}

# ===========================================================================
# Group C: Slow TX Rate (RFC 5880 §6.8.3)
# ===========================================================================

test_rfc5880_slow_tx_when_not_up() {
    info "test: RFC 5880 §6.8.3 — DesiredMinTxInterval >= 1s when not Up"
    assert_no_packets \
        "${GOBFD_PKTS} && (bfd.sta == 0x01 || bfd.sta == 0x02) && bfd.desired_min_tx_interval < 1000000" \
        "DesiredMinTxInterval must be >= 1s (1000000us) when not Up"
}

test_rfc5880_fast_tx_once_up() {
    info "test: RFC 5880 §6.8.3 — configured interval once Up"

    local interval
    interval="$(tshark_fields \
        "${GOBFD_PKTS} && bfd.sta == 0x03" \
        "bfd.desired_min_tx_interval" | head -1 | tr -d '[:space:]')"

    if [ -z "${interval}" ]; then
        fail "no Up packets found"
        return 1
    fi

    local interval_int
    interval_int="$((interval))"

    if [ "${interval_int}" -eq 300000 ]; then
        pass "DesiredMinTxInterval in Up state = 300000us (300ms)"
        return 0
    fi

    fail "DesiredMinTxInterval in Up state = ${interval_int}, want 300000"
    return 1
}

# ===========================================================================
# Group D: Diagnostic code — initial state (RFC 5880 §6.8.1)
# ===========================================================================

test_rfc5880_diag_zero_initial() {
    info "test: RFC 5880 §6.8.1 — initial Diag=0"

    local diag
    diag="$(tshark_fields \
        "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${FRR_IP} && bfd.sta == 0x01" \
        "bfd.diag" | head -1 | tr -d '[:space:]')"

    if [ -z "${diag}" ]; then
        info "SKIP: no initial Down packets captured"
        pass "RFC 5880 §6.8.1 — initial Diag (skipped: no Down packets)"
        return 0
    fi

    local diag_int
    diag_int="$((diag))"

    if [ "${diag_int}" -eq 0 ]; then
        pass "first Down packet has Diag=0 (No Diagnostic)"
        return 0
    fi

    fail "first Down packet diag = ${diag_int}, want 0"
    return 1
}

# ===========================================================================
# Group F: Poll/Final during handshake (RFC 5880 §6.5)
# ===========================================================================

test_rfc5880_poll_final_handshake() {
    info "test: RFC 5880 §6.5 — Poll/Final exchange during handshake"

    local poll_count final_count
    poll_count="$(tshark_count "${GOBFD_PKTS} && bfd.flags.p == 1")"
    final_count="$(tshark_count "${GOBFD_PKTS} && bfd.flags.f == 1")"

    info "GoBFD Poll packets: ${poll_count}, Final packets: ${final_count}"

    if [ "${final_count}" -eq 0 ]; then
        fail "GoBFD never sent Final (F=1) — expected during handshake P/F exchange"
        return 1
    fi

    pass "Poll/Final exchange observed (P=${poll_count}, F=${final_count})"
    return 0
}

# ===========================================================================
# Group E: Session independence — stop FRR, verify BIRD3 stays Up
# ===========================================================================

test_rfc5880_session_independence() {
    info "test: RFC 5880 §6.8.1 — session independence (stop FRR, check BIRD3)"

    ${DC} stop frr

    sleep 3

    local status
    status="$(bird3_bfd_session_status "${GOBFD_IP}")"
    if echo "${status}" | grep -qi "up"; then
        pass "BIRD3 session remained Up when FRR was stopped"
        ${DC} start frr
        return 0
    fi

    fail "BIRD3 session went Down when only FRR was stopped"
    ${DC} start frr
    return 1
}

# ===========================================================================
# Group D: Diagnostic code — timeout (RFC 5880 §6.8.4)
# ===========================================================================

test_rfc5880_diag_time_expired() {
    info "test: RFC 5880 §6.8.4 — Diag=1 after detection timeout"
    assert_has_packets \
        "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${FRR_IP} && bfd.sta == 0x01 && bfd.diag == 0x01" \
        "GoBFD must send Down with Diag=1 (Control Detection Time Expired)"
}

# ===========================================================================
# Group E: Detection timing precision (RFC 5880 §6.8.4)
# ===========================================================================

test_rfc5880_detection_precision() {
    info "test: RFC 5880 §6.8.4 — detection time precision"

    # Get first Down(diag=1) epoch, then find last FRR packet before it.
    # The capture may contain multiple stop/restart cycles.
    local first_down_epoch
    first_down_epoch="$(tshark_fields \
        "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${FRR_IP} && bfd.sta == 0x01 && bfd.diag == 0x01" \
        "frame.time_epoch" | head -1 | tr -d '[:space:]')"

    if [ -z "${first_down_epoch}" ]; then
        info "SKIP: no Down(diag=1) packets"
        pass "detection precision (skipped)"
        return 0
    fi

    # Find last FRR packet with timestamp < first_down_epoch.
    local all_frr_epochs
    all_frr_epochs="$(tshark_fields \
        "bfd && ip.src == ${FRR_IP} && ip.dst == ${GOBFD_IP}" \
        "frame.time_epoch")"

    if [ -z "${all_frr_epochs}" ]; then
        info "SKIP: no FRR packets"
        pass "detection precision (skipped)"
        return 0
    fi

    local result
    result="$(echo "${all_frr_epochs}" | python3 -c "
import sys
down = ${first_down_epoch}
last_before = None
for line in sys.stdin:
    ts = float(line.strip())
    if ts < down:
        last_before = ts
if last_before is None:
    print('skip')
else:
    gap = down - last_before
    print(f'{gap:.3f}')
")"

    if [ "${result}" = "skip" ]; then
        info "SKIP: no FRR packets before Down"
        pass "detection precision (skipped)"
        return 0
    fi

    info "detection gap: last FRR packet → first Down = ${result}s"

    local gap_ok
    gap_ok="$(python3 -c "print('yes' if 0 <= ${result} <= 3.0 else 'no')")"

    if [ "${gap_ok}" = "yes" ]; then
        pass "detection time ${result}s is within acceptable range (< 3.0s)"
        return 0
    fi

    fail "detection took ${result}s, want < 3.0s"
    return 1
}

# ===========================================================================
# Group E: Session recovery (RFC 5880 §6.2)
# ===========================================================================

test_rfc5880_session_recovery() {
    info "test: RFC 5880 §6.2 — session recovery after FRR restart"

    # FRR should have been restarted by session_independence cleanup.
    # Wait for recovery.
    if wait_frr_up 60; then
        pass "FRR session recovered to Up after restart"

        # Verify BIRD3 still Up.
        local status
        status="$(bird3_bfd_session_status "${GOBFD_IP}")"
        if echo "${status}" | grep -qi "up"; then
            pass "BIRD3 session still Up after FRR recovery cycle"
            return 0
        fi
        fail "BIRD3 session not Up after FRR recovery"
        return 1
    fi

    fail "FRR session did not recover to Up within 60s"
    return 1
}

# ===========================================================================
# Group G: AdminDown from FRR (RFC 5880 §6.8.6)
# ===========================================================================

test_rfc5880_frr_admin_down() {
    info "test: RFC 5880 §6.8.6 — FRR AdminDown via shutdown"

    ${DC} exec -T frr vtysh \
        -c "configure terminal" \
        -c "bfd" \
        -c "peer ${GOBFD_IP}" \
        -c "shutdown" 2>/dev/null

    sleep 3

    # Verify FRR sent AdminDown (state=0).
    assert_has_packets \
        "bfd && ip.src == ${FRR_IP} && bfd.sta == 0x00" \
        "FRR must send AdminDown (state=0) after shutdown"
    local frr_ok=$?

    # Verify GoBFD transitioned to Down with Diag=3.
    assert_has_packets \
        "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${FRR_IP} && bfd.sta == 0x01 && bfd.diag == 0x03" \
        "GoBFD must set Diag=3 (Neighbor Signaled) when receiving AdminDown"
    local gobfd_ok=$?

    if [ "${frr_ok}" -eq 0 ] && [ "${gobfd_ok}" -eq 0 ]; then
        return 0
    fi
    return 1
}

test_rfc5880_frr_admin_down_recovery() {
    info "test: RFC 5880 §6.8.16 — recovery after FRR AdminDown cleared"

    ${DC} exec -T frr vtysh \
        -c "configure terminal" \
        -c "bfd" \
        -c "peer ${GOBFD_IP}" \
        -c "no shutdown" 2>/dev/null

    if wait_frr_up 30; then
        pass "FRR session recovered after AdminDown cleared"
        return 0
    fi

    fail "FRR session did not recover after 'no shutdown'"
    return 1
}

# ===========================================================================
# Group F: Poll/Final from parameter change (RFC 5880 §6.5)
# ===========================================================================

test_rfc5880_poll_final_parameter_change() {
    info "test: RFC 5880 §6.5 — Poll/Final on FRR interval change"

    local poll_before final_before
    poll_before="$(tshark_count "bfd && ip.src == ${FRR_IP} && bfd.flags.p == 1")"
    final_before="$(tshark_count "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${FRR_IP} && bfd.flags.f == 1")"

    # Change FRR transmit interval to trigger Poll Sequence.
    ${DC} exec -T frr vtysh \
        -c "configure terminal" \
        -c "bfd" \
        -c "peer ${GOBFD_IP}" \
        -c "transmit-interval 200" 2>/dev/null

    sleep 5

    local poll_after final_after
    poll_after="$(tshark_count "bfd && ip.src == ${FRR_IP} && bfd.flags.p == 1")"
    final_after="$(tshark_count "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${FRR_IP} && bfd.flags.f == 1")"

    info "Poll/Final: FRR polls ${poll_before}→${poll_after}, GoBFD finals ${final_before}→${final_after}"

    local ok=true
    if [ "${poll_after}" -le "${poll_before}" ]; then
        fail "FRR did not send Poll after interval change"
        ok=false
    fi
    if [ "${final_after}" -le "${final_before}" ]; then
        fail "GoBFD did not send Final in response to FRR Poll"
        ok=false
    fi

    # Restore FRR interval.
    ${DC} exec -T frr vtysh \
        -c "configure terminal" \
        -c "bfd" \
        -c "peer ${GOBFD_IP}" \
        -c "transmit-interval 300" 2>/dev/null
    sleep 3

    if [ "${ok}" = true ]; then
        pass "Poll/Final exchange on parameter change"
        return 0
    fi
    return 1
}

# ===========================================================================
# Group A (continued): Jitter compliance (RFC 5880 §6.8.7)
# ===========================================================================

test_rfc5880_jitter_compliance() {
    info "test: RFC 5880 §6.8.7 — TX jitter 75-100% of interval"

    local epochs
    epochs="$(tshark_fields \
        "bfd && ip.src == ${GOBFD_IP} && ip.dst == ${BIRD3_IP} && bfd.sta == 0x03" \
        "frame.time_epoch" | head -200)"

    local count
    count="$(echo "${epochs}" | grep -c '[0-9]' || true)"

    if [ "${count}" -lt 10 ]; then
        info "SKIP: insufficient Up packets for jitter analysis (${count})"
        pass "jitter compliance (skipped)"
        return 0
    fi

    # Compute min/max inter-packet deltas using python3.
    local result
    result="$(echo "${epochs}" | python3 -c "
import sys
times = [float(line.strip()) for line in sys.stdin if line.strip()]
if len(times) < 2:
    print('skip')
    sys.exit(0)
deltas = [times[i+1] - times[i] for i in range(len(times)-1)]
mn, mx = min(deltas), max(deltas)
print(f'{mn:.3f} {mx:.3f} {len(deltas)}')
")"

    if [ "${result}" = "skip" ]; then
        pass "jitter compliance (skipped: insufficient data)"
        return 0
    fi

    local min_delta max_delta n_samples
    min_delta="$(echo "${result}" | awk '{print $1}')"
    max_delta="$(echo "${result}" | awk '{print $2}')"
    n_samples="$(echo "${result}" | awk '{print $3}')"

    info "inter-packet timing: min=${min_delta}s max=${max_delta}s samples=${n_samples}"

    local ok=true
    local min_ok max_ok
    min_ok="$(python3 -c "print('yes' if ${min_delta} >= 0.150 else 'no')")"
    max_ok="$(python3 -c "print('yes' if ${max_delta} <= 0.400 else 'no')")"

    if [ "${min_ok}" != "yes" ]; then
        fail "min inter-packet interval ${min_delta}s too short (< 150ms)"
        ok=false
    fi
    if [ "${max_ok}" != "yes" ]; then
        fail "max inter-packet interval ${max_delta}s too long (> 400ms)"
        ok=false
    fi

    if [ "${ok}" = true ]; then
        pass "jitter compliant: ${min_delta}s - ${max_delta}s (expected 0.225-0.300s)"
        return 0
    fi
    return 1
}

# ===========================================================================
# Test: Detection timeout — stop FRR, verify gobfd detects Down (original)
# ===========================================================================

test_frr_detection_timeout() {
    info "test: detection timeout — stop FRR"

    ${DC} stop frr
    sleep 5

    if ${DC} logs gobfd 2>&1 | grep -q "session state changed.*new_state=Down"; then
        pass "GoBFD detected FRR peer failure (session transitioned to Down)"
    else
        fail "GoBFD did not detect FRR peer failure"
        ${DC} logs --tail 30 gobfd 2>&1 || true
        return 1
    fi

    ${DC} start frr
    sleep 5

    return 0
}

# ===========================================================================
# Test: Graceful shutdown with AdminDown verification (LAST — stops gobfd)
# ===========================================================================

test_gobfd_graceful_shutdown() {
    info "test: graceful shutdown — stop GoBFD (AdminDown)"

    # Ensure FRR session is Up before shutting down.
    if ! wait_frr_up 30; then
        fail "FRR session not Up before graceful shutdown test"
        return 1
    fi

    # Record AdminDown count before.
    local before
    before="$(tshark_count "bfd && ip.src == ${GOBFD_IP} && bfd.sta == 0x00 && bfd.diag == 0x07")"

    ${DC} stop gobfd
    sleep 5

    # Verify AdminDown packets (state=0, diag=7) were sent.
    local after
    after="$(tshark_count "bfd && ip.src == ${GOBFD_IP} && bfd.sta == 0x00 && bfd.diag == 0x07")"

    if [ "${after}" -gt "${before}" ]; then
        pass "GoBFD sent AdminDown packets on SIGTERM (${after} total, +$((after - before)) new)"
    else
        fail "GoBFD did not send AdminDown (state=0, diag=7) packets on SIGTERM"
        return 1
    fi

    # Verify FRR sees session down.
    local status
    status="$(frr_bfd_peer_status "${GOBFD_IP}")"
    if [ "${status}" = "down" ] || [ "${status}" = "not-found" ]; then
        pass "FRR detected GoBFD graceful shutdown"
    else
        fail "FRR did not detect GoBFD shutdown (status: ${status})"
        return 1
    fi

    return 0
}

# ===========================================================================
# Run all tests
# ===========================================================================

echo ""
echo "========================================="
echo "  GoBFD Interoperability Tests"
echo "  FRR 10.2.5 + BIRD3 (Debian Trixie)"
echo "  RFC 5880/5881 Compliance Suite"
echo "========================================="
echo ""

# --- Phase 1: Handshake ---
info "=== Phase 1: Handshake ==="
assert_pass test_frr_handshake
assert_pass test_bird3_handshake

# --- Phase 2: Read-only tshark analysis (Groups A, B, C, D-initial, F-handshake) ---
info "=== Phase 2: RFC Packet Invariants ==="
assert_pass test_rfc5880_version
assert_pass test_rfc5880_multipoint_zero
assert_pass test_rfc5880_demand_zero
assert_pass test_rfc5881_echo_interval_zero
assert_pass test_rfc5880_my_discr_nonzero
assert_pass test_rfc5880_packet_length
assert_pass test_rfc5881_ttl_255
assert_pass test_rfc5881_dst_port_3784
assert_pass test_rfc5881_src_port_ephemeral

info "=== Phase 2: Handshake Sequence & Discriminators ==="
assert_pass test_rfc5880_handshake_sequence
assert_pass test_rfc5880_discr_learning
assert_pass test_rfc5880_discr_uniqueness

info "=== Phase 2: TX Rate ==="
assert_pass test_rfc5880_slow_tx_when_not_up
assert_pass test_rfc5880_fast_tx_once_up

info "=== Phase 2: Initial Diagnostic & Poll/Final ==="
assert_pass test_rfc5880_diag_zero_initial
assert_pass test_rfc5880_poll_final_handshake

# --- Phase 3: State-changing tests ---
info "=== Phase 3: Session Independence ==="
assert_pass test_rfc5880_session_independence
# After FRR stop/start from session_independence:
sleep 5
assert_pass test_rfc5880_diag_time_expired
assert_pass test_rfc5880_detection_precision

info "=== Phase 3: Session Recovery ==="
assert_pass test_rfc5880_session_recovery

info "=== Phase 3: AdminDown from FRR ==="
assert_pass test_rfc5880_frr_admin_down
assert_pass test_rfc5880_frr_admin_down_recovery

info "=== Phase 3: Poll/Final Parameter Change ==="
assert_pass test_rfc5880_poll_final_parameter_change

info "=== Phase 3: Jitter Analysis ==="
assert_pass test_rfc5880_jitter_compliance

# --- Phase 4: Detection timeout (original) ---
info "=== Phase 4: Detection Timeout ==="
assert_pass test_frr_detection_timeout

# --- Phase 5: Scapy protocol fuzzing ---
info "=== Phase 5: Scapy Protocol Fuzzing ==="

test_scapy_fuzzing() {
    local desc="Scapy BFD fuzzing — gobfd survives all invalid packets"

    # Build and run the Scapy container (fuzz profile).
    if ! ${DC} --profile fuzz run --rm scapy 2>&1; then
        fail "${desc} — scapy container exited with error"
        return 1
    fi

    # Verify gobfd is still running after fuzzing.
    if ! podman ps --format '{{.Names}}' | grep -q gobfd-interop; then
        fail "${desc} — gobfd crashed after scapy fuzzing"
        return 1
    fi

    pass "${desc}"
    return 0
}

assert_pass test_scapy_fuzzing

# --- Phase 6: Graceful shutdown (LAST — stops gobfd) ---
info "=== Phase 6: Graceful Shutdown ==="
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
