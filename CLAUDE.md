# GoBFD — BFD Protocol Daemon

Go 1.26 implementation of Bidirectional Forwarding Detection (RFC 5880/5881).
Four binaries: `gobfd` (daemon), `gobfdctl` (CLI), `gobfd-haproxy-agent` (HAProxy bridge), `gobfd-exabgp-bridge` (ExaBGP bridge).

## Commands
```sh
make build                                         # сборка всех 4 бинарников с ldflags
go test ./... -race -count=1                       # тесты с race detector
go test -run '^TestFSMTransition$' ./internal/bfd  # один тест
golangci-lint run                                  # линтер (v2, строгий)
buf generate                                       # генерация proto
buf lint                                           # проверка proto
make interop                                       # interop tests (FRR + BIRD3 + aiobfd + Thoro, 4 peers)
make interop-bgp                                   # BGP+BFD tests (FRR, BIRD3, ExaBGP)
make int-bgp-failover                              # integration: BGP fast failover demo
make int-haproxy                                   # integration: HAProxy agent-check bridge
make int-observability                             # integration: Prometheus + Grafana
make int-exabgp-anycast                            # integration: ExaBGP anycast
make int-k8s                                       # integration: Kubernetes DaemonSet
```

## Architecture
- `internal/bfd/` — FSM (RFC 5880 §6.8), session management, packet codec, auth
- `internal/server/` — ConnectRPC server, interceptors
- `internal/netio/` — raw socket abstraction (Linux-specific), UDP listeners 3784/4784
- `internal/config/` — koanf/v2: YAML + env + flags
- `internal/metrics/` — Prometheus collectors for BFD sessions
- `internal/version/` — shared version package with ldflags injection (Version, GitCommit, BuildDate)
- `internal/gobgp/` — GoBGP integration handler (BFD↔BGP session coupling)
- `cmd/gobfd/` — daemon entry point (signal handling, graceful shutdown)
- `cmd/gobfdctl/` — CLI: cobra (non-interactive) + reeflective/console (interactive shell)
- `cmd/gobfd-haproxy-agent/` — HAProxy agent-check bridge (BFD state → agent TCP responses)
- `cmd/gobfd-exabgp-bridge/` — ExaBGP process API bridge (BFD state → route announcements)
- `pkg/bfdpb/` — generated protobuf types (public API for external consumers)
- `api/v1/` — proto definitions (buf managed)
- `test/interop/` — 4-peer interop tests (FRR, BIRD3, aiobfd, Thoro/bfd) with tshark capture
- `test/interop-bgp/` — BGP+BFD interop tests (GoBGP + FRR, BIRD3, ExaBGP)
- `test/interop-clab/` — Containerlab vendor NOS interop tests (Nokia, Arista, FRR)
- `deployments/integrations/` — 5 integration examples (BGP failover, HAProxy, observability, ExaBGP, k8s)

## Code style
- Errors: always wrap with `%w` and context: `fmt.Errorf("send control packet to %s: %w", peer, err)`
- Use `errors.Is`/`errors.As`, never string matching
- Context: first param, never store in struct
- Concurrency: sender closes channels; tie goroutine lifetime to context.Context
- Logging: ONLY `log/slog` with structured fields, NEVER `fmt.Println` or `log`
- Naming: avoid stutter (`package bfd; type Session` not `BFDSession`)
- Imports: stdlib → blank line → external → blank line → internal
- Interfaces: small, near consumers, composition over inheritance
- Tests: table-driven, `t.Parallel()` where safe, always `-race`
- FSM: all state transitions MUST match RFC 5880 §6.8.6 exactly
- Zero allocation: hot paths (packet codec, FSM, timers, session event loop) MUST be 0 allocs/op in benchmarks
- No duplication: extract shared logic into reusable functions; session types share packet codec, timer, FSM infrastructure via composition and interfaces
- Go 1.26 best practices: use `testing/synctest` for timer tests, `runtime/trace.FlightRecorder` for debugging, `os.Root` for sandboxed I/O, `GOMEMLIMIT`+`GOGC=off` for bounded memory, `weak.Pointer` for caches, range-over-func iterators, `slices`/`maps`/`cmp` stdlib packages

## Git
- Commits: NEVER add Co-Authored-By or any AI/Claude mentions in commit messages
- Module: `github.com/dantte-lp/gobfd` — owner dantte-lp, NOT wolfguard

## Important: don't
- NEVER modify generated files in `pkg/bfdpb/` — regenerate with `buf generate`
- NEVER use `unsafe` package — this is a network daemon handling untrusted input
- NEVER skip error checks on socket operations in `internal/netio/`
- NEVER add dependencies without checking: `go mod tidy && govulncheck ./...`
- Timer intervals in BFD are in MICROSECONDS per RFC — don't confuse with milliseconds
- See `docs/en/01-architecture.md` for connection lifecycle and FSM state diagram
- See `docs/en/08-rfc-compliance.md` for implementation decisions per RFC section
