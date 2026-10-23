# ADR-0006: Политика reload с сохранением сессии

| Поле | Значение |
|---|---|
| Status | Accepted |
| Date | 2026-10-24 |
| Qualification | Принята policy; implementation и peer qualification проверяются отдельно |

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
| Wire identity или другой неподдерживаемый transition | Не заменять автоматически; disruptive replacement требует отдельного явного намерения оператора |

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
`gobfd-qj0.8.2.2.7.2` ещё требует final gates;
`gobfd-qj0.8.2.2.7.4` (reconciliation/rekey integration) остаётся P0.
`gobfd-qj0.8.2.2.7.5` владеет P1 actual-wire final gate для всех reload fields,
включая RFC 9764 peer/MTU qualification. Исполняемые планы, приоритеты, зависимости и acceptance evidence
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
