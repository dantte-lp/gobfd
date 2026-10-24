# Реестр рисков зависимостей

Документ отслеживает внешние зависимости с нетривиальным риском из-за статуса сопровождения, проектов с единственным мейнтейнером или изменений в upstream. Каждая запись включает оценку риска и стратегию митигации.

Pins берутся из `go.mod`; advisory acceptance — из `scripts/vuln-audit.go`
и задачи владельца в Beads. Реестр не заменяет свежие scans финальных runtime/
tool graphs и не устанавливает текущее число upstream maintainers.

## reeflective/console — Средний риск

| Поле | Значение |
|------|----------|
| **Модуль** | `github.com/reeflective/console v0.5.0` |
| **Используется в** | `cmd/gobfdctl/commands/shell.go` — интерактивная REPL-оболочка |
| **Уровень риска** | Средний |
| **Причина** | API до v1.0 и зависимость от внешнего сопровождения |

### Описание

Библиотека `reeflective/console` обеспечивает интерактивную оболочку для `gobfdctl`. Она оборачивает команды Cobra в readline-based REPL с табуляционным дополнением, историей и настройкой приглашения.

### Факторы риска

- **Pre-1.0 API**: Библиотека не достигла v1.0, что означает возможные ломающие изменения между минорными версиями.
- **Внешнее сопровождение**: Upstream staffing и release activity требуют свежей проверки до upgrade.
- **Транзитивные зависимости**: Тянет модули readline, completions и terminal UI.
- **Нет альтернативы в stdlib**: Стандартная библиотека Go не включает Cobra-совместимый REPL-фреймворк.

### Митигация

1. **Фиксация версии**: Зависимость закреплена на `v0.5.0` в `go.mod`. Перед
   принятием проверен диапазон release notes от `v0.1.25` до `v0.5.0`, CLI- и
   race-тесты.
2. **Вендоринг**: Если проект станет заброшенным, вендорить исходный код в `internal/console/`.
3. **Runtime scope**: Неинтерактивные команды не запускают REPL. CLI binary
   по-прежнему зависит от модуля при сборке; это не optional build dependency.
4. **Ограниченная область**: Product integration находится в `shell.go`;
   daemon и protocol implementation не импортируют console package.

---

## osrg/gobgp — Высокий риск

| Поле | Значение |
|------|----------|
| **Модуль в `dev`** | `github.com/osrg/gobgp/v4 v4.10.0`; импортируется только `github.com/osrg/gobgp/v4/api` |
| **Модуль в `release/v0.6`** | `github.com/osrg/gobgp/v3 v3.37.0` |
| **Используется в** | `internal/gobgp/` — опциональная интеграция GoBGP |
| **Известный advisory** | `GO-2026-4736` / `CVE-2026-30405` / `GHSA-4p9m-8gc4-rw2h` |
| **Уровень риска** | Высокий |
| **Владелец allowlist** | `@dantte-lp` |
| **Дата expiry (пересмотра)** | `2026-12-31` |

### Описание

GoBGP затронут denial-of-service advisory в обработке BGP path attribute
NEXT_HOP. GHSA-4p9m-8gc4-rw2h указывает v4.4.0 как исправленную версию
(upstream commit `583080a`). Go vulnerability database не указывает
исправленную версию GoBGP v4 и ограничивает advisory символом
`fsmHandler.recvMessageloop` в `github.com/osrg/gobgp/v4/pkg/server`, который
GoBFD не импортирует. В `dev` строгий `govulncheck@v1.8.0 ./...` сообщает
advisory только на уровне модуля, без импортированного пакета и достижимого
символа; `osv-scanner` на уровне модуля также сообщает advisory.

GoBGP v3 обслуживает `/apipb.GobgpApi`, v4 — `/api.GoBgpService`; они
несовместимы по wire. GoBFD в `dev` требует демон GoBGP v4. Для GoBGP v3.37.0
в `release/v0.6` исправленной версии нет.

### Митигация

1. **Ограниченная экспозиция**: путь GoBGP является опциональным и должен
   подключаться только к GoBGP gRPC endpoint на localhost или в доверенной
   management-сети.
