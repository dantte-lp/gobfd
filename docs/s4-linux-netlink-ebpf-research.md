# S4 Linux Link-State Monitoring Research

## Decision

S4 uses Linux `NETLINK_ROUTE` / rtnetlink for interface link-state
notifications. Do not add `github.com/cilium/ebpf` for this sprint.

## Rationale

`github.com/cilium/ebpf` is the right tool when GoBFD needs to load and attach
eBPF programs to kernel hooks such as XDP, TC, kprobes, tracepoints, perf
events, or ring buffers. Link-state notification is not a packet datapath
problem; Linux already exposes it as rtnetlink `RTM_NEWLINK` / `RTM_DELLINK`
messages over the `RTMGRP_LINK` multicast group.

rtnetlink is part of Linux since 2.2 and is the kernel interface used by
iproute2-class tools for network objects: links, addresses, routes,
neighbours, queueing disciplines, and related state. For GoBFD's target Linux
daemon use case, that makes rtnetlink the lowest-risk and most portable
implementation.

## Distribution and Kernel Notes

- Any currently supported mainstream Linux distribution has rtnetlink support.
  The practical requirement is Linux, not a specific modern kernel feature.
- Network namespaces matter: a netlink socket sees the namespace where it was
  opened. A Kubernetes/Podman deployment that should watch host interfaces must
  run in the host network namespace.
- Netlink delivery from kernel to userspace can drop messages if socket buffers
  overflow. Production code should treat `ENOBUFS` as a resync signal.
- Joining multicast groups can be capability-sensitive. Linux man-pages note
  the general `root` / `CAP_NET_ADMIN` rule, while `NETLINK_ROUTE` groups are
  among the groups that allow non-root receives on modern kernels. Container
  profiles still need validation.
- eBPF adds materially higher deployment requirements: eBPF program loading,
  kernel feature probing, verifier constraints, bpffs for pinned objects, and
  elevated privileges such as `CAP_SYS_ADMIN` in Cilium's documented deployment
  model. That is unjustified for link notifications.

## Correct Linux Networking API Choice

| Task | Preferred Linux API |
|---|---|
| Interface create/delete/up/down notifications | rtnetlink `RTMGRP_LINK` |
| Interface address changes | rtnetlink address groups |
| Route changes | rtnetlink route groups |
| NIC driver/offload settings | ethtool netlink |
| Packet fast path / filtering / telemetry | TC/XDP/eBPF |
| Legacy one-off device flags | ioctl only when no netlink API exists |

## S4 Implementation Scope

- Add a Linux rtnetlink monitor in `internal/netio`.
- Parse `RTM_NEWLINK` and `RTM_DELLINK`.
- Emit `InterfaceEvent{IfName, IfIndex, Up}`.
- Treat `IFF_UP && IFF_RUNNING` as operationally up for BFD purposes.
- On link-down, transition sessions bound to the interface to `Down` with
  `DiagPathDown` before the BFD detection timer expires.

## Sources

- Linux man-pages `netlink(7)`: `NETLINK_ROUTE`, multicast groups, delivery
  limitations, and `RTMGRP_LINK` example.
- Linux man-pages `rtnetlink(7)`: rtnetlink has existed since Linux 2.2.
- Linux kernel `rt-link` netlink specification: `newlink`, `dellink`,
  `rtnlgrp-link`, `ifname`, `operstate`, `carrier`, and related attributes.
- `github.com/cilium/ebpf` README and Context7: library scope is loading,
  compiling, debugging, and attaching eBPF programs; Linux kernel.org LTS
  kernels are tested, with `>= 4.4` expected but EOL kernels unsupported.
- Cilium system requirements: eBPF datapath deployments require host network
  namespace access and elevated privileges for system-wide eBPF programs.
