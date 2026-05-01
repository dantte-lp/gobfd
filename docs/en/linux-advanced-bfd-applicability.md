# Linux Advanced BFD Applicability

Date: 2026-05-01

Scope: GoBFD applicability on Linux for RFC 7130 Micro-BFD, RFC 8971 VXLAN
BFD, and RFC 9521 Geneve BFD.

## Status

| Mode | Linux fit | Current GoBFD state | Production gap |
|---|---:|---:|---|
| Micro-BFD | High | Per-member sessions, aggregate state, kernel-bond, OVSDB and NetworkManager enforcement paths | Dedicated API/CLI create flows and broader interop matrix |
| VXLAN BFD | High for VTEP/NVE checks | `userspace-udp` VXLAN socket and codec | Owner-specific kernel/OVS/OVN/Cilium/NSX/Calico integrations |
| Geneve BFD | Medium-High for OVN/NSX/OVS | `userspace-udp` Geneve socket and codec | Owner-specific dataplane integrations and rate policy |

GoBFD is a userspace BFD engine. It detects BFD state and executes explicitly
configured enforcement backends. It is not a universal Linux dataplane
controller.

## Micro-BFD on Linux

RFC 7130 defines one independent BFD session per LAG member link. GoBFD
implements:

- one session per configured member link;
- UDP destination port 6784;
- `SO_BINDTODEVICE` on the member interface;
- aggregate state tracking through `MicroBFDGroup`;
- session snapshots for API/CLI/monitoring surfaces;
- guarded actuator policy with disabled, dry-run and enforce modes;
- Linux kernel-bond sysfs backend;
- native OVSDB bonded-port backend;
- NetworkManager D-Bus backend for NM-owned bond profiles;
- OVS CLI fallback type for diagnostics.

Production constraints:

1. Enforcement requires an explicit backend and `owner_policy`.
2. Kernel bonding, OVS and NetworkManager ownership models are mutually
   distinct.
3. Manual operator changes require reconciliation on daemon restart.
4. Teamd and additional switching stacks remain future integrations.

## VXLAN BFD on Linux

RFC 8971 defines BFD over VXLAN with a Management VNI for VTEP/NVE
reachability checks. GoBFD implements:

- outer UDP port 4789;
- VXLAN header validation and Management VNI matching;
- inner Ethernet/IPv4/UDP/BFD packet assembly;
- local demux through `OverlayReceiver`;
- explicit `vxlan.backend: userspace-udp`.

Production constraints:

1. `userspace-udp` owns the local UDP 4789 socket.
2. Kernel VXLAN, OVS, OVN, Cilium, Calico or another dataplane may already own
   UDP 4789 in the target network namespace.
3. Reserved owner backend names must fail closed until implementation and
   interop evidence exist.
4. Startup diagnostics must report socket ownership conflicts with actionable
   errors.

## Geneve BFD on Linux

RFC 9521 defines BFD for point-to-point Geneve tunnels. GoBFD implements:

- outer UDP port 6081;
- Geneve O bit set and C bit clear;
- Protocol Type `0x6558`;
- VNI validation;
- inner Ethernet/IPv4/UDP/BFD packet assembly;
- explicit `geneve.backend: userspace-udp`.

Production constraints:

1. `userspace-udp` owns the local UDP 6081 socket.
2. OVS, OVN, NSX or another Geneve dataplane may already own the same socket.
3. RFC 9521 requires controlled traffic conditions or provisioned BFD rates to
   avoid congestion-driven false failures.
4. Owner-specific backends require separate implementation and interop tests.

## Applicability Matrix

| Scenario | Fit | Required ownership |
|---|---:|---|
| Linux router or NFV appliance with LACP uplinks | High | Kernel bonding, OVS or NetworkManager backend selected explicitly |
| Bare-metal Kubernetes node | Medium | `hostNetwork` and host interface ownership policy |
| EVPN/VXLAN validation endpoint | High | Dedicated Management VNI endpoint or owner-specific VXLAN backend |
| OVN/NSX/Geneve gateway | Medium-High | Dedicated endpoint or owner-specific Geneve backend |
| Interop lab against EOS/FRR/Linux | High | Explicit namespace, socket and interface ownership |
| Generic application host without LAG/overlay ownership | Low | External routing daemon or network device BFD preferred |

## Official Sources

- RFC 7130: <https://datatracker.ietf.org/doc/html/rfc7130>
- RFC 8971: <https://datatracker.ietf.org/doc/html/rfc8971>
- RFC 9521: <https://datatracker.ietf.org/doc/html/rfc9521>
- Linux `netlink(7)`: <https://man7.org/linux/man-pages/man7/netlink.7.html>
- Linux `rtnetlink(7)`: <https://man7.org/linux/man-pages/man7/rtnetlink.7.html>
- Linux kernel bonding documentation:
  <https://docs.kernel.org/networking/bonding.html>
- Open vSwitch OVSDB reference:
  <https://docs.openvswitch.org/en/stable/ref/ovsdb.7/>
- NetworkManager D-Bus API:
  <https://networkmanager.dev/docs/api/latest/spec.html>

## Sprint Impact

| Sprint | Result |
|---|---|
| S6.1 | Documentation aligned with Linux applicability and limits. |
| S7.1a | Micro-BFD actuator hook and guarded LAG actuator policy added. |
| S7.1b | Micro-BFD actuator configuration and daemon dry-run wiring added. |
| S7.1c | Linux kernel-bond sysfs backend added. |
| S7.1d | Transitional OVS CLI bonded-port backend added. |
| S7.1d2 | OVSDB native integration path documented. |
| S7.1e | Native OVSDB bonded-port backend added. |
| S7.1f | NetworkManager D-Bus backend added. |
| S7.2 | Overlay backend model for VXLAN/Geneve dataplane coexistence added. |
| S8 | README, docs indexes and pkg.go.dev release surface aligned to `v0.5.0`. |
