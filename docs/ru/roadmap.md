# Дорожная карта GoBFD

## Текущая очередь — 2026-10-20

Статусы и зависимости находятся в Beads. Этот снимок заменяет активную очередь
и инструкции окружения от 2026-10-15; датированные протоколы ниже подтверждают
только указанные в них ревизии.

- Принятый implementation baseline локального `dev`:
  `4e582b75dd5e6f0b8d994a66e8c9f3585bee82fa`.
  GitHub `dev` остаётся на `e6baa6fa416877a3caefb3e68421309578e749b0`;
  этот baseline опережает его на 59 commit. Локальная приёмка не подтверждает
  remote integration или stable delivery.
- API/core-задачи `gobfd-qj0.8.2.1.3.4`, `gobfd-qj0.8.2.1.3.5` и
  `gobfd-qj0.8.2.1.1.3` закрыты после итогового общего gate ветки
  `task/api-validation-padding`, принятой в локальный `dev` на
  `20e951ac14e0515eda6698df7c70e7bf9ee77a32`.
  Локальный receipt: `reports/worktree-archive-20261020/api-validation-padding/reports/api-validation-padding-20261020/acceptance.md`;
  `canonical-2560m02` содержит 25 ожидаемых top-level тестов, 142 PASS rows в
  четырёх пакетах с race detector, все 18 lint profiles/92 linters и command,
  container, host exit 0. Native/pinned formatter diffs пусты; vet и оба module
  tidy checks завершились с 0, Markdown охватил 83 файла, spelling — пять
  изменённых English inputs. Там сохранены readbacks 730 исходных файлов и
  harness, точный cleanup. Full suite, новые peer/path-MTU, performance, soak
  и stable qualification остаются отдельными проверками.
- Остановка unsolicited TX `gobfd-qj0.8.2.1.10.6`, родитель `.1.10`,
  диагностика/аутентификация `.1.2`, AdminDown drain `.1.3.3`, учёт доставки
  `.1.3.1`, Echo Watch `.1.3.10` и восстановление после raw loss `.1.3.11`
  приняты локально. Полная RFC-квалификация, выпуск preview как stable и
  релизные проверки остаются отдельными задачами.
- Delivery parent `gobfd-qj0.8.2.1.3` закрыт в границах локального контракта
  `dev` admission/commit/drain/status после аудита приёмки одиннадцати закрытых
  children. Typed queue admission не означает синхронный receive commit;
  legacy events остаются best effort, telemetry вне согласованного causal
  tuple — sampled. Отдельные владельцы interface, Micro-BFD, transport,
  performance, combined consumers, RFC и stable остаются открытыми.
- Actions-задача `gobfd-qj0.8.6.4.2` принята на implementation baseline:
  четыре Actions, десять uses, существующая fixture, оба changelog и
  Go-generated dependency inventory; восемь файлов, +20/-12 строк. Actual cold
  gate: 33 critical top-level race tests, все 18 lint profiles/92 linters,
  source/cleanup readbacks прошли. Parent `.8.6.4` сохраняет hosted execution
  и nested OCI qualification.
- Cleanup-задача `gobfd-qj0.8.2.8.5.3.4` закрыта: удалены 33 clean working
  copies и три точных старых образа, ветки сохранены, 18 архивов/2557 файлов
  проверены по hash. Receipts перенесены в `reports/worktree-archive-20261020/`;
  абсолютные пути внутри исходных receipts остаются историческими. Сохранены
  два dirty drafts, два nonancestor-варианта и два дерева с container binds.
  Подробности: `reports/space-merge-20261020/final-receipt.md`.

| Порядок | Владелец | Требуемый результат |
|---|---|---|
| 1 | `.8.6.3.3` | Квалифицировать допустимые scanner/release CLI/benchstat updates после принятых Actions; включить generated dependency inventory |
| 2 | `.8.6.6.2`, `.8.6.2.1` | Квалифицировать исправленный BIRD baseline; godbus отдельно допустим по возрасту с 2026-10-20 16:01:38 UTC, свежая проверка source/advisories ещё требуется |
| 3 | `.1.1.1.4.3.1`, `.1.17` | Скомпилировать и квалифицировать timer draft на принятом исправленном пире; отдельно выполнить actual API Echo wrong-port RED до production fix |
| 4 | `.1.1`, `.1.4`, `.1.6`, `.2`, `.3` | Закрыть protocol, transport, measurement, ownership, readiness и systemd acceptance; сохранить preview boundaries |
| 5 | `.8.4`, `.8.2.6`, `.8.2.7`, `.8.2.8` | Закрыть stable advisory, combined consumers, независимое ревью, quality, packaging и scale qualification до подготовки релиза |

Timer draft прошёл source review, но не скомпилирован и не выполнен. Echo draft
содержит только будущий regression test; ожидаемый RED не запускался. Это
незавершённая реализация. Большая задача разработки остаётся приостановленной;
ретро обновляет её план без возобновления runtime implementation. Числовые
приоритеты и существующие blocking edges сохранены.

Числовые приоритеты сохраняются: актуальность стека — P0, обычные RFC-задачи —
P1, классификация API `.1.3.5` — P2, stable advisory disposition `.8.4` — P1.
Сохраняется намеренная зависимость итоговой приёмки security `.8.2.3` P0 от
ownership `.8.2.2` P1. Порядок выполнения не создаёт доказательства reachable
security finding и не отменяет релизную проверку. Закрытая core-задача `.1.1.3`
использует принятый API mapping из `.1.3.5`; padding имеет отдельные границы.

