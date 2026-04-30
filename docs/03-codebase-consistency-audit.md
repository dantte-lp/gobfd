# Codebase Consistency Audit

Date: 2026-05-01

Scope: repository structure, README, changelogs, English/Russian docs, public
API, CLI, configuration schema, Makefile gates, and independent production
networking applicability.

Evidence sources:
- Local code and tests under `cmd/`, `internal/`, `pkg/`, `api/`, `configs/`,
  `deployments/`, and `test/`.
- Local documentation under `README.md`, `docs/`, `CHANGELOG.md`, and
  `CHANGELOG.ru.md`.
- Public production scenarios: Linux routing hosts, BGP fast failover,
  EVPN/VXLAN and Geneve overlays, Kubernetes host-network daemon deployment,
  Cilium/Calico-style BGP environments, partner edge failover, and DCI-style
  fast-failover expectations.
- MCP checks: Context7 `gopls` docs for build environment scoping; Arista MCP
  EOS BFD snippets for BFD interface/BGP/per-link feature context.
- Linux advanced BFD applicability note:
  `docs/04-linux-advanced-bfd-applicability.md`.

## Executive Summary

The implementation is ahead of the top-level README and the control-plane API.
The daemon and YAML configuration support the advanced session families that
the RFC docs describe. Public API vocabulary, snapshots, and `gobfdctl`
formatting now expose those families, while `AddSession` and
`gobfdctl session add` intentionally remain limited to single-hop and
multi-hop sessions until dedicated transport-specific configuration APIs exist.
The primary functional gap for independent production use is therefore not the
BFD packet engine; it is the operator-facing surface: dedicated API/CLI create
flows, Kubernetes packaging, public interop examples, and failure-drill
documentation.

The most important tooling inconsistency was `make gopls-check`: it printed
Darwin diagnostics for Linux-only networking code but exited 0. S4.1 fixes the
gate to run under a Linux build context and fail on any diagnostics.

## Consistency Matrix

| Area | Code reality | Documentation reality | Status |
|---|---|---|---|
| RFC 5880 auth | YAML, gRPC, CLI, snapshots, sequence reset, and hardening are implemented for static per-session keys. | Config and changelog describe static key material; dynamic rotation is deferred. | Consistent |
| Single-hop / multi-hop | API and CLI create only `single-hop` and `multi-hop` sessions. | CLI docs match this behavior. | Consistent |
| Echo / Micro-BFD / VXLAN / Geneve | Daemon, config, reconcile paths, codecs, receivers, and tests exist. | RFC/config docs describe implemented support. Previous README RFC table lagged behind. | Fixed in S4.1 |
| Linux Micro-BFD enforcement | Per-member sessions and aggregate state tracking exist; no Linux bond/team/OVS actuator exists yet. | RFC docs now separate detection/reporting from load-balancer membership enforcement. | Partial |
| Linux VXLAN/Geneve dataplane coexistence | Userspace UDP sockets bind `localAddr:4789` and `localAddr:6081`. | RFC/config docs now warn that kernel VXLAN/Geneve, OVS/OVN, or Cilium socket ownership needs explicit design. | Partial |
| Advanced API vocabulary | Proto enum, server mappings, snapshots, and CLI output know Echo, Micro-BFD, VXLAN, and Geneve. Generic `AddSession` rejects these types until dedicated APIs are added. | Plan now separates vocabulary/snapshot exposure from advanced create flows. | Partial |
| Unsolicited BFD | Manager auto-creates passive sessions behind explicit policy. | Config and RFC docs describe opt-in behavior. | Consistent |
| Interface monitor | Linux rtnetlink monitor transitions sessions on link-down. Non-Linux has stub behavior. | S4 research doc and implementation plan describe Linux scope. | Consistent |
| `gopls-check` | Old target checked raw file list, mixed GOOS scopes, printed diagnostics, and exited 0. | Plan claimed `gopls-check` as a green quality gate. | Fixed in S4.1 |
| pkg.go.dev command page | `cmd/gobfd` has a minimal package comment. | Plan marks pkg.go.dev as weak. | Open |
| Graceful AdminDown | `SetAdminDown` routes through the session control channel while the session goroutine is running; startup syncs `cachedState` from atomic state for pre-run administrative changes. | Docs claim graceful AdminDown drain. | Fixed in S5.1 |
| Production integration readiness | Core BFD can support BGP fast failover, EVPN/VXLAN checks, and Kubernetes daemon deployment patterns. | Public docs still need generic Kubernetes, routing daemon, vendor-neutral, and failure-drill assets. | Partial |

