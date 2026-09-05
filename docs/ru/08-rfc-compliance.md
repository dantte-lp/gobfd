# Соответствие RFC

[![RFC 5880](https://img.shields.io/badge/RFC_5880-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5880)
[![RFC 5881](https://img.shields.io/badge/RFC_5881-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5881)
[![RFC 5882](https://img.shields.io/badge/RFC_5882-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5882)
[![RFC 5883](https://img.shields.io/badge/RFC_5883-Constrained-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5883)
[![RFC 7419](https://img.shields.io/badge/RFC_7419-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7419)
[![RFC 9384](https://img.shields.io/badge/RFC_9384-Not_Implemented-ea4335?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9384)
[![RFC 9468](https://img.shields.io/badge/RFC_9468-Unsafe_Preview-ea4335?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9468)
[![RFC 9747](https://img.shields.io/badge/RFC_9747-Preview-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9747)
[![RFC 7130](https://img.shields.io/badge/RFC_7130-Partial_Production-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7130)
[![RFC 8971](https://img.shields.io/badge/RFC_8971-Unsafe_Preview-ea4335?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc8971)
[![RFC 9521](https://img.shields.io/badge/RFC_9521-Unsafe_Preview-ea4335?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9521)
[![RFC 9764](https://img.shields.io/badge/RFC_9764-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9764)
[![RFC 7880](https://img.shields.io/badge/RFC_7880-Planned-2196f3?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7880)
[![RFC 7881](https://img.shields.io/badge/RFC_7881-Planned-2196f3?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7881)
[![RFC 5884](https://img.shields.io/badge/RFC_5884-Stub-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5884)

> Матрица соответствия RFC, постраничные заметки по реализации, обоснование дизайна и ссылки на исходные тексты RFC.

---

## Содержание

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
| [RFC 5880](https://datatracker.ietf.org/doc/html/rfc5880) | Базовый протокол BFD | **Асинхронное ядро; частично** | Ответ Final на входящий Poll есть; локальный/crossed Poll и Demand procedures не завершены |
| [RFC 5881](https://datatracker.ietf.org/doc/html/rfc5881) | BFD для IPv4/IPv6 Single-Hop | **Частично; numbered-multiaccess profile** | UDP 3784, TTL=255 и явная привязка к интерфейсу реализованы; initial demux покрыт не полностью |
| [RFC 5882](https://datatracker.ietf.org/doc/html/rfc5882) | Общее применение BFD | **Application integration частичная** | State delivery и actuator convergence не завершены; penalty dampening является implementation policy |
| [RFC 5883](https://datatracker.ietf.org/doc/html/rfc5883) | BFD для Multihop | **Ограниченный GTSM profile** | UDP 4784 с TTL>=254 допускает не более одного промежуточного router; arbitrary-hop qualification не завершена |
| [RFC 7419](https://datatracker.ietf.org/doc/html/rfc7419) | Common Interval Support | **Реализован** | 6 общих интервалов, опциональное выравнивание |
| [RFC 9384](https://datatracker.ietf.org/doc/html/rfc9384) | BGP Cease NOTIFICATION для BFD | **Не реализован** | GoBGP v3 отправляет Cease/2; Cease/10 присутствует только в операторском тексте |
| [RFC 9468](https://datatracker.ietf.org/doc/html/rfc9468) | Unsolicited BFD | **Небезопасный preview** | Пустой prefix policy принимает любой source, неположительный session limit не ограничен |
| [RFC 9747](https://datatracker.ietf.org/doc/html/rfc9747) | Unaffiliated BFD Echo | **Preview** | Echo session и port 3785 wiring существуют; полная RFC-квалификация не завершена |
| [RFC 7130](https://datatracker.ietf.org/doc/html/rfc7130) | Micro-BFD для LAG | **Preview; owner integration частичная** | Протокол и отдельные actuators существуют; production ownership ограничен |
| [RFC 8971](https://datatracker.ietf.org/doc/html/rfc8971) | BFD для VXLAN туннелей | **Небезопасный preview** | Identity поддерживаемого профиля привязана точно; owner-specific dataplane integration не завершена |
| [RFC 9521](https://datatracker.ietf.org/doc/html/rfc9521) | BFD для Geneve туннелей | **Небезопасный preview** | Явная IPv4 Format A VAP identity привязана; unsupported formats и неполная identity fail closed |
| [RFC 9764](https://datatracker.ietf.org/doc/html/rfc9764) | BFD Large Packets | **Частично** | Padding без auth и DF реализованы; hashing authenticated padding не завершён |
| [RFC 7880](https://datatracker.ietf.org/doc/html/rfc7880) | Seamless BFD Base | **Планируется** | Stateless рефлектор + инициатор для проверки инфраструктуры |
| [RFC 7881](https://datatracker.ietf.org/doc/html/rfc7881) | S-BFD для IPv4/IPv6 | **Планируется** | Инкапсуляция на порт 7784 для S-BFD |
| [RFC 5884](https://datatracker.ietf.org/doc/html/rfc5884) | BFD для MPLS LSP | **Stub** | Интерфейсы определены, ожидает LSP Ping |
| [RFC 5885](https://datatracker.ietf.org/doc/html/rfc5885) | BFD для PW VCCV | **Stub** | Интерфейсы определены, ожидает VCCV/LDP |

> Traditional Echo Mode (RFC 5880 Section 6.4, affiliated с контрольной
> сессией) не реализован. Поля Demand Mode декодируются, но runtime procedures
> RFC 5880 Section 6.6 не завершены. Unaffiliated echo (RFC 9747) является
> отдельной preview-реализацией.

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

Входящий пакет с Poll планирует ответ с Final. Реализация ещё не инициирует
локальные Poll-последовательности и не завершает crossed-Poll, commit параметров
и timer semantics. Наличие `pollActive` и `terminatePollSequence` поэтому не
означает полного соответствия Section 6.5.

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
- Используется crypto-seeded session-local PRNG; джиттер не является security
  boundary, но seed непредсказуем и нет contention на глобальном RNG в hot path

#### Section 6.8.16: Административное управление

Graceful shutdown запрашивает AdminDown с Diag=7, ожидает фиксированное
двухсекундное окно `drainTimeout`, затем отменяет горутины сессий. Это
best-effort путь: текущая реализация не подтверждает отправку и получение
пиром. Атомарное завершение AdminDown отслеживается для v1.

#### Не реализовано

| Секция | Функция | Обоснование |
|---|---|---|
| 6.4 | Affiliated Echo Mode | Требует контрольной сессии; RFC 9747 unaffiliated echo реализован вместо |
| 6.5 | Полные Poll Sequence procedures | Ответ на входящий Poll есть; локальная инициация, crossed Poll и timer semantics ожидаются |
| 6.6 | Demand Mode | Поля декодируются, но remote Demand behavior и timer procedures ожидаются |
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

Реализованная граница — numbered-multiaccess single-hop profile с явным
интерфейсом. Initial demultiplexing для полного объёма RFC 5881, включая
point-to-point behavior, остаётся открытым и не прошёл production qualification.

### Заметки по RFC 5882

Реализация: [`internal/gobgp/`](../../internal/gobgp/)

- Section 3.1 разрешает implementation-defined session-state hysteresis.
  Настраиваемое penalty-based dampening в GoBFD является implementation policy,
  а не стандартизированным RFC-алгоритмом.
- Section 4.3: отслеживание состояний BFD и вызов GoBGP gRPC API
  - BFD Down --> `DisablePeer()`
  - BFD Up --> `EnablePeer()`
  - Универсальный отзыв/восстановление маршрутов не реализован; при включённой
    интеграции с GoBGP зарезервированная стратегия `withdraw-routes` отклоняется
    при валидации конфигурации
  - Каждый вызов GoBGP API ограничен `gobgp.action_timeout`, чтобы медленный внешний API не блокировал обработку изменений состояния бесконечно

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

RFC 9384 требует Cease NOTIFICATION subcode 10 ("BFD Down") при разрыве BGP
из-за BFD. GoBGP v3 не позволяет выбрать subcode через `DisablePeer`: текущая
интеграция отправляет Administrative Shutdown (Cease/2), а строка
`"BFD Down (RFC 9384 Cease/10): diag=..."` служит только для операторской
корреляции. Поэтому wire-соответствие RFC 9384 не реализовано.

### Заметки по RFC 9468

Реализация: [`internal/bfd/unsolicited.go`](../../internal/bfd/unsolicited.go), [`internal/bfd/manager.go`](../../internal/bfd/manager.go)

RFC 9468 позволяет динамически создавать пассивные сессии при получении BFD-пакетов от неизвестных пиров. Автосоздание выполняется в `Manager.demuxByPeer()` с политикой per-interface. Положительный `MaxSessions` ограничивает число сессий; нулевое или отрицательное значение оставляет его неограниченным. Пустой `AllowedPrefixes` принимает любой валидный source address без проверки connected subnet. Квота резервируется атомарно до создания и освобождается при ошибке создания, явном удалении или cleanup пассивной Down-сессии после `CleanupTimeout`.

Эта preview-возможность fail-open при пустом `AllowedPrefixes` и не ограничена
при `MaxSessions <= 0`. Её нельзя включать на недоверенном интерфейсе до
реализации connected-subnet admission и обязательного положительного лимита.

### Заметки по RFC 9747

**Статус**: Preview

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

**Статус**: Протокол реализован; production integration частичная

Реализация: [`internal/bfd/micro.go`](../../internal/bfd/micro.go)

RFC 7130 определяет Micro-BFD — независимые BFD-сессии на каждом member link LAG для верификации пересылки per-link с более быстрым обнаружением, чем LACP.

| Требование | Реализация |
|---|---|
| UDP порт 6784 | `netio.PortMicroBFD = 6784`, per-member слушатели в `createMicroBFDListeners()` |
| Одна BFD-сессия на member link | `MicroBFDGroup.members` map, `AddMember()`/`RemoveMember()` |
| `SO_BINDTODEVICE` на каждый member | Функциональная опция `WithBindDevice()` на отправителе |
| Отслеживание агрегатного состояния | Порог `upCount >= minActive` |
| Обработка Member Down | `UpdateMemberState()` фиксирует состояние member и вызывает изменение агрегатного порога |
| Выделенный multicast MAC | `01-00-5E-90-00-01` для начальных пакетов |
| Только асинхронный режим | Стандартные процедуры RFC 5880 для каждого member |
| Тип сессии | Константа `SessionTypeMicroBFD` |
| Конфигурация per-group | `MicroBFDGroupConfig` с LAG-интерфейсом + member-линки |
| Реконсиляция групп | `reconcileMicroBFDGroups()` в `main.go`, SIGHUP reload |
| Диспетчер состояний | `RunDispatch` fan-out горутина маршрутизирует изменения состояний в группы |
| Actuator hook | `MicroBFDActuator` получает события member state после обновления состояния группы |
| Policy gate | `netio.LAGActuator` поддерживает режимы `disabled`, `dry-run` и `enforce` |
| Daemon wiring | `micro_bfd.actuator` задаёт mode, backend, OVSDB endpoint, owner policy и действия с member |
| Kernel bond backend | `KernelBondLAGBackend` пишет `-member` / `+member` в Linux bonding sysfs |
| OVS backend | `OVSDBLAGBackend` мутирует `Port.interfaces` через OVSDB; `OVSLAGBackend` остаётся CLI fallback type |
| NetworkManager backend | `NetworkManagerLAGBackend` деактивирует и активирует NM-owned bond port profiles через D-Bus |

Логика агрегатного состояния:
- Группа стартует со всеми member-ами Down, агрегат Down
- Когда `upCount >= MinActiveLinks`, агрегат переходит в Up
- Когда `upCount < MinActiveLinks`, агрегат переходит в Down
- Изменения состояния сообщаются только при переходах агрегата (пересечение порога)
- Состояние Init на member не считается как Up (только `StateUp` увеличивает `upCount`)

`MicroBFDGroupSnapshot` предоставляет read-only представление состояния группы включая детали per-member link, полезно для ответов gRPC API и мониторинга.

**Production-ограничение Linux**: RFC 7130 также требует выводить member link,
чья micro-BFD-сессия перешла в Down, из load-balancing table LAG. GoBFD теперь
имеет hook `MicroBFDActuator` и покрытый тестами policy gate
`netio.LAGActuator` для режимов disabled, dry-run и enforce. YAML wiring уже
добавлен, включая NetworkManager-aware owner policy. `backend: kernel-bond`
может выполнять remove/add member через Linux bonding sysfs при явном
`owner_policy: allow-external`. `backend: ovs` может выполнять remove/add
member на существующем OVS bonded port через native OVSDB transactions против
`Port.interfaces`. `OVSLAGBackend` остаётся direct CLI fallback type.
`backend: networkmanager` может выполнять remove/add member через deactivation
активного NetworkManager bond port profile и activation remembered или
available bond port profile при явном `owner_policy: networkmanager-dbus`.

### Заметки по RFC 8971

**Статус**: Небезопасный/неполный preview; owner-specific backends planned

Реализация: [`internal/netio/vxlan.go`](../../internal/netio/vxlan.go), [`internal/netio/vxlan_conn.go`](../../internal/netio/vxlan_conn.go), [`internal/netio/overlay.go`](../../internal/netio/overlay.go), [`internal/netio/overlay_backend.go`](../../internal/netio/overlay_backend.go), [`internal/netio/overlay_inner.go`](../../internal/netio/overlay_inner.go)

RFC 8971 определяет BFD в VXLAN-инкапсуляции для обнаружения отказов forwarding-path между VTEP (Virtual Tunnel Endpoints). BFD Control пакеты переносятся внутри VXLAN-инкапсулированных inner Ethernet фреймов.

| Требование | Реализация |
|---|---|
| Внешний UDP порт 4789 | `netio.VXLANPort = 4789`, `VXLANConn` через явный backend `userspace-udp` |
| Внутренний UDP порт 3784 | `BuildInnerPacket()` с dst-портом 3784 |
| Кодек VXLAN-заголовка | `MarshalVXLANHeader` / `UnmarshalVXLANHeader` |
| Management VNI | `VXLANConfig.ManagementVNI`, отклонение при несовпадении VNI |
| Валидация VNI (24 бит) | `ErrInvalidVXLANVNI` валидация конфигурации |
| Валидация I-флага | Сентинел `ErrVXLANInvalidFlags` |
| Inner destination MAC | `VXLANBFDInnerMAC = 00:52:02:00:00:00` (IANA) |
| Inner source MAC | Для каждого пира требуются remote/local MAC исходящих VTEP и exact match |
| Inner TTL=255 | `BuildInnerPacket()` устанавливает TTL=255 (RFC 5881 GTSM) |
| Inner IPv4 checksum | `ipv4HeaderChecksum()` по RFC 1071 |
| Тип сессии | Константа `SessionTypeVXLAN` |
| Адаптер OverlaySender | `OverlaySender` реализует `bfd.PacketSender` |
| Цикл OverlayReceiver | Снимает VXLAN + inner заголовки, доставляет в `Manager.DemuxWithWire` |
| Модель backend | `NewVXLANOverlayBackend` поддерживает `userspace-udp`; зарезервированные kernel/OVS/OVN/Cilium/Calico/NSX backend fail closed |
| Валидация receive path | Полный outer/VNI/inner IPv4/MAC tuple точно сопоставляется до discriminator demux; malformed inner framing отклоняется |
| Декларативные пиры | `vxlan.peers[]` в конфиге, реконсиляция при SIGHUP |
| Валидация конфигурации | Диапазон VNI, конкретные IPv4 endpoints, remote/local unicast source MAC, detect_mult и обнаружение дублирующей wire identity |

Стек инкапсуляции пакетов:
```
Outer IP → Outer UDP (4789) → VXLAN Header (8B) →
Inner Ethernet (14B) → Inner IPv4 (20B) → Inner UDP (8B, dst 3784) → BFD Control
```

Кодек VXLAN-заголовка обрабатывает 8-байтный фиксированный формат с I-флагом (VNI валиден) и 24-битным кодированием VNI. Пакеты Management VNI обрабатываются локально и не перенаправляются в tenant-сети.

**Production-ограничение Linux**: `vxlan.backend: userspace-udp` владеет UDP
сокетом на `localAddr:4789`. Это подходит для лабораторного endpoint,
выделенного Management VNI endpoint или Linux VTEP, где GoBFD владеет сокетом.
Если kernel VXLAN, OVS/OVN, Cilium, Calico, NSX или другой dataplane уже владеет UDP
4789 в том же namespace/local address, зарезервированные backend names fail
closed до появления owner-specific integration. Sender reconciliation
использует runtime backend, который уже обслуживает receiver, и не bind-ит
второй socket.
Listeners группируются по local VTEP, а sender каждой сессии сохраняет exact
tunnel scope. Userspace backend остаётся небезопасным там, где тем же UDP
socket владеет другой dataplane.

### Заметки по RFC 9521

**Статус**: Небезопасный preview; требуется явная Format A VAP identity

Реализация: [`internal/netio/geneve.go`](../../internal/netio/geneve.go), [`internal/netio/geneve_conn.go`](../../internal/netio/geneve_conn.go), [`internal/netio/overlay.go`](../../internal/netio/overlay.go), [`internal/netio/overlay_backend.go`](../../internal/netio/overlay_backend.go), [`internal/netio/overlay_inner.go`](../../internal/netio/overlay_inner.go)

RFC 9521 определяет BFD в Geneve-инкапсуляции для обнаружения отказов forwarding-path между NVE (Network Virtualization Edges) на уровне VAP (Virtual Access Point). Geneve — эволюция VXLAN для cloud-native сред.

| Требование | Реализация |
|---|---|
| Внешний UDP порт 6081 | `netio.GenevePort = 6081`, `GeneveConn` через явный backend `userspace-udp` |
| Кодек Geneve-заголовка | `MarshalGeneveHeader` / `UnmarshalGeneveHeader` |
| O бит (control) = 1 | RFC 9521 Section 4: установлен при отправке и проверяется при приёме |
| C бит (critical) = 0 | RFC 9521 Section 4: сброшен при отправке и проверяется при приёме |
| Protocol Type 0x6558 | Format A: Ethernet payload (`GeneveProtocolEthernet`); receive codec validation существует |
| Валидация VNI (24 бит) | `ErrInvalidGeneveVNI` при валидации конфигурации; receive codec validation существует |
| Валидация версии | `ErrGeneveInvalidVersion` (только версия 0 поддерживается) |
| Ethernet payload (Format A) | `GeneveProtocolEthernet = 0x6558` |
| Опции переменной длины | `GeneveHeader.OptLen` + `TotalHeaderSize()` |
| Inner TTL=255 | `BuildInnerPacket()` устанавливает TTL=255 (RFC 5881 GTSM) |
| Тип сессии | Константа `SessionTypeGeneve` |
| Адаптер OverlaySender | `OverlaySender` реализует `bfd.PacketSender` |
| Цикл OverlayReceiver | Достигает `Manager.DemuxWithWire` только после exact match configured VAP identity |
| Модель backend | `NewGeneveOverlayBackend` поддерживает `userspace-udp`; зарезервированные kernel/OVS/OVN/Cilium/Calico/NSX backend fail closed |
| Валидация receive path | Сопоставляет outer endpoints, VNI, inner VAP IP, address family и source/destination MAC до BFD demux |
| Декларативные пиры | `geneve.peers[]` в конфиге, переопределение VNI per-peer, реконсиляция при SIGHUP |
| Валидация конфигурации | Проверяются VNI, concrete outer IPv4 endpoints, detect_mult, дубликаты и полный unicast VAP MAC/IPv4 tuple |

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

**Production-ограничение Linux**: `geneve.backend: userspace-udp` требует
явные local и remote VAP MAC/IPv4 identities. Неполная или special-use identity
(unspecified, loopback, multicast), inner IPv6,
Format B и другие protocol types fail closed. Exact scopes используют общий
local listener без aliasing VNI/VAP tuples. Backend не интегрируется с socket
ownership kernel Geneve, OVS/OVN или NSX dataplane.

### Заметки по RFC 9764

**Статус**: Частично

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

Authenticated padded packets не завершены: authentication hash должен
покрывать padded length RFC 9764. Стабильное соответствие заблокировано до
исправления и interop-проверки combined authenticated-padding path.

### RFC 7880/7881 (Планируется)

**Статус**: Планируется

RFC 7880 определяет Seamless BFD (S-BFD) — упрощённый механизм BFD для тестирования доступности инфраструктуры. В отличие от стандартного BFD, требующего трёхстороннего рукопожатия, S-BFD использует stateless рефлектор, который немедленно отвечает на запросы инициатора.

RFC 7881 определяет инкапсуляцию S-BFD для IPv4 и IPv6 с использованием порта назначения 7784.

| Требование | Планируемая реализация |
|---|---|
| Stateless рефлектор (RFC 7880) | Будущий рефлектор на порту 7784 |
| Сопоставление пула дискриминаторов | Рефлектор сопоставляет `YourDiscriminator` с локальным пулом |
| Ответ с State=Up | Состояние сессии не сохраняется |
| S-BFD инициатор (RFC 7880) | Будущий инициатор с таймером обнаружения |
| Порт 7784 (RFC 7881) | Будущий выделенный слушатель S-BFD |
| Без трёхстороннего рукопожатия | Инициатор отправляет, рефлектор отвечает немедленно |

В текущем коде нет `SBFDReflector`, `SBFDInitiator` или listener порта 7784;
описанные выше имена являются ролями плана, а не существующими API.

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
| [rfc9985.txt](../rfc/rfc9985.txt) | RFC 9985 -- Optimizing BFD Authentication |
| [rfc9986.txt](../rfc/rfc9986.txt) | RFC 9986 -- Meticulous Keyed ISAAC for Optimized BFD Authentication |

### Связанные документы

- [02-protocol.md](./02-protocol.md) -- Детали протокола BFD
- [01-architecture.md](./01-architecture.md) -- Архитектура системы
- [05-interop.md](./05-interop.md) -- Тестирование совместимости

---

*Последнее обновление: 2026-02-23*
