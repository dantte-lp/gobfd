# ADR-0006: Политика reload с сохранением сессии

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-24 |
| Qualification | Accepted policy; implqual-local; actual-wire qualification remains open |

## Контекст

Replacement established session меняет discriminator, protocol state и client observations. Сохранение local object не доказывает peer continuity либо forwarding. Timer/auth/admin procedures RFC 5880 и client policies RFC 5882 не дают общего hitless-reload guarantee.

## Решение

Сохраняются instance, discriminator, sender lease, auth replay state, counters и claims для stable canonical base identity с одним config owner и поддерживаемым complete tuple. Несовместимые shared-owner changes отклоняются. Unsupported update не преобразуется неявно в delete/recreate.

### Five-field transaction

| Field/change | Обязательный эффект | Ограничение |
|:---|:---|:---|
| TX increase / RX decrease в Up | Прежний protected local effect до relevant Final | Existing Poll contract |
| TX decrease / RX increase в Up | Применить при activation proposal | Existing Poll contract |
| Non-Up TX/RX proposal | Immediate local effects при promotion | Poll/separation сериализуют confirmation |
| DetectMult в Async | Применить и объявить next Control без Poll | Nonzero; пересчитать jitter при crossing 1 |
| DetectMult при обоих peers Up и remote Demand | Poll/Final confirmation | Не квалифицирует general Demand |
| MinTTL | Local multihop floor `255−N` под mutex без Poll, включая remote Demand | GoBFD product policy |
| PaddedPduSize | Positive→positive, прежний auth, Async, constructor-proven `PaddingPrepared` | Без zero transition, unknown/unprepared sender и shared base owner |

Tuple содержит TX, RX, DetectMult, MinTTL, PaddedPduSize. Один active proposal сохраняет advertised tuple; хранится последний waiting proposal, включая RX=0. Final подтверждает только после successful Poll transmission. Crossed Final-only reply не подтверждает local Poll. Следующая транзакция ждёт measured exchange duration после Final. Expiry переводит receipt в failed без отмены advertised values.

Desired, advertised, effective и confirmed values/revisions различаются. MinTTL не передаётся по wire. Pending admission/advertisement не означает completion. Same-generation/revision reapply не создаёт replacement, Poll либо duplicate side effect; stale completion не перезаписывает новый intent.

Полный tuple проверяется и готовится до owner field commit. Координатор передаёт только `candidate.base` в `Manager.AdmitSessionReload`. Micro-BFD/overlay не получают padding/MinTTL fields и остаются вне qualified profile. Other-source conflict/stale behavior не становится five-source atomicity.

Padding policy проверяется до equality shortcut, включая возврат к confirmed target при Demand. Перед promotion queued tuple перепроверяется; newly active Demand отклоняет весь tuple до field commit. Capacity growth готовит один replaceable waiting buffer при control admission; owner выполняет swap до commit. Promotion и packet send не выполняют growth allocation.

Padding target считает transport payload без IP/UDP. Send length равен `max(actual PDU, target)`, сохраняя auth. Padding нулевой и вне authentication domain. Auth minimum 26 и zero-target DF defaults сохраняются. Enable/disable/dynamic DF — вне принятого reload profile.

Источники: [owner transaction](../../../internal/bfd/session_update.go), [tests](../../../internal/bfd/session_update_test.go), [packet construction](../../../internal/bfd/session_packet.go), [timer transaction specification](../../superpowers/specs/2026-09-08-gobfd-timer-update-transactions-design.md).

### Authentication admission

Same-identity enable/disable, algorithm и unqualified key transitions отклоняются до desired generation acceptance, включая combined update. Sequence/replay state не сбрасывается; replacement fallback запрещён.

Immutable semantic fingerprints сравниваются с retained accepted base intent, включая отсутствующие runtime entries, и live registry независимо от API/shared ownership. Fingerprint включает enablement, algorithm, key IDs/material, TX key и receive-key set. Startup/new/removed identities и fresh stores с прежней семантикой допустимы.

Порядок locks: lifecycle→ownership→registry. Registry освобождается до короткого local acceptance; ownership удерживается до generation publication и освобождается до configured-peer callbacks/resource application. Отказ предшествует generation, log/readiness и resource mutation. Поздние claims проходят ordinary reconciliation. Это admission protection, а не whole-source atomicity либо seamless rollback.

