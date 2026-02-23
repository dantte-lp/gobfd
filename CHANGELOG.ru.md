# Журнал изменений

Все заметные изменения в этом проекте документируются в данном файле.

Формат основан на [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/),
проект придерживается [Семантического версионирования](https://semver.org/lang/ru/spec/v2.0.0.html).

## [Не выпущено]

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

[Не выпущено]: https://github.com/dantte-lp/gobfd/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/dantte-lp/gobfd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dantte-lp/gobfd/releases/tag/v0.1.0
