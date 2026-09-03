# Разработка

![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![golangci--lint](https://img.shields.io/badge/golangci--lint-v2-1a73e8?style=for-the-badge)
![buf](https://img.shields.io/badge/buf-Protobuf-4353FF?style=for-the-badge)
![Podman](https://img.shields.io/badge/Podman-Dev_Container-892CA0?style=for-the-badge&logo=podman)
![synctest](https://img.shields.io/badge/synctest-Virtual_Time-34a853?style=for-the-badge)

> Рабочий процесс разработки, Make-цели, стратегия тестирования, линтинг, генерация protobuf и руководство по вкладу.

---

## Содержание

- [Предварительные требования](#предварительные-требования)
- [Настройка разработки](#настройка-разработки)
- [Работа с ветками и релизами](#работа-с-ветками-и-релизами)
- [Make-цели](#make-цели)
- [Стратегия тестирования](#стратегия-тестирования)
- [Линтинг](#линтинг)
- [CI-хелперы без shell](#ci-хелперы-без-shell)
- [Рабочий процесс Protobuf](#рабочий-процесс-protobuf)
- [Baseline Go 1.27](#baseline-go-127)
- [Конвенции кода](#конвенции-кода)
- [Вклад в проект](#вклад-в-проект)

### Предварительные требования

- **Podman** + **Podman Compose** (все команды выполняются в контейнерах)
- **Git** для управления версиями
- Go 1.27 (нужен только для поддержки IDE; сборка в контейнерах)

> **Важно**: Все тестирование, сборка и линтинг выполняются внутри контейнеров Podman. Локальный Go-тулчейн не требуется для CI-эквивалентных сборок.

### Настройка разработки

```bash
# Клонирование репозитория
git clone https://github.com/dantte-lp/gobfd.git
cd gobfd

# Запуск контейнера разработки
make up

# Сборка всех бинарных файлов
make build

# Запуск тестов
make test

# Запуск линтера
make lint

# Всё вместе
make all
```

### Работа с ветками и релизами

У веток разные роли в развитии продукта:

- `dev` служит интеграционной веткой для следующей продуктовой линии. Ветки для
  функциональных изменений создаются от `dev` и вливаются в `dev`; стабильные
  релизы никогда не получают тег непосредственно на `dev`.
- `master` — ветка по умолчанию с последним принятым стабильным состоянием.
- Поддерживаемые линии используют имена `release/vMAJOR.MINOR`. Линия
  `release/v0.6` сохраняет GoBGP v3.37.0 и публичные контракты v0.6.

Исправление для v0.6 начинается от `release/v0.6` в короткоживущей ветке
`fix/v0.6-*` и возвращается в `release/v0.6` через проверенный pull request.
После приёмки сопровождающие проверяют наличие того же дефекта в `master` и
`dev`; если исправление применимо, его переносят отдельным проверенным pull
request.
Подготовка релиза проходит тем же путём в соответствующей поддерживаемой линии.

Набор правил для релизных веток должен быть активен до создания каждой новой
подходящей релизной ветки. Набор правил для тегов должен быть активен до
создания каждого нового подходящего тега, в частности до `v0.6.2`. Существующие
теги с `v0.1.0` по `v0.6.1` сохраняются без изменений: их никогда не
перемещают, не удаляют и не используют повторно. Новый стабильный тег указывает
на точный проверенный коммит в подходящей релизной ветке; его также никогда не
перемещают, не удаляют и не используют повторно. GitHub Actions по событию тега
создаёт черновик релиза GitHub, полностью формирует и проверяет его описание и
артефакты, а затем автоматически публикует релиз как последнее изменение.

### Make-цели

Обычные цели сборки и тестирования запускают Go внутри Podman через
`podman compose exec`.
Development stack изолирован через `COMPOSE_PROJECT_NAME`, который по
умолчанию равен имени директории текущего checkout. Parallel worktrees
используют разные default project names или явно задают `COMPOSE_PROJECT_NAME`.

Интерактивной Make-цели для shell нет. Для отдельного неинтерактивного теста
следует использовать управляемую Make маршрутизацию Compose project, например:

```bash
make test-run RUN=TestFSMTransition PKG=./internal/bfd
```

#### Жизненный цикл

| Цель | Описание |
|---|---|
| `make up` | Запуск контейнера разработки |
| `make down` | Остановка контейнера |
| `make restart` | Перезапуск (down + up) |
| `make logs` | Просмотр логов контейнера |
| `make dev-project` | Показать Compose project name active checkout |
| `make dev-ps` | Показать development stack active checkout |

#### Сборка и тесты

| Цель | Описание |
|---|---|
| `make all` | Сборка + тесты + линтинг |
| `make build` | Компиляция всех бинарных файлов с информацией о версии |
| `make test` | Все тесты с `-race -count=1` |
| `make test-v` | Подробный вывод тестов |
| `make test-run RUN=TestFSM PKG=./internal/bfd` | Конкретный тест |
| `make fuzz FUNC=FuzzControlPacket PKG=./internal/bfd` | Фаззинг (60с) |
| `make test-integration` | Интеграционные тесты |

#### Тесты совместимости

| Цель | Описание |
|---|---|
| `make interop` | Полный цикл: сборка + запуск + тесты + очистка |
| `make interop-up` | Запуск 4-пировой топологии |
| `make interop-test` | Запуск Go-тестов совместимости |
| `make interop-down` | Остановка и очистка |
| `make interop-logs` | Просмотр логов interop-контейнеров |
| `make interop-capture` | Живой захват BFD-пакетов |
| `make interop-pcap` | Расшифровка захваченных пакетов |
| `make interop-pcap-summary` | CSV-сводка захватов |
| `make interop-bgp` | Полный цикл BGP+BFD тестов (FRR, BIRD3, ExaBGP) |
| `make interop-bgp-up` | Запуск топологии BGP+BFD |
| `make interop-bgp-test` | Запуск Go-тестов BGP+BFD |
| `make interop-bgp-down` | Остановка топологии BGP+BFD |
| `make interop-clab-bootstrap` | Подготовка вендорных образов и receipt-owned образа GoBFD (`ARGS=...`) |
| `make interop-clab` | Полный цикл вендорных NOS-тестов (Nokia, FRR и др.) |
| `make interop-clab-up` | Деплой вендорной NOS-топологии |
| `make interop-clab-test` | Запуск вендорных interop Go-тестов |
| `make interop-clab-down` | Удаление записанных ресурсов NOS и точного owned-образа GoBFD |

#### Примеры интеграций

Testcontainers-цели для core, BGP fast failover, HAProxy health и observability
делегируют сбор отчётов Go-owned runner `test/cmd/e2ectl`. Он создаёт
эксклюзивный каталог отчёта, направляет одинаковый вывод `go test -json` в
`go-test.json` и `go-test.log` и сохраняет код завершения теста.

| Цель | Описание |
|---|---|
| `make e2e-core-testcontainers` | Core daemon testcontainers gate и отчёт `e2ectl` |
| `make int-bgp-failover-testcontainers` | BGP fast-failover testcontainers gate и отчёт `e2ectl` |
| `make int-haproxy-testcontainers` | HAProxy health testcontainers gate и отчёт `e2ectl` |
| `make int-observability-testcontainers` | Observability testcontainers gate и отчёт `e2ectl` |
| `make int-bgp-failover` | Go testcontainers gate для BGP fast failover; операционный Compose-пример доступен через `-up`, `-logs` и `-down` |
| `make int-haproxy` | Демо HAProxy agent-check bridge |
| `make int-observability` | Стек наблюдаемости Prometheus + Grafana |
| `make int-exabgp-anycast` | Анонсирование anycast-сервиса ExaBGP |
| `make int-k8s` | Kubernetes DaemonSet с GoBGP sidecar |

#### Качество

| Цель | Описание |
|---|---|
| `make lint` | Запуск golangci-lint v2 |
| `make lint-fix` | Автоматическое исправление проблем линтера |
| `make semgrep` | Локальный Semgrep OSS scan с ruleset `p/golang` |
| `make semgrep-json` | Semgrep OSS scan с JSON-выводом |
| `make semgrep-pro` | Semgrep с `--pro`; требует Semgrep Pro Engine и `semgrep login` |
| `make vulncheck` | Контролируемый vulnerability audit (`govulncheck` + OSV Scanner) |
| `make osv-scan` | Алиас для контролируемого vulnerability audit |
| `make vulncheck-strict` | Raw `govulncheck ./...` без project allowlist |
| `make osv-scan-strict` | Raw `osv-scanner scan -r .` без project allowlist |

Контролируемый audit использует `go.mod` и `tools/go.mod` как раздельные входы
OSV. CI сохраняет runtime govulncheck/OSV JSON, tools OSV JSON и раздельные
runtime/tools CycloneDX SBOM в артефакте `dependency-security-reports`.

#### Protobuf

| Цель | Описание |
|---|---|
| `make proto-gen` | Генерация Go-кода из `.proto` |
| `make proto-lint` | Линтинг protobuf-определений |
| `make proto-breaking` | Проверка на несовместимые изменения |
| `make proto-update` | Обновление зависимостей buf |

#### Зависимости

| Цель | Описание |
|---|---|
| `make tidy` | Запуск `go mod tidy` |
| `make download` | Загрузка зависимостей модуля |
| `make clean` | Удаление бинарников и кэшей |
| `make versions` | Показать версии инструментов |

### Стратегия тестирования

#### Юнит-тесты

- **Table-driven** тесты для всех пакетов
- **`t.Parallel()`** где безопасно (нет общего изменяемого состояния)
- **Всегда** с `-race -count=1`
- **`goleak.VerifyTestMain(m)`** в шести concurrency-heavy пакетах, которые
  владеют жизненным циклом демона, протокола, сети, метрик, конфигурации и
  интеграционных тестов

#### Тесты FSM (`testing/synctest`)

Go 1.27 `testing/synctest` обеспечивает детерминированное тестирование на
основе виртуального времени и добавляет `synctest.Sleep` для операции
advance-time-and-settle:

```go
func TestFSMDetectionTimeout(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        sess := newTestSession(t, SessionConfig{
            DesiredMinTxInterval:  100 * time.Millisecond,
            RequiredMinRxInterval: 100 * time.Millisecond,
            DetectMultiplier:      3,
        })

        // Перевод сессии в Up
        sess.injectPacket(controlPacket(StateInit, 0))
        synctest.Wait()
        require.Equal(t, StateUp, sess.State())

        // Таймаут обнаружения = 3 x 100ms = 300ms
        synctest.Sleep(350 * time.Millisecond)
        require.Equal(t, StateDown, sess.State())
    })
}
```

Преимущества:
- Тесты работают в виртуальном времени (мгновенное выполнение)
- Детерминированность -- никаких нестабильных тестов с таймерами
- Идеально для таймеров BFD-протокола и таймаутов обнаружения

#### Фаззинг

GoBFD включает fuzz-тесты для всех парсеров пакетов, обрабатывающих ненадёжный сетевой ввод:

```bash
# Кодек BFD Control пакетов
make fuzz FUNC=FuzzControlPacket PKG=./internal/bfd

# Overlay-кодек VXLAN (RFC 8971)
make fuzz FUNC=FuzzVXLANHeader PKG=./internal/netio

# Overlay-кодек Geneve (RFC 9521)
make fuzz FUNC=FuzzGeneveHeader PKG=./internal/netio

# Сборка/разбор внутренних пакетов
make fuzz FUNC=FuzzInnerPacket PKG=./internal/netio
```

Каждый fuzz-тест имеет два варианта:
- **Round-trip**: проверяет `parse(marshal(packet)) == packet` для структурированных входных данных
- **Raw input**: подаёт произвольные байты в парсер, проверяя отсутствие паники

Длительность фаззинга по умолчанию — 60 секунд. Для более длительного запуска:

```bash
make fuzz FUNC=FuzzVXLANHeader PKG=./internal/netio FUZZTIME=300s
```

#### Интеграционные тесты

```bash
make test-integration
```

Используют `testcontainers-go` с бэкендом Podman для тестирования полного жизненного цикла демона.

#### Тесты совместимости

См. [05-interop.md](./05-interop.md) для фреймворка 4-пирового interop-тестирования.

### Линтинг

golangci-lint v2.13.1 с конфигурацией maximum-by-default:

```bash
make lint
```

Инструмент закреплён директивой `tool` в изолированном модуле `tools/go.mod`.
Локальный lint выполняется только в контейнере: `make lint` приводит dev-сервис
к актуальной конфигурации и запускает в нём заранее собранный закреплённый
бинарник. `make lint-ci` является внутренним контрактом для CI-контейнеров и
намеренно отказывается запускаться на хосте. По умолчанию dev-сервис ограничен
4 CPU, жёстким лимитом памяти 8 GiB, мягким лимитом Go runtime 6 GiB, без swap
сверх жёсткого лимита и 1 024 PID. Кэши Go и golangci-lint остаются в удаляемом
слое контейнера. Образ содержит C-компилятор и runtime-заголовки, необходимые
Linux race detector Go, поэтому race-гейты не устанавливают пакеты во время
выполнения. Все команды, собранные через `go install`, используют абсолютный
`GOBIN=/go/bin`, а `/go/bin` входит в `PATH`. При необходимости лимиты
переопределяются через `GOBFD_DEV_CPUS`,
`GOBFD_DEV_MEMORY_LIMIT`,
`GOBFD_DEV_MEMORY_RESERVATION`, `GOBFD_DEV_GOMEMLIMIT` и
`GOBFD_DEV_PIDS_LIMIT`.

В `.golangci.yml` используется
`linters.default: all`: включены 92 значимых линтера, отключены 20
поддерживаемых линтеров без входных данных проекта, с дублирующими проверками или
документированными семантическими конфликтами, а также два deprecated.
CI проверяет v2-схему, точное число активных линтеров, обычную сборку и каждый
репозиторный build tag отдельно. Ключевые проверки:

- `gosec` (с `audit: true`) -- анализ безопасности
- `govet`, `staticcheck`, `errcheck` -- стандартные проверки Go
- `noctx` -- проверки передачи контекста
- `exhaustive` -- исчерпывающие switch/map проверки
- `cyclop`, `gocognit`, `maintidx` -- ограничения сложности
- `revive`, `wrapcheck`, `gochecknoglobals`, `mnd`, `lll` -- проверки API,
  ошибок, состояния и дисциплины исходного кода
- `depguard`, `gomoddirectives` -- гигиена зависимостей
- `nolintlint` -- качество директив `//nolint`

### Политика документации и коммитов

`make lint-md` запускает репозиторный stdlib-checker Go 1.27 по непустому,
ограниченному и детерминированному набору Markdown-файлов. Fixtures сохраняют
36 активных правил markdownlint 0.41. Команда
`make lint-commit MSG='feat(bfd): add peer'` проверяет сохранённые границы
Conventional Commit для type, scope и case, блокирующего 100-байтного предела
header, неблокирующего 120-байтного предупреждения body и default-ignore. Ни
одна из этих проверок не требует Node.js или npm.

`make lint-spell` запускает закреплённый Go-инструмент `misspell` по точному
набору поддерживаемой англоязычной документации. `make lint-yaml` запускает
закреплённый Go `yamlfmt` в lint-режиме с сохраняющей строки политикой
`.yamlfmt.yaml`.

### Контракт отсутствия Python

Репозиторий не содержит Python-кода, окружения, manifest или lock-файла.
Подготовка вендорных образов, spelling, YAML-политика и преобразование JUnit в
HTML реализованы на Go. ExaBGP остаётся внешним immutable interop-образом.

### CI-хелперы без shell

В затронутых шагах GitHub Actions остаётся по одной Go-команде. Репозиторный
код получает только строгий boolean `SONAR_TOKEN_PRESENT`, а также
`GITHUB_ACTOR` и `GITHUB_OUTPUT`; исходный `SONAR_TOKEN` доступен только
закреплённому SonarSource scanner action. `cictl sonar-mode` добавляет
`mode=run` для `true` или `mode=skip-dependabot` только для `false` и точного
actor `dependabot[bot]`. Некорректные и отсутствующие значения, как и другие
запуски без токена, завершаются ошибкой; id шага и последующие условия по mode
не меняются.

`cictl build --output /tmp/gobfd-build` проверяет `GITHUB_SHA`, формирует
восьмизначные CI version metadata и время сборки UTC RFC3339, после чего
напрямую вызывает `go build` для четырёх поддерживаемых бинарных файлов. Pin
Go 1.27 и build flags сохраняются без многострочной shell-программы в workflow.

### Инвентаризация зависимостей

Машиночитаемый snapshot цепочки поставки находится в
`docs/supply-chain/dependency-inventory.json`. Он охватывает полные выбранные
графы runtime и изолированных инструментов, а также объявленные в репозитории
инструменты, GitHub Actions, OCI-образы и interop-демоны. Перегенерируйте его
только после проверки release notes,
безопасности, лицензий, archive status и неизменяемых pin:

```bash
make dependency-inventory
make dependency-inventory-check
```

Генерация разрешает каждую точную выбранную версию Go-модуля через стабильный
API deps.dev v3 и записывает GitHub evidence точного commit там, где deps.dev
не возвращает license expression.
Для immutable OCI registry availability хранится отдельно от точного commit
канонического build-source и hash его license-файла. Offline-проверка
завершается ошибкой при drift любого Go module graph, объявленного компонента,
source location, evidence binding или числа Go-пакетов относительно
проверенного snapshot.

Каждая принятая или сохранённая запись с незакрытой блокирующей проверкой
содержит собственный `review_exception`: точные измерения проверки,
ответственного, причину, Bead и дату пересмотра. Offline-проверка отклоняет
неполное или лишнее покрытие исключениями; отложенные и устаревшие решения
ограничиваются отдельно владельцем и датой пересмотра самого решения.

### Semgrep

Semgrep используется как дополнительный локальный SAST-проход:

```bash
make semgrep       # Semgrep OSS, ruleset p/golang
make semgrep-json  # тот же scan, JSON-вывод
make semgrep-pro   # требует Semgrep Pro Engine и semgrep login
```

Согласно Semgrep CLI reference, `semgrep scan` предназначен для локальных
проверок и может запускать registry rulesets вроде `p/golang` без аккаунта
Semgrep. `semgrep ci` использует политики Semgrep App, diff-aware поведение в
CI и Pro-анализ, когда CLI авторизован. Флаг `--pro` включает interfile-анализ
и требует Pro Engine плюс авторизацию.

Текущие принятые предупреждения Semgrep задокументированы в
[SECURITY.md](../../SECURITY.md): MD5 и SHA1 реализованы только для
совместимости с аутентификацией RFC 5880.
Соответствующее исключение Sonar `go:S4790` ограничено в
`sonar-project.properties` файлом `internal/bfd/auth.go`; для всех остальных
файлов правило остаётся активным. In-code resolution не используется, потому
что SonarQube Cloud поддерживает `sonar-resolve` только для языков C-family,
но не для Go.

### Рабочий процесс Protobuf

Protobuf управляется `buf`:

```bash
# После изменения api/bfd/v1/bfd.proto:
make proto-lint      # Линтинг определений
make proto-gen       # Генерация Go-кода в pkg/bfdpb/
make proto-breaking  # Проверка на несовместимые изменения vs master
```

> **НИКОГДА** не редактируйте файлы в `pkg/bfdpb/` вручную -- они генерируются через `buf generate`.

### Baseline Go 1.27

GoBFD использует Go 1.27, сохраняя нужные API безопасности,
производительности и отладки из Go 1.26.

#### `testing/synctest` -- Детерминированные тесты таймеров

Все тесты BFD-таймеров и таймаутов обнаружения используют `testing/synctest`
с виртуальным временем. Соседние `time.Sleep` и `synctest.Wait` записываются
как `synctest.Sleep`; реальные ожидания E2E и interop остаются ограниченными
контекстом wall-clock waits. См. [Тесты FSM](#тесты-fsm-testingsynctest) выше.

#### `os.Root` -- Песочница для доступа к файлам

Загрузка конфигурации использует `os.OpenRoot` для ограничения доступа к файловой системе директорией конфигурации. Это предотвращает атаки обхода пути (path traversal):

```go
root, err := os.OpenRoot(filepath.Dir(path))
if err != nil { return nil, err }
defer root.Close()
f, err := root.Open(filepath.Base(path))
```

Применяется в `config.Load` и `gobfd-haproxy-agent` `loadConfig`.

#### `errors.AsType[T]()` -- Типобезопасное сопоставление ошибок

Тесты сервера используют обобщённый сопоставитель ошибок Go 1.26 вместо двухшагового паттерна `errors.As`:

```go
// Идиоматичный Go 1.26
if connectErr, ok := errors.AsType[*connect.Error](err); ok {
    require.Equal(t, connect.CodeNotFound, connectErr.Code())
}
```

#### Диагностика утечек горутин

В Go 1.27 runtime-профиль `goroutineleak` доступен без experiment flag.
Автоматические тесты продолжают использовать `go.uber.org/goleak` в шести
concurrency-heavy пакетах. Это разные механизмы; GoBFD не регистрирует полный
набор обработчиков `net/http/pprof` на публичном metrics mux.

#### `runtime/trace.FlightRecorder`

HTTP-endpoint предоставляет flight recorder для посмертного захвата трассировки. Демон непрерывно записывает последние N секунд данных трассировки, которые можно получить по запросу для отладки всплесков задержки или дедлоков.

#### Swiss Tables

Go 1.26 ввёл Swiss tables как реализацию `map` по умолчанию. Поиск
дискриминаторов, таблица переходов FSM и демультиплексирование сессий GoBFD
выигрывают от улучшенной локальности кэша. Go 1.27 удаляет прежний
диагностический experiment `noswissmap`, поэтому он не должен появляться в
командах сборки и бенчмарков.

#### Совместимость HTTP и JSON

Оба публичных HTTP-сервера используют один явный `MaxHeaderValueCount`,
сохраняя `ReadHeaderTimeout`; parser-level тесты проверяют разрешённую и
запрещённую границы. В Go 1.27 существующий API `encoding/json` работает на
реализации v2; compatibility-тесты покрывают duplicate object members и
invalid UTF-8 в CLI, Podman, FRR и vulnerability audit без сравнения точного
текста ошибок.

### Конвенции кода

| Правило | Описание |
|---|---|
| **Ошибки** | Всегда оборачивать с `%w` и контекстом: `fmt.Errorf("send control packet to %s: %w", peer, err)` |
| **Сопоставление ошибок** | Использовать `errors.Is`/`errors.As`, никогда string matching |
| **Context** | Первый параметр, никогда не сохранять в struct |
| **Горутины** | Отправитель закрывает каналы; время жизни привязано к `context.Context` |
| **Логирование** | ТОЛЬКО `log/slog` со структурированными полями |
| **Именование** | Без повторов: `package bfd; type Session`, не `BFDSession` |
| **Импорты** | stdlib, пустая строка, external, пустая строка, internal |
| **Интерфейсы** | Маленькие, рядом с потребителями |
| **Тесты** | Table-driven, `t.Parallel()` где безопасно, всегда `-race` |
| **FSM** | Все переходы ОБЯЗАНЫ точно соответствовать RFC 5880 Section 6.8.6 |
| **Таймеры** | Интервалы BFD в МИКРОСЕКУНДАХ по RFC -- не путать с мс |

### Вклад в проект

1. Откройте issue для обсуждения изменения перед отправкой PR
2. Следуйте стилю кода (см. `AGENTS.md` для соглашений)
3. Добавляйте тесты для новой функциональности (`go test ./... -race -count=1`)
4. Убедитесь, что `make lint` проходит
5. Запустите `buf lint` при изменении proto-файлов
6. Пишите описательные и лаконичные commit-сообщения

```bash
# Цикл разработки
make up           # Запуск среды разработки
# ... внесение изменений ...
make all          # Сборка + тесты + линтинг

# Для изменений протокола:
make interop      # Проверка совместимости с 4 пирами

# Для изменений proto:
make proto-gen    # Перегенерация Go-кода
make proto-lint   # Линтинг proto-определений
```

### Связанные документы

- [01-architecture.md](./01-architecture.md) -- Архитектура и структура пакетов
- [05-interop.md](./05-interop.md) -- Тестирование совместимости
- [AGENTS.md](../../AGENTS.md) -- Полные конвенции кода и команды

---

*Последнее обновление: 2026-08-27*
