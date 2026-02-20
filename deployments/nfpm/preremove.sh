#!/bin/sh
set -e

# Stop and disable the service before package removal.
if command -v systemctl >/dev/null 2>&1; then
    systemctl stop gobfd 2>/dev/null || true
    systemctl disable gobfd 2>/dev/null || true
    systemctl daemon-reload
fi
