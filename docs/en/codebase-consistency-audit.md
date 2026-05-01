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
- OVSDB API research note: `docs/en/ovsdb-api-research.md`.
- Official and primary docs for `gopls`, NetworkManager D-Bus, `godbus/dbus`,
  `cilium/ebpf`, Arista EOS BFD behavior, and Open vSwitch OVSDB behavior.
- Linux advanced BFD applicability note:
  `docs/en/linux-advanced-bfd-applicability.md`.

## Executive Summary

The implementation is ahead of the top-level README and the control-plane API.
The daemon and YAML configuration support the advanced session families that
the RFC docs describe. Public API vocabulary, snapshots, and `gobfdctl`
formatting now expose those families, while `AddSession` and
`gobfdctl session add` intentionally remain limited to single-hop and
multi-hop sessions until dedicated transport-specific configuration APIs exist.
The primary functional gap for independent production use is therefore not the
BFD packet engine; it is the remaining operator-facing surface: dedicated
advanced API/CLI create flows, owner-specific overlay backends, and broader
interop evidence for production dataplane owners.

The most important tooling inconsistency was `make gopls-check`: it printed
Darwin diagnostics for Linux-only networking code but exited 0. S4.1 fixes the
gate to run under a Linux build context and fail on any diagnostics.

## Consistency Matrix

| Area | Code reality | Documentation reality | Status |
|---|---|---|---|
| RFC 5880 auth | YAML, gRPC, CLI, snapshots, sequence reset, and hardening are implemented for static per-session keys. | Config and changelog describe static key material; dynamic rotation is deferred. | Consistent |
| Single-hop / multi-hop | API and CLI create only `single-hop` and `multi-hop` sessions. | CLI docs match this behavior. | Consistent |
| Echo / Micro-BFD / VXLAN / Geneve | Daemon, config, reconcile paths, codecs, receivers, and tests exist. | RFC/config docs describe implemented support. Previous README RFC table lagged behind. | Fixed in S4.1 |
| Linux Micro-BFD enforcement | Per-member sessions, aggregate state tracking, actuator policy, dry-run config wiring, explicit kernel-bond sysfs backend, native OVSDB bonded-port backend, NetworkManager D-Bus backend, and transitional OVS CLI fallback type exist. | RFC/config docs separate detect/report, dry-run policy, kernel-bond/OVS enforcement with `allow-external`, OVSDB endpoint config, and NetworkManager D-Bus owner policy. | Partial |
| Linux VXLAN/Geneve dataplane coexistence | `userspace-udp` backend binds `localAddr:4789` and `localAddr:6081`; reserved kernel/OVS/OVN/Cilium/NSX backend names fail closed. | RFC/config docs describe explicit backend ownership and future non-userspace integration scope. | Partial |
| Advanced API vocabulary | Proto enum, server mappings, snapshots, and CLI output know Echo, Micro-BFD, VXLAN, and Geneve. Generic `AddSession` rejects these types until dedicated APIs are added. | Plan now separates vocabulary/snapshot exposure from advanced create flows. | Partial |
| Unsolicited BFD | Manager auto-creates passive sessions behind explicit policy. | Config and RFC docs describe opt-in behavior. | Consistent |
| Interface monitor | Linux rtnetlink monitor transitions sessions on link-down. Non-Linux has stub behavior. | S4 research doc and implementation plan describe Linux scope. | Consistent |
| `gopls-check` | Old target checked raw file list, mixed GOOS scopes, printed diagnostics, and exited 0. | Plan claimed `gopls-check` as a green quality gate. | Fixed in S4.1 |
| pkg.go.dev command page | `v0.5.2` is indexed on pkg.go.dev with Apache-2.0 detection and command documentation. | README, changelog, and implementation plan treat pkg.go.dev as closed for the current release. | Consistent |
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

### F2: pkg.go.dev surface closed in v0.5.2

The public package pages render with a detected Apache-2.0 license, tagged
module version, README content, and command package documentation. Further
pkg.go.dev changes are editorial improvements, not release blockers.

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
  tied to public vendor documentation.

Next sprint: S9, `docs: sync scorecard and release documentation`.

### F5: Linux advanced BFD needs explicit dataplane ownership

The latest RFC/Linux review confirms that Micro-BFD, VXLAN BFD, and Geneve BFD
are applicable to Linux, but the current implementation is not a complete
dataplane controller.

For Micro-BFD, GoBFD creates one RFC 7130 session per member link, tracks
aggregate state, wires a dry-run actuator policy into the daemon, and can
remove/add members for explicit Linux kernel bonding through sysfs when the
operator sets `owner_policy: allow-external`. It can also remove/add members on
an existing OVS bonded port through native OVSDB transactions against
`Port.interfaces` when `backend: ovs` is selected. The older `OVSLAGBackend`
remains a direct CLI fallback type for diagnostics, but it is no longer the
factory default for `backend: ovs`. NetworkManager-owned bond ports are handled
through D-Bus by deactivating the active slave profile and reactivating the
remembered or available bond port profile when the member recovers.

For VXLAN/Geneve, GoBFD now models the overlay transport backend explicitly.
The implemented backend is `userspace-udp`, which owns the standard outer UDP
socket. Reserved `kernel`, `ovs`, `ovn`, `cilium`, `calico`, and `nsx` backends fail
closed until those dataplane integrations exist. Sender reconciliation reuses
the runtime backend already serving the receiver, so the daemon no longer
tries to bind duplicate VXLAN/Geneve sockets for the same local endpoint.

Next sprints:
- S5b, dedicated advanced API/CLI create flows
- Future owner-specific overlay backends for kernel, OVS, OVN, Cilium, Calico,
  and NSX dataplanes

