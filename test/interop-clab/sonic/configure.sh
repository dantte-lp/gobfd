#!/usr/bin/env bash
# SONiC-VS post-deploy configuration.
#
# SONiC does not support startup-config in the same way as other vendors.
# This script is executed inside the SONiC container after deployment.
set -euo pipefail

# Configure data interface.
config interface ip add Ethernet0 10.0.4.2/30
config interface startup Ethernet0

# Wait for FRR to be ready.
for i in $(seq 1 30); do
    if vtysh -c "show version" >/dev/null 2>&1; then
        break
    fi
    sleep 2
done

# Configure BFD and BGP via FRR vtysh.
vtysh <<'VTYSH_EOF'
configure terminal

bfd
 peer 10.0.4.1 interface Ethernet0
  detect-multiplier 3
  receive-interval 300
  transmit-interval 300
  no shutdown
 exit
exit

ip route 10.20.4.0/24 blackhole

router bgp 65005
 bgp router-id 10.0.4.2
 no bgp ebgp-requires-policy
 neighbor 10.0.4.1 remote-as 65001
 neighbor 10.0.4.1 bfd
 address-family ipv4 unicast
  network 10.20.4.0/24
 exit-address-family
exit

end
VTYSH_EOF
