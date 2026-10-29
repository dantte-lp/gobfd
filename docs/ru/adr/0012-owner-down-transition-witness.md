# ADR-0012: Монотонные данные owner о переходе в Down

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-26 |
| Изменение Echo proof | 2026-10-29 |
| Изменение выборочного Control proof | 2026-10-29 |
| Изменение независимых свидетельств истечения peer | 2026-10-29 |

## Контекст

Directional Detection Time и actual owner transition нужны для оценки intentional blackhole. Unix-time LastPacketReceived и notification timestamp после logging/metrics не ограничивают commit. Serialization удаляет Go monotonic time; wall-clock conversion не создаёт отсутствующий witness.

## Решение

Optional value witness фиксируется только для actual Up→Down. Под existing observation mutex сохраняются last validated owner receive time, прежний negotiated D и actual dispatched detection deadline до применения pending RX при timer interruption. Порядок interruption сохраняется; два monotonic timestamps ограничивают `commitObservation`. Witness связывается с returned ObservationVersion.

Publication копирует witness только для exact version. Missing, stripped, inconsistent/mismatched evidence остаётся unknown. Bracket содержит local observation commit; logging/delivery/actuator delays следуют после него. ValidatedAt — owner processing после validation, а не kernel arrival либо конец processing.

| Evidence | Правило хранения | Граница |
|:---|:---|:---|
| Down witness | Frozen cause, remote facts, endpoint, discriminator, version | Без SessionRef в lifetime array |
| Relative phase | Вычислить до потери monotonic time.Time, сохранить offsets/availability | Unknown остаётся unknown |
| Source journal | Все delivered witnesses через setup/drain | down_events/cutoff сохраняют notification-time meaning |
| Remote discriminator | Старый scalar до timeout reset; ValidatedRemoteDiscriminator | Ноль — unknown; protocol reset order прежний |

Источники: [Control commit](../../../internal/bfd/session_observation.go), [timer handler](../../../internal/bfd/session_loop.go), [witness projection](../../../test/internal/bfdload/down_witness_linux.go), [RFC 5880 §6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4).

### Incarnation identity

Row связан с fixed manifest slot, generation и полным endpoint, с known/error. Inventory seed снимается после subscription до churn. Сопоставляются captured physical reference, version, tuple, type, interface, discriminator; delayed notification использует captured identity.

Только matching Deleted→Added увеличивает generation. Repeated Added прежней active identity idempotent. Missing seed, GAP, unmatched lifecycle, inconsistent history либо overflow необратимо нарушают identity; последующий snapshot её не восстанавливает. Active refs удаляются при deletion/invalidation/collector stop. Manifest/active table ограничены session count; lifetime rows содержат scalars без нового core ID/per-packet observer.

`incarnations_complete` требует seeded uninterrupted history и normal closure; `incarnation_error` независим от source-count `complete`. Classification требует оба признака и known identity каждого eligible row. Источник: [incarnation tracking](../../../test/internal/bfdload/down_incarnation_linux.go).

### Conditional Control classification

Actual direction-specific blackhole windows содержат wire-identity segments, omission offsets и decision sequences. TX valid означает codec verification несостоявшегося packet; RX discard предшествует acceptance; отдельный `receive_qualified` фиксирует проверки codec/tuple/TTL и протокольного принятия на копии состояния peer. Фактическое состояние получения и disposition не меняются. Packet/auth slices/retired sessions не сохраняются. Bounds: 256 windows/peer, 16 segments и 128 combined directional omission times/window. Overflow увеличивает omitted evidence и запрещает completion, включая cancellation races.

Reports связываются с actual boot ID, private control-directory device/inode, native start и time namespace. Offset x с bracket B означает [x,x+B]; commit — [commit_start,commit_end+Bowner]. Используются checked arithmetic и bracket_known, включая measured zero. Пересечение boundaries остаётся unresolved; delivery time не задаёт measurement denominator.

Требуются settled source counts, scalar incarnation history, complete omissions и consistent TX schedule/write results/RX dispositions. Directional omission sequence возрастает между segments/windows и ограничена actual Scheduled/Read totals; interwindow gaps допустимы. Invalid arithmetic/accounting оставляет Down unresolved.

Intentional verdict требует timeout cause, prior remote Up, old actual deadline, commit целиком внутри measurement/fault window и matching valid Up packets. Восстанавливаются только зафиксированные would-be Up packets в точке доставки владельцу; остальные recorded dispositions, включая RX omissions и удержанные expired Down packets, сохраняются. Такое выборочное вмешательство не воспроизводит peer generator с изменённым transmit decision и не устраняет весь физический отказ.

