# GoBFD — BFD Protocol Daemon

Go 1.26 implementation of Bidirectional Forwarding Detection (RFC 5880/5881).
Two binaries: `gobfd` (daemon) and `gobfdctl` (CLI client over gRPC).

## Commands
```sh
go build ./cmd/gobfd && go build ./cmd/gobfdctl   # сборка
go test ./... -race -count=1                       # тесты с race detector
go test -run '^TestFSMTransition$' ./internal/bfd  # один тест
golangci-lint run                                  # линтер (v2, строгий)
buf generate                                       # генерация proto
buf lint                                           # проверка proto
make integration                                   # интеграционные тесты (testcontainers + podman)
```

## Architecture
- `internal/bfd/` — FSM (RFC 5880 §6.8), session management, packet codec, auth
- `internal/server/` — ConnectRPC server, interceptors
- `internal/netio/` — raw socket abstraction (Linux-specific), UDP listeners 3784/4784
- `internal/config/` — koanf/v2: YAML + env + flags
- `internal/metrics/` — Prometheus collectors for BFD sessions
- `cmd/gobfd/` — daemon entry point (signal handling, graceful shutdown)
- `cmd/gobfdctl/` — CLI: cobra (non-interactive) + go-prompt (interactive shell)
- `pkg/bfdpb/` — generated protobuf types (public API for external consumers)
- `api/v1/` — proto definitions (buf managed)

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

## Git
- Commits: NEVER add Co-Authored-By or any AI/Claude mentions in commit messages
- Module: `github.com/dantte-lp/gobfd` — owner dantte-lp, NOT wolfguard

## Important: don't
- NEVER modify generated files in `pkg/bfdpb/` — regenerate with `buf generate`
- NEVER use `unsafe` package — this is a network daemon handling untrusted input
- NEVER skip error checks on socket operations in `internal/netio/`
- NEVER add dependencies without checking: `go mod tidy && govulncheck ./...`
- Timer intervals in BFD are in MICROSECONDS per RFC — don't confuse with milliseconds
- See `docs/architecture.md` for connection lifecycle and FSM state diagram
- See `docs/rfc5880-notes.md` for implementation decisions per RFC section
