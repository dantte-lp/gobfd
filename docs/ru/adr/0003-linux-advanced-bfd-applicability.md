# ADR-0003: Применимость Micro-BFD, VXLAN BFD и Geneve BFD в Linux

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-05-01 |

## Контекст

Advanced modes используют Linux resources, которыми владеют bonding, OVS, OVN, NetworkManager либо overlay dataplane. Protocol detection не разрешает второму controller менять эти ресурсы.

## Решение

GoBFD — userspace BFD engine с явно выбранными enforcement adapters. Каждый mode имеет собственную ownership boundary.

| Mode | Реализованный profile | Deployment boundary |
|:---|:---|:---|
| Micro-BFD | Member sessions, aggregate state, API/CLI preview, guarded actuators | Dedicated member-wire/MAC и actual-owner qualification обязательны |
| VXLAN BFD | Exact IPv4 `userspace-udp` | Lab/isolated endpoint с UDP/4789 |
| Geneve BFD | Exact IPv4 Format A `userspace-udp` | Lab/isolated VAP endpoint с UDP/6081 |

Shared Linux sender требует fixed source port 49152–65535, включая Echo как product policy. TTL/Hop Limit и interface indices используют native Linux ancillary byte order; destination IP — network order. Intended linux/amd64 и linux/arm64 требуют native kernel evidence отдельно от source correction и cross-compilation. Источники: [sender](../../../internal/netio/sender.go), [raw sockets](../../../internal/netio/rawsock.go).

### Micro-BFD в Linux

Каждый configured member/family имеет Control session UDP/6784 и member-device binding. `MicroBFDGroup` считает Up-member threshold. Режимы actuator: disabled, dry-run, enforce; каждый вызов выполняется вне Manager locks с context пять секунд.

| Backend | Mutation | Required authority |
|:---|:---|:---|
| Kernel bond | Запись `-member`/`+member` в bonding sysfs | Явная policy `allow-external` |
| OVS | Native OVSDB mutation `Port.interfaces` | Явная OVS owner policy |
| NetworkManager | Deactivate/reactivate bond port profiles по D-Bus | Явная policy `networkmanager-dbus` |
| OVS CLI fallback | Явно создаваемый diagnostic/fallback adapter | Не выбирается daemon configuration |

Ownership models backends различаются. Restart reconciliation учитывает manual changes. Teamd и другие switching stacks остаются future integrations. Источники: [actuator selection](../../../internal/netio/lag_backend.go), [member wiring](../../../cmd/gobfd/micro_bfd.go), [OVSDB](../../../internal/netio/lag_ovsdb.go), [NetworkManager](../../../internal/netio/lag_networkmanager.go).

### Граница выделений actuator Micro-BFD — принята 2026-10-29

Сохранить существующие нулевые бюджеты выделений packet/FSM/timer и Micro-BFD dispatch без actuator, включая включённое JSON-журналирование. Каждый вызов actuator получает новый `context.WithTimeout(parent, 5*time.Second)` с отложенной отменой. Сохраняются более ранний deadline и отмена родителя; удержанный контекст сохраняет исходную семантику `Deadline`, `Done`, `Err` и `Value`. Не возвращать выданный контекст в pool и не сбрасывать его ради показателей выделений.

Для квалификации на закреплённом Go 1.27.3 linux/amd64 неизменённый benchmark с `Background` и пустым actuator имеет бюджет не более 272 B/op и 4 allocs/op: три прогона минимум по 200000 итераций. Пустой callback не читает `Done`; его чтение, другой родитель и работа реального backend могут добавлять выделения. Бюджет относится только к этому сценарию. Измерять выделения backend и задержку применения состояния отдельно; компонентный результат не квалифицирует владение, wire behavior, масштаб или сходимость.

Единственный dispatcher вызывает actuator без блокировок Manager, после рассылки текущего уведомления. Медленный вызов задерживает следующие уведомления. Отмена требует сотрудничества вызываемого кода: пятисекундный контекст не прерывает принудительно произвольный callback или файловую операцию и не гарантирует время применения состояния. Сохраняются preview, отключение по умолчанию и явные границы владения.

