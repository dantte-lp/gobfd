# GoBFD — Agile Development Plan

**Правило**: сначала планируй, потом делай.
**Ограничение**: все Go-операции (build, test, lint, buf) выполняются ТОЛЬКО через Podman/podman-compose. Локальные вызовы `go` запрещены.

---

## Инфраструктурная основа: Podman-Only Development

Все команды разработки выполняются через контейнеры. Никаких локальных `go build`, `go test`, `golangci-lint run`.

### Dev-контейнер (Containerfile.dev)

```dockerfile
FROM docker.io/golang:1.26-trixie AS dev
RUN apt-get update && apt-get install -y --no-install-recommends \
    git ca-certificates tzdata make bash curl \
    && rm -rf /var/lib/apt/lists/*
RUN go install github.com/bufbuild/buf/cmd/buf@latest \
    && go install google.golang.org/protobuf/cmd/protoc-gen-go@latest \
    && go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest \
    && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest \
    && go install golang.org/x/vuln/cmd/govulncheck@latest
WORKDIR /app
VOLUME /app
```

### Compose-файл для разработки (compose.dev.yml)

```yaml
services:
  dev:
    build:
      context: .
      dockerfile: deployments/docker/Containerfile.dev
    container_name: gobfd-dev
    volumes:
      - .:/app:z
    working_dir: /app
    command: sleep infinity
    # Для интеграционных тестов с raw sockets:
    cap_add: [NET_RAW, NET_ADMIN]
    network_mode: host

  lint:
    build:
      context: .
      dockerfile: deployments/docker/Containerfile.dev
    volumes:
      - .:/app:z
    working_dir: /app
    command: golangci-lint run ./...
    profiles: [ci]
```

### Алиасы команд (через Taskfile.yml или Makefile)

| Действие | Команда |
|----------|---------|
| Сборка | `podman-compose -f compose.dev.yml exec dev go build ./cmd/gobfd ./cmd/gobfdctl` |
| Тесты | `podman-compose -f compose.dev.yml exec dev go test ./... -race -count=1` |
| Один тест | `podman-compose -f compose.dev.yml exec dev go test -run '^TestFSM$' ./internal/bfd/` |
| Линтер | `podman-compose -f compose.dev.yml exec dev golangci-lint run` |
| Proto gen | `podman-compose -f compose.dev.yml exec dev buf generate` |
| Proto lint | `podman-compose -f compose.dev.yml exec dev buf lint` |
| Fuzz | `podman-compose -f compose.dev.yml exec dev go test -fuzz=FuzzControlPacket ./internal/bfd/ -fuzztime=60s` |
| Go mod tidy | `podman-compose -f compose.dev.yml exec dev go mod tidy` |
| Govulncheck | `podman-compose -f compose.dev.yml exec dev go run golang.org/x/vuln/cmd/govulncheck@latest ./...` |

---

## Sprint 0: Foundation (Фундамент)

**Цель**: рабочее окружение, полностью контейнеризированное. Ни одного локального Go-вызова.

### Задачи

| # | Задача | Артефакт | Definition of Done |
|---|--------|----------|--------------------|
| 0.1 | Инициализировать git-репозиторий | `.gitignore`, initial commit | Repo инициализирован с правильным .gitignore |
| 0.2 | Создать `go.mod` через Podman | `go.mod` | `module github.com/wolfguard/gobfd`, Go 1.26 |
| 0.3 | Создать dev Containerfile | `deployments/docker/Containerfile.dev` | Go 1.26 + buf + protoc-gen-go + protoc-gen-connect-go + golangci-lint v2 |
| 0.4 | Создать compose.dev.yml | `deployments/compose/compose.dev.yml` | `podman-compose up -d` запускает dev-контейнер |
| 0.5 | Создать Taskfile.yml | `Taskfile.yml` | Все команды через `task build`, `task test`, `task lint` обёрнуты в podman-compose exec |
| 0.6 | Создать `.golangci.yml` v2 | `.golangci.yml` | Строгая конфигурация из HELP.md (~40 линтеров) |
| 0.7 | Создать структуру каталогов | Пустые `.gitkeep` в каждом пакете | Все каталоги из HELP.md/CLAUDE.md созданы |
| 0.8 | Создать `buf.yaml` + `buf.gen.yaml` | Конфигурация buf | `task proto-lint` проходит |
| 0.9 | Написать `.claude/` конфигурацию | `.claude/agents/code-reviewer.md`, `.mcp.json` | Субагенты и MCP готовы |
| 0.10 | Проверить полный pipeline | — | `task build && task test && task lint` в Podman без ошибок |

