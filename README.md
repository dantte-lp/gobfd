# GoBFD

Production-grade BFD (Bidirectional Forwarding Detection) protocol daemon
written in Go. Implements RFC 5880 (base protocol), RFC 5881 (single-hop
IPv4/IPv6), RFC 5882 (generic application), and RFC 5883 (multihop).

GoBFD detects forwarding path failures between adjacent routers in
milliseconds, enabling fast convergence for BGP, OSPF, and other routing
protocols. It integrates with [GoBGP](https://github.com/osrg/gobgp)
via gRPC to trigger automatic route withdrawal on BFD session failure.

## Features

- RFC 5880 compliant BFD state machine with table-driven FSM
- RFC 5881 single-hop (UDP 3784, TTL=255 GTSM)
- RFC 5883 multihop (UDP 4784)
- Five authentication modes: Simple Password, Keyed MD5, Meticulous Keyed MD5, Keyed SHA1, Meticulous Keyed SHA1
- GoBGP integration for BFD-triggered BGP actions (RFC 5882)
- BFD flap dampening (RFC 5882 Section 3.2)
- ConnectRPC/gRPC API for session management
- CLI client (`gobfdctl`) with interactive shell
- Prometheus metrics and Grafana dashboard
- systemd integration (Type=notify, watchdog, socket activation)
- Hot reload via SIGHUP for session reconciliation
- Zero-allocation packet codec on the hot path
- Go 1.26 flight recorder for post-mortem debugging

## Quick Start

### Using Podman Compose (recommended for development)

```bash
# Start the development environment
podman-compose -f deployments/compose/compose.dev.yml up -d --build

# Build both binaries
podman-compose -f deployments/compose/compose.dev.yml exec dev go build ./cmd/gobfd
podman-compose -f deployments/compose/compose.dev.yml exec dev go build ./cmd/gobfdctl

# Run tests
podman-compose -f deployments/compose/compose.dev.yml exec dev go test ./... -race -count=1

# Run linter
podman-compose -f deployments/compose/compose.dev.yml exec dev golangci-lint run ./...
```

Or use the Makefile shortcuts:

```bash
make up        # start dev container
make build     # compile gobfd and gobfdctl
make test      # run tests with race detector
make lint      # run golangci-lint v2
make all       # build + test + lint
```

### Using the production stack

```bash
# Start gobfd with Prometheus and Grafana
podman-compose -f deployments/compose/compose.yml up -d

# Access services:
#   gobfd gRPC API:   localhost:50051
#   Prometheus:       http://localhost:9090
#   Grafana:          http://localhost:3000 (admin/admin)
```

### Using packages (deb/rpm)

```bash
# Install from .deb
sudo dpkg -i gobfd_*.deb

# Edit configuration
sudo vim /etc/gobfd/gobfd.yml

# Start the daemon
sudo systemctl enable --now gobfd

# Check status
sudo systemctl status gobfd
```

## CLI Usage

`gobfdctl` communicates with the gobfd daemon via ConnectRPC.

```bash
# List all BFD sessions
gobfdctl session list

# Show a specific session by peer address or discriminator
gobfdctl session show 10.0.0.1
gobfdctl session show 42

# Create a new single-hop BFD session
gobfdctl session add \
  --peer 10.0.0.1 \
  --local 10.0.0.2 \
  --interface eth0 \
  --type single-hop \
  --tx-interval 100ms \
  --rx-interval 100ms \
  --detect-mult 3

# Create a multihop session
gobfdctl session add \
  --peer 192.168.1.1 \
  --local 192.168.2.1 \
  --type multi-hop \
  --tx-interval 300ms \
  --detect-mult 5

# Delete a session by its local discriminator
gobfdctl session delete 42

# Stream live BFD events (state changes, session creation/deletion)
gobfdctl monitor

# Stream events, including a snapshot of current sessions first
gobfdctl monitor --current

# Use JSON output
gobfdctl session list --format json

# Connect to a remote daemon
gobfdctl --addr 10.0.0.1:50051 session list

# Interactive shell with tab completion
gobfdctl shell
```

## Configuration

GoBFD reads its configuration from a YAML file. All settings can be
overridden via environment variables with the `GOBFD_` prefix.

```yaml
grpc:
  addr: ":50051"

metrics:
  addr: ":9100"
  path: "/metrics"

log:
  level: "info"       # debug, info, warn, error
  format: "json"      # json, text

bfd:
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3

# GoBGP integration (RFC 5882 Section 4.3)
gobgp:
  enabled: true
  addr: "127.0.0.1:50052"
  strategy: "disable-peer"   # or "withdraw-routes"
  dampening:
    enabled: true
    suppress_threshold: 3
    reuse_threshold: 2
    max_suppress_time: "60s"
    half_life: "15s"

# Declarative sessions (reconciled on SIGHUP reload)
sessions:
  - peer: "10.0.0.1"
    local: "10.0.0.2"
    interface: "eth0"
    type: single_hop
    desired_min_tx: "100ms"
    required_min_rx: "100ms"
    detect_mult: 3
  - peer: "10.0.1.1"
    local: "10.0.1.2"
    type: multi_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 5
```

Environment variable examples:

```bash
GOBFD_GRPC_ADDR=:50051
GOBFD_METRICS_ADDR=:9100
GOBFD_LOG_LEVEL=debug
GOBFD_LOG_FORMAT=text
```

See [`configs/gobfd.example.yml`](configs/gobfd.example.yml) for the full
annotated example.

## Architecture

```
cmd/gobfd/main.go
  |-- internal/config       koanf/v2: YAML + env + flags
  |-- internal/server       ConnectRPC handler
  |     |-- internal/bfd    session Manager, FSM, packet codec
  |     +-- pkg/bfdpb       generated protobuf types
  |-- internal/netio        raw sockets, UDP listeners (3784/4784)
  |-- internal/gobgp        GoBGP gRPC client, flap dampening
  |-- internal/metrics      Prometheus collectors
  +-- internal/version      build info

cmd/gobfdctl/main.go
  +-- cmd/gobfdctl/commands  cobra CLI, ConnectRPC client, go-prompt shell
        +-- pkg/bfdpb        generated protobuf types
```

Key design decisions:

- **Table-driven FSM**: All state transitions are defined in a
  `map[stateEvent]transition` table matching RFC 5880 Section 6.8.6 exactly.
  No if-else chains.
- **Pre-built cached packets**: Following the FRR bfdd pattern, each session
  maintains a pre-serialized 24-byte BFD Control Packet that is rebuilt only
  on parameter changes, eliminating per-TX-interval allocations.
- **Two-tier demultiplexing**: Incoming packets are looked up first by
  YourDiscriminator (O(1) map), then by (SrcIP, DstIP, Interface) composite
  key for initial session establishment.
- **Zero-allocation hot path**: `sync.Pool` for packet buffers,
  `encoding/binary.BigEndian` on pre-allocated slices, no reflection.

See [`docs/architecture.md`](docs/architecture.md) for the full packet flow
diagrams, FSM state table, and timer negotiation details.

## Building from Source

Requirements:

- Go 1.26 or later
- buf CLI (for protobuf generation)
- golangci-lint v2 (for linting)
- Podman or Docker (for containerized builds)

```bash
# Clone
git clone https://github.com/dantte-lp/gobfd.git
cd gobfd

# Build binaries
go build -o bin/gobfd ./cmd/gobfd
go build -o bin/gobfdctl ./cmd/gobfdctl

# Build with version tag
go build -ldflags="-X github.com/dantte-lp/gobfd/internal/version.Version=v1.0.0" \
  -o bin/gobfd ./cmd/gobfd

# Run tests
go test ./... -race -count=1

# Lint
golangci-lint run ./...

# Generate protobuf code (after proto changes)
buf generate

# Build container image
podman build -f deployments/docker/Containerfile -t gobfd .

# Build debug image (Alpine with tcpdump, iproute2)
podman build -f deployments/docker/Containerfile.debug -t gobfd:debug .
```

## Monitoring

GoBFD exposes Prometheus metrics at the configured metrics endpoint
(default `:9100/metrics`).

Available metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `gobfd_bfd_sessions_total` | Gauge | Currently active BFD sessions |
| `gobfd_bfd_packets_sent_total` | Counter | BFD Control packets transmitted |
| `gobfd_bfd_packets_received_total` | Counter | BFD Control packets received |
| `gobfd_bfd_packets_dropped_total` | Counter | Packets dropped (validation/buffer) |
| `gobfd_bfd_state_transitions_total` | Counter | FSM state transitions |
| `gobfd_bfd_auth_failures_total` | Counter | Authentication failures |

A pre-built Grafana dashboard is included at
[`deployments/compose/configs/grafana/dashboards/bfd.json`](deployments/compose/configs/grafana/dashboards/bfd.json).

## RFC Compliance

| RFC | Title | Status |
|-----|-------|--------|
| RFC 5880 | BFD Base Protocol | Implemented (no Echo/Demand mode) |
| RFC 5881 | BFD for IPv4/IPv6 Single-Hop | Implemented |
| RFC 5882 | Generic Application of BFD | Implemented (BGP integration) |
| RFC 5883 | BFD for IPv4/IPv6 Multihop | Implemented |
| RFC 5884 | BFD for MPLS LSP | Stub interfaces |
| RFC 5885 | BFD for VCCV | Stub interfaces |
| RFC 7130 | Micro-BFD for LAG | Stub interfaces |

## License

Apache License 2.0
