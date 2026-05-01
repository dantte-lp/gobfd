# S4 Linux Link-State Monitoring Research

## Решение

S4 uses Linux `NETLINK_ROUTE` / rtnetlink for interface link-state
notifications. `github.com/cilium/ebpf` is not part of S4.

## Обоснование

Link-state notification is a control-plane state-change problem, not a packet
datapath problem. Linux exposes interface state changes through rtnetlink
`RTM_NEWLINK` / `RTM_DELLINK` messages over the `RTMGRP_LINK` multicast group.

`github.com/cilium/ebpf` is applicable to eBPF program loading and attachment
for XDP, TC, kprobes, tracepoints, perf events and ring buffers. That scope is
not required for link-state notification.

## Distribution and Kernel Notes

| Item | Requirement |
|---|---|
| Kernel API | rtnetlink exists since Linux 2.2 |
| Distribution support | Current mainstream Linux distributions include rtnetlink |
| Network namespace | The netlink socket observes the namespace where it is opened |
| Kubernetes/Podman deployment | Host interface monitoring requires host network namespace |
| Message loss | `ENOBUFS` is a resync signal |
| Capabilities | Container profiles must validate netlink group subscription and host namespace access |
| eBPF | Program loading introduces verifier, bpffs and elevated capability requirements |

## Linux Networking API Selection

| Task | Preferred Linux API |
|---|---|
| Interface create/delete/up/down notifications | rtnetlink `RTMGRP_LINK` |
| Interface address changes | rtnetlink address groups |
| Route changes | rtnetlink route groups |
| NIC driver/offload settings | ethtool netlink |
| Packet fast path, filtering, telemetry | TC/XDP/eBPF |
| Legacy one-off device flags | ioctl only when no netlink API exists |

## S4 Implementation Scope

- Linux rtnetlink monitor in `internal/netio`.
- `RTM_NEWLINK` and `RTM_DELLINK` parsing.
- `InterfaceEvent{IfName, IfIndex, Up}` emission.
- Operational-up rule: `IFF_UP && IFF_RUNNING`.
- Link-down transition to BFD `Down` with `DiagPathDown` before detection
  timer expiry.

## Official Sources

- Linux `netlink(7)`: <https://man7.org/linux/man-pages/man7/netlink.7.html>
- Linux `rtnetlink(7)`: <https://man7.org/linux/man-pages/man7/rtnetlink.7.html>
- Linux kernel rt-link specification:
  <https://docs.kernel.org/networking/netlink_spec/rt_link.html>
- `github.com/cilium/ebpf` package documentation:
  <https://pkg.go.dev/github.com/cilium/ebpf>
- Cilium system requirements:
  <https://docs.cilium.io/en/stable/operations/system_requirements/>
