#!/usr/bin/env bash
# Set up a single-node k3s cluster for GoBFD DaemonSet demo.
#
# k3s runs directly on the host (not inside containers), which provides
# real host networking for BFD raw sockets.
#
# Prerequisites:
#   - Root or sudo access
#   - curl
#   - podman (for building GoBFD image)
#
# Usage: ./setup-cluster.sh

set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
KUBECONFIG_PATH="/etc/rancher/k3s/k3s.yaml"

# --- Step 1: Install k3s ---
if command -v k3s &>/dev/null; then
    info "k3s already installed: $(k3s --version | head -1)"
else
    info "Installing k3s..."
    curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--disable=traefik,servicelb --write-kubeconfig-mode=644" sh -
    info "k3s installed."
fi

# --- Step 2: Wait for k3s to be ready ---
info "Waiting for k3s to be ready..."
export KUBECONFIG="${KUBECONFIG_PATH}"

for i in $(seq 1 60); do
    if kubectl get nodes &>/dev/null; then
        break
    fi
    if [ "$i" -eq 60 ]; then
        error "k3s did not become ready within 60s."
        exit 1
    fi
    sleep 1
done

info "k3s cluster ready:"
kubectl get nodes -o wide
echo ""

# --- Step 3: Build GoBFD image ---
info "Building GoBFD container image..."
podman build -f "${REPO_ROOT}/deployments/docker/Containerfile" -t gobfd:local "${REPO_ROOT}"
info "Image built: gobfd:local"

# --- Step 4: Import image into k3s ---
# k3s uses containerd; import via ctr or save/load.
info "Importing GoBFD image into k3s containerd..."
podman save gobfd:local -o /tmp/gobfd-local.tar
k3s ctr images import /tmp/gobfd-local.tar
rm -f /tmp/gobfd-local.tar
info "Image imported into k3s."

# --- Step 5: Verify ---
info "Verifying image in k3s:"
k3s ctr images ls | grep gobfd || warn "Image not found in k3s (may use different tag)."

echo ""
info "Cluster setup complete."
info "KUBECONFIG: ${KUBECONFIG_PATH}"
info "Next: kubectl apply -f ${SCRIPT_DIR}/manifests/"