### Приёмочный критерий Sprint 0

```bash
podman-compose -f deployments/compose/compose.dev.yml up -d
podman-compose -f deployments/compose/compose.dev.yml exec dev go version
# → go version go1.26.x linux/amd64
podman-compose -f deployments/compose/compose.dev.yml exec dev buf --version
# → 1.x.x
podman-compose -f deployments/compose/compose.dev.yml exec dev golangci-lint version
# → golangci-lint has version v2.x.x
```

---

## Sprint 1: Proto & API Definition

**Цель**: protobuf-определения, генерация кода, ConnectRPC-скелет.

### Задачи

| # | Задача | Артефакт | Definition of Done |
|---|--------|----------|--------------------|
| 1.1 | Определить `api/v1/bfd.proto` | `api/v1/bfd.proto` | Все сообщения и RPC из HELP.md |
| 1.2 | Сгенерировать Go-код через buf | `pkg/bfdpb/` | `task proto-gen` генерирует без ошибок |
| 1.3 | Проверить proto-линтером | — | `task proto-lint` проходит |
| 1.4 | Скелет ConnectRPC сервера | `internal/server/server.go` | Компилируется, реализует сгенерированный интерфейс |

### Proto-определение (api/v1/bfd.proto)

```protobuf
syntax = "proto3";
package bfd.v1;

import "google/protobuf/timestamp.proto";
import "google/protobuf/duration.proto";
import "buf/validate/validate.proto";

option go_package = "github.com/wolfguard/gobfd/pkg/bfdpb/v1;bfdpb";

// === Enums ===

enum SessionState {
  SESSION_STATE_UNSPECIFIED = 0;
  SESSION_STATE_ADMIN_DOWN = 1;
  SESSION_STATE_DOWN = 2;
  SESSION_STATE_INIT = 3;
  SESSION_STATE_UP = 4;
}

enum DiagnosticCode {
  DIAGNOSTIC_CODE_UNSPECIFIED = 0;
  DIAGNOSTIC_CODE_NONE = 1;
  DIAGNOSTIC_CODE_CONTROL_TIME_EXPIRED = 2;
  DIAGNOSTIC_CODE_ECHO_FAILED = 3;
  DIAGNOSTIC_CODE_NEIGHBOR_DOWN = 4;
  DIAGNOSTIC_CODE_FORWARDING_RESET = 5;
  DIAGNOSTIC_CODE_PATH_DOWN = 6;
  DIAGNOSTIC_CODE_CONCAT_PATH_DOWN = 7;
  DIAGNOSTIC_CODE_ADMIN_DOWN = 8;
  DIAGNOSTIC_CODE_REVERSE_CONCAT_PATH_DOWN = 9;
}

enum AuthenticationType {
  AUTHENTICATION_TYPE_UNSPECIFIED = 0;
  AUTHENTICATION_TYPE_NONE = 1;
  AUTHENTICATION_TYPE_SIMPLE_PASSWORD = 2;
  AUTHENTICATION_TYPE_KEYED_MD5 = 3;
  AUTHENTICATION_TYPE_METICULOUS_KEYED_MD5 = 4;
  AUTHENTICATION_TYPE_KEYED_SHA1 = 5;
  AUTHENTICATION_TYPE_METICULOUS_KEYED_SHA1 = 6;
}

enum SessionType {
  SESSION_TYPE_UNSPECIFIED = 0;
  SESSION_TYPE_SINGLE_HOP = 1;
  SESSION_TYPE_MULTI_HOP = 2;
}

// === Messages ===

message BfdSession {
  string peer_address = 1 [(buf.validate.field).string.ip = true];
  string local_address = 2 [(buf.validate.field).string.ip = true];
  string interface = 3;
  SessionType type = 4;
  SessionState local_state = 5;
  SessionState remote_state = 6;
  DiagnosticCode local_diagnostic = 7;
  uint32 local_discriminator = 8;
  uint32 remote_discriminator = 9;
  google.protobuf.Duration desired_min_tx_interval = 10;
  google.protobuf.Duration required_min_rx_interval = 11;
  google.protobuf.Duration remote_min_rx_interval = 12;
  uint32 detect_multiplier = 13 [(buf.validate.field).uint32 = {gte: 1}];
  google.protobuf.Duration negotiated_tx_interval = 14;
  google.protobuf.Duration detection_time = 15;
  AuthenticationType auth_type = 16;
  google.protobuf.Timestamp last_state_change = 17;
  google.protobuf.Timestamp last_packet_received = 18;
  SessionCounters counters = 19;
}

message SessionCounters {
  uint64 packets_sent = 1;
  uint64 packets_received = 2;
  uint64 packets_dropped = 3;
  uint64 state_transitions = 4;
  uint64 auth_failures = 5;
}

message AddSessionRequest {
  string peer_address = 1 [(buf.validate.field).string.ip = true];
  string local_address = 2 [(buf.validate.field).string.ip = true];
  string interface = 3;
  SessionType type = 4;
  google.protobuf.Duration desired_min_tx_interval = 5;
  google.protobuf.Duration required_min_rx_interval = 6;
  uint32 detect_multiplier = 7 [(buf.validate.field).uint32 = {gte: 1}];
  AuthenticationType auth_type = 8;
}

message AddSessionResponse {
  BfdSession session = 1;
}

message DeleteSessionRequest {
  uint32 local_discriminator = 1;
}

message DeleteSessionResponse {}

message ListSessionsRequest {}

message ListSessionsResponse {
  repeated BfdSession sessions = 1;
}

message GetSessionRequest {
  oneof identifier {
    uint32 local_discriminator = 1;
    string peer_address = 2;
  }
}

message GetSessionResponse {
  BfdSession session = 1;
}

message WatchSessionEventsRequest {
  bool include_current = 1; // send current state first, then stream
}

message SessionEvent {
  enum EventType {
    EVENT_TYPE_UNSPECIFIED = 0;
    EVENT_TYPE_STATE_CHANGE = 1;
    EVENT_TYPE_SESSION_ADDED = 2;
    EVENT_TYPE_SESSION_DELETED = 3;
  }
  EventType type = 1;
  BfdSession session = 2;
  SessionState previous_state = 3;
  google.protobuf.Timestamp timestamp = 4;
}

// === Service ===

service BfdService {
  rpc AddSession(AddSessionRequest) returns (AddSessionResponse);
  rpc DeleteSession(DeleteSessionRequest) returns (DeleteSessionResponse);
  rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse);
  rpc GetSession(GetSessionRequest) returns (GetSessionResponse);
  rpc WatchSessionEvents(WatchSessionEventsRequest) returns (stream SessionEvent);
}
```

