# Соответствие RFC

[![RFC 5880](https://img.shields.io/badge/RFC_5880-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5880)
[![RFC 5881](https://img.shields.io/badge/RFC_5881-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5881)
[![RFC 5882](https://img.shields.io/badge/RFC_5882-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5882)
[![RFC 5883](https://img.shields.io/badge/RFC_5883-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5883)
[![RFC 7419](https://img.shields.io/badge/RFC_7419-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7419)
[![RFC 9384](https://img.shields.io/badge/RFC_9384-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9384)
[![RFC 9468](https://img.shields.io/badge/RFC_9468-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9468)
[![RFC 9747](https://img.shields.io/badge/RFC_9747-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9747)
[![RFC 7130](https://img.shields.io/badge/RFC_7130-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7130)
[![RFC 8971](https://img.shields.io/badge/RFC_8971-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc8971)
[![RFC 9521](https://img.shields.io/badge/RFC_9521-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9521)
[![RFC 9764](https://img.shields.io/badge/RFC_9764-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9764)
[![RFC 7880](https://img.shields.io/badge/RFC_7880-Planned-2196f3?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7880)
[![RFC 7881](https://img.shields.io/badge/RFC_7881-Planned-2196f3?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7881)
[![RFC 5884](https://img.shields.io/badge/RFC_5884-Stub-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5884)

> Матрица соответствия RFC, постраничные заметки по реализации, обоснование дизайна и ссылки на исходные тексты RFC.

---

### Содержание

- [Матрица соответствия](#матрица-соответствия)
- [Заметки по RFC 5880](#заметки-по-rfc-5880)
- [Заметки по RFC 5881](#заметки-по-rfc-5881)
- [Заметки по RFC 5882](#заметки-по-rfc-5882)
- [Заметки по RFC 5883](#заметки-по-rfc-5883)
- [Заметки по RFC 7419](#заметки-по-rfc-7419)
- [Заметки по RFC 9384](#заметки-по-rfc-9384)
- [Заметки по RFC 9468](#заметки-по-rfc-9468)
- [Заметки по RFC 9747](#заметки-по-rfc-9747)
- [Заметки по RFC 7130](#заметки-по-rfc-7130)
- [Заметки по RFC 8971](#заметки-по-rfc-8971)
- [Заметки по RFC 9521](#заметки-по-rfc-9521)
- [Заметки по RFC 9764](#заметки-по-rfc-9764)
- [RFC 7880/7881 (Планируется)](#rfc-78807881-планируется)
- [Stub-интерфейсы](#stub-интерфейсы)
- [Справочные RFC](#справочные-rfc)
- [Исходные файлы RFC](#исходные-файлы-rfc)

### Матрица соответствия

| RFC | Название | Статус | Примечания |
|---|---|---|---|
| [RFC 5880](https://datatracker.ietf.org/doc/html/rfc5880) | Базовый протокол BFD | **Реализован** | FSM, кодек, auth, таймеры, джиттер, Poll/Final |
| [RFC 5881](https://datatracker.ietf.org/doc/html/rfc5881) | BFD для IPv4/IPv6 Single-Hop | **Реализован** | UDP 3784, TTL=255, `SO_BINDTODEVICE` |
| [RFC 5882](https://datatracker.ietf.org/doc/html/rfc5882) | Общее применение BFD | **Реализован** | Интеграция с GoBGP, демпфирование flap-ов |
| [RFC 5883](https://datatracker.ietf.org/doc/html/rfc5883) | BFD для Multihop | **Реализован** | UDP 4784, проверка TTL>=254 |
| [RFC 7419](https://datatracker.ietf.org/doc/html/rfc7419) | Common Interval Support | **Реализован** | 6 общих интервалов, опциональное выравнивание |
| [RFC 9384](https://datatracker.ietf.org/doc/html/rfc9384) | BGP Cease NOTIFICATION для BFD | **Реализован** | Cease/10 subcode в строке shutdown |
| [RFC 9468](https://datatracker.ietf.org/doc/html/rfc9468) | Unsolicited BFD | **Реализован** | Автосоздание пассивных сессий, политика per-interface |
| [RFC 9747](https://datatracker.ietf.org/doc/html/rfc9747) | Unaffiliated BFD Echo | **Реализован** | EchoSession FSM, слушатель порта 3785, echo receiver, подключение к демону |
| [RFC 7130](https://datatracker.ietf.org/doc/html/rfc7130) | Micro-BFD для LAG | **Реализован** | MicroBFDGroup, per-member сессии, порт 6784, `SO_BINDTODEVICE`, RunDispatch |
| [RFC 8971](https://datatracker.ietf.org/doc/html/rfc8971) | BFD для VXLAN туннелей | **Реализован** | VXLANConn порт 4789, сборка inner-пакетов, OverlaySender/Receiver, подключение к демону |
| [RFC 9521](https://datatracker.ietf.org/doc/html/rfc9521) | BFD для Geneve туннелей | **Реализован** | GeneveConn порт 6081, O=1/C=0, сборка inner-пакетов, OverlaySender/Receiver, подключение к демону |
| [RFC 9764](https://datatracker.ietf.org/doc/html/rfc9764) | BFD Large Packets | **Реализован** | PaddedPduSize, бит DF (`IP_PMTUDISC_DO`), zero-padding в TX-пути |
| [RFC 7880](https://datatracker.ietf.org/doc/html/rfc7880) | Seamless BFD Base | **Планируется** | Stateless рефлектор + инициатор для проверки инфраструктуры |
| [RFC 7881](https://datatracker.ietf.org/doc/html/rfc7881) | S-BFD для IPv4/IPv6 | **Планируется** | Инкапсуляция на порт 7784 для S-BFD |
| [RFC 5884](https://datatracker.ietf.org/doc/html/rfc5884) | BFD для MPLS LSP | **Stub** | Интерфейсы определены, ожидает LSP Ping |
| [RFC 5885](https://datatracker.ietf.org/doc/html/rfc5885) | BFD для PW VCCV | **Stub** | Интерфейсы определены, ожидает VCCV/LDP |

> Traditional Echo Mode (RFC 5880 Section 6.4, affiliated с контрольной сессией) и Demand Mode (Section 6.6) намеренно не реализованы. Асинхронный режим покрывает основной сценарий BFD-assisted failover в ISP/DC. Unaffiliated echo (RFC 9747) реализован как автономный тест forwarding-path без контрольной сессии.

### Заметки по RFC 5880

#### Section 4.1: Формат пакета

Реализация: [`internal/bfd/packet.go`](../../internal/bfd/packet.go). 24-байтный заголовок кодируется/декодируется через `encoding/binary.BigEndian`. Zero-allocation кодек с `sync.Pool`.

См. [02-protocol.md](./02-protocol.md) для полной таблицы формата пакета.

#### Section 6.1: Переменные состояния

Реализация: [`internal/bfd/session.go`](../../internal/bfd/session.go). Все обязательные переменные реализованы. Потокобезопасность через `atomic.Uint32`.

#### Section 6.2: FSM

Реализация: [`internal/bfd/fsm.go`](../../internal/bfd/fsm.go). Table-driven FSM с `map[stateEvent]transition`. Чистая функция без побочных эффектов. Все 16 переходов из Section 6.8.6 реализованы.

#### Section 6.3: Демультиплексирование

Реализация: [`internal/bfd/manager.go`](../../internal/bfd/manager.go). Двухуровневое демультиплексирование:
- Уровень 1: O(1) поиск по Your Discriminator (быстрый путь)
- Уровень 2: Составной ключ (SrcIP, DstIP, Interface) для установления сессии

#### Section 6.5: Poll-последовательности

Только одна активная Poll-последовательность. Ожидающие изменения параметров применяются только после получения бита Final.

#### Section 6.7: Аутентификация

Реализация: [`internal/bfd/auth.go`](../../internal/bfd/auth.go). Реализованы все пять типов:

| Тип | Реализация |
|---|---|
| Simple Password (1) | `SimplePasswordAuth` |
| Keyed MD5 (2) | `KeyedMD5Auth` |
| Meticulous Keyed MD5 (3) | `MeticulousKeyedMD5Auth` |
| Keyed SHA1 (4) | `KeyedSHA1Auth` |
| Meticulous Keyed SHA1 (5) | `MeticulousKeyedSHA1Auth` |

Ключевые особенности:
- Meticulous варианты инкрементируют последовательность на каждый пакет; non-meticulous -- только при смене состояния
- Окно последовательности: `3 * DetectMult` для non-meticulous
- `AuthKeyStore` поддерживает несколько ключей для бесшовной ротации

#### Section 6.8.6: Приём пакетов

Валидация разделена на два уровня:

| Уровень | Шаги | Файл |
|---|---|---|
| Кодек | 1-7 (без состояния) | `packet.go` |
| Сессия | 8-18 (с состоянием) | `session.go` |

#### Section 6.8.7: Джиттер

- Нормальный (DetectMult > 1): 75-100% интервала
- DetectMult == 1: 75-90% интервала
- Используется `math/rand/v2`

#### Section 6.8.16: Административное управление

Graceful shutdown отправляет AdminDown с Diag=7, ожидает 2x TX-интервал, затем отменяет горутины.

#### Не реализовано

| Секция | Функция | Обоснование |
|---|---|---|
| 6.4 | Affiliated Echo Mode | Требует контрольной сессии; RFC 9747 unaffiliated echo реализован вместо |
| 6.6 | Demand Mode | Редко используется; настройка интервалов достигает той же цели |
| 4.1 | Бит Multipoint | Зарезервирован для будущих P2MP расширений |

### Заметки по RFC 5881

Реализация: [`internal/netio/`](../../internal/netio/)

| Требование | Реализация |
|---|---|
| Порт назначения 3784 | `netio.PortSingleHop = 3784` |
| Порт источника 49152-65535 | `SourcePortAllocator` |
| TTL=255 исходящий | `ipv4.SetTTL(255)` через `x/net/ipv4` |
| Проверка TTL=255 входящий | `IP_RECVTTL` + проверка в слушателе |
| `SO_BINDTODEVICE` | Применяется при указании интерфейса |
| Раздельные IPv4/IPv6 слушатели | Раздельные `ipv4.PacketConn` / `ipv6.PacketConn` |

### Заметки по RFC 5882

Реализация: [`internal/gobgp/`](../../internal/gobgp/)

- Section 3.2: демпфирование flap-ов с настраиваемыми порогами
- Section 4.3: отслеживание состояний BFD и вызов GoBGP gRPC API
  - BFD Down --> `DisablePeer()` (или `DeletePath()` по стратегии)
  - BFD Up --> `EnablePeer()` (или `AddPath()`)

### Заметки по RFC 5883

| Требование | Реализация |
|---|---|
| Порт назначения 4784 | `netio.PortMultiHop = 4784` |
| TTL=255 исходящий | Аналогично single-hop |
| Проверка TTL>=254 входящий | Отдельная валидация TTL для multihop |
| Демультиплексирование по (MyDiscr, SrcIP, DstIP) | `Manager.DemuxWithWire` составной ключ |

### Заметки по RFC 7419

Реализация: [`internal/bfd/intervals.go`](../../internal/bfd/intervals.go)

RFC 7419 определяет набор общих интервалов BFD для совместимости с аппаратными реализациями: 3.3мс, 10мс, 20мс, 50мс, 100мс, 1с. Опция `bfd.align_intervals: true` выравнивает интервалы вверх до ближайшего общего значения.

### Заметки по RFC 9384

Реализация: [`internal/gobgp/rfc9384.go`](../../internal/gobgp/rfc9384.go)

RFC 9384 определяет Cease NOTIFICATION subcode 10 ("BFD Down") для BGP-сессий, разорванных из-за BFD. Строка communication обогащена `"BFD Down (RFC 9384 Cease/10): diag=..."`.

### Заметки по RFC 9468

Реализация: [`internal/bfd/unsolicited.go`](../../internal/bfd/unsolicited.go), [`internal/bfd/manager.go`](../../internal/bfd/manager.go)

RFC 9468 позволяет динамически создавать пассивные сессии при получении BFD-пакетов от неизвестных пиров. Автосоздание в `Manager.demuxByPeer()` с политикой per-interface и ограничением `MaxSessions`.

### Заметки по RFC 9747

**Статус**: Реализован

Реализация: [`internal/bfd/echo.go`](../../internal/bfd/echo.go), [`internal/netio/echo_receiver.go`](../../internal/netio/echo_receiver.go)

RFC 9747 определяет unaffiliated BFD echo для обнаружения отказов forwarding-path без необходимости запуска BFD на удалённой стороне. Локальная система отправляет BFD Control пакеты (echo-пакеты) на удалённую сторону, которая пересылает их обратно через обычную IP-маршрутизацию.

| Требование | Реализация |
|---|---|
| UDP порт 3785 | `netio.PortEcho = 3785`, слушатель в `createListeners()` |
| Стандартный формат BFD Control | Переиспользование кодека `MarshalControlPacket` |
| DiagEchoFailed при таймауте | `DiagEchoFailed` (значение 2) |
| Локально настроенные таймеры | `EchoSessionConfig.TxInterval`, без согласования |
| Двухсостоянийная FSM (Up/Down) | Упрощённая FSM в `EchoSession` |
| DetectionTime = DetectMult * TxInterval | `EchoSession.DetectionTime()` |
| Демультиплексирование по MyDiscriminator | `EchoReceiver` сопоставляет возвращённые пакеты |
| Тип сессии | Константа `SessionTypeEcho` |
| TTL 255 отправка, TTL >= 254 приём | Валидация GTSM через `netio.ValidateTTL` |
| Декларативные echo-пиры | `echo.peers[]` в конфиге, реконсиляция при SIGHUP |
| Отправитель с портом назначения 3785 | Функциональная опция `WithDstPort(PortEcho)` |

Ключевые отличия от BFD control сессий:
- Нет трёхстороннего рукопожатия (нет состояния Init)
- Нет согласования таймеров с удалённой стороной (локальные настройки)
- Нет аутентификации (echo-пакеты отправлены самим собой)
- Отдельный тип `EchoSession` с упрощённой FSM

### Заметки по RFC 7130

**Статус**: Реализован

Реализация: [`internal/bfd/micro.go`](../../internal/bfd/micro.go)

RFC 7130 определяет Micro-BFD — независимые BFD-сессии на каждом member link LAG для верификации пересылки per-link с более быстрым обнаружением, чем LACP.

| Требование | Реализация |
|---|---|
| UDP порт 6784 | `netio.PortMicroBFD = 6784`, per-member слушатели в `createMicroBFDListeners()` |
| Одна BFD-сессия на member link | `MicroBFDGroup.members` map, `AddMember()`/`RemoveMember()` |
| `SO_BINDTODEVICE` на каждый member | Функциональная опция `WithBindDevice()` на отправителе |
| Отслеживание агрегатного состояния | Порог `upCount >= minActive` |
| Member удалён при BFD Down | `UpdateMemberState()` вызывает изменение агрегата |
| Выделенный multicast MAC | `01-00-5E-90-00-01` для начальных пакетов |
| Только асинхронный режим | Стандартные процедуры RFC 5880 для каждого member |
| Тип сессии | Константа `SessionTypeMicroBFD` |
| Конфигурация per-group | `MicroBFDGroupConfig` с LAG-интерфейсом + member-линки |
| Реконсиляция групп | `reconcileMicroBFDGroups()` в `main.go`, SIGHUP reload |
| Диспетчер состояний | `RunDispatch` fan-out горутина маршрутизирует изменения состояний в группы |

Логика агрегатного состояния:
- Группа стартует со всеми member-ами Down, агрегат Down
- Когда `upCount >= MinActiveLinks`, агрегат переходит в Up
- Когда `upCount < MinActiveLinks`, агрегат переходит в Down
- Изменения состояния сообщаются только при переходах агрегата (пересечение порога)
- Состояние Init на member не считается как Up (только `StateUp` увеличивает `upCount`)

`MicroBFDGroupSnapshot` предоставляет read-only представление состояния группы включая детали per-member link, полезно для ответов gRPC API и мониторинга.

### Заметки по RFC 8971

**Статус**: Реализован

Реализация: [`internal/netio/vxlan.go`](../../internal/netio/vxlan.go), [`internal/netio/vxlan_conn.go`](../../internal/netio/vxlan_conn.go), [`internal/netio/overlay.go`](../../internal/netio/overlay.go), [`internal/netio/overlay_inner.go`](../../internal/netio/overlay_inner.go)

RFC 8971 определяет BFD в VXLAN-инкапсуляции для обнаружения отказов forwarding-path между VTEP (Virtual Tunnel Endpoints). BFD Control пакеты переносятся внутри VXLAN-инкапсулированных inner Ethernet фреймов.

| Требование | Реализация |
|---|---|
| Внешний UDP порт 4789 | `netio.VXLANPort = 4789`, сокет `VXLANConn` |
| Внутренний UDP порт 3784 | `BuildInnerPacket()` с dst-портом 3784 |
| Кодек VXLAN-заголовка | `MarshalVXLANHeader` / `UnmarshalVXLANHeader` |
| Management VNI | `VXLANConfig.ManagementVNI`, отклонение при несовпадении VNI |
| Валидация VNI (24 бит) | `ErrInvalidVXLANVNI` валидация конфигурации |
| Валидация I-флага | Сентинел `ErrVXLANInvalidFlags` |
| Inner destination MAC | `VXLANBFDInnerMAC = 00:52:02:00:00:00` (IANA) |
| Inner TTL=255 | `BuildInnerPacket()` устанавливает TTL=255 (RFC 5881 GTSM) |
| Inner IPv4 checksum | `ipv4HeaderChecksum()` по RFC 1071 |
| Тип сессии | Константа `SessionTypeVXLAN` |
| Адаптер OverlaySender | `OverlaySender` реализует `bfd.PacketSender` |
| Цикл OverlayReceiver | Снимает VXLAN + inner заголовки, доставляет в `Manager.DemuxWithWire` |
| Декларативные пиры | `vxlan.peers[]` в конфиге, реконсиляция при SIGHUP |
| Валидация конфигурации | Диапазон VNI, адреса пиров, detect_mult, обнаружение дубликатов |

Стек инкапсуляции пакетов:
```
Outer IP → Outer UDP (4789) → VXLAN Header (8B) →
Inner Ethernet (14B) → Inner IPv4 (20B) → Inner UDP (8B, dst 3784) → BFD Control
```

Кодек VXLAN-заголовка обрабатывает 8-байтный фиксированный формат с I-флагом (VNI валиден) и 24-битным кодированием VNI. Пакеты Management VNI обрабатываются локально и не перенаправляются в tenant-сети.

### Заметки по RFC 9521

**Статус**: Реализован

Реализация: [`internal/netio/geneve.go`](../../internal/netio/geneve.go), [`internal/netio/geneve_conn.go`](../../internal/netio/geneve_conn.go), [`internal/netio/overlay.go`](../../internal/netio/overlay.go), [`internal/netio/overlay_inner.go`](../../internal/netio/overlay_inner.go)

RFC 9521 определяет BFD в Geneve-инкапсуляции для обнаружения отказов forwarding-path между NVE (Network Virtualization Edges) на уровне VAP (Virtual Access Point). Geneve — эволюция VXLAN для cloud-native сред.

| Требование | Реализация |
|---|---|
| Внешний UDP порт 6081 | `netio.GenevePort = 6081`, сокет `GeneveConn` |
| Кодек Geneve-заголовка | `MarshalGeneveHeader` / `UnmarshalGeneveHeader` |
| O бит (control) = 1 | RFC 9521 Section 4: установлен при отправке, проверяется при приёме (`ErrGeneveOBitNotSet`) |
| C бит (critical) = 0 | RFC 9521 Section 4: сброшен при отправке, проверяется при приёме (`ErrGeneveCBitSet`) |
| Protocol Type 0x6558 | Format A: Ethernet payload (`GeneveProtocolEthernet`), проверяется при приёме |
| Валидация VNI (24 бит) | `ErrInvalidGeneveVNI` валидация конфигурации, несовпадение VNI при приёме |
| Валидация версии | `ErrGeneveInvalidVersion` (только версия 0 поддерживается) |
| Ethernet payload (Format A) | `GeneveProtocolEthernet = 0x6558` |
| Опции переменной длины | `GeneveHeader.OptLen` + `TotalHeaderSize()` |
| Inner TTL=255 | `BuildInnerPacket()` устанавливает TTL=255 (RFC 5881 GTSM) |
| Тип сессии | Константа `SessionTypeGeneve` |
| Адаптер OverlaySender | `OverlaySender` реализует `bfd.PacketSender` |
| Цикл OverlayReceiver | Снимает Geneve + inner заголовки, доставляет в `Manager.DemuxWithWire` |
| Декларативные пиры | `geneve.peers[]` в конфиге, переопределение VNI per-peer, реконсиляция при SIGHUP |
| Валидация конфигурации | Диапазон VNI, адреса пиров, detect_mult, обнаружение дубликатов |

Стек инкапсуляции пакетов (Format A):
```
Outer IP → Outer UDP (6081) → Geneve Header (8B, O=1, C=0, Proto=0x6558) →
Inner Ethernet (14B) → Inner IPv4 (20B) → Inner UDP (8B, dst 3784) → BFD Control
```

Ключевые отличия от VXLAN BFD (RFC 8971):
- Geneve поддерживает опции TLV переменной длины (VXLAN имеет фиксированный 8-байтный заголовок)
- Два формата payload: Ethernet (Format A) и IP (Format B)
- O-бит управляющий флаг указывает на management/control трафик
- Сессии создаются/завершаются на уровне VAP, а не напрямую на NVE

### Заметки по RFC 9764

**Статус**: Реализован

Реализация: [`internal/bfd/session.go`](../../internal/bfd/session.go) (padding), [`internal/netio/sender.go`](../../internal/netio/sender.go) (бит DF)

RFC 9764 определяет BFD Large Packets для проверки MTU пути. Реализация дополняет BFD Control пакеты до настроенного размера нулями и устанавливает бит IP Don't Fragment (DF). Если дополненный пакет превышает MTU пути, он будет отброшен, что позволит BFD обнаружить проблему с MTU.

| Требование | Реализация |
|---|---|
| Дополнение пакета до настроенного размера | `SessionConfig.PaddedPduSize`, zero-padding в TX-пути |
| Установка бита DF (IP_PMTUDISC_DO) | Функциональная опция `WithDFBit()` на `UDPSender` |
| Заполнение нулями | `cachedPacket` расширяется нулевыми байтами после BFD payload |
| Конфигурация per-session | `padded_pdu_size` в YAML конфигурации сессии |
| Глобальное значение по умолчанию | `bfd.default_padded_pdu_size` в YAML конфигурации |
| Допустимый диапазон | 24-9000 байт (24 = минимальный BFD Control пакет) |

### RFC 7880/7881 (Планируется)

**Статус**: Планируется

RFC 7880 определяет Seamless BFD (S-BFD) — упрощённый механизм BFD для тестирования доступности инфраструктуры. В отличие от стандартного BFD, требующего трёхстороннего рукопожатия, S-BFD использует stateless рефлектор, который немедленно отвечает на запросы инициатора.

RFC 7881 определяет инкапсуляцию S-BFD для IPv4 и IPv6 с использованием порта назначения 7784.

| Требование | Планируемая реализация |
|---|---|
| Stateless рефлектор (RFC 7880) | `SBFDReflector` на порту 7784 |
| Сопоставление пула дискриминаторов | Рефлектор сопоставляет `YourDiscriminator` с локальным пулом |
| Ответ с State=Up | Состояние сессии не сохраняется |
| S-BFD инициатор (RFC 7880) | `SBFDInitiator` с таймером обнаружения, Down→Up при первом ответе |
| Порт 7784 (RFC 7881) | Выделенный слушатель S-BFD |
| Без трёхстороннего рукопожатия | Инициатор отправляет, рефлектор отвечает немедленно |

### Stub-интерфейсы

| RFC | Зависимость | Статус |
|---|---|---|
| RFC 5884 (BFD для MPLS) | LSP Ping (RFC 4379) | Интерфейсы определены в `internal/bfd` |
| RFC 5885 (BFD для VCCV) | VCCV (RFC 5085), LDP (RFC 4447) | Интерфейсы определены |

### Справочные RFC

Эти RFC упоминаются, но не реализуются напрямую:

| RFC | Название | Отношение |
|---|---|---|
| RFC 8203 | BGP Administrative Shutdown | Строка communication для DisablePeer |
| RFC 5082 | GTSM | Основание для требования TTL=255 |
| RFC 4379 | LSP Ping | Зависимость RFC 5884 |
| RFC 5085 | VCCV | Зависимость RFC 5885 |
| RFC 4447 | LDP | Зависимость RFC 5885 |
| RFC 7726 | Clarifying BFD for MPLS | Процедуры MPLS-сессий |
| RFC 9127 | YANG Data Model for BFD | Эталон модели конфигурации |
| RFC 9355 | OSPF BFD Strict-Mode | Требует интеграции с OSPF-демоном (отложено) |

### Исходные файлы RFC

Полные тексты RFC доступны в директории `docs/rfc/`:

| Файл | RFC |
|---|---|
| [rfc5880.txt](../rfc/rfc5880.txt) | RFC 5880 -- Bidirectional Forwarding Detection |
| [rfc5881.txt](../rfc/rfc5881.txt) | RFC 5881 -- BFD for IPv4/IPv6 (Single Hop) |
| [rfc5882.txt](../rfc/rfc5882.txt) | RFC 5882 -- Generic Application of BFD |
| [rfc5883.txt](../rfc/rfc5883.txt) | RFC 5883 -- BFD for Multihop Paths |
| [rfc5884.txt](../rfc/rfc5884.txt) | RFC 5884 -- BFD for MPLS LSPs |
| [rfc5885.txt](../rfc/rfc5885.txt) | RFC 5885 -- BFD for PW VCCV |
| [rfc7130.txt](../rfc/rfc7130.txt) | RFC 7130 -- BFD on LAG |

### Связанные документы

- [02-protocol.md](./02-protocol.md) -- Детали протокола BFD
- [01-architecture.md](./01-architecture.md) -- Архитектура системы
- [05-interop.md](./05-interop.md) -- Тестирование совместимости

---

*Последнее обновление: 2026-02-23*
