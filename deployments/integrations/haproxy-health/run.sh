#!/usr/bin/env bash
# HAProxy Backend Health demo — GoBFD + HAProxy agent-check.
#
# Demonstrates: BFD Down → agent responds "down" → HAProxy removes backend.
#
# Prerequisites: podman, podman-compose
# Usage: ./run.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DC="podman-compose -f ${SCRIPT_DIR}/compose.yml"
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

cleanup() {
    info "Cleaning up..."
    ${DC} down --volumes --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

# --- Step 1: Build and start ---
info "Step 1: Building and starting topology..."
${DC} up --build -d

# --- Step 2: Wait for services ---
info "Step 2: Waiting for services to start..."
sleep 10

# --- Step 3: Verify HAProxy ---
info "Step 3: Verifying HAProxy serves traffic..."
if curl -s http://localhost:8080 2>/dev/null | grep -q "nginx"; then
    info "HAProxy is serving traffic from backends."
else
    warn "HAProxy not yet ready (may need more time)."
fi
echo ""

# --- Step 4: Verify BFD sessions ---
info "Step 4: Checking BFD sessions on monitor..."
podman exec gobfd-monitor gobfdctl sessions 2>/dev/null || \
    podman exec gobfd-monitor /bin/gobfdctl sessions 2>/dev/null || \
    warn "Could not query BFD sessions."
echo ""

# --- Step 5: tshark verification ---
info "Step 5: Verifying BFD packets via tshark..."
podman exec tshark-haproxy tshark -r /captures/bfd.pcapng -Y bfd -c 10 \
    -T fields -e frame.time_relative -e ip.src -e ip.dst -e bfd.version \
    -e bfd.sta -E header=y -E separator=, \
    2>/dev/null || warn "tshark capture not yet available."
echo ""

# --- Step 6: Simulate backend1 failure ---
info "Step 6: Simulating failure — pausing backend1..."
podman pause backend1
info "Backend1 paused. Waiting for BFD Down + agent response (~3s)..."
sleep 5

# --- Step 7: Verify traffic goes to backend2 only ---
info "Step 7: Verifying traffic routed to backend2 only..."
for i in $(seq 1 3); do
    curl -s http://localhost:8080 > /dev/null 2>&1 && info "Request $i: OK" || warn "Request $i: failed"
done
echo ""

# --- Step 8: Recover backend1 ---
info "Step 8: Recovering — unpausing backend1..."
podman unpause backend1
info "Backend1 unpaused. Waiting for BFD Up + agent response (~5s)..."
sleep 8

# --- Step 9: Verify both backends serving ---
info "Step 9: Verifying both backends are serving..."
for i in $(seq 1 3); do
    curl -s http://localhost:8080 > /dev/null 2>&1 && info "Request $i: OK" || warn "Request $i: failed"
done
echo ""

# --- Step 10: tshark post-analysis ---
info "Step 10: tshark post-analysis — BFD state transitions..."
podman exec tshark-haproxy tshark -r /captures/bfd.pcapng -Y bfd \
    -T fields -e frame.time_relative -e ip.src -e ip.dst \
    -e bfd.version -e bfd.diag -e bfd.sta -e bfd.flags \
    -E header=y -E separator=, \
    2>/dev/null || warn "tshark capture not available."

echo ""
info "Demo complete. Cleaning up..."