### buf.yaml

```yaml
version: v2
deps:
  - buf.build/bufbuild/protovalidate
modules:
  - path: api
lint:
  use:
    - STANDARD
breaking:
  use:
    - FILE
```

### buf.gen.yaml

```yaml
version: v2
clean: true
plugins:
  - local: protoc-gen-go
    out: pkg/bfdpb
    opt: paths=source_relative
  - local: protoc-gen-connect-go
    out: pkg/bfdpb
    opt:
      - paths=source_relative
inputs:
  - directory: api
managed:
  enabled: true
  override:
    - file_option: go_package_prefix
      value: github.com/wolfguard/gobfd/pkg/bfdpb
```

---

## Sprint 2: BFD Core — Packet Codec & FSM

**Цель**: ядро протокола — кодек пакетов и конечный автомат. 100% покрытие тестами FSM-переходов.

**Агент**: `rfc-bfd-engineer` — основной исполнитель.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 2.1 | BFD Control Packet codec | `internal/bfd/packet.go` | Marshal/Unmarshal, все константы (State, Diag, AuthType) |
| 2.2 | Packet validation (§6.8.6 steps 1-7) | `internal/bfd/packet.go` | 13 проверок из RFC |
| 2.3 | Fuzz-тесты кодека | `internal/bfd/packet_test.go` | `FuzzControlPacket` — round-trip без паники |
| 2.4 | Table-driven unit-тесты кодека | `internal/bfd/packet_test.go` | Все граничные случаи, invalid packets |
| 2.5 | FSM transition table | `internal/bfd/fsm.go` | 20+ переходов из §6.8.6, map[stateEvent]transition |
| 2.6 | FSM unit-тесты | `internal/bfd/fsm_test.go` | Каждый переход покрыт, включая self-loops |
| 2.7 | Buffer pool | `internal/bfd/packet.go` | `sync.Pool` для zero-allocation I/O |

