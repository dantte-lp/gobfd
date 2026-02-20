# Исследование для ТЗ и CLAUDE.md проекта GoBFD

**GoBFD — BFD-демон на Go — требует CLI-архитектуру по модели GoBGP (Cobra + gRPC), интерактивный shell через go-prompt, строгий линтинг golangci-lint v2, контейнеризацию через rootful Podman с CAP_NET_RAW и CLAUDE.md с акцентом на краткость и прогрессивное раскрытие.** Ниже — полное исследование всех 12 тем, готовое к трансформации в ТЗ и CLAUDE.md.

---

## Часть 1: CLI сетевых демонов — три парадигмы

Сетевые демоны сформировали три архитектурных паттерна CLI, каждый с чёткими trade-offs. Выбор для GoBFD определяет UX на годы.

### vtysh (FRR): иерархическая модальная оболочка

vtysh — **CLI-мультиплексор** FRRouting, поддерживающий постоянные Unix-сокетные соединения с до **22 демонами** (zebra, bgpd, ospfd, bfdd и др.). Ключевой механизм: скрипт `python/xref2vtysh.py` сканирует `frr.xref`, извлекает все DEFUN-определения команд и генерирует `vtysh_cmd.c` с битовыми масками демонов. При вводе команды vtysh проверяет `cmd->daemon` и маршрутизирует только нужным демонам.

**Иерархия режимов** (наследие Cisco IOS):

| Режим | Промпт | Переход |
|-------|--------|---------|
| User EXEC | `Router>` | Начальный |
| Privileged EXEC | `Router#` | `enable` |
| Global Config | `Router(config)#` | `configure terminal` |
| Interface | `Router(config-if)#` | `interface eth0` |
| BFD | `Router(config-bfd)#` | `bfd` |
| BFD Peer | `Router(config-bfd-peer)#` | `peer 192.168.2.1` |

**IPC-протокол** предельно прост: NUL-терминированная строка → демону через Unix socket (`/var/run/frr/<daemon>.vty`) → NUL-терминированный ответ + 3 нулевых маркера + 1 байт статуса. Буфер начинается с 4 КБ, расширяется динамически.

Конкретные BFD-команды vtysh:
```
Router# show bfd peers
Router# show bfd peers brief
Router# show bfd peer 192.168.0.1 counters json
Router(config)# bfd
Router(config-bfd)# peer 192.168.2.1 interface r2-eth2
Router(config-bfd-peer)# detect-multiplier 3
Router(config-bfd-peer)# receive-interval 300
Router(config-bfd-peer)# transmit-interval 300
```

**Сильные стороны**: знакомый UX для сетевых инженеров, tab-дополнение через граф команд, JSON-вывод (`show bfd peers counters json`), batch-режим (`vtysh -c "show bfd peers"`), фильтрация (`| include REGEX`). **Слабые стороны**: безопасность не гарантируется (vtysh «никогда не проектировался как привилегированный брокер»), потеря конфигурации нестартованных демонов при `write memory`, ограниченные pipe-фильтры по сравнению с Junos.

### birdc (BIRD): плоская оболочка с откатом конфигурации

birdc подключается через Unix socket `/run/bird/bird.ctl`, использует GNU Readline и **не имеет модальных режимов** — все команды на одном уровне. Ключевое отличие от vtysh — **невозможность онлайн-конфигурации**: рабочий процесс строго «редактируй файл → проверь → применяй».

Уникальная функция — **механизм безопасного отката**:
```
bird> configure timeout 60       # применить с таймером 60 сек
bird> configure confirm          # подтвердить (отменить откат)
bird> configure undo             # откатить к предыдущей
bird> configure check            # проверить синтаксис без применения
```

Если администратор потерял связность после применения конфигурации и не может подтвердить — конфигурация автоматически откатывается. Этот паттерн аналогичен `commit confirm` в Junos OS.

Примеры команд birdc:
```
bird> show protocols all bgp1
bird> show route for 192.0.2.0/24
bird> show route where bgp_path ~ [= 4242120045 =]
bird> show route table master4 count
bird> disable bgp1 / enable bgp1 / restart bgp1
bird> show memory
```

