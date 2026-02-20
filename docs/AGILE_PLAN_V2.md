# GoBFD -- Agile Plan V2: Audit Results & Updated Roadmap

**Date**: 2026-02-20
**Scope**: Full audit of recommendations (gobfd-technology-stack.md, HELP.md, AGILE_PLAN.md, implementation-plan.md, CLAUDE.md) vs actual implementation. Updated sprints for remaining work.

---

## Part 1: Audit -- Completed Sprints (0-8)

### Sprint 0: Foundation -- COMPLETED

All Sprint 0 tasks delivered:

| # | Task | Status | Notes |
|---|------|--------|-------|
| 0.1 | Git repo init | DONE | Repository exists |
| 0.2 | `go.mod` | DONE | `module github.com/dantte-lp/gobfd`, Go 1.26.0 |
| 0.3 | Containerfile.dev | DONE | `deployments/docker/Containerfile.dev` -- Go 1.26-trixie, buf, protoc-gen-go, protoc-gen-connect-go, golangci-lint v2, govulncheck |
| 0.4 | compose.dev.yml | DONE | `deployments/compose/compose.dev.yml` -- CAP_NET_RAW, NET_ADMIN, host networking |
| 0.5 | Makefile (not Taskfile) | DONE, DEVIATION | `Makefile` used instead of Taskfile.yml. Acceptable: technology-stack.md recommends Makefile as "standard for Go networking OSS" |
| 0.6 | `.golangci.yml` v2 | DONE | Strict config, ~35 linters enabled |
| 0.7 | Directory structure | DONE | All packages created |
| 0.8 | `buf.yaml` + `buf.gen.yaml` | DONE | buf v2 config |
| 0.9 | `.claude/` configuration | NOT DONE | No `.claude/agents/` or `.mcp.json` found |
| 0.10 | Pipeline verification | DONE | build/test/lint work through Podman |

**Deviations found**:

1. **Task 0.5**: Makefile instead of Taskfile.yml
   - Recommended: Taskfile.yml (AGILE_PLAN.md task 0.5)
   - Implemented: Makefile (`/opt/projects/repositories/gobfd/Makefile`)
   - Assessment: ACCEPTABLE. Technology-stack.md explicitly recommends Makefile: "Makefile -- standard for Go open-source networking projects. GoBGP, bio-routing, MetalLB use Makefile."

2. **Task 0.9**: `.claude/` configuration not created
   - Recommended: `.claude/agents/code-reviewer.md`, `.mcp.json`
   - Implemented: Not present
   - Action: LOW PRIORITY. These are optional tooling configs, not blocking.

---

### Sprint 1: Proto & API Definition -- COMPLETED

| # | Task | Status | Notes |
|---|------|--------|-------|
| 1.1 | `api/bfd/v1/bfd.proto` | DONE | All messages, enums, service defined |
| 1.2 | buf generate | DONE | `pkg/bfdpb/` generated |
| 1.3 | Proto lint | DONE | buf lint configured |
| 1.4 | ConnectRPC server skeleton | DONE | `internal/server/server.go` |

**Deviations found**:

1. **Proto path**: `api/bfd/v1/bfd.proto` vs recommended `api/v1/bfd.proto`
   - Recommended (AGILE_PLAN.md): `api/v1/bfd.proto`
   - Implemented: `api/bfd/v1/bfd.proto`
   - Assessment: ACCEPTABLE. The `bfd/v1/` path is actually better practice for buf module layout. Proto is valid and generates correctly.

2. **WatchSessionEvents response**: Different message structure
   - Recommended (AGILE_PLAN.md): Separate `SessionEvent` message, `returns (stream SessionEvent)`
   - Implemented: Inline `WatchSessionEventsResponse` with `EventType` nested enum, `returns (stream WatchSessionEventsResponse)`
   - Assessment: ACCEPTABLE. The implemented approach is actually more idiomatic for Connect streaming where the response type is conventional.

3. **Proto field name**: `interface_name` vs `interface`
   - Recommended (AGILE_PLAN.md): field `string interface = 3;`
   - Implemented: field `string interface_name = 3;`
   - Assessment: CORRECT DEVIATION. `interface` is a reserved keyword in many languages. `interface_name` is the proper protobuf practice.

4. **buf.gen.yaml**: Missing `simple` option
   - Recommended (AGILE_PLAN.md): no `simple` option for protoc-gen-connect-go
   - Implemented: `opt: [paths=source_relative, simple]`
   - Assessment: ACCEPTABLE. `simple` generates simpler server handler signatures. Intentional choice.

---

### Sprint 2: BFD Core -- Packet Codec & FSM -- COMPLETED

| # | Task | Status | Notes |
|---|------|--------|-------|
| 2.1 | Control Packet codec | DONE | `internal/bfd/packet.go` -- MarshalControlPacket/UnmarshalControlPacket |
| 2.2 | Packet validation (steps 1-7) | DONE | All 7 steps in UnmarshalControlPacket |
| 2.3 | Fuzz tests | DONE | `FuzzControlPacket` with 4 seeds |
| 2.4 | Table-driven unit tests | DONE | 20+ validation cases, all flags, all auth types |
| 2.5 | FSM transition table | DONE | `internal/bfd/fsm.go` -- 17 entries in fsmTable |
| 2.6 | FSM unit tests | DONE | 12 test functions covering all paths |
| 2.7 | Buffer pool | DONE | `PacketPool` using sync.Pool |

**Deviations found**:

1. **FSM table size**: 17 entries vs "20+ transitions" recommended
   - Recommended (implementation-plan.md): "20+ transitions including self-loops"
   - Implemented: 17 entries in `fsmTable` (`internal/bfd/fsm.go`)
   - Assessment: NEEDS REVIEW. The FSM table includes all RFC 5880 Section 6.8.6 transitions. The 17 entries cover: Down(4 events), Init(3 events), Up(5 events), AdminDown(2 events), plus self-loops. Missing entries may be for EventAdminUp/EventAdminDown self-loops in certain states. Test `TestFSMTableCompleteness` verifies 28 state-event combinations. The gap is covered by the "no entry = no transition" semantic.

