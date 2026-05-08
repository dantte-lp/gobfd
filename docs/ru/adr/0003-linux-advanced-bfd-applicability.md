# ADR-0003: Применимость Micro-BFD, VXLAN BFD и Geneve BFD в Linux

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

GoBFD поставляет userspace-реализации RFC 7130 Micro-BFD, RFC 8971 BFD
для VXLAN и RFC 9521 BFD для Geneve. У каждого режима свои
ownership-ограничения в Linux: kernel bonding, OVS, OVN,
NetworkManager, Cilium, Calico и NSX могут уже владеть
соответствующим устройством или сокетом. Единое декларативное описание
применимости и требуемого ownership предотвращает misconfiguration и
неверные production-заявления.

## Decision

GoBFD -- это userspace BFD-движок. Он определяет состояние BFD и
выполняет явно сконфигурированные enforcement-backend. Это не
универсальный Linux dataplane controller. У каждого продвинутого
режима фиксированная применимость и фиксированный набор
production-ограничений, перечисленных в этой записи.

## Consequences

### Матрица состояния

| Mode | Linux fit | Текущее состояние GoBFD | Production gap |
|---|---:|---:|---|
| Micro-BFD | High | Per-member сессии, агрегатное состояние, kernel-bond, OVSDB и NetworkManager пути enforcement | Выделенные API/CLI create-flows и расширение interop-матрицы |
| VXLAN BFD | High для VTEP/NVE проверок | `userspace-udp` VXLAN socket и codec | Owner-специфичные интеграции kernel/OVS/OVN/Cilium/Calico/NSX |
| Geneve BFD | Medium-High для OVN/NSX/OVS | `userspace-udp` Geneve socket и codec | Owner-специфичные dataplane-интеграции и rate-policy |

### Micro-BFD в Linux

RFC 7130 определяет одну независимую BFD-сессию на каждый member-линк
LAG. GoBFD реализует:

- одну сессию на каждый сконфигурированный member-линк;
- UDP destination port 6784;
- `SO_BINDTODEVICE` на member-интерфейсе;
- агрегатный учёт состояния через `MicroBFDGroup`;
- snapshots сессий для API/CLI/мониторинга;
- guarded actuator policy с режимами `disabled`, `dry-run` и `enforce`;
- backend kernel-bond через sysfs;
- нативный OVSDB bonded-port backend;
- NetworkManager D-Bus backend для NM-owned bond-профилей;
- OVS CLI fallback-тип для диагностики.

Production-ограничения:

1. Enforcement требует явного backend и `owner_policy`.
2. Модели ownership kernel bonding, OVS и NetworkManager взаимно
   несовместимы.
3. Ручные изменения оператора требуют reconciliation при рестарте
   демона.
4. Teamd и дополнительные switching-стеки остаются будущими
   интеграциями.

### VXLAN BFD в Linux

RFC 8971 определяет BFD поверх VXLAN с Management VNI для проверок
доступности VTEP/NVE. GoBFD реализует:

- внешний UDP port 4789;
- валидацию VXLAN-заголовка и сравнение Management VNI;
- сборку внутренних Ethernet/IPv4/UDP/BFD пакетов;
- локальный demux через `OverlayReceiver`;
- явный `vxlan.backend: userspace-udp`.

Production-ограничения:

1. `userspace-udp` владеет локальным сокетом UDP 4789.
2. Kernel VXLAN, OVS, OVN, Cilium, Calico или другой dataplane может
   уже владеть UDP 4789 в целевом network namespace.
3. Зарезервированные имена owner-backend должны fail closed до
   появления реализации и interop-evidence.
4. Стартовая диагностика должна сообщать о конфликтах ownership
   сокета с actionable-ошибками.

### Geneve BFD в Linux

RFC 9521 определяет BFD для point-to-point Geneve-туннелей. GoBFD
реализует:

- внешний UDP port 6081;
- бит `O` установлен, бит `C` сброшен;
- Protocol Type `0x6558`;
- валидацию VNI;
- сборку внутренних Ethernet/IPv4/UDP/BFD пакетов;
- явный `geneve.backend: userspace-udp`.

Production-ограничения:

1. `userspace-udp` владеет локальным сокетом UDP 6081.
2. OVS, OVN, NSX или другой Geneve-dataplane может уже владеть тем же
   сокетом.
3. RFC 9521 требует контролируемых traffic-условий или provisioned
   BFD-rate, чтобы избежать ложных отказов из-за congestion.
4. Owner-специфичные backend требуют отдельной реализации и
   interop-тестов.

### Матрица применимости

| Сценарий | Fit | Требуемый ownership |
|---|---:|---|
| Linux router либо NFV-appliance с LACP-uplinks | High | Backend kernel bonding, OVS либо NetworkManager выбран явно |
| Bare-metal Kubernetes node | Medium | `hostNetwork` и host-interface ownership policy |
| EVPN/VXLAN validation endpoint | High | Выделенный Management VNI endpoint либо owner-специфичный VXLAN backend |
| OVN/NSX/Geneve gateway | Medium-High | Выделенный endpoint либо owner-специфичный Geneve backend |
| Interop-лаборатория против EOS/FRR/Linux | High | Явный namespace, socket и interface ownership |
| Generic application-host без LAG/overlay ownership | Low | Предпочтителен внешний routing-демон либо BFD сетевого устройства |

## References

- RFC 7130: <https://datatracker.ietf.org/doc/html/rfc7130>
- RFC 8971: <https://datatracker.ietf.org/doc/html/rfc8971>
- RFC 9521: <https://datatracker.ietf.org/doc/html/rfc9521>
- Linux `netlink(7)`: <https://man7.org/linux/man-pages/man7/netlink.7.html>
- Linux `rtnetlink(7)`:
  <https://man7.org/linux/man-pages/man7/rtnetlink.7.html>
- Документация bonding ядра Linux:
  <https://docs.kernel.org/networking/bonding.html>
- Open vSwitch OVSDB reference:
  <https://docs.openvswitch.org/en/stable/ref/ovsdb.7/>
- NetworkManager D-Bus API:
  <https://networkmanager.dev/docs/api/latest/spec.html>
