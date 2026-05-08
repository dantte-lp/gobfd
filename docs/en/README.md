# GoBFD Documentation

![Version](https://img.shields.io/badge/Version-0.5.2-1a73e8?style=for-the-badge)
![Language](https://img.shields.io/badge/Lang-English-ea4335?style=for-the-badge)
![Status](https://img.shields.io/badge/Status-Active-brightgreen?style=for-the-badge)

> Canonical technical documentation for **GoBFD** -- a production-oriented BFD protocol daemon (RFC 5880/5881) written in Go 1.26.

---

## Documentation Map

```mermaid
graph TD
    IDX["docs/en/README.md"]

    subgraph "Architecture"
        A1["01-architecture.md"]
        A2["02-protocol.md"]
    end

    subgraph "Operations"
        B1["03-configuration.md"]
        B2["04-cli.md"]
        B3["06-deployment.md"]
        B4["15-security.md"]
        B5["16-production-runbooks.md"]
    end

    subgraph "Testing & Quality"
        C1["05-interop.md"]
        C2["07-monitoring.md"]
        C3["09-development.md"]
    end

    subgraph "Performance"
        E1["12-benchmarks.md"]
        E2["13-competitive-analysis.md"]
        E3["14-performance-analysis.md"]
    end

    subgraph "Reference"
        D1["08-rfc-compliance.md"]
        D2["10-changelog.md"]
        D3["11-integrations.md"]
        D4["adr/"]
        D5["reference/"]
        D6["rfc/"]
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
    IDX --> D2
    IDX --> D3
    IDX --> D4
    IDX --> D5
    IDX --> D6
    IDX --> E1
    IDX --> E2
    IDX --> E3

    A1 --> A2
    A2 --> D1
    B1 --> B2
    B3 --> C2
    C1 --> C3
    D1 --> D6
    E1 --> E2
    E2 --> E3

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
| 04 | [**CLI Reference**](./04-cli.md) | `gobfdctl` commands, interactive shell, output formats |
| 05 | [**Interop Testing**](./05-interop.md) | Interop suites: 4-peer, BGP+BFD, RFC-specific, vendor NOS |
| 06 | [**Deployment**](./06-deployment.md) | systemd, Podman Compose, container image, production |
| 07 | [**Monitoring**](./07-monitoring.md) | Prometheus metrics, Grafana dashboard, alerting |
| 15 | [**Security Policy**](./15-security.md) | API, GoBGP, BFD auth, container, and vulnerability policy |
| 16 | [**Production Runbooks**](./16-production-runbooks.md) | Kubernetes, BGP, Prometheus, packet verification, failure drills |

### Reference

| # | Document | Description |
|---|---|---|
| 08 | [**RFC Compliance**](./08-rfc-compliance.md) | RFC compliance matrix, implementation notes, links to RFCs |
| 09 | [**Development**](./09-development.md) | Dev workflow, Make targets, testing, linting, proto generation |
| 10 | [**Changelog Guide**](./10-changelog.md) | How to maintain CHANGELOG.md, release process, semantic versioning |
| 11 | [**Integrations**](./11-integrations.md) | BGP failover, HAProxy, observability, ExaBGP, Kubernetes examples |

### Performance

| # | Document | Description |
|---|---|---|
| 12 | [**Benchmarks**](./12-benchmarks.md) | How to run, read, and interpret benchmark results |
| 13 | [**Competitive Analysis**](./13-competitive-analysis.md) | Comparison with FRR, BIRD, aiobfd, hardware platforms |
| 14 | [**Performance Analysis**](./14-performance-analysis.md) | GoBFD vs C implementations: benchmarks, architecture, CPU load behavior |

### Architecture Decision Records

Living decision records under [`adr/`](./adr/README.md). Each record states
context, decision, and consequences. Records carry an immutable date and
move to `Superseded` when replaced.

### Reference Material

Living technical references under [`reference/`](./reference/README.md):
dependency risk register and other auxiliary material.

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
git clone https://github.com/dantte-lp/gobfd.git && cd gobfd
make build
make test
make up
```

See [06-deployment.md](./06-deployment.md) for production deployment and
[09-development.md](./09-development.md) for the full dev workflow.
