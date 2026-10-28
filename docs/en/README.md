# GoBFD manual

English is canonical. [Russian](../ru/README.md) mirrors the technical
contract. Use pages from the deployed binary's commit: `dev`, `master` and
`release/v0.6` have different delivery and compatibility boundaries.

## Operator references

| Task | Document |
|:---|:---|
| Identify components and state owners | [01 Architecture](01-architecture.md) |
| Calculate timers and inspect packet processing | [02 Protocol](02-protocol.md) |
| Configure sessions, credentials and reload | [03 Configuration](03-configuration.md) |
| Inspect and modify managed resources | [04 CLI](04-cli.md) |
| Select peer and packet qualification | [05 Interoperability](05-interop.md) |
| Install packages, services and containers | [06 Deployment](06-deployment.md) |
| Interpret metrics and alerts | [07 Monitoring](07-monitoring.md) |
| Check implemented RFC procedures and exclusions | [08 RFC matrix](08-rfc-compliance.md) |
| Connect routing and application clients | [11 Integrations](11-integrations.md) |
| Secure management and credentials | [15 Security](15-security.md) |
| Validate startup, failure and recovery | [16 Runbooks](16-production-runbooks.md) |
| Configure equipment peers | [Cisco, Arista, Juniper and Huawei](reference/vendor-bfd.md) |

## Engineering references

| Task | Document |
|:---|:---|
| Build, review and test | [09 Development](09-development.md) |
| Maintain changelogs and release evidence | [10 Release process](10-changelog.md) |
| Reproduce micro-benchmarks | [12 Benchmarks](12-benchmarks.md) |
| Compare implementations by supported profile | [13 Comparisons](13-competitive-analysis.md) |
| Evaluate runtime and workload evidence | [14 Performance](14-performance-analysis.md) |
| Check architectural constraints | [ADRs](adr/README.md) |
| Inspect dependency risks | [References](reference/README.md) |
| Check outstanding release acceptance | [Dated status](roadmap.md) |

## Reading rules

- Treat an RFC requirement as normative; treat a vendor manual as a
  platform-specific contract. Neither proves a GoBFD interoperability result.
- Distinguish implemented, locally tested, wire-qualified and released behavior.
- Apply imperative procedures only to the explicitly selected environment.
  Retain the pre-change configuration and verify the documented postcondition.
- Verify acceptance against the exact candidate and deployment profile.

Historical material: [external audit reconciliation](gist-audit-reconciliation.md),
[release retrospective](audits/2026-10-26-release-retrospective.md),
[release changelog](../../CHANGELOG.md), [verbatim RFC texts](../rfc/).
