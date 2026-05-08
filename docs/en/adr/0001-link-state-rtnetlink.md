# ADR-0001: Link-state monitoring via rtnetlink

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

GoBFD must observe Linux interface state changes to translate link-down
events into BFD `Down` transitions before the detection timer expires.
Two candidate APIs exist on Linux:

- `NETLINK_ROUTE` / rtnetlink, which exposes `RTM_NEWLINK` and `RTM_DELLINK`
  messages on the `RTMGRP_LINK` multicast group.
- `github.com/cilium/ebpf`, which loads and attaches eBPF programs for XDP,
  TC, kprobes, tracepoints, perf events, and ring buffers.

Link-state notification is a control-plane state-change problem, not a
packet datapath problem. eBPF is applicable to packet path observation and
filtering, not to interface notification.

## Decision

GoBFD uses Linux rtnetlink for interface link-state notifications.
`github.com/cilium/ebpf` is not part of the link-state monitoring path.

## Consequences

| Item | Requirement |
|---|---|
| Kernel API | rtnetlink exists since Linux 2.2 |
| Distribution support | Current mainstream Linux distributions ship rtnetlink |
| Network namespace | The netlink socket observes the namespace where it is opened |
| Kubernetes/Podman deployment | Host interface monitoring requires the host network namespace |
| Message loss | `ENOBUFS` is a resync signal; the receiver must reload the link table |
| Capabilities | Container profiles must allow netlink group subscription and host-namespace access |
| eBPF | Out of scope for link-state notification; reserved for future packet-path features |

### Linux networking API selection

| Task | Preferred Linux API |
|---|---|
| Interface create/delete/up/down notifications | rtnetlink `RTMGRP_LINK` |
| Interface address changes | rtnetlink address groups |
| Route changes | rtnetlink route groups |
| NIC driver / offload settings | ethtool netlink |
| Packet fast path, filtering, telemetry | TC, XDP, eBPF |
| Legacy one-off device flags | ioctl, only when no netlink API exists |

### Implementation contract

- Linux rtnetlink monitor lives in `internal/netio`.
- The monitor parses `RTM_NEWLINK` and `RTM_DELLINK` and emits
  `InterfaceEvent{IfName, IfIndex, Up}`.
- Operational-up rule: `IFF_UP && IFF_RUNNING`.
- A link-down transition triggers BFD `Down` with `DiagPathDown` before
  the detection timer expires.

## References

- Linux `netlink(7)`: <https://man7.org/linux/man-pages/man7/netlink.7.html>
- Linux `rtnetlink(7)`: <https://man7.org/linux/man-pages/man7/rtnetlink.7.html>
- Linux kernel rt-link specification:
  <https://docs.kernel.org/networking/netlink_spec/rt_link.html>
- `github.com/cilium/ebpf` package documentation:
  <https://pkg.go.dev/github.com/cilium/ebpf>
- Cilium system requirements:
  <https://docs.cilium.io/en/stable/operations/system_requirements/>