GitHub API на 2026-10-20 показывает одну открытую issue
[#88](https://github.com/dantte-lp/gobfd/issues/88) и десять открытых PR;
их disposition отделён от локальной реализации. GitLab API подтверждает
метаданные upstream-тега BIRD; это не источник приёмки GoBFD mirror или
pipeline. Эта сверка не разрешает remote writes.

### Очередь release notes — проверено 2026-10-20

Это цели планирования, а не принятые pins или результаты совместимости.
Порог UTC равен времени источника плюс семь полных суток. Проверяется весь
диапазон от установленной версии до целевой, включая промежуточные релизы.
Предыдущий аудит охватывал 67 uses из 17 Action repositories. Четыре допустимых
обновления Actions уже приняты локально в P0 `gobfd-qj0.8.6.4.2`; строки ниже
сохраняют историю installed-to-target. Parent `.8.6.4` сохраняет hosted smoke
и nested OCI provenance. CodeQL остаётся на hold; ретро не перепроверяет все
upstream version channels.

| Компонент | Установлен → цель | Самое раннее принятие UTC | Источник и следующая проверка |
|---|---|---|---|
| godbus | 5.2.2 → 5.2.4 | 2026-10-20 16:01:38 | [5.2.3](https://github.com/godbus/dbus/releases/tag/v5.2.3): allocation/error hardening; [5.2.4](https://github.com/godbus/dbus/releases/tag/v5.2.4): system-bus lookup errors; NetworkManager missing-bus/cancellation tests; `gobfd-qj0.8.6.2.1` |
| ConnectRPC | 2.0.0 → 2.0.1 | 2026-10-21 13:57:00 | [Notes](https://github.com/connectrpc/connect-go/releases/tag/v2.0.1): draining отклонённых сообщений, early responses, truncated/error envelopes; RPC/Watch/generator parity; `gobfd-qj0.8.6.10` |
| ExaBGP | 5.0.13 → 5.0.15 | 2026-10-24 06:08:19 | [5.0.14](https://github.com/Exa-Networks/exabgp/releases/tag/5.0.14): UPDATE/withdraw/helper; [5.0.15](https://github.com/Exa-Networks/exabgp/releases/tag/5.0.15): только CI/tests; BGP/helper regression gates; `gobfd-qj0.8.6.6.1` |
| OSV Scanner | 2.6.0 → 2.7.0 | 2026-10-19 01:32:07 | [Notes](https://github.com/google/osv-scanner/releases/tag/v2.7.0): parser/offline/call-analysis и SBOM; число входов scan и совместимость JSON/SBOM; `gobfd-qj0.8.6.3` |
| GoReleaser | 2.18.2 → 2.18.3 | 2026-10-16 03:27:35 | [Notes](https://github.com/goreleaser/goreleaser/releases/tag/v2.18.3): upload redirects, webhook headers, image metadata; config validation без подготовки релиза; `gobfd-qj0.8.6.3` |
| Buf | 1.73.0 → 1.74.0 | 2026-10-21 17:36:02 | [Notes](https://github.com/bufbuild/buf/releases/tag/v1.74.0): generation/import strategy и YAML validation; generation drift, lint, breaking checks; `gobfd-qj0.8.6.3` |
| Trivy | 0.75.0 → 0.76.0 | 2026-10-21 11:45:44 | [Notes](https://github.com/aquasecurity/trivy/releases/tag/v0.76.0): parser/CSAF и crypto classification; scan inputs и совместимость SARIF/result; `gobfd-qj0.8.6.3` |
| goreleaser-action | 7.2.3 → 7.3.0 | 2026-10-19 17:33:04 | [Notes](https://github.com/goreleaser/goreleaser-action/releases/tag/v7.3.0), публикация 2026-10-12 17:33:04 UTC; peeled `a4f02ab5355a589de60d917a659ce617ed19b3de`: runner cache разделён по distribution и уровню checksum/cosign; Node 24 не меняется. Версии Action и GoReleaser CLI независимы; cache/verification smoke без публикации; `gobfd-qj0.8.6.4.2` |
| download-artifact | 8.0.1 → 8.0.2 | 2026-10-14 10:09:51 | [Notes](https://github.com/actions/download-artifact/releases/tag/v8.0.2), публикация 2026-10-07 10:09:51 UTC; peeled `9000827ccba6bdab643e8b6fd33ac0654aef8333`: повторы HTTP 429 учитывают допустимый `Retry-After`; сохранить `digest-mismatch: error`, квалифицировать artifact round-trip; `gobfd-qj0.8.6.4.2` |
| upload-artifact | 7.0.1 → 7.0.2 | 2026-10-14 10:08:14 | [Notes](https://github.com/actions/upload-artifact/releases/tag/v7.0.2), публикация 2026-10-07 10:08:14 UTC; peeled `cf430e030ddbb5b0abf93d22962f4752f3646cd9`: artifact dependency и повторы HTTP 429; обновить все семь uses и workflow fixture, квалифицировать artifact round-trip; `gobfd-qj0.8.6.4.2` |
| scorecard-action | 2.4.4 → 2.5.0 | 2026-10-19 02:54:09 | [Notes](https://github.com/ossf/scorecard-action/releases/tag/v2.5.0), публикация 2026-10-12 02:54:09 UTC; peeled `fa7117db83c52d787ae5a89f350999701505d78e`: Scorecard 5.6.0, удаление вывода token variable и изменения results; отдельно квалифицировать SARIF/results и вложенный OCI image; `gobfd-qj0.8.6.4.2` |
| CodeQL Action | 4.38.2 → 4.39.0 | 2026-10-22 10:57:05 | [4.38.3](https://github.com/github/codeql-action/releases/tag/v4.38.3) и [4.39.0](https://github.com/github/codeql-action/releases/tag/v4.39.0), публикация target 2026-10-15 10:57:05 UTC; peeled `6f2ac81f5cd81fe351a197ba0b00d91d03cd2aee`: bundle selection/minimum и проверка `checkout_path`; P0 `gobfd-qj0.8.6.4.1` deferred до этого точного порога |

Все шесть uses CodeQL меняются атомарно после hold и свежей проверки
source/advisories. Требуется отдельно разрешённый disposable hosted Go
init/autobuild/analyze и SARIF smoke; он **не выполнен**. Локальные workflow
checks не квалифицируют hosted execution. [Buildx Action 4.4.1](https://github.com/docker/setup-buildx-action/releases/tag/v4.4.1) с
`f87e5991a6d7451dcb8d9637bfbc97413f497069` уже установлен в локальном `dev`;
PR [#97](https://github.com/dantte-lp/gobfd/pull/97) направлен в `master` и
не является development gap.
SHA Scorecard Action сохраняет ссылку на mutable nested OCI tag; registry
digest/provenance и hosted results требуют отдельной квалификации.

Для BIRD 3.4.0 нет GitHub Release `publishedAt`. Официальный
[GitLab tag](https://gitlab.nic.cz/labs/bird/-/tags/v3.4.0) содержит
`created_at=2026-10-12T19:10:15.000Z`, peeled commit
`c3669497e3949b942d86d07e343092cc439b4659`; порог возраста tag channel —
2026-10-19 19:10:15 UTC. Требуются `NEWS` 3.3.3 и 3.4.0, включая раздел
2.20.0, затем authenticated BFD, timers, BGP и CLI parsing в
`gobfd-qj0.8.6.6`. Истории 3.3.2 и 3.4.0 расходятся: повторение исправления
в commit range не доказывает новую уязвимость установленного дерева.

Последующий source/security audit требует исправленный basis из
`gobfd-qj0.8.6.6.2`: указанный target commit вместе с upstream fix
`7cf75587b6f38b87849fe192bf5dfd124b7702bc`, а не unpatched 3.4.0. Записанный
порог возраста fix, 2026-10-20 15:01:43 UTC, прошёл. Actual patched-image build,
fix provenance/security regression, SBOM и BASE/BGP qualification ещё не
выполнены. Статическое применение patch не подтверждает приёмку образа.

govulncheck 1.9.0 подтверждён
[метаданными Go module](https://proxy.golang.org/golang.org/x/vuln/@v/v1.9.0.info),
`Time=2026-10-11T03:39:53Z`; порог module channel — 2026-10-18 03:39:53 UTC.
Старый GitHub `latestRelease` не задаёт актуальную версию этого канала.
[Диапазон 1.8.0→1.9.0](https://github.com/golang/vuln/compare/v1.8.0...v1.9.0)
обновляет зависимости; требуются оба tool pin sites и проверка diagnostics в
`gobfd-qj0.8.6.3`. Tag/module timestamps отделены от Release `publishedAt`.
Остальные OCI и vendor channels этой таблицей не перепроверены; их владельцы
и исторические наблюдения сохранены ниже. Проверка исходников Actions не
подтверждает adoption или hosted compatibility.

Control slice `gobfd-qj0.8.2.1.3.1.1` добавляет типизированные queue outcomes,
owner terminal reasons, stop abandonment и ограниченный process-lifetime
ledger ListSessions/Prometheus. Get/List/Control Watch используют общие session
drop/auth projections. `gobfd-qj0.8.2.1.3.1.2` расширяет ledger на Echo,
raw/dispatch/recipient/legacy notification outcomes и сохраняет private cleanup
после raw loss через один parked worker на retained physical entry. Parent
`gobfd-qj0.8.2.1.3.1` закрыт в границах delivery accounting и private cleanup
на `2220cc0865752f2df31859a636d376fdc7fdddec`.
Echo Watch lifecycle реализован в `gobfd-qj0.8.2.1.3.10`.
Реализация `gobfd-qj0.8.2.1.3.11` добавляет coalesced public Control/Echo
raw-loss invalidation для live revisioned Watch/GoBGP consumers и authoritative
resnapshots без следующего owner edge или reconnect. Private cleanup и legacy
best-effort delivery сохраняют прежние границы. Local acceptance evidence
фиксируется в Beads; full RFC, stable и soak qualification не заявлены. Kernel
`SO_RXQ_OVFL` — в `gobfd-qj0.8.2.1.6`. Development slices не квалифицируют stable
delivery.

Development-срез причинных наблюдений (`gobfd-qj0.8.2.1.2.1` и
`gobfd-qj0.8.2.4.8`) охватывает согласованные scalar facts, версии строк и
причинную policy GoBGP. Он не квалифицирует stable release или C-bit graceful
restart. Control Watch registration/status реализован в `gobfd-qj0.8.2.1.3.8`;
`gobfd-qj0.8.2.1.3.10` добавляет Echo frozen lifecycle и mixed snapshots без
изменения typed Echo RPC или области Control List/Get/GoBGP.
См. [контракт измерения статуса](01-architecture.md).
H-12 (`gobfd-qj0.8.2.1.3.3`) принят в локальном `dev`: подтверждение успешной
записи, negotiated notification window и отмена owning UDP writes. Локальный
SIGTERM capture для FRR и BIRD подтвердил повторные AdminDown/7 и окно не менее
трёх секунд; sampled BGP uptime и routes сохранились. Это не доказывает
непрерывность между samples или получение пиром. Точная source identity и scope
указаны в [RFC compliance](08-rfc-compliance.md).
Development-срез последнего claim (`gobfd-qj0.8.2.1.3.9`) использует тот же drain
для финального Control-session claim перед освобождением sender, с одним
ограниченным параллельным бюджетом reconciliation и явным incomplete outcome.
Нефинальный claim сохраняет wire state. Failure cleanup unsolicited Down-сессии
останавливает передачу без административного drain по RFC 9468 Section 2.
Эти срезы не квалифицируют stable delivery; raw-loss convergence и RFC 9747
preview blockers остаются отдельными задачами.

![Текущий релиз](https://img.shields.io/badge/Current-v0.6.4-1a73e8?style=for-the-badge)
![Следующий релиз](https://img.shields.io/badge/Next-TBD-34a853?style=for-the-badge)
![Цель](https://img.shields.io/badge/Target-v1.0.0-ea4335?style=for-the-badge)

> Историческая проекция Beads от 2026-10-15 18:10 UTC; текущий снимок выше заменяет её состояние выполнения. Реестр задач находится в
> Beads; этот документ объясняет публичную последовательность релизов и не
> является независимым checklist.

Последний опубликованный релиз GitHub —
[`v0.6.4`](https://github.com/dantte-lp/gobfd/releases/tag/v0.6.4).
Неизменяемые теги `v0.6.2` и `v0.6.3` остаются неопубликованными неудачными
попытками. Артефакты и cumulative notes v0.6.4 проверены, а принятая стабильная
история и оба changelog доставлены в `master`. Все 13 находок независимой
проверки и финальная локальная квалификация приняты в `dev`; это не доказывает
их поставку в stable. Maintenance вновь открыт для пробелов между ветками,
указанных ниже. Защищённая линия `release/v0.6` сохраняет GoBGP v3.37.0;
разработка v1 и переход на GoBGP v4 продолжаются в `dev`.

Новые релизы выполняются только после завершения разработки и финальной
квалификации. Промежуточные задачи доставляют код и локальные evidence в `dev`,
а не создают release tags, drafts или артефакты.

## Обозначения статусов

| Статус | Значение |
|---|---|
| Готово | Принято в `dev`, если не названы другая ветка и commit; не доказывает поставку в stable или публикацию |
| В работе | Активная работа или независимая проверка не завершена |
| Открыто | Запланировано в Beads, но ещё не принято |

## Актуальность стека и совместимость с GoBGP

Направление maintainer (2026-10-13): основная задача — перевести весь стек
`dev` на последние версии компонентов и обеспечить совместимость с GoBGP.
Эпик Beads: `gobfd-qj0.8.6` (P0). Область — `dev`; для `release/v0.6` —
только `gobfd-qj0.8.1.16.4`. Остальные бывшие P0 переведены в P1, кроме
работ по безопасности. Компоненты моложе семи дней принимаются с даты
«допуска».

Решения maintainer:

- GoBGP v4.10.0 — базовая версия `dev` (`f85ca09`); демоны GoBGP v3 в `dev`
  не поддерживаются.
- ConnectRPC переводится на connect-go v2 сейчас. Это breaking для
  Go-потребителей `pkg/bfdpb`; wire-протокол `bfd.v1` не меняется.

Статус первой очереди актуальности стека (2026-10-14):

| Шаг | Задача | Статус |
|---|---|---|
| 1 | `.8.6.2` runtime-модули Go | Проход 1 выполнен (`a0c3a55`); koanf v2.3.8 выполнено (`5584967`); проход 2b обновляет client_golang v1.25.0 и moby client v0.6.2; dbus v5.2.4 ждёт 2026-10-20 |
| 2 | `.8.6.3` инструменты | Проход 1 выполнен (`a07791e`); govulncheck v1.9.0, osv-scanner v2.7.0, GoReleaser v2.18.3, buf v1.74.0, trivy v0.76.0 ждут дат с 2026-10-16 по 2026-10-21 |
| 3 | `.8.2.4.4` capability preflight | Выполнено (`2816f0c`) |
| 4 | Anycast, Kubernetes, Containerlab на GoBGP v4 | Anycast выполнен (`7255aeb`); GoBGP в Containerlab выполнен (`a7c58ae`); код Kubernetes выполнен (`2418fb2`), живой прогон заблокирован (нет k3s) |
| 5 | `.8.6.8` connect-go v2 | Выполнено, в `dev` с `f75c31c` (`2514ae1`, `707ae7d`); совместимость wire закреплена `TestGRPCServerWireCompatibility`; bead закрывается после `.8.6.2`; v2.0.1 с 2026-10-21 (`.8.6.10`) |
| 6 | `.8.6.5` базовые образы, `.8.5.2.6` наблюдаемость | Debian, HAProxy, nginx выполнены (`04df88e`); Oracle Linux с 2026-10-19; наблюдаемость открыта |
| 7 | `.8.6.4` Actions, `.8.6.6` пиры | Открыто; часть допускается с 2026-10-19 |
| 8 | `.8.2.4.5`, `gobfd-0dc`, `.8.6.1` | Preflight нативного BFD выполнен (`f5c6e42`); заявление о версиях и ручной Compose открыты |
| 9 | `.8.4`, `.8.5.2.8` | Область `dev` в `.8.4` выполнена, теперь P1 для доставки в stable; решение по Oracle открыто |

Ретроспектива 2026-10-14 относительно очереди 2026-10-13:

- Доставлено в `dev`: koanf v2.3.8 (`5584967`), проверка исходящей
  аутентификации H-03 (`1629b3c`), повторы действий GoBGP и истечение
  подавления (`eda8e91`).
- Проверено на одном стеке, слияние 2026-10-14 21:04 UTC: connect-go v2
  (`2514ae1`, `707ae7d`), сброс ожидающего Down на AdminDown и при удалении
  сессии (`76415f7`), приём RFC 9468 только single-hop, по connected subnet,
  с лимитом сессий и скорости (`e8f9a38`, `cc9b40e`) и ответы с полученного
  кортежа (`59bfee9`).
- Найдено ревью, а не тестами: изменение с повторами могло поздно отключить
  неуправляемый пир (исправлено в `76415f7`), а single-hop из раздела 6.1
  RFC 9468 не проверялся для пакетов порта 4784 (исправлено в `cc9b40e`).
- Окружение: корневая файловая система хоста заполнилась (1.8 GB свободно) и
  очищена с согласия maintainer; `e2ectl rfc` требует dev-контейнер, поэтому
  RFC-гейт запускается через `interop_rfc_testcontainers`.
- Новые upstream-данные: connect-go v2.0.1 (2026-10-14, четыре исправления,
  включая drain после отклонённого сообщения), допуск с 2026-10-21.

Ретроспектива вечерней сессии 2026-10-14 относительно очереди с 2026-10-15
(предыдущая версия этого раздела):

- Шаг 1 выполнен: проверенный стек и следующий срез попали в `dev` одним
  fast-forward `5584967..f75c31c` (20 коммитов) в 21:05 UTC, после даты
  допуска grpchealth v2.0.0. Локальные гейты на `f75c31c`: race 37 пакетов,
  lint 18 профилей, core 64 s, haproxy-health 75 s, bgp-fast-failover 111 s,
  exabgp-anycast 108 s, RFC 159 s. GitHub CI не запускается на push в `dev`
  (`ci.yml` срабатывает на `master`, `main` и `release/v*`), поэтому
  локальные гейты — единственное подтверждение для `dev`.
- Шаг 3 выполнен сверх плана: `.8.2.1.10.2` закрыт; P1 `.8.2.1.10.3`
  (проверка политики при загрузке и внедрение отказов при откате, `36d5434`,
  `f75c31c`), `.8.2.1.10.4` (удаление shared lease, `e90e9b1`) и `gobfd-b52`
  (`e0a22e0`, `06422d7`) закрыты; `.8.2.4.1.2` закрыт.
- Не выполнено: `.8.6.8` остаётся открытым, так как Beads блокирует его
  задачей `.8.6.2`; `.8.2.1.10.1` (захват IPv6, внедрение отказов при
  закрытии lease); `.8.2.1.8` требует решений maintainer (шаг 8).
- Причины: `gobfd-b52` вызван тем, что tshark `-c` учитывает пакеты, не
  прошедшие display filter (tshark(1)), а не задержкой сброса захвата. Ревью
  нашло, что weak decoding превращает `detect_mult: 4294967299` в 3;
  исправлено для `unsolicited` (`f75c31c`), остальные поля — `gobfd-4j4`.
- Процесс: скрипт мутаций от 2026-10-13 сутки ждал ответа на интерактивный
  запрос `cp` (на хосте `cp` — алиас `cp -i`); скрипты используют
  `command cp -f`, фоновые задачи проверяются при возобновлении сессии.
- Гигиена репозитория: 15 PR Dependabot в `master` закрыты со ссылкой на
  `dev`; удалены 5 влитых удалённых веток, 29 влитых worktree и 87 влитых
  локальных веток; отчёты worktree сохранены в `reports/session-20261014/`.
- Окружение: корневая файловая система заполнена на 96% (13 GB свободно)
  после гейтов.

Ретроспектива ночной сессии 2026-10-14/15 (с 21:15 до 01:10 UTC)
относительно очереди с 2026-10-15 (предыдущая версия очереди ниже):

- Влито в `dev`: 31 коммит `22abeea..f0e442a`, из них шесть ломающих.
- GitHub #86 закрыт: метрики GoBGP и health check `gobfd.gobgp`
  (`2a4afb1`, `.8.2.4.6`).
- GitHub #87 закрыт: метрики и agent-check на loopback (`ff2d70f`), API на
  `127.0.0.1:50052` (`0314466`),
  [решение о транспорте](../superpowers/specs/2026-10-15-management-transport-decision.md)
  (`5879b15`) и `gobfd-330` в четыре фазы: Unix-сокет (`02ffd16`), mTLS
  (`aae60cf`), по умолчанию только сокет (`319d2bb`), claims по принципалам
  и роли mTLS (`0d949dd`).
- `.8.2.3.2`: дамп flight recorder доступен только за границей управления
  (`c7bfe14`).
- Шаг 2 частично: освобождение lease при конкурентном приёме и reload
  (`bc768d6`); захват IPv6 ждёт `.8.2.1.10.5`.
- Шаг 5: `.8.2.4.1` пиры, отключённые оператором (`384b8a5`), и `.8.2.4.2`
  каноническая идентичность сессии (`8536702`, `d9380e1`); оба закрываются
  после `.8.2.1.3.2`.
- Вне очереди:
  [оценка cilium/ebpf](../superpowers/specs/2026-10-15-ebpf-evaluation.md)
  (`b5a24b5`), `gobfd-376` перенесён в backlog.
- Не выполнено: шаг 1 (ни одно обновление не допускается раньше 2026-10-15
  13:02 UTC); шаг 3 `.8.2.1.8` (решения); шаг 4 `.8.5.2.7` (containerlab
  v0.81.0 с 2026-10-18); шаг 6 `.8.6.1` (`.8.6.9`); шаг 7 `gobfd-4j4`;
  шаг 8: решений maintainer не поступило.
- Найдено ревью, а не тестами: проверка канонической сессии для BFD Up
  оставляла отключённым пир, который отключил GoBFD, после снятия
  неоднозначности; исправлено до слияния (`d9380e1`). Клиентам grpc-go нужен
  ALPN `h2` на listener mTLS (измерено, `aae60cf`).
- Новые beads: `gobfd-6de` (Prometheus в production Compose не может
  собирать метрики; дефект существовал до `ff2d70f`), `gobfd-gyg` (opt-out
  сессии из управления GoBGP, P3), `gobfd-5w0` (дизайн #88).
- Процесс: `bd update --notes` заменил существующие заметки у двух beads;
  они восстановлены из `bd history`, датированные записи добавляются через
  `--append-notes`. Однажды правки попали в основной checkout через `cd` в
  параллельных командах и были перенесены в worktree. Прогон гейтов на
  заменённой ревизии оставлен до завершения: Ryuk отключён, и прерванный
  прогон оставляет контейнеры.
- Локальные гейты на `f0e442a`: race 38 пакетов, lint 18 профилей,
  bgp-fast-failover 110 s, BGP interop 175 s. Корневая файловая система
  заполнена на 89% (34 GB свободно).

Изменения приоритетов 2026-10-15 по результату поиска открытых блокеров с
приоритетом ниже блокируемого bead:

- `.8.2.1.3.2` P1 → P0: блокирует P0 `.8.2.4.1` и `.8.2.4.2`, код которых
  выполнен.
- `.8.2.1.10.5` P1 → P0: блокирует security P0 `.8.2.1.10.1`.
- `gobfd-6de` P2 → P1: в документированном развёртывании Compose сбор
  метрик не работает.
- Срезы Q4 и Q5 `.8.2.8.1.4`, `.8.2.8.5.1`, `.8.2.8.6`, `.8.2.8.6.2` и их
  дочерние задачи (12 beads) P2 → P1: блокируют закрытие спринтов P1.
- Оставлено: эпик `.8.2.3` (P0) зависит от эпика `.8.2.2` (P1, 13 открытых
  beads).

Ретроспектива дневной сессии 2026-10-15 (с 01:25 до 18:10 UTC)
относительно очереди 2026-10-15 01:20 UTC (ниже, обновлена на месте):

- Влито в `dev`: 9 коммитов `393e0d8..044d776`, из них три ломающих.
- Шаг 2 выполнен: `.8.2.1.3.2` закрыт (ревизии Manager, маркер разрыва в
  зарезервированном последнем слоте подписчика, аддитивные поля ревизии
  `bfd.v1`, `GAP` и `SYNC_COMPLETE`, неоднозначный `GetSession` завершается
  с `FAILED_PRECONDITION`); `.8.2.4.1` закрыт (восстановление GoBGP после
  разрыва потока, демпфирование, которое никогда не задерживает Down,
  ссылка на RFC 5882 Section 10.2); `.8.2.4.2` закрыт.
- Не выполнено: шаг 1 (client_golang v1.25.0 допускается с 13:02 UTC, не
  начат); шаги 3–9 без изменений; решений maintainer не поступило.
- Найдено ревью, а не тестами: фильтр типов H-31 отбросил бы маркер разрыва
  (проверка разрыва теперь идёт первой); устаревшие комментарии о
  демпфировании и ссылка на RFC 5882 Section 4.3, которую матрица
  2026-09-08 относит к H-27. Найдено тестами: фильтр повторного состояния
  ломал тесты демпфирования, поэтому применяется только к сессиям,
  затронутым resync. Одна граничная мутация (`<=` → `<`) выжила, пока в тест
  не добавлено покрытое событие.
- Измерено: `buf breaking` против `dev` — exit 0; проба с удалением поля —
  exit 100.
- Процесс: проверка атрибуции совпала с фразой о регенерации кода buf в
  сообщении коммита (переформулировано до push); `buf breaking` из worktree требует
  монтирования репозитория по абсолютному пути.
- Новые beads: `.8.2.1.3.6` (P1, события удаления), `.8.2.4.7` (P1,
  наблюдаемость демпфирования). Новых инверсий приоритетов нет; осталась
  только `.8.2.3` от `.8.2.2`.
- Гейты: на `d42553e` race 38 пакетов, lint 18 профилей, core 69 s,
  haproxy-health 81 s, bgp-fast-failover 109 s, exabgp-anycast 110 s, BGP
  interop 176 s; на `d65b994` race 38, lint 18, bgp-fast-failover 110 s, BGP
  interop 175 s; lint 18 на `35155e3`.
- Очистка с разрешения maintainer: удалены 15 влитых worktree задач и их
  локальные ветки (`task/preview-boundaries` — дубль `b0d1620`); отчёты
  worktree сохранены в `reports/session-20261015/`; удалены остатки сборок
  GoBFD (47 образов без тега, 8 зависших рабочих контейнеров buildah, 14
  устаревших образов гейтов); свободное место на корневой ФС — с 17 до 31 GB.
- Перепроверка версий в 18:10 UTC (`proxy.golang.org`, go.dev, `gh api
  graphql`): изменений нет, кроме containerlab v0.81.1 (2026-10-15 08:21,
  допуск 2026-10-22; исправление подсетей управления Docker).
- Передача работы: [handoff агента 2026-10-15](../superpowers/plans/2026-10-15-agent-handoff.md).

Продолжение 2026-10-15 после снимка 18:10 UTC:

- Закрыты в локальном `dev`: `gobfd-6de`, `gobfd-qj0.8.2.1.3.7`,
  `gobfd-qj0.8.2.1.14`, `gobfd-qj0.8.2.1.3.6`,
  `gobfd-qj0.8.2.5.1`–`gobfd-qj0.8.2.5.3`, `gobfd-qj0.8.2.4.7` и
  `gobfd-4j4` и `gobfd-8f3.7`–`gobfd-8f3.9`. Принятый код включён в
  `9eaa4707877505a2439039957e606fd4fdfd3a82`. Закрытие двенадцати задач не
  закрывает родительские вехи с оставшимися зависимостями безопасности.
- Политика UDP-буферов подключена ко всем существующим точкам создания
  сокетов демона; реальные Linux-пробы охватывают control/Echo/Micro-BFD
  listeners, четыре пути отправителей и duplex VXLAN/Geneve. Ошибочный
  словарь логирования отклоняется до startup/reload mutation. Объединённая
  критическая проверка с race проходит на всех десяти затронутых пакетах
  принятого дерева исходников. Итоговые ограниченные проверки на
  `9eaa4707877505a2439039957e606fd4fdfd3a82`: собраны четыре бинарника с
  `CGO_ENABLED=0` и `-trimpath`; 18 профилей canonical lint — 0 замечаний;
  `go vet ./...`, `go mod tidy -diff` и `go mod verify` — PASS.
- Production Compose: отрендерены все десять файлов. В изолированном
  общем network namespace конфигурация мониторинга показала одну здоровую
  цель Prometheus `127.0.0.1:9100` и здоровый datasource Grafana; listener
  портов 3000, 9090 и 9100 оставались только на loopback.
- Живая квалификация компаньонов на
  `55e3f94ee2af82bd6cb9377de1708b9e77834341`: HAProxy исключает
  приостановленный sidecar и восстанавливает его, PASS за 97.29 s;
  ExaBGP анонсирует, отзывает и восстанавливает маршрут, PASS. Локальные
  доказательства — `reports/session-20261015-continuation/`. Эти прогоны
  не квалифицируют последующие изменения и весь релизный envelope v1.
- Строгий `govulncheck` на
  `9eaa4707877505a2439039957e606fd4fdfd3a82`: достижимых уязвимостей — 0,
  уязвимых импортированных пакетов — 0. Сохраняются два module-only finding;
  это не подтверждает чистый граф зависимостей для stable.
- Удалённая интеграция следует правилу reviewed PR из `CONTRIBUTING.md`.
  Ветка публикации — `task/v1-runtime-recovery-20261015`; прямое обновление
  `origin/dev` не выполняется. Даже draft PR запускает полный тестовый набор
  из `.github/workflows/build.yml`, поэтому этот шаг остаётся отложенным
  при текущем ограничении «только критические тесты». Локальное принятие
  изменений не означает удалённую интеграцию.
- Stable v1 блокируют утверждённые
  [условие по advisory GoBGP и контракт 24-часовой квалификации](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md).
  На 2026-10-15 [Go vulnerability database](https://vuln.go.dev/ID/GO-2026-4736)
  всё ещё не указывает исправленный диапазон GoBGP v4. Исключение для
  разработки не снимает это условие stable. 24-часовой прогон не выполнялся.

Текущий порядок выполнения и точные UTC-пороги указаны в датированном
снимке в начале документа. Старые протоколы относятся только к своим
ревизиям; текущий реестр задач находится в Beads.

Выполнено до 2026-10-14 и оставлено для справки: заявление о
поддерживаемых версиях GoBGP `gobfd-0dc` (`1381879`), наблюдаемость на
trixie `.8.2.8.5.2.6` (`5cf77e9`).

Заблокировано окружением: живой прогон Kubernetes `.8.5.2.5.3` (нет k3s),
прогоны Containerlab с vendor NOS (нет бинарника containerlab и
vendor-образов).

Матрица версий измерена 2026-10-14; релизы GitHub, Go-модули и go.dev перепроверены 2026-10-15 в 18:10 UTC, остальные каналы не перепроверялись. Источники: релизы через `gh api graphql`,
`proxy.golang.org`, `go list -m -u`, API реестров Docker Hub и GHCR, индекс
пакетов `deb.frrouting.org`.

| Компонент | Канал | `dev` | Последняя | Допуск | Владелец |
|---|---|---|---|---|---|
| Go toolchain | go.dev | 1.27.2 | 1.27.2 | актуально | выполнено |
| GoBGP | GitHub release | v4.10.0 | v4.10.0 | актуально | выполнено |
| connect-go | Go-модуль | v2.0.0 | v2.0.1 | 2026-10-21 | `.8.6.10` |
| grpchealth | Go-модуль | v2.0.0 | v2.0.0 | актуально | выполнено |
| grpc-go | Go-модуль | v1.84.0 | v1.84.0 | актуально | выполнено |
| koanf/v2 | Go-модуль | v2.3.8 | v2.3.8 | актуально | выполнено |
| client_golang | Go-модуль | v1.25.0 | v1.25.0 | актуально | `.8.6.2` |
| client_model | Go-модуль | v0.6.3 | v0.6.3 | актуально | выполнено |
| godbus/dbus/v5 | Go-модуль | v5.2.2 | v5.2.4 | 2026-10-20 | `.8.6.2` |
| moby api / client | Go-модуль | v1.56.1 / v0.6.2 | v1.56.1 / v0.6.2 | актуально | `.8.6.2` |
| golangci-lint | tools-модуль | v2.14.0 | v2.14.0 | актуально | выполнено |
| govulncheck | Go-модуль | v1.8.0 | v1.9.0 | 2026-10-18 | `.8.6.3` |
| osv-scanner | GitHub release | v2.6.0 | v2.7.0 | 2026-10-19 | `.8.6.3` |
| buf | GitHub release | v1.73.0 | v1.74.0 | 2026-10-21 | `.8.6.3` |
| Docker Compose provider | GitHub release | 5.6.0 | 5.6.0 | актуально | выполнено |
| syft / trivy | GitHub release | v1.54.1 / v0.75.0 | v1.54.1 / v0.76.0 | trivy 2026-10-21 | `.8.6.3` |
| goreleaser | GitHub release | v2.18.2 | v2.18.3 | 2026-10-16 | `.8.6.3` |
| containerlab | GitHub release | 0.79.0 | v0.81.1 | v0.81.0 2026-10-18, v0.81.1 2026-10-22 | `.8.5.2.7` |
| debian:trixie-slim | digest Docker Hub | `a29215f6` (13.7) | `a29215f6` | актуально | выполнено |
| oraclelinux:10-slim | digest Docker Hub | `965eb786` | `ea76251f` | 2026-10-19 | `.8.6.5` |
| HAProxy | Docker Hub trixie | 3.4.6 | 3.4.6 | актуально | выполнено |
| nginx (mainline) | Docker Hub trixie | 1.31.6 | 1.31.6 | актуально | выполнено |
| Prometheus | GitHub release | v3.15.0 | v3.15.0 | актуально | выполнено |
| Grafana | GitHub release | 13.2.4 | 13.2.4 | актуально (исключение по безопасности) | выполнено |
| FRR | deb.frrouting.org frr-10.7 | 10.7.1 | 10.7.1 | актуально | выполнено |
| BIRD | GitHub tag | v3.3.2 | v3.4.0 | 2026-10-19 | `.8.6.6` |
| ExaBGP | GitHub release | 5.0.13 | 5.0.14 | 2026-10-19 | `.8.6.6` |
| Holo | GHCR bundle | v0.9.0 | v0.9.0 | актуально | выполнено |
| Thoro/bfd | Git commit | `5d09e29` | `5d09e29` (коммитов нет с 2021-05-15) | решение | `.8.6.6` |
| Nokia SR Linux | GHCR | 25.10.2 | 26.7.2 | сейчас | `.8.5.2.7` |
| Arista cEOS | портал вендора | 4.36.0.1F | не проверено | — | `.8.5.2.7` |
| GitHub Actions | GitHub release | 7 из 11 обновлены | artifact actions, codeql v4.38.3, goreleaser-action v7.3.0, scorecard v2.5.0 | с 2026-10-14 по 2026-10-19 | `.8.6.4` |

Актуальность Arista cEOS не проверена: `arista-mcp` был недоступен
2026-10-13. Образы XRd, SONiC и VyOS выдаются вендором или являются
изменяемыми сторонними тегами. Кластер k3s для примера Kubernetes внешний и
не закреплён.

## Сверка GitHub issues 2026-10-15

Источник: issues `dantte-lp/gobfd` через `gh api graphql` (3 открыто,
0 закрыто), сверены с `dev@610ce2e`, одобренным
[дизайном v1](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md)
и текстами RFC ниже.

| Issue | Beads | Состояние в `dev` | Остаётся |
|---|---|---|---|
| [#86](https://github.com/dantte-lp/gobfd/issues/86) управление пирами GoBGP v4 | `gobfd-qj0.8.2.4.6` (P1), родитель `.8.2.4` | Выполнено, issue закрыт. Клиент v4.10.0, preflight, политика «только v4» и документация, повторы действий, failover e2e на GoBGP v4.10.0, метрики GoBGP и health check `gobfd.gobgp` (`2a4afb1`) | ни один релиз ещё не содержит поддержку v4 |
| [#87](https://github.com/dantte-lp/gobfd/issues/87) безопасные адреса по умолчанию | `gobfd-qj0.8.2.3.4` (P0), вместе с `gobfd-330` и `.8.2.3.2` | API `:50051` и метрики `:9100` на всех адресах; `gobgp.addr` по умолчанию `127.0.0.1:50051` на том же порту; пакеты ставят `configs/gobfd.example.yml` как `/etc/gobfd/gobfd.yml` с теми же wildcard-адресами; агент HAProxy слушает все адреса | `.8.2.3.4` выполнен (`ff2d70f`, `0314466`): API `127.0.0.1:50052` и метрики `127.0.0.1:9100` по умолчанию, `listen_addr` агента, предупреждение при запуске, тест портов рядом с gobgpd; Выполнено, issue закрыт. `gobfd-330.1`–`.4`: Unix-сокет с принципалами по UID peer, mTLS, по умолчанию только сокет, анонимный loopback только для чтения (`319d2bb`), claims по принципалам и роли mTLS (`0d949dd`) |
| [#88](https://github.com/dantte-lp/gobfd/issues/88) anycast VIP и здоровье HAProxy | `gobfd-5w0` (P2), заблокирован `.8.2.3` и `.8.2.5.2` | не начато | сначала spec дизайна |

Архитектурные заметки для `gobfd-5w0`:

- RFC 4786, раздел 4.4.1 связывает анонс с доступностью сервиса и требует
  минимальной задержки перед повторным анонсом.
- BFD AdminDown — ненадёжный триггер отзыва: RFC 5882, раздел 3.2 указывает,
  что система SHOULD NOT сообщать об отказе связности при AdminDown клиенту
  со своей проверкой живости, а у BGP она есть. В `bfd.v1` нет RPC AdminDown.
- `AddPath`/`DeletePath` GoBGP v4.10.0 (`proto/api/gobgp.proto`) позволяют
  владеть путём VIP; `DisablePeer` остаётся запасным вариантом.
- `show stat` и `show info` через stats socket HAProxy дают состояние
  frontend и серверов (HAProxy `doc/management.txt`).
- Форма: отдельный компаньон по образцу `gobfd-exabgp-bridge`, не модуль
  демона: источник здоровья, автомат hold-down, владелец пути GoBGP, команда
  drain, метрики.

`gobfd-5w0` ждёт блокеров и ревью дизайна. Изменения приоритетов записаны
в ретроспективе 2026-10-15 выше.

## Оставшиеся работы до v1.0.0

Снимок ретро на 2026-10-20 16:10 UTC: закрыты 353 из 513 repository issues,
49 закрыты с 2026-10-15. Это число записей tracker, включая parent closures,
а не процент готовности или свежая runtime-проверка. Сама ограниченная задача
ретро в снимок не включена. В v1 scope остаются 154 незавершённые записи,
включая epics и milestones: 119 open, 26 in progress, четыре blocked и пять
deferred. Scope включает prerequisite сопровождения v0.6, stack, quality и
configuration owners; исключает root, `.8.5` и четыре post-v1-задачи ниже.

| Направление | Незавершено |
|---|---:|
| Актуальность стека | 18 |
| Ядро RFC и performance | 18 |
| Ownership и configuration, включая `gobfd-8f3` | 10 |
| Secure management | 3 |
| GoBGP и companions | 2 |
| Спринты качества | 90 |
| Release qualification и независимое ревью | 4 |
| Stable maintenance и advisory disposition | 6 |
| Отдельные CI/attribution-задачи и v1 milestone | 3 |

Реализованные companion children и delivery/accounting children закрыты;
combined-consumer acceptance `.8.2.6.2` и родительские проверки открыты.
Не квалифицированы readiness, systemd, transactional replace/Micro-BFD,
transport/latency, preview boundaries, final interop/security, SBOM/provenance,
upgrade/rollback и gates 1 000 сессий за 24 часа/5 000 сессий.
Локальные индексы evidence: `reports/retro-20261020/completed-all.tsv`,
`completed-since-20261015.tsv` и `remaining-all.tsv` в том же каталоге.

### Исторический снимок — 2026-10-15

Beads на 2026-10-15 18:10 UTC: 171 открытый или выполняемых beads в объёме
v1, включая эпики и веху `.8.2`. Это исторический снимок 18:10; изменения
статусов продолжения перечислены выше. Без корня `gobfd-qj0.8` и beads после v1,
перечисленных в конце. Числа даны как P0/P1/P2.

| Направление | Beads | Открыто | Состояние |
|---|---|---|---|
| Актуальность стека | `.8.6` | 11 (9/2/0) | Пять в работе; вторые проходы по датам с 2026-10-15 по 2026-10-21; `.8.6.1` ждёт `.8.6.9` |
| Ядро RFC и учёт потерь | `.8.2.1` | 30 (4/23/3) | P0 `.1.8`, `.10`, `.10.1`, `.10.5`; `.1.3.2` выполнен; события удаления `.1.3.6` и учёт потерь `.1.3.1` открыты; Poll/Final и Demand в работе; живая квалификация таймеров `.1.1.1.4.3` открыта |
| Гейты производительности | `.8.2.1.4.1`, `.8.2.1.6`, `.8.2.1.7` | 3 (0/3/0) | Базовая линия задержки, политика QoS сокетов, отказ от закрепления OS-потоков |
| Владение и конфигурация | `.8.2.2`, вместе с `gobfd-8f3` | 13 (0/11/2) | Четыре в работе; типизированная замена, транзакции Micro-BFD, буферы сокетов, привязки listener |
| Безопасное управление | `.8.2.3` | 3 (1/2/0) | `gobfd-330`, `.3.2`, `.3.4` выполнены; `.3.1` и `.3.3` открыты |
| GoBGP v4 | `.8.2.4` | 2 (1/1/0) | `.4.1` и `.4.2` закрыты; открыт `.4.7` наблюдаемость демпфирования; эпик ждёт `.8.2.3` |
| Компаньоны | `.8.2.5` | 4 (0/4/0) | Переподключение ExaBGP и HAProxy, каноническая идентичность |
| Независимое ревью | `.8.2.7` | 1 (0/1/0) | Ждёт `.8.2.4` и `.8.2.5` |
| Спринты качества Q3–Q6 | `.8.2.8` | 90 (6/81/2), один P3 | Q3 без shell и Python 12; Q4 артефакты и образы 25 (P0: образы trixie `.8.5.2`, FRR `.8.5.2.4`, failover и ExaBGP `.8.5.2.5`, Containerlab `.8.5.2.7`, Oracle `.8.5.2.8`); Q5 семантика и Sonar 21; Q6 квалификация, документация, ревью и закрытие 27; ещё четыре |
| Релизная квалификация | `.8.2.6` | 3 (0/2/1) | Зависит от `.8.1`, `.8.2.4`, `.8.2.5`, `.8.2.7`, `.8.4` |
| Решение по maintenance v0.6 | `.8.1` | 5 (0/5/0) | Требуется для `.8.2.6` |
| Allowlist уязвимостей | `.8.4` | 1 (0/1/0) | Записи истекают 2026-12-31 |
| Отдельные | `gobfd-6de`, `gobfd-20z`, `gobfd-31o`, `gobfd-4j4` | 4 (0/3/1) | Метрики Compose, решение о CI на `dev`, продолжение по атрибуции (остановлено), проверки диапазона `detect_mult` |

Критический путь: оставшиеся работы по владению `.8.2.2` и управлению
`.8.2.3`, затем закрытие родителей `.8.2.4` и `.8.2.5`, независимое ревью
`.8.2.7`, закрытие Q6 `.8.2.8.7` и релизная квалификация
`.8.2.6`; параллельно идут security P0 `.8.2.1.10` и `.8.2.1.8` и P0 по
образам. Release notes v1.0.0 должны описать ломающие изменения, уже
находящиеся в `dev`: connect-go v2 для Go-потребителей `pkg/bfdpb`, только
демоны GoBGP v4, API управления на Unix-сокете `/run/gobfd/gobfd.sock` (TCP
`127.0.0.1:50052` только при настройке, без анонимных изменений), метрики на
`127.0.0.1:9100`, flight recorder за границей управления, одна BFD-сессия
на соседа GoBGP, демпфирование, которое никогда не задерживает Down
(требуются положительные `reuse_threshold` и `max_suppress_time`), и ошибка
`FAILED_PRECONDITION` у `GetSession` по неоднозначному адресу пира
(`.8.2.8.7.2.4`).

Вне v1: `gobfd-5w0` (компаньон #88), `gobfd-gyg`, `gobfd-376`, `gobfd-dor`,
`.8.5` (Dependabot в `master`).

## Сверка плана 2026-10-13

Головы веток: `dev` и `origin/dev` — `524ba67`, `origin/master` — `5680385`,
`origin/release/v0.6` — `145d7a3`. После 2026-09-08 коммитов ни в одну из них
не поступало. Два task worktree не содержат коммитов сверх `dev`.

Измерено на `dev@524ba67` с локальным `go1.27.0`:

- `govulncheck@v1.7.0 ./...` завершается с кодом 1: 13 достижимых advisory.
  Двенадцать исправлены в `go1.27.2` и `golang.org/x/net` v0.60.0:
  GO-2026-6599, 6600, 6603, 6604, 6605, 6607, 6608, 6610, 6611, 6612,
  6613 и 6617. Релиз `go1.27.2` (2026-10-08) содержит security-исправления go
  command, `crypto/tls`, `html/template`, `net/http`, `net/textproto` и `os`
  ([история релизов Go](https://go.dev/doc/devel/release)). Владелец:
  `gobfd-qj0.8.2.11`, P0.
- `make vulncheck` завершается с кодом 1. Записи allowlist GO-2026-4736
  (GoBGP v3) и GO-2026-5932 (`x/crypto` openpgp, module-only) истекли
  2026-09-30 в `dev`, `master` и `release/v0.6`. GoBGP v4 устраняет
  GO-2026-4736 только в `dev`, так как `release/v0.6` сохраняет GoBGP v3.37.0.
  Владелец решений по веткам: `gobfd-qj0.8.4`, P0. Даты не продлеваются без
  зафиксированного решения maintainer.
- Экспозиция stable `v0.6.4` и неуспешный nightly E2E run `38376260900` в
  `master` (сборка Compose для `test/interop/compose.yml`, причина не
  установлена) относятся к `gobfd-qj0.8.1.16.4`.
- Пятнадцать Dependabot PR и внешний PR `#62` нацелены на `master`; девять из
  шестнадцати в конфликте. Владелец разбора: `gobfd-qj0.8.5`, P2.

Изменения плана:

- Финальная квалификация `gobfd-qj0.8.2.6` зависит от `gobfd-qj0.8.2.11` и
  `gobfd-qj0.8.4`.
- Живая квалификация таймеров `.1.1.1.4.3` зависит от принятого BASE-потребителя
  FRR `.8.5.2.4.2.1`, а не от всего родителя образов. Её харнесс
  (`test/interop`) не содержит исполняемых ссылок на Alpine или
  `quay.io/frrouting`. Требуется подтверждение maintainer.
- Задачи Q3 `.8.2.2.5.1`, `.8.2.2.8` и задача `gobfd-31o` зафиксированы как
  остановленные, а не выполненные.

Порядок очереди P0 в `dev` (заменён очередью актуальности стека выше):

1. `gobfd-qj0.8.2.11` — `go1.27.2` и `x/net` v0.61.0; реализовано в `dev`;
   двенадцать advisory больше не обнаруживаются.
2. `gobfd-qj0.8.2.4.3` — первый этап GoBGP v4. Клиент и все потребители демона
   GoBGP переводятся одновременно, так как v3 `/apipb.GobgpApi` и v4
   `/api.GoBgpService` несовместимы по wire. Устраняет GO-2026-4736 в `dev`.
   Доставлено в `dev` (`f85ca09`..`8365f1c`) с GoBGP v4.10.0 для
   клиента и потребителей общего рецепта пира. Локальные amd64 gates
   2026-10-13: `TestBGPBFDTopologyTestcontainers` пройден за 359.25 с (четыре
   race-подтеста, без пропусков, версия GoBGP 4.10.0); testcontainers BGP
   fast-failover пройден за 105.39 с; RFC testcontainers gate пройден за
   162.12 с с четырьмя race-сценариями. Ручные пути Compose не
   перезапускались. `govulncheck` не обнаруживает достижимого GO-2026-4736;
   finding на уровне модуля остаётся в allowlist (шаг 3). ExaBGP anycast,
   Kubernetes и Containerlab остаются на образах GoBGP v3.37.0 в рамках
   `gobfd-qj0.8.2.8.5.2.5.2`, `gobfd-qj0.8.2.8.5.2.5.3` и
   `gobfd-qj0.8.2.8.5.2.7`.
3. `gobfd-qj0.8.4` — решения по истёкшему allowlist для каждого ID и ветки;
   решение для GO-2026-4736 в `dev` зависит от шага 2. Решения maintainer от
   2026-10-13: `dev` использует GoBGP v4.10.0 с записью `ModuleOnly` для
   GO-2026-4736 по модулю `github.com/osrg/gobgp/v4`, запись `ModuleOnly` для
   GO-2026-5932 продлена, обе до 2026-12-31; `release/v0.6` принимает
   GO-2026-4736 на GoBGP v3.37.0 до 2026-12-31 при условии доступа к GoBGP API
   только с localhost или из доверенной management-сети, поставка отдельно в
   `gobfd-qj0.8.1.16.4`.
4. `gobfd-qj0.8.2.1.1.1.4.3` — живая квалификация перезагрузки таймеров с FRR
   и BIRD.
5. `.8.5.2.5.2`, `.8.5.2.5.3`, `.8.5.2.6`, `.8.5.2.7`, `.8.5.2.8` — оставшиеся
   потребители образов и решение по продукту Oracle Linux.
6. Остальной объём reconciliation `gobfd-qj0.8.2.4`, дочерние задачи RFC core
   и ownership в порядке `bd ready`.

## Maintenance baseline v0.6

Milestone Beads: `gobfd-qj0.8.1` — **Открыто; вновь открыт для поставки в stable**
в рамках `gobfd-qj0.8.1.16`.

Защищённая ветка `release/v0.6` сохраняет GoBGP v3.37.0 и существующие
runtime-контракты `bfd.v1` и YAML. Она обновляет зависимости, инструменты, CI,
воспроизводимость, документацию и тестовую инфраструктуру без добавления нового
поведения протокола BFD. Тег, assets и OCI-образы v0.6.4 проверены.
Qualification `gobfd-qj0.8.1.7` фиксирует принятый baseline `dev`, а не
квалификацию текущих stable heads. Следующие строки описывают приёмку
реализации в `dev`, а не состав v0.6.4.

| Часть поставки | Статус |
|---|---|
| Инвентаризация версий зависимостей и инструментов | Готово |
| Toolchain Go 1.27 и обновление CI | Готово |
| Go-owned Podman testcontainers harness | Готово |
| Миграция interop, integration и E2E orchestration | Готово |
| Исторический Python tooling island и контракт Docker Compose v5 | Принято ранее; сохранение Python отменено политикой no-Python |
| Инвентарь лицензий, SBOM, OCI provenance и уязвимостей | Готово |
| Граница образов Debian trixie / Oracle Linux 10 | Готово |
| Исправление публичных RFC- и benchmark-заявлений | Готово |
| Roadmap, Quick Start, архитектура и EN/RU parity | Готово |
| Независимая проверка всех частей v0.6 и исправление P0/P1 | Готово |
| Финальная локальная квалификация `dev` | Готово |
| Регистрация изолированного `tools/go.mod` в Dependabot | Готово |

Release-задача `gobfd-qj0.8.1.15` завершена после исправления: неизменяемый
`v0.6.4` по-прежнему указывает на
`b1c0bcd7d2e9abed00368b2082e34f521084c087`, все 12 assets и OCI indexes
проверены, а body теперь охватывает v0.6.2-v0.6.4. PR `#67` и `#68` доставили
принятое исправление в `release/v0.6` и `master`; эта история `dev` содержит
отдельный forward-port. Этот published release receipt остаётся принятым и
не переписывается при проверке stable gaps. Независимая проверка
`gobfd-qj0.8.1.8` и все 13 дочерних находок остаются закрытыми как evidence
реализации; поставка в stable имеет отдельных владельцев.

### Пробелы maintenance по веткам

Сравнение от 2026-09-07 использует `dev` на
`401f6a6a0adc20cd440f4de2d424327984c49111`, `master` на
`5680385679d8e03d74226b5fcde269210d63bde8` и `release/v0.6` на
`145d7a33571b76f234fd06ccb00798586a40b8d5`. Исправление находок 1–7,
`e0e85b1`, является предком всех трёх heads; текущее поведение здесь не
квалифицируется повторно. Шесть последующих fix commits присутствуют только в `dev`:

| Закрытая находка | Принятое исправление в `dev` | Текущий пробел stable | Владелец поставки в Beads |
|---|---|---|---|
| `gobfd-qj0.8.1.8.8` | `37f1b26` | Неквалифицированный `ENV GOMEMLIMIT=256MiB` в `deployments/docker/Containerfile` | `gobfd-qj0.8.1.16.2` — Открыто |
| `gobfd-qj0.8.1.8.9` | `4965ef4` | Заявления о числе RFC и scratch-образе в EN/RU performance analysis | `gobfd-qj0.8.1.16.2` — Открыто |
| `gobfd-qj0.8.1.8.11` | `c5aa0c2` | Заявления об отсутствии потерь сессий при SIGHUP в EN/RU performance analysis | `gobfd-qj0.8.1.16.2` — Открыто |
| `gobfd-qj0.8.1.8.10` | `b3677f8` — image ownership | В stable используется прежний guarded shell runner; применимость не доказана | `gobfd-qj0.8.1.16.3` — Открыто |
| `gobfd-qj0.8.1.8.12` | `0632c99` — host-global lock | В stable отсутствует этот Go lifecycle; применимость не доказана | `gobfd-qj0.8.1.16.3` — Открыто |
| `gobfd-qj0.8.1.8.13` | `d462b4c` — live identity validation | В stable отсутствует этот Go lifecycle; применимость не доказана | `gobfd-qj0.8.1.16.3` — Открыто |

Последние три находки требуют анализа применимости по исходникам, а не
слепого backport или новых shell-изменений. Исправление более поздних находок
аудита v1 в stable также не подразумевается. Реестр поставки остаётся в Beads.

## Сверка legacy S12

Прежний waterfall-документ S12-S20 появился до утверждённого релизного плана
Beads. Typed CRUD из S12 был поставлен в `v0.6.0` только частично:

| Контракт S12 | Текущее подтверждение | Статус |
|---|---|---|
| CRUD `EchoService` и `gobfdctl echo` | Есть proto, server, CLI и tests | Готово |
| CRUD `MicroBFDService` и `gobfdctl micro` | Есть proto, server, CLI и tests | Готово |
| `OverlayService` для VXLAN/Geneve | Service и CLI-команды отсутствуют | Не поставлено |

Наличие конфигурационных и runtime-путей VXLAN/Geneve не означает наличия
typed Overlay API. Публичная граница поддержки остаётся указанной в
[матрице соответствия RFC](./08-rfc-compliance.md).

Старые checklists S13-S20 заменены текущим планом. Secure management, GoBGP v4,
S-BFD, kernel backends и AF_XDP нельзя считать запланированными или готовыми по
историческим целям; авторитетны только текущие issues Beads и принятый код.

## Production-контракт v1.0.0

Milestone Beads: `gobfd-qj0.8.2` — **Открыто; baseline `dev` принят,
поставка в stable остаётся открытой**.

Разработка продуктовой линии v1, включая переход на GoBGP v4, ведётся в `dev`
и не меняет границу GoBGP v3.37.0 ветки `release/v0.6`.
Финальная квалификация `gobfd-qj0.8.2.6` зависит от `gobfd-qj0.8.1` для
решения maintenance-задач. Это не блокирует очередь разработки v1;
исторический publication prerequisite `gobfd-qj0.8.1.15` остаётся принятым.

### Сверка внешнего аудита

[Полная матрица gist](../en/gist-audit-reconciliation.md) сверяет все 69 пунктов
с `dev@49085d9`: **25 исправлено, 9 частично, 34 открыто, 1 принятое ограничение**.
Частичные пункты: H-02, H-06, H-16, H-19, H-23, H-24, H-30, M-05 и M-09.
Каждый пункт имеет владельца в Beads; отдельно сопоставлены предложения gist
по regression tests и финальной квалификации. Это результаты проверки исходников,
не новая runtime-квалификация и не доказательство поставки в stable.

Сквозная приёмка `gobfd-qj0.8.2.6.2` объединяет семь существующих владельцев
реализации C-06/H-31 без дублирования работы. Micro-BFD ownership
`gobfd-qj0.8.2.2.1` явно включает общую валидацию и callbacks вне locks Manager
(M-09/M-10). Сверка gist не изменила последовательность P0; её заменяет
порядок 2026-10-13 выше. Приоритет companion
recovery ниже исправлен на уже действующий P0 из Beads.

### Последовательность P0

С 2026-10-13 эти части имеют приоритет P1 в Beads, кроме работ по
безопасности (безопасные management defaults, исходящая аутентификация,
допуск unsolicited-сессий и границы preview); впереди эпик актуальности
стека `gobfd-qj0.8.6`.

| Часть поставки | Статус |
|---|---|
| Корректность RFC core и учёт потерь | В работе; реализованы Final retry и исключение P/F |
| Reconciliation ownership и конфигурации | В работе; реализованы C01.1--C01.7 |
| Удаление repository-owned shell | В работе в `dev`; ноль tracked `.sh`/`.bash` файлов не означает отсутствие inline shell |
| Безопасные management defaults | В работе; `gobfd-330`, `.8.2.3.2` и `.8.2.3.4` выполнены; `.8.2.3.1` и `.8.2.3.3` открыты |
| Безопасный переход на GoBGP v4 | В работе; клиент v4.10.0, preflight, повторы, метрики, пиры, отключённые оператором, каноническая идентичность, восстановление после разрыва и демпфирование только восстановления выполнены; открыта наблюдаемость демпфирования `.8.2.4.7` |
| Восстановление companion binaries и каноническая идентичность | В работе; каноническая идентичность GoBGP выполнена; HAProxy и ExaBGP `.8.2.5` открыты |
| Независимая проверка реализации | Открыто |
| Interop, scale, security и release qualification | В работе; строгий локальный release-quality gate прошёл в чистом worktree 2026-09-08; на 2026-10-13 vulnerability gate не проходит (`gobfd-qj0.8.2.11`, `gobfd-qj0.8.4`); более широкая interop-, scale- и security-квалификация остаётся открытой |

Первая часть maintainability-работ по release-quality принята: устранены все 85
измеренных strict-lint findings, а закреплённый набор cyclop, funlen и gocognit
теперь сообщает ноль замечаний без ослабления конфигурации.
Последующий полный base profile выявил ещё 151 diagnostic. Все 151 устранены;
закреплённый base profile из 92 линтеров и все 17 build-tag profiles теперь
сообщают ноль замечаний без ослабления конфигурации.
Два ранее существовавших дефекта публикации `GITHUB_PATH`, найденные при review,
устранены как отдельные release blockers в Beads вне lint-refactor.
В текущем дереве `dev` нет tracked Python source или Python manifests;
удаление Python в stable остаётся в `gobfd-qj0.8.2.8.3.5.2`. Обязательный
no-shell/no-Python scope не меняется: Makefile recipes, workflow blocks,
container commands, test invocations и Bash packages всё ещё требуют работы
по удалению shell в `dev`.

Принятый core C01.1 предоставляет канонический ключ сессии, отделённый от
packet demultiplexing, сериализованные типизированные claims конфигурации,
compatibility/API и unsolicited, а также неизменяемую static-auth identity.
C01.2 добавляет проверку полного candidate до создания sender, передачу пустого
desired set и отдельные типизированные owners для base BFD, Micro-BFD, VXLAN и
Geneve. C01.3a добавляет ленивые Manager-owned sender leases для принятых
physical sessions, точное освобождение при последнем claim и shutdown, возврат
API source port и явные non-owning leases для общих overlay и unsolicited
transports. C01.3b добавляет lifecycle Manager Open/Closing/Closed, fail-closed
gates для мутаций и подписок, ожидание зарегистрированных goroutines, точное
закрытие notification channels и sender callbacks после завершения session и
вне locks Manager. Он также охватывает echo reconciliation и CRUD/reconciliation
Micro-BFD groups как одну top-level lifecycle operation. Рекурсивный blocking
Close из synchronous release callback требует явного API design, который
отслеживается в `gobfd-qj0.8.2.2.5.1`; остальные Manager APIs callback может
безопасно вызывать повторно. Он не завершает C01 или SIGHUP reload. C01.4a
добавляет ленивые Manager-owned Echo sender leases, изоляцию API/config
sources, полную preflight-проверку Echo candidate, передачу пустого desired set
и rollback новых принятых Echo sessions при ошибке получения sender. Замена
listeners/backends, стабильные owners отдельных groups/tunnels,
согласование Poll/Final, transport-aware demultiplexing и аутентифицированные
API principals остаются открытыми.
C01.4b сериализует компиляцию/apply startup и SIGHUP, публикует desired и
applied generations, сохраняет ограниченные receipts шести sources и управляет
gRPC readiness пустого service без изменения process readiness systemd. Slice
остаётся нетранзакционным между sources и не добавляет автоматический retry.
C01.5 загружает YAML через уже проверенный descriptor. C01.6 отклоняет
неподдерживаемые GoBGP strategies из одного общего словаря. C01.7 отклоняет
startup-owned изменения SIGHUP до мутации generation или runtime и разрешает
изменения desired-set membership только в пределах открытых при startup
transport bindings; same-key изменения параметров остаются явными
reconciliation conflicts. Runtime wiring socket buffers, неоднозначные объявления
listener interfaces и строгая проверка log vocabulary остаются отдельными
отслеживаемыми задачами.

Overlay-обновление (2026-09-06): задачи `gobfd-qj0.8.2.1.9.1` — `.9.3` охватывают проверку inner
packet, точную tunnel identity и владение портом/отменой операций listener
(C-02/C-03, H-22, M-11 — M-14). Задача `.9.4` добавляет real UDP qualification:
32 захваченных VXLAN/Geneve пакета сопоставлены с 12 принятыми и 20 отклонёнными
wire cases; шесть ограниченных parser fuzz targets с race detector проходят.
Остальные acceptance vectors сохраняются в parser/discriminator regression
tests. Это квалифицирует только проверенный Linux userspace IPv4-профиль, не
production deployment или vendor interop; owner-specific backends остаются
недоступны. Точные команды и checksums evidence сохранены в Beads.

Poll/Final-задача `gobfd-qj0.8.2.1.1.1.1` сохраняет Final до успешной отправки
и исключает одновременные P/F (M-03/H-04). Дочерняя задача
`gobfd-qj0.8.2.1.1.1.2` запускает локальный Poll при переходе из медленного
режима в Up, используя существующие передачи. Задача `.1.1.1.3` принимает
Final только после успешной отправки Poll: ошибки отправки и crossed Final-only
ответы не подтверждают неотправленный Poll. H-05 остаётся частичной: следующая
P0-задача `.1.1.1.4` охватывает очередь изменений, четырёхстороннюю матрицу
таймеров, owner-safe обновления через config, достоверные apply receipts и
FRR/BIRD qualification. Задача `.1.1.1.4.1` фиксирует согласованную политику:
одно активное и последнее ожидающее изменение, отмена без отката уже
объявленных значений. Подробный
[контракт транзакций](../superpowers/specs/2026-09-08-gobfd-timer-update-transactions-design.md)
определяет согласованные правила разделения Final, таймаута и восстановления.
Согласование `.4.1` и реализация `.1.1.1.4.2` завершены: базовые сессии только
с config owner поддерживают ограниченные TX/RX-транзакции и автоматические
completion receipts; локальные race, lint и независимые ревью пройдены.
Этап реальных пиров `.1.1.1.4.3` был заблокирован P0-заменой образа
`gobfd-qj0.8.2.8.5.2.4` до сужения зависимости 2026-10-13 до BASE-этапа
`.8.5.2.4.2.1`: подтверждено, что закреплённый vendor-образ FRR
наследует Alpine. Этап `.8.5.2.4.1` добавляет рецепт Debian FRR 10.7.1 со
smoke запуска/config/остановки на amd64 и двумя форматами SBOM, но не
переключает потребителей. Задача `.8.5.2.4.2` охватывает переключение всех
потребителей и локальную квалификацию пиров. Live-квалификация таймеров
не запускалась. Обновлённый план
Beads переиспользует project-owned выполнение, захват пакетов и JSON receipts
демона, без нового публичного RPC. H-05 остаётся partial до приёмки допустимой
топологии и доказательств на проводе.

Задача `.8.5.2.4.2.1` прошла локальные amd64 gates BASE Compose и
testcontainers с Debian FRR 10.7.1, лимитами сборки/runtime и проверенным
удалением своих ресурсов. Подзадача `.8.5.2.4.2.1.1` добавляет последовательные
ручные сборки с лимитами и `up --no-build`. Доказательства сохранены в
`reports/e2e/frr-base-caps-20260908/`; зависящие от захвата RFC-пропуски явные.
P0-задача `.8.5.2.4.2.2` прошла локальные amd64 gates ручного BGP и
testcontainers: все четыре race-теста каждого пути без пропусков, проверку
лимитов сборки/runtime и удаления своих ресурсов. Полный testcontainers gate
занял 200.07 секунды. Scoped race-контракты, lint, vet, gopls, inventory,
Markdown, рендер всех десяти Compose и независимые SPEC/QUALITY-ревью также
пройдены. Доказательства: `reports/e2e/bgp-trixie-20260908/`.
P0-подзадача `.8.5.2.4.2.2.1` заменяет GoBGP Alpine/scratch и ExaBGP bookworm
рецептами trixie с закреплёнными исходниками. Согласованное исключение для
системного Python только в runtime ExaBGP записано в `AGENTS.md`; Python
tooling репозитория запрещён. Оба рецепта имеют локальные BGP- и SBOM-доказательства.
Полная инвентаризация образов `.8.5.2.1` также выявила Grafana Alpine и
Prometheus BusyBox. Следующие P0-этапы: интеграции/Kubernetes
`.8.5.2.5`, observability `.8.5.2.6`, Containerlab/vendor-границы `.8.5.2.7`,
решение по именованному Oracle release-продукту `.8.5.2.8`. Нельзя обозначать
Debian-артефакт как Oracle Linux или превращать vendor NOS в другую тестируемую
систему. Holo уже использует trixie; download-only scratch stages не содержат
исполняемый runtime. RFC-задача `.8.5.2.4.2.3` прошла полный локальный amd64
testcontainers gate за 117.50 секунды: четыре race-сценария без пропусков,
проверенные лимиты, отсутствие OOM и удаление своих ресурсов. Доказательства
и SBOM пиров: `reports/e2e/rfc-trixie-20260908/`. Приёмка BASE/BGP/RFC не
закрывает задачи образа и таймеров, полный RFC-объём и security-пробелы.
Подзадача BGP fast-failover `.8.5.2.5.1` проходит локальный amd64 race-сценарий
за 142.20 секунды: отзыв/восстановление маршрута, packet evidence, лимиты всех
пяти runtime-контейнеров, отсутствие OOM и точный cleanup. Compose использует
те же trixie-пиры и существующий lifecycle-контроллер с лимитами; ручной
отзыв и восстановление маршрута также проверены локально.
Доказательства и SBOM пиров: `reports/e2e/bgp-failover-trixie-20260908/`.
Родительская `.8.5.2.5` остаётся открытой для ExaBGP anycast `.8.5.2.5.2`,
затем Kubernetes immutable build/import `.8.5.2.5.3`.
Задача `gobfd-qj0.8.2.1.1.2.1` добавляет пересчёт TX deadline по параметрам
пира и подавление передачи при remote Demand/нулевом receive interval с
сохранением Final retry. В родительской `.1.1.2` остаются полные Demand
procedures, оставшийся объём H-08 и FRR/BIRD interop.

Задача `gobfd-qj0.8.2.1.1.2.2` добавляет начальный нулевой receive interval
в общее ядро, YAML обычных базовых сессий и generic API. Пропущенные значения
сохраняют defaults; контракты per-peer overrides preview не изменены.
Историческая матрица 69 ID выше остаётся снимком указанного SHA.
H-08 имеет ограниченную реализацию, не полную квалификацию Poll/Demand.
Реализация `gobfd-qj0.8.2.1.3.5` отклоняет структурно некорректные protobuf
durations в generic, Echo и Micro-BFD запросах с `InvalidArgument`; тот же код
возвращается для клиентских интервалов, отклонённых ядром, и повторяющихся
Micro-BFD members. Пропущенные generic/Micro интервалы сохраняют `1s`, generic
RX zero остаётся допустимым, Echo требует явный положительный интервал.
Классификация внутренних ошибок Manager сохранена. Локальная приёмка на
`20e951ac14e0515eda6698df7c70e7bf9ee77a32` закрывает эту ограниченную задачу;
проверка и применение структурно корректных таймеров Micro-BFD members
остаются в `gobfd-qj0.8.2.2.1` (M09).

Задача `gobfd-qj0.8.2.1.3.3` реализует prerequisite H-09: owner-only commit
receipts AdminDown/7, ограниченные контекстом admission в очередь и ожидание,
one-shot `Run` и Manager fanout вне registry, ownership и lifecycle locks.
Принятое продолжение H-12 добавляет повторы до успешной записи, negotiated
Detection Time notification window и bounded I/O. Фиксированное двухсекундное
окно — историческое поведение. Один commit receipt не квалифицирует доставку.

RFC core продолжается с Poll/Final, обработки remote Demand, оставшейся приёмки
доставки, transport demultiplexing RFC 5881/5883 и fail-closed границ preview.
Публичный padding API/CLI принят локально; core RFC/path-MTU qualification
остаётся отдельной. Принятые core-срезы диагностики, аутентификации и padded PDU
этой очередью не переоткрываются.

Родитель `gobfd-qj0.8.2.1.2` принят локально через
`e4f3761a0f1fc59fbf9b2f859923a81125d157bf`; полная релизная квалификация отдельна.

### Последовательность P1

| Часть поставки | Статус |
|---|---|
| Настраиваемая BFD QoS socket policy с packet evidence | Открыто |
| Измерение committed latency и корректные performance gates | Открыто |
| Удаление постоянного OS-thread pinning сессий с A/B evidence | Открыто |

Post-v1 R&D по scheduler, kernel, warm restart, S-BFD и аутентификации находится
вне этого релизного контракта и отслеживается отдельно в Beads.

[Оценка cilium/ebpf](../superpowers/specs/2026-10-15-ebpf-evaluation.md)
(2026-10-15, ebpf-go v0.22.0) оставляет eBPF вне v1: измеренного узкого
места на приёме пока нет, XDP добавляет BPF-привилегии и конфликты
владения интерфейсом, а библиотека до 1.0 ломала API в v0.21.0 и v0.22.0.
Счётчики потерь сокета берутся из `SO_RXQ_OVFL`. Прототип приёма через
XDP/AF_XDP (`gobfd-376`, backlog P4) ждёт базового замера задержки `.8.2.1.6` и
прототипа `recvmmsg`.

## Релизные контракты

- [Maintenance-дизайн v0.6.2](../superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md)
- [Production-дизайн v1](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md)
- [Матрица соответствия RFC](./08-rfc-compliance.md)
- [Разработка и quality gates](./09-development.md)
