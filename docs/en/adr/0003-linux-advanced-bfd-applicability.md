# ADR-0003: Linux applicability of Micro-BFD, VXLAN BFD, and Geneve BFD

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

Advanced modes interact with Linux resources owned by bonding, OVS, OVN, NetworkManager or an overlay dataplane. Protocol detection does not authorize a second controller to change those resources.

## Decision

GoBFD is a userspace BFD engine with explicitly selected enforcement adapters. Each mode has its own ownership boundary.

| Mode | Implemented profile | Deployment boundary |
|:---|:---|:---|
| Micro-BFD | Member sessions, aggregate state, API/CLI preview and guarded actuators | Dedicated member-wire/MAC and actual-owner qualification remain required |
| VXLAN BFD | Exact IPv4 `userspace-udp` profile | Lab/isolated endpoint owning UDP/4789 |
| Geneve BFD | Exact IPv4 Format A `userspace-udp` profile | Lab/isolated VAP endpoint owning UDP/6081 |

The shared Linux sender requires a fixed source port 49152–65535, including Echo as product policy. TTL/Hop Limit and interface indices use native Linux ancillary byte order; destination IP remains network order. Intended linux/amd64 and linux/arm64 support needs native kernel evidence separately from source correction and cross-compilation. Source: [sender](../../../internal/netio/sender.go), [raw sockets](../../../internal/netio/rawsock.go).

### Micro-BFD on Linux

Each configured member/family has its own Control session on UDP/6784 and member-device binding. `MicroBFDGroup` tracks the Up-member threshold. Actuator modes are disabled, dry-run and enforce; each call runs outside Manager locks with a five-second context.

| Backend | Mutation | Required authority |
|:---|:---|:---|
| Kernel bond | Write `-member`/`+member` to bonding sysfs | Explicit `allow-external` policy |
| OVS | Native OVSDB mutation of `Port.interfaces` | Explicit OVS owner policy |
| NetworkManager | Deactivate/reactivate bond port profiles over D-Bus | Explicit `networkmanager-dbus` policy |
| OVS CLI fallback | Directly constructed diagnostic/fallback adapter | Not selected by daemon configuration |

Backend ownership models remain distinct. Restart reconciliation must account for manual changes. Teamd and additional switching stacks are future integrations. Source: [actuator selection](../../../internal/netio/lag_backend.go), [member wiring](../../../cmd/gobfd/micro_bfd.go), [OVSDB](../../../internal/netio/lag_ovsdb.go), [NetworkManager](../../../internal/netio/lag_networkmanager.go).

### Micro-BFD actuator allocation boundary — accepted 2026-10-29

Preserve the existing zero-allocation packet/FSM/timer budgets and zero-allocation Micro-BFD dispatch without an actuator, including enabled JSON logging. Keep a fresh `context.WithTimeout(parent, 5*time.Second)` and deferred cancellation for every actuator call. The earlier parent deadline and parent cancellation still apply; retained contexts keep their original `Deadline`, `Done`, `Err` and `Value` semantics. Do not pool or reset an exposed context to meet an allocation target.

For the pinned Go 1.27.3 linux/amd64 qualification, the unchanged `Background` plus noop-actuator benchmark has a budget of at most 272 B/op and 4 allocs/op, checked over three runs of at least 200000 iterations each. The noop callback does not read `Done`; reading it, different parents and actual backend work can allocate more. This budget covers that fixture only. Measure backend allocations and enforcement latency separately; a component allocation result does not qualify ownership, wire behavior, scale or convergence.

The single dispatcher calls the actuator outside Manager locks, after broadcasting the current notification. A slow call delays later notifications. Cancellation is cooperative: the five-second context does not forcibly interrupt arbitrary callback or filesystem work and is not an enforcement latency guarantee. Preview, default-disabled and explicit ownership boundaries remain.