### GoBGP: API-first с CLI как тонким gRPC-клиентом

**GoBGP — ближайший архитектурный аналог GoBFD.** Проект состоит из `gobgpd` (демон, gRPC API на порту **50051**) и `gobgp` (CLI, чистый gRPC-клиент). Всё, что делает CLI, можно сделать через API — CLI лишь тонкая обёртка. Код CLI в `cmd/gobgp/`: root.go, global.go, neighbor.go, policy.go, monitor.go, vrf.go. Каждый файл — набор cobra.Command с gRPC-вызовами внутри.

**Инициализация gRPC** происходит в `PersistentPreRun` корневой cobra-команды:
```go
rootCmd := &cobra.Command{
    Use: "gobgp",
    PersistentPreRun: func(cmd *cobra.Command, args []string) {
        conn := connGrpc()
        client = api.NewGobgpApiClient(conn)
    },
}
```

**Структура команд** — плоская на верхнем уровне, иерархическая внутри:
```bash
gobgp global rib -a ipv4                              # просмотр RIB
gobgp global rib add 10.0.0.0/24 nexthop 20.20.20.20 # добавление маршрута
gobgp neighbor                                         # список соседей
gobgp neighbor 10.0.255.1 adj-in                      # Adj-RIB-In
gobgp neighbor 10.0.255.1 reset                       # сброс сессии
gobgp monitor global rib --current                    # streaming + текущее состояние
gobgp monitor neighbor                                 # streaming статусов
gobgp -j neighbor                                      # JSON-вывод
gobgp --target unix:///var/run/go-bgp.sock neighbor   # Unix socket
gobgp --tls --tls-ca-file ca.pem neighbor             # TLS
```

**Streaming** (критичен для BFD) реализован через gRPC server-side streaming:
```go
stream, err := client.WatchEvent(ctx, &api.WatchEventRequest{
    Peer: &api.WatchEventRequest_Peer{},
})
for {
    r, err := stream.Recv()
    if err == io.EOF { break }
    if p := r.GetPeer(); p != nil && p.Type == api.WatchEventResponse_PeerEvent_STATE {
        // обработка изменения состояния
    }
}
```

Флаг `--current` сначала получает текущее состояние, затем переключается в streaming-режим.

### Cilium/Hubble и MetalLB — дополнительные паттерны

**Hubble CLI** — ещё один пример gRPC streaming-клиента на Cobra. Подключается к Hubble Relay (порт **4245**), поддерживает TLS и OIDC. Команда `hubble observe --follow` устанавливает бесконечный gRPC stream через `GetFlows` RPC. Фильтрация мощная: `--pod`, `--from-label`, `--to-port`, `--protocol`, `--verdict DROPPED`, `--output json`.

**MetalLB** использует радикально иной подход — **без CLI**, управление через Kubernetes CRD (IPAddressPool, BGPPeer, BFDProfile). Показывает альтернативу для cloud-native контекста, но не подходит для standalone BFD-демона.

**bio-routing** использует `urfave/cli` (не Cobra), позиционируется как Go-библиотека и пока **не имеет полноценной CLI-утилиты** — она в планах.

---

## Рекомендация по CLI-архитектуре для GoBFD

### Cobra + go-prompt через cobra-prompt мост

Анализ библиотек показал чёткую картину:

| Библиотека | Звёзды | Подходит | Почему |
|-----------|--------|----------|--------|
| **cobra** | ~38K | ✅ Не-интерактивный режим | Де-факто стандарт; GoBGP, kubectl, gh, cilium |
| **go-prompt** | ~5.3K | ✅ Интерактивный shell | Dropdown-autocomplete как в IDE; evans, kube-prompt |
| ishell | ~1.7K | ❌ | Не активен 2+ года, примитивный completion |
| liner | ~1.1K | ❌ | Слишком низкоуровневый |
| bubbletea | ~36K | ❌ | Overkill для CLI shell, захватывает terminal |

**Рекомендуемая архитектура gobfdctl:**