2. **FSM as pure function, not struct method**
   - Recommended (system prompt): `session.applyEvent(event)` as method on Session
   - Implemented: `ApplyEvent(state, event) FSMResult` as a pure exported function
   - Assessment: ACCEPTABLE. The pure function approach is better for testing and aligns with the bench tests. Session.handleRecvPacket calls the pure function internally.

---

### Sprint 3: BFD Core -- Session & Timers -- COMPLETED

| # | Task | Status | Notes |
|---|------|--------|-------|
| 3.1 | Session struct | DONE | `internal/bfd/session.go` -- all RFC 6.8.1 state variables |
| 3.2 | Session goroutine (Run) | DONE | select loop with txTimer, detectTimer, recvCh, ctx.Done |
| 3.3 | processPacket (steps 8-18) | DONE | `handleRecvPacket` implements steps 8-18 |
| 3.4 | Timer negotiation | DONE | `calcTxInterval`, `calcDetectionTime` |
| 3.5 | Jitter | DONE | `ApplyJitter` -- 75-100% and 75-90% for DetectMult=1 |
| 3.6 | Poll Sequence | DONE | P/F bit handling, pendingPollTx/pendingFinalResponse |
| 3.7 | Slow TX rate | DONE | >= 1s enforcement in calcTxInterval when not Up |
| 3.8 | Discriminator allocator | DONE | `internal/bfd/discriminator.go` -- crypto/rand |
| 3.9 | Cached packet rebuild | DONE | `rebuildCachedPacket` per FRR pattern |
| 3.10 | Timer-based tests | DONE | testing/synctest used throughout |

**Deviations found**:

1. **Session.processPacket naming**: `handleRecvPacket` vs `processPacket`
   - Recommended (AGILE_PLAN.md): "processPacket (Section 6.8.6)"
   - Implemented: `handleRecvPacket` in `internal/bfd/session.go`
   - Assessment: COSMETIC. Function name is clear and descriptive.

2. **Passive role initial state**: Initial TX sending behavior
   - Recommended (implementation-plan.md): Passive role sessions should not transmit until receiving a packet from active peer
   - Implemented: `session.go` line ~320 -- `if s.cfg.Role == RolePassive && s.remoteDiscr.Load() == 0 { return }` in tx path
   - Assessment: CORRECT. Passive role is properly handled.

3. **AuthKey integration**: Session does not use auth in handleRecvPacket
   - Recommended (implementation-plan.md): Steps 8-18 include auth verification
   - Implemented: `handleRecvPacket` does NOT call auth verification -- auth modules exist but are not wired into session packet reception
   - Assessment: **GAP**. Auth types are implemented in `auth.go` but Session does not call Sign/Verify. This is documented in Sprint 4 analysis below.

---

### Sprint 4: Authentication -- COMPLETED (with integration gap)

| # | Task | Status | Notes |
|---|------|--------|-------|
| 4.1 | Authenticator interface | DONE | `internal/bfd/auth.go` -- Sign/Verify |
| 4.2 | AuthKeyStore interface | DONE | `AuthKeyStore` with `LookupKey` |
| 4.3 | Keyed SHA1 | DONE | `KeyedSHA1Auth` |
| 4.4 | Meticulous Keyed SHA1 | DONE | `MeticulousKeyedSHA1Auth` |
| 4.5 | Keyed MD5 | DONE | `KeyedMD5Auth` |
| 4.6 | Meticulous Keyed MD5 | DONE | `MeticulousKeyedMD5Auth` |
| 4.7 | Simple Password | DONE | `SimplePasswordAuth` |
| 4.8 | SeqInWindow helper | DONE | Circular uint32 arithmetic with wrap-around |
| 4.9 | AuthSeqKnown reset | PARTIAL | `AuthSeqKnown` field exists in `AuthState`, but automatic reset after 2x DetectionTime is NOT implemented |
| 4.10 | Test vectors | DONE | All 5 auth types tested with sign/verify/replay/mismatch |

**Deviations found**:

1. **AUTH NOT WIRED INTO SESSION** (CRITICAL GAP)
   - Recommended: Session.handleRecvPacket should call auth.Verify, Session TX should call auth.Sign
   - Implemented: Auth modules exist standalone but `Session` struct has no `Authenticator` field. `handleRecvPacket` skips auth verification entirely. `rebuildCachedPacket` does not sign packets.
   - Impact: Authentication is implemented but non-functional at the session level
   - Action: **HIGH PRIORITY** -- Wire auth into Session (Sprint 11)

2. **AuthSeqKnown auto-reset after 2x DetectionTime**
   - Recommended (RFC 5880 Section 6.7): "The bfd.AuthSeqKnown variable MUST be set to zero after no packets have been received for at least two Detection Time periods."
   - Implemented: `AuthState.AuthSeqKnown` exists but no timer resets it
   - Action: **MEDIUM PRIORITY** -- Add timer-based reset (Sprint 11)

3. **Auth key configuration**
   - Recommended: AuthKeyStore integration with koanf config, per-session auth configuration via gRPC API
   - Implemented: `AuthKeyStore` interface exists but no concrete implementation for configuration loading
   - Action: **MEDIUM PRIORITY** -- Add config-based AuthKeyStore (Sprint 11)

---

### Sprint 5: Network I/O -- COMPLETED

| # | Task | Status | Notes |
|---|------|--------|-------|
| 5.1 | PacketConn interface | DONE | `internal/netio/rawsock.go` |
| 5.2 | Linux raw sockets | DONE | `internal/netio/rawsock_linux.go` -- x/sys/unix |
| 5.3 | SingleHop listener (port 3784) | DONE | `NewSingleHopListener` -- TTL=255, IP_RECVTTL, SO_BINDTODEVICE |
| 5.4 | MultiHop listener (port 4784) | DONE | `NewMultiHopListener` |
| 5.5 | Source port allocator | DONE | `SourcePortAllocator` -- range 49152-65535 |
| 5.6 | Listener wrapper | DONE | `internal/netio/listener.go` -- Recv() with buffer pool |
| 5.7 | GTSM validation | DONE | `ValidateTTL` -- single-hop=255, multi-hop>=254 |
| 5.8 | Mock socket | DONE | `internal/netio/mock_test.go` |

**Deviations found**:

