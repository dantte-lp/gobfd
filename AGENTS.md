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

## Go 1.27 migration guardrails
The current baseline remains Go 1.26.3. Do not use Go 1.27 syntax or APIs until
the `go` and `toolchain` directives plus every first-party compiler pin in
Containerfiles, CI, release workflows, and test harnesses move together and the
full quality gates pass. Use the official
[Go 1.27 release notes](https://go.dev/doc/go1.27) as the source of truth.

- Remove `GOEXPERIMENT=goroutineleakprofile` before switching toolchains; the
  `goroutineleak` pprof profile is generally available in Go 1.27. Keep
  `go.uber.org/goleak` test coverage and never expose all `net/http/pprof`
  handlers on the public metrics listener.
- Keep the default `go test` `stdversion` vet check enabled. Do not use
  `-vet=off` to bypass APIs newer than the module or file build constraint.
- Audit every repository `GODEBUG` value before the upgrade. Removed settings
  no longer accept historical values in Go 1.27; never keep a removed setting
  merely to silence the startup failure.
- During the toolchain upgrade, compatibility-test all existing `encoding/json`
  v1 consumers in the CLI, Podman, FRR, and audit code because Go 1.27 backs v1
  with the v2 implementation. Cover duplicate object members, invalid UTF-8,
  and error compatibility without matching exact error text. Adopt the
  `encoding/json/v2` API only deliberately; use `GOEXPERIMENT=nojsonv2` only for
  diagnosis, never in shipped builds.
- In a `synctest` bubble, replace only adjacent `time.Sleep(d)` plus
  `synctest.Wait()` calls with `synctest.Sleep(d)`. E2E and interoperability
  waits use real time and must remain context-bounded wall-clock waits.
- After adopting Go 1.27, configure and test one named `MaxHeaderValueCount`
  bound on both the ConnectRPC and metrics HTTP servers; retain the existing
  `ReadHeaderTimeout` defense.
- Prefer `httptest.NewTestServer(t, handler)` after the upgrade when a test does
  not require real socket behavior. Retain explicit loopback or Unix listeners
  when the listener itself is under test.
- Benchmark packet codec, FSM, timers, and session event loop before and after
  the upgrade; preserve 0 allocs/op and compare binary size. Use
  `GOEXPERIMENT=nosizespecializedmalloc` only to diagnose allocator changes,
  never in release artifacts.
- Use generic methods only on concrete internal types when they remove an
  existing abstraction cost. Do not churn public protobuf APIs or interfaces;
  interface methods still cannot declare type parameters.
- Do not adopt experimental `simd` or `simd/archsimd`, and never use `unsafe`.
  Reconsider SIMD only after its API is stable and both linux/amd64 and
  linux/arm64 benchmarks prove a material codec benefit.
- Expect `go mod tidy` under a Go 1.27 module to consolidate `require` blocks.
  Preserve dependency comments and require `go mod tidy -diff` to be clean.
- Treat goroutine labels, tracebacks, traces, and profiles as sensitive data.
  Keep analysis HTTP UIs on loopback and never put secrets or peer addresses
  into goroutine labels.

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
