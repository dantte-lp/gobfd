#!/usr/bin/env bash
# Observability demo — GoBFD + Prometheus + Grafana.
#
# Demonstrates: metrics collection, alerting rules, Grafana dashboard.
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

# --- Step 2: Wait for BFD session ---
info "Step 2: Waiting for BFD session to establish..."
sleep 5

# --- Step 3: Verify Prometheus targets ---
info "Step 3: Checking Prometheus targets..."
for i in $(seq 1 20); do
    if curl -s http://localhost:9090/api/v1/targets 2>/dev/null | grep -q '"health":"up"'; then
        info "Prometheus is scraping GoBFD metrics."
        break
    fi
    if [ "$i" -eq 20 ]; then
        warn "Prometheus target not yet healthy (may need more time)."
    fi
    sleep 1
done

# --- Step 4: Verify metrics ---
info "Step 4: Querying GoBFD metrics from Prometheus..."
curl -s 'http://localhost:9090/api/v1/query?query=gobfd_bfd_sessions' 2>/dev/null | \
    python3 -m json.tool 2>/dev/null || \
    curl -s 'http://localhost:9090/api/v1/query?query=gobfd_bfd_sessions' 2>/dev/null || \
    warn "Could not query Prometheus API."
echo ""

# --- Step 5: tshark verification ---
info "Step 5: Verifying BFD packets via tshark..."
sleep 3
podman exec tshark-observability tshark -r /captures/bfd.pcapng -Y bfd -c 5 \
    -T fields -e frame.time_relative -e ip.src -e ip.dst -e bfd.version \
    -e bfd.sta -E header=y -E separator=, \
    2>/dev/null || warn "tshark capture not yet available."
echo ""

# --- Step 6: Trigger BFDSessionDown alert ---
info "Step 6: Stopping FRR to trigger BFDSessionDown alert..."
podman stop frr-observability
info "FRR stopped. Waiting 30s for alert to fire..."
sleep 30

info "Checking active alerts..."
curl -s 'http://localhost:9090/api/v1/alerts' 2>/dev/null | \
    python3 -m json.tool 2>/dev/null || \
    curl -s 'http://localhost:9090/api/v1/alerts' 2>/dev/null || \
    warn "Could not query Prometheus alerts API."
echo ""

# --- Step 7: Recover ---
info "Step 7: Starting FRR to resolve alert..."
podman start frr-observability
info "FRR started. Waiting 15s for recovery..."
sleep 15

info "Checking alerts after recovery..."
curl -s 'http://localhost:9090/api/v1/alerts' 2>/dev/null | \
    python3 -m json.tool 2>/dev/null || true
echo ""

# --- Step 8: tshark post-analysis ---
info "Step 8: tshark post-analysis..."
podman exec tshark-observability tshark -r /captures/bfd.pcapng -Y bfd \
    -T fields -e frame.time_relative -e ip.src -e ip.dst \
    -e bfd.version -e bfd.diag -e bfd.sta -e bfd.flags \
    -E header=y -E separator=, \
    2>/dev/null || warn "tshark capture not available."

echo ""
info "Grafana dashboard: http://localhost:3000 (admin/admin)"
info "Prometheus: http://localhost:9090"
info "Demo complete. Cleaning up..."
