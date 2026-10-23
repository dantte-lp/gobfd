# Архитектура

![Go](https://img.shields.io/badge/Go-1.27-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![RFC 5880](https://img.shields.io/badge/RFC-5880-1a73e8?style=for-the-badge)
![ConnectRPC](https://img.shields.io/badge/ConnectRPC-gRPC-ea4335?style=for-the-badge)
![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C?style=for-the-badge&logo=prometheus)
![Linux](https://img.shields.io/badge/Linux-Raw_Sockets-FCC624?style=for-the-badge&logo=linux&logoColor=black)

> Архитектура GoBFD: зависимости пакетов, путь пакета, жизненный цикл сессии и модель горутин.

---

## Содержание

- [Обзор системы](#обзор-системы)
- [Диаграмма зависимостей пакетов](#диаграмма-зависимостей-пакетов)
- [Правила зависимостей](#правила-зависимостей)
- [Путь приёма пакета (RX)](#путь-приёма-пакета-rx)
- [Путь отправки пакета (TX)](#путь-отправки-пакета-tx)
- [Демультиплексирование](#демультиплексирование)
- [Идентичность и ownership сессий](#идентичность-и-ownership-сессий)
- [Трёхстороннее рукопожатие](#трёхстороннее-рукопожатие)
- [Модель горутин](#модель-горутин)
- [Корректное завершение](#корректное-завершение)
- [Структура проекта](#структура-проекта)
- [Технологический стек](#технологический-стек)

### Обзор системы

GoBFD -- production-oriented демон протокола BFD (Bidirectional Forwarding Detection). Состоит из четырёх бинарных файлов:

- **gobfd** -- демон, управляющий BFD-сессиями, отправляющий/принимающий BFD Control пакеты и интегрированный с GoBGP
- **gobfdctl** -- CLI-клиент, взаимодействующий с gobfd через ConnectRPC
- **gobfd-haproxy-agent** -- мост для HAProxy agent-check (состояние BFD в TCP-ответы агента)
- **gobfd-exabgp-bridge** -- мост для ExaBGP process API (состояние BFD в анонсы маршрутов)

```mermaid
graph TB
    subgraph "gobfd daemon"
        MAIN["cmd/gobfd<br/>main.go"]
        CFG["internal/config<br/>koanf/v2"]
        SRV["internal/server<br/>ConnectRPC"]
        BFD["internal/bfd<br/>FSM + Sessions"]
        NET["internal/netio<br/>Raw Sockets"]
        MET["internal/metrics<br/>Prometheus"]
        BGP["internal/gobgp<br/>GoBGP Client"]
        PB["pkg/bfdpb<br/>Generated Proto"]
    end

    subgraph "gobfdctl CLI"
        CLI["cmd/gobfdctl<br/>Cobra + reeflective/console"]
    end

    subgraph "External"
        GOBGP["GoBGP<br/>gRPC :50052"]
        PROM["Prometheus<br/>:9100/metrics"]
        PEER["BFD Peers<br/>UDP 3784/4784/3785/6784/4789/6081/7784"]
    end

    MAIN --> CFG
    MAIN --> SRV
    MAIN --> NET
    MAIN --> MET
    MAIN --> BGP
    SRV --> BFD
    SRV --> PB
    NET --> BFD
    CLI --> SRV
    BGP --> GOBGP
    MET --> PROM
    NET --> PEER

    style BFD fill:#1a73e8,color:#fff
    style NET fill:#34a853,color:#fff
    style SRV fill:#ea4335,color:#fff
```

### Диаграмма зависимостей пакетов

```mermaid
graph TB
    subgraph "cmd/"
        GOBFD["cmd/gobfd<br/>main.go"]
        GOBFDCTL["cmd/gobfdctl<br/>main.go + commands/"]
    end

    subgraph "internal/"
        CFG["config<br/>koanf/v2"]
        SRV["server<br/>ConnectRPC"]
        BFD["bfd<br/>FSM, Session,<br/>Packet, Auth"]
        NET["netio<br/>Raw Sockets,<br/>UDP Listeners"]
        MET["metrics<br/>Prometheus"]
        BGP["gobgp<br/>gRPC Client"]
        VER["version<br/>Build Info"]
    end

    PB["pkg/bfdpb<br/>Generated Proto"]

    GOBFD --> CFG
    GOBFD --> SRV
    GOBFD --> NET
    GOBFD --> MET
    GOBFD --> BGP
    GOBFD --> VER
    SRV --> BFD
    SRV --> PB
    NET --> BFD
    GOBFDCTL --> PB

    style BFD fill:#1a73e8,color:#fff
```

Ключевые пакеты:

| Пакет | Ответственность |
|---|---|
| `internal/bfd` | Ядро протокола: FSM, сессии, кодек пакетов, аутентификация |
| `internal/netio` | Абстракция сырых сокетов, UDP-слушатели, overlay-туннели (Linux) |
| `internal/server` | ConnectRPC-сервер с перехватчиками (логирование, recovery) |
| `internal/config` | Конфигурация через koanf/v2: YAML + env + флаги |
| `internal/metrics` | Prometheus-коллекторы для BFD-сессий |
| `internal/gobgp` | gRPC-клиент к GoBGP + демпфирование flap-ов (RFC 5882) |
| `pkg/bfdpb` | Сгенерированные protobuf-типы (публичный API) |

### Правила зависимостей

- `internal/bfd` **не зависит** от `internal/server`, `internal/netio` или `internal/config`
- `internal/server` зависит от `internal/bfd` (Manager, Session, типы) и `pkg/bfdpb`
- `internal/netio` переиспользует кодек и пул пакетов, метаданные пакетов,
  интерфейс отправки и типы состояния/событий Micro-BFD из `internal/bfd`
- `pkg/bfdpb` -- сгенерированный код, никогда не редактируется вручную

### Путь приёма пакета (RX)

```mermaid
flowchart TD
    NET["Network<br/>UDP 3784 / 4784"] --> LISTEN["netio.Listener<br/>ReadMsgUDP<br/>PacketPool.Get()"]
    LISTEN --> UNMARSHAL["bfd.UnmarshalControlPacket<br/>RFC 5880 steps 1-7<br/>version, length, detect mult,<br/>multipoint, discriminators"]
    UNMARSHAL --> DEMUX["Manager.DemuxWithWire<br/>Tier 1: YourDiscr (O1 map)<br/>Tier 2: PeerKey (SrcIP, DstIP, If)"]
    DEMUX --> RECV["Session.RecvPkt<br/>buffered chan"]
    RECV --> HANDLE["handleRecvPacket<br/>RFC 5880 steps 8-18:<br/>auth, FSM event, timer reset"]
```

Валидация из 13 шагов RFC 5880 Section 6.8.6 разделена на два уровня:

| Уровень | Шаги | Ответственность |
|---|---|---|
| **Кодек** (`packet.go`) | 1-7 | Версия, длина, detect mult, multipoint, дискриминаторы (без состояния) |
| **Сессия** (`session.go`) | 8-18 | Проверка auth, событие FSM, обновление таймеров, обновление переменных |

Это разделение позволяет слушателю отбрасывать невалидные пакеты до захвата блокировки сессии.

### Путь отправки пакета (TX)

```mermaid
flowchart TD
    TIMER["txTimer fires<br/>jittered per RFC 5880 6.8.7"] --> CHECK["maybeSendControl<br/>passive role check,<br/>RemoteMinRx check"]
    CHECK --> REBUILD["rebuildCachedPacket<br/>reused buffer<br/>rebuilt before each send"]
    REBUILD --> SEND["PacketSender.SendPacket<br/>raw UDP socket"]
```

**Переиспользуемый packet buffer:** `sendControl` вызывает `rebuildCachedPacket`
перед каждой отправкой, включая сериализацию header и authentication. Рост
padding готовит один waiting buffer при control admission; owner меняет его до
commit полей. Packet loop использует готовую capacity без growth allocation.

### Демультиплексирование

Двухуровневый поиск по RFC 5880 Section 6.8.6:

1. **Уровень 1** -- Your Discriminator ненулевой: O(1) поиск по дискриминатору. Быстрый путь для установленных сессий.
2. **Уровень 2** -- Your Discriminator нулевой И состояние Down/AdminDown: поиск по составному ключу (SrcIP, DstIP, Interface). Используется только при первоначальном установлении сессии.

### Идентичность и ownership сессий

`SessionKey` -- сравнимый канонический идентификатор одной желаемой wire
session. Он содержит тип сессии и семейство адресов, нормализованные адреса
peer и local, interface, network scope и transport scope. IPv4-mapped адреса
нормализуются в IPv4. Этот идентификатор отделён от более узкого ключа packet
demultiplexing, который используется только для доставки начального пакета.

Для включённых unsolicited-интерфейсов открываются отдельные wildcard
single-hop listeners IPv4 и IPv6 с `SO_BINDTODEVICE`, независимо от настроенных
сессий. Ancillary data сохраняет фактический destination и ingress index.
Сначала выполняется lookup настроенного peer; для несовпавшего пакета unbound
listener разрешает ingress index по startup snapshot включённых интерфейсов,
затем выполняются unsolicited lookup и admission. Системный поиск имени
интерфейса на пути пакета отсутствует. Изменения интерфейсов и политики
unsolicited требуют перезапуска.

Owner получает признак единственного unsolicited claim через существующие
timer-intent lock и wakeup. В этом режиме ownership останавливается
non-Final Control TX при natural failure или независимом setup deadline.
Окно покрывает наибольший наблюдаемый local RX Detection Time от начала попытки;
повторный валидный RX его не перезапускает. Смена claims сохраняет исходное окно
и failure latch. Configured/API claims сохраняют обычный Control TX даже при
истечении private setup window. Setup expiry сохраняет FSM и
diagnostic RFC 5880. Private notification запрашивает cleanup без публикации
фиктивного перехода состояния. Cleanup проверяет локальную попытку под owner
lock и запрещает возобновление до удаления единственного claim; shared
configured/API claims сохраняют wire session. Final и local AdminDown сохраняют
процедуры RFC 5880.

Manager хранит типизированные claims базовой конфигурации, Micro-BFD members,
VXLAN, Geneve, compatibility/API-пути и unsolicited BFD. Изменения ownership
сериализованы. Каждый декларативный adapter реконсилирует только свой
типизированный source. Claims с одинаковым каноническим ключом и эффективными
параметрами разделяют одну wire session и discriminator; освобождение одного
claim сохраняет остальные, а освобождение последнего уничтожает wire session.
Пустые декларативные наборы передаются в Manager, поэтому удаление последней
записи освобождает только claims этого source. Демон проверяет полный
объединённый candidate control sessions base, Micro-BFD members, VXLAN и
Geneve до применения любого из этих sources. Compiler каждого source также
проверяет полный набор до открытия sender adapter или изменения session
ownership.

Daemon-level coordinator сериализует компиляцию и применение startup и SIGHUP.
Он хранит deep-copied контракт startup-runtime. Каждый SIGHUP candidate
сверяется с этим контрактом до компиляции candidate и до изменения generation,
health, log level, sessions или ресурсов. Startup-owned поля servers, unsolicited listeners,
sockets, policy, actuator, GoBGP и глобальные поля overlay backend/VNI
отклоняются с ограниченными
идентификаторами полей; значения конфигурации и secrets в ошибке не сохраняются.
Изменения desired-set membership могут потребовать новые listeners control,
Echo или Micro-BFD либо overlay backends, которые их владельцы открывают в
составе generation. Base sessions с единственным config-owner и стабильной
wire identity принимают in-place TX/RX, Detect Multiplier, multihop minimum-TTL
и ограниченные padding changes через единый tuple из пяти полей и
generation/revision receipts с прежней authentication. Detect-only Async update
применяется локально без Poll; remote Demand при обоих концах Up требует
Poll/Final. Комбинация с TX/RX сохраняет directional deferral, активный
объявленный tuple и последний queued proposal. Local TTL-only update публикует
receive floor под `Session.mu` без Poll даже при Demand. Padding допускает
только positive-to-positive changes в Async на sender с constructor proof DF
preparation. Неизвестный/неподготовленный sender, padding enable/disable и
shared-owner/API base padding updates отклоняются до generation acceptance.
Coordinator проверяет только `candidate.base`; Micro-BFD/overlay остаются вне
допустимого профиля, conflict/stale других sources не меняется. Owner повторно
проверяет queued policy до commit любого поля,
если Demand включился во время ожидания. Неподдерживаемые same-identity authentication changes
отклоняются до принятия generation. [ADR-0006](adr/0006-session-preserving-reload-policy.md)
принимает общую policy, но не квалифицирует реализацию или live peers.
Coordinator публикует монотонное desired generation только после этой проверки
и компиляции полного неизменяемого session candidate. Один receipt затем хранит claim-level счётчики
`created`, `released`, `pending` и `failed`, а также ограниченную гистограмму
кодов ошибок ровно для пяти sources: base, Echo, Micro-BFD (группы вместе с
member-сессиями), VXLAN и Geneve. Applied generation продвигается только при convergence
всех sources. Частичный проход сохраняет последнее applied generation,
помечает snapshot как stale и не откатывает изменения, уже принятые другим
source. Следующий SIGHUP создаёт явное новое generation и может привести runtime
к convergence. Недоступные interface claims переходят в `pending` после
preflight всего source без освобождения старых claims. Linux link up/down/delete
events запускают проверку и retry затронутых pending sources в том же generation.
Фиксированная маска sources и wake-up channel ёмкостью один объединяют события;
устаревшие generations и retry converged sources отбрасываются. Переполнение
netlink или канала запрашивает полную сверку интерфейсов. Monitor закрывает
канал; отмена контекста завершает consumer и worker. Non-Linux stub не выдаёт
событий и не обеспечивает автоматическое восстановление. Владелец listeners
(`cmd/gobfd/listeners.go`) делает приёмные сокеты частью применения каждого
source: sources base, Echo и Micro-BFD захватывают свои listeners и открывают
недостающие до apply в Manager, поэтому source не может сойтись только на
senders. Отсутствующий интерфейс listener оставляет source в `pending` для
interface retry. После применения source без failed и resource-pending записей
source снимает claims вне своего desired set. Control- и Echo-сессии,
созданные через API, захватывают listeners вместе с senders через того же
владельца под одним mutex и завершают запрос ошибкой, если listener не
открывается; listener закрывается при снятии последнего claim. Каждый listener выполняет собственный обязательный receiver;
намеренное закрытие сначала отменяет этот receiver, поэтому fail-stop вызывает
только неожиданная потеря сокета. Listener с привязкой к устройству для
пересозданного интерфейса перепривязывается на месте через `SO_BINDTODEVICE`.
Владелец overlay (`cmd/gobfd/overlay_owner.go`) делает то же для sources VXLAN и
Geneve: один backend на вид туннеля и outer local address открывается при первом
claim, расширяется на месте при добавлении identity (`SetScopes` подменяет
copy-on-write набор identities, который читается одной атомарной загрузкой на
пакет), сужается после чистого применения и закрывается, когда опустеет;
работающие сессии не меняют сокет. Каждый local endpoint захватывается
независимо, поэтому endpoint, который не открывается, переводит в pending или
failed только свои identities. Классифицируются только `ENODEV` от привязки
listener к устройству (недоступный интерфейс) и `EADDRNOTAVAIL` от bind listener,
sender или overlay (недоступный адрес); оба оставляют source в `pending`.
Address events и сверка повторяют применение всех pending sources без влияния
на link. Остальные runtime failures учитываются как `failed` без классификации
по строкам.

Проверка gRPC health с пустым service возвращает `NOT_SERVING` до первого
converged generation и пока desired отличается от applied; после convergence
она возвращает `SERVING`, если ни один runtime worker не потерян и каждый
интерфейс listener с привязкой к устройству включён и сохраняет ifindex
последнего bind. Один агрегатор (`cmd/gobfd/readiness.go`) выводит named services BFD, Echo
и Micro-BFD из их sources, listeners и runtime workers; service
`grpc.health.v1.Health` остаётся `SERVING`. Потеря receiver или dispatch loop
приводит к fail-stop: health переходит в `NOT_SERVING`, daemon завершается с
ошибкой (см. `07-monitoring.md`). SIGHUP регистрируется в bounded channel до startup apply, но
handler запускается только после startup reconciliation, поэтому ранний reload
не может примениться раньше startup candidate. systemd `READY=1` остаётся
сигналом готовности процесса/listeners и отправляется даже при stale
configuration health.

Для новой принятой physical session Manager лениво открывает один sender lease
и хранит его идемпотентную release-операцию в session entry. Неизменившаяся
reconciliation и совпадающий claim другого source не открывают новый sender.
Creation rollback, освобождение последнего claim и закрытие Manager освобождают
принятый lease один раз; освобождение не последнего claim сохраняет его. Base
configuration, Micro-BFD и compatibility API используют owning UDP leases,
которые также возвращают выделенный source port. Overlay sessions используют
явные non-owning leases, потому что их sender разделяет backend connection.
Каждая RFC 9468 session владеет UDP lease на локальном адресе и интерфейсе,
на которые пришёл пакет, создавший её; cleanup session закрывает этот lease
один раз.

Echo sessions используют ту же границу lease принятой session с отдельными API
и declarative sources. Compatibility-путь `CreateEchoSession` сохраняет явный
non-owning wrapper для raw sender, а API adapter и declarative reconciler
передают ленивые owning factories. Declarative reconciliation проверяет полный
canonical candidate до открытия senders, не доверяет adapter-supplied keys как
identity, сохраняет API-created Echo sessions и передаёт пустой desired set.
Ошибка получения sender откатывает только новые принятые declarative Echo
sessions текущего прохода; removal и shutdown ровно один раз освобождают каждый
принятый lease после завершения Echo goroutine.

Для сессий с аутентификацией эффективные параметры включают тип встроенного
authenticator и неизменяемый fingerprint `StaticAuthKeyStore`. Static store
клонирует входные данные конструктора и возвращает caller-owned копии ключей;
неизвестные реализации key store отклоняются, потому что для них нельзя
получить стабильную семантическую идентичность.

`Manager.AdmitSessionReload` проверяет stable-field/ownership policy и
сравнивает authentication fingerprints с retained accepted
base intent, включая записи без live session, и live Manager registry независимо
от API/shared ownership. Enable/disable, algorithm, key IDs/material, выбранный
TX key и receive-key-set changes отклоняются до изменений generation, log level,
readiness, configured peers или ресурсов. Прежние accepted configuration и
session сохраняются; аутентификация при startup, новые/удаляемые identities и
новые stores с прежней семантикой остаются допустимыми.
Порядок блокировок: lifecycle → ownership → registry. Registry locks
освобождаются до короткого локального acceptance; ownership удерживается до
публикации desired generation и освобождается до callbacks или resource apply.
Поздние API claims проверяются обычной fail-closed reconciliation. Граница не
делает последующий whole-source apply атомарным и не гарантирует бесшовный rollback.

Это ownership core C01.1 вместе со slice C01.2 для atomic candidate/изоляции
sources, slice C01.3a для sender lease принятой control session, slice C01.3b
для lifecycle Manager, slice C01.4a для sender lease/изоляции source Echo и
slice C01.4b для coordinator generations/receipts, а
не полный контракт v1 reconciliation или RFC. Отложены следующие границы:

- runtime-изменение глобальных полей overlay backend и VNI по умолчанию;
- стабильные owner identifiers для отдельных groups и tunnels;
- полная Poll/Demand qualification за пределами ограниченных config-owned TX/RX/Detect Multiplier updates;
- transport-aware packet demultiplexing; и
- идентичности аутентифицированных API principals вместо одного
  compatibility/API owner.

### Трёхстороннее рукопожатие

BFD-сессии используют трёхстороннее рукопожатие (RFC 5880 Section 6.2):

```mermaid
sequenceDiagram
    participant A as Peer A (Down)
    participant B as Peer B (Down)

    A->>B: Control(State=Down)
    Note over B: Down -> Init
    B->>A: Control(State=Down)
    Note over A: Down -> Init

    A->>B: Control(State=Init)
    Note over B: Init -> Up
    B->>A: Control(State=Init)
    Note over A: Init -> Up

    A->>B: Control(State=Up)
    B->>A: Control(State=Up)
    Note over A,B: Both peers Up
```

Последовательность переходов FSM:

1. A(Down) отправляет State=Down. B(Down) получает State=Down --> B переходит в Init.
2. B(Init) отправляет State=Init. A(Down) получает State=Init --> A переходит в Up.
3. A(Up) отправляет State=Up. B(Init) получает State=Up --> B переходит в Up.

### Модель горутин

Владелец сессии фиксирует под `Session.mu` причинный кортеж: локальные
state/diagnostic, remote-known/state/diagnostic, оба C-bit, cause и
`ObservationVersion`. Локальная пара дополнительно хранится в одном atomic
для scalar getters и сборки пакета. Принятые remote facts публикуются вместе
с итоговой локальной парой. Обработка перехода таймеров видит старое состояние
до commit; журналирование, события и отправка выполняются после снятия lock.
Изменение только диагностики/remote facts не увеличивает счётчики переходов и
не меняет `LastStateChange`; неизменившийся пакет не увеличивает версию.

Часы Manager выдают свежие версии регистрации, семантического commit и
удаления. `Revision` отдельно упорядочивает dispatch; snapshot может опережать
очередь событий владельца. Версии сравниваются для каждой строки до замены
или удаления. Внутренний `SessionRef` различает повторное использование
дискриминатора; clock не является общей snapshot watermark.
Control и Echo Watch передают полную API-строку статуса по значению.
Причинный кортеж берётся из явного commit владельца; конфигурация, таймеры,
счётчики и timestamps измеряются на границе публикации или List отдельно от
кортежа. Публикация владельца следует за обновлением счётчиков/`LastStateChange`
и предшествует немедленной Control-отправке, поэтому её TX count может ещё не
учитывать эту отправку. Телеметрия не увеличивает `ObservationVersion` на
каждом пакете.

Control delivery разделяет wire parse, base TTL или overlay decapsulation,
demux, unsolicited policy, admission и owner processing. `Queued` подтверждает
только admission. Полная или остановленная очередь возвращает типизированный
отказ. Заголовок копируется по значению; authenticated wire storage остаётся
неизменяемым до owner processing. Общий admission/stop lock запрещает enqueue
после terminal drain. Первый `Run` с отменённым context один раз учитывает
оставшуюся работу как abandoned; квитанция abandoned AdminDown не подтверждает
commit. Полная очередь AdminDown сохраняет ожидание capacity/context без
блокирования RX.

`ListSessions.delivery_counters` и Prometheus с фиксированными метками хранят
решения стадий за время жизни процесса, включая удалённые сессии. У каждой стадии
свой знаменатель; сумма ledger не является количеством пакетов. Каждый bucket
измеряется один раз в фиксированном порядке, включая нулевые строки; lifecycle
revision и observation version не обеспечивают atomic snapshot счётчиков.
Session drop/auth являются проекцией terminal reasons, auth входит в drops;
проекции не прибавляются повторно в глобальный ledger. Echo admission и один
winning owner используют общий terminal barrier: pre-Run admission разрешён,
full/closed возвращают типизированные отказы, stop один раз учитывает pending
RX как abandoned. Raw Echo notifications используют общий с Control
сравнимый `SessionRef` с одним private owner pointer. Managed Echo registration,
state commits и removal используют общий Manager observation clock. Coherent
Echo tuple содержит local state, diagnostic, cause и version; initial и
returned-Echo имеют CauseUnknown, detection expiry — CauseDetectionTimeout.
Watch проецирует фактические Echo TX interval, detection parameters,
sent/received counts, transitions и timestamps в существующий status.
Remote, negotiated, RX, authentication и drop fields, которыми Echo не владеет,
равны нулю; нулевые Echo drop/auth fields не доказывают отсутствие потерь.

Raw Control/private/Echo notifications, dispatch, каждый Watch recipient и
legacy delivery имеют отдельные ограниченные стадии ledger. GAP marker
сообщает потерю исходных событий и не считается успешно доставленным original.
Raw shutdown drain выполняется после join active operations и workers.

Private unsolicited failure коммитится до coalesced capacity-one cleanup hint,
независимого от best-effort raw admission. Dispatch сканирует failures только
при старте и по coalesced hints. Initial attempt zero без failure cause
сохраняется; committed Down DetectionTimeout/PathDown при attempt zero допускает
cleanup. Retained physical entry лениво получает не более одного parked worker,
одного переиспользуемого timer и одного pending attempt/deadline. Дубликат не
продлевает retention, новая failure заменяет pending work. Retirement сохраняет
identity, current-attempt, AdminDown и shared-claim fences. Отмена dispatch не
обещает restart/progress; Manager.Close ожидает workers и освобождает ресурсы.
Worker bound определяется retained physical entries, а не MaxSessions.

Public Control/Echo raw QueueFull передаёт отдельный capacity-one loss hint
после owner commit и до внешних delivery callbacks. Dispatch потребляет hint
до постановки нового revisioned GAP каждому subscriber, включая уже
прочитавшего предыдущий GAP. Полный subscriber уже содержит непрочитанный GAP;
его последующий snapshot покрывает committed loss. Recovery markers не
учитываются как исходные dispatch, recipient или legacy deliveries и не
вызывают Micro actuators. Watch получает снимок обоих реестров; GoBGP сохраняет
Control-only snapshot. Observation-version floors отклоняют старые raw events,
доставленные после снимка. При работающем dispatch следующий owner edge или
reconnect не требуется. Private failure hints сохраняют cleanup-only область;
legacy `StateChanges()` остаётся best effort без resynchronization. Kernel
`SO_RXQ_OVFL` остаётся отдельной задачей; unsolicited/Echo сохраняют текущие
границы preview qualification.

Регистрация публикует `SESSION_ADDED` после добавления в реестры
и до запуска владельца, включая пассивную сессию, остающуюся Down.
Контракт охватывает Control-реестр (base, Micro-BFD, VXLAN/Geneve и unsolicited)
и отдельный Echo-реестр. Регистрация доставляется revisioned-подписчикам без Micro-актуатора
и unsolicited cleanup; legacy `StateChanges()` сохраняет FSM/removal events.
Control и Echo updates/tombstones заменяют строку клиента целиком, включая zero,
false, NONE и отсутствующие timestamps. Echo removal сохраняет final tuple с
новой global version до cancellation/join и освобождения lease. API delete,
declarative removal и rollback используют общий detach. Только Watch снимает
оба реестра под одним Manager read lock, читая dispatch revision до чтения
обоих реестров. Control List/Get, GoBGP snapshots и typed Echo RPC сохраняют прежнюю
область. Manager.Close закрывает streams для reconnect без гарантии terminal
tombstone для каждой сессии.

GoBGP сохраняет известный remote AdminDown при последующем timeout и
подавляет административные Down. Явный локальный PathDown действует и из
Down. Его cause сохраняется при последующем timeout в Down; Diag становится
1 без перезапуска ожидающего routing action. Перед каждым disable,
включая возврат блокирующего `PeerAdminDown`,
проверяются текущие canonical identity и административная policy. Локальная
проверка не делает удалённый RPC атомарным с будущими BFD commit.
C-bit только сохраняется и отображается; политика остаётся hard-fail.

Каждая BFD-сессия работает как независимая горутина с собственными таймерами и
состоянием. Её контекст отделён от сигнального контекста демона, чтобы SIGTERM
не остановил сессию до AdminDown drain. `Manager.Close()` явно отменяет
индивидуальный контекст сессии и ожидает завершения каждой зарегистрированной
session- и echo-горутины до освобождения sender lease или discriminator.

Lifecycle Manager имеет состояния `Open -> Closing -> Closed`. После начала
`Closing` новые session claims, reconciliation, unsolicited claims и подписки
на изменения состояния отклоняются стабильными lifecycle errors без мутаций.
Демон по-прежнему запускает `RunDispatch` из своего errgroup, но Manager
регистрирует этот запуск и владеет его остановкой. `RunDispatch` допускает
только один запуск: второй вызов сразу возвращается. Отмена caller context или
`Manager.Close()` останавливает dispatch и ровно один раз закрывает legacy-
канал `StateChanges()`. Канал каждой отдельной подписки ровно один раз закрывает
её зарегистрированная goroutine при отмене subscriber context или закрытии
Manager.

После lifecycle transition Close отделяет registries под `ownershipMu` и
`manager.mu`, а затем освобождает оба lock до cancellation и ожидания
goroutines. Callback освобождения sender выполняется только после завершения
соответствующей session goroutine и без обоих locks; после него по порядку
освобождаются discriminator и регистрация metrics. Конкурентные вызовы Close
ожидают один и тот же результат shutdown. Operation освобождает lifecycle
mutation lock до detached или unused sender callbacks, поэтому Close может
перейти в `Closing`, но сохраняет регистрацию active operation до завершения
callback, освобождения discriminator и cleanup metrics. Поэтому Close ожидает
весь cleanup, а callback освобождения может вызывать snapshot APIs Manager и
пытаться выполнить mutation APIs, которые возвращают `ErrManagerClosing`. Он не
должен рекурсивно вызывать блокирующий `Manager.Close()`: synchronous callback
входит в завершение того же Close, поэтому два вызова будут ожидать друг друга.
Явный recursive-safe callback или асинхронный shutdown API отложен в
`gobfd-qj0.8.2.2.5.1`, чтобы не ослаблять семантику конкурентных Close.

Lifecycle gate охватывает создание и удаление control- и echo-sessions,
reconciliation control- и echo-sessions, создание, удаление и reconciliation
Micro-BFD groups, unsolicited claims и регистрацию subscriptions. Каждая
top-level reconciliation удерживает одну lifecycle operation и вызывает
внутренние helpers без дополнительного gate, предотвращая nested read-lock
deadlock, когда Close уже ожидает write-side lifecycle transition.

```mermaid
graph TB
    subgraph "Manager"
        M["Manager goroutine<br/>session CRUD"]
    end

    subgraph "Session N goroutines"
        S1["Session 1<br/>TX timer + RX channel"]
        S2["Session 2<br/>TX timer + RX channel"]
        SN["Session N<br/>TX timer + RX channel"]
    end

    subgraph "Shared Receivers"
        L["netio.Listener<br/>ReadMsgUDP goroutine"]
        R["netio.Receiver<br/>demux + dispatch"]
        ER["netio.EchoReceiver<br/>port 3785"]
        OR["netio.OverlayReceiver<br/>VXLAN 4789 / Geneve 6081"]
        MD["MicroBFD Dispatch<br/>port 6784 per-member"]
    end

    L --> R
    R --> S1
    R --> S2
    R --> SN
    ER --> S1
    OR --> S2
    MD --> SN
    M --> S1
    M --> S2
    M --> SN
```

Ключевые принципы:
- Отправитель закрывает каналы
- Время жизни горутины = время жизни контекста
- Каждая горутина обязана иметь механизм завершения

### Корректное завершение

При SIGTERM/SIGINT (RFC 5880 Section 6.8.16):

1. `Manager.DrainAllSessions(ctx)` -- снимок конкретных сессий, освобождение
   Manager locks и параллельное ожидание owner commit, успешной записи AdminDown/7
   и negotiated notification window. Все стадии используют общий новый deadline
   десять секунд, независимый от отменённого signal context. `DrainIncompleteError`
   сохраняет стадию, причину suppression и исходную ошибку.
2. Передача Control в течение окна с retry через существующий TX timer и
   сохранением RFC transmission suppression
3. `Manager.Close()` -- переход в Closing, отделение sessions, отмена и
   ожидание зарегистрированных Manager goroutines, закрытие notification
   channels и освобождение sender resources
4. Закрытие сокетов слушателей
5. Остановка HTTP-серверов (gRPC, метрики)

`Session.SetAdminDown(ctx)` использует существующий control channel, в том числе
до `Run`. Заполненная очередь ожидает capacity, отмену контекста или выход owner;
вызывающая сторона не изменяет состояние сессии. Receipt подтверждает state,
cached state и diagnostic владельца до немедленной попытки передачи. Timeout
оставляет принятый запрос в очереди; выход owner возвращает `ErrSessionStopped`.
Повторный запрос подтверждает commit без нового перехода или немедленного TX burst.
`Run` допускает одного owner за время жизни сессии и обрабатывает стартовые
команды до передачи по таймеру, сохраняя ограничения Passive role.

Окно начинается с первой успешной записи AdminDown и равно максимуму local RX
Detection Time и peer-facing границы
`localDetectMult * max(advertisedAdminDownTX, remoteMinRX)`. Non-Up TX floor
даёт три секунды для стандартных параметров. Увеличение negotiation продлевает
deadline; неизменные или меньшие значения не перезапускают и не сокращают окно.
Конкурентные drain используют общий progress; выход owner до конца окна не
превращается в успех после ожидания. Успех подтверждает локальные kernel writes
и длительность уведомления, без подтверждения получения пиром.

Owning UDP sender lease прерывает pending write при отмене сессии и ожидает
callback до owner completion и освобождения source port. Non-owning shared
overlay sender не прерывается сессионным lease. `Manager.Close()` не гарантирует
границу для произвольного внешнего `PacketSender` или release callback.

Освобождение последнего Control-session claim отсоединяет сохранённую identity,
затем использует тот же drain до cancel/join и освобождения sender/discriminator.
Общий путь охватывает API, base configuration, Micro-BFD members, VXLAN, Geneve
и rollback принятой новой сессии. Нефинальный claim не
изменяет wire session. Внутренний предел десять секунд действует и для
Background; более ранняя отмена caller возвращает `DrainIncompleteError`, после
чего cleanup освобождает ресурсы. Reconciliation выполняет drain отсоединённых
сессий параллельно в одном бюджете. Ожидание не удерживает Manager locks;
замена с тем же ключом не наследует drain прежней сессии. `Manager.Close()`
выполняет только cancel/join/release: daemon shutdown уже выполнил общий drain.
Failure cleanup unsolicited Down-сессии выполняет cancel без AdminDown drain:
RFC 9468 Section 2 требует прекратить Control-передачу пассивной стороны.
Standalone Echo и удаление только Micro-BFD group не имеют Control-session
AdminDown exchange. Echo removal публикует Watch tombstone без изменения protocol state.

### Структура проекта

```
gobfd/
+-- api/bfd/v1/bfd.proto          # Protobuf service definitions (buf managed)
+-- cmd/
|   +-- gobfd/main.go             # Daemon entry point
|   +-- gobfdctl/                 # CLI client
|   |   +-- main.go
|   |   +-- commands/             # Cobra commands + reeflective/console shell
|   +-- gobfd-haproxy-agent/      # HAProxy agent-check bridge
|   +-- gobfd-exabgp-bridge/      # ExaBGP process API bridge
+-- internal/
|   +-- bfd/                      # Core protocol (FSM, session, packet, auth)
|   +-- config/                   # koanf/v2 configuration
|   +-- gobgp/                    # GoBGP gRPC client + flap dampening
|   +-- metrics/                  # Prometheus collectors
|   +-- netio/                    # Raw sockets, UDP listeners, overlay tunnels (Linux)
|   +-- sdnotify/                 # уведомления systemd readiness/watchdog
|   +-- server/                   # ConnectRPC server + interceptors
|   +-- version/                  # Build info
+-- pkg/bfdpb/                    # Generated protobuf types (public API)
+-- test/interop/                 # 4-peer interop tests (FRR, BIRD3, Holo, Thoro/bfd)
+-- test/interop-bgp/            # BGP+BFD interop tests (GoBGP, FRR, BIRD3, ExaBGP)
+-- test/interop-rfc/            # RFC-specific interop tests (7419, 9384, 9468)
+-- test/interop-clab/           # Vendor NOS interop tests (Nokia, Arista, Cisco, FRR, SONiC, VyOS)
+-- test/integration/            # Integration tests (datapath, CLI, server)
+-- configs/                      # Example configuration
+-- deployments/
|   +-- compose/                  # Podman Compose (dev + prod stacks)
|   +-- docker/                   # Containerfile + debug image
|   +-- systemd/                  # systemd unit file
|   +-- nfpm/                     # deb/rpm install scripts
|   +-- integrations/            # 5 integration examples (BGP, HAProxy, observability, ExaBGP, k8s)
+-- docs/                         # Documentation + RFC texts
```

### Технологический стек

| Компонент | Технология | Назначение |
|---|---|---|
| Язык | Go 1.27 | Green Tea GC, `testing/synctest`, flight recorder |
| Сетевой I/O | `x/net/ipv4`, `x/net/ipv6`, `x/sys/unix` | Сырые сокеты, управление TTL, `SO_BINDTODEVICE` |
| RPC-сервер | ConnectRPC | gRPC + Connect + gRPC-Web из одного обработчика |
| RPC-клиент | `google.golang.org/grpc` | Интеграция с GoBGP (gRPC-клиент) |
| CLI | Cobra + reeflective/console | Неинтерактивный + интерактивная оболочка |
| Конфигурация | koanf/v2 | YAML + env vars + flags, горячая перезагрузка |
| Метрики | Prometheus `client_golang` | Счётчики, gauge, гистограммы |
| Логирование | `log/slog` (stdlib) | Структурированное JSON/text логирование |
| Protobuf | buf CLI | Линтинг, проверка совместимости, генерация кода |
| Линтинг | golangci-lint v2.14.0 | 92 значимых линтера, проверка схемы и матрицы build tags |
| Релизы | GoReleaser v2 | Бинарники + deb/rpm + контейнерные образы |
| Контейнеры | Podman + Podman Compose | Разработка и тестирование |
| systemd | Type=notify, watchdog | Жизненный цикл production-демона |

### Карта UDP-портов

| Порт | Протокол | RFC | Направление | Статус |
|---|---|---|---|---|
| 3784 | BFD Single-Hop | RFC 5881 | TX + RX | Активен |
| 4784 | BFD Multi-Hop | RFC 5883 | TX + RX | Активен |
| 3785 | BFD Echo | RFC 9747 | TX + RX | Активен |
| 6784 | Micro-BFD (LAG) | RFC 7130 | TX + RX | Активен |
| 4789 | VXLAN BFD (outer) | RFC 8971 | TX + RX | Активен |
| 6081 | Geneve BFD (outer) | RFC 9521 | TX + RX | Активен |
| 7784 | S-BFD Reflector | RFC 7881 | RX (рефлектор) + TX (инициатор) | Планируется |

### Карта TCP/HTTP-портов

| Порт | Протокол | Назначение | Статус |
|---|---|---|---|
| `/run/gobfd/gobfd.sock` | ConnectRPC (gRPC) через Unix-сокет | API управления сессиями; необязательный TCP через `grpc.addr` | Активен |
| 9100 | HTTP | Метрики Prometheus (`/metrics`) | Активен |

### Связанные документы

- [02-protocol.md](./02-protocol.md) -- Детали протокола BFD (FSM, таймеры, формат пакета)
- [03-configuration.md](./03-configuration.md) -- Справочник конфигурации
- [06-deployment.md](./06-deployment.md) -- Развёртывание в production
- [09-development.md](./09-development.md) -- Рабочий процесс разработки

---

*Последнее обновление: 2026-08-28*