2. **Контролируемый CI allowlist в `dev` и stable v1**: по решениям
   maintainer от 2026-10-13 и 2026-10-21 (stable v1, `gobfd-qj0.8.4.1`) запись
   `GO-2026-4736` имеет режим `ModuleOnly` для точного модуля
   `github.com/osrg/gobgp/v4` и содержит owner `@dantte-lp`, дату expiry
   (пересмотра) 2026-12-31, reason и mitigation. Stable v1 дополнительно
   требует, чтобы GHSA/OSV указывали исправленную версию не выше закреплённой,
   а govulncheck финального candidate сообщал advisory только на уровне модуля.
   Reachable symbol, импортированный затронутый пакет, package mismatch,
   неизвестный scanner, дополнительный advisory, expired entry или stale entry
   без соответствующего finding ломает CI.
3. **Принятие риска в `release/v0.6`**: по решению maintainer от 2026-10-13
   линия v0.6 принимает риск GoBGP v3.37.0 до 2026-12-31 как границу
   поддержки при условии, что GoBGP API доступен только на localhost или в
   доверенной management-сети. Поставка в stable-ветку отслеживается в Beads
   `gobfd-qj0.8.1.16.4`.
4. **Триггер удаления**: удалить allowlist-запись, когда Go vulnerability
   database укажет исправленную версию GoBGP v4 (golang/vulndb#5035, #6562).
   После этого audit падает на записи, так как она не соответствует ни одному
   finding сканеров.

---

## x/crypto/openpgp — Advisory только на уровне модуля

| Поле | Значение |
|------|----------|
| **Модуль** | `golang.org/x/crypto v0.58.0` (непрямая зависимость) |
| **Затронутый пакет** | `golang.org/x/crypto/openpgp` (отсутствует в build graph) |
| **Известный advisory** | `GO-2026-5932` |
| **Режим allowlist** | Только inventory findings точного модуля |
| **Дата expiry (пересмотра)** | `2026-12-31` |

### Митигация

1. **Нет затронутого import**: вывод `go list -deps ./...` не должен содержать
   `golang.org/x/crypto/openpgp`.
2. **Fail-closed reachability**: аудит принимает только недостижимые findings
   точного модуля `golang.org/x/crypto` от `govulncheck` или `osv-scanner`.
   Reachable symbol, затронутый subpackage, package mismatch или неизвестный
   scanner ломает CI даже при совпадении advisory ID.
3. **Триггер удаления**: удалить исключение, когда module inventory finding
   исчезнет; никогда не добавлять import `openpgp`.

---

## Переход пути модуля YAML v3 — Низкий риск

| Поле | Значение |
|------|----------|
| **First-party модуль** | `go.yaml.in/yaml/v3 v3.0.5` (прямая зависимость) |
| **Устаревший модуль** | `gopkg.in/yaml.v3 v3.0.1` (только транзитивно) |
| **Используется** | CLI output, HAProxy config, integration tests и транзитивный console-код |
| **Уровень риска** | Низкий |

### Описание

Модуль `gopkg.in/yaml.v3` был архивирован мейнтейнером в апреле 2025 года. Все
импорты GoBFD переведены на поддерживаемый API-совместимый наследник
`go.yaml.in/yaml/v3`. Старый путь остаётся только транзитивно через
`reeflective/console`/Carapace.

### Факторы риска

- **Архивированный репозиторий**: Дальнейшие исправления ошибок и патчи безопасности для пути `gopkg.in` выпускаться не будут.
- **Транзитивная миграция в ожидании**: legacy-модуль нельзя убрать из полного
  MVS-графа, пока console/Carapace не переведут свои внутренние импорты.

### Митигация

1. **First-party миграция завершена**: код GoBFD импортирует только
   `go.yaml.in/yaml/v3`; `go mod why` показывает console/Carapace как
   единственный оставшийся путь к архивному модулю.
2. **Мониторинг upstream**: удалить транзитивный legacy-модуль после миграции
   console/Carapace, когда это разрешит `go mod tidy`.
3. **Regression gate**: новые first-party импорты `gopkg.in/yaml.v3`
   запрещены.
4. **Нет известного advisory**: для архивного `v3.0.1` нет известной
   уязвимости; поддерживаемый наследник закреплён на `v3.0.5`.
