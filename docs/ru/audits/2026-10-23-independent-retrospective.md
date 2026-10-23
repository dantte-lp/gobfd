**Итог:** выполнено; независимая сверка выявила шесть дефектов, полное соответствие контракту v1.0.0 не подтверждено.
**От вас:** не требуется

## Сделано

Период: 2026-02-20 — 2026-10-23. Проверенная реализация: `dev`,
`f9ed31bdd81425ba4efaf9688bb452ddea142266`.
Техническое задание: `docs/superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md`
с октябрьскими поправками. Прочитаны четыре ADR и их русские переводы.
Источники требований отделены от исторических планов и датированных roadmaps.
Исполнимые задачи, зависимости и критерии остаются исключительно в Beads.

Реестр охватывает 543 отслеживаемых Go-файла: 212 файлов без тестов и
генерированного кода, 329 тестовых, два генерированных. Все 541 рукописных
файла переданы анализатору дублей. Независимые проверки разделены на протокол,
управление/владение и ADR/поставку. Семантическая проверка углублена на границах
авторизации, reconciliation, sender ownership, таймеров и внешних actuator.
Каждое тело теста и каждая ветвь поставочного парсера отдельно не проверены.
Карты покрытия и исключений сохранены в локальных записях
`reports/independent-retro-20261023/{protocol,reconciliation,adr-delivery}.txt`.

Ретроспектива развития:

| Период | План и фактический результат | Проверяемая граница |
|---|---|---|
| 20–24 февраля | Основа: codec/FSM, sessions/timers, auth/netio, API/CLI и интеграции | Начальный план и история не доказывают production acceptance |
| 19 августа | Введён отдельный production contract v1 | Stable/preview, ownership, security и измеряемая нагрузка стали самостоятельными требованиями |
| Август–сентябрь | Канонические ключи, claims/leases, source reconciliation, точные overlay identities, Poll/Final | Реализованные компоненты отделены от wire-квалификации |
| 15–19 октября | Unix/mTLS, principal claims, защищённый debug, revisioned Watch, восстановление companions и GoBGP | Нормальные сценарии покрыты; новый аудит выявил непройденные trust-boundary случаи |
| 21–22 октября | Micro-BFD transactions, API listener ownership, systemd/lifecycle, timer wire fixes, preview opt-in | Это реализации и локальные receipts; они не закрывают весь v1 |
| 22–23 октября | Commit-based RX timing, socket drops, workload/environment tooling; Oracle Linux исключён из v1 | Незавершённая UDP-ветка и reference workload остаются отдельными от `dev` |

Начальный коммит: `5e8ffdc85adf63bf0d41c243bff4a8c53c522e2a`.
Коммит production contract: `88b07ee0e1e272fe6017a9ce0125c2bf8b0c8713`.
Последний опубликованный stable, проверенный через GitHub API: `v0.6.4`,
2026-08-27. Проверены 96 PR-записей: 39 merged, 11 open. Это история поставки,
а не результаты свежих Actions; удалённые изменения не выполнялись.

| Сравнение деревьев | Коммиты после базы | Файлы | Добавлено строк | Удалено строк |
|---|---:|---:|---:|---:|
| Начальный коммит → проверенный `dev` | 757, из них 726 без merge | 806 | 235207 | 2987 |
| Родитель первого production contract → проверенный `dev` | 623 | 705 | 132206 | 16743 |

Это разность конечных деревьев, а не суммарная работа или личная статистика.
В первом сравнении runtime/API дают +32650/−17 строк; тесты, инструменты и CI —
+126906/−0; документация — +60645/−2968; generated — +3522/−0.
Остальное относится к конфигурации и поставочным файлам. Рост связан прежде
всего с проверяемыми контрактами, тестовой инфраструктурой и документацией;
количество строк само по себе не подтверждает качество.

Сопоставление требований и реализации:

| Область | Подтверждено исходниками и выбранными проверками | Остаток |
|---|---|---|
| RFC core | Одна FSM; взаимоисключение Poll/Final; bounded timer queue; monotonic commit-based detection | Полный RFC/wire/load gate; source-port/QoS и allocation boundaries уже имеют задачи |
| Base ownership | Typed owners, canonical keys, lazy sender leases, source-scoped release, детальные receipts | Same-key mutation policy и взаимодействие старого drain с новым wire epoch |
| Идемпотентность | Повтор Micro reconcile, shared base claims и concurrent Close проходят выбранные тесты | Echo Add создаёт дубликат; Echo Delete не изолирует principal |
| Management | Unix UID, verified mTLS, роли, loopback metrics и защищённый debug существуют | TLS principal namespace collision и пропущенный startup-only unsafe flag |
| GoBGP/companions | Desired/applied reconciler, retry/gap recovery, operator-disable protection и Watch recovery существуют | Final compatibility/security/interop; dynamic native-BFD conflict после startup остаётся известным ограничением |
| ADR-0001 | rtnetlink, link flags, ENOBUFS/MSG_TRUNC resync, PathDown consumer | Свежая host/netns/wire-квалификация этим аудитом не выполнена |
| ADR-0002 | Native OVSDB default, явный CLI fallback, общая проверка interface names | Отдельный NM backend нарушает проверку owner и ограничение context |
| ADR-0003 | Advanced modes обозначены preview/experimental; reserved backends fail closed | Это не полная RFC7130/8971/9521-квалификация; Echo admission уже имеет открытый P0 |
| ADR-0004 | `internal/overlay` — общий leaf-пакет vocabulary/normalization для config и netio | Семантического расхождения ADR в этом пути не найдено |
| Поставка | Go-owned tools, отдельный tools module, trixie OCI, сохранённый generic RPM | Новые generated-файлы не замечаются proto drift guard; финальные OCI/security/Kubernetes gates открыты |

