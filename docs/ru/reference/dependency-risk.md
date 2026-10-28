# Риски зависимостей и исключения

Выбранные версии заданы в [go.mod](../../../go.mod), [tools/go.mod](../../../tools/go.mod) и отдельном [Smallstep profile](../../../test/stepca/README.md). Каноническая runtime/tools реализация исключений — [vuln-audit.go](../../../scripts/vuln-audit.go). Реестр фиксирует правила исходного кода; текущая advisory database и final выбранный граф требуют свежего scan.

## Владение graphs

| Graph | Назначение | Граница приёмки |
|:---|:---|:---|
| Runtime | Daemon, CLI, bridges и test helpers | Runtime package/symbol reachability |
| Tools | Изолированные development/scanning/generation tools | Отдельный selected tool graph |
| Smallstep qualification | Полный внешний test provider | Независимые build/scan/link/записи выполнения |

Inventory success не определяет security. Module finding, imported affected package и reachable symbol — разные результаты.

## Управляемые исключения

| Advisory | Точный module | Owner / expiry | Допустимая область |
|:---|:---|:---|:---|
| GO-2026-4736 | github.com/osrg/gobgp/v4 | @dantte-lp; 2026-12-31 | Module-only, точное совпадение scanner/package |
| GO-2026-5932 | golang.org/x/crypto | maintainers; 2026-12-31 | Module-only inventory; без affected openpgp import |

Reachable findings, affected imported packages, module mismatch, unknown scanner, additional advisory, expiry и exception без corresponding finding завершают audit ошибкой. Не расширяйте exception ради зелёного scan.

### GoBGP

Dev pin — github.com/osrg/gobgp/v4 v4.10.0; рукописная integration импортирует API package. Recorded exception относится к GO-2026-4736/CVE-2026-30405/GHSA-4p9m-8gc4-rw2h и upstream server path. Source rationale фиксирует patched v4.4.0 и отсутствие fixed-v4 data в Go database; это не live query базы.

Stable-v1 acceptance требует fixed version в GHSA/OSV не выше pin и module-only scope в final-candidate govulncheck. Держите GoBGP на loopback/trusted management network. Удалите exception после исчезновения matching inventory finding.

Линия v0.6 сохраняет GoBGP v3.37.0 и отдельно принятый support boundary до 2026-12-31. Проверьте delivery на поддерживаемой ветке отдельно. Dev acceptance не переносится на эту линию. RPC service contracts v3/v4 отличаются; используйте соответствующий daemon.

### OpenPGP

Selected indirect crypto module — v0.58.0. Exception допускает только exact-module inventory findings GO-2026-5932. Подтвердите отсутствие golang.org/x/crypto/openpgp в final build graph. Любой affected import или reachable symbol отменяет disposition.

## Другие сохранённые зависимости

| Dependency | Использование | Требуемая проверка |
|:---|:---|:---|
| reeflective/console v0.5.0 | Интерактивный shell gobfdctl | CLI/race compatibility; fresh upstream review до upgrade |
| go.yaml.in/yaml/v3 v3.0.5 | Собственный YAML | Совместимость decoding/output |
| gopkg.in/yaml.v3 v3.0.1 | Transitive legacy graph | Точная go mod why chain; без новых собственный imports |
| golang.org/x/net v0.62.0 | Transport и выбранные графы | Свежий security scan и IPv4 transport regression |

Console остаётся build dependency CLI, даже если noninteractive command не запускает REPL. Integration point — [shell.go](../../../cmd/gobfdctl/commands/shell.go); реестр не устанавливает staffing/maintenance upstream.

YAML migration фиксирует историческое решение module path. Текущие archival/release status из него не следуют. Удаляйте transitive path только при допустимости final graph; не добавляйте другую реализацию заранее.

Сохранённое обновление x/net устраняет [GO-2026-6860](https://vuln.go.dev/ID/GO-2026-6860) и [GO-2026-6866](https://vuln.go.dev/ID/GO-2026-6866), без allowlist entry. Отдельный Smallstep profile также выбирает v0.62.0. Pin не доказывает неизменности текущей scanner data.

## Проверка кандидата

1. Сохраните точные runtime/tools/provider идентификаторы графов.
2. Выполните make dependency-inventory-check и controlled make vulncheck в закреплённой среде.
3. Проверьте raw scanner results, reachability и точные exception matches.
4. Выполните затронутые transport/CLI/API regressions и provider qualification.
5. Сохраните scanner version/data time, SHA исходников, findings и disposition. Отделите поддерживаемой линии и приёмка выпуска.

[Разработка](../09-development.md), [automation](automation.md), [security policy](../../../SECURITY.md), [supply-chain inventory](../../supply-chain/dependency-inventory.json).
