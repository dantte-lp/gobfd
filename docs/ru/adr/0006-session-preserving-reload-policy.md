# ADR-0006: Политика reload с сохранением сессии

| Поле | Значение |
|---|---|
| Status | Accepted |
| Date | 2026-10-24 |
| Qualification | Принята policy; implqual-local; actual-wire qualification открыта |

## Контекст

Reload конфигурации не должен заменять установленную BFD-сессию только из-за
изменения поддерживаемого effective parameter. Замена меняет discriminator и
протокольное состояние и может повлиять на routing clients. Сохранение
локального экземпляра само по себе не доказывает непрерывность peer state или
forwarding.

RFC 5880 разделяет согласование таймеров, изменение Detect Multiplier,
аутентификацию и administrative control. В RFC 5882 обработка AdminDown и
graceful restart дополнительно зависит от независимого liveness detection
клиента и fate sharing с control plane. Эти механизмы не дают общей гарантии
hitless reload.

## Решение

Сохранять существующую base session, если canonical wire identity не меняется,
единственный владелец — декларативная конфигурация, а весь предлагаемый tuple
параметров поддерживается. Сохранять экземпляр, discriminator, sender lease,
authentication replay state, counters и ownership. Shared session нельзя
изменять в интересах одного владельца; несовместимый proposal отклоняется.

| Изменение | Требуемое поведение |
|---|---|
| Desired TX / required RX intervals | Сохранить действующие Poll/Final transaction rules RFC 5880 §6.8.3 |
| Detect Multiplier | Объявить новое ненулевое значение в следующем Control packet без Poll в Async; при активном remote Demand выполнить требования Poll из §6.6 |
| Multihop minimum TTL | Применять локальный receive floor 255 − N под Session.mu без замены и Poll, в том числе при remote Demand; это policy GoBFD, не требование RFC 5883 к receive TTL |
| Padded PDU target | Допускать только positive-to-positive changes в Async на уже DF-prepared sender с прежней authentication и единственным config-owner; неподдерживаемый base candidate отклонять до generation acceptance |
| Неподдерживаемый authentication transition | Отклонить весь candidate до принятия новой generation; сохранить прежнюю конфигурацию |
| Неквалифицированное изменение wire identity или другой неподдерживаемый transition | Не заменять автоматически; disruptive replacement требует отдельного явного намерения оператора |

Квалификация multihop receive floor использует authenticated UDP/4784 startup
profile штатной crafted-peer topology. Peer приостанавливается до reload;
повтор его последнего принятого signed payload с допустимым TTL должен быть
отклонён до свежей boundary-пробы. Этот намеренный replay добавляет ровно
один auth failure и ноль accepted inputs; сам reload не добавляет auth failures
или state transitions. Исходный identity/counter baseline охватывает все
reload и вход в remote Demand. За подписанным Poll ниже `255 - N`
следует идентичный payload с тем же sequence на пороге. Только второй пакет
может увеличивать accepted-input counter и вызывать Final; отклонённый пакет
не продвигает receive authentication state. Один capture/authentication
checkpoint сохраняется через reload и вход в remote Demand. Probe укладывается
в действующий Detection Time, чтобы expiry replay state не подменяло проверку
непрерывности. Source tests не закрывают actual kernel/capture gate
`gobfd-qj0.8.2.2.7.5.4.3`. Профиль не квалифицирует routed/cloud paths или
общую работу Demand.

Увеличение TX и уменьшение RX сохраняют защищённый прежний локальный эффект
до соответствующего Final; обратные изменения сохраняют действующий immediate
effect. Активный Poll сохраняет объявленный tuple, последующий proposal
ожидает по действующему transaction contract. Добавление Detect Multiplier,
TTL или padding не ослабляет эти правила. Исключение для remote Demand не
квалифицирует общую поддержку Demand mode.

Срез packet policy v1 расширяет tuple, refs и confirmed/effective receipts до
пяти полей: TX, RX, DetectMult, MinTTL, PaddedPduSize. Coordinator передаёт
`Manager.AdmitSessionReload` только `candidate.base`; guard проверяет его
stable-identity field/ownership policy до локального generation
acceptance, сохраняя описанные ниже retained/live authentication checks.
Padding требует immutable constructor proof `PaddingPrepared`, а не requested
option или утверждение socket liveness. Zero-to-positive и positive-to-zero
transitions, неизвестный/неподготовленный sender, remote Demand и shared-owner
base padding updates отклоняются. Micro-BFD и overlay остаются вне допустимого
профиля; поля padding/minTTL в их configuration не добавляются. Effective changes
других sources сохраняют прежний per-source conflict/stale; новый preflight всех
пяти sources не подразумевается. Policy проверяется до equality
shortcuts, включая возврат к confirmed target при активном Demand. Owner
повторно проверяет queued tuple и отклоняет его целиком до commit любого поля,
если Demand включился во время ожидания.

