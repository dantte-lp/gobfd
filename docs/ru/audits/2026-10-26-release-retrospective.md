# Ретроспектива выпуска v1 и оценка трудозатрат — 2026-10-26

Производная публикации; оригинал сохранён в Git-срезе `c5bcbfcb7e058a5a7f22b8a6811c8e4250963d50`, blob `257da081eaf44d463535aa46b22a30bb2536cd6c`. Удалены только внутренние идентификаторы и служебная привязка владельцев; результаты, статусы, оценки и ограничения относятся к исходной дате. [Английская версия](../../en/audits/2026-10-26-release-retrospective.md).

**Итог:** выполнено; сверка выявила незавершённую разработку, уточнила критический путь и дала оценку готовности v1 за 4–6 суток.
**От вас:** не требуется

## Сделано

Сравнены [аудит 23 октября](2026-10-23-independent-retrospective.md) и локальный
`dev@1c535f9ac1de7a12f7d85054a9d0fe81216f02c0`.
Предыдущий аудит проверял `f9ed31bdd81425ba4efaf9688bb452ddea142266`.
Область выпуска определяют [production contract](../reference/production-contract.md)
и принятые поправки. Исполнение и критерии приёмки учитываются отдельно. Документ фиксирует состояние на дату и не разрешает
публикацию исходников, Dolt, Actions или релиза.

Уточнение: запрос отдельного профиля YC снят. Программа runner создаёт только
ресурсы Proxmox в существующем VLAN 101; инвентаризация YC появилась из общего
greenfield-аудита сети. Его применимость относится к инфраструктурной задаче
и не доказывает отсутствия пользовательских credentials. Существующие
Teleport/PVE и доставка образа остаются путём provisioning; уточнение не
означает готовность VM или завершение её предварительных проверок.

### Сравнение с предыдущей ретроспективой

Шесть подтверждённых дефектов от 23 октября исправлены в принятых локальных
исходниках:

| Предыдущее нарушение | Текущая граница реализации | Историческая запись |
|:---|:---|:---|
| TLS identity могла получить привилегии Unix UID | Типизированный transport principal разделяет Unix, TLS URI и TLS DNS | закрыта |
| Unsafe loopback mutation policy пропущена в startup-only контракте reload | Startup contract включает `grpc.unsafe_loopback_mutations` | закрыта |
| Повтор Echo Add и cross-owner Delete | Канонический admission и claims transport principal | закрыта |
| NetworkManager мог выбрать профиль другого bond | Проверяются active/cached profile и владелец controller | закрыта |
| Чтения свойств NetworkManager обходили context | Используются `CallWithContext` и ограниченная настройка соединения | закрыта |
| Proto guard пропускал untracked generated-файлы | Git status включает untracked под `pkg/bfdpb` | закрыта |

Квалификация same-key replacement при незавершённом старом drain остаётся
открытой как отдельная квалификация same-key replacement. Timer addendum теперь явно отменяет прежнее
ограничение promotion только состоянием Up. Эти результаты не означают
окончательную wire-приёмку.

После предыдущей проверенной ревизии добавлено 79 коммитов, включая два merge.
Разность конечных деревьев: 386 файлов, +41 665/−8 562 строки:

| Область | Файлов изменено | Добавлено | Удалено |
|:---|---:|---:|---:|
| Runtime/API без тестов и generated | 59 | 3 341 | 581 |
| Тесты, инструменты, scripts и CI | 240 | 25 608 | 887 |
| Документация и данные под docs | 81 | 12 095 | 7 080 |
| Generated | 2 | 603 | 0 |
| Остальное | 4 | 18 | 14 |

Это разность деревьев, не личная производительность и не мера готовности.
В репозитории 698 отслеживаемых Go-файлов. Проверены покрытие контракта и
выбранные границы реализации/потребителей; новый семантический аудит каждого
тела функции и повтор всей матрицы тестов не выполнялись.