1. **No IPv6 support**
   - Recommended (technology-stack.md): "x/net/ipv4 and x/net/ipv6", "Separate IPv4/IPv6 listeners"
   - Implemented: Only IPv4 socket options (IP_TTL, IP_RECVTTL, IP_PKTINFO). No IPV6_UNICAST_HOPS, IPV6_RECVHOPLIMIT handling.
   - Assessment: **GAP**. IPv6 is mentioned in RFC 5881 as required.
   - Action: **MEDIUM PRIORITY** -- Add IPv6 support (Sprint 13)

2. **No x/net/ipv4.PacketConn usage**
   - Recommended (technology-stack.md): "ipv4.PacketConn with SetTTL(255), SetControlMessage(FlagTTL|FlagInterface|FlagDst, true), ReadBatch/WriteBatch for batch I/O"
   - Implemented: Uses raw `net.UDPConn` + `syscall.RawConn` Control callback + `unix.SetsockoptInt` directly
   - Assessment: **DEVIATION but functional**. The current implementation correctly sets all socket options but does not use x/net/ipv4's higher-level API. x/net/ipv4.PacketConn would provide ReadBatch/WriteBatch for better performance.
   - Action: **LOW PRIORITY** -- Consider migration to x/net/ipv4.PacketConn for batch I/O in optimization sprint

3. **syscall import in rawsock_linux.go line 14**
   - Recommended: "NEVER use deprecated `syscall` -- only `x/sys/unix`"
   - Implemented: `rawsock_linux.go` imports `syscall` for `syscall.RawConn` type in `setSocketOpts`
   - Assessment: **MINOR ISSUE**. `syscall.RawConn` is the only way to access the raw conn from `net.ListenConfig.Control` -- this is the standard Go pattern and is not the deprecated syscall package usage the rule refers to. The actual socket operations use `unix.SetsockoptInt` correctly.
   - Action: NO ACTION NEEDED. This is idiomatic Go.

4. **Source port allocator not integrated with sessions**
   - Recommended: Source port allocation per session for sending packets
   - Implemented: `SourcePortAllocator` exists but is never called from Session or Manager
   - Action: **MEDIUM PRIORITY** -- Wire into session creation (Sprint 12)

5. **No BPF socket filter**
   - Recommended (technology-stack.md): "SetBPF for BPF socket filter (8x performance)"
   - Implemented: No BPF filter
   - Action: **LOW PRIORITY** -- Performance optimization, not functional requirement

---

### Sprint 6: Session Manager & Daemon -- COMPLETED

| # | Task | Status | Notes |
|---|------|--------|-------|
| 6.1 | Manager struct | DONE | Dual-index: `map[uint32]*sessionEntry` + `map[sessionKey]*sessionEntry` |
| 6.2 | CreateSession / DestroySession | DONE | Validation, goroutine lifecycle, discriminator management |
| 6.3 | Demux | DONE | Two-tier: YourDiscr -> peer key fallback |
| 6.4 | recvLoop | NOT DONE | No receive loop goroutine in Manager |
| 6.5 | StateChanges channel | DONE | Buffered channel (size 64) |
| 6.6 | Daemon entry point | DONE | `cmd/gobfd/main.go` -- signal.NotifyContext, errgroup |
| 6.7 | Manager unit tests | DONE | Using synctest |

**Deviations found**:

1. **No recvLoop in Manager** (SIGNIFICANT GAP)
   - Recommended (AGILE_PLAN.md task 6.4): "Goroutine reads socket -> Demux"
   - Implemented: Manager has Demux() method but no goroutine that reads from netio.Listener and calls Demux. The netio.Listener.Recv() exists but is not called from anywhere.
   - Impact: The daemon can manage sessions via gRPC but cannot receive BFD packets from the network.
   - Action: **CRITICAL** -- Implement recvLoop that connects netio.Listener to Manager.Demux (Sprint 11)

2. **No PacketSender wiring to real sockets**
   - Recommended: Sessions send packets through real sockets
   - Implemented: `server.go` uses `noopSender{}` -- a placeholder that discards all packets. No real sender implementation connects sessions to netio.
   - Impact: Sessions cannot send BFD packets to the network.
   - Action: **CRITICAL** -- Implement real PacketSender using netio sockets (Sprint 11)

3. **Missing systemd sd_notify integration**
   - Recommended (technology-stack.md): "coreos/go-systemd/v22 -- sd_notify(READY), watchdog, socket activation"
   - Implemented: systemd unit file exists (`deployments/systemd/gobfd.service`) with `Type=simple` but no sd_notify calls in `cmd/gobfd/main.go`. `coreos/go-systemd` is not in `go.mod`.
   - Action: **LOW PRIORITY** -- Add systemd integration (Sprint 14)

4. **SIGHUP reload is partial**
   - Recommended: SIGHUP reloads session configuration (add/remove sessions)
   - Implemented: SIGHUP only reloads log level (`cmd/gobfd/main.go` line 169-186)
   - Action: **MEDIUM PRIORITY** -- Extend SIGHUP to reload BFD sessions from config (Sprint 14)

---

### Sprint 7: gRPC Server & Configuration -- COMPLETED

| # | Task | Status | Notes |
|---|------|--------|-------|
| 7.1 | ConnectRPC server | DONE | `internal/server/server.go` implements BfdServiceHandler |
| 7.2 | AddSession RPC | DONE | Creates session via Manager |
| 7.3 | DeleteSession RPC | DONE | Destroys session via Manager |
| 7.4 | ListSessions RPC | DONE | Snapshot of all sessions |
| 7.5 | GetSession RPC | DONE | By discriminator or peer address |
| 7.6 | WatchSessionEvents | DONE | Server-side streaming via StateChanges channel |
| 7.7 | Interceptors | DONE | Logging + Recovery interceptors |
| 7.8 | koanf/v2 config | DONE | `internal/config/config.go` -- YAML loading + defaults |
| 7.9 | Example config | DONE | `configs/gobfd.example.yml` |
| 7.10 | Prometheus metrics | DONE | `internal/metrics/collector.go` -- 6 metrics |
| 7.11 | Server unit tests | DONE | All RPCs tested including error codes |

**Deviations found**:

1. **Metrics not wired into sessions**
   - Recommended: Metrics collector called from Session (TX/RX counters, state transitions)
   - Implemented: `bfdmetrics.Collector` exists with methods like `IncPacketsSent`, `RecordStateTransition`, but Session does not hold a reference to any metrics collector. Metrics are registered in `main.go` but never called from BFD protocol code.
   - Action: **HIGH PRIORITY** -- Wire metrics collector into Session and Manager (Sprint 12)

2. **No environment variable support in config**
   - Recommended (technology-stack.md): "koanf/v2: YAML + env + flags"
   - Implemented: Only YAML file loading via `file.Provider`. No `env.Provider` for environment variable overrides. Example config comments mention env vars but they don't work.
   - Action: **MEDIUM PRIORITY** -- Add koanf env provider (Sprint 14)

3. **No h2c (HTTP/2 cleartext) support**
   - Recommended (AGILE_PLAN.md Sprint 7): "h2c for HTTP/2 without TLS"
   - Implemented: Standard HTTP/1.1 server. ConnectRPC works over HTTP/1.1 but gRPC protocol requires HTTP/2.
   - Impact: Standard gRPC clients (like grpcurl, GoBGP) may not work without h2c
   - Action: **MEDIUM PRIORITY** -- Add h2c support via `golang.org/x/net/http2/h2c` (Sprint 12)

4. **Session counters not populated in proto response**
   - Recommended (proto): BfdSession includes `SessionCounters counters = 19;`
   - Implemented: `snapshotToProto` does not populate counters field. Proto has the field but session snapshot lacks counter data.
   - Action: **MEDIUM PRIORITY** -- Add per-session counters, expose in snapshot (Sprint 12)

5. **No grpchealth integration**
   - Recommended (technology-stack.md go.mod): `connectrpc.com/grpchealth`
   - Implemented: Not in go.mod, no health check endpoint
   - Action: **LOW PRIORITY** -- Add gRPC health checking (Sprint 14)

---

### Sprint 8: CLI Client (gobfdctl) -- COMPLETED (with gaps)

| # | Task | Status | Notes |
|---|------|--------|-------|
| 8.1 | Root command + gRPC init | DONE | PersistentPreRunE creates ConnectRPC client |
| 8.2 | `session list` | DONE | Table/JSON output |
| 8.3 | `session show <peer>` | DONE | By discriminator or peer address |
| 8.4 | `session add` | DONE | All params via flags |
| 8.5 | `session delete` | DONE | By discriminator |
| 8.6 | `monitor` | DONE | gRPC streaming + --current |
| 8.7 | `version` | DONE | ldflags version info |
| 8.8 | `shell` (go-prompt) | PARTIAL | Basic bufio.Scanner REPL, NOT go-prompt |
| 8.9 | Dynamic completer | NOT DONE | No tab-completion |
| 8.10 | Output formatter | PARTIAL | table and json only, NOT yaml |

**Deviations found**:

1. **Shell is a basic REPL, not go-prompt** (SIGNIFICANT DEVIATION)
   - Recommended (technology-stack.md, AGILE_PLAN.md): "c-bata/go-prompt for interactive shell with dropdown-autocomplete" + "stromland/cobra-prompt for cobra<->go-prompt bridge"
   - Implemented: `cmd/gobfdctl/commands/shell.go` uses `bufio.Scanner` for a simple read-eval loop. No tab-completion, no syntax highlighting, no dropdown suggestions.
   - Impact: Poor interactive UX compared to specification
   - Action: **MEDIUM PRIORITY** -- Replace with go-prompt + cobra-prompt (Sprint 15)

2. **Missing YAML output format**
   - Recommended: `--format table|json|yaml`
   - Implemented: `--format table|json` only. No yaml format support.
   - Impact: Minor UX gap
   - Action: **LOW PRIORITY** -- Add yaml format (Sprint 15)

3. **No go-prompt / cobra-prompt dependencies in go.mod**
   - Recommended: `c-bata/go-prompt`, `stromland/cobra-prompt`
   - Implemented: Not in go.mod
   - Action: Part of shell improvement task

---

## Part 2: Cross-Cutting Deviations

### Missing Dependencies (go.mod gaps)

| Recommended Dependency | Purpose | In go.mod? | Priority |
|----------------------|---------|-----------|----------|
| `coreos/go-systemd/v22` | sd_notify, watchdog | NO | LOW |
| `uber-go/goleak` | Goroutine leak detection in tests | NO | HIGH |
| `testify/require` | Test assertions | NO | MEDIUM |
| `google/go-cmp` | Struct comparison with diff | NO | MEDIUM |
| `c-bata/go-prompt` | Interactive shell | NO | MEDIUM |
| `stromland/cobra-prompt` | Cobra <-> go-prompt bridge | NO | MEDIUM |
| `connectrpc.com/grpchealth` | gRPC health check | NO | LOW |
| `google.golang.org/grpc` | GoBGP client | NO | FUTURE (Sprint 16) |
| `mdlayher/netlink` | Interface monitoring | NO | FUTURE |
| `golang.org/x/net` (ipv4/ipv6) | Packet I/O abstraction | NO (uses net stdlib) | MEDIUM |

### Missing Test Infrastructure

1. **uber-go/goleak not used anywhere**
   - Recommended: `goleak.VerifyTestMain(m)` in each package
   - Implemented: No goleak usage. No TestMain functions found.
   - Action: **HIGH PRIORITY** -- Add goleak to all test packages (Sprint 11)

2. **testify/require not used**
   - Recommended: testify/require for assertions + go-cmp for struct diff
   - Implemented: Manual `t.Fatalf`/`t.Errorf` throughout. This is valid but more verbose.
   - Assessment: ACCEPTABLE but verbose. Manual assertions are idiomatic Go.
   - Action: **LOW PRIORITY** -- Optional adoption

3. **No testcontainers-go integration tests**
   - Recommended (AGILE_PLAN.md Sprint 9): testcontainers with Podman for E2E
   - Implemented: `test/integration/server_test.go` is an in-process test, not container-based
   - Action: **MEDIUM PRIORITY** -- Real E2E tests (Sprint 15)

### golangci-lint Configuration Gaps

Comparing recommended (~40 linters from HELP.md) vs implemented (35 linters in `.golangci.yml`):

**Missing linters**:
- `goheader` -- Not critical
- `whitespace` -- Not in current config but goimports handles most
- `tagliatelle` -- JSON tag naming validation
- `protogetter` -- Useful for proto code