Рост capacity готовит один заменяемый waiting buffer при control admission;
owner меняет buffer до commit полей. Growth allocation не откладывается до
owner promotion или packet transmission. Target считает transport payload
bytes без IP/UDP headers; длина отправки равна максимуму declared PDU и target,
поэтому authentication не усекается. Padding нулевой и находится за пределами
действующего authentication MAC domain. Прежние authenticated minimum 26 и
zero-target DF defaults сохраняются. Padding enable/disable и dynamic DF
preparation — отдельная after-v1 P2 задача `gobfd-ebu`.

Полностью валидировать candidate, включая same-session transitions и ownership,
до принятия его desired generation. Применять изменение одной сессии через
её protocol owner как согласованную transaction: нельзя фиксировать одно поле
и оставлять его изменённым после отказа валидации или подготовки другого.
Намеренно разделённые desired, advertised и effective values отражаются как
pending, а не как завершённое обновление.

Повторное применение одной generation/revision идемпотентно: не создаёт
новую сессию, replacement, Poll или повторный side effect. Устаревший completion
не перезаписывает новую desired revision. Receipts различают desired,
advertised и effective values для полей с wire representation; minimum TTL
остаётся локальным admission value. Receipt state различает pending,
committed и failed. Admission или advertisement не доказывает effective
completion и readiness. Это transaction policy одной сессии, а не утверждение
об атомарном commit всех reload sources.

Смена authentication algorithm, enable/disable аутентификации и
неквалифицированные изменения ключей отклоняются до принятия новой generation,
в том числе вместе с поддерживаемыми изменениями. Не допускаются скрытый
delete/recreate и сброс sequence/replay protection. RFC 5880 §6.7 оставляет
координацию за пределами BFD; наличие key store не квалифицирует rotation
protocol.

Admission сравнивает immutable authentication fingerprints с двумя наборами:
retained accepted base desired set, включая отсутствующие в runtime записи,
и live Manager registry без фильтрации API или shared owners. Сравниваются
enablement, algorithm, key IDs/material, выбранный TX key и receive-key set.
Аутентификация при startup, новые или удаляемые identities и новые экземпляры
stores с прежней семантикой остаются допустимыми.

Порядок блокировок: Manager lifecycle, ownership, registry. Registry lock
освобождается до короткого локального acceptance; ownership serialization
сохраняется до публикации desired generation, затем освобождается до
configured-peer callbacks и применения listener/sender resources. Отказ
предшествует изменениям generation, log level и readiness, сохраняя прежние
accepted configuration и sessions. Claims, появившиеся после acceptance,
проверяются обычной fail-closed Manager reconciliation. Эта admission boundary
не является whole-source atomic transaction или гарантией бесшовного rollback.

Опциональная coordinated key-ID rotation в пределах одного authentication
algorithm — отдельная задача `gobfd-khr`, P2, after-v1. Graduation требует
квалификации receive overlap, transmit cutover, вывода старого ключа,
replay/sequence continuity и peer interoperability. Этот ADR не включает
такую возможность.

Disruptive replacement отдельно от обычного reload определяет ожидаемые
identity/transition и влияние на clients. Ошибка подготовки до peer-visible
cutover должна откатывать подготовленные ресурсы. После публикации изменённого
состояния или пакетов peer бесшовный rollback не гарантируется.
Administrative signaling следует RFC 5880 §6.8.16 и применимой client policy
RFC 5882; AdminDown не обещает неизменность BGP или forwarding во всех случаях.

Source получает/готовит desired resources до освобождения stale claims. Снятие
старых claims допустимо только при нулевых `Failed` и `ResourcePending`;
timer negotiation учитывается отдельно. Fatal preparation откатывает новые
claims, valid disjoint claims могут сохраниться для resource-pending retry.
Занятый receive key допускает подготовленный SingleHop/MultiHop swap только
для base sessions без interface с одним config-owner, отсутствующим в desired
старым ключом и прежними остальными identity fields. Другие collisions
отклоняются до sender factory, а для base reload — и до desired-generation
acceptance. Отмена до публикации swap сохраняет прежнюю identity;
подготовленные lease/discriminator освобождаются ровно один раз. После публикации
прежняя captured session фиксирует AdminDown и выполняет drain вне Manager и
lifecycle mutation locks, удерживая lease/discriminator до завершения или
typed incomplete failure, после чего выполняется cleanup.