```
gobfdctl session list                          # не-интерактивный (cobra)
gobfdctl session list --format json            # JSON для автоматизации
gobfdctl session show 10.0.0.1                 # детали сессии
gobfdctl session add 10.0.0.1 --interval 300ms # добавить сессию
gobfdctl monitor session --current             # streaming событий
gobfdctl shell                                 # интерактивный режим (go-prompt)

gobfdctl> session list [TAB]
┌──────────────┬─────────────────────┐
│ 10.0.0.1     │ State: Up           │
│ 10.0.0.2     │ State: Down         │
│ 192.168.1.1  │ State: AdminDown    │
└──────────────┴─────────────────────┘
```

**Мост cobra ↔ go-prompt** через `cobra-prompt`:
```go
cp := cobraprompt.CobraPrompt{
    RootCmd:                rootCmd,
    PersistFlagValues:      true,
    ShowHelpCommandAndFlags: true,
    AddDefaultExitCommand:  true,
}
cp.Run()
```

**Контекстно-зависимый completer** с динамическим запросом IP-адресов сессий через gRPC:
```go
func (c *BFDCompleter) Complete(d prompt.Document) []prompt.Suggest {
    args := parseArgs(d.TextBeforeCursor())
    if args[0] == "session" && args[1] == "show" {
        sessions, _ := c.client.ListSessions(context.Background())
        var s []prompt.Suggest
        for _, sess := range sessions {
            s = append(s, prompt.Suggest{
                Text:        sess.PeerAddress,
                Description: fmt.Sprintf("State: %s", sess.State),
            })
        }
        return prompt.FilterHasPrefix(s, d.GetWordBeforeCursor(), true)
    }
    return nil
}
```

**Форматирование вывода** — три формата через `--format` флаг:
- `table` (по умолчанию) — tabwriter для человека
- `json` — для автоматизации и пайплайнинга
- `yaml` — для конфигурационных целей

**Proto-определение для streaming BFD-событий** (по модели GoBGP WatchEvent):
```protobuf
service BfdService {
    rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse);
    rpc AddSession(AddSessionRequest) returns (AddSessionResponse);
    rpc DeleteSession(DeleteSessionRequest) returns (DeleteSessionResponse);
    rpc WatchSessionEvents(WatchSessionEventsRequest) returns (stream SessionEvent);
}

message SessionEvent {
    enum EventType {
        STATE_CHANGE = 0;
        SESSION_ADDED = 1;
        SESSION_DELETED = 2;
    }
    EventType type = 1;
    BfdSession session = 2;
    google.protobuf.Timestamp timestamp = 3;
}
```

---

## Часть 2: CLAUDE.md — формат, содержимое, рабочий процесс

### Принципы эффективного CLAUDE.md

CLAUDE.md — файл, который Claude Code автоматически читает в начале каждой сессии и включает в системный промпт. Официальная документация Anthropic формулирует **пять ключевых принципов**:

1. **Краткость важнее полноты** — длинные файлы игнорируются, важные правила теряются в шуме
2. **Прогрессивное раскрытие** — указывайте КАК найти информацию, а не давайте всю сразу
3. **Claude — не линтер** — используйте golangci-lint, gofmt для стиля кода
4. **Не генерируйте автоматически** — файл имеет высочайший рычаг влияния, создавайте вручную
5. **Нет обязательного формата** — но concise human-readable Markdown рекомендуется

**Иерархия файлов** (в порядке приоритета):
- `~/.claude/CLAUDE.md` — глобальные настройки пользователя
- `CLAUDE.md` в корне проекта — командные настройки (коммитится в git)
- `CLAUDE.md` в подкаталогах — специфика компонентов
- `CLAUDE.local.md` — личные настройки (в .gitignore)

### Рекомендуемый CLAUDE.md для GoBFD

