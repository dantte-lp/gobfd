# cilium/ebpf evaluation

Date: 2026-10-15. Status: decided — not integrated in v1; post-v1 backlog
issue `gobfd-376` (P4) gated on the latency baseline. Scope: whether
[cilium/ebpf](https://github.com/cilium/ebpf) (ebpf-go) should be used in
GoBFD, where, and when.

## Library facts

Measured on 2026-10-15 with `gh api graphql`, `proxy.golang.org` and
context7 (`/cilium/ebpf`):

- Repository active (last push 2026-10-12), not archived, MIT licence.
  Latest release and module version v0.22.0 (GitHub release 2026-06-29;
  module time 2026-06-26). Previous releases: v0.21.0 (2026-03-05), v0.20.0
  (2025-10-31), v0.19.0 (2025-06-26). The module is pre-1.0.
- Requirements (README): a Go version supported upstream; Linux amd64/arm64
  with CI on kernel.org LTS releases, ">= 4.4 should work"; 32-bit
  architectures unsupported.
- Programs are written in C and compiled with `bpf2go` (needs clang/LLVM,
  libbpf and kernel headers at build time), or assembled in Go with the
  `asm` package without a C compiler. The `link` package attaches XDP
  (`AttachXDP`, generic/driver/offload modes) and other hooks. The README
  read through context7 is from `main`, not the `v0.22.0` tag.

## Feasibility

Integration is technically possible:

- `v0.22.0` `go.mod` (`proxy.golang.org`) declares `go 1.25.0`; GoBFD builds
  with `go 1.27` and `toolchain go1.27.2`.
- Direct requirements: `golang.org/x/sys v0.43.0` (GoBFD already requires
  `v0.49.0`, which minimal version selection keeps), `golang.org/x/sync`,
  `github.com/jsimonetti/rtnetlink/v2`, `github.com/google/go-cmp` and
  `github.com/go-quicktest/qt`. Its `tool` directives (bpf2go, crane,
  staticcheck) do not become GoBFD build dependencies.
- context7 describes the library as pure Go; GoBFD's `CGO_ENABLED=0`
  builds stay valid (not measured with a build).
- The daemon already runs with `CAP_NET_RAW` and `CAP_NET_ADMIN`
  (`deployments/systemd/gobfd.service`). Loading programs additionally needs
  `CAP_BPF` (capabilities(7): since Linux 5.8, split out of
  `CAP_SYS_ADMIN`), the `bpf()` syscall in the filter that
  `gobfd-qj0.8.2.3.3` is narrowing, and `AF_XDP` among allowed address
  families for an AF_XDP design.

Release notes read across v0.19.0 to v0.22.0:

| Release | Relevant content |
|---|---|
| v0.22.0 | Linux 7.1 compatibility: older versions fail to parse 7.1 vmlinux BTF; BPF token consumption (delegated BPF permissions without `CAP_SYS_ADMIN`); breaking removal of the global BTF cache |
| v0.21.0 | Breaking: XDP programs now carry the `AttachXDP` attach type (Linux 6.18 forbids mixing attach types in a program array); removal of long-deprecated APIs; breaking changes to `Memory`, `Variable`, link info and BTF APIs |
| v0.20.0 | Memory-mapped vmlinux BTF, ring buffer and BTF race fixes, arena maps preview |
| v0.19.0 | Lazy BTF decoding, CO-RE against all kernel modules, LLVM 14/17/20 toolchain (LLVM 11 dropped) |

No release in the range lists a security advisory.

## GoBFD facts

- Receive path: UDP sockets read one packet per `ReadMsgUDP` call; GTSM
  (TTL/Hop Limit 255) is checked in userspace from `IP_RECVTTL` /
  `IPV6_RECVHOPLIMIT` ancillary data (`internal/netio/rawsock_linux.go`).
- No end-to-end latency baseline exists: the benchmarks cover processing
  stages only (`BENCHMARKS.md`). `gobfd-qj0.8.2.1.6` owns the committed
  latency baseline and already keeps `recvmmsg` as a separate prototype
  "until the baseline identifies syscall pressure".
- RFC 9747 Echo needs no reflector on the remote: "the one-hop-away BFD
  peer device B immediately loops them back by normal IP forwarding" and
  "would not ... parse any BFD protocol field" (RFC 9747 Section 2,
  `docs/rfc/rfc9747.txt`). The `echo-reflector` container is a lab tool.
- GoBFD documents dataplane ownership limits for VXLAN/Geneve userspace
  sockets next to Cilium, OVS and kernel dataplanes; an XDP program on the
  same interface has the same ownership problem.
- Hardening work in progress narrows the daemon's syscalls, address
  families and file descriptors (`gobfd-qj0.8.2.3.3`); it does not change
  the granted capabilities.

## Candidate uses

| Use | Benefit | Cost and risk | Verdict |
|---|---|---|---|
| XDP or AF_XDP receive fast path for BFD control packets | Lower receive latency and syscall load at high session counts | New `CAP_BPF` (or `CAP_SYS_ADMIN` before Linux 5.8), `bpf()` and `AF_XDP` allowed in the hardened unit, driver-dependent XDP modes, conflicts with Cilium or other XDP owners on the interface, BPF build toolchain, verifier maintenance; no measured bottleneck | Post-v1 research only, after the baseline |
| eBPF socket filter (`SO_ATTACH_BPF`) to drop non-BFD or non-GTSM traffic in the kernel | Less userspace work under floods | Classic BPF (`SO_ATTACH_FILTER`, socket(7)) needs no library; GTSM is already enforced in userspace | Not needed; reconsider with the baseline |
| Kernel-side Echo reflection | — | Not required by RFC 9747 | Rejected |
| Packet and drop counters | Per-socket drop visibility | `SO_RXQ_OVFL` (socket(7), Linux 2.6.33+) reports socket drops in ancillary data without eBPF | Use `SO_RXQ_OVFL` for loss accounting instead |
| Full in-kernel BFD state machine | Sub-millisecond detection independent of Go scheduling | Reimplements RFC 5880 in the verifier's limits; out of v1 scope | Rejected for v1 |

## Decision

Do not add cilium/ebpf to v1.

1. No measured problem: there is no end-to-end receive latency or syscall
   pressure baseline; `gobfd-qj0.8.2.1.6` must produce it first, and the
   cheaper `recvmmsg` prototype is already ordered before kernel bypass.
2. Privilege and security: loading BPF programs adds `CAP_BPF` and needs
   the `bpf()` syscall (and `AF_XDP` for AF_XDP) inside the syscall and
   address-family filter that `gobfd-qj0.8.2.3.3` narrows; BPF tokens
   (v0.22.0) need delegation by systemd or the container runtime.
3. Dataplane ownership: an XDP program competes with Cilium, OVS and other
   XDP users on the same interface, the conflict GoBFD already documents for
   overlays.
4. Dependency churn: the pre-1.0 library broke APIs in v0.21.0 and v0.22.0,
   and Linux 7.1 requires v0.22.0, so kernel releases force upgrades.
5. Build: `bpf2go` adds a clang/LLVM and kernel-header build dependency to a
   repository that pins every toolchain; the `asm` alternative avoids it but
   is hard to review.

Adopt now, without eBPF: `SO_RXQ_OVFL` for socket drop counts in the loss
accounting work (`gobfd-qj0.8.2.1.3.1`, `gobfd-qj0.8.2.1.6`).

Revisit post-v1 (`gobfd-376`) when the baseline shows receive-path syscall
pressure that `recvmmsg` does not remove at the target session scale. The
prototype then uses ebpf-go v0.22.0 or later (Linux 7.1 support), XDP in
generic and driver mode on a dedicated interface, the `asm` package or
`bpf2go` in the pinned build container, and BPF tokens where the runtime
delegates them.

## Sources

- `gh api graphql` on `cilium/ebpf` (release list, activity, licence) and
  `gh api repos/cilium/ebpf/releases/tags/<tag>` for v0.19.0 to v0.22.0,
  2026-10-15.
- `https://proxy.golang.org/github.com/cilium/ebpf/@latest` and `@v/list`.
- context7 `/cilium/ebpf`: README requirements and packages,
  `link/xdp.go`, `cmd/bpf2go/README.md`, getting-started guide.
- socket(7), https://man7.org/linux/man-pages/man7/socket.7.html
  (`SO_RXQ_OVFL`, `SO_ATTACH_FILTER`, `SO_ATTACH_BPF`); capabilities(7),
  https://man7.org/linux/man-pages/man7/capabilities.7.html (`CAP_BPF`).
- `https://proxy.golang.org/github.com/cilium/ebpf/@v/v0.22.0.mod`.
- RFC 9747 Section 2, `docs/rfc/rfc9747.txt`.
- GoBFD: `internal/netio/rawsock_linux.go`, `BENCHMARKS.md`,
  `deployments/systemd/gobfd.service`, `go.mod`.
- Not verified: `IP_MINTTL` for UDP sockets (not documented in ip(7)).