Восстанавливаемые Up packets требуют codec validity, ожидаемых peer/owner discriminators, diagnostic None, сброшенных Poll/Final и неизменных workload DesiredMinTX, RequiredMinRX и DetectMult. Old D совпадает с этими wire inputs и fixed workload RX. До положительного вывода проверяются body и expected identity каждого TX packet, потенциально попадающего в интервал. Удержанный Down поддерживается только с ожидаемым peer discriminator, нулевым YourDiscriminator, diagnostic ControlTimeExpired, сброшенным Final, DesiredMinTX равным `max(workload_interval,1s)`, Poll установленным только при `workload_interval<1s` и неизменными RequiredMinRX/DetectMult. Он остаётся удержанным; его временной интервал не ограничивает доказательство по восстановленным Up, включая пересечение old due. Неподдерживаемые bodies, states, identities и Up после Down запрещают положительный вывод.

Для непустого набора выбранных Up каждый actual offset p удовлетворяет `p>r+owner_bracket` и `p+peer_bracket<d`, где r — последний validated receive, d — old actual deadline. Требуется checked `max(p)+D>d+owner_bracket`. При явном допущении завершения owner acceptance и detection Reset до old due время сброса s следует после p, поэтому `s+D>=max(p)+D>d+owner_bracket`. Новый deadline строго позже любого old due, допустимого bracket. Количество и номинальная частота пакетов не заменяют доказательство; допуск на gap и предполагаемое расписание не вводятся. Полный учёт actual decisions обязателен.

Reused discriminator, unknown phase/identity, boundary overlap, отсутствие progress и peer-originated Down вне модели первого истечения остаются unresolved. Одна omission/window overlap недостаточна. Дополнительная known terminal loss, counter reset либо отсутствие commit progress запрещают positive attribution; aggregate loss counters не задают exact fault time.

Positive attribution предполагает доставку выбранных пакетов до owner acceptance и завершение detection Reset до old due при отсутствии independent hidden fault. Выборочное вмешательство и эти допущения сериализуются. Вывод относится к старому deadline, а не отсутствию последующих Down или Down к моменту задержанного actual commit. Passing относится только к conditional model; false/unresolved eligible Down запрещает pass. Total uncertainty unknown, reference_qualified false. Fixture-only TX-only blackhole проверяет predicate отдельно; named profiles запрещают selector.

Поправка от 2026-10-29 заменяет прежнюю политику multiplier-count и nominal cadence. Оценки по прежней политике сохраняются. Повторная оценка записывается отдельно с original producer SHA и new assessor SHA; изменение проверяющего кода не доказывает native acceptance.

Источники: [classifier](../../../test/internal/bfdload/down_classification_linux.go), [regressions](../../../test/internal/bfdload/down_classification_linux_test.go).

### Независимое свидетельство истечения таймера peer

Каждое фактическое истечение detection timer эталонного peer сохраняется в `detection_expiry_events`, включая истечения в уже установленном Down. ID равен порядковому номеру в lifetime-счётчике `detection_expirations`. Прежнее состояние, локальный и прежний удалённый discriminators, последнее принятое получение и фактический deadline копируются до очистки удалённой идентичности в `expire`. Время истечения соответствует фактической проверке обработчика; задержка обработчика не сдвигает deadline. Наблюдение следует [RFC 5880 §6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4) и не изменяет FSM peer.

Только Init/Up→Down создаёт ожидание свидетельства первой записи Down. Каждый принятый вход и следующее фактическое истечение сбрасывают ожидание, даже если принятый вход оставляет состояние Down. Random/blackhole omissions и неуспешные/отменённые отправки не подтверждают успешную запись. Первая успешная запись в сокет сохраняет скалярный Control body, lifetime-номер `scheduled`, время решения и отдельные timestamps вокруг `SendPacket`. Интервал записи не доказывает доставку или принятие владельцем. Последующие записи его не заменяют; журнал на каждый пакет не добавляется.

