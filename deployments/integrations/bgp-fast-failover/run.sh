#!/usr/bin/env bash
# BGP Fast Failover demo — GoBFD + GoBGP + FRR.
#
# Demonstrates: BFD Down → BGP peer disabled → routes withdrawn → recovery.
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

# --- Step 2: Wait for BGP established ---
info "Step 2: Waiting for BGP session to establish..."
for i in $(seq 1 30); do
    if podman exec gobgp-bgp-failover gobgp neighbor 2>/dev/null | grep -q "Establ"; then
        info "BGP session established."
        break
    fi
    if [ "$i" -eq 30 ]; then
        error "BGP session did not establish within 30s."
        podman exec gobgp-bgp-failover gobgp neighbor 2>/dev/null || true
        exit 1
    fi
    sleep 1
done

# --- Step 3: Verify routes ---
info "Step 3: Verifying routes in GoBGP RIB..."
podman exec gobgp-bgp-failover gobgp global rib
echo ""
if podman exec gobgp-bgp-failover gobgp global rib 2>/dev/null | grep -q "10.20.0.0/24"; then
    info "Route 10.20.0.0/24 present in RIB."
else
    error "Route 10.20.0.0/24 NOT found in RIB."
    exit 1
fi

# --- Step 4: tshark verification ---
info "Step 4: Verifying BFD packets via tshark..."
sleep 3
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd -c 5 \
    -T fields -e frame.time_relative -e ip.src -e ip.dst -e bfd.version \
    -e bfd.sta -e bfd.desired_min_tx_interval -E header=y -E separator=, \
    2>/dev/null || warn "tshark capture not yet available."
echo ""

# --- Step 5: Simulate failure (pause FRR) ---
info "Step 5: Simulating failure — pausing FRR container..."
podman pause frr-bgp-failover
info "FRR paused. Waiting for BFD Down + BGP teardown (~3s)..."
sleep 5

# --- Step 6: Verify route withdrawal ---
info "Step 6: Verifying route withdrawal..."
podman exec gobgp-bgp-failover gobgp global rib
echo ""
if podman exec gobgp-bgp-failover gobgp global rib 2>/dev/null | grep -q "10.20.0.0/24"; then
    error "Route 10.20.0.0/24 still present — failover did NOT work!"
    exit 1
else
    info "Route 10.20.0.0/24 withdrawn — failover successful!"
fi

# Check BGP neighbor state.
info "BGP neighbor state after failure:"
podman exec gobgp-bgp-failover gobgp neighbor 2>/dev/null || true
echo ""

# --- Step 7: Recover (unpause FRR) ---
info "Step 7: Recovering — unpausing FRR container..."
podman unpause frr-bgp-failover
info "FRR unpaused. Waiting for BFD Up + BGP re-establishment (~10s)..."
sleep 15

# --- Step 8: Verify restoration ---
info "Step 8: Verifying route restoration..."
podman exec gobgp-bgp-failover gobgp global rib
echo ""
if podman exec gobgp-bgp-failover gobgp global rib 2>/dev/null | grep -q "10.20.0.0/24"; then
    info "Route 10.20.0.0/24 restored — recovery successful!"
else
    warn "Route 10.20.0.0/24 not yet restored (BGP may need more time)."
fi

info "BGP neighbor state after recovery:"
podman exec gobgp-bgp-failover gobgp neighbor 2>/dev/null || true
echo ""

# --- Step 9: tshark post-analysis ---
info "Step 9: tshark post-analysis — BFD state transitions..."
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd \
    -T fields -e frame.time_relative -e ip.src -e ip.dst \
    -e bfd.version -e bfd.diag -e bfd.sta -e bfd.flags \
    -e bfd.detect_time_multiplier -e bfd.desired_min_tx_interval \
    -e bfd.required_min_rx_interval -E header=y -E separator=, \
    2>/dev/null || warn "tshark capture not available."

echo ""
info "Demo complete. Cleaning up..."
