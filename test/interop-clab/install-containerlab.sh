#!/usr/bin/env bash
# Install containerlab if not already present.
#
# containerlab is a multi-vendor network lab orchestration tool.
# https://containerlab.dev
#
# Usage:
#   ./test/interop-clab/install-containerlab.sh

set -euo pipefail

if command -v containerlab &>/dev/null; then
    echo "containerlab already installed: $(containerlab version 2>/dev/null | head -1)"
    exit 0
fi

echo "installing containerlab..."
bash -c "$(curl -sL https://get.containerlab.dev)"

echo "containerlab installed: $(containerlab version 2>/dev/null | head -1)"