После завершения workers сохранённые монотонные timestamps проецируются относительно окончательно принятой общей фазы, включая отрицательные setup offsets. Без общей фазы отсчёт ведётся от запуска группы генератора, `phase_known=false`. `timing_known` требует монотонных показаний и порядка receive, deadline, expiry. Время записи независимо проверяется по `decision ≤ start ≤ end`; причинный порядок между истечением и записью эта проверка не устанавливает. Неизвестное время истечения не удаляет известный интервал записи. Отсутствующие, лишённые монотонной части или обратные timestamps остаются unknown. Лимит — 256 истечений на peer; переполнение увеличивает `detection_expiry_events_omitted`, возвращает ошибку и запрещает completion даже при последующей отмене. Это предел количества записей, не измерение RSS.

Журнал сохраняет только исходные свидетельства. RemoteDown требует описанного ниже доказательства первого истечения; RemoteAdminDown и неоднозначный источник из последующих истечений остаются unresolved. Повторная оценка прежних отчётов не может добавить отсутствующие записи.

Источники: [журнал истечений peer](../../../test/internal/bfdload/peer_expiry_linux.go), [worker](../../../test/internal/bfdload/peer_worker_linux.go), [критические регрессии](../../../test/internal/bfdload/peer_expiry_linux_test.go).

### Происхождение RemoteDown от первого истечения

Сегменты Control RX blackhole сохраняют `receive_qualified` независимо от TX `valid`. Квалификация проверяет фактический codec status, tuple, TTL и успешный `receive` на копии текущего состояния независимого peer. Признак входит в identity сегмента; для TX/Echo он запрещён. Существующие decision sequences и лимиты журнала обязательны. Квалификация не учитывает пакет как принятый и не обновляет фактический таймер peer.

Положительный RemoteDown требует совпадающих owner incarnation, validated receive и commit, ожидаемого discriminator peer, локального NeighborDown и удалённых Down/ControlTimeExpired. Журнал истечений должен быть полным и ограниченным, с последовательными ID, известными фазой/временем и упорядоченными receive/deadline/expiry. Допускается только ID 1 с прежним Up и текущим discriminator владельца. До первого истечения независимый peer не отправлял diagnostic Expired. Проверка каждого последующего истечения должна строго следовать после всего интервала получения владельцем; иначе одинаковый задержанный Down имеет неоднозначный источник. Выбор ближайшего expiry и оценка задержки транспорта эту неоднозначность не устраняют.

Тело первой успешной записи Down проверяется по идентичности истёкшего peer, нулевому YourDiscriminator, нижней границе slow TX, RX/multiplier нагрузки и ожидаемым Poll/Final. Требуются `expiry ≤ decision ≤ write_start ≤ write_end` и checked `write_start+Bpeer < owner_validated`. UDP-приём может предшествовать возврату из вызова записи. Старый due peer и весь интервал проверки expiry должны лежать внутри одного фактического RX-blackhole и measurement. Owner commit может следовать после конца blackhole из-за удержанной или задержанной отправки Down.

Номер успешной записи `scheduled` учитывает TX attempts за всё время работы; omission sequences учитывают решения внутри фазы. Отправки при setup дают разные системы отсчёта. Для каждого TX omission строго до решения о записи требуется `omission_sequence < write_scheduled`: фазовый префикс задаёт нижнюю границу, поскольку lifetime attempts включают и setup. Фазовый номер самой записи этим не восстанавливается. Совпадающий номер у последующего omission не является конфликтом и не отклоняется. Порядок устанавливается offsets одного peer; оценка задержки транспорта не добавляется.

Пусть r — последнее принятое получение peer, d — его старый due, `D=d-r=interval×DetectMult`. Восстанавливаются только фактические квалифицированные matching stable Up RX inputs из этого blackhole с неизменными intervals/multiplier, diagnostic None и сброшенными Poll/Final. Проверяется body каждого потенциально выбранного входа. Для каждого p требуются checked `p>r+Bpeer`, `p+Bpeer<d` и непустой набор с `max(p)+D>d+Bpeer`. При явном допущении принятия peer и Reset до старого due время Reset s следует после p, поэтому `s+D≥max(p)+D>d+Bpeer`. Все остальные dispositions сохраняются.

Вывод относится к происхождению через первый старый deadline peer. Он не устанавливает отсутствие последующих peer/owner Down и полную неопределённость времени. Неполные или переполненные свидетельства, независимые наблюдаемые потери, неподдерживаемый вход и неоднозначные последующие epochs остаются unresolved. Проверка журнала истечений применяется только в этой RemoteDown-ветке; прежние local-timeout verdicts сохраняют свои правила свидетельств. Любой unresolved eligible Down запрещает passing; reference qualification остаётся false.