### Ключевые типы (из implementation-plan.md)

- `ControlPacket` — 24-byte header, все 13 полей
- `State` (AdminDown=0, Down=1, Init=2, Up=3)
- `Diag` (9 кодов диагностики)
- `AuthType` (6 типов аутентификации)
- `Event` (7 FSM-событий)
- `fsmTable` — `map[stateEvent]transition`

### Проверка

```bash
# Через Podman:
task test -- -run 'TestPacket' ./internal/bfd/
task test -- -run 'TestFSM' ./internal/bfd/
task test -- -fuzz=FuzzControlPacket ./internal/bfd/ -fuzztime=60s
task lint
```

---

## Sprint 3: BFD Core — Session & Timers

**Цель**: сессия с полным жизненным циклом, таймеры по RFC, Poll Sequence.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 3.1 | Session struct (§6.8.1) | `internal/bfd/session.go` | Все 15+ state variables из RFC с правильными начальными значениями |
| 3.2 | Session goroutine (run) | `internal/bfd/session.go` | select loop: recvCh, txTimer, detectTimer, ctx.Done |
| 3.3 | processPacket (§6.8.6) | `internal/bfd/session.go` | Полная 18-шаговая процедура приёма |
| 3.4 | Timer negotiation | `internal/bfd/session.go` | TX = max(DesiredMinTx, RemoteMinRx), Detection Time = RemoteDetectMult * max(RequiredMinRx, RemoteDesiredMinTx) |
| 3.5 | Jitter (§6.8.7) | `internal/bfd/session.go` | 75-100%, 75-90% для DetectMult=1 |
| 3.6 | Poll Sequence (§6.5) | `internal/bfd/session.go` | P/F bit exchange, pending param changes |
| 3.7 | Slow TX rate (§6.8.3) | `internal/bfd/session.go` | >= 1s когда не Up |
| 3.8 | Discriminator allocator | `internal/bfd/discriminator.go` | crypto/rand, уникальность, nonzero |
| 3.9 | Cached packet rebuild | `internal/bfd/session.go` | Pre-serialized packet per FRR pattern |
| 3.10 | Timer-based тесты | `internal/bfd/session_test.go` | testing/synctest для детерминистичных таймерных тестов |

### Формулы таймеров (RFC 5880)

```
TX Interval:
  actual_tx = max(bfd.DesiredMinTxInterval, bfd.RemoteMinRxInterval)
  If State != Up: actual_tx = max(1_000_000μs, bfd.RemoteMinRxInterval)

Detection Time (Async mode):
  detect_time = remote.DetectMult * max(bfd.RequiredMinRxInterval, remote.DesiredMinTxInterval)

Jitter:
  actual_tx_with_jitter = actual_tx * random(0.75, 1.0)
  If DetectMult == 1: random(0.75, 0.90)  // avoid exceeding detection time
```

---

## Sprint 4: Authentication

**Цель**: SHA1 (MUST), MD5, Simple Password аутентификация.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 4.1 | Authenticator interface | `internal/bfd/auth.go` | Sign/Verify contract |
| 4.2 | AuthKeyStore interface | `internal/bfd/auth.go` | LookupKey/CurrentKey |
| 4.3 | Keyed SHA1 (§6.7.4, MUST) | `internal/bfd/auth.go` | 28-byte auth section, sequence window |
| 4.4 | Meticulous Keyed SHA1 (§6.7.4, MUST) | `internal/bfd/auth.go` | Increment on every packet |
| 4.5 | Keyed MD5 (§6.7.3) | `internal/bfd/auth.go` | 24-byte auth section |
| 4.6 | Meticulous Keyed MD5 (§6.7.3) | `internal/bfd/auth.go` | Strict sequence |
| 4.7 | Simple Password (§6.7.2) | `internal/bfd/auth.go` | Password matching |
| 4.8 | seqInWindow helper | `internal/bfd/auth.go` | Circular uint32 arithmetic |
| 4.9 | AuthSeqKnown reset | `internal/bfd/auth.go` | Reset after 2x Detection Time |
| 4.10 | Test vectors | `internal/bfd/auth_test.go` | Interop vectors, replay attack tests |

---

## Sprint 5: Network I/O

**Цель**: raw sockets для Linux, UDP listeners, TTL/GTSM validation.

