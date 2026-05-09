# Журнал изменений

Все заметные изменения в этом проекте документируются в данном файле.

Формат основан на [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/),
проект придерживается [Семантического версионирования](https://semver.org/lang/ru/spec/v2.0.0.html).

## [Не выпущено]

### Удалено

- Каталог `.archive/` удалён из репозитория. Sprint planning records,
  cleanup plan и промо-черновики больше не трекаются. Каталог остаётся
  в `.gitignore`, чтобы maintainer мог держать локальные scratch-файлы
  без риска снова закоммитить их в git. Содержимое сохранено в истории
  коммитов (`git log -- .archive/`).

## [0.6.1] - 2026-05-09

### Исправлено

- Обновлена пиннинг-версия `github.com/golangci/golangci-lint/v2` с
  `v2.11.4` до `v2.12.1` в `go.mod`. Релизный workflow вызывает
  `go tool golangci-lint`, который использует именно эту пиннинг-версию;
  v2.11.4 не знала линтера `gomodguard_v2`, добавленного в `v0.6.0`,
  из-за чего release CI на теге `v0.6.0` падал на шаге Lint.

## [0.6.0] - 2026-05-09

### Добавлено

- gRPC сервис `EchoService` (RFC 9747) с `AddEchoSession`,
  `DeleteEchoSession` и `ListEchoSessions`. Сервер валидирует
  положительный `TxInterval` и `DetectMultiplier` в диапазоне
  `[1, 255]`. Зарегистрирован на HTTP/2 ConnectRPC mux демона по пути
  `/bfd.v1.EchoService/...` и анонсируется через `grpc.health.v1`.
- gRPC сервис `MicroBFDService` (RFC 7130) с `AddMicroBFDGroup`,
  `DeleteMicroBFDGroup` и `ListMicroBFDGroups`. Сервер проверяет
  инвариант RFC 7130 `1 <= min_active_links <= len(member_links)`.
- Подкоманды `gobfdctl echo {add, list, delete}` и
  `gobfdctl micro {add, list, delete}` с форматами вывода table,
  JSON и YAML.
- Roadmap-документ `docs/en/roadmap.md` с waterfall-планом спринтов
  S12-S20 до версии `v1.0.0`.
- End-to-end цели `make e2e-core`, `make e2e-routing`, `make e2e-rfc`,
  `make e2e-overlay`, `make e2e-linux` и `make e2e-vendor` с Podman-only
  выполнением, packet captures и стандартизованными артефактами.
- `make e2e-help` и `make gopls-check` для покрытия E2E build tags.
- Podman-only путь `make interop-clab` для vendor NOS interop с Arista cEOS
  и FRRouting IPv4/IPv6 BFD evidence.
- Профили vendor NOS на публичных образах: Arista cEOS, Nokia SR Linux,
  SONiC-VS и VyOS. Cisco XRd остаётся opt-in и operator-provided.
- Worktree-безопасная автоматизация development Compose: generated container
  names, `COMPOSE_PROJECT_NAME`, `make dev-project`, `make dev-ps`.
- Конвейер GoReleaser snapshot собирает OCI-образы `linux/amd64` и
  `linux/arm64` на Debian trixie и Oracle Linux 10, deb/rpm пакеты и
  Syft SBOMs.
- Vendored `buf/validate/validate.proto` из `bufbuild/protovalidate`
  `v1.2.0`: `buf lint` работает без подключения к Buf Schema Registry.
- Reusable GitHub Actions Podman installer с pin `podman-compose` `1.5.0`.
- Workflow E2E evidence в GitHub Actions с PR-safe, nightly и manual vendor
  gates и 30-дневным сроком хранения артефактов.

### Изменено

- Документация реорганизована: канонические reference-документы только в
  `docs/en/01..16-*.md`; sprint-планы перенесены в `.archive/sprints/`;
  введены architecture decision records в `docs/en/adr/`; вспомогательные
  справочники в `docs/en/reference/`. RU-зеркало эквивалентно EN
  файл-в-файл.
- Унифицирован шаблон шапки в `docs/en/12..16-*.md`: badge-row,
  декларативный summary, заголовок Table of Contents уровня h2.
- Добавлены `doc.go` для всех пакетов `internal/` (`bfd`, `gobgp`,
  `sdnotify`, `version`); существующие inline package-комментарии удалены
  из логических файлов.
- `.golangci.yml` мигрирован с устаревшего `gomodguard` на `gomodguard_v2`
  с новой схемой module-list.

### Безопасность

- Toolchain Go поднят до `1.26.3` в `go.mod`, dev-контейнере,
  runtime-контейнерах и GitHub Actions. Закрывает
  [GO-2026-4986 / CVE-2026-39820](https://pkg.go.dev/vuln/GO-2026-4986)
  в `html/template`.

### Исправлено

- Новая Make-цель `dev-ensure` пересобирает dev-контейнер, если его
  bind-mount source не совпадает с `$(CURDIR)`. Устраняет ошибку
  `crun: getcwd: Operation not permitted`, всплывавшую после удаления
  worktree без пересборки dev-контейнера.
- Цель `lint-spell` больше не ссылается на документы, перенесённые в
  `.archive/sprints/`; теперь линтит каноничный `roadmap.md`.
- `lint-yaml` исключает каталоги `.archive/` и `.serena/`.
- В dev-контейнере `golangci-lint` поднят до `v2.12.1`, чтобы
  соответствовать миграции на `gomodguard_v2` на хосте.
- Linux rtnetlink interface monitor shutdown теперь ограничен receive
  timeout, поэтому cancellation завершается детерминированно, даже если
  закрытие netlink file descriptor не прерывает receive syscall сразу.
- Authenticated BFD sessions теперь сериализуют authentication section в
  cached transmit packet перед отправкой, поэтому declarative RFC 5880 auth
  sessions могут устанавливаться с peers, требующими authentication.
- Устранены `goconst` нарушения: литералы `single-hop`/`multi-hop`
  вынесены в константы `cmd/gobfdctl/commands`, `_uuid` -- в
  `internal/netio.ovsdbUUIDColumn`.
- Удалены устаревшие `//nolint:gosec` директивы из `internal/bfd/manager.go`,
  `internal/netio/rawsock_linux.go`, `internal/netio/sender.go` и
  `test/interop-rfc/echo-reflector/main.go`.

## [0.5.2] - 2026-05-01

### Исправлено

- Восстановлен каноничный текст лицензии Apache-2.0, чтобы pkg.go.dev мог
  определить лицензию модуля и показывать документацию пакетов.

## [0.5.1] - 2026-05-01

### Добавлено

- Badge pkg.go.dev в README и package documentation для command packages:
  `gobfd`, `gobfdctl`, `gobfd-haproxy-agent` и `gobfd-exabgp-bridge`.

### Изменено

- PR benchmark comparison теперь запускает только стабильные hot-path
  benchmarks и использует явный timeout для `go test`.

## [0.5.0] - 2026-05-01

### Добавлено

- Repository governance и community-health files:
  `CODE_OF_CONDUCT.md`, `SUPPORT.md`, `GOVERNANCE.md`, `MAINTAINERS.md`,
  `.github/CODEOWNERS`, `.github/pull_request_template.md`, issue forms и
  `.github/repository-settings.md`.
- Аудит консистентности кодовой базы
  `docs/ru/codebase-consistency-audit.md`, сверяющий README/docs/API/CLI/config
  с фактической реализацией и независимой production-применимостью в сетевых
  сценариях.
- Linux rtnetlink monitor интерфейсов для событий `RTM_NEWLINK` /
  `RTM_DELLINK`, с немедленным переводом BFD-сессий на отказавшем интерфейсе
  в `Down` / `Path Down`.
- Исследовательская заметка S4 по Linux netlink и eBPF с обоснованием выбора
  rtnetlink для мониторинга состояния интерфейсов.
- Каноничный поэтапный план разработки `docs/ru/implementation-plan.md`,
  согласованный с Keep a Changelog, SemVer, Conventional Commits,
  Compose Specification, Containerfile, `.containerignore` и containers.conf.
- Podman-only проверки документации: `make lint-md`, `make lint-yaml`,
  `make lint-spell`, `make lint-docs` и `make lint-commit`.
- Конфигурации `.containerignore`, Markdown lint, YAML lint, cspell и
  commitlint на уровне репозитория.
- CI-задачи для проверки документации и Conventional Commit в заголовках pull
  request.
- CI spell-check paths теперь используют каноничные planning docs из
  `docs/en/` и community-health files.
- Gate `make gopls-check` на базе `gopls v0.21.1` в Podman dev-контейнере.
- Декларативное подключение аутентификации RFC 5880 для BFD-сессий из YAML,
  включая валидацию статического хранилища ключей и отображение типа auth в
  API/snapshot сессии.
- Поля управления ключами RFC 5880 в gRPC `AddSession`: `auth_type`,
  `auth_key_id` и `auth_secret`.
- Флаги аутентификации `gobfdctl session add`: `--auth-type`,
  `--auth-key-id` и `--auth-secret`.
- Словарь типов сессий публичного API для RFC 9747 Echo, RFC 7130 Micro-BFD,
  RFC 8971 VXLAN и RFC 9521 Geneve.
- Production security policy для BFD authentication, экспозиции ConnectRPC,
  GoBGP TLS/localhost границ, контейнерных привилегий и ownership
  vulnerability gate.
- Заметка о применимости Micro-BFD, VXLAN BFD и Geneve BFD в Linux:
  `docs/ru/linux-advanced-bfd-applicability.md`.
- Generic production runbooks в `docs/en/16-production-runbooks.md` и
  `docs/ru/16-production-runbooks.md` для Kubernetes, BGP failover,
  Prometheus alerts, packet verification и открытых production gaps.
- Runbook FRR/GoBGP BGP fast-failover с RFC packet checks,
  troubleshooting и optional public Arista EOS verification notes.
- Micro-BFD actuator hook и guarded policy layer `netio.LAGActuator` для Linux
  LAG enforcement.
- Owner-aware конфигурация `micro_bfd.actuator` и daemon dry-run wiring для
  kernel bond, OVS и NetworkManager backend-ов Micro-BFD enforcement.
- Linux kernel-bond backend для Micro-BFD enforcement, который пишет RFC 7130
  remove/add действия через bonding sysfs при явном `backend: kernel-bond` и
  `owner_policy: allow-external`.
- OVS backend для Micro-BFD enforcement, который запускает команды
  `ovs-vsctl del-bond-iface` и `ovs-vsctl add-bond-iface` при явном
  `backend: ovs` и `owner_policy: allow-external`.
- OVSDB API research, фиксирующий OVSDB JSON-RPC как native OVS integration
  path и `libovsdb` как предпочтительный Go route для следующего backend.
- Native OVSDB backend для Micro-BFD enforcement с `backend: ovs`, который
  использует `libovsdb` transactions против `Port.interfaces` и настраиваемый
  `micro_bfd.actuator.ovsdb_endpoint`.
- NetworkManager D-Bus backend для Micro-BFD enforcement с `backend:
  networkmanager`, который использует `GetDeviceByIpIface`,
  `ActiveConnection`, `DeactivateConnection`, `AvailableConnections`,
  `GetSettings` и `ActivateConnection` для управления NM-owned bond port
  profiles.
- Модель overlay backend для VXLAN/Geneve с явным ownership `userspace-udp`
  и зарезервированными именами `kernel`, `ovs`, `ovn`, `cilium`, `calico` и `nsx`.
- Каноничная структура документации: English sources в `docs/en/`, русский
  перевод в `docs/ru/`, в корне `docs/` только глобальный индекс
  `docs/README.md`.
- Русские переводы S8 planning, consistency audit, Linux advanced BFD,
  Linux netlink/eBPF и OVSDB API research документов.

### Изменено

- Documentation style теперь использует декларативные status tables,
  official standards, RFCs, primary vendor/library references и не содержит
  internal validation process artifacts в published documents.
- RFC compliance docs, примеры конфигурации и комментарии кода теперь отделяют
  реализованное обнаружение Micro-BFD от будущего Linux bond/team/OVS
  enforcement, а также описывают ограничения ownership userspace-сокетов
  VXLAN/Geneve для kernel, OVS, Cilium, Calico и NSX dataplane.
- S7.1 разделён на неразрушающий actuator config wiring, explicit
  kernel-bond enforcement, transitional OVS CLI fallback, native OVSDB backend
  и NetworkManager D-Bus backend.
- Overlay sender reconciliation теперь использует runtime VXLAN/Geneve backend,
  который уже обслуживает receiver, без повторного bind на UDP 4789/6081.
- `backend: ovs` теперь выбирает native OVSDB implementation; прежний
  `OVSLAGBackend` остаётся explicit CLI fallback type.
- Roadmap S7 теперь нацелен на независимые production integration assets, без
  привязки к site-specific контуру применимости.
- Kubernetes integration manifests теперь используют согласованные app labels,
  named ports, Linux node selection, TCP readiness/liveness probes и
  host-network DNS policy.
- Observability alert rules теперь отделяют "нет активных
  сконфигурированных сессий" от реального BFD transition Up-to-Down и
  используют flapping detection по счётчику transitions, совпадающему с
  экспортируемыми метриками GoBFD.
- `make gopls-check` теперь проверяет Linux target через `go list`, включает
  проектные build tags и падает при любых diagnostics `gopls check`, вместо
  прежнего вывода diagnostics с exit code 0.
- RFC-статус в README теперь согласован с подробными RFC compliance документами
  для Echo, Micro-BFD, VXLAN, Geneve, Unsolicited BFD, common intervals и large
  packets.
- `make all` теперь включает проверки документации; `make verify` является
  каноничным регулярным gate для сборки, тестов, линтеров, proto lint и аудита
  уязвимостей.
- Makefile-цели interop Go tests теперь выполняются через Podman dev-контейнер,
  а не через локальный Go toolchain.
- Dev-контейнер теперь включает Node.js и Python-анализаторы документации, а
  доступ к Podman socket исправлен через `security_opt: label=disable`.
- CI workflow теперь использует read-only token policy на уровне workflow и
  именованные задачи, согласованные с локальными quality gates.
- Go tools в CI и release workflow теперь запускаются через Go `tool`
  directives, записанные в `go.mod`/`go.sum`: `gotestsum`, `benchstat` и
  `golangci-lint`; Node и Python analyzer installs закрепляют версии
  `markdownlint-cli2`, `cspell`, `commitlint`, `yamllint` и `junit2html`, а
  также используют package-manager controls согласно supply-chain scanners.
- Форматирование `gobfdctl` list/show/event теперь отображает advanced session
  families вместо `unknown`.
- Политика покрытия SonarCloud и Codecov теперь исключает command entrypoints
  и host-network integration boundaries, проверяемые build, lint, security и
  system/container checks.

### Исправлено

- Graceful drain теперь проводит `SetAdminDown` через control channel сессии,
  когда session goroutine запущена: goroutine-confined cached state остаётся
  согласованным с atomic state, а отправляемый control packet несёт
  `AdminDown` / `DiagAdminDown`.
- Путь приема RFC 9747 Echo теперь принимает только looped-back пакеты с
  TTL/Hop Limit 254, сохраняя проверку TTL 255 для single-hop BFD.
- RFC interop packet capture теперь включает UDP 3785 Echo-пакеты.
- Создание сессии теперь отклоняет аутентификацию без хранилища ключей вместо
  panic во время подписи cached packet.
- Проверка hash-auth теперь отклоняет отсутствие raw wire bytes вместо panic,
  если legacy/internal caller передал только разобранный пакет.
- Аутентифицированные сессии теперь сбрасывают receive sequence window после
  2x Detection Time без валидных пакетов, а пакеты с ошибкой auth больше не
  обновляют `LastPacketReceived` и `PacketsReceived`.
- gRPC `AddSession` теперь отклоняет неполный или неожиданный auth key material
  вместо тихого создания неаутентифицированной сессии.
- gRPC `AddSession` теперь отклоняет распознанные transport-specific типы
  сессий до появления dedicated API для Echo, Micro-BFD, VXLAN и Geneve.
- Записи vulnerability allowlist теперь требуют owner, expiry, reason и
  mitigation metadata; expired entries ломают audit gate.

## [0.4.0] - 2026-02-24

### Добавлено

- Полное тестовое покрытие `cmd/gobfd/main.go` -- 32+ table-driven тестов для `configSessionToBFD`, `buildUnsolicitedPolicy`, `configEchoToBFD`, `configMicroBFDToBFD`, `buildOverlaySessionConfig`, `loadConfig`, `newLoggerWithLevel`.
- Fuzz-тесты для overlay-кодеков: `FuzzVXLANHeader`, `FuzzGeneveHeader`, `FuzzInnerPacket` с round-trip и raw-input вариантами для ненадёжного сетевого ввода.
- Бенчмарки overlay-кодеков: `BenchmarkBuildInnerPacket`, `BenchmarkStripInnerPacket`, `BenchmarkVXLAN/GeneveHeaderMarshal/Unmarshal` (0 аллокаций/оп).
- Тестовое покрытие `internal/version` -- формат `Full()`, значения по умолчанию.
- Тестовое покрытие `gobfd-haproxy-agent` -- конкурентность `stateMap`, `handleAgentCheck` через `net.Pipe()`, `loadConfig`, `envOrDefault`.
- Тестовое покрытие `gobfd-exabgp-bridge` -- `handleStateChange` с перехватом stdout, `envOrDefault`.
- Бенчмарки масштабирования сессий: `BenchmarkManagerCreate100/1000Sessions`, `BenchmarkManagerDemux1000Sessions` (проверка O(1) демультиплексирования), `BenchmarkManagerReconcile`.
- Настраиваемые буферы сокетов через `socket.read_buffer_size` и `socket.write_buffer_size` (по умолчанию 4 МиБ каждый) для `SO_RCVBUF`/`SO_SNDBUF` на слушателях и отправителях.
- `os.Root` для безопасного доступа к файлам конфигурации в `config.Load` и `gobfd-haproxy-agent` `loadConfig` (защита Go 1.26 от обхода пути).
- `GOEXPERIMENT=goroutineleakprofile` в dev-контейнере для профилирования утечек горутин в runtime.
- HTTP-endpoint `runtime/trace.FlightRecorder` для посмертной отладки.
- Комментарии к PR с результатами бенчмарков в CI через `actions/github-script`.
- Пакет `internal/sdnotify` вместо внешней зависимости `go-systemd`.
- Тесты config, server, netio и интеграции GoBGP (Sprint 1 quality foundation).

### Изменено

- Версия golangci-lint зафиксирована на `v2.1.6` в CI и release воркфлоу (было `@latest`).
- Добавлен флаг `-race` в SonarQube тестовый воркфлоу для обнаружения гонок данных.
- CI-бенчмарки расширены с `./internal/bfd/` до `./...` для покрытия overlay-кодеков.
- Замена `errors.As` на `errors.AsType[T]()` Go 1.26 в тестах сервера для типобезопасного сопоставления ошибок.
- 15 тестов с таймерами конвертированы в `testing/synctest` для детерминированного выполнения с виртуальным временем.
- Замена внешней зависимости `go-systemd` на `internal/sdnotify` (ноль внешних зависимостей для systemd notify).

## [0.3.0] - 2026-02-24

### Добавлено

- RFC 7419: поддержка общего интервала для согласования таймеров BFD-сессий.
- RFC 9468: незапрашиваемый режим BFD для бессессионных приложений с пассивным слушателем.
- RFC 9747: неаффилированная функция эхо BFD с приёмником и рефлектором эхо-пакетов.
- RFC 7130: Micro-BFD для LAG-интерфейсов с посессионным мониторингом участников и агрегированным состоянием.
- RFC 8971: BFD для VXLAN-туннелей с обработкой пакетов с учётом оверлея.
- RFC 9521: BFD для Geneve-туннелей с инкапсуляцией option-C.
- RFC 9384: BGP Cease NOTIFICATION подкод 10 (BFD Down) через интеграцию с GoBGP.
- Скрипт подготовки вендорной interop-лаборатории (`test/interop-clab/bootstrap.py`): автоматическая подготовка образов для Nokia SR Linux, SONiC-VS, FRRouting, VyOS, Arista cEOS, Cisco XRd.
- RFC-специфичный набор interop-тестов (`test/interop-rfc/`): выделенные тесты для незапрашиваемого BFD, функции эхо и BGP Cease notification.
- Поддержка вендорного interop Cisco XRd с конфигурацией XR и обработкой лимитов PID.
- Улучшения interop SONiC-VS с надёжным скриптом конфигурации BGP/BFD.

### Изменено

- Вендорный interop `run.sh` корректно пропускает вендоров с ошибкой инициализации вместо прерывания.

## [0.2.0] - 2026-02-23

### Добавлено

- Тестирование BFD с двойным стеком IPv6 в наборе вендорных interop-тестов (RFC 5881 Section 5): Arista cEOS, Nokia SR Linux, FRRouting с ULA-адресами fd00::/8 и /127-префиксами по RFC 6164.
- Интеграция SonarCloud для непрерывного анализа качества кода.
- Интеграция Codecov для отслеживания покрытия тестами.
- Рабочие процессы CodeQL и gosec SARIF для глубокого анализа безопасности.
- Конфигурация Dependabot для автоматического обновления зависимостей (Go, Docker, GitHub Actions).
- Руководство по ведению changelog (docs/en/10-changelog.md, docs/ru/10-changelog.md).
- Сканирование уязвимостей `osv-scanner` в CI и Makefile (`make osv-scan`).
- Форматировщики `gofumpt` и `golines` (max-len: 120) в golangci-lint.
- Полноцикловые interop-тесты BGP+BFD: GoBFD+GoBGP <-> FRR, BIRD3, ExaBGP (3 сценария с проверкой маршрутов).
- Вендорные BFD interop-тесты Containerlab: Nokia SR Linux, FRRouting, Arista cEOS (доступны); Cisco XRd, SONiC-VS, VyOS (определены, пропуск при отсутствии образа).
- Поддержка Arista cEOS 4.35.2F: `start_arista_ceos()` с 8 обязательными переменными окружения, `wait_arista_ceos()` проверка загрузки, BFD через BGP.
- Исправление таймеров BFD Nokia SR Linux: перезапуск субинтерфейса после коммита конфигурации для согласования на 300 мс.
- Интеграция netlab задокументирована как перспективное направление для тестирования на VM вендоров.
- Пример интеграции: GoBFD + GoBGP + FRR (быстрое переключение BGP с отзывом маршрутов).
- Пример интеграции: GoBFD + HAProxy (agent-check для проверки здоровья бэкенда с переключением менее секунды).
- Пример интеграции: GoBFD + Prometheus + Grafana (наблюдаемость с 4 правилами алертинга).
- Пример интеграции: GoBFD + ExaBGP (anycast-анонсирование сервиса через BFD-управляемый process API).
- Пример интеграции: GoBFD DaemonSet в Kubernetes (k3s с GoBGP sidecar и host networking).
- Новый бинарник: `gobfd-haproxy-agent` — мост agent-check HAProxy для мониторинга здоровья через BFD.
- Новый бинарник: `gobfd-exabgp-bridge` — мост process API ExaBGP для управления анонсами маршрутов через BFD.
- tshark-сайдкар для верификации пакетов во всех интеграционных стеках.
- Документация по интеграциям (docs/en/11-integrations.md, docs/ru/11-integrations.md).
- Цели Makefile для всех примеров интеграции (`int-bgp-failover`, `int-haproxy`, `int-observability`, `int-exabgp-anycast`, `int-k8s`).
- Отображение версии (`--version`) для всех бинарников с хешем коммита и датой сборки.
- Общий пакет версии (`internal/version`) с инжекцией через ldflags.
- Инжекция версии в Makefile, CI, GoReleaser и всех Containerfile.

### Изменено

- `make build` теперь инжектирует версию, хеш коммита и дату сборки через ldflags для всех 4 бинарников.
- Замена `c-bata/go-prompt` на `reeflective/console` для интерактивной оболочки.
- Расширение golangci-lint с 39 до 68 линтеров со строгой конфигурацией, ориентированной на безопасность.
- Разделение CI-воркфлоу на параллельные задачи (build-and-test, lint, vulnerability-check, sonarcloud, buf).
- Улучшение воркфлоу релиза для извлечения release notes из CHANGELOG.md.
- Переименование метрики Prometheus `gobfd_bfd_sessions_total` в `gobfd_bfd_sessions` (исправление конвенции).

## [0.1.0] - 2026-02-21

### Добавлено

- Кодек BFD Control-пакетов с round-trip fuzz-тестированием.
- Табличная FSM, соответствующая RFC 5880 Section 6.8.6.
- Пять режимов аутентификации: Simple Password, Keyed MD5/SHA1, Meticulous MD5/SHA1.
- Абстракция raw-сокетов для Linux (UDP 3784/4784, TTL=255 GTSM).
- Менеджер сессий с аллокацией дискриминаторов и detection timeout.
- Сервер ConnectRPC/gRPC с перехватчиками восстановления и логирования.
- CLI `gobfdctl` с командами Cobra и интерактивной оболочкой.
- Интеграция GoBGP с демпфированием осцилляций BFD (RFC 5882 Section 3.2).
- Коллектор метрик Prometheus и дашборд Grafana.
- Интеграция systemd (Type=notify, watchdog, SIGHUP горячая перезагрузка).
- Конфигурация YAML с наложением переменных окружения (koanf/v2).
- Фреймворк interop-тестов на 4 пирах (FRR, BIRD3, aiobfd, Thoro/bfd).
- Пакеты Debian и RPM через GoReleaser nfpms.
- Docker-образ, публикуемый в ghcr.io/dantte-lp/gobfd.
- CI-пайплайн: сборка, тесты, линтер, govulncheck, buf lint/breaking.
- Двуязычная документация (английский и русский).

[Не выпущено]: https://github.com/dantte-lp/gobfd/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/dantte-lp/gobfd/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/dantte-lp/gobfd/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/dantte-lp/gobfd/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/dantte-lp/gobfd/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/dantte-lp/gobfd/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/dantte-lp/gobfd/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/dantte-lp/gobfd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dantte-lp/gobfd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dantte-lp/gobfd/releases/tag/v0.1.0
