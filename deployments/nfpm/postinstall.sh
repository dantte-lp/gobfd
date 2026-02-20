#!/bin/sh
set -e

# Create gobfd system user if it does not exist.
if ! getent passwd gobfd >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /sbin/nologin --user-group gobfd
fi

# Ensure config directory has correct ownership.
chown root:gobfd /etc/gobfd
chmod 750 /etc/gobfd

# Reload systemd to pick up the new unit file.
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
fi