**Тестирование**: обязательно через Podman с `CAP_NET_RAW`.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 5.1 | PacketConn interface | `internal/netio/rawsock.go` | Platform-agnostic, mockable |
| 5.2 | Linux raw sockets | `internal/netio/rawsock_linux.go` | x/net/ipv4, x/net/ipv6, x/sys/unix |
| 5.3 | SingleHop listener (port 3784) | `internal/netio/rawsock_linux.go` | TTL=255, IP_RECVTTL, SO_BINDTODEVICE |
| 5.4 | MultiHop listener (port 4784) | `internal/netio/rawsock_linux.go` | TTL=255 TX, >=254 RX check |
| 5.5 | Source port allocator | `internal/netio/rawsock_linux.go` | Range 49152-65535, unique per session |
| 5.6 | Listener wrapper | `internal/netio/listener.go` | Recv() returns buf + PacketMeta |
| 5.7 | GTSM validation | `internal/netio/rawsock_linux.go` | TTL=255 check для single-hop (§5) |
| 5.8 | Mock socket для тестов | `internal/netio/mock_test.go` | In-memory PacketConn |

### Socket Options (RFC 5881)

| Опция | Назначение | RFC |
|-------|-----------|-----|
| `IP_TTL = 255` | Исходящий TTL | §5 |
| `IP_RECVTTL` | Получение TTL через ancillary data | §5 |
| `IP_PKTINFO` | Dest addr + interface | §6 |
| `SO_BINDTODEVICE` | Привязка к интерфейсу | §3 |
| `SO_REUSEADDR` | Множественные listeners | Impl |

### Проверка (требует rootful Podman)

```bash
# compose.dev.yml уже имеет cap_add: [NET_RAW, NET_ADMIN]
task test -- -run 'TestSocket' -tags integration ./internal/netio/
```

---

## Sprint 6: Session Manager & Демон

**Цель**: менеджер сессий, демультиплексирование, точка входа демона.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 6.1 | Manager struct | `internal/bfd/manager.go` | Dual-index: map[discr]*Session + map[peerKey]*Session |
| 6.2 | CreateSession / DestroySession | `internal/bfd/manager.go` | Валидация, goroutine lifecycle |
| 6.3 | Demux (§6.3, §6.8.6) | `internal/bfd/manager.go` | Two-tier: YourDiscr → peer key fallback |
| 6.4 | recvLoop | `internal/bfd/manager.go` | Goroutine читает сокет → Demux |
| 6.5 | StateChanges channel | `internal/bfd/manager.go` | Для gRPC streaming |
| 6.6 | Daemon entry point | `cmd/gobfd/main.go` | signal.NotifyContext, errgroup, graceful shutdown |
| 6.7 | Manager unit-тесты | `internal/bfd/manager_test.go` | Mock socket, session lifecycle |

---

## Sprint 7: gRPC Server & Configuration

**Цель**: ConnectRPC сервер, koanf конфигурация, Prometheus метрики.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 7.1 | ConnectRPC server | `internal/server/server.go` | Реализует BfdServiceHandler |
| 7.2 | AddSession RPC | `internal/server/server.go` | Создаёт сессию через Manager |
| 7.3 | DeleteSession RPC | `internal/server/server.go` | Удаляет сессию через Manager |
| 7.4 | ListSessions RPC | `internal/server/server.go` | Snapshot всех сессий |
| 7.5 | GetSession RPC | `internal/server/server.go` | По discriminator или peer address |
| 7.6 | WatchSessionEvents (streaming) | `internal/server/server.go` | Server-side streaming через StateChanges channel |
| 7.7 | Interceptors (logging, recovery) | `internal/server/interceptors.go` | slog structured logging |
| 7.8 | koanf/v2 config | `internal/config/config.go` | YAML + env + flags, defaults |
| 7.9 | Example config | `configs/gobfd.example.yml` | Все параметры с комментариями |
| 7.10 | Prometheus metrics | `internal/metrics/collector.go` | Sessions gauge, packets counters, state transitions |
| 7.11 | Server unit-тесты | `internal/server/server_test.go` | Все RPC, включая streaming |

### ConnectRPC Server Pattern (из Context7)

