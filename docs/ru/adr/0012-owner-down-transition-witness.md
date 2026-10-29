# ADR-0012: Монотонные данные owner о переходе в Down

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-26 |
| Изменение Echo proof | 2026-10-29 |

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

Actual direction-specific blackhole windows содержат wire-identity segments, omission offsets и decision sequences. TX valid означает codec verification несостоявшегося packet; RX discard предшествует acceptance и не квалифицирован. Packet/auth slices/retired sessions не сохраняются. Bounds: 256 windows/peer, 16 segments и 128 combined directional omission times/window. Overflow увеличивает omitted evidence и запрещает completion, включая cancellation races.

Reports связываются с actual boot ID, private control-directory device/inode, native start и time namespace. Offset x с bracket B означает [x,x+B]; commit — [commit_start,commit_end+Bowner]. Используются checked arithmetic и bracket_known, включая measured zero. Пересечение boundaries остаётся unresolved; delivery time не задаёт measurement denominator.

Требуются settled source counts, scalar incarnation history, complete omissions и consistent TX schedule/write results/RX dispositions. Directional omission sequence возрастает между segments/windows и ограничена actual Scheduled/Read totals; interwindow gaps допустимы. Invalid arithmetic/accounting оставляет Down unresolved.

Intentional verdict требует timeout cause, prior remote Up, old actual deadline, commit целиком внутри measurement/fault window и matching valid Up packets. Минимум negotiated multiplier consecutive actual TX decisions покрывают receive→deadline: первая в течение interval, каждый gap не больше interval, final interval покрывает due. Old D совпадает с wire inputs и fixed workload RX.

Reused discriminator, unknown phase/identity, boundary overlap, отсутствие progress и peer-originated Down остаются unresolved. Одна omission/window overlap недостаточна. Дополнительная known terminal loss, counter reset либо отсутствие commit progress запрещают positive attribution; aggregate loss counters не задают exact fault time.

Positive attribution предполагает доставку omitted packets неизменным transport до due и отсутствие independent hidden fault. Эти assumptions сериализуются. Passing относится только к conditional model; false/unresolved eligible Down запрещает pass. Total uncertainty unknown, reference_qualified false. Fixture-only TX-only blackhole проверяет predicate отдельно; named profiles запрещают selector.

Источники: [classifier](../../../test/internal/bfdload/down_classification_linux.go), [regressions](../../../test/internal/bfdload/down_classification_linux_test.go).

### Echo owner witness

TransitionTiming используется под Echo observation mutex/version fence для actual Up→Down. ValidatedAt означает last return, обработанный preview owner, без заявления RFC 9747 validation/authentication. D задаётся локально. Копируется actual TimerEchoDetection deadline; direct handler без dispatch оставляет его unknown. Remote discriminator нулевой, remote state unknown.

Capture не добавляет callback/wire field/observer и сохраняет Echo FSM, forwarding, receiver и scheduling policy. Control remote-Up/discriminator predicates не квалифицируют Echo. Actual Echo UDP workload требует независимых returns и typed incarnation identity.

Local consolidation instruction от 2026-10-28 отдельно разрешает integration с удалением permanent Control/Echo owner thread pins. Witness не квалифицирует это изменение. Native 1000-session Control/Echo A/B, resource/timer criteria и final stable gates обязательны. PID headroom ADR-0007 и pressure-worker pin для CLOCK_THREAD_CPUTIME_ID сохраняются. Comparable secured inputs связываются при actual runner admission; documentation-only commits не требуют новых пар.

Источники: [Echo owner](../../../internal/bfd/echo.go), [Echo observations](../../../internal/bfd/echo_observation.go), [Echo workload](../../../test/internal/bfdload/owner_echo_linux.go).

### Echo old-deadline classification

Control predicate сохраняется. Echo требует checked `D=interval*detect_multiplier`, actual old deadline d, last accepted return r, matching incarnation/version и diagnostic EchoFailed. Remote Control state/discriminator unknown.

RX/return-TX omissions объединяются по actual received_sequences, а не отдельным decision counters. Read indices уникальны/возрастают между windows, input times не убывают. Каждый выбранный вход должен совпадать с owner wire generation; выбранные read indices последовательны и непусты. Каждый p удовлетворяет `p>r+owner_bracket` и `p+peer_bracket<d`. Полный commit bracket следует после d внутри того же window. Journal bounds прежние; сортировка bounded references не меняет reports.

Каждый положительный вывод требует checked `max(p)+D>d+owner_bracket`. Counterfactual owner Reset во время s следует после выбранного входа, поэтому `s+D>=max(p)+D>d+owner_bracket`: новый deadline строго позже любого old due, допустимого bracket. DetectMultiplier уже входит в D; количество пропущенных входов не заменяет интервальное доказательство. ValidatedAt предшествует Reset, поэтому равенство `d=r+D` не предполагается.

Явное assumption: выбранные withheld inputs вернулись бы, были приняты владельцем и сбросили detection до old deadline. Одного прибытия в socket для этого недостаточно. Вывод относится к этому deadline, а не отсутствию всех later counterfactual timeouts. Actual Echo inputs не требуют вымышленного непрерывного TX schedule.

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