## Findings

### F1: API/CLI advanced session gap

`internal/bfd`, `internal/netio`, and `cmd/gobfd` support Echo, Micro-BFD,
VXLAN, and Geneve through declarative config and daemon reconciliation. S5a
adds public enum values and formatting for:

- `SESSION_TYPE_ECHO`
- `SESSION_TYPE_MICRO_BFD`
- `SESSION_TYPE_VXLAN`
- `SESSION_TYPE_GENEVE`

Generic `AddSession` rejects these recognized types because RFC 7130 uses UDP
6784 per LAG member, RFC 8971 requires a Management VNI, and RFC 9521 depends
on Geneve VAP/VNI encapsulation. Creating them through the existing generic
sender would be a misleading API contract.

Remaining impact: automation can observe advanced sessions via API/CLI output,
but can create them only by editing YAML and triggering reload. For production
operations, this is still insufficient for dynamic failover drills, GitOps
controllers, or incident tooling.

Next sprint: S5b, add dedicated advanced create/update API shape without
reusing the generic `AddSession` transport contract.

### F2: pkg.go.dev surface is too thin

The command package comment is one line. The desired public page at
`pkg.go.dev/github.com/dantte-lp/gobfd/cmd/gobfd` needs a concise package
comment that explains daemon purpose, Linux requirements, config path,
capabilities, metrics, API endpoint, and related binaries without becoming a
README clone.

Next sprint: S8, `docs(pkg): improve command package documentation`.

### F3: AdminDown state path was serialized in S5.1

`Session.SetPathDown` uses a control channel so state changes happen in the
session goroutine and keep `cachedState` coherent. S5.1 applied the same model
to `SetAdminDown` when the session goroutine is running. If an administrative
change happens before `Run`, startup syncs `cachedState` from the atomic state
before timers are calculated.

Evidence: `TestSessionSetAdminDownSendsAdminDownPacket` verifies that an Up
session sends `AdminDown` / `DiagAdminDown` on the wire after graceful drain.

### F4: Independent production use needs operator assets

GoBFD is an independent project. Its public roadmap must describe generic,
reusable production scenarios instead of site-specific topology. The relevant
scenario families are: Linux routing hosts, BGP fast-failover, Kubernetes
host-network daemons, EVPN/VXLAN and Geneve overlays, partner edge failover,
and DCI-style fast-failover drills.

S7a and S7b moved the operator-assets layer from planning to published
generic examples:

- Kubernetes DaemonSet manifests now document `NET_RAW`, `NET_ADMIN`,
  hostNetwork, node selectors, probes, labels, and network namespace
  assumptions.
- FRR/GoBGP BGP fast-failover docs now cover BGP neighbor BFD, `300/300/3`
  timers, RFC-visible packet checks, failure drills, and rollback.
- Prometheus alerts now map to exported GoBFD metrics for active sessions,
  Up-to-Down transitions, flapping, auth failures, and packet drops.
- Optional public Arista EOS notes are separated from runnable examples and
  validated through Arista MCP.

Next sprint: S7.1b, `feat(netio): wire linux lag actuator backend`.

### F5: Linux advanced BFD needs explicit dataplane ownership

The latest RFC/Linux review confirms that Micro-BFD, VXLAN BFD, and Geneve BFD
are applicable to Linux, but the current implementation is not a complete
dataplane controller.

For Micro-BFD, GoBFD creates one RFC 7130 session per member link and tracks
aggregate state. It does not yet remove a failed member from Linux bonding,
team, or OVS load-balancing tables. This is the main gap between protocol
detection and full RFC 7130 enforcement on Linux.