Чистый overlay scope preflight проверяет целый source VXLAN или Geneve до
любого backend open или `SetScopes`. Неравный scope с wire identity живого или
draining lease, включая изменение VXLAN local MAC, требует отдельного явного
removal/recreation. Duplicate desired wire identities недопустимы; никогда не
закоммиченную unused preparation можно заменить новым same-wire scope.
После успешного preflight obsolete unused preparations удаляются до следующих
claims. Retained set — пересечение existing scopes и current desired плюс scopes
с положительными physical `SenderLease` refs. Фактическая sender factory повышает
ref. Registered physical session удерживает ref через retirement drain;
idempotent lease close затем освобождает его. Unused или unpublished lease
освобождается ordinary once-only cleanup без drain при construction failure или
отмене. Active/draining exact scopes переживают failed generations. Неполный acquire возвращает Pending/Failed
до Manager apply/release этого source, сохраняя live/draining scopes/backends;
preparations текущего desired могут остаться для retry. Это не общий pre-generation overlay admission и не
атомарность всех sources.

## Последствия

Поддерживаемые изменения сохраняют локальную identity сессии и исключают
ненужный restart. Peer behavior, timing, padding acceptance и forwarding
continuity по-прежнему требуют измеренной квалификации. RFC 9764 §4.1 явно
описывает возможный переход установленной Up-сессии в Down, если peer не
принимает padded Control packets.

Cloud и hardware requirements остаются отдельными target profiles. Ограничения
таймеров, Async/Echo и graceful-restart policy проверяются для конкретных
provider и connection; common intervals RFC 7419 не доказывают cloud
acceptance. Прежнее сравнение источников сохранено в
`reports/reload-policy-20261024/sources.txt`. Исторический статус обсуждения
не подтверждает одобрение этого решения или implementation qualification.
ADR не утверждает provider-specific runtime interoperability.

Implementation и qualification принадлежат Beads parent `gobfd-qj0.8.2.2.7`.
Detect Multiplier `gobfd-qj0.8.2.2.7.1` и authentication admission
`gobfd-qj0.8.2.2.7.3` закрыты на dev. Packet-policy child
`gobfd-qj0.8.2.2.7.2` закрыт на dev. Reconciliation/rekey integration
`gobfd-qj0.8.2.2.7.4`, `gobfd-qj0.8.2.2.7.4.1` и
`gobfd-qj0.8.2.2.7.4.2` реализованы и прошли ограниченную локальную
Go-квалификацию; доказательства интеграции и закрытия задач хранятся в Beads.
Evidence — `reports/rekey-lifecycle-20261024/`. Critical01 содержит
1049 top-level PASS, ноль FAIL, пять пакетов и два прежних environment skips;
quality04 — ноль issues, vet01 и оба benchmark-прогона завершились с exit 0.
Все 27 выбранных allocation budgets совпадают с базой; 577 source-inventory
checks совпадают с frozen critical input. Это не all-wire, scale или stable acceptance.
Overlay lease-ref/preparation cleanup `gobfd-qj0.8.2.2.7.4.2.1` входит в эту квалификацию.
`gobfd-qj0.8.2.2.7.5` владеет обязательным P0 actual-wire final gate с явной
зависимостью от `gobfd-qj0.8.2.2.7.4`. Существующий wire gate должен до v1
квалифицировать все пять reload fields, включая RFC 9764 peer/MTU behavior.
Source unit tests не подтверждают interoperability; policy не вводит новый harness.
Wire-квалификация выбирает ожидаемую семантику receipts по сценарию независимо
от проверки capture. Немедленные изменения допускают прямую сходимость или
ожидание owner; прежние Poll-фазы сохраняют pending-before-applied evidence.
Этот порядок задаёт текущий owner/coordinator contract, не требования RFC к JSON.
Ни один режим сам по себе не доказывает наличие или отсутствие wire Poll/Final.
Подготовка общего validator ведётся в `gobfd-qj0.8.2.2.7.5.1`.