```markdown
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

## Important: don't
- NEVER modify generated files in `pkg/bfdpb/` — regenerate with `buf generate`
- NEVER use `unsafe` package — this is a network daemon handling untrusted input
- NEVER skip error checks on socket operations in `internal/netio/`
- NEVER add dependencies without checking: `go mod tidy && govulncheck ./...`
- Timer intervals in BFD are in MICROSECONDS per RFC — don't confuse with milliseconds
- See `docs/architecture.md` for connection lifecycle and FSM state diagram
- See `docs/rfc5880-notes.md` for implementation decisions per RFC section
```

### Agile-воркфлоу с Claude Code для GoBFD

**Четыре фазы разработки фичи:**

1. **Исследование** (Plan Mode, Shift+Tab дважды) — Claude читает файлы, отвечает на вопросы, НЕ делает изменений:
   ```
   > [Plan Mode] read internal/bfd/ and understand the current FSM implementation.
     How does it handle detect-multiplier timeout?
   ```

2. **Планирование** — Claude создаёт детальный план:
   ```
   > I want to add BFD echo mode (RFC 5880 §6.4). What files need to change?
     Create a detailed implementation plan.
   ```

3. **Реализация** — переключение в Normal Mode, код по плану:
   ```
   > [Normal Mode] implement echo mode from your plan. Start with internal/bfd/echo.go
   ```

4. **Верификация** — тесты, lint, проверка:
   ```
   > run the tests and fix any failures. Then run golangci-lint and fix warnings.
   ```

**Субагенты** получают собственное контекстное окно. Определяются в `.claude/agents/`:

```markdown
# .claude/agents/code-reviewer.md
---
name: code-reviewer
description: Reviews code for quality, security, and Go best practices
tools: Read, Grep, Glob
model: sonnet
---
You are a code reviewer for a BFD network daemon in Go.
Focus on: error handling, race conditions, RFC compliance, goroutine leaks.
```

**Hooks** для автоматических проверок:
```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Write|Edit",
        "command": "gofmt -w $FILE && golangci-lint run $FILE"
      }
    ]
  }
}
```

**Параллельная работа** через git worktrees:
```bash
git worktree add ../gobfd-fsm feature/echo-mode
git worktree add ../gobfd-cli feature/interactive-shell
# Запустить Claude Code в каждом worktree отдельно
```

### MCP-конфигурация для GoBFD

Файл `.mcp.json` в корне проекта:
```json
{
  "mcpServers": {
    "context7": {
      "command": "npx",
      "args": ["-y", "@upstash/context7-mcp@latest"]
    },
    "go-dev-mcp": {
      "type": "stdio",
      "command": "godevmcp",
      "args": ["serve"]
    },
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_TOKEN}"
      }
    }
  }
}
```

**Context7** подтягивает актуальную документацию библиотек в контекст — критично для ConnectRPC, buf, koanf, которые быстро меняются. **GoDevMCP** (`go install github.com/fpt/go-dev-mcp/godevmcp@latest`) предоставляет навигацию по Go-проекту. Также доступен экспериментальный **gopls MCP** — встроенный MCP-сервер в gopls для go to definition и find references.

---

## Часть 3: golangci-lint v2 — строгая конфигурация

### Ключевые изменения в v2

golangci-lint v2.10.1 (17 февраля 2026) вносит **breaking changes**: обязательное поле `version: "2"`, новая структура `linters.default` вместо `enable-all/disable-all`, секция `formatters` отделена от `linters`, gosimple/stylecheck слиты в staticcheck, нет таймаута по умолчанию. Миграция: `golangci-lint migrate`.

### Критичные линтеры для сетевого демона

Для BFD-демона, обрабатывающего недоверенный сетевой трафик, **три категории** линтеров приоритетны:

**Безопасность сетевого кода:**
- **gosec** (режим `audit: true`) — 50+ правил с CWE-привязкой: TLS-конфигурация (G402), weak RSA (G403), path traversal (G304)
- **bodyclose** — незакрытые HTTP response body = утечка файловых дескрипторов
- **noctx** — HTTP-запросы без context = невозможность отмены и таймаутов
- **errcheck** (`check-type-assertions: true`) — непроверенные ошибки в сокетных операциях = молчаливые сбои