For VXLAN/Geneve, GoBFD owns userspace UDP sockets on the standard outer ports.
That works for dedicated management endpoints and labs, but production Linux
VTEPs often already have kernel VXLAN/Geneve, OVS/OVN, Cilium, or NSX owning
those ports. That needs a backend model rather than a blanket claim that the
userspace socket can always coexist with the dataplane.

Next sprints:
- S7.1b, `feat(netio): wire linux lag actuator backend`
- S7.2, `feat(netio): add overlay backend model`

## Sprint Plan

| Sprint | Goal | Deliverable | Commit |
|---|---|---|---|
| S4.1 | Close code/docs/tooling drift. | Harden `gopls-check`, update README RFC status, record this audit. | `chore(lint): harden gopls diagnostics gate` |
| S5 | Make public control plane match daemon capabilities. | In progress: API vocabulary, session snapshots, and CLI output cover Echo, Micro-BFD, VXLAN, Geneve; dedicated create flows remain. | `feat(api): expose advanced session type vocabulary` |
| S5.1 | Keep session state mutation paths coherent. | Done: AdminDown transition serialized through the session goroutine and covered by wire test. | `fix(bfd): serialize admin-down transition` |
| S6 | Production security policy. | Done: mTLS/localhost policy, vulnerability allowlist expiry, secret-handling docs. | `docs(security): define production hardening policy` |
| S6.1 | Linux advanced BFD applicability. | In progress: align RFC docs, config examples, and code comments with Micro-BFD actuator and overlay dataplane limits. | `docs(linux): document advanced bfd applicability` |
| S7 | Independent production integration readiness. | In progress: generic runbooks, Kubernetes manifest hardening, alert rule correction, FRR/GoBGP example documentation, public EOS verification notes, and Micro-BFD actuator policy are done; remaining work is S7.1b/S7.2 implementation. | `feat(examples): add production integration assets` |
| S7a | Production runbooks and manifest hardening. | Generic EN/RU production runbooks, Kubernetes probes/labels, and Prometheus alerts aligned with exported GoBFD metrics. | `docs(examples): add production integration runbooks` |
| S7b | BGP failover interop documentation. | FRR/GoBGP example README, RFC packet checks, troubleshooting matrix, and optional public Arista EOS verification note. | `docs(examples): document bgp failover interop` |
| S7.1 | Linux Micro-BFD enforcement. | In progress: Manager actuator hook and guarded `netio.LAGActuator` policy are done; Linux bond/team/OVS backend and YAML wiring remain. | `feat(netio): add linux lag actuator` |
| S7.1b | Linux LAG backend wiring. | Add backend implementation, config validation, and daemon wiring for dry-run/enforce modes. | `feat(netio): wire linux lag actuator backend` |
| S7.2 | VXLAN/Geneve dataplane coexistence. | Backend abstraction for kernel/OVS/Cilium/NSX-compatible overlay BFD transport. | `feat(netio): add overlay backend model` |
| S8 | `v0.5.0` release readiness without v1 bump. | pkg.go.dev polish, release dry-run, changelog, SemVer tag plan. | `chore(release): prepare v0.5.0` |

## Current Readiness

| Dimension | Readiness | Notes |
|---|---:|---|
| Core RFC packet engine | 85% | Strong coverage for base, auth, echo, unsolicited, overlays; MPLS/PW remain stubs. |
| API/CLI operational completeness | 62% | Good for base sessions, auth, and advanced session observability; advanced create/update flows still missing. |
| Linux production daemon behavior | 82% | Raw sockets, buffers, rtnetlink, systemd, metrics, serialized drain behavior, and hardened Kubernetes examples exist. |
| Independent production applicability | 66% | Generic runbooks and BGP fast-failover examples are published; Linux LAG actuator and overlay dataplane backend are still open. |
| Release/presentation quality | 70% | Changelog/standards/gates are improving; pkg.go.dev and README polish remain. |
