# ADR-0003: Linux applicability of Micro-BFD, VXLAN BFD, and Geneve BFD

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

GoBFD ships userspace Micro-BFD paths and incomplete preview paths for RFC
8971 BFD for VXLAN and RFC 9521 BFD for Geneve. Each mode has distinct
ownership constraints on Linux: kernel bonding, OVS, OVN, NetworkManager,
Cilium, Calico, and NSX may already own the relevant device or socket.
A single declarative statement of applicability and required ownership
prevents misconfiguration and incorrect production claims.

## Decision

GoBFD is a userspace BFD engine. It detects BFD state and executes
explicitly configured enforcement backends. It is not a universal Linux
dataplane controller. Each advanced mode has a fixed applicability and a
fixed set of production constraints listed in this record.

The shared Linux UDP sender requires a fixed source port in 49152-65535,
including Micro-BFD and Echo callers; the Echo restriction is product policy,
not an additional RFC requirement. TTL/Hop Limit and packet-info interface
indices use the native Linux ancillary byte order, while destination addresses
retain network order. Intended `linux/amd64` and `linux/arm64` support requires
native kernel evidence for each architecture (`gobfd-qj0.8.2.1.16.2`), separate
from source correction (`gobfd-qj0.8.2.1.16.1`) and cross-compilation.

## Consequences

### Status matrix

| Mode | Linux fit | Current GoBFD state | Production gap |
|---|---:|---:|---|
| Micro-BFD | High | Per-member sessions, aggregate state, API/CLI preview and enforcement backend paths | Dedicated multicast MAC/member-wire qualification and owner/backend interoperability |
| VXLAN BFD | Lab or isolated endpoint only | Exact IPv4 Format A `userspace-udp` preview | Owner-specific kernel/OVS/OVN/Cilium/Calico/NSX integrations |
| Geneve BFD | Lab or isolated endpoint only | Exact IPv4 Format A `userspace-udp` preview | Owner-specific dataplane integrations and rate policy |

### Micro-BFD on Linux

RFC 7130 defines one independent BFD session per LAG member link. GoBFD
implements:

- one session per configured member link;
- UDP destination port 6784;
- `SO_BINDTODEVICE` on the member interface;
- aggregate state tracking through `MicroBFDGroup`;
- session snapshots for API/CLI/monitoring surfaces;
- guarded actuator policy with `disabled`, `dry-run`, and `enforce` modes;
- Linux kernel-bond sysfs backend;
- native OVSDB bonded-port backend;
- NetworkManager D-Bus backend for NM-owned bond profiles;
- OVS CLI fallback type for diagnostics.

Production constraints:

1. Enforcement requires an explicit backend and `owner_policy`.
2. Kernel bonding, OVS, and NetworkManager ownership models are mutually
   distinct.
3. Manual operator changes require reconciliation on daemon restart.
4. Teamd and additional switching stacks remain future integrations.
5. Linux NetworkManager setup and property reply waits use the caller's context.
   Manager assigns a 5 s context to each actuator call. The connector uses
   `net.Dialer.DialContext` before godbus takes ownership, then retains godbus
   authentication, Hello and Unix FD negotiation. `WithContext` alone cannot
   cancel the initial godbus transport dial. Address selection follows godbus
   v5.2.4: only transport failures try another address; authentication and Hello
   never retry against another peer. `godbus_no_tcp` disables TCP and nonce-TCP
   before DNS. Nonce files must be trusted local regular files of exactly 16
   bytes; bounded reads and nonblocking open reject malformed files and pipes,
   but cannot bound filesystem calls on a stalled mount. Local private-bus
   cancellation checks do not qualify real owner interoperability or member wire.
6. Active and cached NetworkManager profile paths are hints, not ownership
   proof. Reload settings before deactivate, active no-op or cached activate;
   accept only the requested bond port. Reject malformed or conflicting aliases
   and reprofiled/deleted cached connections. A port's optional interface binding
   is checked against the explicitly resolved device. Resolve UUID controllers
   through `GetConnectionByUuid` and require a bond profile with the requested
   interface name, including the deprecated bond-setting fallback. An unbound
   controller requires an actual active-profile binding on the requested bond
   device. The new UUID reply decoder requires one nonroot object path and
   preserves remote/context errors; `Store` permits numeric/string coercion.
   Reply qualification is tracked separately in `gobfd-qj0.8.2.2.11`: all used
   RPC adapters share exact type/arity decoding, property reads retain their
   Variant envelope, available profile paths reject root, and the inactive
   property sentinel and empty available list remain valid. Deactivation
   requires an empty reply. Typed remote/context errors are preserved;
   malformed values return `ErrNetworkManagerSettingsInvalid`. Validating a
   reply does not undo a mutation already processed by the peer.
   Checks and mutations use separate D-Bus calls;
   concurrent administrator edits require external coordination. Local fixtures
   do not qualify actual NetworkManager ownership or member-wire enforcement.