**Корректность конкурентного кода:**
- **govet** с включёнными `shadow`, `copylocks`, `atomic`, `loopclosure`
- **fatcontext** — вложенные контексты в циклах
- **staticcheck** — весь набор SA* анализаторов

**Качество и поддерживаемость:**
- **cyclop** (`max-complexity: 15`) — критичные пути FSM должны быть простыми
- **wrapcheck** — обёртка ошибок для диагностики
- **exhaustive** — полнота switch по enum (критично для FSM-состояний)
- **depguard** — запрет `math/rand` (→ `crypto/rand`), `log` (→ `slog`), `io/ioutil`

**Конфигурация, вдохновлённая Cilium** (крупнейший сетевой Go-проект) и «золотой конфигурацией» maratori. Cilium уже на v2 формате с `linters.default: none` и ручным включением ~20 линтеров. Полная конфигурация `.golangci.yml`:

```yaml
version: "2"

run:
  timeout: 10m
  relative-path-mode: gomod
  tests: true
  build-tags:
    - integration

formatters:
  enable:
    - goimports
    - gofmt
  settings:
    goimports:
      local-prefixes:
        - github.com/yourorg/gobfd

output:
  formats:
    text:
      print-linter-name: true
      colors: true
  show-stats: true

issues:
  max-issues-per-linter: 0
  max-same-issues: 0

linters:
  default: none
  enable:
    # Безопасность
    - gosec
    - bidichk
    # Корректность
    - govet
    - staticcheck
    - errcheck
    - bodyclose
    - noctx
    - contextcheck
    - nilerr
    - nilnesserr
    - durationcheck
    - fatcontext
    - copyloopvar
    # Ошибки
    - errorlint
    - wrapcheck
    - err113
    - errname
    # Сетевые
    - nosprintfhostport
    # Качество
    - gocritic
    - revive
    - cyclop
    - gocognit
    - funlen
    - nestif
    - exhaustive
    - unparam
    - unconvert
    - unused
    - ineffassign
    # Производительность
    - prealloc
    - perfsprint
    # Стиль
    - goconst
    - dupl
    - misspell
    - sloglint
    - musttag
    - modernize
    # Зависимости
    - depguard
    # Дисциплина
    - nolintlint
    # Тесты
    - tparallel
    - testpackage

  settings:
    gosec:
      config:
        global:
          audit: "true"
    govet:
      enable:
        - shadow
        - atomic
        - copylocks
        - loopclosure
        - httpmux
    cyclop:
      max-complexity: 15
      skip-tests: true
    exhaustive:
      default-signifies-exhaustive: true
    depguard:
      rules:
        main:
          deny:
            - pkg: "math/rand$"
              desc: "Use crypto/rand or math/rand/v2"
            - pkg: "log"
              desc: "Use log/slog"
            - pkg: "io/ioutil"
              desc: "Deprecated in Go 1.16"
    nolintlint:
      require-explanation: true
      require-specific: true
    sloglint:
      attr-only: false

  exclusions:
    generated: strict
    presets:
      - std-error-handling
      - common-false-positives
    rules:
      - path: _test\.go
        linters: [gosec, dupl, funlen, cyclop, err113, wrapcheck]

severity:
  default: warning
  rules:
    - linters: [gosec, errcheck, bodyclose, noctx, staticcheck]
      severity: error
```

---

## Часть 4: Podman, OCI и контейнеризация

### CAP_NET_RAW — обязателен rootful Podman

BFD требует raw sockets для UDP с IP-заголовками. **CAP_NET_RAW невозможна в rootless Podman** на хостовом network namespace — ядро Linux запрещает это. Решение: `sudo podman-compose up` с `cap_add: NET_RAW` + `network_mode: host`.

### Compose для dev-окружения