**Present but not in recommendation**:
- `modernize` -- Go 1.26 modernization suggestions (GOOD addition)
- `perfsprint` -- Performance-aware fmt replacements (GOOD addition)

Assessment: The lint configuration is solid and covers the critical linters (gosec with audit:true, errcheck, staticcheck, exhaustive, depguard blocking math/rand and log). The deviation is minor.

### CI Pipeline Gaps

**`.github/workflows/ci.yml` analysis**:

1. **No buf lint/breaking in CI**
   - Recommended (technology-stack.md): "bufbuild/buf-action@v1 covers build, lint, breaking detection"
   - Implemented: CI does build, test, lint (golangci-lint), vulncheck. No buf lint or buf breaking step.
   - Action: **LOW PRIORITY** -- Add buf lint/breaking to CI

2. **No benchmark CI**
   - Recommended: Track performance regressions
   - Implemented: Benchmarks exist in bench_test.go but not run in CI
   - Action: **LOW PRIORITY**

3. **No goreleaser in CI**
   - `.goreleaser.yml` exists but no release workflow
   - Action: **LOW PRIORITY** -- Add release workflow

### Production Dockerfile Deviations

**`deployments/docker/Containerfile` analysis**:

1. **scratch instead of Alpine**
   - Recommended (technology-stack.md): "Docker: Alpine (~5MB) -- optimal base image for network daemon. scratch/distroless too minimal -- no shell for debugging, no tcpdump, ip."
   - Implemented: `FROM scratch AS production` with no debugging tools
   - Assessment: DEVIATION from recommendation. However, scratch is more secure and standard for Go static binaries.
   - Action: **LOW PRIORITY** -- Consider Alpine base for debug builds

2. **Base image uses `trixie` (Debian) not Alpine**
   - Recommended: Alpine base for production
   - Implemented: Builder uses `golang:1.26-trixie` (Debian), production uses scratch
   - Assessment: Builder stage is fine with any base. Production scratch vs Alpine is a trade-off.

---

## Part 3: Architecture Gaps (Not Covered by Sprints 0-8)

### GAP-1: Packet I/O Pipeline Not Connected (CRITICAL)

The daemon's data path is not wired end-to-end:

```
CURRENT:
  netio.Listener.Recv() -- exists but no caller
  Manager.Demux()       -- exists but no caller
  Session.Run()         -- runs but SendPacket goes to noopSender{}

REQUIRED:
  netio.Listener.Recv() --> parse --> Manager.Demux() --> Session.RecvPacket()
  Session.Run() txTimer --> rebuildCachedPacket() --> netio.WritePacket() --> wire
```

Files affected:
- `cmd/gobfd/main.go` -- needs recvLoop goroutine
- `internal/server/server.go` line 78 -- `noopSender{}` needs real sender
- `internal/bfd/manager.go` -- needs recv loop method or separate orchestrator
- New: Orchestrator/packet router connecting netio <-> bfd.Manager

### GAP-2: Authentication Not Integrated Into Sessions

```
CURRENT:
  auth.go -- Sign/Verify implementations (standalone)
  session.go -- No auth calls in handleRecvPacket or rebuildCachedPacket

REQUIRED:
  session.handleRecvPacket() --> auth.Verify(pkt) before processing
  session.rebuildCachedPacket() --> auth.Sign(pkt) before caching
  SessionConfig --> AuthType + AuthKeyStore fields
  AddSessionRequest --> auth_type field wiring
```

### GAP-3: Metrics Not Integrated Into Protocol Path

```
CURRENT:
  metrics/collector.go -- 6 Prometheus metrics defined and registered
  main.go line 89 -- Collector created but result discarded (_)
  session.go -- No metric calls

REQUIRED:
  Session.Run() --> IncPacketsSent on each TX
  Manager.Demux() --> IncPacketsReceived on each RX
  Session.applyEvent() --> RecordStateTransition on state change
  Manager.CreateSession() --> RegisterSession
  Manager.DestroySession() --> UnregisterSession
```

### GAP-4: Missing docs/architecture.md and docs/rfc5880-notes.md

- CLAUDE.md references: "See `docs/architecture.md` for connection lifecycle and FSM state diagram"
- CLAUDE.md references: "See `docs/rfc5880-notes.md` for implementation decisions per RFC section"
- Neither file exists in `docs/` directory

---

## Part 4: Updated Sprint Plan

### Sprint Status Summary

| Sprint | Name | Status | Completion |
|--------|------|--------|------------|
| 0 | Foundation | DONE | 90% (missing .claude/) |
| 1 | Proto & API | DONE | 100% |
| 2 | Codec/FSM | DONE | 100% |
| 3 | Session/Timers | DONE | 100% |
| 4 | Authentication | DONE | 85% (not wired into sessions) |
| 5 | Network I/O | DONE | 90% (IPv4 only, no batch I/O) |
| 6 | Manager/Daemon | DONE | 75% (no recvLoop, no real sender) |
| 7 | gRPC/Config | DONE | 85% (metrics not wired, no h2c) |
| 8 | CLI | DONE | 70% (basic shell, no yaml output) |
| 9 | Integration | PARTIAL | 20% (basic in-process test only) |
| 10 | GoBGP/Polish | NOT STARTED | 0% |

---

### Sprint 11: Data Path Integration (CRITICAL)

**Goal**: Wire the complete BFD data path end-to-end. After this sprint, two gobfd instances can establish a BFD session over the network.

**Priority**: P0 -- Blocking all further protocol work.

