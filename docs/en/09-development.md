# Development

![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![golangci--lint](https://img.shields.io/badge/golangci--lint-v2-1a73e8?style=for-the-badge)
![buf](https://img.shields.io/badge/buf-Protobuf-4353FF?style=for-the-badge)
![Podman](https://img.shields.io/badge/Podman-Dev_Container-892CA0?style=for-the-badge&logo=podman)
![synctest](https://img.shields.io/badge/synctest-Virtual_Time-34a853?style=for-the-badge)

> Development workflow, Make targets, testing strategy, linting, protobuf generation, and contribution guidelines.

---

### Table of Contents

- [Prerequisites](#prerequisites)
- [Development Setup](#development-setup)
- [Make Targets](#make-targets)
- [Testing Strategy](#testing-strategy)
- [Linting](#linting)
- [Protobuf Workflow](#protobuf-workflow)
- [Go 1.26 Features](#go-126-features)
- [Code Conventions](#code-conventions)
- [Contributing](#contributing)

### Prerequisites

- **Podman** + **Podman Compose** (all commands run inside containers)
- **Git** for version control
- Go 1.26 (only needed for local IDE support; builds run in containers)

> **Important**: All testing, building, scanning, and linting runs inside Podman containers. No local Go toolchain is required for CI-equivalent builds.

### Development Setup

```bash
# Clone the repository
git clone https://github.com/dantte-lp/gobfd.git
cd gobfd

# Start the development container
make up

# Build all binaries
make build

# Run all tests
make test

# Run linter
make lint

# All at once
make all
```

### Make Targets

All Go commands run inside Podman containers via `podman-compose exec`.

#### Lifecycle

| Target | Description |
|---|---|
| `make up` | Start development container |
| `make down` | Stop development container |
| `make restart` | Restart (down + up) |
| `make logs` | Follow development container logs |
| `make shell` | Open bash in development container |

#### Build and Test

| Target | Description |
|---|---|
| `make all` | Build + test + lint |
| `make build` | Compile all binaries with version info |
| `make test` | Run all tests with `-race -count=1` |
| `make test-v` | Verbose test output |
| `make test-run RUN=TestFSM PKG=./internal/bfd` | Run specific test |
| `make fuzz FUNC=FuzzControlPacket PKG=./internal/bfd` | Fuzz test (60s) |
| `make test-integration` | Run integration tests |

#### Interoperability Testing

| Target | Description |
|---|---|
| `make interop` | Full cycle: build + start + test + cleanup |
| `make interop-up` | Start 4-peer topology |
| `make interop-test` | Run interop Go tests |
| `make interop-down` | Stop and cleanup |
| `make interop-logs` | Follow interop container logs |
| `make interop-capture` | Live BFD packet capture |
| `make interop-pcap` | Decode captured packets |
| `make interop-pcap-summary` | CSV summary of captures |
| `make interop-bgp` | Full cycle BGP+BFD tests (FRR, BIRD3, ExaBGP) |
| `make interop-bgp-up` | Start BGP+BFD topology |
| `make interop-bgp-test` | Run BGP+BFD Go tests |
| `make interop-bgp-down` | Stop BGP+BFD topology |
| `make interop-clab` | Full cycle vendor NOS tests (Nokia, FRR, etc.) |
| `make interop-clab-up` | Deploy vendor NOS topology |
| `make interop-clab-test` | Run vendor interop Go tests |
| `make interop-clab-down` | Destroy vendor NOS containers |

#### Integration Examples

| Target | Description |
|---|---|
| `make int-bgp-failover` | BGP fast failover demo (GoBFD + GoBGP + FRR) |
| `make int-haproxy` | HAProxy agent-check bridge demo |
| `make int-observability` | Prometheus + Grafana observability stack |
| `make int-exabgp-anycast` | ExaBGP anycast service announcement |
| `make int-k8s` | Kubernetes DaemonSet with GoBGP sidecar |

#### Quality

| Target | Description |
|---|---|
| `make lint` | Run golangci-lint v2 |
| `make lint-fix` | Auto-fix lint issues |
| `make semgrep` | Run local Semgrep OSS scan with `p/golang` rules |
| `make semgrep-json` | Run Semgrep OSS scan and emit JSON |
| `make semgrep-pro` | Run Semgrep with `--pro`; requires Semgrep Pro Engine and `semgrep login` |
| `make vulncheck` | Run the controlled vulnerability audit (`govulncheck` + OSV Scanner) |
| `make osv-scan` | Alias for the controlled vulnerability audit |
| `make vulncheck-strict` | Run raw `govulncheck ./...` without the project allowlist |
| `make osv-scan-strict` | Run raw `osv-scanner scan -r .` without the project allowlist |

#### Protobuf

| Target | Description |
|---|---|
| `make proto-gen` | Generate Go code from `.proto` files |
| `make proto-lint` | Lint protobuf definitions |
| `make proto-breaking` | Check for breaking changes |
| `make proto-update` | Update buf dependencies |

#### Dependencies

| Target | Description |
|---|---|
| `make tidy` | Run `go mod tidy` |
| `make download` | Download module dependencies |
| `make clean` | Remove binaries and caches |
| `make versions` | Show tool versions |

### Testing Strategy

#### Unit Tests

- **Table-driven** tests for all packages
- **`t.Parallel()`** where safe (no shared mutable state)
- **Always** run with `-race -count=1`
- **`goleak.VerifyTestMain(m)`** in every package for goroutine leak detection

#### FSM Tests (`testing/synctest`)

Go 1.26 `testing/synctest` enables deterministic time-based testing:

```go
func TestFSMDetectionTimeout(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        sess := newTestSession(t, SessionConfig{
            DesiredMinTxInterval:  100 * time.Millisecond,
            RequiredMinRxInterval: 100 * time.Millisecond,
            DetectMultiplier:      3,
        })

        // Bring session to Up state
        sess.injectPacket(controlPacket(StateInit, 0))
        synctest.Wait()
        require.Equal(t, StateUp, sess.State())

        // Detection timeout = 3 x 100ms = 300ms
        time.Sleep(350 * time.Millisecond) // virtual time
        synctest.Wait()
        require.Equal(t, StateDown, sess.State())
    })
}
```

Benefits:
- Tests run in virtual time (instant execution)
- Deterministic -- no flaky timer-dependent tests
- Perfect for BFD protocol timers and detection timeouts

#### Fuzz Testing

GoBFD includes fuzz tests for all packet parsers that handle untrusted network input:

```bash
# BFD Control packet codec
make fuzz FUNC=FuzzControlPacket PKG=./internal/bfd

# VXLAN overlay codec (RFC 8971)
make fuzz FUNC=FuzzVXLANHeader PKG=./internal/netio

# Geneve overlay codec (RFC 9521)
make fuzz FUNC=FuzzGeneveHeader PKG=./internal/netio

# Inner packet assembly/disassembly
make fuzz FUNC=FuzzInnerPacket PKG=./internal/netio
```

Each fuzz test has two variants:
- **Round-trip**: verifies `parse(marshal(packet)) == packet` for structured inputs
- **Raw input**: feeds arbitrary bytes to the parser, verifying it never panics

The default fuzz duration is 60 seconds. To run longer:

```bash
make fuzz FUNC=FuzzVXLANHeader PKG=./internal/netio FUZZTIME=300s
```

#### Integration Tests

```bash
make test-integration
```

Uses `testcontainers-go` with Podman backend for testing the full daemon lifecycle.

#### Interoperability Tests

See [05-interop.md](./05-interop.md) for the 4-peer interop testing framework.

### Linting

golangci-lint v2 with a strict curated configuration:

```bash
make lint
```

Configuration in `.golangci.yml`. Key linters enabled:
- `gosec` (with `audit: true`) -- security analysis
- `govet`, `staticcheck`, `errcheck` -- standard Go checks
- `noctx` -- context propagation checks
- `exhaustive` -- exhaustive switch/map checks
- `depguard`, `gomoddirectives` -- dependency hygiene
- `nolintlint` -- quality of `//nolint` directives

### Semgrep

Semgrep is used as an additional local SAST pass:

```bash
make semgrep       # Semgrep OSS, p/golang ruleset
make semgrep-json  # same scan, JSON output
make semgrep-pro   # requires Semgrep Pro Engine and semgrep login
```

Per the Semgrep CLI reference, `semgrep scan` is the local scan command and can
run registry rulesets such as `p/golang` without a Semgrep account. `semgrep ci`
uses Semgrep App policies, diff-aware CI behavior, and Pro analysis when the
CLI is logged in. The `--pro` flag enables interfile analysis and requires the
Pro Engine plus authentication.

Current accepted Semgrep warnings are documented in [SECURITY.md](../../SECURITY.md):
MD5 and SHA1 are implemented only for RFC 5880 authentication interoperability.

### Protobuf Workflow

Protobuf is managed by `buf`:

```bash
# After modifying api/bfd/v1/bfd.proto:
make proto-lint      # Lint definitions
make proto-gen       # Generate Go code to pkg/bfdpb/
make proto-breaking  # Check for breaking changes vs master
```

> **NEVER** edit files in `pkg/bfdpb/` manually -- they are generated by `buf generate`.

### Go 1.26 Features

GoBFD leverages several Go 1.26 features for safety, performance, and debugging:

#### `testing/synctest` -- Deterministic Timer Tests

All BFD timer and detection timeout tests use `testing/synctest` for virtual-time execution. Tests run instantly (no real sleeps) and are fully deterministic. See [FSM Tests](#fsm-tests-testingsynctest) above.

#### `os.Root` -- Sandboxed File Access

Configuration file loading uses `os.OpenRoot` to sandbox filesystem access within the config directory. This prevents path traversal attacks where a malicious config path could read arbitrary files:

```go
root, err := os.OpenRoot(filepath.Dir(path))
if err != nil { return nil, err }
defer root.Close()
f, err := root.Open(filepath.Base(path))
```

Applied in `config.Load` and `gobfd-haproxy-agent` `loadConfig`.

#### `errors.AsType[T]()` -- Type-Safe Error Matching

Server tests use the Go 1.26 generic error matcher instead of the two-step `errors.As` pattern:

```go
// Go 1.26 idiomatic
if connectErr, ok := errors.AsType[*connect.Error](err); ok {
    require.Equal(t, connect.CodeNotFound, connectErr.Code())
}
```

#### `GOEXPERIMENT=goroutineleakprofile`

The dev container (`Containerfile.dev`) enables the goroutine leak profiler experiment. Combined with `goleak.VerifyTestMain(m)` in test packages, this provides runtime detection of leaked goroutines during development.

#### `runtime/trace.FlightRecorder`

An HTTP endpoint exposes the flight recorder for post-mortem trace capture. When enabled, the daemon continuously records the last N seconds of trace data, which can be dumped on demand for debugging latency spikes or deadlocks.

#### Swiss Tables

Go 1.26 uses Swiss tables as the default `map` implementation. GoBFD's discriminator lookup, FSM transition table, and session demuxing benefit from improved cache locality. See [BENCHMARKS.md](../../BENCHMARKS.md) for comparison with `GOEXPERIMENT=noswissmap`.

### Code Conventions

| Rule | Description |
|---|---|
| **Errors** | Always wrap with `%w` and context: `fmt.Errorf("send control packet to %s: %w", peer, err)` |
| **Error matching** | Use `errors.Is`/`errors.As`, never string matching |
| **Context** | First parameter, never stored in struct |
| **Goroutines** | Sender closes channels; tie lifetime to `context.Context` |
| **Logging** | ONLY `log/slog` with structured fields |
| **Naming** | Avoid stutter: `package bfd; type Session` not `BFDSession` |
| **Imports** | stdlib, blank line, external, blank line, internal |
| **Interfaces** | Small, near consumers |
| **Tests** | Table-driven, `t.Parallel()` where safe, always `-race` |
| **FSM** | All transitions MUST match RFC 5880 Section 6.8.6 exactly |
| **Timers** | BFD intervals in MICROSECONDS per RFC -- never confuse with ms |

### Contributing

1. Open an issue to discuss the change before submitting a PR
2. Follow the existing code style (see `CLAUDE.md` for conventions)
3. Add tests for new functionality (`go test ./... -race -count=1`)
4. Ensure `golangci-lint run ./...` passes
5. Run `buf lint` if proto files are modified
6. Keep commit messages descriptive and concise

```bash
# Development loop
make up           # Start dev environment
# ... make changes ...
make all          # Build + test + lint

# For protocol changes:
make interop      # Verify interop with 4 peers

# For proto changes:
make proto-gen    # Regenerate Go code
make proto-lint   # Lint proto definitions
```

### Related Documents

- [01-architecture.md](./01-architecture.md) -- System architecture and package structure
- [05-interop.md](./05-interop.md) -- Interoperability testing
- [CLAUDE.md](../../CLAUDE.md) -- Full code conventions and commands

---

*Last updated: 2026-02-24*
