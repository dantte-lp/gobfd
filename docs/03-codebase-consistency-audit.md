# Codebase Consistency Audit

Date: 2026-05-01

Scope: repository structure, README, changelogs, English/Russian docs, public
API, CLI, configuration schema, Makefile gates, and applicability to the
`um-docs` infrastructure target.

Evidence sources:
- Local code and tests under `cmd/`, `internal/`, `pkg/`, `api/`, `configs/`,
  `deployments/`, and `test/`.
- Local documentation under `README.md`, `docs/`, `CHANGELOG.md`, and
  `CHANGELOG.ru.md`.
- Neighbor repository patterns from `/opt/projects/repositories/pulumi-eos`.
- `um-docs` references to Arista EOS, EVPN/VXLAN, RKE2, Cilium/Calico, BGP,
  BFD `300/300/3`, partner failover, and DCI fast-failover expectations.
- MCP checks: Context7 `gopls` docs for build environment scoping; Arista MCP
  EOS BFD snippets for BFD interface/BGP/per-link feature context.

## Executive Summary

The implementation is ahead of the top-level README and the control-plane API.
The daemon and YAML configuration support the advanced session families that
the RFC docs describe. Public API vocabulary, snapshots, and `gobfdctl`
formatting now expose those families, while `AddSession` and
`gobfdctl session add` intentionally remain limited to single-hop and
multi-hop sessions until dedicated transport-specific configuration APIs exist.
The primary functional gap for `um-docs` production use is therefore not the
BFD packet engine; it is the operator-facing surface: dedicated API/CLI create
flows, Kubernetes packaging, Arista/FRR examples, and failure-drill
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
| Advanced API vocabulary | Proto enum, server mappings, snapshots, and CLI output know Echo, Micro-BFD, VXLAN, and Geneve. Generic `AddSession` rejects these types until dedicated APIs are added. | Plan now separates vocabulary/snapshot exposure from advanced create flows. | Partial |
| Unsolicited BFD | Manager auto-creates passive sessions behind explicit policy. | Config and RFC docs describe opt-in behavior. | Consistent |
| Interface monitor | Linux rtnetlink monitor transitions sessions on link-down. Non-Linux has stub behavior. | S4 research doc and implementation plan describe Linux scope. | Consistent |
| `gopls-check` | Old target checked raw file list, mixed GOOS scopes, printed diagnostics, and exited 0. | Plan claimed `gopls-check` as a green quality gate. | Fixed in S4.1 |
| pkg.go.dev command page | `cmd/gobfd` has a minimal package comment. | Plan marks pkg.go.dev as weak. | Open |
| Graceful AdminDown | `SetAdminDown` routes through the session control channel while the session goroutine is running; startup syncs `cachedState` from atomic state for pre-run administrative changes. | Docs claim graceful AdminDown drain. | Fixed in S5.1 |
| `um-docs` readiness | Core BFD can support BGP fast failover, EVPN/VXLAN checks, and Kubernetes daemon deployment patterns. | `um-docs` expects BFD on BGP links, RKE2/Cilium/Calico context, and multi-site failover docs. | Partial |

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
but can create them only by editing YAML and triggering reload. For `um-docs`
style operations, this is still insufficient for dynamic failover drills,
GitOps controllers, or incident tooling.

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

### F4: `um-docs` production use needs operator assets

The `um-docs` repository describes Arista EOS EVPN/VXLAN fabric, BGP
fast-failover, RKE2 Cilium/Calico clusters, partner tunnels, and DCI scenarios.
GoBFD aligns with these needs only after adding:

- Kubernetes DaemonSet or Helm packaging with `NET_RAW`, `NET_ADMIN`,
  hostNetwork, node selectors, and explicit network namespace assumptions.
- Arista EOS and FRR examples for BGP neighbor BFD, timers, and failure drills.
- Prometheus alerts and Grafana panels tied to BFD session state, auth failures,
  flap dampening, and link-down diagnostics.
- Runbooks for BFD `300/300/3`, interface-down simulation, BGP withdraw
  verification, and rollback.

Next sprint: S7, `feat(k8s): add production integration assets`.

## Sprint Plan

| Sprint | Goal | Deliverable | Commit |
|---|---|---|---|
| S4.1 | Close code/docs/tooling drift. | Harden `gopls-check`, update README RFC status, record this audit. | `chore(lint): harden gopls diagnostics gate` |
| S5 | Make public control plane match daemon capabilities. | In progress: API vocabulary, session snapshots, and CLI output cover Echo, Micro-BFD, VXLAN, Geneve; dedicated create flows remain. | `feat(api): expose advanced session type vocabulary` |
| S5.1 | Keep session state mutation paths coherent. | Done: AdminDown transition serialized through the session goroutine and covered by wire test. | `fix(bfd): serialize admin-down transition` |
| S6 | Production security policy. | Done: mTLS/localhost policy, vulnerability allowlist expiry, secret-handling docs. | `docs(security): define production hardening policy` |
| S7 | `um-docs` integration readiness. | Kubernetes manifests, Arista/FRR/GoBGP examples, alerts, and failure drills. | `feat(k8s): add production integration assets` |
| S8 | `v0.5.0` release readiness without v1 bump. | pkg.go.dev polish, release dry-run, changelog, SemVer tag plan. | `chore(release): prepare v0.5.0` |

## Current Readiness

| Dimension | Readiness | Notes |
|---|---:|---|
| Core RFC packet engine | 85% | Strong coverage for base, auth, echo, unsolicited, overlays; MPLS/PW remain stubs. |
| API/CLI operational completeness | 62% | Good for base sessions, auth, and advanced session observability; advanced create/update flows still missing. |
| Linux production daemon behavior | 78% | Raw sockets, buffers, rtnetlink, systemd, metrics, and serialized drain behavior exist; Kubernetes assets need hardening. |
| `um-docs` applicability | 60% | Suitable direction for BGP fast failover, not yet packaged/documented enough for full platform use. |
| Release/presentation quality | 70% | Changelog/standards/gates are improving; pkg.go.dev and README polish remain. |
