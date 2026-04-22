# Разработка

![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![golangci--lint](https://img.shields.io/badge/golangci--lint-v2-1a73e8?style=for-the-badge)
![buf](https://img.shields.io/badge/buf-Protobuf-4353FF?style=for-the-badge)
![Podman](https://img.shields.io/badge/Podman-Dev_Container-892CA0?style=for-the-badge&logo=podman)
![synctest](https://img.shields.io/badge/synctest-Virtual_Time-34a853?style=for-the-badge)

> Рабочий процесс разработки, Make-цели, стратегия тестирования, линтинг, генерация protobuf и руководство по вкладу.

---

### Содержание

- [Предварительные требования](#предварительные-требования)
- [Настройка разработки](#настройка-разработки)
- [Make-цели](#make-цели)
- [Стратегия тестирования](#стратегия-тестирования)
- [Линтинг](#линтинг)
- [Рабочий процесс Protobuf](#рабочий-процесс-protobuf)
- [Возможности Go 1.26](#возможности-go-126)
- [Конвенции кода](#конвенции-кода)
- [Вклад в проект](#вклад-в-проект)

### Предварительные требования

- **Podman** + **Podman Compose** (все команды выполняются в контейнерах)
- **Git** для управления версиями
- Go 1.26 (нужен только для поддержки IDE; сборка в контейнерах)

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

### Make-цели

Все Go-команды выполняются внутри контейнеров Podman через `podman-compose exec`.

#### Жизненный цикл

| Цель | Описание |
|---|---|
| `make up` | Запуск контейнера разработки |
| `make down` | Остановка контейнера |
| `make restart` | Перезапуск (down + up) |
| `make logs` | Просмотр логов контейнера |
| `make shell` | Открыть bash в контейнере |

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
| `make interop-clab` | Полный цикл вендорных NOS-тестов (Nokia, FRR и др.) |
| `make interop-clab-up` | Деплой вендорной NOS-топологии |
| `make interop-clab-test` | Запуск вендорных interop Go-тестов |
| `make interop-clab-down` | Уничтожение вендорных NOS-контейнеров |

#### Примеры интеграций

| Цель | Описание |
|---|---|
| `make int-bgp-failover` | Демо BGP fast failover (GoBFD + GoBGP + FRR) |
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
- **`goleak.VerifyTestMain(m)`** в каждом пакете для обнаружения утечек горутин

#### Тесты FSM (`testing/synctest`)

Go 1.26 `testing/synctest` обеспечивает детерминированное тестирование на основе виртуального времени:

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
        time.Sleep(350 * time.Millisecond) // виртуальное время
        synctest.Wait()
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

golangci-lint v2 со строгой curated-конфигурацией:

```bash
make lint
```

Конфигурация в `.golangci.yml`. Ключевые линтеры:
- `gosec` (с `audit: true`) -- анализ безопасности
- `govet`, `staticcheck`, `errcheck` -- стандартные проверки Go
- `noctx` -- проверки передачи контекста
- `exhaustive` -- исчерпывающие switch/map проверки
- `depguard`, `gomoddirectives` -- гигиена зависимостей
- `nolintlint` -- качество директив `//nolint`

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

### Рабочий процесс Protobuf

Protobuf управляется `buf`:

```bash
# После изменения api/bfd/v1/bfd.proto:
make proto-lint      # Линтинг определений
make proto-gen       # Генерация Go-кода в pkg/bfdpb/
make proto-breaking  # Проверка на несовместимые изменения vs master
```

> **НИКОГДА** не редактируйте файлы в `pkg/bfdpb/` вручную -- они генерируются через `buf generate`.

### Возможности Go 1.26

GoBFD использует возможности Go 1.26 для безопасности, производительности и отладки:

#### `testing/synctest` -- Детерминированные тесты таймеров

Все тесты BFD-таймеров и таймаутов обнаружения используют `testing/synctest` для выполнения с виртуальным временем. Тесты выполняются мгновенно (без реальных sleep) и полностью детерминированы. См. [Тесты FSM](#тесты-fsm-testingsynctest) выше.

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

#### `GOEXPERIMENT=goroutineleakprofile`

Dev-контейнер (`Containerfile.dev`) включает эксперимент профилирования утечек горутин. В сочетании с `goleak.VerifyTestMain(m)` в тестовых пакетах это обеспечивает обнаружение утечек горутин в runtime во время разработки.

#### `runtime/trace.FlightRecorder`

HTTP-endpoint предоставляет flight recorder для посмертного захвата трассировки. Демон непрерывно записывает последние N секунд данных трассировки, которые можно получить по запросу для отладки всплесков задержки или дедлоков.

#### Swiss Tables

Go 1.26 использует Swiss tables как реализацию `map` по умолчанию. Поиск дискриминаторов, таблица переходов FSM и демультиплексирование сессий GoBFD выигрывают от улучшенной локальности кэша. См. [BENCHMARKS.md](../../BENCHMARKS.md) для сравнения с `GOEXPERIMENT=noswissmap`.

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
2. Следуйте стилю кода (см. `CLAUDE.md` для конвенций)
3. Добавляйте тесты для новой функциональности (`go test ./... -race -count=1`)
4. Убедитесь, что `golangci-lint run ./...` проходит
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
- [CLAUDE.md](../../CLAUDE.md) -- Полные конвенции кода и команды

---

*Последнее обновление: 2026-02-24*
