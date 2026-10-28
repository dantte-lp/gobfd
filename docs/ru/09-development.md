# Разработка

Разработка следующей продуктовой линии ведётся в ветке `dev` с закреплённым инструментарием Podman. Обязательные правила заданы в [AGENTS.md](../../AGENTS.md), команды — в [Makefile](../../Makefile). Успешная проверка исходного кода не является приёмкой демона, сети или выпуска.

## Подготовка рабочей копии

1. Выберите ветку: `dev` для функций, `release/vMAJOR.MINOR` для исправления поддерживаемой линии. Направьте pull request в ту же линию. Сохраните контракт GoBGP v3 для v0.6.
2. Установите Podman, Git, GNU Make и util-linux `flock`. Настройте закреплённый провайдер Compose на Go по [процедуре развёртывания](06-deployment.md#podman-compose).
3. Задайте разные `COMPOSE_PROJECT_NAME` для параллельных рабочих копий. По умолчанию используется нормализованное имя каталога; одинаковые имена создают коллизию.
4. Запустите среду и проверьте её проект:

```bash title="Среда разработки"
make up
make dev-project
make dev-ps
```

`go.mod`, `tools/go.mod` и `deployments/docker/Containerfile.dev` закрепляют Go 1.27.3. Сборка, тесты и линтер выполняются в контейнере dev. `dev-ensure` проверяет bind mount `/app` и может пересоздать контейнер при смене рабочей копии. `make up` может собирать образы; это не проверка только для чтения.

## Процедура изменения

1. Зафиксируйте область изменения, критерии приёмки, зависимости и владельца проверки.
2. Прочитайте вызывающий код, тесты, конфигурацию и затронутые публичные контракты.
3. Измените рукописные исходники; перегенерируйте protobuf вместо ручной правки `pkg/bfdpb/`.
4. Выполните целевой тест с race detector, затем обычный набор проверок:

```bash title="Целевая и общие проверки"
make test-run RUN=TestFSMTransition PKG=./internal/bfd
make lint-docs
make verify
```

5. Для изменений протокола, транспорта, аутентификации или формата пакетов выполните соответствующую [interop-проверку](05-interop.md). Для горячих путей и компилятора выполните бенчмарки.
6. При изменении пользовательского поведения обновите обе языковые версии руководств и журналов изменений. Укажите в pull request точные идентификаторы исходников и бинарников, выполненные проверки, пропуски и оставшуюся приёмку.
7. Запросите проверку владельцев по [.github/CODEOWNERS](../../.github/CODEOWNERS). Соблюдайте [CONTRIBUTING.md](../../CONTRIBUTING.md) и [правила выпуска](10-changelog.md).

`make all` выполняет сборку, race-тесты, gopls, линтер и проверки документации. `make verify` добавляет проверку protobuf и управляемый аудит уязвимостей. В него не входят фактическая совместимость, жизненный цикл пакетов, эталонная нагрузка и приёмка публикации.

## Среда и сборка

| Цель | Поведение |
|:---|:---|
| `up`, `dev-ensure` | Создание/запуск dev и проверка монтирования исходников |
| `dev-project`, `dev-ps` | Идентификатор проекта или состояние среды |
| `logs` | Наблюдение за журналом dev |
| `down`, `restart` | Остановка или остановка с последующим запуском |
| `build` | Четыре бинарника с ldflags версии, commit и времени UTC |
| `all`, `verify` | Наборы проверок, определённые выше |
| `versions` | Идентификаторы установленных инструментов |

## Тестовые цели

| Цель | Область |
|:---|:---|
| `test`, `test-v` | Все обычные пакеты, race detector, один запуск без кеша; подробный вывод по выбору |
| `test-run RUN=NAME PKG=PATH` | Целевой race-тест |
| `fuzz FUNC=NAME PKG=PATH` | Один fuzz-target, бюджет 60 секунд |
| `test-integration` | Тесты Manager/API/моста пакетов в процессе, тег `integration` |
| `testcontainers-smoke` | Обязательная проверка провайдера Podman, тег `testcontainers` |
| `coverage`, `coverage-func` | Покрытие с race detector; HTML или сводка по функциям |
| `test-report` | Race-тесты и JUnit/JSON/HTML в `reports/tests/` |
| `report-all` | Отчёт тестов, снимок и сравнение бенчмарков |
| `benchmark*`, `profile` | [Измерения и профили](12-benchmarks.md) |

Обычный `go test` не выполняет все наборы с build tags. Мост в памяти не является тестом UDP или процесса. Используйте виртуальное время `testing/synctest` для детерминированной логики таймеров и ограниченные ожидания реального времени для внешних соседей.

Fuzz-targets покрывают Control, VXLAN, Geneve и внутренние пакеты. Перед запуском выберите точное имя функции в пакете. Сохраните ошибочный корпус и regression-тест; ограниченный fuzz-запуск не означает исчерпывающей проверки.

## Совместимость и интеграции

| Семейство | Команда и граница |
|:---|:---|
| Четыре соседа | `interop` — алиас `interop-testcontainers`; FRR/BIRD3/Holo/Thoro, crafted-сценарии |
| Связь с BGP | `interop-bgp` — алиас `interop-bgp-testcontainers` |
| Сценарии RFC | `interop-rfc` — алиас `interop-rfc-testcontainers` |
| Сохранённые Compose-среды | `interop[-bgp,-rfc]-up/-test/-logs/-down`; иные наборы и правила очистки |
| Containerlab | `interop-clab-bootstrap`, `interop-clab-up/-test/-down`, `interop-clab`; выполнение только собственного FRR |
| Ядро | `e2e-core`, `e2e-core-testcontainers` |
| Агрегаты | `e2e-routing`, `e2e-rfc`, `e2e-overlay`, `e2e-linux`, `e2e-vendor` |
| Подцели | `e2e-routing-test`, `e2e-rfc-test`, `e2e-overlay-test`, `e2e-linux-test`, `e2e-vendor-test` |
| Пример BGP | `int-bgp-failover[-testcontainers,-up,-logs,-down]` |
| Пример HAProxy | `int-haproxy[-testcontainers,-up,-logs,-down]` |
| Пример наблюдаемости | `int-observability[-testcontainers,-up,-logs,-down]` |
| Пример ExaBGP | `int-exabgp-anycast[-up,-logs,-down]` |
| Пример Kubernetes | `int-k8s[-up,-down]`; нужны существующий кластер и владение namespace |
| Наблюдение | `e2e-help`, `interop-capture`, `interop-pcap`, `interop-pcap-summary` |
| Алиас совместимости | `integration` — алиас `interop` |

[Реестр E2E](../../test/e2e/targets.md) отделяет проверки с assertions от демонстраций и проверки manifests. `e2e-vendor` не означает выполнения NOS; именованный RFC-тест не означает полной проверки wire-процедуры. Цели очистки могут удалять принадлежащие тесту ресурсы и требуют соответствующего разрешения.

### Проверка overlay на линии

1. Выполните `make e2e-overlay` в изолированном Linux namespace со свободными UDP 4789/6081.
2. Сохраните Go test JSON для двух внутренних кортежей при одинаковых внешних endpoints/VNI. Проверяются двунаправленный userspace UDP, TTL 255, отказ для неверной идентичности/формата, отмена, повторное использование и Close.
3. Выполните захват loopback UDP 4789/6081 существующим образом tshark в тестовом namespace. Ограничьте захват `-a duration:20 -a filesize:1024`.
4. Сопоставьте декодированные кадры и discriminators с принятыми/отклонёнными событиями теста; сохраните команду, image IDs, лимиты и hashes.
5. Повторите шесть overlay fuzz-targets с race detector, двумя workers, бюджетом 20 секунд и внешним timeout.

Файл `packets.csv` содержит сводку codec-fixture. Он не является PCAP. Эти проверки не квалифицируют vendor-backends и backends конкретного владельца overlay.

### Жизненный цикл пакетов

Выполните в закреплённом SDK с проверенным UPX в PATH:

```bash
go run ./test/cmd/cictl package-build
go run ./test/cmd/cictl package-lifecycle
```

`package-build` использует закреплённый выпуском GoReleaser, тестовую версию `v0.0.0` и native-финализацию RPM. Публикация отключена; требуется Git checkout. `package-lifecycle` требует API Podman и проверяет DEB в Debian с systemd: запуск, sandbox, работу дольше watchdog, reload с тем же PID, перезапуск после SIGKILL, остановку с недоступным соседом, unit, идемпотентное обновление enabled/disabled и удаление работающего сервиса. Upgrade-fixtures меняют только control-версию пакета; они не квалифицируют обновление между реализациями демона.

Лимиты сборки — 2 CPU/2 GiB. Лимиты выполнения — 1 CPU/1 GiB, без swap, 1024 PIDs и частный cgroup namespace; тест проверяет эффективные значения. SYS_ADMIN, SYS_PTRACE и исключения namespace/security относятся к одноразовой systemd-fixture. Тестовые Debian-пакеты разрешаются через зеркала. Доказательства находятся в `reports/e2e/package-lifecycle/`; генерация RPM отделена от runtime-приёмки DEB.

## Качество, protobuf и зависимости

| Цель | Контракт |
|:---|:---|
| `lint`, `lint-ci` | Канонический Go-wrapper линтера с базовым и tagged-профилями |
| `lint-fix` | Исправления линтера; полученный diff требует проверки |
| `gopls-check` | Ограниченная диагностика, включая Darwin-профиль генератора зависимостей |
| `lint-md`, `lint-yaml`, `lint-spell`, `lint-docs` | Markdown, YAML, выбранные английские файлы; общий documentation gate |
| `lint-commit MSG='fix(bfd): reject invalid input'` | Проверка одного Conventional Commit subject |
| `semgrep`, `semgrep-json`, `semgrep-pro` | Выбранный Semgrep на хосте; Pro требует собственных engine/access |
| `vulncheck`, `osv-scan` | Управляемый аудит govulncheck + OSV |
| `vulncheck-strict`, `osv-scan-strict` | Прямые scans без исключений проекта |
| `proto-gen`, `proto-lint` | Генерация или проверка protobuf |
| `proto-breaking`, `proto-update` | Сравнение с локальным master или обновление Buf dependencies |
| `dependency-inventory`, `dependency-inventory-check` | Генерация inventory или офлайн-проверка |
| `tidy`, `download` | Tidy или загрузка runtime dependencies |
| `clean` | Удаление daemon/CLI и Go caches; bridge-бинарники сохраняются |

CI проверяет drift генерации и сравнивает protobuf с точным fetched commit базы PR; локальный `proto-breaking` использует `master`. Инструменты принадлежат `tools/go.mod`. Сохраняйте отдельные runtime/tools graphs. Inventory фиксирует наблюдения; офлайн-проверка доказывает согласованность со снимком, а не актуальность upstream, безопасность или принятие выпуском. Исключения advisory ограничены точным module, scanner, reachability, owner и expiry; см. [реестр рисков](reference/dependency-risk.md).

## Границы выполнения инструментов

| Точка входа | Компилятор и runtime |
|:---|:---|
| Обычные Make-цели | Go в выбранном dev-контейнере |
| Пять агрегатов E2E | Компилятор с digest без engine socket собирает игнорируемый `bin/e2ectl`; хост запускает этот бинарник |
| Пять Containerlab-команд | Новая частная сборка controller без engine socket; отдельные race-компилятор и Debian runtime |
| Сохранённые bootstrap/integration `interopctl` и `benchmark-report` | Прямой `go run`; нужен явный запуск в закреплённом контейнере или точный pinned host compiler |
| `lint-ci` и команды CI | Прямые Go entrypoints для закреплённого CI-контейнера |
| `semgrep*` | Выбранный исполняемый файл на хосте |

Блокировка каталога агрегатов E2E охватывает пересоздание dev, сборку и выполнение; ожидание — 120 секунд. Лимиты compiler — 2 CPU/2 GiB/512 PIDs, исходники только для чтения, запись только binary output. Make dry-run печатает делегирование без создания каталога. Containerlab help/dry-run/down также собирают новый controller; старый бинарник не служит fallback.

Инструментарий репозитория принадлежит Go. Не добавляйте Python-среды, Bash-скрипты репозитория и shell-программы в CI. Единственное Python runtime-исключение — checksum-pinned upstream ExaBGP в Debian peer image. Пакетные startup-внутренности vendor не являются launcher репозитория. Контракты команд приведены в [справочнике CI и выпуска](reference/automation.md).

## Правила кода и runtime

| Область | Требование |
|:---|:---|
| Ошибки | Контекст и `%w`; `errors.Is`/`errors.As` |
| Конкурентность | Context первым, goroutines ограничены отменой, channel закрывает sender |
| Логи | Структурированный `log/slog`; защита чувствительных данных |
| Структура | Малые интерфейсы у consumer; группы imports standard/external/internal |
| Протокол | [RFC 5880 §6.8.6](../../docs/rfc/rfc5880.txt), микросекунды, доказательства процедуры |
| Память | Измерение именованного пути; без общих обещаний GC и масштаба |
| Unsafe/generated | Без рукописного `unsafe`; protobuf перегенерируется |
| Контейнеры | Debian trixie с digest; сохранение исторических контрактов линий |

Сохраняйте stdversion vet. Используйте `synctest.Sleep` только в детерминированных test bubbles. Сохраняйте проверки JSON-совместимости, именованные лимиты HTTP headers и изоляцию pprof. Проект не задаёт глобальный GOMAXPROCS; квалифицируйте GOMEMLIMIT и сохраняйте стандартный GOGC без основания измерениями. Go API и удалённые compatibility settings определяются [контрактом pinned baseline](../../AGENTS.md), а не иллюстративными примерами.

## Диагностика

| Симптом | Проверка | Действие |
|:---|:---|:---|
| Тестируется другая рабочая копия | `make dev-project`, `make dev-ps`, mount | Задайте отдельный проект и выполните `dev-ensure` |
| Compose не запускается или требует Buildx | Provider и DOCKER_BUILDKIT | Повторите [bootstrap провайдера](06-deployment.md#podman-compose) |
| Обычные тесты проходят, tagged-код нет | Точный build-tag profile | Выполните canonical lint и выбранный E2E gate |
| Отчёт расходится с журналом | Run ID, revision, timestamps, ожидаемые файлы | Сохраните сырой вывод ошибки; используйте новый evidence directory |
| Scan зависимостей не проходит | Reachability, точное исключение и expiry | Исправьте зависимость или оформите ограниченное решение |
| Очистка не завершена | Записанные IDs, метки владения, журналы | Восстановите через owning controller; сохраните чужие ресурсы |

## Связанные документы

[Совместимость](05-interop.md), [бенчмарки](12-benchmarks.md), [измерения runtime](14-performance-analysis.md), [E2E harness](../../test/e2e/README.md), [Smallstep fixture](../../test/stepca/README.md).
