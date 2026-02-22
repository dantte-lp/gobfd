# Architecture

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![RFC 5880](https://img.shields.io/badge/RFC-5880-1a73e8?style=for-the-badge)
![ConnectRPC](https://img.shields.io/badge/ConnectRPC-gRPC-ea4335?style=for-the-badge)
![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C?style=for-the-badge&logo=prometheus)
![Linux](https://img.shields.io/badge/Linux-Raw_Sockets-FCC624?style=for-the-badge&logo=linux&logoColor=black)

> System architecture of GoBFD: package dependencies, packet flow, session lifecycle, and goroutine model.

---

### Table of Contents

- [System Overview](#system-overview)
- [Package Dependency Diagram](#package-dependency-diagram)
- [Dependency Rules](#dependency-rules)
- [Packet RX Flow](#packet-rx-flow)
- [Packet TX Flow](#packet-tx-flow)
- [Demultiplexing](#demultiplexing)
- [Three-Way Handshake](#three-way-handshake)
- [Goroutine Model](#goroutine-model)
- [Graceful Shutdown](#graceful-shutdown)
- [Project Structure](#project-structure)
- [Technology Stack](#technology-stack)

### System Overview

GoBFD is a production-grade BFD (Bidirectional Forwarding Detection) protocol daemon. It consists of two binaries:

- **gobfd** -- the daemon that manages BFD sessions, sends/receives BFD Control packets, and integrates with GoBGP
- **gobfdctl** -- the CLI client that communicates with gobfd via ConnectRPC

```mermaid
graph TB
    subgraph "gobfd daemon"
        MAIN["cmd/gobfd<br/>main.go"]
        CFG["internal/config<br/>koanf/v2"]
        SRV["internal/server<br/>ConnectRPC"]
        BFD["internal/bfd<br/>FSM + Sessions"]
        NET["internal/netio<br/>Raw Sockets"]
        MET["internal/metrics<br/>Prometheus"]
        BGP["internal/gobgp<br/>GoBGP Client"]
        PB["pkg/bfdpb<br/>Generated Proto"]
    end

    subgraph "gobfdctl CLI"
        CLI["cmd/gobfdctl<br/>Cobra + reeflective/console"]
    end

    subgraph "External"
        GOBGP["GoBGP<br/>gRPC :50052"]
        PROM["Prometheus<br/>:9100/metrics"]
        PEER["BFD Peers<br/>UDP 3784/4784"]
    end

    MAIN --> CFG
    MAIN --> SRV
    MAIN --> NET
    MAIN --> MET
    MAIN --> BGP
    SRV --> BFD
    SRV --> PB
    NET --> BFD
    CLI --> SRV
    BGP --> GOBGP
    MET --> PROM
    NET --> PEER

    style BFD fill:#1a73e8,color:#fff
    style NET fill:#34a853,color:#fff
    style SRV fill:#ea4335,color:#fff
```

### Package Dependency Diagram

```mermaid
graph TB
    subgraph "cmd/"
        GOBFD["cmd/gobfd<br/>main.go"]
        GOBFDCTL["cmd/gobfdctl<br/>main.go + commands/"]
    end

    subgraph "internal/"
        CFG["config<br/>koanf/v2"]
        SRV["server<br/>ConnectRPC"]
        BFD["bfd<br/>FSM, Session,<br/>Packet, Auth"]
        NET["netio<br/>Raw Sockets,<br/>UDP Listeners"]
        MET["metrics<br/>Prometheus"]
        BGP["gobgp<br/>gRPC Client"]
        VER["version<br/>Build Info"]
    end

    PB["pkg/bfdpb<br/>Generated Proto"]

    GOBFD --> CFG
    GOBFD --> SRV
    GOBFD --> NET
    GOBFD --> MET
    GOBFD --> BGP
    GOBFD --> VER
    SRV --> BFD
    SRV --> PB
    NET --> BFD
    GOBFDCTL --> PB

    style BFD fill:#1a73e8,color:#fff
```

### Dependency Rules

- `internal/bfd` has **zero** dependency on `internal/server`, `internal/netio`, or `internal/config`
- `internal/server` depends on `internal/bfd` (Manager, Session, types) and `pkg/bfdpb`
- `internal/netio` depends on `internal/bfd` only for the `PacketSender` interface and `ControlPacket`
- `pkg/bfdpb` is generated code -- never edited manually

### Packet RX Flow

```mermaid
flowchart TD
    NET["Network<br/>UDP 3784 / 4784"] --> LISTEN["netio.Listener<br/>ReadBatch<br/>PacketPool.Get()"]
    LISTEN --> UNMARSHAL["bfd.UnmarshalControlPacket<br/>RFC 5880 steps 1-7<br/>version, length, detect mult,<br/>multipoint, discriminators"]
    UNMARSHAL --> DEMUX["Manager.DemuxWithWire<br/>Tier 1: YourDiscr (O1 map)<br/>Tier 2: PeerKey (SrcIP, DstIP, If)"]
    DEMUX --> RECV["Session.RecvPkt<br/>buffered chan"]
    RECV --> HANDLE["handleRecvPacket<br/>RFC 5880 steps 8-18:<br/>auth, FSM event, timer reset"]
```

The 13-step validation from RFC 5880 Section 6.8.6 is split across two layers:

| Layer | Steps | Responsibility |
|---|---|---|
| **Codec** (`packet.go`) | 1-7 | Version, length, detect mult, multipoint, discriminators (stateless) |
| **Session** (`session.go`) | 8-18 | Auth verification, FSM event, timer update, state variable update |

This split allows the listener to discard invalid packets before any session lock is acquired.

### Packet TX Flow

```mermaid
flowchart TD
    TIMER["txTimer fires<br/>jittered per RFC 5880 6.8.7"] --> CHECK["maybeSendControl<br/>passive role check,<br/>RemoteMinRx check"]
    CHECK --> REBUILD["rebuildCachedPacket<br/>pre-serialized 24-byte header<br/>rebuilt only on param change"]
    REBUILD --> SEND["PacketSender.SendPacket<br/>raw UDP socket"]
```

**Cached Packet Pattern** (inspired by FRR bfdd): each session maintains a pre-serialized `cachedPacket []byte` that is rebuilt only when parameters change (state transition, Poll/Final, timer negotiation). On each TX interval, the cached bytes are sent directly without re-encoding. For authenticated sessions, the auth sequence number is updated in the cached packet on each transmission without full re-serialization.

### Demultiplexing

Two-tier lookup per RFC 5880 Section 6.8.6:

1. **Tier 1** -- Your Discriminator is nonzero: O(1) map lookup by discriminator. Fast path for established sessions.
2. **Tier 2** -- Your Discriminator is zero AND state is Down/AdminDown: lookup by composite key (SrcIP, DstIP, Interface). Used only during initial session establishment.

### Three-Way Handshake

BFD sessions use a three-way handshake (RFC 5880 Section 6.2):

```mermaid
sequenceDiagram
    participant A as Peer A (Down)
    participant B as Peer B (Down)

    A->>B: Control(State=Down)
    Note over B: Down -> Init
    B->>A: Control(State=Down)
    Note over A: Down -> Init

    A->>B: Control(State=Init)
    Note over B: Init -> Up
    B->>A: Control(State=Init)
    Note over A: Init -> Up

    A->>B: Control(State=Up)
    B->>A: Control(State=Up)
    Note over A,B: Both peers Up
```

FSM transitions in sequence:

1. A(Down) sends State=Down. B(Down) receives State=Down --> B transitions to Init.
2. B(Init) sends State=Init. A(Down) receives State=Init --> A transitions to Up.
3. A(Up) sends State=Up. B(Init) receives State=Up --> B transitions to Up.

### Goroutine Model

Each BFD session runs as an independent goroutine with its own timers and state. The goroutine lifetime is bound to a `context.Context` from the Manager.

```mermaid
graph TB
    subgraph "Manager"
        M["Manager goroutine<br/>session CRUD"]
    end

    subgraph "Session N goroutines"
        S1["Session 1<br/>TX timer + RX channel"]
        S2["Session 2<br/>TX timer + RX channel"]
        SN["Session N<br/>TX timer + RX channel"]
    end

    subgraph "Shared"
        L["netio.Listener<br/>ReadBatch goroutine"]
        R["netio.Receiver<br/>demux + dispatch"]
    end

    L --> R
    R --> S1
    R --> S2
    R --> SN
    M --> S1
    M --> S2
    M --> SN
```

### Graceful Shutdown

On SIGTERM/SIGINT (RFC 5880 Section 6.8.16):

1. `Manager.DrainAllSessions()` -- set all sessions to AdminDown with Diag = Administratively Down (7)
2. Wait 2x TX interval for final AdminDown packets to transmit
3. `Manager.Close()` -- cancel all session goroutines
4. Close listener sockets
5. Shut down HTTP servers (gRPC, metrics)

This ensures remote peers see AdminDown rather than a detection timeout, preventing unnecessary BGP route withdrawals.

### Project Structure

```
gobfd/
+-- api/bfd/v1/bfd.proto          # Protobuf service definitions (buf managed)
+-- cmd/
|   +-- gobfd/main.go             # Daemon entry point
|   +-- gobfdctl/                 # CLI client
|       +-- main.go
|       +-- commands/             # Cobra commands + reeflective/console shell
+-- internal/
|   +-- bfd/                      # Core protocol (FSM, session, packet, auth)
|   +-- config/                   # koanf/v2 configuration
|   +-- gobgp/                    # GoBGP gRPC client + flap dampening
|   +-- metrics/                  # Prometheus collectors
|   +-- netio/                    # Raw sockets, UDP listeners (Linux)
|   +-- server/                   # ConnectRPC server + interceptors
|   +-- version/                  # Build info
+-- pkg/bfdpb/                    # Generated protobuf types (public API)
+-- test/interop/                 # 4-peer interop tests
+-- configs/                      # Example configuration
+-- deployments/
|   +-- compose/                  # Podman Compose (dev + prod stacks)
|   +-- docker/                   # Containerfile + debug image
|   +-- systemd/                  # systemd unit file
|   +-- nfpm/                     # deb/rpm install scripts
+-- docs/                         # Documentation + RFC texts
```

### Technology Stack

| Component | Technology | Purpose |
|---|---|---|
| Language | Go 1.26 | Green Tea GC, `testing/synctest`, flight recorder |
| Network I/O | `x/net/ipv4`, `x/net/ipv6`, `x/sys/unix` | Raw sockets, TTL control, `SO_BINDTODEVICE` |
| RPC Server | ConnectRPC | gRPC + Connect + gRPC-Web from one handler |
| RPC Client | `google.golang.org/grpc` | GoBGP integration (gRPC client) |
| CLI | Cobra + reeflective/console | Non-interactive + interactive shell |
| Configuration | koanf/v2 | YAML + env vars + flags, hot reload |
| Metrics | Prometheus `client_golang` | Counters, gauges, histograms |
| Logging | `log/slog` (stdlib) | Structured JSON/text logging |
| Protobuf | buf CLI | Lint, breaking detection, code generation |
| Lint | golangci-lint v2 | 35+ linters, strict configuration |
| Release | GoReleaser v2 | Binaries + deb/rpm + container images |
| Containers | Podman + Podman Compose | Development and testing |
| systemd | Type=notify, watchdog | Production daemon lifecycle |

### Related Documents

- [02-protocol.md](./02-protocol.md) -- BFD protocol details (FSM, timers, packet format)
- [03-configuration.md](./03-configuration.md) -- Configuration reference
- [06-deployment.md](./06-deployment.md) -- Production deployment
- [09-development.md](./09-development.md) -- Development workflow

---

*Last updated: 2026-02-21*
