# Linux Advanced BFD Applicability

Date: 2026-05-01

Scope: applicability of GoBFD on Linux for RFC 7130 Micro-BFD, RFC 8971
VXLAN BFD, and RFC 9521 Geneve BFD after the S4 netlink/eBPF research.

## Summary

GoBFD is applicable on Linux for all three advanced BFD families, but the
production model is different for each family:

| Mode | Linux fit | Current GoBFD state | Production gap |
|---|---:|---:|---|
| Micro-BFD | High | Per-member sessions and aggregate state | LAG actuator |
| VXLAN BFD | High for VTEP/NVE checks | Userspace VXLAN socket and codec | Dataplane coexistence |
| Geneve BFD | Medium-High for OVN/NSX/OVS | Userspace Geneve socket and codec | Dataplane coexistence and rate policy |

The current implementation is a userspace BFD engine. It can detect and report
session state, but it should not be documented as a complete Linux dataplane
controller until the actuator/backend work is implemented.

## Micro-BFD on Linux

RFC 7130 runs an independent BFD session on every LAG member link. GoBFD
implements the protocol side with:

- one session per configured member link;
- UDP destination port 6784;
- `SO_BINDTODEVICE` on the member interface;
- aggregate state tracking through `MicroBFDGroup`;
- session snapshots for API/CLI/monitoring surfaces.

This is enough for detection, alerting, and policy decisions. It is not enough
for full Linux enforcement because RFC 7130 also requires a failed member link
to be removed from the LAG load-balancing table. Linux bonding, team, and OVS
do not automatically consume GoBFD state today.

Required production work:

1. Add an explicit actuator policy: `detect-only`, `disable-member`,
   `remove-member`, and `external-hook`.
2. Implement a Linux bond/team backend with rtnetlink/sysfs reconciliation.
3. Add an OVS backend for environments where LAG ownership is in OVS.
4. Add rollback and resync logic for daemon restart, missed netlink events, and
   manual operator changes.

## VXLAN BFD on Linux

RFC 8971 uses a Management VNI to monitor reachability between VTEPs/NVEs.
GoBFD implements the packet path with:

- outer UDP port 4789;
- VXLAN header validation and Management VNI matching;
- inner Ethernet/IPv4/UDP/BFD packet assembly;
- local demux through `OverlayReceiver`.

This is suitable when GoBFD owns the VXLAN socket, such as a lab endpoint, a
dedicated management endpoint, or a purpose-built Linux VTEP.

The production risk is socket ownership. Kernel VXLAN, OVS, Cilium, or another
dataplane can already own UDP 4789 in the same network namespace and local
address. In that case a userspace GoBFD socket may fail to bind or may not see
the packets that the dataplane consumes.

Required production work:

1. Document socket ownership as a deployment precondition.
2. Add startup diagnostics for UDP 4789 bind conflicts with actionable errors.
3. Design a pluggable overlay backend: userspace socket, kernel/AF_PACKET,
   OVS/OVN integration, or eBPF/XDP only for a proven packet-fast-path need.
4. Add interop tests with Linux kernel VXLAN and OVS.

## Geneve BFD on Linux

RFC 9521 monitors point-to-point Geneve tunnels. GoBFD implements the Format A
Ethernet payload path with:

- outer UDP port 6081;
- Geneve O bit set and C bit clear;
- Protocol Type 0x6558;
- VNI validation;
- inner Ethernet/IPv4/UDP/BFD packet assembly.

This is applicable to OVN, NSX, OVS, and cloud overlay environments where
Geneve is the real tenant dataplane. It is less universal than VXLAN, but it is
important for modern virtualized networks.

The production risks are similar to VXLAN, with one additional RFC constraint:
Geneve BFD should run in a traffic-managed controlled environment or otherwise
have provisioned BFD transmit rates to avoid congestion-driven false failures.

Required production work:

1. Reuse the overlay backend model from VXLAN.
2. Add rate-policy documentation and config validation for aggressive timers.
3. Add interop tests with Linux Geneve and OVS/OVN.

## Applicability Scenarios

| Scenario | Fit | Notes |
|---|---:|---|
| Linux router or NFV appliance with LACP uplinks | High | Micro-BFD detects per-member blackholes faster than LACP. |
| Bare-metal Kubernetes/RKE2 node | Medium | Useful as a hostNetwork daemon for uplink and peer monitoring; not a CNI dataplane replacement. |
| EVPN/VXLAN validation endpoint | High | VXLAN BFD can verify VTEP-to-VTEP reachability through a Management VNI. |
| OVN/NSX/Geneve gateway | Medium-High | Geneve BFD is useful when Geneve is the real overlay transport. |
| Interop lab against EOS/FRR/Linux | High | Strong fit for packet capture, RFC validation, and failure-drill automation. |
| Generic application host without LAG/overlay ownership | Low | Prefer network device or routing daemon BFD. |

## MCP and Source Notes

- Arista MCP confirms EOS supports BFD per-link and RFC 7130 mode where member
  ports are removed from a port channel when micro sessions are Down; EOS also
  exposes `RFC7130` and `Vxlan` session types in BFD show output.
- Context7 for `github.com/cilium/ebpf` confirms the library is for loading,
  compiling, debugging, and attaching eBPF programs; it is not the right
  default for link-state notification, where rtnetlink is the Linux API.
- Linux `ip-link(8)` documents `bond`, `vxlan`, and `geneve` link types, which
  confirms the kernel has the required network object primitives.
- Linux kernel bonding docs confirm 802.3ad support and link monitoring, but
  not native consumption of external BFD state.

## Sprint Impact

| Sprint | Result |
|---|---|
| S6.1 | Align documentation with the actual Linux applicability and limits. |
| S7.1a | Add Micro-BFD actuator hook and guarded LAG actuator policy. |
| S7.1b | Add Micro-BFD actuator configuration and daemon dry-run wiring. |
| S7.1c | Add Linux kernel-bond sysfs backend implementation. |
| S7.1d | Add OVS bonded-port backend implementation. |
| S7.1e | Add optional NetworkManager D-Bus backend implementation. |
| S7.2 | Add overlay backend model for VXLAN/Geneve dataplane coexistence. |
| S8 | Ensure README and pkg.go.dev do not overclaim production readiness before release. |