Qualification: policy accepted; each final candidate still requires its own measurements and existing runtime gates. Sources: [handoff](../../../internal/bfd/manager_micro.go), [allocation fixtures](../../../internal/bfd/manager_micro_bench_test.go), [retained-context guard](../../../internal/bfd/manager_micro_context_test.go), [Go context contract](https://pkg.go.dev/context), [benchmark boundaries](../12-benchmarks.md).

### NetworkManager trust boundary

Connection setup and property replies obey caller cancellation. The connector uses `DialContext` before godbus authentication, Hello and Unix FD negotiation. Only transport failures try another address; authentication/Hello do not retry another peer. `godbus_no_tcp` rejects TCP/nonce-TCP before DNS.

Nonce input must be a trusted local regular file of exactly 16 bytes. Bounded reads and nonblocking open reject malformed files/pipes; filesystem calls on a stalled mount remain unbounded.

Active/cached profile paths are hints. Reload settings before deactivate, active no-op or cached activate. Accept only the requested bond port; reject malformed/conflicting aliases and deleted/reprofiled cached connections. Check optional interface binding against the resolved device. Resolve UUID controllers through `GetConnectionByUuid` and require the requested bond interface, retaining the deprecated bond-setting fallback. An unbound controller needs an actual active-profile binding on that bond device.

All used RPC replies require exact type and arity. Property replies retain their Variant envelope; UUID lookup requires one nonroot object path; available-profile lists reject root but may be empty; inactive sentinel remains valid; deactivate requires an empty reply. Preserve typed remote/context errors and use `ErrNetworkManagerSettingsInvalid` for malformed values.

Checks and mutations are separate calls. Concurrent administrator edits require coordination, and invalid reply detection cannot undo a mutation already processed remotely. Local private-bus fixtures do not qualify actual ownership or member-wire enforcement. Reply-shape and real-owner qualification remain separate. Sources: [D-Bus adapter](../../../internal/netio/lag_networkmanager.go), [tests](../../../internal/netio/lag_networkmanager_test.go).

### VXLAN and Geneve on Linux

| Property | VXLAN | Geneve |
|:---|:---|:---|
| Outer UDP destination | 4789 | 6081 |
| Envelope | VXLAN I flag, Management VNI | Version 0, O=1, C=0, Protocol `0x6558` |
| Inner profile | Ethernet/IPv4/UDP/BFD, exact endpoint/MAC scope | Ethernet/IPv4 Format A, explicit exact VAP IP/MAC |
| Ownership | Local socket belongs to GoBFD | Local socket belongs to GoBFD |
| Unsupported owners | Kernel/OVS/OVN/Cilium/Calico/NSX backend names fail closed | Same reserved names fail closed |

A kernel or vendor overlay owning the same local address/port/namespace is outside the userspace profile. Missing/special-use Geneve VAP identity, IPv6 inner payload and Format B fail closed. RFC 9521 traffic must have controlled/provisioned rates; congestion can cause false failures. Sources: [backend admission](../../../internal/netio/overlay_backend.go), [inner framing](../../../internal/netio/overlay_inner.go), [RFC 8971](../../rfc/rfc8971.txt), [RFC 9521](../../rfc/rfc9521.txt).

## Consequences

| Scenario | Applicability | Required condition |
|:---|:---|:---|
| Linux router/NFV LACP uplink | Candidate Micro-BFD profile | Explicit kernel/OVS/NM ownership and qualified actuator |
| Bare-metal Kubernetes | Conditional | Host namespace and explicit host-interface authority |
| EVPN/VXLAN validation | Lab endpoint | Isolated Management VNI/socket ownership |
| OVN/NSX/Geneve gateway | Lab endpoint | Explicit IPv4 VAP identity and isolated socket |
| Generic application host | Limited | External routing/device BFD is a separate integration |

No mode is a universal Linux dataplane controller. Local implementation, vendor documentation and socket ownership do not replace actual path/owner qualification. Check the selected [vendor profile](../reference/vendor-bfd.md).

## References

- [RFC 7130](../../rfc/rfc7130.txt), [RFC 8971](../../rfc/rfc8971.txt), [RFC 9521](../../rfc/rfc9521.txt).
- [NetworkManager D-Bus API](https://networkmanager.dev/docs/api/latest/spec.html).
- [RFC coverage](../08-rfc-compliance.md).