Квалификация: политика принята; финальный кандидат требует собственных измерений и существующих runtime-проверок. Источники: [передача управления](../../../internal/bfd/manager_micro.go), [сценарии выделений](../../../internal/bfd/manager_micro_bench_test.go), [проверка удержанного контекста](../../../internal/bfd/manager_micro_context_test.go), [контракт Go context](https://pkg.go.dev/context), [границы benchmarks](../12-benchmarks.md).

### Граница доверия NetworkManager

Connection setup и property replies подчиняются caller cancellation. Connector использует `DialContext` до godbus authentication, Hello и Unix FD negotiation. Другой address пробуется только при transport failure; authentication/Hello не повторяются на другом peer. `godbus_no_tcp` отклоняет TCP/nonce-TCP до DNS.

Nonce input должен быть trusted local regular file ровно 16 байт. Bounded reads и nonblocking open отклоняют malformed files/pipes; filesystem calls на stalled mount остаются неограниченными.

Active/cached profile paths — hints. Повторно загрузите settings до deactivate, active no-op либо cached activate. Принимайте только requested bond port; отклоняйте malformed/conflicting aliases и deleted/reprofiled cached connections. Проверяйте optional interface binding против resolved device. UUID controllers разрешаются через `GetConnectionByUuid` с требованием requested bond interface и сохранением deprecated bond-setting fallback. Unbound controller требует actual active-profile binding на этом bond device.

Все используемые RPC replies требуют exact type/arity. Property replies сохраняют Variant envelope; UUID lookup требует один nonroot object path; available-profile list не допускает root, но может быть пустым; inactive sentinel допустим; deactivate требует empty reply. Typed remote/context errors сохраняются; malformed values возвращают `ErrNetworkManagerSettingsInvalid`.

Checks и mutations — отдельные вызовы. Concurrent administrator edits требуют координации; invalid reply detection не отменяет уже выполненную remote mutation. Local private-bus fixtures не квалифицируют actual ownership либо member-wire enforcement. Reply-shape и real-owner qualification отдельны. Источники: [D-Bus adapter](../../../internal/netio/lag_networkmanager.go), [tests](../../../internal/netio/lag_networkmanager_test.go).

### VXLAN и Geneve в Linux

| Свойство | VXLAN | Geneve |
|:---|:---|:---|
| Outer UDP destination | 4789 | 6081 |
| Envelope | VXLAN I flag, Management VNI | Version 0, O=1, C=0, Protocol `0x6558` |
| Inner profile | Ethernet/IPv4/UDP/BFD, exact endpoint/MAC scope | Ethernet/IPv4 Format A, explicit exact VAP IP/MAC |
| Ownership | Local socket принадлежит GoBFD | Local socket принадлежит GoBFD |
| Unsupported owners | Kernel/OVS/OVN/Cilium/Calico/NSX names fail closed | Те же reserved names fail closed |

Kernel/vendor overlay с тем же local address/port/namespace находится вне userspace profile. Missing/special-use Geneve VAP identity, IPv6 inner payload и Format B отклоняются. RFC 9521 traffic требует controlled/provisioned rates; congestion может создавать false failures. Источники: [backend admission](../../../internal/netio/overlay_backend.go), [inner framing](../../../internal/netio/overlay_inner.go), [RFC 8971](../../rfc/rfc8971.txt), [RFC 9521](../../rfc/rfc9521.txt).

## Последствия

| Сценарий | Применимость | Обязательное условие |
|:---|:---|:---|
| Linux router/NFV LACP uplink | Candidate Micro-BFD profile | Явное kernel/OVS/NM ownership и qualified actuator |
| Bare-metal Kubernetes | Условная | Host namespace и явная host-interface authority |
| EVPN/VXLAN validation | Lab endpoint | Изолированные Management VNI/socket |
| OVN/NSX/Geneve gateway | Lab endpoint | Явная IPv4 VAP identity и isolated socket |
| Generic application host | Ограниченная | External routing/device BFD — отдельная integration |

Ни один mode не является универсальным Linux dataplane controller. Local implementation, vendor documentation и socket ownership не заменяют actual path/owner qualification. Проверьте выбранный [vendor profile](../reference/vendor-bfd.md).

## Источники

- [RFC 7130](../../rfc/rfc7130.txt), [RFC 8971](../../rfc/rfc8971.txt), [RFC 9521](../../rfc/rfc9521.txt).
- [NetworkManager D-Bus API](https://networkmanager.dev/docs/api/latest/spec.html).
- [RFC coverage](../08-rfc-compliance.md).
