#!/usr/bin/env bash
# Kubernetes DaemonSet demo — GoBFD + k3s.
#
# Demonstrates: GoBFD as a DaemonSet with GoBGP sidecar, host networking,
# and inter-node BFD sessions on a k3s cluster.
#
# Prerequisites: root/sudo, curl, podman, kubectl
# Usage: sudo ./run.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KUBECONFIG="/etc/rancher/k3s/k3s.yaml"
export KUBECONFIG

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

# --- Step 1: Set up k3s cluster and import image ---
info "Step 1: Setting up k3s cluster..."
"${SCRIPT_DIR}/setup-cluster.sh"
echo ""

# --- Step 2: Apply manifests ---
info "Step 2: Applying Kubernetes manifests..."
kubectl apply -f "${SCRIPT_DIR}/manifests/"
echo ""

# --- Step 3: Wait for DaemonSet rollout ---
info "Step 3: Waiting for DaemonSet rollout..."
kubectl -n gobfd rollout status daemonset/gobfd --timeout=120s
echo ""

# --- Step 4: Show pods ---
info "Step 4: Listing GoBFD pods..."
kubectl -n gobfd get pods -o wide
echo ""

# --- Step 5: Get node IP ---
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
info "Node IP: ${NODE_IP}"

# --- Step 6: Get pod name ---
POD=$(kubectl -n gobfd get pods -l app=gobfd -o jsonpath='{.items[0].metadata.name}')
info "GoBFD pod: ${POD}"

# --- Step 7: Verify GoBFD is running ---
info "Step 7: Verifying GoBFD daemon..."
kubectl -n gobfd exec "${POD}" -c gobfd -- /bin/gobfdctl sessions 2>/dev/null || \
    warn "No sessions configured yet (single-node cluster)."
echo ""

# --- Step 8: Verify GoBGP sidecar ---
info "Step 8: Verifying GoBGP sidecar..."
kubectl -n gobfd exec "${POD}" -c gobgp -- gobgp global 2>/dev/null || \
    warn "GoBGP not yet ready."
echo ""

# --- Step 9: Check metrics endpoint ---
info "Step 9: Checking Prometheus metrics..."
kubectl -n gobfd exec "${POD}" -c gobfd -- wget -qO- http://127.0.0.1:9100/metrics 2>/dev/null | \
    grep "^gobfd_bfd_" | head -10 || \
    warn "Metrics endpoint not responding."
echo ""

# --- Step 10: tshark capture (ephemeral, if tshark available) ---
info "Step 10: Attempting ephemeral BFD packet capture..."
kubectl -n gobfd exec "${POD}" -c gobfd -- \
    sh -c 'command -v tshark && tshark -i any -c 10 -Y bfd 2>/dev/null' 2>/dev/null || \
    info "tshark not available in GoBFD image (expected — use host tshark for captures)."
echo ""

info "Demo complete."
info ""
info "Single-node notes:"
info "  This demo runs on a single k3s node. In a multi-node cluster,"
info "  BFD sessions would be added between nodes via:"
info "    kubectl exec -n gobfd <pod> -c gobfd -- gobfdctl session add --peer <other-node-ip>"
info "    kubectl exec -n gobfd <pod> -c gobgp -- gobgp neighbor add <other-node-ip> as 65001"
info ""
info "Cleanup: sudo ${SCRIPT_DIR}/teardown.sh"
