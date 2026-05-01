# Журнал изменений

Все заметные изменения в этом проекте документируются в данном файле.

Формат основан на [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/),
проект придерживается [Семантического версионирования](https://semver.org/lang/ru/spec/v2.0.0.html).

## [Не выпущено]

### Добавлено

- Аудит консистентности кодовой базы
  `docs/03-codebase-consistency-audit.md`, сверяющий README/docs/API/CLI/config
  с фактической реализацией и независимой production-применимостью в сетевых
  сценариях.
- Linux rtnetlink monitor интерфейсов для событий `RTM_NEWLINK` /
  `RTM_DELLINK`, с немедленным переводом BFD-сессий на отказавшем интерфейсе
  в `Down` / `Path Down`.
- Исследовательская заметка S4 по Linux netlink и eBPF с обоснованием выбора
  rtnetlink для мониторинга состояния интерфейсов.
- Каноничный поэтапный план разработки `docs/02-implementation-plan.md`,
  согласованный с Keep a Changelog, SemVer, Conventional Commits,
  Compose Specification, Containerfile, `.containerignore` и containers.conf.
- Podman-only проверки документации: `make lint-md`, `make lint-yaml`,
  `make lint-spell`, `make lint-docs` и `make lint-commit`.
- Конфигурации `.containerignore`, Markdown lint, YAML lint, cspell и
  commitlint на уровне репозитория.
- CI-задачи для проверки документации и Conventional Commit в заголовках pull
  request.
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
  `docs/04-linux-advanced-bfd-applicability.md`.
- Generic production runbooks в `docs/en/16-production-runbooks.md` и
  `docs/ru/16-production-runbooks.md` для Kubernetes, BGP failover,
  Prometheus alerts, packet verification и открытых production gaps.
- Runbook FRR/GoBGP BGP fast-failover с RFC packet checks,
  troubleshooting и optional public Arista EOS verification notes.
- Micro-BFD actuator hook и guarded policy layer `netio.LAGActuator` для
  будущего Linux LAG enforcement.
- Owner-aware конфигурация `micro_bfd.actuator` и daemon dry-run wiring для
  будущих kernel bond, OVS и NetworkManager backend-ов Micro-BFD enforcement.
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

### Изменено

- RFC compliance docs, примеры конфигурации и комментарии кода теперь отделяют
  реализованное обнаружение Micro-BFD от будущего Linux bond/team/OVS
  enforcement, а также описывают ограничения ownership userspace-сокетов
  VXLAN/Geneve для kernel, OVS, Cilium и NSX dataplane.
- S7.1 разделён на неразрушающий actuator config wiring, explicit
  kernel-bond enforcement, transitional OVS CLI fallback, native OVSDB backend
  и следующий sprint NetworkManager backend.
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
- Форматирование `gobfdctl` list/show/event теперь отображает advanced session
  families вместо `unknown`.

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

[Не выпущено]: https://github.com/dantte-lp/gobfd/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/dantte-lp/gobfd/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/dantte-lp/gobfd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dantte-lp/gobfd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dantte-lp/gobfd/releases/tag/v0.1.0