Coordinated same-algorithm key-ID rotation не квалифицирована: нужны receive overlap, TX cutover, old-key retirement, replay continuity и peer interop. Key store не включает rotation. Источники: [admission](../../../internal/bfd/manager_auth_reload.go), [tests](../../../internal/bfd/manager_auth_reload_test.go), [RFC 5880 §6.7](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.7).

### Source reconciliation и replacement

Desired resources готовятся/claim до stale release. Old claims освобождаются только при Failed=0 и ResourcePending=0; TimerPending отдельно. Fatal preparation откатывает new claims; valid disjoint claims могут оставаться до resource-pending retry.

Receive-key collision разрешает prepared SingleHop/MultiHop swap только для interface-empty base identity, одного config owner, отсутствия old key в desired и неизменных остальных identity fields. Другие collisions отказывают до sender factory и, для base admission, generation acceptance. Cancellation до publication сохраняет old identity и освобождает prepared lease/discriminator один раз.

После publication captured old session фиксирует AdminDown и выполняет drain вне mutation locks. Lease/discriminator сохраняются до completion либо typed incomplete failure и освобождаются один раз. Retiring exact key зарезервирован до owner join, lease/discriminator release и metrics cleanup, включая errors. Add/reverse rekey возвращает `ErrDuplicateSession`/`AlreadyExists` до factory; явный retry после Delete создаёт следующую incarnation. Automatic retry не добавляется.

Disruptive replacement требует отдельного explicit intent с identity/transition и client impact. До peer visibility failure откатывает preparation. После cutover seamless rollback не гарантируется. AdminDown не обещает сохранение BGP/forwarding. [Same-key FRR fixture](../../../test/interop/same_key_topology_test.go) проверяет conflict при занятом old UDP inode, closure до retry и old/new discriminator epochs. Peer Down допустим. Passing same-key wire receipt обязателен на exact candidate.

