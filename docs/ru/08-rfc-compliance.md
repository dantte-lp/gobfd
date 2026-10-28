# Соответствие RFC

Матрица описывает поведение исходников `dev` и не подтверждает stable-release acceptance. Реализованная функция, local fixture, actual UDP/capture result и приёмка final candidate — разные уровни evidence. [Interop](05-interop.md) и [performance](14-performance-analysis.md) содержат процедуры и измерения; final-candidate qualification остаётся отдельной.

## Матрица соответствия

| RFC | Профиль | Состояние исходников | Граница квалификации |
|:---|:---|:---|:---|
| [5880](../rfc/rfc5880.txt) | Base protocol | Asynchronous core; partial | Complete Poll/Demand, affiliated Echo и final qualification открыты |
| [5881](../rfc/rfc5881.txt) | IPv4/IPv6 single-hop | Numbered multiaccess; partial | Point-to-point initial demux и native architecture gates открыты |
| [5882](../rfc/rfc5882.txt) | Client application | Partial integration | State delivery и external convergence измеряются отдельно |
| [5883](../rfc/rfc5883.txt) | Multihop | Ограниченный GTSM product profile | Real routed 2/5-hop qualification открыта |
| [7419](../rfc/rfc7419.txt) | Общие интервалы | Helpers и optional alignment реализованы | Универсальный hardware/cloud minimum не следует |
| [9384](../rfc/rfc9384.txt) | BGP Cease/10 | Не реализовано | GoBGP action передаёт Cease/2 |
| [9468](../rfc/rfc9468.txt) | Unsolicited BFD | Unsafe default-off preview | Admission limits не разрешают untrusted exposure |
| [9747](../rfc/rfc9747.txt) | Unaffiliated Echo | Default-off preview | FSM, demux, authentication, pacing и forwarding gaps |
| [7130](../rfc/rfc7130.txt) | Per-member Micro-BFD | Preview; selected actuators | Dedicated member wire/MAC и owner interoperability открыты |
| [8971](../rfc/rfc8971.txt) | VXLAN BFD | Incomplete userspace preview | Exact IPv4 profile; owner-specific integration открыта |
| [9521](../rfc/rfc9521.txt) | Geneve BFD | Incomplete userspace preview | Только IPv4 Format A; owner integration открыта |
| [9764](../rfc/rfc9764.txt) | Large packets | Partial | Auth vectors и scoped padded TX; path MTU/final gates отдельно |
| [7880](https://www.rfc-editor.org/rfc/rfc7880.html), [7881](https://www.rfc-editor.org/rfc/rfc7881.html) | S-BFD | Planned | Reflector, initiator и UDP/7784 listener отсутствуют |
| [5884](../rfc/rfc5884.txt), [5885](../rfc/rfc5885.txt) | MPLS LSP/PW VCCV | Не реализовано | Session implementation и stub API отсутствуют |

Пять preview profiles явно представлены в [CapabilityService](../../internal/server/capabilities.go). Maturity, enablement и local readiness независимы. `ready` требует owned receive path и готовности source/runtime/binding; он не подтверждает peer health, dataplane ownership либо RFC compliance. Отсутствующие runtime data остаются unknown.

## RFC 5880 Implementation Notes

### Границы пакета, состояния и приёма

| Section | Поведение | Repository source |
|:---|:---|:---|
| 4.1 | Fixed header 24 байта, проверка формы auth, отказ M=1 | [packet.go](../../internal/bfd/packet.go) |
| 6.1–6.2 | Packed local pair, coherent committed observation, чистый FSM | [session.go](../../internal/bfd/session.go), [fsm.go](../../internal/bfd/fsm.go) |
| 6.3 | Discriminator/initial-key lookup и exact transport checks | [manager_session.go](../../internal/bfd/manager_session.go) |
| 6.7 | Пять auth types, immutable wire verification, bounded inline payload | [auth.go](../../internal/bfd/auth.go), [ADR-0011](adr/0011-bfd-auth-inline-payload.md) |
| 6.8.3–6.8.4 | Directional timing, non-Up floor одна секунда, receive expiry | [session_packet.go](../../internal/bfd/session_packet.go), [session_loop.go](../../internal/bfd/session_loop.go) |
| 6.8.6 | Codec validation, Manager admission, owner auth и FSM | [Приём пакета](01-architecture.md#приём-пакета) |
| 6.8.7 | Jitter 75–100%, при local multiplier 1 — 75–90% | [Jitter](02-protocol.md#jitter) |
| 6.8.12 | DetectMult входит в five-field reload tuple | [session_update.go](../../internal/bfd/session_update.go) |

Выбранная Held-errata 5205/7240 policy задаёт diagnostic 1 при Down timeout без state edge и сохраняет diagnostic при переходе в Up. AdminDown/7 сохраняется при local PathDown. Это явные project policies; Held for Document Update не изменяет normative RFC requirements. Источники: [FSM](../../internal/bfd/fsm.go), [owner](../../internal/bfd/session_loop.go), [RFC errata record](https://www.rfc-editor.org/errata/rfc5880).

### Poll Sequences и reload

Один active advertised tuple и последний queued tuple сериализуют TX, RX, DetectMult, MinTTL и PaddedPduSize. В Up TX increase и RX decrease ждут соответствующий Final; обратные направления действуют при activation. Final не подтверждает Poll, который не был успешно передан. Crossed Poll, Final retry, P/F exclusion и transaction separation имеют отдельные tests/wire checks.

Detect-only asynchronous change не требует Poll; remote Demand может его требовать. TTL — локальная product policy без Poll. Positive-to-positive padding требует unchanged auth, sole config ownership, asynchronous mode и immutable constructor proof DF preparation. Auth transitions отклоняются до generation acceptance по retained intent и live owners. Источники: [ADR-0006](adr/0006-session-preserving-reload-policy.md), [owner transactions](../../internal/bfd/session_update.go), [admission](../../internal/bfd/manager_auth_reload.go).

Scoped DetectMult, TTL и padding receipts сохраняются как исторические slice evidence. Unified final-candidate gate выполняется после оставшихся production Session/Echo thread/scale работ. Эти slices не подтверждают general Demand, deployed RTT envelope, forwarding continuity либо stable acceptance.

### Административное управление

AdminDown drain подтверждает owner commit, первую успешную разрешённую запись и elapsed negotiated window в общем бюджете десять секунд. Window покрывает оба directional bounds; passive unknown-peer и remote RX=0 suppression сохраняются. Final reply может стать первой записью. Last-claim release выполняет drain вне Manager locks и резервирует exact key до cleanup. Unsolicited failure cleanup использует отдельный stop-TX path. Источники: [drain](../../internal/bfd/session_drain.go), [replacement](../../internal/bfd/manager_replace.go), [RFC 5880 §6.8.16](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.16).

Датированный SIGTERM receipt в `reports/h12-wire-20261016/` содержит восемь AdminDown/7 packets, по четыре для FRR/BIRD, с TTL 255, multiplier 3, advertised TX 1000000 мкс и RX 300000 мкс. First-wire-to-drain: 3,000171 с и 3,000147 с. Сорок samples за 20,280366 с сохраняют BGP Established и два test routes; промежутки не наблюдаются. Input: source patch SHA256 `d1394d09fb3b902d3e34a5e34467c5aa4c506bd1d71c49416f6176773dc86947` поверх commit `e4f3761a0f1fc59fbf9b2f859923a81125d157bf`; PCAP SHA256 `f10937cc6a7969bc336e4f1b5217d2f458e5f046236f68530225ef26ec281c68`. Это историческое local evidence, а не результат current candidate.

### Нереализованные функции

Local D остаётся нулём. Affiliated Echo отсутствует; Control Required Min Echo RX равен нулю. Remote-Demand periodic suppression и crafted-peer timer Poll дают частичную поддержку. P2MP отсутствует. См. [ограничения протокола](02-protocol.md#нереализованные-функции).

## RFC 5881 Implementation Notes

[UDP sender](../../internal/netio/sender.go), [listener](../../internal/netio/listener.go) и [Manager admission](../../internal/bfd/manager_session.go) реализуют destination 3784, фиксированный source port 49152–65535, TX TTL/Hop Limit 255 и per-session RX=255. Отдельные IPv4/IPv6 sockets сохраняют packet-info destination и ingress interface.

Single-hop RX проверяет настроенный интерфейс либо интерфейс local address при создании. Отсутствующее/неоднозначное владение local address оставляет configuration pending; API creation возвращает `FailedPrecondition`. Loopback/dummy local address требует явного physical `interface`, если пакеты приходят через него. Point-to-point initial demux с произвольным remote source отложен.

IPv6 link-local zones выбирают интерфейс и должны с ним совпадать; initial key их не содержит. Linux TTL/Hop Limit и interface indices имеют native-endian ancillary representation; IP addresses сохраняют network order. Ограничение Echo source port в shared sender — product policy: RFC 5881 его не задаёт. Native `linux/amd64` и `linux/arm64` kernel qualification остаётся обязательной; cross-compilation её не закрывает.

## RFC 5882 Implementation Notes

[GoBGP handler](../../internal/gobgp/handler.go) вызывает disable при ordinary remote Down, detection failure и explicit local PathDown. Local либо known remote AdminDown не вызывает disable; retained remote AdminDown переживает последующие timeout diagnostics. Up включает только peers, выключенные этим процессом. Действие ограничено `gobgp.action_timeout`.

Penalty dampening — implementation policy по §3.1, а не алгоритм RFC. Generic route withdrawal/restoration отсутствует; `withdraw-routes` отклоняется validation. Watch delivery, external action success и routing convergence требуют независимых receipts. Источники: [integration](../../internal/gobgp), [config validation](../../internal/config/config.go), [RFC 5882 §§3.2, 4.2, 10.2](https://www.rfc-editor.org/rfc/rfc5882.html#section-3.2).

## RFC 5883 Implementation Notes

Multihop использует UDP/4784 и address-pair initial demux; явный interface запрещён. TX TTL равен 255. Per-session `multihop_max_hops=N` принимает TTL/Hop Limit ≥255−N: default N=1 даёт 254; N=2 — 253; N=5 — 250. Это ограниченный GoBFD GTSM profile, а не receive-TTL requirement RFC 5883. Неверный transport class отклоняется до delivery.

[RFC 5883 §§3, 6](https://www.rfc-editor.org/rfc/rfc5883.html#section-3) запрещает multihop Echo и рекомендует cryptographic authentication. Настройте authentication для multihop; TTL floor обеспечивает только дополнительную защиту. Используйте поддерживаемый meticulous SHA1, если разрешает peer policy. Actual routed/cloud paths требуют отдельной квалификации. Источники: [Manager](../../internal/bfd/manager_session.go), [config](../../internal/config/config.go), [ADR-0006](adr/0006-session-preserving-reload-policy.md).

## RFC 7419 Implementation Notes

[Interval helpers](../../internal/bfd/intervals.go) задают шесть common intervals: 3300, 10000, 20000, 50000, 100000, 1000000 микросекунд. Alignment округляет вверх внутри набора и сохраняет значения больше максимума. Отдельный graceful-restart interval равен 10 с. Common values не подтверждают timer precision либо hardware/provider acceptance. Проверьте [профиль target peer](reference/vendor-bfd.md).

## RFC 9384 Implementation Notes

[GoBGP adapter](../../internal/gobgp/rfc9384.go) добавляет Cease/10 в communication text, но текущий DisablePeer передаёт Administrative Shutdown, Cease/2. RFC identifier в строке не реализует wire subcode 10. Полная поддержка требует action API и capture с этим subcode.

## RFC 9468 Implementation Notes

| Admission condition | Product behavior | Source |
|:---|:---|:---|
| Opt-in/interface | Default-off; явные nonempty interfaces, device-bound dual-stack listeners | [config](../../internal/config/config.go), [listeners](../../cmd/gobfd/listeners.go) |
| Peer/path | TTL 255, single-hop listener, source в receiving local subnet; optional narrower prefixes | [unsolicited](../../internal/bfd/unsolicited.go) |
| Capacity/rate | Enabled `max_sessions≥1`; 10 admissions/с, burst 20 | [Manager](../../internal/bfd/manager.go) |
| Auth | Optional Meticulous Keyed SHA1 trigger verification; без policy A-bit отклоняется | [admission tests](../../internal/bfd/unsolicited_auth_test.go) |
| Sender/cleanup | Receiving local tuple/interface, собственный source-port lease; stop non-Final TX после natural failure/setup expiry | [owner](../../internal/bfd/session_loop.go), [cleanup](../../internal/bfd/unsolicited.go) |

Unnumbered admission отклоняется. Quota резервируется атомарно и освобождается при failure/destroy/cleanup. Повторные unchanged packets не перезапускают independent setup budget. Общие API/config claims сохраняют обычный Control TX. Cleanup проверяет actual failed attempt и не удаляет resumed attempt либо local AdminDown. Final responses и administrative notification сохраняют base-protocol rules.

Dual-stack/78-lease receipt не снимает unsafe preview. Не открывайте unsolicited mode на недоверенном интерфейсе.

## RFC 9747 Implementation Notes

[Echo owner](../../internal/bfd/echo.go) и [receiver](../../internal/netio/echo_receiver.go) реализуют port 3785, Control-shaped packets, local timers, Down↔Up, My Discriminator return demux, TX TTL 255/RX точно 254 и timeout diagnostic 2.

RFC требует другой bootstrap, последующий Your Discriminator demux, base FSM без AdminDown, slow pacing и return validation/authentication. Текущий send path также не подтверждает adjacent forwarding-plane traversal. Qualification должна закрыть FSM, returned-packet identity/authentication и pacing gaps.

Каждый Add, включая retry, требует preview enablement и local RX readiness. Совместимые principals разделяют wire по [ADR-0009](adr/0009-echo-principal-claims.md); management ownership не устраняет protocol gaps. Owner witnesses и conditional load assessment из [ADR-0012](adr/0012-owner-down-transition-witness.md) не квалифицируют RFC 9747.

## RFC 7130 Implementation Notes

Одна member/family session использует UDP/6784 и member-device binding. `MicroBFDGroup` учитывает только Up members; aggregate становится Up при `upCount≥MinActiveLinks`. Existing shared session задаёт начальное состояние group. Group/source claims проходят reconciliation с member sessions; crossing threshold публикует aggregate transition.

Actuator получает упорядоченные member changes вне Manager locks с deadline пять секунд. Режимы: `disabled`, `dry-run`, `enforce`; enforcement требует явных backend и owner policy. Реализованы kernel-bond sysfs, native OVSDB и NetworkManager D-Bus; OVS CLI fallback не выбирается daemon. Dedicated multicast MAC/member-wire и real-owner qualification остаются отдельными. Источники: [group](../../internal/bfd/micro.go), [daemon wiring](../../cmd/gobfd/micro_bfd.go), [actuator](../../internal/netio/lag_backend.go), [ADR-0003](adr/0003-linux-advanced-bfd-applicability.md).

## RFC 8971 Implementation Notes

VXLAN preview использует outer UDP/4789, eight-byte VXLAN header с I flag, Management VNI и inner Ethernet/IPv4/UDP/3784/BFD. Inner destination MAC: `00:52:02:00:00:00`; local/remote originating source MACs и полный endpoint/VNI/IP tuple должны совпадать с configured scope.

Реализован только `userspace-udp`. Один listener на local VTEP разделяет exact scopes и physical source-port ownership. Reserved kernel/OVS/OVN/Cilium/Calico/NSX backends отклоняются. Сокет другого dataplane находится вне профиля. Источники: [VXLAN connection](../../internal/netio/vxlan_conn.go), [inner validation](../../internal/netio/overlay_inner.go), [backend admission](../../internal/netio/overlay_backend.go), [overlay ownership](../../cmd/gobfd/overlay_owner.go).

## RFC 9521 Implementation Notes

Geneve preview использует outer UDP/6081, O=1, C=0, version 0, Protocol Type `0x6558`, VNI и явную IPv4 Format A VAP IP/MAC identity. Inner Ethernet/IPv4/UDP/3784/BFD проверяется до discriminator lookup. Отсутствующая/special-use VAP identity, IPv6 inner payload, Format B и другие protocol types отклоняются.

Реализован только `userspace-udp`; socket-sharing integration с dataplane отсутствует. Send/receive cancellation не закрывает healthy shared listener; Close разблокирует I/O, payload заимствует receive buffer до следующего read. Rates и congestion exposure требуют target profile. Источники: [Geneve connection](../../internal/netio/geneve_conn.go), [inner validation](../../internal/netio/overlay_inner.go), [ADR-0003](adr/0003-linux-advanced-bfd-applicability.md).

## RFC 9764 Implementation Notes

Target считает BFD transport-payload bytes без IP/UDP headers. Ноль выключает padding; nonzero ranges: 24–9000 без auth и 26–9000 с auth. Отказ authenticated 24/25 — принятая product policy; target меньше actual auth PDU его не обрезает. TX использует `max(actual PDU, target)`, zero-fill и IPv4 DF, подготовленный при создании sender.

Hash verification охватывает объявленный BFD Length, включая in-PDU extension bytes, и исключает transport padding. Contents padding при приёме не проверяются. Независимые local vectors покрывают пять auth types и unpadded, 64-, 128-, 9000-byte datagrams. Direct receive pool принимает до 9000 и отклоняет Linux `MSG_TRUNC`; codec `MaxPacketSize=64` не задаёт datagram bound. Overlay buffers добавляют framing, не расширяя inner BFD: VXLAN 9090, Geneve 9342 байта.

[Critical BASE interop](05-interop.md) проверяет padded GoBFD TX 128 байт к FRR Simple Password и BIRD Keyed SHA1, BFD Length, zero padding и DF. Reverse peer TX — обычный authenticated PDU; это не квалифицирует padded peer-originated traffic либо path MTU. Positive-to-positive reload и датированные padding slices заданы в [ADR-0006](adr/0006-session-preserving-reload-policy.md). Enable/disable/dynamic DF остаётся вне принятого reload profile; unified exact-candidate gate остаётся обязательным.

Источники: [padding policy](../../internal/config/padding.go), [auth vectors](../../internal/bfd/auth_padding_test.go), [receive bounds](../../internal/netio/receive_size_test.go), [sender](../../internal/netio/sender.go), [RFC 9764 §3](https://www.rfc-editor.org/rfc/rfc9764.html#section-3).

## RFC 7880/7881 (Planned)

S-BFD reflector, initiator и port-7784 listener отсутствуют. Сохранённые планы и RFC links не являются API. Реализация требует самостоятельных discriminator, packet и ownership contracts.

## MPLS и VCCV

RFC 5884/5885 остаются reference scope. MPLS/VCCV sessions и stub interfaces отсутствуют. MPLS LSP Ping и PW signaling integration требуют отдельной работы.

## Справочные RFC

RFC 5082 задаёт контекст GTSM; RFC 8203 — administrative shutdown communication. RFC 9127 используется как management-model reference. OSPF strict mode из RFC 9355 требует отсутствующей OSPF integration. Источники и applicability связаны из [протокола](02-protocol.md), [GoBGP adapter](../../internal/gobgp/rfc9384.go), [vendor references](reference/vendor-bfd.md).

## Исходные тексты RFC

[Локальный RFC archive](../rfc/) сохраняет стандарты. Наличие RFC 9985/9986 либо другого текста не подтверждает реализацию. Используйте section-specific references вместо безусловной метки compliance.

## Связанные документы

- [Архитектура](01-architecture.md) — physical ownership, delivery и retirement.
- [Протокол](02-protocol.md) — packet/timer reference.
- [ADR index](adr/README.md) — принятая policy и отдельная qualification.
