# GoBFD Documentation

English is the source documentation; [Russian](../ru/README.md) mirrors it.
These pages describe the current `dev` implementation. The accepted stable
line on `master` and `release/v0.6` has different management defaults and GoBGP
contracts. A feature described here is not automatically present in a published
release. Use documentation from the same commit as the binary.

## Read by task

| Task | Document |
|---|---|
| Understand component ownership and lifecycle | [Architecture](01-architecture.md) |
| Understand packet handling, timers and authentication | [BFD protocol](02-protocol.md) |
| Configure sessions, transports and reload | [Configuration](03-configuration.md) |
| Operate the daemon and renew a management certificate | [CLI](04-cli.md) |
| Run peer and wire qualification | [Interoperability](05-interop.md) |
| Install a service, package or container | [Deployment](06-deployment.md) |
| Interpret metrics and alerts | [Monitoring](07-monitoring.md) |
| Check implemented procedures and unsupported RFC scope | [RFC compliance](08-rfc-compliance.md) |
| Build, test, lint and contribute | [Development](09-development.md) |
| Maintain changelogs and prepare a release | [Changelog and release guide](10-changelog.md) |
| Use BGP, HAProxy, ExaBGP, observability and Kubernetes examples | [Integrations](11-integrations.md) |
| Run and compare micro-benchmarks | [Benchmark guide](12-benchmarks.md) |
| Assess comparisons with other implementations | [Comparison boundaries](13-competitive-analysis.md) |
| Interpret receive telemetry and workload measurements | [Performance analysis](14-performance-analysis.md) |
| Secure management, credentials and network access | [Security](15-security.md) |
| Validate an isolated deployment and failure drill | [Operational runbooks](16-production-runbooks.md) |

## Decisions, sources and status

| Material | Purpose |
|---|---|
| [ADRs](adr/README.md) | Accepted decisions and proposed changes; acceptance is separate from qualification |
| [References](reference/README.md) | Dependency risks and supporting technical material |
| [RFC texts](../rfc/) | Local normative source texts; presence does not imply support |
| [Roadmap](roadmap.md) | Dated status projection and historical receipts |
| [External audit snapshot](gist-audit-reconciliation.md) | Historical reconciliation with immutable source references |
| [Changelog](../../CHANGELOG.md) | User-visible changes; `Unreleased` is not a published release |

Beads is the sole executable plan. `bd show gobfd-qj0.8` and `bd ready`
provide current priorities, dependencies and acceptance criteria. No page in
this documentation grants publication approval or replaces exact-candidate
wire, security, integration and runtime qualification.

## Start locally

Follow [Development](09-development.md) to create the pinned Podman environment,
then [Deployment](06-deployment.md) for daemon privileges and socket access.
The default management endpoint is `/run/gobfd/gobfd.sock`; metrics binds to
`127.0.0.1:9100`. Read [Security](15-security.md) before enabling TCP management
or any preview profile.
