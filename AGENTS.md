# GoBFD — BFD Protocol Daemon

Go 1.27 implementation of Bidirectional Forwarding Detection (RFC 5880/5881).
Four binaries: `gobfd` (daemon), `gobfdctl` (CLI), `gobfd-haproxy-agent` (HAProxy bridge), `gobfd-exabgp-bridge` (ExaBGP bridge).

## Commands
```sh
make build                                         # сборка всех 4 бинарников с ldflags
go test ./... -race -count=1                       # тесты с race detector
go test -run '^TestFSMTransition$' ./internal/bfd  # один тест
go tool -modfile=tools/go.mod golangci-lint run ./... # линтер (v2, строгий)
buf generate                                       # генерация proto
buf lint                                           # проверка proto
make interop                                       # interop tests (FRR + BIRD3 + Holo + Thoro/bfd, 4 peers)
make interop-testcontainers                        # Go-owned Podman lifecycle for the same 4-peer gate
make interop-bgp                                   # BGP+BFD tests (FRR, BIRD3, ExaBGP)
make interop-bgp-testcontainers                    # Go-owned Podman lifecycle for the BGP+BFD gate
make interop-rfc-testcontainers                    # Go-owned Podman lifecycle for RFC 7419/9384/9468/9747
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
- `api/bfd/v1/` — proto definitions (buf managed)
- `test/interop/` — 4-peer interop tests (FRR, BIRD3, Holo, Thoro/bfd) with tshark capture
- `test/interop-bgp/` — BGP+BFD interop tests (GoBGP + FRR, BIRD3, ExaBGP)
- `test/interop-clab/` — Containerlab vendor NOS interop tests (Nokia, Arista, FRR)
- `deployments/integrations/` — 5 integration examples (BGP failover, HAProxy, observability, ExaBGP, k8s)
- `tools/` — isolated Go tool module; never add developer tools to the runtime `go.mod`

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
- Go 1.27 best practices: use `testing/synctest` and `synctest.Sleep` for virtual-time timer tests, `runtime/trace.FlightRecorder` for debugging, `os.Root` for sandboxed I/O, `GOMEMLIMIT`+`GOGC=off` for bounded memory, generic methods only when justified, range-over-func iterators, and `slices`/`maps`/`cmp` stdlib packages

## Go 1.27 project rules
The baseline is `go 1.27` with `toolchain go1.27.0`. Keep every first-party
compiler pin in Containerfiles, CI, release workflows, and test harnesses on
that exact toolchain. Use the official
[Go 1.27 release notes](https://go.dev/doc/go1.27) as the source of truth.

- Never reintroduce the removed `GOEXPERIMENT=goroutineleakprofile` or
  `GOEXPERIMENT=noswissmap` switches. The `goroutineleak` pprof profile is
  generally available in Go 1.27. Keep
  `go.uber.org/goleak` test coverage and never expose all `net/http/pprof`
  handlers on the public metrics listener.
- Keep the default `go test` `stdversion` vet check enabled. Do not use
  `-vet=off` to bypass APIs newer than the module or file build constraint.
- Audit every new repository `GODEBUG` value against Go 1.27. Removed settings
  no longer accept historical values; never add a removed setting merely to
  silence a startup failure.
- Keep compatibility tests for all `encoding/json` v1 consumers in the CLI,
  Podman, FRR, and audit code because Go 1.27 backs v1 with the v2
  implementation. Cover duplicate object members, invalid UTF-8, and error
  compatibility without matching exact error text. Adopt the
  `encoding/json/v2` API only deliberately; use `GOEXPERIMENT=nojsonv2` only for
  diagnosis, never in shipped builds.
- In a `synctest` bubble, replace only adjacent `time.Sleep(d)` plus
  `synctest.Wait()` calls with `synctest.Sleep(d)`. E2E and interoperability
  waits use real time and must remain context-bounded wall-clock waits.
- Keep one named `MaxHeaderValueCount` bound and parser-level enforcement tests
  on both the ConnectRPC and metrics HTTP servers; retain `ReadHeaderTimeout`.
- Prefer `httptest.NewTestServer(t, handler)` when a test does not require real
  socket behavior. Retain explicit loopback or Unix listeners when the listener
  itself is under test.
- Benchmark packet codec, FSM, timers, and session event loop for every
  toolchain change; preserve 0 allocs/op and compare binary size. Use
  `GOEXPERIMENT=nosizespecializedmalloc` only to diagnose allocator changes,
  never in release artifacts.
- Use generic methods only on concrete internal types when they remove an
  existing abstraction cost. Do not churn public protobuf APIs or interfaces;
  interface methods still cannot declare type parameters.
- Do not adopt experimental `simd` or `simd/archsimd`, and never use `unsafe`.
  Reconsider SIMD only after its API is stable and both linux/amd64 and
  linux/arm64 benchmarks prove a material codec benefit.
- Keep the Go 1.27 `go mod tidy` layout at no more than two `require` blocks.
  Preserve dependency comments and require `go mod tidy -diff` to be clean.
- Treat goroutine labels, tracebacks, traces, and profiles as sensitive data.
  Keep analysis HTTP UIs on loopback and never put secrets or peer addresses
  into goroutine labels.

## Python tooling rules

The only supported Python environment is Python 3.14.7 managed by uv 0.12.6
from the root `.python-version`, `pyproject.toml`, and `uv.lock`.

- Keep one lock. Use the `peer`, `runtime`, and `quality` dependency groups;
  never add a second requirements or lock file.
- Never use `pip`, `pipx`, or `uv tool install`. Bootstrap uv from the
  checksum-pinned release archive or digest-pinned OCI image.
- Run repository Python entrypoints as `uv run --frozen`; CI and image builds
  must use `uv sync --frozen`.
- Run `make python-check` after changing Python code, pins, or invocation
  paths. It executes Ruff, ty, Bandit, and pip-audit over the owned Python
  file and verifies the exact interpreter.
- ExaBGP is an external immutable interop image, not a dependency of the
  repository Python lock.

## Podman Compose rules

- Use only `podman compose` with the checksum-pinned Docker Compose v5 Go
  provider. Never call `docker-compose` directly and never reintroduce Python
  `podman-compose`.
- Keep `PODMAN_COMPOSE_PROVIDER` explicit in CI and the development image so
  Podman cannot fall back to another provider from the host.
- Keep `DOCKER_BUILDKIT=0` for Podman-backed Compose builds. Compose v5
  delegates BuildKit builds to Docker Buildx/Bake, which is not part of the
  Podman runtime contract.
- Run the provider version check and render every tracked Compose file before
  live topology validation.

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
