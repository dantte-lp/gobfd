# Дорожная карта GoBFD

![Текущий релиз](https://img.shields.io/badge/Current-v0.6.4-1a73e8?style=for-the-badge)
![Следующий релиз](https://img.shields.io/badge/Next-TBD-34a853?style=for-the-badge)
![Цель](https://img.shields.io/badge/Target-v1.0.0-ea4335?style=for-the-badge)

> Проекция состояния Beads, сверенная 2026-09-08. Реестр задач находится в
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
(M-09/M-10). Текущая последовательность P0 не изменяется; приоритет companion
recovery ниже исправлен на уже действующий P0 из Beads.

### Последовательность P0

| Часть поставки | Статус |
|---|---|
| Корректность RFC core и учёт потерь | В работе; реализованы Final retry и исключение P/F |
| Reconciliation ownership и конфигурации | В работе; реализованы C01.1--C01.7 |
| Удаление repository-owned shell | В работе в `dev`; ноль tracked `.sh`/`.bash` файлов не означает отсутствие inline shell |
| Безопасные management defaults | Открыто |
| Безопасный переход на GoBGP v4 | Открыто |
| Восстановление companion binaries и каноническая идентичность | Открыто |
| Независимая проверка реализации | Открыто |
| Interop, scale, security и release qualification | В работе; строгий локальный release-quality gate прошёл в чистом worktree, более широкая interop-, scale- и security-квалификация остаётся открытой |

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
Этап реальных пиров `.1.1.1.4.3` заблокирован P0-заменой образа
`gobfd-qj0.8.2.8.5.2.4`: подтверждено, что закреплённый vendor-образ FRR
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
Задача `gobfd-qj0.8.2.1.1.2.1` добавляет пересчёт TX deadline по параметрам
пира и подавление передачи при remote Demand/нулевом receive interval с
сохранением Final retry. В родительской `.1.1.2` остаются полные Demand
procedures, оставшийся объём H-08 и FRR/BIRD interop.

Задача `gobfd-qj0.8.2.1.1.2.2` добавляет начальный нулевой receive interval
в общее ядро, YAML обычных базовых сессий и generic API. Пропущенные значения
сохраняют defaults; контракты per-peer overrides preview не изменены.
Историческая матрица 69 ID выше остаётся снимком указанного SHA.
H-08 имеет ограниченную реализацию, не полную квалификацию Poll/Demand.
Прежняя ошибка классификации API вынесена в P2 `gobfd-qj0.8.2.1.3.5`:
недопустимые receive intervals отклоняются, но могут возвращать `Internal`
вместо `InvalidArgument`.

RFC core продолжается с отслеживаемых пробелов Poll/Final и Demand procedures,
диагностик и сброса аутентификации, атомарной доставки BFD/AdminDown,
transport demultiplexing RFC 5881/5883, authenticated padding RFC 9764 и
fail-closed границ preview-возможностей.

### Последовательность P1

| Часть поставки | Статус |
|---|---|
| Настраиваемая BFD QoS socket policy с packet evidence | Открыто |
| Измерение committed latency и корректные performance gates | Открыто |
| Удаление постоянного OS-thread pinning сессий с A/B evidence | Открыто |

Post-v1 R&D по scheduler, kernel, warm restart, S-BFD и аутентификации находится
вне этого релизного контракта и отслеживается отдельно в Beads.

## Релизные контракты

- [Maintenance-дизайн v0.6.2](../superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md)
- [Production-дизайн v1](../superpowers/specs/2026-08-18-gobfd-v1-production-contract-design.md)
- [Матрица соответствия RFC](./08-rfc-compliance.md)
- [Разработка и quality gates](./09-development.md)