Wire-квалификация Detect Multiplier (`gobfd-qj0.8.2.2.7.5.2`) расширяет
существующие аутентифицированные Async-фазы последовательностью 1→5→3,
remote-Demand — 5→3. При согласованном интервале 300 ms множитель 1 имеет
jitter 75–90% и ожидаемое среднее 247.5 ms; остальные проверяемые множители —
75–100% и 262.5 ms (RFC 5880 §6.8.7). Изменение локального множителя сохраняет
зависимость receive Detection Time GoBFD от принятого множителя peer.
Async Detection Time соседа вычисляется, но не измеряется этими фазами.
Для crafted remote-Demand peer значение 5×500 ms задаёт расчётную границу
локального Poll; у этого peer нет detection timer. Ответы Final и quiet window
не квалифицируют missing-Final expiry или общую поддержку Demand. Capture
проверяет непрерывность discriminator, UDP source port/length, типа/key/sequence
аутентификации, timer tuple и state независимо от generation receipts.
Для закрытия wire-задачи обязательны фактические captures кандидата.
Async capture допускает один старый in-flight Control только первым локальным
post-receipt пакетом в пределах существующего timer slack; первый новый Control
обязателен за один negotiated TX плюс slack. Поздний старый множитель отклоняется.
Runtime discovery использует запросы registry по префиксам и точное сравнение
первого столбца; полный registry не буферизуется в race runner.

Локальная DetectMult wire-квалификация прошла на
`task/detect-wire-20261024@d9e67064e51012c0d25a1a22dc9bd469600d8469`:
шесть аутентифицированных Async-фаз и две crafted remote-Demand фазы; captures
и receipts — в `reports/detect-wire-20261024/`. Задачи `.5.2` и `.5.2.1`
закрыты локально. Prepared padding, фактический TTL admission и unified
exact-candidate gate остаются P0; stable acceptance не подтверждается.

Квалификация prepared padding (`gobfd-qj0.8.2.2.7.5.3`) сохраняет capture cursor
и receive authentication state каждого направления на всей цепочке resize
и отклонённых кандидатов. Повторная оценка транзакционна: ошибка или неполное
evidence не продвигают cursor и replay state. Независимый peer сохраняет
собственный размер PDU; локальная цель 128→192→128 не доказывает двусторонний
MTU. DF, declared BFD Length, UDP payload, нулевой padding и граница MAC
проверяются отдельно. Meticulous SHA1 задаётся только при старте того же
FourPeer lifecycle; default keyed SHA1 квалифицируется отдельно. Runtime auth
rotation остаётся вне v1. Aggregate и named BFD readiness проверяются через
существующий management Unix socket внутри собственного контейнера демона
тем же собранным interop-бинарником. Он копируется существующим механизмом
container files; пути runner не передаются host engine как bind paths.
TCP listener остаётся loopback-only.
Source contracts не закрывают эту actual-wire задачу.

Локальная prepared-padding квалификация прошла на
`task/padding-wire-20261024@b97729458a696aee21023ba147af51417ed9ff7b`:
default auth4 и meticulous auth5 FourPeer profiles, а также отклонение resize
при crafted remote Demand; captures и receipts — в `reports/padding-wire-20261024/`.
Восемь resize-фаз и семь отклонённых кандидатов охватывают семнадцать peer windows;
aggregate и named BFD health остаются SERVING. Лимит runner 512 MiB сохранён,
max/OOM events и swap трёх прогонов — 0. Проверки целостности подтверждают
853 файла исходников и 675 sidecar. Это закрывает локальный padding-срез,
но не actual TTL, unified final-candidate, forwarding, general Demand или
квалификацию stable release.

Исполняемые планы, приоритеты, зависимости и acceptance evidence
ведутся в Beads. Принятие policy не означает acceptance реализации,
interop или stable release.

## Источники

- [RFC 5880 §6.6: Demand mode](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.6)
- [RFC 5880 §6.7: Authentication](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.7)
- [RFC 5880 §6.8.3: Timer manipulation](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.3)
- [RFC 5880 §6.8.12: Detect Multiplier change](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.12)
- [RFC 5880 §6.8.16: Administrative control](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.16)
- [RFC 5882 §3.2: AdminDown state](https://www.rfc-editor.org/rfc/rfc5882.html#section-3.2)
- [RFC 5882 §4.3: Graceful restart interactions](https://www.rfc-editor.org/rfc/rfc5882.html#section-4.3)
- [RFC 7419: Common interval support](https://www.rfc-editor.org/rfc/rfc7419.html)
- [RFC 9764 §§3–4.1: Large packets и peer compatibility](https://www.rfc-editor.org/rfc/rfc9764.html#section-3)
- [AWS Direct Connect BGP/BFD settings](https://docs.aws.amazon.com/directconnect/latest/UserGuide/bgp-specific-settings.html)
- [Google Cloud Router BFD profile](https://docs.cloud.google.com/network-connectivity/docs/router/concepts/bfd)
- [Azure ExpressRoute BFD profile](https://learn.microsoft.com/en-us/azure/expressroute/expressroute-bfd)
- [Действующий timer transaction contract](../../superpowers/specs/2026-09-08-gobfd-timer-update-transactions-design.md)