Появились actual UDP owner/peer control, сверка terminal packets и генераторы
CPU/GC/scheduler pressure и churn. Добавлены фактические пропуски peer,
монотонные данные о commit Down и скалярная привязка поколения. Engineering
blackhole теперь имеет период 10 минут: два плановых окна внутри измеряемых
30 минут. До исправления равные 30-минутные длительность и период не давали
ни одного такого окна. Это изменения исходников и fixtures, а не завершённая
нативная нагрузочная квалификация.

Начальный адаптер Smallstep renewal и проверка с реальным provider приняты
локально. Initial enrollment и unattended renewal остаются отдельными;
ретроспектива не добавляет их в критический путь стабильного выпуска.

### План в сравнении с кодом

| Область | Подтверждённое текущее состояние | Остаток до выпуска |
|:---|:---|:---|
| Протокол, владение и управление | RFC/timer, claims, reload, principal и preview-границы реализованы с отдельными receipts | Packet/reload/cross-consumer/failure-квалификация одного окончательного кандидата |
| Классификация Down | Реализованы timing, фактические окна пропусков и slot/generation | Историческая идентичность валидного пропущенного пакета, provenance прогона/часов, независимая классификация и actual execution |
| Receive/timer qualification | Есть actual UDP и условные данные часов; общая неопределённость остаётся неизвестной | Распределения с проверкой валидности, численный pass/fail и допущенный reference-прогон |
| Масштабирование потоков | Control и Echo вызывают `runtime.LockOSThread` | Удалить постоянное закрепление с native A/B на 1000 сессий; 1000 Control-потоков не укладываются в предел 48 |
| Socket QoS | Нет конфигурации и реализации DSCP/Traffic Class/SO_PRIORITY/SO_MARK обычного BFD | Реализовать согласованную политику и packet/failure-проверки IPv4/IPv6 |
| Micro-BFD dispatch | Nil/JSON logging дают 0 allocations; noop actuator сохраняет 4 allocations/272 B | Выполнить строгий критерий без нарушения per-call timeout, cancellation и времени жизни context |
| Benchmark verdict | `BenchmarkReport` возвращает `writeBenchmarkWarnings` | Калиброванные критические регрессии должны давать ненулевой exit с сохранением отчётов |
| Небольшие дефекты поставки | Fresh-empty alert использует `sum(gobfd_bfd_sessions) == 0`; пять aggregate E2E wrappers используют host Go; dev/debug images устанавливают Bash | Исправить обязательные alert/launcher/zero-shell границы с сохранением потребителей |
| OCI и integration | Общие trixie recipes и часть consumers приняты; Containerlab candidate `4852538df409e0b0309b89505b6dc88861a49540` остаётся изолированным | Native FRR lifecycle/wire assertions, Kubernetes immutable build/import, архитектуры и явные vendor OS-policy dispositions |
| Runner | Исправление datastore в Pulumi source/operator принято в um-iac; VM отсутствует | Существующий Teleport/PVE, применимая предварительная проверка, управляемая доставка Debian image, preview/apply и guest admission |
| Финальный выпуск | Есть локальные данные отдельных изменений | Окончательные security/inventory, package upgrade/rollback, обязательные quality/review, engineering и 24-часовой reference |
| Внешние по отношению к v1-дереву security/failure owners | Recipes сохраняют base из исторического сканирования с исправляемым liblzma5; intermittent race-run failure не атрибутирован | Fresh-candidate disposition/remediation в security disposition базового образа и диагностике intermittent race failure; исторические finding не объявляются свежими измерениями |

Проверенные входы: `internal/bfd/session_loop.go`, `internal/bfd/echo.go`,
`internal/bfd/manager_micro.go`, `internal/server/principal.go`,
`internal/server/echo.go`, `internal/netio/lag_networkmanager.go`,
`cmd/gobfd/startup_contract.go`, `test/internal/cirunner/proto.go`,
`test/internal/cirunner/benchmark_report.go`, `test/internal/bfdload/`,
`Makefile`, `deployments/docker/Containerfile.dev`,
`deployments/docker/Containerfile.debug` и
`deployments/integrations/observability/configs/prometheus/alert-rules.yml`.

