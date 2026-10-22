//go:build package_lifecycle_testcontainers

package packagelifecycle_test

import (
	"fmt"
	"strings"
)

type side struct {
	iface, local, peer, loopback, peerLoopback string
	local6, peer6, localMAC, peerMAC           string
	innerLocal, innerPeer, lagLocal, lagPeer   string
	member1, member2, socket, socketGroup      string
}

func sideA() side {
	return side{
		iface: "v0a", local: "10.10.0.1", peer: "10.10.0.2", loopback: "10.30.0.1", peerLoopback: "10.30.0.2",
		local6: "fd00:10::1", peer6: "fd00:10::2", localMAC: "02:00:00:00:00:01", peerMAC: "02:00:00:00:00:02",
		innerLocal: "192.0.2.1", innerPeer: "192.0.2.2", lagLocal: "10.20.0.1", lagPeer: "10.20.0.2",
		member1: "m1a", member2: "m2a", socket: "/run/gobfd/gobfd.sock", socketGroup: "gobfd-admin",
	}
}

func sideB() side {
	return side{
		iface: "v0b", local: "10.10.0.2", peer: "10.10.0.1", loopback: "10.30.0.2", peerLoopback: "10.30.0.1",
		local6: "fd00:10::2", peer6: "fd00:10::1", localMAC: "02:00:00:00:00:02", peerMAC: "02:00:00:00:00:01",
		innerLocal: "192.0.2.2", innerPeer: "192.0.2.1", lagLocal: "10.20.0.2", lagPeer: "10.20.0.1",
		member1: "m1b", member2: "m2b", socket: "/run/gobfd-peer/gobfd.sock",
	}
}

// configA is the packaged daemon: every transport mode, GoBGP, metrics and
// the management socket. Dropping the IPv6 session exercises reload.
func configA(withIPv6 bool) string {
	sessions := sideA().sessions()
	if withIPv6 {
		sessions += sideA().ipv6Session()
	}
	return sideA().common() + sessions
}

// configB is the unsandboxed peer in the "peer" network namespace. Its
// session to 10.10.0.11 makes the packaged daemon create an unsolicited
// passive session.
func configB() string {
	return sideB().common() + sideB().sessions() + sideB().ipv6Session() + fmt.Sprintf(`  - peer: "10.10.0.11"
    local: %q
    interface: %q
    type: single_hop
`, sideB().local, sideB().iface)
}

func (s side) common() string {
	return strings.NewReplacer(
		"@IF@", s.iface, "@L@", s.local, "@R@", s.peer, "@LM@", s.localMAC, "@RM@", s.peerMAC,
		"@LI@", s.innerLocal, "@RI@", s.innerPeer, "@ML@", s.lagLocal, "@MR@", s.lagPeer,
		"@M1@", s.member1, "@M2@", s.member2, "@SOCK@", s.socket, "@GROUP@", s.socketGroup,
	).Replace(`grpc:
  socket:
    path: "@SOCK@"
    mode: "0660"
    group: "@GROUP@"
metrics:
  addr: "127.0.0.1:9100"
  path: "/metrics"
log:
  level: "debug"
  format: "json"
bfd:
  default_desired_min_tx: "300ms"
  default_required_min_rx: "300ms"
  default_detect_multiplier: 3
unsolicited:
  enabled: true
  max_sessions: 10
  cleanup_timeout: "30s"
  session_defaults:
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
  interfaces:
    @IF@:
      enabled: true
      allowed_prefixes: ["10.10.0.0/24"]
echo:
  enabled: true
  default_tx_interval: "300ms"
  default_detect_multiplier: 3
  peers:
    - peer: "@R@"
      local: "@L@"
      interface: "@IF@"
micro_bfd:
  enabled: true
  actuator:
    mode: "dry-run"
    backend: "kernel-bond"
    owner_policy: "allow-external"
    down_action: "remove-member"
    up_action: "add-member"
  groups:
    - lag_interface: "bond0"
      member_links: ["@M1@", "@M2@"]
      peer_addr: "@MR@"
      local_addr: "@ML@"
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 3
      min_active_links: 1
vxlan:
  enabled: true
  backend: "userspace-udp"
  management_vni: 16777215
  default_desired_min_tx: "300ms"
  default_required_min_rx: "300ms"
  default_detect_multiplier: 3
  peers:
    - peer: "@R@"
      local: "@L@"
      peer_mac: "@RM@"
      local_mac: "@LM@"
geneve:
  enabled: true
  backend: "userspace-udp"
  default_vni: 100
  default_desired_min_tx: "300ms"
  default_required_min_rx: "300ms"
  default_detect_multiplier: 3
  peers:
    - peer: "@R@"
      local: "@L@"
      vni: 200
      inner_peer: "@RI@"
      inner_local: "@LI@"
      peer_mac: "@RM@"
      local_mac: "@LM@"
gobgp:
  enabled: true
  addr: "127.0.0.1:50051"
  strategy: "disable-peer"
`)
}

func (s side) sessions() string {
	return fmt.Sprintf(`sessions:
  - peer: %q
    local: %q
    interface: %q
    type: single_hop
  - peer: %q
    local: %q
    type: multi_hop
    multihop_max_hops: 2
`, s.peer, s.local, s.iface, s.peerLoopback, s.loopback)
}

func (s side) ipv6Session() string {
	return fmt.Sprintf(`  - peer: %q
    local: %q
    interface: %q
    type: single_hop
`, s.peer6, s.local6, s.iface)
}
