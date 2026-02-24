#!/usr/bin/env bash
# SONiC-VS post-deploy configuration.
#
# SONiC does not support startup-config in the same way as other vendors.
# This script is executed inside the SONiC container after deployment.
#
# SONiC-VS (netreplica/docker-sonic-vs) ships bgpd and bfdd as supervisord
# services but they are NOT started by default. They must be started
# explicitly before FRR configuration can be applied.
set -euo pipefail

# Configure data interface.
config interface ip add Ethernet0 10.0.4.2/30
config interface startup Ethernet0

# Start FRR daemons that SONiC-VS does not auto-start.
# bgpd is a supervisord-managed service; bfdd is not in supervisord
# but the binary exists in /usr/lib/frr/.
supervisorctl start bgpd 2>/dev/null || true
if ! pgrep -f "/usr/lib/frr/bfdd" >/dev/null 2>&1; then
    /usr/lib/frr/bfdd -A 127.0.0.1 &
    sleep 1
fi

# Wait for bgpd and bfdd to be ready (accept vtysh commands).
for i in $(seq 1 30); do
    if vtysh -c "show bfd peers brief" >/dev/null 2>&1 && \
       vtysh -c "show bgp summary" >/dev/null 2>&1; then
        break
    fi
    sleep 2
done

# Configure BFD and BGP via individual vtysh -c commands.
# Heredoc-style input to vtysh is unreliable with SONiC's FRR 7.5.1
# when daemons are freshly started.
vtysh \
    -c "configure terminal" \
    -c "ip route 10.20.4.0/24 blackhole" \
    -c "router bgp 65005" \
    -c "bgp router-id 10.0.4.2" \
    -c "neighbor 10.0.4.1 remote-as 65001" \
    -c "neighbor 10.0.4.1 bfd" \
    -c "address-family ipv4 unicast" \
    -c "network 10.20.4.0/24" \
    -c "exit-address-family" \
    -c "exit" \
    -c "bfd" \
    -c "peer 10.0.4.1 interface Ethernet0" \
    -c "detect-multiplier 3" \
    -c "receive-interval 300" \
    -c "transmit-interval 300" \
    -c "no shutdown" \
    -c "exit" \
    -c "exit" \
    -c "end"
