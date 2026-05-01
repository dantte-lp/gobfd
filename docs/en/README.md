# GoBFD Documentation

![Version](https://img.shields.io/badge/Version-0.5.2-1a73e8?style=for-the-badge)
![Documents](https://img.shields.io/badge/Documents-27-34a853?style=for-the-badge)
![Language](https://img.shields.io/badge/Lang-English-ea4335?style=for-the-badge)
![Status](https://img.shields.io/badge/Status-Active-brightgreen?style=for-the-badge)

> Canonical technical documentation for **GoBFD** -- a production-oriented BFD protocol daemon (RFC 5880/5881) written in Go 1.26.

---

## Documentation Map

```mermaid
graph TD
    IDX["docs/en/README.md<br/>"]

    subgraph "Architecture"
        A1["01-architecture.md<br/>System Architecture"]
        A2["02-protocol.md<br/>BFD Protocol"]
    end

    subgraph "Operations"
        B1["03-configuration.md<br/>Configuration"]
        B2["04-cli.md<br/>CLI Reference"]
        B3["06-deployment.md<br/>Deployment"]
        B4["15-security.md<br/>Security Policy"]
        B5["16-production-runbooks.md<br/>Production Runbooks"]
    end

    subgraph "Testing & Quality"
        C1["05-interop.md<br/>Interop Testing"]
        C2["07-monitoring.md<br/>Monitoring"]
        C3["09-development.md<br/>Development"]
    end

    subgraph "Performance"
        E1["12-benchmarks.md<br/>Benchmarks"]
        E2["13-competitive-analysis.md<br/>Competitive Analysis"]
        E3["14-performance-analysis.md<br/>Performance Analysis"]
    end

    subgraph "Reference"
        D1["08-rfc-compliance.md<br/>RFC Compliance"]
        D2["rfc/<br/>RFC Text Files"]
        D3["10-changelog.md<br/>Changelog Guide"]
        D4["11-integrations.md<br/>Integrations"]
        D5["dependency-risk.md<br/>Dependency Risk"]
    end

    subgraph "Release Planning"
        F1["implementation-plan.md<br/>S8 Plan"]
        F2["codebase-consistency-audit.md<br/>Consistency Audit"]
        F3["linux-advanced-bfd-applicability.md<br/>Linux Advanced BFD"]
        F4["linux-netlink-ebpf-research.md<br/>Netlink/eBPF Research"]
        F5["ovsdb-api-research.md<br/>OVSDB API Research"]
        F6["17-scorecard-hardening.md<br/>Scorecard Hardening"]
        F7["18-s10-extended-e2e-interop.md<br/>S10 E2E Interop"]
        F8["19-s10-s1-harness-contract-plan.md<br/>S10.1 Harness Plan"]
        F9["20-s10-closeout-analysis.md<br/>S10 Closeout Analysis"]
        F10["21-s11-full-e2e-interop-plan.md<br/>S11 Full E2E Plan"]
    end

    IDX --> A1
    IDX --> A2
    IDX --> B1
    IDX --> B2
    IDX --> B3
    IDX --> B4
    IDX --> B5
    IDX --> C1
    IDX --> C2
    IDX --> C3
    IDX --> D1
    IDX --> D5
    IDX --> E1
    IDX --> F1
    IDX --> F2
    IDX --> F3
    IDX --> F4
    IDX --> F5
    IDX --> F6
    IDX --> F7
    IDX --> F8
    IDX --> F9
    IDX --> F10

    A1 --> A2
    A2 --> D1
    B1 --> B2
    B3 --> C2
    C1 --> C3
    D1 --> D2
    E1 --> E2
    E2 --> E3
    F1 --> F2
    F2 --> F3
    F3 --> F4
    F3 --> F5
    F2 --> F6
    F6 --> F7
    F7 --> F8
    F8 --> F9
    F9 --> F10

    style IDX fill:#1a73e8,color:#fff
```

---

## Table of Contents

### Architecture

| # | Document | Description |
|---|---|---|
| 01 | [**Architecture**](./01-architecture.md) | System architecture, package diagram, packet flow, FSM design |
| 02 | [**BFD Protocol**](./02-protocol.md) | FSM, timers, jitter, packet format, authentication (RFC 5880) |

### Operations

| # | Document | Description |
|---|---|---|
| 03 | [**Configuration**](./03-configuration.md) | YAML config reference, environment variables, hot reload |
| 04 | [**CLI Reference**](./04-cli.md) | gobfdctl commands, interactive shell, output formats |
| 05 | [**Interop Testing**](./05-interop.md) | Interop suites: 4-peer, BGP+BFD, RFC-specific, vendor NOS |
| 06 | [**Deployment**](./06-deployment.md) | systemd, Podman Compose, container image, production |
| 07 | [**Monitoring**](./07-monitoring.md) | Prometheus metrics, Grafana dashboard, alerting |
| 15 | [**Security Policy**](./15-security.md) | Production API, GoBGP, BFD auth, container, and vulnerability policy |
| 16 | [**Production Runbooks**](./16-production-runbooks.md) | Kubernetes, BGP, Prometheus, packet verification, and failure drills |

### Reference

| # | Document | Description |
|---|---|---|
| 08 | [**RFC Compliance**](./08-rfc-compliance.md) | RFC compliance matrix, implementation notes, links to RFCs |
| 09 | [**Development**](./09-development.md) | Dev workflow, Make targets, testing, linting, proto generation |
| 10 | [**Changelog Guide**](./10-changelog.md) | How to maintain CHANGELOG.md, release process, semantic versioning |
| 11 | [**Integrations**](./11-integrations.md) | BGP failover, HAProxy, observability, ExaBGP, Kubernetes examples |
| -- | [**Dependency Risk**](./dependency-risk.md) | Current dependency-risk notes and mitigations |

### Performance

| # | Document | Description |
|---|---|---|
| 12 | [**Benchmarks**](./12-benchmarks.md) | How to run, read, and interpret benchmark results |
| 13 | [**Competitive Analysis**](./13-competitive-analysis.md) | Comparison with FRR, BIRD, aiobfd, hardware platforms |
| 14 | [**Performance Analysis**](./14-performance-analysis.md) | GoBFD vs C implementations: benchmarks, architecture, CPU load behavior |

### Release Planning and Research

| Document | Description |
|---|---|
| [**Implementation Plan**](./implementation-plan.md) | Canonical phased sprint plan through `v0.5.2` |
| [**Codebase Consistency Audit**](./codebase-consistency-audit.md) | Code, API, CLI, config, docs, and production-applicability audit |
| [**Linux Advanced BFD Applicability**](./linux-advanced-bfd-applicability.md) | Applicability of Micro-BFD, VXLAN BFD, and Geneve BFD on Linux |
| [**Linux Netlink/eBPF Research**](./linux-netlink-ebpf-research.md) | S4 decision record for rtnetlink vs eBPF link-state monitoring |
| [**OVSDB API Research**](./ovsdb-api-research.md) | OVSDB JSON-RPC and `libovsdb` backend design notes |
| [**OpenSSF Scorecard Hardening**](./17-scorecard-hardening.md) | S9 one-maintainer Scorecard hardening plan |
| [**S10 Extended E2E and Interoperability**](./18-s10-extended-e2e-interop.md) | S10 evidence plan for E2E, interop, Linux dataplane, overlay, and vendor profiles |
| [**S10.1 Harness Inventory and Contract Plan**](./19-s10-s1-harness-contract-plan.md) | Detailed S10.1 implementation plan for the E2E harness contract |
| [**S10 Closeout Analysis**](./20-s10-closeout-analysis.md) | S10 completion, remaining work, and next-sprint candidate backlog |
| [**S11 Full E2E and Interoperability Plan**](./21-s11-full-e2e-interop-plan.md) | Full E2E execution, vendor evidence, reports, CI artifacts, and owner-backend decision plan |

### RFC Source Files

| File | RFC | Title |
|---|---|---|
| [rfc5880.txt](../rfc/rfc5880.txt) | RFC 5880 | Bidirectional Forwarding Detection (BFD) |
| [rfc5881.txt](../rfc/rfc5881.txt) | RFC 5881 | BFD for IPv4 and IPv6 (Single Hop) |
| [rfc5882.txt](../rfc/rfc5882.txt) | RFC 5882 | Generic Application of BFD |
| [rfc5883.txt](../rfc/rfc5883.txt) | RFC 5883 | BFD for Multihop Paths |
| [rfc5884.txt](../rfc/rfc5884.txt) | RFC 5884 | BFD for MPLS Label Switched Paths |
| [rfc5885.txt](../rfc/rfc5885.txt) | RFC 5885 | BFD for PW VCCV |
| [rfc7130.txt](../rfc/rfc7130.txt) | RFC 7130 | Bidirectional Forwarding Detection (BFD) on LAG |
| [rfc7419.txt](../rfc/rfc7419.txt) | RFC 7419 | Common Interval Support |
| [rfc9384.txt](../rfc/rfc9384.txt) | RFC 9384 | BGP Cease Notification Subcode for BFD |
| [rfc9468.txt](../rfc/rfc9468.txt) | RFC 9468 | Unsolicited BFD for Sessionless Applications |
| [rfc9747.txt](../rfc/rfc9747.txt) | RFC 9747 | Unaffiliated BFD Echo |
| [rfc8971.txt](../rfc/rfc8971.txt) | RFC 8971 | BFD for VXLAN |
| [rfc9521.txt](../rfc/rfc9521.txt) | RFC 9521 | BFD for Geneve |
| [rfc9764.txt](../rfc/rfc9764.txt) | RFC 9764 | BFD Encapsulated in Large Packets |

---

## Quick Start

```bash
# Clone and build
git clone https://github.com/dantte-lp/gobfd.git && cd gobfd
make build

# Run tests
make test

# Start dev environment
make up
```

See [06-deployment.md](./06-deployment.md) for production deployment and [09-development.md](./09-development.md) for the full dev workflow.

---

*Last updated: 2026-05-01*