```go
func main() {
    mux := http.NewServeMux()
    path, handler := bfdpbconnect.NewBfdServiceHandler(
        &bfdServer{manager: mgr},
        connect.WithInterceptors(loggingInterceptor(), recoveryInterceptor()),
    )
    mux.Handle(path, handler)

    srv := &http.Server{Addr: cfg.GRPCAddr, Handler: mux}
    // h2c for HTTP/2 without TLS
}
```

### koanf Config Structure

```yaml
# configs/gobfd.example.yml
grpc:
  addr: ":50051"

metrics:
  addr: ":9100"
  path: "/metrics"

log:
  level: "info"    # debug, info, warn, error
  format: "json"   # json, text

bfd:
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
```

---

## Sprint 8: CLI Client (gobfdctl)

**Цель**: Cobra CLI + go-prompt интерактивный shell.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 8.1 | Root command + gRPC init | `cmd/gobfdctl/commands/root.go` | PersistentPreRun: gRPC dial |
| 8.2 | `session list` | `cmd/gobfdctl/commands/session.go` | Table/JSON/YAML вывод |
| 8.3 | `session show <peer>` | `cmd/gobfdctl/commands/session.go` | Детальная информация |
| 8.4 | `session add` | `cmd/gobfdctl/commands/session.go` | Все параметры через флаги |
| 8.5 | `session delete` | `cmd/gobfdctl/commands/session.go` | По discriminator |
| 8.6 | `monitor session` | `cmd/gobfdctl/commands/monitor.go` | gRPC streaming + --current |
| 8.7 | `version` | `cmd/gobfdctl/commands/version.go` | ldflags version info |
| 8.8 | `shell` (go-prompt) | `cmd/gobfdctl/commands/shell.go` | cobra-prompt мост, tab-completion |
| 8.9 | Dynamic completer | `cmd/gobfdctl/commands/shell.go` | gRPC запрос IP-адресов сессий |
| 8.10 | Output formatter | `cmd/gobfdctl/commands/format.go` | --format table|json|yaml |

### Cobra Structure (из Context7)

```go
var rootCmd = &cobra.Command{
    Use:   "gobfdctl",
    Short: "GoBFD CLI client",
    PersistentPreRun: func(cmd *cobra.Command, args []string) {
        conn := grpcConnect(grpcAddr)
        client = bfdpbconnect.NewBfdServiceClient(conn)
    },
}

rootCmd.PersistentFlags().StringVar(&grpcAddr, "addr", "localhost:50051", "gRPC server address")
rootCmd.PersistentFlags().StringVar(&outputFormat, "format", "table", "Output format: table|json|yaml")
```

---

## Sprint 9: Integration Tests & Production

**Цель**: E2E тесты через testcontainers + Podman, production Dockerfile, systemd.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 9.1 | Production Dockerfile | `deployments/docker/Dockerfile` | Multi-stage, scratch base, CGO_ENABLED=0 |
| 9.2 | compose.yml (production) | `deployments/compose/compose.yml` | gobfd + gobgp + prometheus + grafana |
| 9.3 | Systemd unit | `deployments/systemd/gobfd.service` | CAP_NET_RAW, ProtectSystem=strict |
| 9.4 | Testcontainers setup | `test/integration/setup_test.go` | ProviderPodman, rootful |
| 9.5 | BFD session E2E test | `test/integration/bfd_session_test.go` | Два контейнера, session Up |
| 9.6 | BFD failover E2E test | `test/integration/bfd_failover_test.go` | Kill one → detect timeout → Down |
| 9.7 | Auth interop E2E test | `test/integration/bfd_auth_test.go` | SHA1 auth between two instances |
| 9.8 | CLI E2E test | `test/integration/cli_test.go` | gobfdctl session list через gRPC |
| 9.9 | Grafana dashboard | `deployments/compose/configs/grafana/` | BFD sessions dashboard |
| 9.10 | docs/architecture.md | `docs/architecture.md` | FSM diagram, connection lifecycle |

### Testcontainers с Podman (из Context7)

```go
func TestBFDSessionEstablishment(t *testing.T) {
    ctx := t.Context()
    // TESTCONTAINERS_RYUK_DISABLED=true для Podman
    t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

    ctr, err := testcontainers.Run(ctx, "gobfd:test",
        testcontainers.WithProvider(testcontainers.ProviderPodman),
        testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
            ContainerRequest: testcontainers.ContainerRequest{
                Privileged: true,
            },
        }),
    )
    require.NoError(t, err)
    defer ctr.Terminate(ctx)
}
```

### Production Dockerfile

