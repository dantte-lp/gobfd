# Соответствие RFC

![RFC 5880](https://img.shields.io/badge/RFC_5880-Implemented-34a853?style=for-the-badge)
![RFC 5881](https://img.shields.io/badge/RFC_5881-Implemented-34a853?style=for-the-badge)
![RFC 5882](https://img.shields.io/badge/RFC_5882-Implemented-34a853?style=for-the-badge)
![RFC 5883](https://img.shields.io/badge/RFC_5883-Implemented-34a853?style=for-the-badge)
![RFC 5884](https://img.shields.io/badge/RFC_5884-Stub-ffc107?style=for-the-badge)
![RFC 7130](https://img.shields.io/badge/RFC_7130-Stub-ffc107?style=for-the-badge)

> Матрица соответствия RFC, постраничные заметки по реализации, обоснование дизайна и ссылки на исходные тексты RFC.

---

### Содержание

- [Матрица соответствия](#матрица-соответствия)
- [Заметки по RFC 5880](#заметки-по-rfc-5880)
- [Заметки по RFC 5881](#заметки-по-rfc-5881)
- [Заметки по RFC 5882](#заметки-по-rfc-5882)
- [Заметки по RFC 5883](#заметки-по-rfc-5883)
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
| [RFC 5884](https://datatracker.ietf.org/doc/html/rfc5884) | BFD для MPLS LSP | **Stub** | Интерфейсы определены, ожидает LSP Ping |
| [RFC 5885](https://datatracker.ietf.org/doc/html/rfc5885) | BFD для PW VCCV | **Stub** | Интерфейсы определены, ожидает VCCV/LDP |
| [RFC 7130](https://datatracker.ietf.org/doc/html/rfc7130) | Micro-BFD для LAG | **Stub** | Сессии per-member-link запланированы |

> Echo Mode (Section 6.4) и Demand Mode (Section 6.6) намеренно не реализованы. Асинхронный режим покрывает основной сценарий BFD-assisted failover в ISP/DC.

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
| 6.4 | Echo Mode | Требует поддержки ядра; малая выгода для BFD-BGP |
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

### Stub-интерфейсы

| RFC | Зависимость | Статус |
|---|---|---|
| RFC 5884 (BFD для MPLS) | LSP Ping (RFC 4379) | Интерфейсы определены в `internal/bfd` |
| RFC 5885 (BFD для VCCV) | VCCV (RFC 5085), LDP (RFC 4447) | Интерфейсы определены |
| RFC 7130 (Micro-BFD для LAG) | Per-member-link сессии | `SO_BINDTODEVICE` per member готов |

### Справочные RFC

Эти RFC упоминаются, но не реализуются напрямую:

| RFC | Название | Отношение |
|---|---|---|
| RFC 5082 | GTSM | Основание для требования TTL=255 |
| RFC 4379 | LSP Ping | Зависимость RFC 5884 |
| RFC 5085 | VCCV | Зависимость RFC 5885 |
| RFC 4447 | LDP | Зависимость RFC 5885 |
| RFC 7419 | Common Interval Support | Руководство по согласованию интервалов |
| RFC 7726 | Clarifying BFD for MPLS | Процедуры MPLS-сессий |
| RFC 9127 | YANG Data Model for BFD | Эталон модели конфигурации |

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

*Последнее обновление: 2026-02-21*