| # | Task | Artifact | DoD |
|---|------|----------|-----|
| 11.1 | Implement real PacketSender using netio | `internal/bfd/sender.go` | `NetSender` struct wraps `netio.LinuxPacketConn.WritePacket`. Implements `PacketSender` interface. |
| 11.2 | Implement recvLoop in Manager or separate orchestrator | `internal/bfd/manager.go` or `internal/bfd/receiver.go` | Goroutine reads from `netio.Listener`, calls `bfd.UnmarshalControlPacket`, calls `Manager.Demux`. Uses `bfd.PacketPool`. |
| 11.3 | Wire real sender into CreateSession | `internal/server/server.go` | Replace `noopSender{}` with `NetSender` wrapping actual socket. |
| 11.4 | Wire auth Sign into Session.rebuildCachedPacket | `internal/bfd/session.go` | If `cfg.AuthType != None`, call `Authenticator.Sign` on cached packet. Update XmitAuthSeq per meticulous/non-meticulous rules. |
| 11.5 | Wire auth Verify into Session.handleRecvPacket | `internal/bfd/session.go` | After step 7 (unmarshal), before step 8 (process FSM), verify auth. On failure: drop packet, log, increment auth failure metric. |
| 11.6 | Add AuthKeyStore config implementation | `internal/config/auth.go` | Concrete AuthKeyStore that loads keys from YAML config. |
| 11.7 | Add Authenticator field to SessionConfig | `internal/bfd/session.go` | SessionConfig gets optional `Authenticator` field, NewSession validates consistency. |
| 11.8 | AuthSeqKnown timer reset | `internal/bfd/auth.go` | Reset AuthSeqKnown to false after 2x DetectionTime without receiving authenticated packet. |
| 11.9 | Add uber-go/goleak to all test packages | All `*_test.go` | `goleak.VerifyTestMain(m)` in each package's TestMain. |
| 11.10 | Integration test: two sessions communicating | `test/integration/bfd_session_test.go` | Two Manager instances with mock sockets, session reaches Up state. |

**Acceptance criteria**:
```bash
# Two gobfd instances on localhost, single-hop session goes Up
make test  # all tests pass with -race -count=1
# New integration test demonstrates full packet round-trip
```

---

### Sprint 12: Metrics, Counters & Protocol Polish

**Goal**: Wire Prometheus metrics into protocol path, add per-session counters, add h2c support.

**Priority**: P1 -- Required for production observability.

| # | Task | Artifact | DoD |
|---|------|----------|-----|
| 12.1 | Wire Collector into Manager | `internal/bfd/manager.go` | Manager holds `*bfdmetrics.Collector`. Calls `RegisterSession`/`UnregisterSession` on create/destroy. |
| 12.2 | Wire Collector into Session | `internal/bfd/session.go` | Session calls `IncPacketsSent` on every TX, receives metrics ref from Manager. |
| 12.3 | Wire Collector into recvLoop | receiver code | Calls `IncPacketsReceived` on successful demux, `IncPacketsDropped` on demux failure. |
| 12.4 | Add state transition metrics | `internal/bfd/session.go` | On FSM state change, call `RecordStateTransition(peer, local, oldState, newState)`. |
| 12.5 | Add per-session counters to Session | `internal/bfd/session.go` | Atomic counters: PacketsSent, PacketsReceived, StateTransitions. Expose via SessionSnapshot. |
| 12.6 | Populate SessionCounters in proto response | `internal/server/server.go` | `snapshotToProto` populates the `counters` field from session counters. |
| 12.7 | Wire source port allocator | `internal/netio/` + session creation | `SourcePortAllocator.Allocate()` called when creating sender socket per session. `Release()` on destroy. |
| 12.8 | Add h2c support for gRPC compatibility | `cmd/gobfd/main.go` | Wrap handler with `h2c.NewHandler` from `golang.org/x/net/http2/h2c` for standard gRPC clients. |
| 12.9 | Expose negotiated TX interval and detection time in snapshot | `internal/bfd/session.go` | SessionSnapshot gains NegotiatedTxInterval, DetectionTime fields. |
| 12.10 | Expose last_state_change and last_packet_received timestamps | `internal/bfd/session.go` | Track and expose timestamps via atomic time values. |

---

### Sprint 13: IPv6 Support

**Goal**: Full IPv6 support for BFD single-hop and multi-hop.

**Priority**: P2 -- Required for production ISP/DC deployment.

| # | Task | Artifact | DoD |
|---|------|----------|-----|
| 13.1 | IPv6 socket options | `internal/netio/rawsock_linux.go` | IPV6_UNICAST_HOPS=255, IPV6_RECVHOPLIMIT for IPv6 sockets. |
| 13.2 | IPv6 listener constructors | `internal/netio/rawsock_linux.go` | `NewSingleHopListenerV6`, `NewMultiHopListenerV6` or auto-detect from addr type. |
| 13.3 | IPv6 ancillary data parsing | `internal/netio/rawsock_linux.go` | Parse IPV6_PKTINFO for dest addr, IPV6_HOPLIMIT for TTL. |
| 13.4 | IPv6 session configuration | `internal/bfd/session.go` | Session supports IPv6 peer/local addresses. |
| 13.5 | IPv6 GTSM validation | `internal/netio/rawsock.go` | ValidateTTL handles hop limit for IPv6. |
| 13.6 | IPv6 unit tests | `internal/netio/mock_test.go` | IPv6 addresses in TTL validation, port allocation, mock tests. |

---

### Sprint 14: Production Hardening

**Goal**: Systemd integration, config hot reload, health checks.

**Priority**: P2 -- Required for production deployment.

| # | Task | Artifact | DoD |
|---|------|----------|-----|
| 14.1 | Add coreos/go-systemd/v22 | `go.mod` + `cmd/gobfd/main.go` | sd_notify(READY) after initialization, sd_notify(STOPPING) before shutdown. |
| 14.2 | Systemd watchdog keepalive | `cmd/gobfd/main.go` | sd_notify(WATCHDOG=1) goroutine at WatchdogSec/2 interval. |
| 14.3 | Update systemd unit to Type=notify | `deployments/systemd/gobfd.service` | Type=notify instead of Type=simple. |
| 14.4 | SIGHUP session reload from config | `cmd/gobfd/main.go` | On SIGHUP: reload YAML, diff sessions, create new / destroy removed sessions. |
| 14.5 | Add BFD sessions to YAML config | `internal/config/config.go` | Config struct gets `Sessions []SessionConfig` for declarative session management. |
| 14.6 | koanf env provider | `internal/config/config.go` | Environment variable overrides (GOBFD_GRPC_ADDR, etc.). |
| 14.7 | gRPC health check | `cmd/gobfd/main.go` | `connectrpc.com/grpchealth` handler mounted on mux. |
| 14.8 | Runtime log level change via gRPC | `internal/server/server.go` + proto | New RPC: SetLogLevel. Uses shared slog.LevelVar. |
| 14.9 | FlightRecorder integration | `cmd/gobfd/main.go` | `runtime/trace.FlightRecorder` with MinAge:500ms, MaxBytes:2MB. Dump on BFD session failure. |
| 14.10 | Graceful session drain on shutdown | `cmd/gobfd/main.go` | On SIGTERM: set all sessions to AdminDown with DiagAdminDown, wait for final TX, then shutdown. |

