# Соответствие RFC

[![RFC 5880](https://img.shields.io/badge/RFC_5880-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5880)
[![RFC 5881](https://img.shields.io/badge/RFC_5881-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5881)
[![RFC 5882](https://img.shields.io/badge/RFC_5882-Partial-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5882)
[![RFC 5883](https://img.shields.io/badge/RFC_5883-Constrained-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc5883)
[![RFC 7419](https://img.shields.io/badge/RFC_7419-Implemented-34a853?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7419)
[![RFC 9384](https://img.shields.io/badge/RFC_9384-Not_Implemented-ea4335?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9384)
[![RFC 9468](https://img.shields.io/badge/RFC_9468-Unsafe_Preview-ea4335?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9468)
[![RFC 9747](https://img.shields.io/badge/RFC_9747-Preview-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc9747)
[![RFC 7130](https://img.shields.io/badge/RFC_7130-Preview-ffc107?style=for-the-badge)](https://datatracker.ietf.org/doc/html/rfc7130)
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
| [RFC 5880](https://datatracker.ietf.org/doc/html/rfc5880) | Базовый протокол BFD | **Асинхронное ядро; частично** | Локальный timer Poll/Final и remote Demand реализованы; crossed/queued wire evidence ожидает интеграции ветки; local Demand и affiliated Echo отложены |
| [RFC 5881](https://datatracker.ietf.org/doc/html/rfc5881) | BFD для IPv4/IPv6 Single-Hop | **Частично; numbered-multiaccess profile** | UDP 3784, TTL=255, проверки транспортного класса и интерфейса приёма в каждой сессии, привязка TX single-hop к интерфейсу; point-to-point initial demux отложен |
| [RFC 5882](https://datatracker.ietf.org/doc/html/rfc5882) | Общее применение BFD | **Application integration частичная** | State delivery и actuator convergence не завершены; penalty dampening является implementation policy |
| [RFC 5883](https://datatracker.ietf.org/doc/html/rfc5883) | BFD для Multihop | **Ограниченный GTSM profile** | UDP 4784; политика hops `multihop_max_hops` в каждой сессии (по умолчанию 1: TTL>=254) -- политика GoBFD, а не правило RFC 5883; квалификация на реальных путях из 2/5 hops не выполнена |
| [RFC 7419](https://datatracker.ietf.org/doc/html/rfc7419) | Common Interval Support | **Реализован** | 6 общих интервалов, опциональное выравнивание |
| [RFC 9384](https://datatracker.ietf.org/doc/html/rfc9384) | BGP Cease NOTIFICATION для BFD | **Не реализован** | GoBGP v4, как и v3, отправляет Cease/2; Cease/10 присутствует только в операторском тексте |
| [RFC 9468](https://datatracker.ietf.org/doc/html/rfc9468) | Unsolicited BFD | **Небезопасный preview** | Проверки connected subnet, лимита сессий и скорости приёма действуют; ответы с полученного локального кортежа; опциональный допуск по Meticulous Keyed SHA1 |
| [RFC 9747](https://datatracker.ietf.org/doc/html/rfc9747) | Unaffiliated BFD Echo | **Preview** | Echo session и port 3785 wiring существуют; полная RFC-квалификация не завершена |
| [RFC 7130](https://datatracker.ietf.org/doc/html/rfc7130) | Micro-BFD для LAG | **Preview; owner integration частичная** | Протокол и отдельные actuators существуют; production ownership ограничен |
| [RFC 8971](https://datatracker.ietf.org/doc/html/rfc8971) | BFD для VXLAN туннелей | **Небезопасный preview** | Identity поддерживаемого профиля привязана точно; owner-specific dataplane integration не завершена |
| [RFC 9521](https://datatracker.ietf.org/doc/html/rfc9521) | BFD для Geneve туннелей | **Небезопасный preview** | Явная IPv4 Format A VAP identity привязана; unsupported formats и неполная identity fail closed |
| [RFC 9764](https://datatracker.ietf.org/doc/html/rfc9764) | BFD Large Packets | **Частично** | PDU-length authentication покрыта локально; padded TX квалифицирован с FRR/BIRD |
| [RFC 7880](https://datatracker.ietf.org/doc/html/rfc7880) | Seamless BFD Base | **Планируется** | Stateless рефлектор + инициатор для проверки инфраструктуры |
| [RFC 7881](https://datatracker.ietf.org/doc/html/rfc7881) | S-BFD для IPv4/IPv6 | **Планируется** | Инкапсуляция на порт 7784 для S-BFD |
| [RFC 5884](https://datatracker.ietf.org/doc/html/rfc5884) | BFD для MPLS LSP | **Stub** | Интерфейсы определены, ожидает LSP Ping |
| [RFC 5885](https://datatracker.ietf.org/doc/html/rfc5885) | BFD для PW VCCV | **Stub** | Интерфейсы определены, ожидает VCCV/LDP |

> Traditional Echo Mode (RFC 5880 Section 6.4, affiliated с контрольной
> сессией) не реализован. Remote Demand подавляет периодический TX, когда оба
> конца Up и локальный Poll не активен; local Demand и полные procedures
> RFC 5880 Section 6.6 не реализованы. Unaffiliated echo (RFC 9747) является
> отдельной preview-реализацией.

### Заметки по RFC 5880

Выбранная policy Held errata 5205/7240 фиксирует timeout в Down как
`Down/ControlTimeExpired` без перехода и сохраняет диагностику при recovery
в Up. Причинный кортеж публикуется владельцем после фиксации итоговых фактов.
Для graceful AdminDown проверка предшествует PathDown и сохраняет Diag=7;
RFC 5880 Section 6.8.16 допускает AdminDown с PathDown, поэтому Diag=7
является выбранной policy, а не общим обязательным требованием RFC.

При выбранной политике [Held Erratum 5205](https://www.rfc-editor.org/errata/rfc5880)
сессия, изначально находящаяся в Down, может опубликовать diagnostic 1 после
молчания длительностью Detection Time, даже если ещё не получала пакетов.
Меняется причинная диагностика, а не счётчик переходов состояния. Статус
erratum остаётся Held for Document Update; его применение — явный выбор
проекта, а не утверждение о требовании исходного текста RFC. См. `gobfd-9ro`.

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

Входящий пакет с Poll планирует немедленный ответ с Final. Ответ сохраняется
при ошибках подписи, кодирования и отправки до успешной отправки; повторные
попытки обходят запрет периодической передачи. Final снимает бит Poll в этом
пакете, не завершая локальную Poll sequence. Переход в Up запускает локальный
Poll, если объявляемый Desired Min TX снижается с медленного интервала в одну
секунду до настроенного значения. Используются существующие передачи по таймеру
и FSM; обновления таймеров через config упорядочивают одно активное и последнее
ожидающее изменение. Final подтверждает только успешно отправленный Poll;
защищённый эффект увеличения TX и уменьшения RX применяется при подтверждении.
Последовательные транзакции разделяются измеренным временем обмена. Выход из Up
завершает receipts ошибкой и упорядочивает обязательный slow-floor Poll с recovery.
Вне Up, включая AdminDown, продвинутое обновление вступает в силу немедленно,
когда нет активного Poll и separation, а его Poll подтверждает receipt
(Section 6.8.3 откладывает изменения только «when bfd.SessionState is Up»). Это
снимает локальный hold-down нулевым Required Min RX, пока соблюдающий его пир
молчит. Receipt получает Applied, только если действующий кортеж совпадает с
подтверждённым. Если до deadline Poll не получает Final, receipt завершается
ошибкой, кортеж остаётся действующим, а следующие поколения ждут за этим Poll.
Вход в Up через Final сохраняет односекундный порог TX, пока recovery Poll не
передаст более быстрый Desired Min TX.
Полная квалификация Section 6.5 ещё впереди, включая live FRR/BIRD reload evidence
и диапазон RTT целевого окружения.

Изменения receive interval пира пересчитывают TX от последней успешной
отправки; неизменившиеся входящие пакеты не откладывают её срок. Нулевой
receive interval пира останавливает периодический TX, как и remote Demand
при обоих концах Up без активного локального Poll. Немедленные ответы Final
и ограниченные по частоте retries обходят эти запреты. Начальные базовые сессии
принимают нулевой локальный receive interval через YAML и generic `AddSession`,
сохраняя defaults для пропущенных полей. Правила per-peer overrides preview
не изменены. Reload таймеров сохраняет ту же семантику явного нуля; объём
interop-квалификации этих процедур указан ниже.

Переход в Down по истечении Detection Time или по принятому Down либо
AdminDown сразу отправляет новое состояние (Section 6.8.7: изменённое
содержимое "SHOULD be transmitted during the interval between periodic Control
packet transmissions"). Действуют те же запреты нулевого remote RX, Passive при
неизвестном пире и unsolicited; ожидающий Final переносит изменение вместо
отдельного пакета, а следующий периодический пакет соблюдает нижнюю границу
медленного режима Section 6.8.3.

Один Detection Time без корректных пакетов возвращает
`bfd.RemoteMinRxInterval` к 1 микросекунде и `bfd.RemoteDemandMode` к 0 в
любом состоянии (Sections 6.8.1 и 6.8.18): Required Min RX пира обязателен,
только пока пир продолжает передачу. Если сброс меняет TX-интервал или
разрешение периодической передачи, следующий пакет планируется от предыдущей
успешной отправки; это снимает и большой ненулевой hold-down. В AdminDown
Active-сессия затем отправляет уведомление Section 6.8.16, и drain может
завершиться в пределах своего бюджета (10 s для drain последнего claim, иначе
context вызывающего); Passive-сессия молчит, потому что `bfd.RemoteDiscr` равен нулю
(Section 6.8.7). Изменения remote Desired Min TX и Detect Mult обновляют
Detection Time на пакете, который их несёт (Sections 6.8.3 и 6.8.4). Тесты
виртуального времени покрывают также timer Poll при remote Demand, истечение
Poll при нулевом remote RX, ответ Final на увеличение remote RX и
возобновление TX после снятия Demand от предыдущей отправки.

FRR 10.7.1 не может объявить нулевой RX (`receive-interval` не меньше 10) и не
имеет `demand-mode`. BIRD 3.4.0 не имеет опции Demand; поддержка нулевого RX не
проверена. Interop remote Demand с обоими закреплёнными пирами и interop remote
zero-RX с FRR неприменимы по исходному коду (решение мейнтейнера 2026-10-21,
`gobfd-qj0.8.2.1.1.2`). `TestCraftedPeerTestcontainers` подтверждает поведение
на wire с репозиторным Go peer
([Interop Testing](05-interop.md#crafted-go-peer), `gobfd-qj0.8.2.1.1.2.3`):
при нулевом remote RX GoBFD передаёт только Final, по одному на Poll пира
(Sections 6.5 и 6.8.7); после одного Detection Time без пакетов переходит в
Down с diag 1, сбрасывает remote receive interval, сразу передаёт Down с Your
Discriminator 0 и далее работает в медленном режиме (Sections 6.8.4, 6.8.7 и
6.8.18); при remote Demand передаёт только кадры Poll или Final,
переносит перезагруженный таймер в Poll Sequence, завершённой Final пира, и
возобновляет periodic TX после снятия Demand (Sections 6.5 и 6.6). Прогон
2026-10-22 на `c954ec1`: окно RX0 8.74 s с 2 Poll, на каждый пришёл Final не
позднее 201 us; Down через 900.4 ms после последнего кадра пира (DT 900 ms),
первый пакет Down через 900.5 ms, далее 6 интервалов медленного режима; окно
Demand 7.24 s с одним Poll GoBFD на 500 ms; periodic TX через 425 ms после
снятия Demand, 17 интервалов, среднее 434.1 ms. До `b72b3c7` первый пакет Down
ждал следующего интервала медленного режима (1.862 s после молчания в прогоне
2026-10-21). Interop FRR/BIRD для ненулевых изменений remote TX/RX остаётся
открытым.

Session receive path отклоняет зарезервированный нулевой Desired Min TX до
обновления состояния пира и таймеров, включая пакеты через `RecvPacket`.
Это предотвращает цикл нулевого detection timer при нулевом локальном RX-интервале.

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

Истечение receive-sequence по RFC 5880 Section 6.8.1 использует монотонное
время последнего принятого корректного пакета в owner loop. После
`2 × DetectionTime` без корректных пакетов `AuthSeqKnown` сбрасывается в любом
состоянии сессии. `RcvAuthSeq` сохраняется до следующего успешного
аутентифицированного приёма. Проверка expiry перед аутентификацией позволяет
перезапущенному peer синхронизировать последовательность, даже когда RX
обрабатывается раньше готового detection timer. Ошибка аутентификации не
обновляет время приёма. Экспортируемый wall-clock timestamp в этом решении
не используется.

Owner detection timer сбрасывает `RemoteDiscr` после одного Detection Time в
Down, Init, Up и AdminDown. Последние наблюдавшиеся remote state, diagnostic и
C-bit сохраняются для causal policy; сброс wire discriminator сам по себе
не публикует новое observation. Passive-сессия с `RemoteDiscr = 0` не передаёт
пакеты, включая прямые FSM sends и повторы pending Final (Section 6.8.7).
Обязательство Final сохраняется до успешной отправки после получения
корректного peer discriminator.

Корректный приём Down/AdminDown и локальный PathDown сохраняют изученный
дискриминатор до истечения detection timer. Ответ Final на Poll содержит
`MyDiscriminator` peer, включая passive-сессии и повторы после ошибки отправки
(Sections 6.8.6–6.8.7). Локальные команды PathDown не переносят срок receive
detection timer.
Если Down прерывает ожидающее уменьшение RX, новый Detection Time отсчитывается
от последнего корректного приёма.

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

Graceful shutdown использует общий deadline десять секунд для owner commit,
первой успешной записи AdminDown/7 и окна уведомления, рекомендованного здесь и
в RFC 5882 Section 8. Ошибки записи повторяются через обычный TX timer. Окно
начинается с первой успешной записи и покрывает local RX DT и
`localDetectMult * max(advertisedAdminDownTX, remoteMinRX)`; только увеличение
negotiation продлевает deadline. Короткий deadline возвращает `DrainIncompleteError`.
Ограничения passive unknown peer и нулевого remote RX сохраняются; нулевой
remote RX действует до сброса Section 6.8.18 после одного Detection Time без
пакетов. Разрешённый Final может подтвердить первую успешную запись.
Owning UDP lease прерывает
pending write и ожидает callback до освобождения ресурсов. Успех подтверждает
локальные записи и время, без подтверждения получения пиром.

Освобождение последнего Control-session claim использует этот drain до
cancel/join и освобождения lease, включая reconciliation rollback.
Внутренний предел десять секунд учитывает более раннюю отмену caller;
ошибки неполного drain и release сохраняются. Нефинальный claim не изменяет wire
state. Reconciliation использует один бюджет для параллельного drain.
RFC 9468 Section 2 требует прекратить Control-передачу пассивной стороны:
failure cleanup unsolicited Down-сессии выполняет cancel без административного
drain. Standalone Echo и удаление только Micro-BFD group не владеют этим Control
exchange. Development-срез не квалифицирует полноту регистрации/статуса Watch
или stable release.

Локальная SIGTERM квалификация 2026-10-16 использовала source patch SHA256
`d1394d09fb3b902d3e34a5e34467c5aa4c506bd1d71c49416f6176773dc86947`
поверх `e4f3761a0f1fc59fbf9b2f859923a81125d157bf`. Capture содержит восемь
пакетов AdminDown/7, по четыре для FRR и BIRD: TTL 255, multiplier 3,
advertised TX 1 000 000 микросекунд, RX 300 000 микросекунд. Drain завершён через
3.000171 секунды после первого wire packet для FRR и 3.000147 для BIRD.
Сорок BGP/route samples за 20.280366 секунды сохранили Established, неизменный
uptime и маршруты `10.20.0.0/24`, `10.30.0.0/24`. Samples не доказывают
непрерывность состояния между наблюдениями или подтверждение получения пиром.
Daemon завершился с code 0; все пять контейнеров остановлены и сохранены без OOM.
Доказательства: `reports/h12-wire-20261016/`; PCAP SHA256
`f10937cc6a7969bc336e4f1b5217d2f458e5f046236f68530225ef26ec281c68`.

#### Не реализовано

| Секция | Функция | Обоснование |
|---|---|---|
| 6.4 | Affiliated Echo Mode | Требует контрольной сессии; вместо него есть RFC 9747 unaffiliated echo в статусе preview |
| 6.5 | Полные Poll Sequence procedures | Есть config-owned транзакции таймеров, подтверждённая отправка Poll, Final retry и исключение P/F; live reload/RTT qualification ещё впереди |
| 6.6 | Полный Demand Mode | Подавление remote periodic TX, Poll локального таймера при remote Demand и сброс Section 6.8.18 подтверждены на wire с crafted Go peer (`TestCraftedPeerTestcontainers`); закреплённые FRR/BIRD не отправляют Demand. Local Demand и полные Poll procedures остаются открытыми |
| 4.1 | Бит Multipoint | Зарезервирован для будущих P2MP расширений |

### Заметки по RFC 5881

Реализация: [`internal/netio/`](../../internal/netio/)

| Требование | Реализация |
|---|---|
| Порт назначения 3784 | `netio.PortSingleHop = 3784` |
| Порт источника 49152-65535 | `SourcePortAllocator` |
| TTL=255 исходящий | `IP_TTL` / `IPV6_UNICAST_HOPS` через `unix.SetsockoptInt` |
| Проверка TTL=255 входящий | `IP_RECVTTL` / `IPV6_RECVHOPLIMIT` + предварительный фильтр listener; демультиплексированная сессия дополнительно требует транспортный класс single-hop (раздел 5) |
| Транспортный класс | Пакет multihop listener 4784 не попадает в single-hop сессию ни по Your Discriminator, ни по ключу пира (`scope_mismatch`) |
| Привязка RX к интерфейсу (раздел 3) | При заданном `interface` пакеты с другого устройства listener отклоняются. Каждая single-hop сессия сравнивает индекс интерфейса из PKTINFO с индексом, определённым при создании по `interface` или по интерфейсу, владеющему `local` |
| Привязка TX к интерфейсу (раздел 6) | `SO_BINDTODEVICE` на отправителе сессии при заданном `interface` (YAML и API); IPv6 link-local адрес отправителя получает zone этого интерфейса |
| Demux IPv6 link-local | Zone не входит в ключ demux; без `interface` zone link-local адреса выбирает интерфейс сессии, устройство listener и отправителя; zone, противоречащая `interface`, отклоняется (`ErrInvalidSessionZone`) |
| TTL в сессии (раздел 5) | Демультиплексированная single-hop сессия также требует TTL/Hop Limit 255 (`demux`/`ttl_rejected`) |
| Раздельные IPv4/IPv6 слушатели | Раздельные сокеты `udp4` / `udp6` |

Реализованная граница — numbered-multiaccess single-hop profile: каждая сессия
привязана к одному интерфейсу приёма, явному или определённому по локальному
адресу при создании. YAML-сессия с локальным адресом, не назначенным ни одному
или назначенным нескольким интерфейсам, остаётся pending и повторяется при
событиях интерфейсов, resync или SIGHUP, readiness при этом `NOT_SERVING`;
адрес, назначенный нескольким интерфейсам, остаётся pending, пока `interface` не
задан в конфигурации и не применён через SIGHUP. API возвращает
`FailedPrecondition`.
Локальный адрес на `lo` или dummy интерфейсе привязывает сессию к этому
интерфейсу, поэтому пакеты с физического интерфейса отклоняются как
`scope_mismatch`; в такой схеме обязателен `interface`. Point-to-point initial
demultiplexing (раздел 6: первый пакет принимается с любого адреса источника)
отложен и не прошёл production qualification.

### Заметки по RFC 5882

Реализация: [`internal/gobgp/`](../../internal/gobgp/)

- Section 3.1 разрешает implementation-defined session-state hysteresis.
  Настраиваемое penalty-based dampening в GoBFD является implementation policy,
  а не стандартизированным RFC-алгоритмом.
- Section 10.2 (взаимодействие с BGP; Section 4.2 — реакция на смену состояния): отслеживание состояний BFD и вызов GoBGP gRPC API
  - Обычный remote Down/detection failure или явный local PathDown --> `DisablePeer()`
  - Local или известный remote AdminDown --> без disable (Sections 3.2 и 4.2);
    последний известный remote AdminDown сохраняется при последующих timeout
  - BFD Up --> `EnablePeer()` только для peer, отключённого данным процессом
  - Универсальный отзыв/восстановление маршрутов не реализован; при включённой
    интеграции с GoBGP зарезервированная стратегия `withdraw-routes` отклоняется
    при валидации конфигурации
  - Каждый вызов GoBGP API ограничен `gobgp.action_timeout`, чтобы медленный внешний API не блокировал обработку изменений состояния бесконечно

### Заметки по RFC 5883

| Требование | Реализация |
|---|---|
| Порт назначения 4784 | `netio.PortMultiHop = 4784` |
| TTL=255 исходящий | Аналогично single-hop |
| Политика входящего TTL | В RFC 5883 отсутствует. GoBFD применяет `multihop_max_hops` N в каждой демультиплексированной сессии: принимается TTL/Hop Limit >= 255 - N (1/2/5 -> 254/253/250, по умолчанию 1), чтение A ненормативного RFC 5082 Appendix A, как `minimum-ttl` во FRR. Отказы учитываются как `demux`/`ttl_rejected` |
| Транспортный класс | Пакет listener 3784 не попадает в multihop сессию (`scope_mismatch`) |
| Интерфейс | `multi_hop` с `interface` отклоняется валидацией YAML и API (раздел 4.1 демультиплексирует по паре адресов) |
| Echo (раздел 3: MUST NOT be used over multiple hops) | Multihop Control пакеты передают Required Min Echo RX Interval 0; у RFC 9747 Echo нет multihop типа |
| Demux (раздел 4.1) | Your Discriminator, при нуле -- пара (источник, назначение); `Manager.DemuxWithWire` |

**Рекомендации по аутентификации (разделы 3 и 6).** TTL/Hop Limit 255 на приёме
не защищает multihop путь; RFC 5883 Section 3 возлагает защиту от спуфинга на
аутентификацию BFD, Section 6 указывает, что реализациям SHOULD использовать
криптографическую аутентификацию на multihop путях. Нижняя граница политики hops
-- только дополнительный уровень защиты. Задавайте `auth` в каждой `multi_hop`
сессии, предпочтительно `meticulous_keyed_sha1`; неаутентифицированная multihop
сессия с `multihop_max_hops` больше 1 принимает любой подделанный пакет с
достаточным TTL и совпадающими дискриминаторами.

### Заметки по RFC 7419

Реализация: [`internal/bfd/intervals.go`](../../internal/bfd/intervals.go)

RFC 7419 определяет набор общих интервалов BFD для совместимости с аппаратными реализациями: 3.3мс, 10мс, 20мс, 50мс, 100мс, 1с. Опция `bfd.align_intervals: true` выравнивает интервалы вверх до ближайшего общего значения.

### Заметки по RFC 9384

Реализация: [`internal/gobgp/rfc9384.go`](../../internal/gobgp/rfc9384.go)

RFC 9384 требует Cease NOTIFICATION subcode 10 ("BFD Down") при разрыве BGP
из-за BFD. GoBGP v4, как и v3, не позволяет выбрать subcode через `DisablePeer`: текущая
интеграция отправляет Administrative Shutdown (Cease/2), а строка
`"BFD Down (RFC 9384 Cease/10): diag=..."` служит только для операторской
корреляции. Поэтому wire-соответствие RFC 9384 не реализовано.

### Заметки по RFC 9468

Реализация: [`internal/bfd/unsolicited.go`](../../internal/bfd/unsolicited.go), [`internal/bfd/manager.go`](../../internal/bfd/manager.go)

RFC 9468 позволяет динамически создавать пассивные сессии при получении BFD-пакетов от неизвестных пиров. Автосоздание выполняется в `Manager.demuxByPeer()` с политикой per-interface. Сессии создаются только из single-hop пакетов с TTL 255, пакеты multi-hop listener (порт 4784) отклоняются (раздел 6.1). Source должен принадлежать подсети локального адреса, на который пришёл пакет (раздел 2); `AllowedPrefixes` дополнительно сужает допуск; ненумерованный путь отклоняется. `max_sessions` при включении должен быть >= 1, новые сессии допускаются не чаще 10 в секунду (burst 20). Квота резервируется атомарно до создания и освобождается при ошибке создания, явном удалении или cleanup пассивной Down-сессии после `CleanupTimeout`.

Каждый явно включённый интерфейс с непустым именем получает отдельные wildcard
listeners IPv4 и IPv6 на порту 3784 с `SO_BINDTODEVICE`; настроенная сессия
на локальном адресе для приёма не требуется. Настроенные per-address listeners
сохраняются; destination и ingress index берутся из ancillary data, а admission
использует фактический интерфейс приёма.

Каждая пассивная сессия получает свой sender на локальном адресе и интерфейсе,
на которые пришёл пакет, со своим source port RFC 5881; IPv6 link-local адрес
привязывается к интерфейсу приёма. Опциональный `unsolicited.auth`
(только Meticulous Keyed SHA1, раздел 6.2) проверяет пакет до создания
сессии; без него пакеты с битом A отклоняются. Включённая политика
проверяется при загрузке: значения `session_defaults` представимы на проводе,
`required_min_rx` ненулевой, хотя бы один интерфейс включён, `allowed_prefixes`
корректны. Development qualification включает dual-stack capture и однократное
закрытие 78 конкурентных sender leases (`gobfd-qj0.8.2.1.10.1`). Режим остаётся
небезопасным preview; его нельзя включать на недоверенном интерфейсе.

При единственном unsolicited claim owner останавливает non-Final Control TX
при natural failure или истечении независимого окна установления. Окно
отсчитывается от первого принятого Control packet и
продлевается только до большего наблюдаемого local RX Detection Time.
Повторные пакеты с теми же таймерами окно не перезапускают. Смена claims сохраняет
исходное окно и failure latch; configured/API claims сохраняют обычный TX даже
при истечении private setup window. Истечение окна в чистом unsolicited режиме
сохраняет FSM и diagnostic RFC 5880; отсутствие валидного RX отдельно сбрасывает
discriminator через один RX Detection Time, а auth sequence window — через два.
Следующий валидный аутентифицированный Control packet начинает новую локальную
попытку на сохранённой FSM. Это implementation policy, а не отдельный wire epoch:
retained Init с remote Up переходит в Up, retained Down с remote Up остаётся Down.
Первоначальный remote AdminDown также получает setup window; AdminDown после
неудачной попытки сохраняет остановку. Final/retry и local AdminDown сохраняют
процедуры RFC 5880 Sections 6.5, 6.8.7 и 6.8.16. Configured/API claims сохраняют
обычный passive режим. Cleanup проверяет failed attempt под owner lock перед
освобождением claim; старый таймер не удаляет более позднюю попытку или локально
отключённую сессию. Private setup failure запускает только cleanup и не публикует
фиктивный переход состояния.

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
| TTL 255 отправка, приём строго с TTL 254 | `netio.ValidateExpectedTTL` на Echo listener |
| Декларативные echo-пиры | `echo.peers[]` в конфиге, реконсиляция при SIGHUP |
| Отправитель с портом назначения 3785 | Функциональная опция `WithDstPort(PortEcho)` |

Ключевые отличия от BFD control сессий:
- Нет трёхстороннего рукопожатия (нет состояния Init)
- Нет согласования таймеров с удалённой стороной (локальные настройки)
- Нет аутентификации (echo-пакеты отправлены самим собой)
- Отдельный тип `EchoSession` с упрощённой FSM

### Заметки по RFC 7130

**Статус**: Preview; протокол реализован, production integration частичная

Реализация: [`internal/bfd/micro.go`](../../internal/bfd/micro.go)

RFC 7130 определяет Micro-BFD — независимые BFD-сессии на каждом member link LAG для верификации пересылки per-link с более быстрым обнаружением, чем LACP.

| Требование | Реализация |
|---|---|
| UDP порт 6784 | `netio.PortMicroBFD = 6784`, per-member слушатели в `createMicroBFDListeners()` |
| Одна BFD-сессия на member link (Sections 2, 2.2) | `microBFDMemberSessionConfig()` выводит из группы одну сессию `SessionTypeMicroBFD` на member link; каждым member link владеет одна группа (`ErrMicroBFDMemberOwned`) |
| Контракт member-сессии (Section 2.2) | `ValidateMicroBFDConfig()` применяет проверку сессии RFC 5880 (адрес пира, wire-границы интервалов, detect multiplier) к каждой member-сессии на пути daemon и прямом пути Manager/API; группы хранят и возвращают общие таймеры |
| `SO_BINDTODEVICE` на каждый member | Функциональная опция `WithBindDevice()` на отправителе |
| Отслеживание агрегатного состояния | Порог `upCount >= minActive` |
| Обработка Member Down | `UpdateMemberState()` фиксирует состояние member и вызывает изменение агрегатного порога |
| Выделенный multicast MAC (§2.3) | `01-00-5E-90-00-01` не передаётся: UDP sender использует MAC пира. Отдельный wire gate красный; см. `gobfd-rle` и `gobfd-j8r`. |
| Только асинхронный режим | Стандартные процедуры RFC 5880 для каждого member |
| Тип сессии | Константа `SessionTypeMicroBFD` |
| Конфигурация per-group | `MicroBFDGroupConfig` с LAG-интерфейсом + member-линки |
| Реконсиляция групп | `Manager.ReconcileMicroBFDGroupsDetailed()` фиксирует каждую группу вместе с её member-сессиями при SIGHUP reload; ошибка claim member-сессии откатывает новые группы и сессии этого прохода |
| Владение группами | Группы несут типизированные claim владельцев, как сессии: reload удаляет только свои группы, одинаковые группы API и reload разделяются, `DeleteMicroBFDGroup` для группы, которой вызывающий не владеет, возвращает `ErrMicroBFDGroupOwnerClaimNotFound` |
| Начальное состояние member (Section 3) | Новая группа берёт состояние каждого member из существующей member-сессии; member в Up учитывается в `MinActiveLinks` и сообщается actuator в порядке dispatch |
| Диспетчер состояний | `RunDispatch` направляет изменение состояния member через индекс `{member link, address family}` в единственную группу-владельца |
| Actuator hook (Section 5) | `MicroBFDActuator` получает события member state после обновления группы, вне блокировок Manager, в порядке dispatch, с context deadline 5 s на вызов |
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

Каждый VXLAN/Geneve listener владеет одним выделенным inner source-port lease;
закрытие listener возвращает порт ровно один раз. Session senders используют
общий listener без права освобождать его порт. Send и receive учитывают отмену
context, включая ожидание очереди отправки; отмена не закрывает исправный listener
и не оставляет deadline для следующих операций. `Close` разблокирует ожидающий
socket I/O. Полученный payload использует буфер listener до следующего receive,
поэтому у каждого listener один receiver loop.

[Проверка overlay на проводе](./09-development.md#проверка-overlay-на-проводе)
проверяет этот userspace IPv4-профиль через реальные UDP-пакеты с результатами
для сверки с capture. Owner-specific backends и vendor forwarding paths она
не квалифицирует.

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
| Установка IPv4 DF (IP_PMTUDISC_DO) | `WithDFBit()` на `UDPSender` для ненулевых targets YAML/API |
| Заполнение нулями | `cachedPacket` расширяется нулевыми байтами после BFD payload |
| Конфигурация per-session | YAML `padded_pdu_size`, API `AddSessionRequest.padded_pdu_size`, CLI `--padded-pdu-size` |
| Статус сессии в API | `BfdSession.padded_pdu_size` передаёт настроенный target в байтах; authenticated PDU может быть длиннее |
| Глобальное значение по умолчанию | `bfd.default_padded_pdu_size` в YAML конфигурации |
| Диапазон настроенного target | 0 отключает padding; 24-9000 байт без аутентификации, 26-9000 с аутентификацией (политика v1) |

Проверка Keyed MD5 и SHA1 вычисляет digest точно до объявленного BFD `Length`,
включая extension bytes внутри PDU. Перед вычислением проверяется, что digest
помещается в `Length`, а `Length` — в принятый payload. Transport padding
находится за этой границей; его содержимое не проверяется согласно
[RFC 9764 Section 3](../rfc/rfc9764.txt) и
[RFC 5880 Sections 6.7.3-6.7.4](../rfc/rfc5880.txt). Simple Password проверяет
пароль и не вычисляет digest пакета. Независимые wire vectors покрывают все
пять типов аутентификации без padding и с payload 64, 128 и 9000 байт.

Утверждённая политика конфигурации v1 отклоняет authenticated targets 24 и
25 байт. YANG typedef RFC 9764 также допускает увеличение меньшего настроенного
target до минимума сессии; отклонение — выбранная политика GoBFD.
Допустимый target 26 не усекает более длинный authenticated PDU: передача
использует `max(actual PDU length, configured target)`. YAML global default
и per-session override проверяются по общей политике до runtime changes;
raw numeric inputs проверяются до возможного wrap или усечения weak decoder.

Пул приёма обычного UDP сохраняет полный BFD transport payload до 9000 байт;
`MaxPacketSize` кодека остаётся равным 64 байтам. Linux отклоняет `MSG_TRUNC`
до разбора metadata и пакета. Listener проверяет некорректные transport read
length до среза и возвращает заимствованный буфер. Аутентифицированный приём
сохраняет отдельную копию полного wire payload.

Буферы userspace overlay учитывают framing вокруг того же inner BFD limit
9000 байт: VXLAN — 9090 байт с inner IPv4 header до 60 байт; Geneve — 9342
байта с тем же IPv4 header и options до 252 байт. Запас под framing не
разрешает inner BFD payload больше 9000 байт. Эти ограничения описывают
transport buffers и не подтверждают поддержку соответствующего path MTU.

Локальное покрытие hash boundary и конфигурации относится к
`gobfd-qj0.8.2.1.5.2`. [Критический BASE gate](05-interop.md) квалифицирует
128-байтные Control payload GoBFD к FRR Simple Password и BIRD Keyed SHA1:
неизменный BFD Length, нулевой padding и IPv4 DF. Обратное направление
использует обычные authenticated PDU пиров; padded peer-originated traffic
покрывают локальные vectors, а не этот interop-запуск. Проверка path MTU
остаётся отдельной задачей; стабильное соответствие
RFC 9764 остаётся частичным.

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