```yaml
# deployments/compose/compose.yml
services:
  gobfd:
    build:
      context: ../..
      dockerfile: deployments/docker/Dockerfile
      target: development
    container_name: gobfd-dev
    cap_add: [NET_RAW, NET_ADMIN]
    cap_drop: [ALL]
    network_mode: host
    volumes:
      - ../../:/app:z
    environment:
      GOBFD_LOG_LEVEL: debug
      GOBFD_GRPC_ADDR: ":50051"
      GOBFD_METRICS_ADDR: ":9100"
    depends_on: [gobgp]

  gobgp:
    image: docker.io/osrg/gobgp:latest
    cap_add: [NET_RAW, NET_ADMIN, NET_BIND_SERVICE]
    cap_drop: [ALL]
    network_mode: host
    volumes:
      - ./configs/gobgpd.toml:/etc/gobgp/gobgpd.toml:ro,z
    command: ["gobgpd", "-f", "/etc/gobgp/gobgpd.toml", "--api-hosts", ":50052"]

  prometheus:
    image: docker.io/prom/prometheus:latest
    cap_drop: [ALL]
    volumes:
      - ./configs/prometheus.yml:/etc/prometheus/prometheus.yml:ro,z
    ports: ["9090:9090"]
    user: "65534:65534"

  grafana:
    image: docker.io/grafana/grafana:latest
    cap_drop: [ALL]
    volumes:
      - ./configs/grafana/provisioning:/etc/grafana/provisioning:ro,z
      - ./configs/grafana/dashboards:/var/lib/grafana/dashboards:ro,z
    ports: ["3000:3000"]
    environment:
      GF_SECURITY_ADMIN_USER: admin
      GF_SECURITY_ADMIN_PASSWORD: admin
```

### Multi-stage Dockerfile

```dockerfile
FROM docker.io/golang:1.26-alpine AS builder
RUN apk add --no-cache git ca-certificates tzdata \
    && addgroup -S gobfd && adduser -S -G gobfd -H -s /sbin/nologin gobfd
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X .../version.Version=${VERSION:-dev}" \
    -o /bin/gobfd ./cmd/gobfd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /bin/gobfdctl ./cmd/gobfdctl

FROM scratch AS production
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /bin/gobfd /bin/gobfdctl /bin/
USER gobfd:gobfd
EXPOSE 3784/udp 3785/udp 4784/udp 50051
ENTRYPOINT ["/bin/gobfd"]
```

Базовый образ **scratch** — минимальная поверхность атаки для production. `CGO_ENABLED=0` обеспечивает сатическую линковку.

### Testcontainers-go с Podman

Обязателен rootful режим (`podman machine set --rootful`). Ryuk часто падает из-за сетевой модели Podman — решение: `tc.WithProvider(tc.ProviderPodman)` или `TESTCONTAINERS_RYUK_DISABLED=true`.

```go
func TestBFDSessionEstablishment(t *testing.T) {
    ctx := t.Context()
    ctr, err := tc.Run(ctx, "gobfd:test",
        tc.WithProvider(tc.ProviderPodman),
        tc.WithPrivilegedMode(true),
    )
    if err != nil { t.Fatal(err) }
    defer ctr.Terminate(ctx)
}
```

### Systemd unit