Источники: [классификатор первого истечения](../../../test/internal/bfdload/remote_down_linux.go), [критические контроли](../../../test/internal/bfdload/remote_down_linux_test.go), [RFC 5880 §6.8.4](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.4).

### Echo owner witness

TransitionTiming используется под Echo observation mutex/version fence для actual Up→Down. ValidatedAt означает last return, обработанный preview owner, без заявления RFC 9747 validation/authentication. D задаётся локально. Копируется actual TimerEchoDetection deadline; direct handler без dispatch оставляет его unknown. Remote discriminator нулевой, remote state unknown.

Capture не добавляет callback/wire field/observer и сохраняет Echo FSM, forwarding, receiver и scheduling policy. Control remote-Up/discriminator predicates не квалифицируют Echo. Actual Echo UDP workload требует независимых returns и typed incarnation identity.

Local consolidation instruction от 2026-10-28 отдельно разрешает integration с удалением permanent Control/Echo owner thread pins. Witness не квалифицирует это изменение. Native 1000-session Control/Echo A/B, resource/timer criteria и final stable gates обязательны. PID headroom ADR-0007 и pressure-worker pin для CLOCK_THREAD_CPUTIME_ID сохраняются. Comparable secured inputs связываются при actual runner admission; documentation-only commits не требуют новых пар.

Источники: [Echo owner](../../../internal/bfd/echo.go), [Echo observations](../../../internal/bfd/echo_observation.go), [Echo workload](../../../test/internal/bfdload/owner_echo_linux.go).

### Echo old-deadline classification

Echo требует checked `D=interval*detect_multiplier`, actual old deadline d, last accepted return r, matching incarnation/version и diagnostic EchoFailed. Remote Control state/discriminator unknown.

RX/return-TX omissions объединяются по actual received_sequences, а не отдельным decision counters. Read indices уникальны/возрастают между windows, input times не убывают. Каждый выбранный вход должен совпадать с owner wire generation; выбранные read indices последовательны и непусты. Каждый p удовлетворяет `p>r+owner_bracket` и `p+peer_bracket<d`. Полный commit bracket следует после d внутри того же window. Journal bounds прежние; сортировка bounded references не меняет reports.

Каждый положительный вывод требует checked `max(p)+D>d+owner_bracket`. Counterfactual owner Reset во время s следует после выбранного входа, поэтому `s+D>=max(p)+D>d+owner_bracket`: новый deadline строго позже любого old due, допустимого bracket. DetectMultiplier уже входит в D; количество пропущенных входов не заменяет интервальное доказательство. ValidatedAt предшествует Reset, поэтому равенство `d=r+D` не предполагается.

Явное assumption: выбранные withheld inputs вернулись бы, были приняты владельцем и сбросили detection до old deadline. Одного прибытия в socket для этого недостаточно. Вывод относится к этому deadline, а не отсутствию всех later counterfactual timeouts. Оба режима используют actual inputs вместо предполагаемого непрерывного TX schedule; packet validation и omission identities различаются.

Missing/replayed read indices, boundary intersection, отсутствие строгого продления, arithmetic overflow, mismatch wire inputs и independent observed losses остаются unresolved и делают Echo assessment incomplete. Проверяйте каждый выбранный вход до принятия доказательства. Peer kernel drops/invalid samples блокируют qualification; unphased lifetime counters не относятся к произвольному window. Software returner не квалифицирует forwarding plane либо RFC 9747 authentication.

Источники: [Echo omission model](../../../test/internal/bfdload/echo_omission_linux.go), [classification tests](../../../test/internal/bfdload/echo_classification_linux_test.go), [RFC 9747 §2](https://www.rfc-editor.org/rfc/rfc9747.html#section-2).

## Последствия

Witness storage ограничено на session/notification. Capture использует два clock reads и mutex update на actual Down без per-packet callback/wire change. Source acceptance требует allocation/notification-size measurements; решение не заявляет RSS/latency improvement.

Witness сам по себе не causal verdict. Independent peer omissions, incarnation identity, phase brackets и conditional validation обязательны. Native environment/clocks, total kernel uncertainty, reference/engineering workloads, final wire и 24-hour release qualification отдельны.

## Источники

- [Workload phase/accounting contract](0007-workload-phase-accounting.md).
- [Witness tests](../../../test/internal/bfdload/down_witness_linux_test.go).
- [Incarnation tests](../../../test/internal/bfdload/down_incarnation_linux_test.go).
- [Echo assessment tests](../../../test/internal/bfdload/assessment_echo_linux_test.go).