Источники: [replacement](../../../internal/bfd/manager_replace.go), [reconciliation](../../../cmd/gobfd/reconciliation.go), [RFC 5880 §6.8.16](https://www.rfc-editor.org/rfc/rfc5880.html#section-6.8.16), [RFC 5882 §§3.2, 4.3](https://www.rfc-editor.org/rfc/rfc5882.html#section-3.2).

### Overlay resource ownership

Pure preflight охватывает весь VXLAN/Geneve source до backend open/`SetScopes`. Duplicate desired wire identities недопустимы. Unequal scopes одного live/draining wire, включая VXLAN local-MAC change, требуют отдельного removal/recreation. Unused never-committed preparation может быть заменена.

После успешного preflight удаляются obsolete unused preparations до claims. Сохраняются existing scopes в current desired union positive physical SenderLease refs. Actual factory увеличивает ref; registered session держит его через drain до idempotent lease close. Unused/unpublished leases имеют once-only cleanup без drain.

Incomplete acquisition возвращает Pending/Failed до Manager apply/release source, сохраняя live/draining exact scopes/backends. Desired preparations могут остаться для retry. Это source-local protection, а не global pre-generation admission либо all-source atomicity. Источник: [overlay owner](../../../cmd/gobfd/overlay_owner.go).

### Availability и receipts

Receipts содержат bounded presence flags interface/address classes без resource identity/raw errors. Смена class обновляет status при прежнем pending count; identical receipts deduplicate, convergence очищает flags. Timer pending и permanent failures имеют отдельные retry/accounting.

Availability проверяет listeners, outer overlay endpoints и bound interfaces. Потеря адреса сохраняет desired generation/claims, задаёт pending и health not ready; recovery повторяет ту же generation. Двухсекундная проверка охватывает пять sources без monitor events. Inventory error задаёт failed и требует явный SIGHUP. Источник: [coordinator](../../../cmd/gobfd/reconciliation.go).

## Последствия

Поддерживаемые changes сохраняют local identity. Peer timing, padding acceptance и forwarding требуют измерений. RFC 9764 §4.1 допускает Down прежней Up session, если peer не принимает padded Control. Cloud/hardware limits, Async/Echo и graceful restart требуют target profiles; RFC 7419 intervals не подтверждают acceptance.

### Qualification contract

Используйте existing actual-wire harness; generation JSON не доказывает wire Poll/Final. Scenario-selected receipt expectations независимы от capture validation: immediate update сходится сразу либо после owner queueing; existing Poll phases требуют pending-before-applied.

- DetectMult: authenticated Async 1→5→3 и remote-Demand 5→3 при negotiated 300 мс. Expected jitter mean: 247,5 мс при multiplier 1, иначе 262,5 мс. Local multiplier не меняет local RX D, использующий received multiplier. Crafted remote-Demand 5×500 мс задаёт derived Poll bound; peer не имеет detection timer. Responsive Finals/quiet window не квалифицируют missing-Final expiry/general Demand. Один old in-flight Control допустим только первым после receipt внутри slack; new Control приходит за один TX плюс slack.
- TTL: authenticated UDP/4784 startup; peer останавливается до reload. Replay последнего accepted payload при допустимом TTL даёт ровно один auth failure и ноль accepted input. Затем signed Poll ниже floor и identical payload/sequence на floor; только второй принимается либо вызывает Final. Общий identity/counter/auth/capture baseline охватывает семь boundaries и remote-Demand entry; probes завершаются внутри прежнего D, исключая подмену continuity replay expiry.
- Padding: один capture cursor/auth RX state на направление для resize/rejections; failed/incomplete evaluation не продвигает их. Local 128→192→128 не меняет peer target и не доказывает bidirectional MTU. DF, UDP payload, BFD Length, zero padding и MAC domain проверяются отдельно. Auth5 — startup-only profile отдельно от auth4. Aggregate/named BFD health проверяются через owned container management Unix socket тем же compiled interop executable; TCP остаётся loopback-only.
- Final gate: ограничивайте TShark results после display filtering; `-c` не доказывает absence. Отсутствие обязательного initial-state/expiry evidence даёт failure. До test-image cleanup сохраните immutable image IDs и требуемые OCI source/base annotations. Runner paths не становятся engine-host bind paths.

### Датированные local receipts

Результаты относятся к указанному input и не являются fresh validation либо final acceptance.

| Slice | Exact source | Сохранённый результат | Evidence |
|:---|:---|:---|:---|
| Lifecycle | Frozen input указан в receipt | 1049 PASS, 0 FAIL; 5 packages, 2 environment skips; 27 budgets, 577 inventory hashes совпадают | `reports/rekey-lifecycle-20261024/` |
| DetectMult | `d9e67064e51012c0d25a1a22dc9bd469600d8469` | 6 Async + 2 crafted remote-Demand phases | `reports/detect-wire-20261024/` |
| TTL | `491b2214994f07e017eb93ab67a29a95378c619e` | Default 162,94 с, multihop 81,98 с; 7 boundary/replay windows | `reports/ttl-wire-20261024/` |
| Padding | `b97729458a696aee21023ba147af51417ed9ff7b` | 8 resize + 7 rejection phases, 17 peer windows; health SERVING | `reports/padding-wire-20261024/` |
| Default qualifier correction | `e4e79a8eee8e7c38bfc78321bdc5e9030909c214` | Local acceptance corrected qualifier | Общая квалификация пяти полей |

Lifecycle receipt также сохраняет zero lint issues, vet/два benchmark exits zero и overlay lease cleanup qualification. Три padding runs сохраняют cap 512 MiB, zero max/OOM/swap, 853 source matches и 675 sidecar matches.

Receipts DetectMult, packet policy, authentication admission и lifecycle подтверждают source slices, а не stable acceptance. Обязательная actual-wire qualification зависит от завершённой lifecycle work. Final four-profile/five-field gate выполняется после production Session/Echo thread/scale work и квалифицирует exact candidate до v1. Позднее production change не наследует прежние receipts.

## Источники

- [Configuration/receipt reference](../03-configuration.md).
- [Interop](../05-interop.md), [RFC coverage](../08-rfc-compliance.md).
- [RFC 5880 §§6.6, 6.8.3, 6.8.12](../../rfc/rfc5880.txt), [RFC 9764 §§3–4.1](../../rfc/rfc9764.txt).
- [Target peer references](../reference/vendor-bfd.md).