---

### Sprint 15: Integration Tests & CLI Polish

**Goal**: E2E tests with testcontainers, improved CLI shell.

**Priority**: P2 -- Required for confidence in releases.

| # | Task | Artifact | DoD |
|---|------|----------|-----|
| 15.1 | Testcontainers Podman setup | `test/integration/setup_test.go` | TESTCONTAINERS_RYUK_DISABLED=true, ProviderPodman. |
| 15.2 | BFD session E2E test | `test/integration/bfd_session_test.go` | Two containers, BFD session reaches Up via real sockets. |
| 15.3 | BFD failover E2E test | `test/integration/bfd_failover_test.go` | Kill container, verify detection timeout triggers Down. |
| 15.4 | Auth interop E2E test | `test/integration/bfd_auth_test.go` | SHA1 auth between two instances. |
| 15.5 | CLI E2E test | `test/integration/cli_test.go` | gobfdctl session add/list/delete/show via gRPC. |
| 15.6 | Replace shell with go-prompt | `cmd/gobfdctl/commands/shell.go` | go-prompt + cobra-prompt for tab-completion and dropdown. |
| 15.7 | Dynamic completer for shell | `cmd/gobfdctl/commands/shell.go` | gRPC queries for session IPs, command suggestions. |
| 15.8 | Add YAML output format | `cmd/gobfdctl/commands/format.go` | `--format yaml` using `gopkg.in/yaml.v3`. |
| 15.9 | Add docs/architecture.md | `docs/architecture.md` | FSM state diagram, packet flow diagram, connection lifecycle. |
| 15.10 | Add docs/rfc5880-notes.md | `docs/rfc5880-notes.md` | Implementation decisions per RFC section. |

---

### Sprint 16: GoBGP Integration

**Goal**: Integrate with GoBGP via gRPC for BFD-triggered route management.

**Priority**: P3 -- Feature completion for production ISP use.

| # | Task | Artifact | DoD |
|---|------|----------|-----|
| 16.1 | GoBGP gRPC client | `internal/gobgp/client.go` | Connect to GoBGP gRPC API (port 50051/50052). |
| 16.2 | BFD flap dampening (RFC 5882 Section 3.2) | `internal/gobgp/dampening.go` | Exponential backoff before propagating rapid Down/Up/Down to BGP. |
| 16.3 | BFD Down -> BGP action | `internal/gobgp/handler.go` | On BFD Down: `DeletePath()` or `DisablePeer()` (configurable strategy). |
| 16.4 | BFD Up -> BGP action | `internal/gobgp/handler.go` | On BFD Up: `AddPath()` or `EnablePeer()`. |
| 16.5 | StateChange consumer goroutine | `internal/gobgp/handler.go` | Reads Manager.StateChanges(), applies dampening, calls GoBGP client. |
| 16.6 | GoBGP config section | `internal/config/config.go` | Config struct gets GoBGP connection settings. |
| 16.7 | Integration test: BFD + GoBGP | `test/integration/gobgp_test.go` | Testcontainers: gobfd + gobgp, BFD Down triggers route withdrawal. |

---

### Sprint 17: Performance Optimization (Optional)

**Goal**: Optimize for sub-50ms BFD intervals, high session count.

**Priority**: P4 -- Performance tuning after correctness.

| # | Task | Artifact | DoD |
|---|------|----------|-----|
| 17.1 | x/net/ipv4.PacketConn migration | `internal/netio/rawsock_linux.go` | ReadBatch/WriteBatch for recvmmsg/sendmmsg. |
| 17.2 | BPF socket filter | `internal/netio/rawsock_linux.go` | SetBPF to filter non-BFD packets in kernel. |
| 17.3 | eBPF TTL filter | `internal/netio/ebpf/` | cilium/ebpf program to drop TTL!=255 in kernel. |
| 17.4 | CPU affinity for timing goroutines | `internal/bfd/session.go` | `runtime.LockOSThread()` + `unix.SchedSetaffinity()` for critical timers. |
| 17.5 | Benchmark suite expansion | `internal/bfd/bench_test.go` | Add full-path benchmarks: recv -> unmarshal -> FSM -> TX. |
| 17.6 | mdlayher/netlink interface monitor | `internal/netio/ifmon.go` | React to interface up/down events, update session state. |

---

### Sprint 18: Release & Documentation

**Goal**: Production release artifacts.

**Priority**: P3 -- Final polish.

| # | Task | Artifact | DoD |
|---|------|----------|-----|
| 18.1 | goreleaser v2 GitHub Actions workflow | `.github/workflows/release.yml` | On tag push: build, test, release binaries + Docker. |
| 18.2 | buf CI integration | `.github/workflows/ci.yml` | Add buf lint and buf breaking steps. |
| 18.3 | Grafana dashboard | `deployments/compose/configs/grafana/` | BFD session dashboard: states, packet counts, jitter histogram. |
| 18.4 | README.md | `README.md` | Quick start, architecture, usage, configuration reference. |
| 18.5 | goreleaser deb/rpm | `.goreleaser.yml` | nFPM config for deb/rpm packages with systemd unit. |
| 18.6 | Alpine debug Docker image | `deployments/docker/Containerfile.debug` | Alpine-based image with tcpdump, ip, shell for debugging. |

---

## Part 5: Dependency Graph (Updated)

```
Sprint 11 (Data Path)     <-- CRITICAL, unblocks everything
    |
    +---> Sprint 12 (Metrics & Counters)
    |         |
    |         +---> Sprint 13 (IPv6)
    |         |
    |         +---> Sprint 14 (Production Hardening)
    |                   |
    |                   +---> Sprint 15 (Integration Tests & CLI)
    |                   |         |
    |                   |         +---> Sprint 16 (GoBGP Integration)
    |                   |                   |
    |                   |                   +---> Sprint 17 (Performance, optional)
    |                   |                   |
    |                   |                   +---> Sprint 18 (Release)
    |                   |
    |                   +---> Sprint 18 (Release, partial)
```