## Sprint Plan

| Sprint | Goal | Deliverable | Commit |
|---|---|---|---|
| S4.1 | Close code/docs/tooling drift. | Harden `gopls-check`, update README RFC status, record this audit. | `chore(lint): harden gopls diagnostics gate` |
| S5 | Make public control plane match daemon capabilities. | Partial: API vocabulary, session snapshots, and CLI output cover Echo, Micro-BFD, VXLAN, Geneve; dedicated create flows remain. | `feat(api): expose advanced session type vocabulary` |
| S5.1 | Keep session state mutation paths coherent. | Done: AdminDown transition serialized through the session goroutine and covered by wire test. | `fix(bfd): serialize admin-down transition` |
| S6 | Production security policy. | Done: mTLS/localhost policy, vulnerability allowlist expiry, secret-handling docs. | `docs(security): define production hardening policy` |
| S6.1 | Linux advanced BFD applicability. | Done: RFC docs, config examples, and code comments align with Micro-BFD actuator and overlay dataplane limits. | `docs(linux): document advanced bfd applicability` |
| S7 | Independent production integration readiness. | Done for generic runbooks, Kubernetes manifest hardening, alert rule correction, FRR/GoBGP example documentation, public EOS verification notes, Micro-BFD actuator policy/config wiring, kernel-bond backend, OVS CLI fallback, OVSDB research, native OVSDB backend, NetworkManager D-Bus backend, and overlay backend model. | `feat(examples): add production integration assets` |
| S7a | Production runbooks and manifest hardening. | Generic EN/RU production runbooks, Kubernetes probes/labels, and Prometheus alerts aligned with exported GoBFD metrics. | `docs(examples): add production integration runbooks` |
| S7b | BGP failover interop documentation. | FRR/GoBGP example README, RFC packet checks, troubleshooting matrix, and optional public Arista EOS verification note. | `docs(examples): document bgp failover interop` |
| S7.1 | Linux Micro-BFD enforcement. | Partial production integration: Manager actuator hook, guarded `netio.LAGActuator` policy, config validation, daemon dry-run wiring, explicit kernel-bond backend, OVS CLI fallback, native OVSDB backend, and NetworkManager D-Bus backend are done; dedicated API/CLI create flows remain. | `feat(netio): add linux lag actuator` |
| S7.1b | Linux LAG actuator config wiring. | Add config validation and daemon wiring for disabled/dry-run/enforce policy modes without destructive Linux changes. | `feat(config): wire micro-bfd actuator config` |
| S7.1c | Kernel bond LAG backend. | Linux bonding sysfs backend and daemon enforce wiring for explicit `backend: kernel-bond` plus `owner_policy: allow-external`. | `feat(netio): add kernel bond lag backend` |
| S7.1d | OVS CLI fallback backend. | OVS bonded-port backend using `ovs-vsctl add-bond-iface` / `del-bond-iface` for explicit `backend: ovs` plus `owner_policy: allow-external`. | `feat(netio): add ovs lag backend` |
| S7.1d2 | OVSDB API research. | OVSDB JSON-RPC documented as the native OVS management API and `libovsdb` selected as the preferred Go route. | `docs(netio): document ovsdb backend path` |
| S7.1e | Native OVSDB backend. | Done: `backend: ovs` selects OVSDB-backed LAG enforcement with configurable `ovsdb_endpoint`; `OVSLAGBackend` remains fallback/diagnostics. | `feat(netio): add ovsdb lag backend` |
| S7.1f | Optional NetworkManager backend. | Done: NetworkManager D-Bus backend deactivates active bond port profiles and reactivates remembered or available bond port profiles. | `feat(netio): add networkmanager lag backend` |
| S7.2 | VXLAN/Geneve dataplane coexistence. | Done: `userspace-udp` is explicit, reserved kernel/OVS/OVN/Cilium/NSX backend names fail closed, and reconciliation reuses the runtime overlay backend. | `feat(netio): add overlay backend model` |
| S8 | `v0.5.0` release readiness without v1 bump. | Done: release dry-run, changelog, SemVer tag plan, docs layout, and package artifacts. | `chore(release): prepare v0.5.0` |
| S8.1 | `v0.5.2` pkg.go.dev close-out. | Done: command package docs and canonical Apache-2.0 license text restored pkg.go.dev rendering. | `fix(docs): restore pkg.go.dev license detection` |
| S9 | Documentation and Scorecard hardening. | In progress: post-release doc synchronization, Scorecard plan for one maintainer, repository settings truth table, and removal of non-canonical Markdown. | `docs: sync scorecard and release documentation` |

## Current Readiness

| Dimension | Readiness | Notes |
|---|---:|---|
| Core RFC packet engine | 85% | Strong coverage for base, auth, echo, unsolicited, overlays; MPLS/PW remain stubs. |
| API/CLI operational completeness | 62% | Good for base sessions, auth, and advanced session observability; advanced create/update flows still missing. |
| Linux production daemon behavior | 82% | Raw sockets, buffers, rtnetlink, systemd, metrics, serialized drain behavior, and hardened Kubernetes examples exist. |
| Independent production applicability | 82% | Generic runbooks, BGP fast-failover examples, Micro-BFD dry-run wiring, kernel-bond enforcement, OVS CLI fallback, OVSDB research, native OVSDB enforcement, NetworkManager D-Bus enforcement, and explicit overlay backend ownership are published; non-userspace overlay integrations remain future work. |
| Release/presentation quality | 86% | Release artifacts, changelogs, pkg.go.dev and docs layout are in place; Scorecard hardening and signed provenance remain. |