```dockerfile
FROM docker.io/golang:1.26-trixie AS builder
RUN apk add --no-cache git ca-certificates tzdata \
    && addgroup -S gobfd && adduser -S -G gobfd -H -s /sbin/nologin gobfd
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/wolfguard/gobfd/internal/version.Version=${VERSION:-dev}" \
    -o /bin/gobfd ./cmd/gobfd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /bin/gobfdctl ./cmd/gobfdctl

FROM scratch AS production
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /bin/gobfd /bin/gobfdctl /bin/
USER gobfd:gobfd
EXPOSE 3784/udp 4784/udp 50051
ENTRYPOINT ["/bin/gobfd"]
```

---

## Sprint 10: GoBGP Integration & Polish

**Цель**: интеграция с GoBGP через gRPC, hot reload, goreleaser.

### Задачи

| # | Задача | Артефакт | DoD |
|---|--------|----------|----|
| 10.1 | GoBGP BFD callback | integration code | BFD session state → GoBGP peer action |
| 10.2 | SIGHUP hot reload | `cmd/gobfd/main.go` | Перезагрузка конфигурации без рестарта |
| 10.3 | Goreleaser config | `.goreleaser.yml` | Binary releases + OCI image |
| 10.4 | README.md | `README.md` | Quick start, architecture, usage |
| 10.5 | govulncheck CI | CI pipeline | Автоматическая проверка уязвимостей |
| 10.6 | Performance benchmarks | `internal/bfd/bench_test.go` | Packet codec, FSM throughput |

---

## Timeline & Dependencies

```
Sprint 0 ──→ Sprint 1 ──→ Sprint 2 ──→ Sprint 3 ──→ Sprint 4
(Foundation)  (Proto/API)  (Codec/FSM)  (Session)    (Auth)
                                            │
                                            ├──→ Sprint 5 ──→ Sprint 6
                                            │    (NetIO)      (Manager/Daemon)
                                            │                      │
                                            │                      ├──→ Sprint 7
                                            │                      │    (gRPC/Config)
                                            │                      │         │
                                            │                      │         ├──→ Sprint 8
                                            │                      │         │    (CLI)
                                            │                      │         │
                                            └──────────────────────┴─────────┴──→ Sprint 9
                                                                                  (Integration)
                                                                                       │
                                                                                       └──→ Sprint 10
                                                                                            (Polish)
```

**Параллелизм**:
- Sprint 4 (Auth) и Sprint 5 (NetIO) могут идти параллельно после Sprint 3
- Sprint 7 (gRPC) и Sprint 8 (CLI) зависят от Sprint 6 (Manager)
- Sprint 9 (Integration) требует все предыдущие спринты

---

## Workflow для каждого спринта

### 1. Планирование (Plan Mode)

```
> [Plan Mode] Проанализируй текущий код и определи, что нужно реализовать в Sprint N.
> Какие файлы затронуты? Какие зависимости?
```

### 2. Реализация (Normal Mode, через Podman)

```
> [Normal Mode] Реализуй задачу N.X. Помни:
> - Все Go-операции через Podman (task build, task test)
> - Следуй CLAUDE.md code style
> - Ссылайся на RFC sections
```

### 3. Верификация (через Podman)

```bash
task test        # go test ./... -race -count=1
task lint        # golangci-lint run
task proto-lint  # buf lint (если менялись proto)
```

### 4. Review (субагент)

```
> @code-reviewer проверь изменения в internal/bfd/ на:
> - RFC compliance
> - Race conditions
> - Error handling
> - Goroutine leaks
```

---

## Критические правила

1. **Podman-only**: `go build` → `task build` → `podman-compose exec dev go build`
2. **RFC-first**: каждый кусок кода ссылается на секцию RFC
3. **Test-first**: тесты пишутся ДО или ВМЕСТЕ с кодом
4. **Microseconds**: BFD таймеры в МИКРОСЕКУНДАХ, не миллисекундах
5. **No unsafe**: сетевой демон обрабатывает недоверенный трафик
6. **Sender closes**: каналы закрывает отправитель
7. **Context first**: context.Context — первый параметр, никогда не в struct
8. **slog only**: никаких fmt.Println или log.Printf
9. **Errors wrap**: всегда `fmt.Errorf("action on %s: %w", target, err)`
10. **Generated = readonly**: никогда не редактируй `pkg/bfdpb/`
