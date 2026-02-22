#!/usr/bin/env bash
# Tear down the k3s cluster and GoBFD DaemonSet.
#
# Usage: sudo ./teardown.sh

set -euo pipefail

GREEN='\033[0;32m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC}  $*"; }

KUBECONFIG="/etc/rancher/k3s/k3s.yaml"
export KUBECONFIG

# Delete GoBFD namespace (removes all resources).
if kubectl get namespace gobfd &>/dev/null; then
    info "Deleting gobfd namespace..."
    kubectl delete namespace gobfd --timeout=30s 2>/dev/null || true
fi

# Uninstall k3s.
if [ -x /usr/local/bin/k3s-uninstall.sh ]; then
    info "Uninstalling k3s..."
    /usr/local/bin/k3s-uninstall.sh
    info "k3s uninstalled."
else
    info "k3s uninstall script not found (already removed or not installed)."
fi

info "Teardown complete."
