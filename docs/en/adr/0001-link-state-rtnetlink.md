# ADR-0001: Link-state monitoring via rtnetlink

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

Interface state is a control-plane input to BFD. A link-down notification should request local PathDown before normal detection expiry, subject to event delivery and owner scheduling. Packet tracing does not supply interface lifecycle ownership.

## Decision

Use Linux `NETLINK_ROUTE` notifications in `internal/netio`. Parse `RTM_NEWLINK`/`RTM_DELLINK` into `InterfaceEvent{IfName, IfIndex, Up}`; operational Up is `IFF_UP && IFF_RUNNING`. Subscribe to IPv4/IPv6 address changes for pending-resource retries. Do not introduce `cilium/ebpf` into link-state monitoring.

| Input | Required behavior | Source |
|:---|:---|:---|
| Link down/delete | Request Down/5 without waiting for Detection Time | [interface_events.go](../../../cmd/gobfd/interface_events.go) |
| Existing local AdminDown | Retain AdminDown/7 | [session_loop.go](../../../internal/bfd/session_loop.go) |
| Address change | Retry pending resource classes without link side effects | [interface_events.go](../../../cmd/gobfd/interface_events.go) |
| Kernel/channel loss | Request full inventory resync | [ifmon_linux.go](../../../internal/netio/ifmon_linux.go) |
| Cancellation | Close monitor socket and owner-owned event channel | [ifmon_linux.go](../../../internal/netio/ifmon_linux.go) |

## Consequences

The socket observes its network namespace; host-interface observation requires the host namespace. Deployment policy must permit that access and netlink subscription. A PathDown request can lose a race to timer expiry; no earlier commit is guaranteed.

Use rtnetlink for link, address and route notifications; reserve TC/XDP/eBPF for independently designed packet-path features. The non-Linux stub provides no automatic retry events. Validate resync and interface recreation with [monitor tests](../../../internal/netio/ifmon_linux_test.go) and runtime reconciliation evidence.

## References

- [Monitor implementation](../../../internal/netio/ifmon_linux.go).
- [Linux rtnetlink API](https://man7.org/linux/man-pages/man7/rtnetlink.7.html).
- [Architecture recovery boundary](../01-architecture.md#recovery-and-readiness).