Проверка Go 1.27:

- Оба модуля задают `go 1.27` и `toolchain go1.27.3`; проверенные compiler
  pins согласованы. Диагностика выполнена именно Go 1.27.3 в pinned container.
- Используются `synctest`, `slices`/`maps`, `os.Root`, flight recorder и общий
  `MaxHeaderValueCount` с parser-тестами. Переход на generic methods или
  JSON v2 сам по себе не является требованием качества.
- Удалённые experiment/debug switches и `-vet=off` в проверенных operational
  surfaces не найдены. Default `stdversion` vet не отключался.
- Ручные источники не импортируют `unsafe`; generated protobuf импортирует
  его для descriptor metadata. Буквальное «unsafe отсутствует во всей базе»
  неверно; область запрета требует уточнения без ручной правки generated code.
- Текущий lint owner содержит 18 tagged profiles и base, итого 19 запусков.
  Полная матрица в этом аудите не запускалась.

Первичные источники Go:
[release notes](https://go.dev/doc/go1.27),
[точный compiler tag](https://github.com/golang/go/tree/go1.27.3).
Context7 использован для Go, golangci-lint и godbus; спорное поведение godbus
дополнительно проверено по исходнику выбранной версии.

Дубли и переиспользование:

- Standalone `dupl`, выбранный через `tools/go.mod`, порог 100, без
  repository exclusions: 12 групп на 212 non-test файлах; 42 группы на
  всех 541 non-generated файлах. Parser errors отсутствуют. Это синтаксические
  совпадения, не 42 доказанных нарушения архитектуры и не результат canonical lint.
- Часть совпадений оправдана: IPv4/IPv6 socket options, разные typed overlay
  constructors с уже общей `openScopedOverlayUDP`, CLI delegates.
  Совпадение блоков sentinel errors и regexp declarations не является общей
  бизнес-логикой.
- Конкретные кандидаты на повторное использование: `repositoryRoot` в
  `test/cmd/e2ectl/main.go` и `test/cmd/integrationctl/main.go`; последовательность
  write/sync/stat/close в `benchreport.finishTemporary` и
  `routingartifacts.writePreparedOutput`. Они отнесены к существующему P2
  `gobfd-qj0.8.2.8.6.1`, без приоритета перед выпуском.
- Base и Micro уже используют typed owner/lease инфраструктуру. Echo сохраняет
  отдельный compatibility lifecycle и не получает тех же гарантий. Это
  функциональный пробел переиспользования, подтверждённый diagnostic RED.

План до аудита: 416 потомков v1, 293 закрыты; 78 незавершённых листовых задач.
После регистрации findings: 423 потомка, 293 закрыты, 130 не закрыты;
322 листовые задачи, 84 не закрыты: 8 P0, 51 P1, 24 P2 и 1 P3.
Подсчёт выполнен по действительным `parent`-связям, а не префиксам ID.
Это количество задач, а не процент готовности или оценка срока. P2/P3 и
общие inventories не автоматически становятся обязательствами до v1.

Release gate `gobfd-qj0.8.2.6` теперь явно зависит от шести новых defect tasks.
Первый приоритет — устранение подмены transport principal. Остальные текущие
блокеры не отменены: preview fail-closed, typed mutation policy, завершение
UDP workload/clock accounting, A/B thread pinning, reference/engineering load,
финальные RFC/interop/security/OCI и независимое принятие release candidate.
Поддерживаемый envelope по-прежнему требует 1000 mixed IPv4/IPv6 sessions
при 100 ms/multiplier 3 в течение 24 часов на reference 8 vCPU/8 GiB;
5000 sessions/300 ms/30 min остаются отдельным engineering gate.

## Успешно

Локальные evidence сохранены под `reports/independent-retro-20261023/`.

| Проверка | Результат | Доказательство |
|---|---|---|
| Независимая сверка ADR | 4 EN/RU пары, code/test mapping | `adr-delivery.txt` |
| Позитивные critical regressions с race | 9 top-level, 19 с под-тестами; 0 FAIL | `critical-baseline01.log` |
| Runtime/CLI vet | exit 0, без diagnostic errors | `vet01.log`, `critical-run01.log` |
| Targeted benchmarks | 11 строк, везде 0 B/op и 0 allocs/op | `benchmarks01.log` |
| Duplication scan | 541 вход; 42 raw groups, stderr без parser errors | `dupl-all-inputs.txt`, `dupl-all.txt` |
| Проверка proto guard | exit 0 при наличии untracked generated fixture | `proto-guard-result.txt` |
| Ограничение ресурсов | 2 CPU/2 GiB/swap 0; пики 1270317056 и 941002752 B; OOM/max events 0 | `critical-run01.log`, `critical-run02.log` |

Benchmarks измеряют отдельные функции на linux/amd64, с GOMAXPROCS=2 и коротким
окном; это не end-to-end UDP latency, не authentication allocation gate и не
доказательство поддерживаемого масштаба.

## Неуспешно

| № | Требование и нарушение | Доказательство | Статус | Дальше |
|---|---|---|---|---|
| Н1 | P0: TLS identity принимается за привилегированный Unix UID | `internal/server/principal.go:72`, `critical-red02.log` | задача | `gobfd-qj0.8.2.3.5` |
| Н2 | P1: startup contract пропускает отзыв anonymous mutation policy | `cmd/gobfd/startup_contract.go:185`, diagnostic RED | задача | `gobfd-qj0.8.2.3.6` |
| Н3 | P1: Echo допускает cross-owner Delete и повторную wire identity на одинаковом Add | `internal/server/echo.go:115`, два diagnostic RED | задача | `gobfd-qj0.8.2.2.8` |
| Н4 | P1: NetworkManager использует active/cached профиль другого bond | `internal/netio/lag_networkmanager.go:142`, три diagnostic RED | задача | `gobfd-qj0.8.2.2.9` |
| Н5 | P1: NM property reads обходят actuator context/deadline | `internal/netio/lag_networkmanager.go:310`, upstream v5.2.2 source | задача | `gobfd-qj0.8.2.2.10` |
| Н6 | P1: proto guard пропускает новые untracked generated-файлы | `test/internal/cirunner/proto.go:68`, isolated Git fixture | задача | `gobfd-qj0.8.2.8.7.3.3.1` |
| Н7 | Не доказана безопасность same-key replacement при незавершённом старом drain | `internal/bfd/manager_claim_drain_test.go:215`, source-only observation | задача | `gobfd-qj0.8.2.1.3.12` |
| Н8 | Timer addendum противоречит принятому immediate non-Up поведению | Timer addendum:53–55, `internal/bfd/session_update.go:192` | задача | `gobfd-qj0.8.2.8.7.2` |
| Н9 | Reference envelope и окончательная release qualification не завершены | ТЗ:443–480, действующие открытые Beads | задача | `gobfd-qj0.8.2.6` |

Н1 требует сертификата, выданного доверенной CA; случайный недоверенный
сертификат TLS не принимается. Diagnostic использует verified-chain fixture,
а не новую проверку handshake/PKI. Для Н2 исполняемо воспроизведён пропуск
startup-field; последствия reload подтверждаются путём captured policy.
Н3–Н4 воспроизведены через существующие Manager/fake sender/DBus seams,
без изменения рабочих сетевых устройств. Для Н5 подтверждён исходный вызов
через `context.Background`, зависание live D-Bus не воспроизводилось.
Для Н6 проверена точная Git guard semantics, а не фактический запуск Buf.
Н7 — проверка контракта и известного разрешённого overlap, не измеренный flap.

Исправления runtime в рамках аудита не применялись. Диагностические тесты
предназначены для воспроизведения дефектов, а не для постоянного принятия
неверного поведения. Полные входы и дальнейшие критерии сохранены в Beads.

## Ошибки и предупреждения

| № | Источник | Суть | Статус | Дальше |
|---|---|---|---|---|
| О1 | `pkg/bfdpb/bfd/v1/bfd.pb.go:17` | Generated protobuf использует unsafe; абсолютный запрет не описывает фактическую границу | задача | Уточнить контракт в `gobfd-qj0.8.2.8.7.2` |
| О2 | Полная квалификация | Не запускались full race suite, все 19 lint profiles, live interop, vulnerability/Buf breaking и 24h load | принято | Аудит не заменяет `gobfd-qj0.8.2.6` и `gobfd-qj0.8.2.7` |
| О3 | Незавершённые рабочие деревья | UDP, Echo port и bond worktrees не включены в принятую реализацию `dev` | принято | Их исходники и receipts сохранены отдельно; квалификация по собственным задачам |

Не проверены полностью runtime kernels, все target architectures и внешние
dataplane integrations. Готовность v1.0.0 и отсутствие всех возможных дефектов
не подтверждены.