Исторический срез исполнения построен по реальным связям `parent` в иерархии работ v1, до
закрытия задачи самой ретроспективы. Включены отложенные работы и parents
приёмки.

| Показатель | 23 октября | 26 октября |
|:---|---:|---:|
| Потомков | 423 | 523 |
| Закрытых потомков | 293 | 384 |
| Незакрытых потомков | 130 | 139 |
| Листовых задач | 322 | 392 |
| Незакрытых листьев | 84 | 75 |
| Незакрытые листья по приоритетам P0/P1/P2/P3 | 8/51/24/1 | 7/41/26/1 |

Добавлено 100 записей задач, число закрытых выросло на 91; открытых листьев
стало на девять меньше. Процент готовности из этого не следует: parents,
разработка, тесты, housekeeping и post-v1 имеют разную стоимость. Открытый
parent не обязательно означает отсутствие реализации; закрытые дети не
доказывают составную приёмку parent.

У release owner не было прямых blocking edges к восьми обязательным owners:
QoS, thread pinning, latency qualification, benchmark regression exit,
Micro-BFD allocations, OCI migration, fresh-empty alert и containerized E2E
launchers. Эти зависимости добавлены в окончательную квалификацию выпуска. Подготовка alpha переведена в open/P2 и не является условием stable.
Два дополнительных обязательных внешних owner — диагностика intermittent race failure и security disposition базового образа —
также стали прямыми release blockers. Они включены в H; отсутствие в
подсчитанном v1-дереве не переводит их в post-v1.

### Инженерная оценка

Диапазоны ниже — оценки трудозатрат, а не измеренная длительность исполнения.
Каждая строка включает реализацию, сфокусированные тесты, документацию и
review. Оценки parent и его детей не складываются.

| Пакет работ | Активных часов |
|:---|---:|
| A: завершить UDP/clock/causal verdict одним сквозным результатом | 10–16 |
| B: убрать постоянное закрепление потоков и разобрать native A/B | 3–5 |
| C: реализовать socket QoS и packet checks | 4–6 |
| D: завершить bounded actuator allocation | 3–5 |
| E: critical regression, alert, launchers и zero-shell | 4–7 |
| F: закрыть OCI consumers и финальную interop/wire-квалификацию | 8–12 |
| G: Pulumi runner и guest admission после G0 | 2–4 |
| H: финальные security/base и intermittent-failure disposition, packages, upgrade/rollback, review и docs | 6–10 |
| Активная инженерная работа, всего | **40–65** |
| Обязательный reference-прогон | **24 часа elapsed** |

Без переделок последовательный диапазон составляет 64–89 часов. Часть
integration-работ может идти параллельно, но квалификация требует кода после
исправлений. **Рабочий ориентир — 4–6 календарных суток непрерывной работы
от 26 октября, примерно 21:00 МСК: 30 октября–1 ноября 2026 года.** Включены
резерв интеграции и возможность одного повторного 24-часового прогона. Это
прогноз локальной готовности, не обязательство публикации. Уверенность
умеренная или низкая до первого допущенного baseline на 1000 сессий.

Критический путь: завершить A и пробелы продукта; допустить G; получить короткий
native baseline/A-B и engineering evidence; зафиксировать кандидата; выполнить
24-часовой reference и финальные supported wire/security/package gates;
свести приёмку выпуска. Изменение кода, нарушающее применимость доказательств,
перезапускает соответствующую проверку. Первая контрольная точка — завершение
реализации в пределах 48 часов; нарушение порогов меняет оценку конкретной
строки и остатка.

Внешнее ожидание не включено в активные часы и прибавляется к календарному
сроку: доступ Teleport/PVE, доступность image/network, необходимые
native architecture resources и решения vendor licensing/OS-policy.
Действующий запрет публикации снимается отдельно после готовности конкретного
кандидата; разрешение на публикацию сейчас не запрашивается.