### VXLAN BFD on Linux

RFC 8971 defines BFD over VXLAN with a Management VNI for VTEP/NVE
reachability checks. GoBFD implements:

- outer UDP port 4789;
- VXLAN header validation and Management VNI matching;
- inner Ethernet/IPv4/UDP/BFD packet assembly;
- local demux through `OverlayReceiver`;
- explicit `vxlan.backend: userspace-udp`.

Production constraints:

1. `userspace-udp` owns the local UDP 4789 socket.
2. Kernel VXLAN, OVS, OVN, Cilium, Calico, or another dataplane may
   already own UDP 4789 in the target network namespace.
3. Reserved owner backend names must fail closed until implementation
   and interop evidence exist.
4. Startup diagnostics must report socket ownership conflicts with
   actionable errors.
5. The supported IPv4 Format A identity is validated exactly; this path remains
   unqualified because owner-specific dataplane integration is incomplete.

### Geneve BFD on Linux

RFC 9521 defines BFD for point-to-point Geneve tunnels. GoBFD implements:

- outer UDP port 6081;
- Geneve `O` bit set and `C` bit clear;
- Protocol Type `0x6558`;
- VNI validation;
- inner Ethernet/IPv4/UDP/BFD packet assembly;
- explicit `geneve.backend: userspace-udp`.

Production constraints:

1. `userspace-udp` owns the local UDP 6081 socket.
2. OVS, OVN, NSX, or another Geneve dataplane may already own the same
   socket.
3. RFC 9521 requires controlled traffic conditions or provisioned BFD
   rates to avoid congestion-driven false failures.
4. Owner-specific backends require separate implementation and interop
   tests.
5. Explicit IPv4 Format A VAP identity is required and matched exactly;
   unsupported formats fail closed, and owner-specific integration is incomplete.

### Applicability matrix

| Scenario | Fit | Required ownership |
|---|---:|---|
| Linux router or NFV appliance with LACP uplinks | High | Kernel bonding, OVS, or NetworkManager backend selected explicitly |
| Bare-metal Kubernetes node | Medium | `hostNetwork` and host interface ownership policy |
| EVPN/VXLAN validation endpoint | Lab only | Isolated Management VNI endpoint until owner-specific dataplane integration exists |
| OVN/NSX/Geneve gateway | Lab only | Explicit Format A VAP identity and isolated socket ownership |
| Interop lab against EOS/FRR/Linux | High | Explicit namespace, socket, and interface ownership |
| Generic application host without LAG/overlay ownership | Low | External routing daemon or network device BFD preferred |

## References

- [Micro-BFD configuration and member wiring](../../../cmd/gobfd/micro_bfd.go)
- [Actuator backend ownership checks](../../../internal/netio/lag_backend.go)
- [Overlay backend admission](../../../internal/netio/overlay_backend.go)
- [Exact supported inner-packet profile](../../../internal/netio/overlay_inner.go)
- RFC 7130: <https://datatracker.ietf.org/doc/html/rfc7130>
- RFC 8971: <https://datatracker.ietf.org/doc/html/rfc8971>
- RFC 9521: <https://datatracker.ietf.org/doc/html/rfc9521>
- Linux `netlink(7)`: <https://man7.org/linux/man-pages/man7/netlink.7.html>
- Linux `rtnetlink(7)`:
  <https://man7.org/linux/man-pages/man7/rtnetlink.7.html>
- Linux kernel bonding documentation:
  <https://docs.kernel.org/networking/bonding.html>
- Open vSwitch OVSDB reference:
  <https://docs.openvswitch.org/en/stable/ref/ovsdb.7/>
- NetworkManager D-Bus API:
  <https://networkmanager.dev/docs/api/latest/spec.html>
- NetworkManager profile settings and UUID lookup:
  <https://networkmanager.dev/docs/api/latest/nm-settings-dbus.html>,
  <https://networkmanager.dev/docs/api/latest/gdbus-org.freedesktop.NetworkManager.Settings.html>