```ini
[Unit]
Description=GoBFD - BFD Protocol Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=gobfd
Group=gobfd
ExecStart=/usr/bin/gobfd --config /etc/gobfd/gobfd.yml
Restart=on-failure
RestartSec=5s
AmbientCapabilities=CAP_NET_RAW CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_RAW CAP_NET_ADMIN
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

---

## Часть 5: структура проекта GoBFD

Структура основана на паттернах **GoBGP** (`cmd/` + `internal/` + `pkg/` + `api/`), **bio-routing** (протоколы как пакеты), **MetalLB** (всё внутреннее в `internal/`) и рекомендациях golang-standards/project-layout.

```
gobfd/
├── CLAUDE.md                              # Claude Code контекст
├── .claude/
│   └── agents/
│       └── code-reviewer.md               # Субагент code review
├── .mcp.json                              # MCP серверы (context7, go-dev-mcp)
├── .golangci.yml                          # Строгий линтинг v2
├── .goreleaser.yml                        # Релизы + OCI + deb/rpm
├── Makefile
├── go.mod / go.sum
├── buf.gen.yaml / buf.yaml                # buf конфигурация
│
├── api/v1/                                # Protobuf-определения
│   └── bfd.proto                          # gRPC сервис + сообщения
│
├── cmd/
│   ├── gobfd/                             # Демон
│   │   └── main.go                        # signal handling, graceful shutdown
│   └── gobfdctl/                          # CLI-клиент
│       ├── main.go
│       └── commands/
│           ├── root.go                    # cobra root + gRPC init в PersistentPreRun
│           ├── session.go                 # session list/get/add/delete
│           ├── monitor.go                 # streaming BFD events
│           ├── shell.go                   # интерактивный go-prompt режим
│           └── version.go
│
├── internal/
│   ├── bfd/                               # Ядро BFD-протокола
│   │   ├── fsm.go                         # FSM (RFC 5880 §6.8)
│   │   ├── session.go                     # Сессия + таймеры
│   │   ├── packet.go                      # Кодек BFD Control Packet
│   │   ├── discriminator.go               # Генерация дискриминаторов
│   │   ├── auth.go                        # MD5, SHA1 (RFC 5880 §6.7)
│   │   └── manager.go                     # Менеджер всех сессий
│   ├── server/                            # ConnectRPC сервер
│   │   ├── server.go
│   │   └── interceptors.go
│   ├── config/                            # koanf/v2
│   │   ├── config.go
│   │   └── defaults.go
│   ├── netio/                             # Сетевой ввод-вывод
│   │   ├── rawsock.go                     # Абстракция raw sockets
│   │   ├── rawsock_linux.go               # Linux-specific (golang.org/x/sys)
│   │   └── listener.go                    # UDP 3784/4784
│   ├── metrics/                           # Prometheus
│   │   └── collector.go
│   └── version/
│       └── version.go                     # ldflags
│
├── pkg/bfdpb/                             # Публичные protobuf-типы
│   ├── bfd.pb.go
│   └── bfdconnect/                        # ConnectRPC generated
│
├── deployments/
│   ├── compose/
│   │   ├── compose.yml
│   │   └── configs/
│   ├── docker/
│   │   └── Dockerfile
│   └── systemd/
│       └── gobfd.service
│
├── configs/
│   └── gobfd.example.yml
│
├── docs/
│   ├── architecture.md
│   └── rfc5880-notes.md
│
├── scripts/
│   └── proto-gen.sh
│
└── test/integration/
    └── bfd_session_test.go                # testcontainers
```

**Ключевые решения:**

- **`internal/bfd/`** приватен — предотвращает зависимость внешних проектов от нестабильного API ядра протокола (паттерн GoBGP: `internal/pkg/table/`)
- **`pkg/bfdpb/`** публичен — позволяет внешним проектам использовать gRPC-клиент (паттерн GoBGP: `pkg/apiutil/`)
- **`internal/netio/`** отделён от логики протокола — упрощает тестирование через mock сокетов
- **`api/v1/`** версионирован — обеспечивает обратную совместимость API
- **Два бинарника** — классический паттерн `gobgpd` + `gobgp`

---

## Заключение: от исследования к действию

Исследование выявило три вывода, которые должны определить архитектуру GoBFD. **Первый**: API-first подход GoBGP (CLI как тонкий gRPC-клиент) — единственный паттерн, проверенный в production для Go сетевых демонов. bio-routing ещё не дошёл до CLI, MetalLB ушёл в K8s CRD, а vtysh монолитен. **Второй**: комбинация Cobra + go-prompt + cobra-prompt мост — оптимальная точка на кривой сложность/UX, дающая и скриптабельность, и интерактивность без overhead bubbletea или примитивности ishell. **Третий**: CLAUDE.md должен быть **коротким** (один экран) — это не документация, а инструкция Claude Code, и каждая лишняя строка размывает фокус. Прогрессивное раскрытие через ссылки на `docs/` решает проблему полноты. Строгий golangci-lint v2 с ~40 линтерами и `gosec audit: true` компенсирует то, что Claude Code — не линтер, а rootful Podman с CAP_NET_RAW — единственный путь для BFD в контейнерах.