Детерминированные случайные потери могут вызвать корректное по RFC истечение
таймера вне blackhole. Классификатор обязан показать такой результат, не
переименовывая его в intentional. Неуспешный resource/latency/false-Down gate
может потребовать исправления и полного повтора. Clock uncertainty и оставшиеся
actuator allocations — технические риски квалификации UDP/clock/causal и allocations actuator, не принятые исключения.

### Причины задержки и корректировка процесса

Утверждение «остались только 24 часа и wire» было неверным. Аудит 23 октября
уже сохранял блокеры реализации и квалификации. Отдельные source-приёмки
сообщались без сводной взвешенной оценки выпуска. Малые producer-этапы и
поздние lint-замечания добавляли повторную проверку; исторические receipts
не квалифицировали окончательного кандидата.

Исполнение остаётся за назначенными техническими владельцами. A завершается одним сквозным
результатом с минимально необходимыми входными данными. Static checks идут
перед дорогими критическими тестами; повторы нужны при изменении поведения или
незакрытых findings. Общая свежесть зависимостей, широкие Q5 duplicate/wrapper
inventories, preview graduation, R&D и unattended certificate enrollment не
входят в stable critical path по принятым поправкам. Обязательные security,
quality, OCI, wire и scale gates сохраняются.

## Успешно

Привязка поколения завершена во время сверки:
`1c535f9ac1de7a12f7d85054a9d0fe81216f02c0`, 14 файлов +538/−50.
Это данные листовой приёмки, а не приёмка выпуска.

| Проверка | Результат | Доказательство |
|:---|:---|:---|
| Критические race-тесты identity leaf | 330 named PASS, 0 FAIL/SKIP | `down-incarnation-final01-tests.json` |
| Scoped static identity leaf | 92 линтера, 0 issues; root/scoped vet, tidy, gopls, CLI build PASS | `down-incarnation-final01-*` |
| Документация до новой ретроспективы | 102 MD, 1108 links без ошибок, 34 EN/RU pairs | `down-incarnation-final01-*` |
| Сопоставление записей исполнения | 523 descendants, реальные parent-связи | `retro-20261026-issues.json`, `retro-20261026-counts.json` |
| Разность исходников после аудита | 386 файлов, +41 665/−8 562 | `retro-20261026-diff-stat.tsv` |
| Очистка identity leaf | Owned SDK/cache и чистый интегрированный worktree удалены; branch/backup refs/reports сохранены | `down-incarnation-review.md`, cleanup receipts |

Evidence находятся под `reports/clab-frr-owned-20261025/`.
Новые runtime и длительные тесты ради этой ретроспективы не запускались.

## Неуспешно

| № | Что | Причина | Статус | Дальше |
|:---|:---|:---|:---|:---|
| Н1 | Создание reference VM | Остаются PVE preview, доставка image и проверка применимости инфраструктурного аудита | задача | допуск reference runner: завершить предварительные проверки и guest admission |
| Н2 | Готовность v1.0.0 | Остались реализации и квалификации A–H, 24h не запускался | задача | окончательная квалификация выпуска: доказательства окончательного кандидата по всем обязательным gates |

## Ошибки и предупреждения

| № | Источник | Суть | Статус | Дальше |
|:---|:---|:---|:---|:---|
| О1 | Предыдущая оценка остатка | «Остались только два прогона» не соответствовало предыдущему аудиту и коду | исправлено | Сверка и диапазон A–H записаны в окончательной квалификации выпуска |
| О2 | Release dependency graph | Не было восьми прямых обязательных dependencies | исправлено | Blocking edges добавлены к окончательной квалификации выпуска |
| О3 | Финальный symbol-level scan identity leaf | 0 reachable/imported, но 2 module-only finding остаются | задача | security disposition окончательного кандидата: final-candidate disposition, без расширения исключений |
| О4 | Календарный прогноз | Зависит от native baseline, внешних ресурсов и результатов пороговых проверок | принято | Резерв включён; внешнее ожидание учитывается отдельно и не скрывается |

Полный семантический аудит всех функций и финальная runtime/wire/security
приёмка не выполнены. Основные риски срока — clock/causal qualification,
actuator allocations, доступ к runner и повтор 24-часового прогона.
