#!/usr/bin/env bash
# ExaBGP Anycast demo — GoBFD + ExaBGP + GoBGP.
#
# Demonstrates: BFD-controlled anycast route announcement/withdrawal.
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

# --- Step 2: Wait for BGP + route ---
info "Step 2: Waiting for BGP established and route announced..."
for i in $(seq 1 30); do
    if podman exec gobgp-anycast gobgp global rib 2>/dev/null | grep -q "198.51.100.1/32"; then
        info "Route 198.51.100.1/32 announced."
        break
    fi
    if [ "$i" -eq 30 ]; then
        warn "Route not yet announced (BGP or BFD may need more time)."
        podman exec gobgp-anycast gobgp neighbor 2>/dev/null || true
    fi
    sleep 1
done
podman exec gobgp-anycast gobgp global rib 2>/dev/null || true
echo ""

# --- Step 3: tshark verification ---
info "Step 3: Verifying BFD packets via tshark..."
sleep 3
podman exec tshark-anycast tshark -r /captures/bfd.pcapng -Y bfd -c 5 \
    -T fields -e frame.time_relative -e ip.src -e ip.dst -e bfd.version \
    -e bfd.sta -E header=y -E separator=, \
    2>/dev/null || warn "tshark capture not yet available."
echo ""

# --- Step 4: Simulate failure ---
info "Step 4: Simulating failure — pausing gobfd..."
podman pause gobfd-anycast
info "GoBFD paused. Waiting for BFD Down + route withdrawal (~5s)..."
sleep 8

# --- Step 5: Verify withdrawal ---
info "Step 5: Verifying route withdrawal..."
podman exec gobgp-anycast gobgp global rib 2>/dev/null || true
if podman exec gobgp-anycast gobgp global rib 2>/dev/null | grep -q "198.51.100.1/32"; then
    warn "Route still present (withdrawal may take longer)."
else
    info "Route 198.51.100.1/32 withdrawn — anycast failover successful!"
fi
echo ""

# --- Step 6: Recover ---
info "Step 6: Recovering — unpausing gobfd..."
podman unpause gobfd-anycast
info "GoBFD unpaused. Waiting for BFD Up + route announcement (~15s)..."
sleep 15

# --- Step 7: Verify restoration ---
info "Step 7: Verifying route restoration..."
podman exec gobgp-anycast gobgp global rib 2>/dev/null || true
if podman exec gobgp-anycast gobgp global rib 2>/dev/null | grep -q "198.51.100.1/32"; then
    info "Route 198.51.100.1/32 restored — recovery successful!"
else
    warn "Route not yet restored (may need more time)."
fi
echo ""

# --- Step 8: tshark post-analysis ---
info "Step 8: tshark post-analysis..."
podman exec tshark-anycast tshark -r /captures/bfd.pcapng -Y bfd \
    -T fields -e frame.time_relative -e ip.src -e ip.dst \
    -e bfd.version -e bfd.diag -e bfd.sta -e bfd.flags \
    -E header=y -E separator=, \
    2>/dev/null || warn "tshark capture not available."

echo ""
info "Demo complete. Cleaning up..."