**Parallelism**:
- Sprint 13 (IPv6) and Sprint 14 (Production) can run in parallel after Sprint 12
- Sprint 15 (Tests) needs Sprint 14 but Sprint 16 (GoBGP) only needs Sprint 11
- Sprint 17 (Perf) is independent and optional

---

## Part 6: Consolidated Differences Summary

### CRITICAL (P0) -- Must fix before any production use

| # | What | Recommended | Implemented | File | Action |
|---|------|-------------|-------------|------|--------|
| C-1 | No packet receive loop | Manager has recvLoop reading from network | Manager.Demux exists but no goroutine calls it | `internal/bfd/manager.go` | Sprint 11.2 |
| C-2 | No real packet sender | Session sends via real socket | `noopSender{}` discards all packets | `internal/server/server.go:78` | Sprint 11.1, 11.3 |
| C-3 | Auth not wired into session | Session calls Sign/Verify | Auth modules standalone, not called from Session | `internal/bfd/session.go` | Sprint 11.4, 11.5 |

### HIGH (P1) -- Required for production quality

| # | What | Recommended | Implemented | File | Action |
|---|------|-------------|-------------|------|--------|
| H-1 | Metrics not wired | Collector called from Session/Manager | Collector created but calls discarded | `cmd/gobfd/main.go:89` | Sprint 12.1-12.4 |
| H-2 | No goleak | goleak.VerifyTestMain in each package | No goleak usage | All test files | Sprint 11.9 |
| H-3 | Session counters missing | Per-session TX/RX/state counters | No counter tracking in Session | `internal/bfd/session.go` | Sprint 12.5-12.6 |
| H-4 | No h2c for gRPC | h2c for HTTP/2 cleartext | HTTP/1.1 only | `cmd/gobfd/main.go` | Sprint 12.8 |

### MEDIUM (P2) -- Important for completeness

| # | What | Recommended | Implemented | File | Action |
|---|------|-------------|-------------|------|--------|
| M-1 | No IPv6 | IPv4 + IPv6 listeners | IPv4 only | `internal/netio/rawsock_linux.go` | Sprint 13 |
| M-2 | Basic shell REPL | go-prompt + cobra-prompt | bufio.Scanner | `cmd/gobfdctl/commands/shell.go` | Sprint 15.6 |
| M-3 | No env config | koanf YAML + env + flags | YAML only | `internal/config/config.go` | Sprint 14.6 |
| M-4 | Source port allocator unused | Allocated per session | Exists but not called | `internal/netio/rawsock_linux.go` | Sprint 12.7 |
| M-5 | AuthSeqKnown no auto-reset | Reset after 2x DetectionTime | Field exists, no timer | `internal/bfd/auth.go` | Sprint 11.8 |
| M-6 | Partial SIGHUP reload | Reload sessions from config | Only reloads log level | `cmd/gobfd/main.go` | Sprint 14.4 |
| M-7 | No session config in YAML | Declarative session definitions | Sessions only via gRPC | `internal/config/config.go` | Sprint 14.5 |
| M-8 | No YAML output format | table/json/yaml | table/json only | `cmd/gobfdctl/commands/format.go` | Sprint 15.8 |
| M-9 | No auth key config | AuthKeyStore from YAML config | AuthKeyStore interface only | `internal/bfd/auth.go` | Sprint 11.6 |
| M-10 | Missing documentation | architecture.md, rfc5880-notes.md | Referenced in CLAUDE.md but not created | `docs/` | Sprint 15.9-15.10 |

### LOW (P3) -- Nice to have

| # | What | Recommended | Implemented | File | Action |
|---|------|-------------|-------------|------|--------|
| L-1 | No systemd sd_notify | coreos/go-systemd/v22 | systemd unit exists but Type=simple | `cmd/gobfd/main.go` | Sprint 14.1-14.3 |
| L-2 | No gRPC health check | connectrpc.com/grpchealth | Not present | `cmd/gobfd/main.go` | Sprint 14.7 |
| L-3 | scratch Docker base | Alpine recommended for debugging | scratch (more secure but no debug tools) | `deployments/docker/Containerfile` | Sprint 18.6 |
| L-4 | No buf in CI | buf lint/breaking in CI | Only go test/lint/vulncheck | `.github/workflows/ci.yml` | Sprint 18.2 |
| L-5 | No FlightRecorder | runtime/trace.FlightRecorder | Not present | `cmd/gobfd/main.go` | Sprint 14.9 |
| L-6 | No BPF/eBPF | BPF socket filter, optional eBPF | Not present | `internal/netio/` | Sprint 17.2-17.3 |
| L-7 | No batch I/O | x/net/ipv4 ReadBatch/WriteBatch | Single-packet read/write | `internal/netio/rawsock_linux.go` | Sprint 17.1 |
| L-8 | .claude/ config missing | .claude/agents/, .mcp.json | Not present | Project root | Optional |
| L-9 | No goreleaser workflow | GitHub Actions release | .goreleaser.yml exists, no workflow | `.github/workflows/` | Sprint 18.1 |
| L-10 | No Grafana dashboard | BFD dashboard | compose.yml references grafana dir | `deployments/compose/configs/grafana/` | Sprint 18.3 |

---

## Part 7: Recommended Execution Order

1. **Sprint 11** (Data Path Integration) -- P0, 1-2 weeks
   - This is the single most important sprint. Without it, gobfd is a BFD session manager that cannot exchange packets.

2. **Sprint 12** (Metrics & Counters) -- P1, 1 week
   - Observability is critical for production. Wire metrics before going to E2E testing.

3. **Sprint 14** (Production Hardening) -- P2, 1 week
   - Systemd, config reload, health checks -- needed before any real deployment.

4. **Sprint 13** (IPv6) -- P2, 3-5 days
   - Required for modern networks but can be parallelized with Sprint 14.

5. **Sprint 15** (Integration Tests & CLI) -- P2, 1 week
   - E2E confidence before release.

6. **Sprint 16** (GoBGP Integration) -- P3, 1 week
   - The key differentiator feature.

7. **Sprint 18** (Release) -- P3, 3-5 days

8. **Sprint 17** (Performance) -- P4, optional
   - Only if sub-50ms intervals are required.
