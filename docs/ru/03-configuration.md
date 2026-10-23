# Конфигурация

![YAML](https://img.shields.io/badge/YAML-Config-cb171e?style=for-the-badge&logo=yaml)
![koanf](https://img.shields.io/badge/koanf-v2-00ADD8?style=for-the-badge)
![ENV](https://img.shields.io/badge/ENV-Override-ffc107?style=for-the-badge)
![SIGHUP](https://img.shields.io/badge/SIGHUP-Hot_Reload-34a853?style=for-the-badge)

> Полный справочник конфигурации демона GoBFD: формат YAML-файла, переопределение через переменные окружения, декларативные сессии, интеграция с GoBGP и горячая перезагрузка через SIGHUP.

---

## Содержание

- [Источники конфигурации](#источники-конфигурации)
- [Полный пример конфигурации](#полный-пример-конфигурации)
- [Статус preview-возможностей](#статус-preview-возможностей)
- [Секции конфигурации](#секции-конфигурации)
- [Настройка буферов сокетов](#настройка-буферов-сокетов)
- [Переменные окружения](#переменные-окружения)
- [Декларативные сессии](#декларативные-сессии)
- [Интеграция с GoBGP](#интеграция-с-gobgp)
- [Горячая перезагрузка (SIGHUP)](#горячая-перезагрузка-sighup)
- [Правила валидации](#правила-валидации)
- [Значения по умолчанию](#значения-по-умолчанию)

### Источники конфигурации

GoBFD загружает конфигурацию из трёх источников в порядке приоритета:

1. **Значения по умолчанию** -- разумные production-defaults
2. **YAML-файл** -- указывается через `-config /путь/к/gobfd.yml`
3. **Переменные окружения** -- с префиксом `GOBFD_`

Каждый последующий источник перекрывает предыдущий.

### Полный пример конфигурации

```yaml
# Конфигурация демона GoBFD
# См.: configs/gobfd.example.yml

grpc:
  socket:
    path: "/run/gobfd/gobfd.sock"
    group: "gobfd-admin"

metrics:
  addr: "127.0.0.1:9100"
  path: "/metrics"

log:
  level: "info"       # debug, info, warn, error
  format: "json"      # json, text

bfd:
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  align_intervals: false
  default_padded_pdu_size: 0       # RFC 9764: 0 = без дополнения

# Unsolicited BFD (RFC 9468)
unsolicited:
  enabled: false

# Echo (RFC 9747)
echo:
  enabled: false

# Micro-BFD (RFC 7130)
micro_bfd:
  enabled: false
  groups: []

# VXLAN BFD (RFC 8971)
vxlan:
  enabled: false

# Geneve BFD (RFC 9521)
geneve:
  enabled: false

# Интеграция с GoBGP (RFC 5882 Section 10.2)
gobgp:
  enabled: true
  addr: "127.0.0.1:50051"
  strategy: "disable-peer"   # единственная реализованная стратегия
  action_timeout: "5s"
  tls:
    enabled: false            # включите для удалённого/non-loopback GoBGP API
    ca_file: ""               # опциональный PEM bundle корневых CA
    server_name: ""           # опциональное имя для проверки сертификата
  dampening:
    enabled: true
    suppress_threshold: 3
    reuse_threshold: 2
    max_suppress_time: "60s"
    half_life: "15s"

# Настройка буферов сокетов (снижает потерю пакетов при большом количестве сессий)
socket:
  read_buffer_size: 4194304    # SO_RCVBUF: 4 МиБ (0 = значение ОС)
  write_buffer_size: 4194304   # SO_SNDBUF: 4 МиБ (0 = значение ОС)

# Декларативные сессии (реконсилируются при SIGHUP)
sessions:
  - peer: "10.0.0.1"
    local: "10.0.0.2"
    interface: "eth0"
    type: single_hop
    desired_min_tx: "100ms"
    required_min_rx: "100ms"
    detect_mult: 3
    auth:
      type: keyed_sha1
      key_id: 7
      secret: "change-me"
  - peer: "10.0.1.1"
    local: "10.0.1.2"
    type: multi_hop
    multihop_max_hops: 2             # минимальный принимаемый TTL 253 (255 - 2)
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 5
    padded_pdu_size: 128             # RFC 9764: дополнение PDU per-session
```

### Статус preview-возможностей

<!-- BEGIN GOBFD PREVIEW CAPABILITIES -->
| Profile | RFC | Maturity |
|---|---|---|
| unsolicited | 9468 | preview; unsafe |
| echo | 9747 | preview; incomplete RFC qualification |
| micro_bfd | 7130 | preview; incomplete L2 owner integration |
| vxlan | 8971 | preview; unsafe/incomplete owner integration |
| geneve | 9521 | preview; unsafe/incomplete IPv4 Format A |
<!-- END GOBFD PREVIEW CAPABILITIES -->

Команда `gobfdctl capabilities` (`--format table`, `json` или `yaml`) запрашивает
демон. Read-only RPC `bfd.v1.CapabilityService.ListCapabilities` остаётся
доступен при неготовом forwarding runtime в рамках management transport policy
и не передаёт адреса peers, endpoints или секреты. Каталог принадлежит демону;
локальная подстановка в клиенте отсутствует.

Maturity не зависит от effective enablement и локальной readiness.
Enablement следует валидированным значениям `unsolicited.enabled`,
`echo.enabled`, `micro_bfd.enabled`, `vxlan.enabled` и `geneve.enabled`.
Включённый профиль VXLAN/Geneve без peers не имеет owned backend и остаётся
`not_ready`. Валидный reload обновляет разрешённые к изменению значения; отклонённый
candidate сохраняет значения retained generation. Изменение
`micro_bfd.enabled` по-прежнему требует перезапуска.

Отсутствующее runtime state даёт `unknown` enablement/readiness; выключенный
профиль имеет `not_ready`. Для `ready` необходимы receive/backend path
владельца и локальная готовность runtime, source и bindings. Значение не
доказывает здоровье peers, соответствие RFC или применение L2 actuator.
API-only Echo без receive listener может оставаться `not_ready`, пока API
admission не получит listener; этот coarse catalog не заменяет admission
preflight. Просмотр сохраняет defaults и не включает preview.

### Секции конфигурации

#### `grpc` -- ConnectRPC-сервер

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `addr` | string | `""` | Необязательный TCP-адрес; пустое значение не открывает TCP. Plaintext только на loopback; non-loopback адрес требует `tls` |
| `mutating_identities` | []string | `[]` | mTLS-идентичности (URI SAN или `dns:<name>`), которым разрешены изменения; если список задан, остальные mTLS-клиенты только читают. Требует `tls` |
| `flight_recorder` | bool | `false` | Отдаёт дамп runtime trace по `/debug/flightrecorder` на сервере управления вызывающим с принципалом |
| `unsafe_loopback_mutations` | bool | `false` | Разрешает анонимным plaintext-клиентам на loopback TCP изменяющие RPC; health `gobfd.management` при этом `NOT_SERVING` |
| `socket.path` | string | `"/run/gobfd/gobfd.sock"` | Абсолютный путь Unix-сокета управления; пустое значение отключает его |
| `socket.mode` | string | `"0660"` | Восьмеричные права файла сокета |
| `socket.group` | string | `""` | Группа-владелец файла сокета; пустое значение оставляет группу демона |
| `tls.cert_file` | string | `""` | Сертификат сервера (PEM) TCP-listener; вместе с `key_file` и `client_ca_file` включает mTLS |
| `tls.key_file` | string | `""` | Закрытый ключ сервера (PEM) |
| `tls.client_ca_file` | string | `""` | CA (PEM), от которого должны происходить сертификаты клиентов |

Запросы через Unix-сокет несут UID peer от ядра (`SO_PEERCRED`) как
принципал; логи RPC записывают его как `principal=uid:<n>`. С `grpc.tls`
TCP-listener требует сертификат клиента от `client_ca_file` и предлагает
HTTP/2 и HTTP/1.1 через ALPN; принципал — первый URI SAN сертификата
(например, SPIFFE ID), иначе `dns:<первый DNS SAN>`. Три файла читаются
заново при каждом handshake, поэтому ротация не требует перезапуска;
отсутствующий файл останавливает запуск. `grpc.tls` требует `grpc.addr` и
отключает предупреждение при запуске для non-loopback адреса. Доступ задают
права и группа сокета, поэтому нужна отдельная группа администраторов, а не
группа демона, которая читает `/etc/gobfd`. `grpc.socket` меняется только
перезапуском ([решение о транспорте](../superpowers/specs/2026-10-15-management-transport-decision.md)).

Анонимные вызывающие — plaintext TCP-клиенты без mTLS, такой listener может
быть только на loopback. Они могут читать (`List*`, `Get*`, `Watch*`), но
получают `PermissionDenied` для `AddSession`, `DeleteSession`,
`AddEchoSession`, `DeleteEchoSession`, `AddMicroBFDGroup` и
`DeleteMicroBFDGroup`, если не задан `unsafe_loopback_mutations`. Клиенты
через сокет и mTLS несут принципал и могут изменять состояние; если задан
`mutating_identities`, это могут только перечисленные mTLS-идентичности.
Сессия, созданная через API, принадлежит принципалу вызвавшего: другой
принципал получает `PermissionDenied` от `DeleteSession` (отказ логируется),
а два принципала, добавившие одну сессию, разделяют её, пока оба её не
освободят. Анонимные клиенты в режиме `unsafe_loopback_mutations` разделяют
владельца `local`.

**Переход с конфигурации только с TCP:** plaintext `grpc.addr` на non-loopback
адресе (например, `":50052"`) теперь останавливает запуск; привяжите его к
loopback или добавьте `grpc.tls`. Переведите изменяющих клиентов на сокет
(`gobfdctl --addr unix:///run/gobfd/gobfd.sock`, новое значение по
умолчанию) и добавьте операторов в группу `gobfd-admin`, либо задайте
`unsafe_loopback_mutations: true` как переходный, явно небезопасный режим.
Заданный `grpc.addr` сохраняет TCP-listener.

#### `metrics` -- Endpoint Prometheus

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `addr` | string | `"127.0.0.1:9100"` | HTTP-адрес для метрик; без аутентификации, поэтому по умолчанию loopback |
| `path` | string | `"/metrics"` | URL-путь для скрапинга Prometheus |

#### `log` -- Логирование

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `level` | string | `"info"` | Уровень лога: `debug`, `info`, `warn`, `error` |
| `format` | string | `"json"` | Формат: `json` (production), `text` (разработка) |

Уровни `debug`, `info`, `warn` и `error` принимаются без учёта регистра.
Формат должен точно совпадать со строчными `json` или `text`. Пропущенные ключи
сохраняют значения по умолчанию `info` и `json`; явные пустые строки, пробелы и
неизвестные значения отклоняются с `ErrInvalidLogLevel` или
`ErrInvalidLogFormat`. Ошибка содержит имя поля без его значения. Проверка
действует для YAML и environment overrides при запуске и SIGHUP до изменения
log level, generation, health, sessions или ресурсов отправителей.

Корректный уровень лога меняется через SIGHUP без перезапуска демона.
Изменение формата требует перезапуска.

#### `bfd` -- Параметры BFD по умолчанию

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `default_desired_min_tx` | duration | `"1s"` | Desired Min TX Interval по умолчанию |
| `default_required_min_rx` | duration | `"1s"` | Required Min RX Interval по умолчанию; ноль запрашивает остановку периодических Control packets |
| `default_detect_multiplier` | uint32 | `3` | Множитель обнаружения (1–255) |
| `align_intervals` | bool | `false` | RFC 7419: выравнивание таймеров до ближайшего общего интервала |
| `default_padded_pdu_size` | uint16 | `0` | RFC 9764: дополнение BFD-пакетов до этого размера с битом DF (0 = отключено; targets v1: 24-9000 без auth, 26-9000 с auth; actual PDU не усекается) |

Все поля detection multiplier проверяются до численного декодирования, включая
Echo, VXLAN, Geneve и Micro-BFD peers и defaults. Значения вне 0–255, дроби и
boolean отклоняются; переполнение и усечение исключены. Ноль сохраняет прежнее
значение наследования там, где оно поддерживается. Базовый BFD default и
включённый unsolicited default должны быть положительными.

Коллекции peers и groups должны быть списками mappings. Одиночный mapping и
элементы списка другого типа отклоняются до weak decoding.
Поиск multiplier использует тот же case-insensitive matching, что decoder;
конфликтующие case aliases одного поля отклоняются.

При `align_intervals: true` значения `DesiredMinTxInterval` и `RequiredMinRxInterval` округляются вверх до ближайшего общего интервала RFC 7419 (3.3мс, 10мс, 20мс, 50мс, 100мс, 1с). Это предотвращает несовпадения при согласовании с аппаратными реализациями BFD.

> **Примечание**: По RFC 5880 Section 6.8.3, когда сессия не в состоянии Up, TX-интервал принудительно устанавливается не менее 1 секунды.

#### `unsolicited` -- RFC 9468 Unsolicited BFD

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `unsolicited.enabled` | bool | `false` | Включить unsolicited BFD (ОБЯЗАН быть выключен по умолчанию по RFC 9468) |
| `unsolicited.max_sessions` | int | `0` | Макс. динамически созданных сессий; при `enabled` должно быть >= 1 |
| `unsolicited.cleanup_timeout` | duration | `"0s"` | Время ожидания перед удалением Down пассивных сессий |
| `unsolicited.session_defaults.desired_min_tx` | duration | `"1s"` | TX-интервал для автосозданных сессий |
| `unsolicited.session_defaults.required_min_rx` | duration | `"1s"` | RX-интервал для автосозданных сессий |
| `unsolicited.session_defaults.detect_mult` | uint32 | `3` | Множитель обнаружения для автосозданных сессий |
| `unsolicited.interfaces.<name>.enabled` | bool | `false` | Включить unsolicited на этом интерфейсе |
| `unsolicited.interfaces.<name>.allowed_prefixes` | []string | `[]` | Дополнительное ограничение исходных адресов (CIDR); проверка connected subnet действует всегда |
| `unsolicited.auth.type` | string | `""` | Пусто — без аутентификации, либо `meticulous_keyed_sha1` (RFC 9468, раздел 6.2) |
| `unsolicited.auth.key_id` | uint32 | `0` | Auth Key ID RFC 5880 (0-255) |
| `unsolicited.auth.secret` | string | `""` | Ключ SHA1 длиной 1-20 байт |

Пример:
```yaml
unsolicited:
  enabled: true
  max_sessions: 10
  cleanup_timeout: "30s"
  session_defaults:
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
  interfaces:
    eth0:
      enabled: true
      allowed_prefixes: ["10.0.0.0/8"]
```

Каждый включённый интерфейс должен иметь явное непустое имя. На нём открываются
отдельные wildcard listeners IPv4 и IPv6 на UDP 3784 с `SO_BINDTODEVICE`;
настроенная сессия на адресе приёма не требуется. Для выключенных интерфейсов
unsolicited listener не открывается. Настроенные per-address listeners сохраняются.

> **Приём (RFC 9468, разделы 2 и 6.1)**: пакет создаёт сессию, только если он
> пришёл single-hop с TTL 255, его source принадлежит подсети локального
> адреса, на который он пришёл, входит в
> `allowed_prefixes`, когда список задан, и не совпадает с самим локальным
> адресом. Новые сессии допускаются не чаще 10 в секунду (burst 20) и не сверх
> `max_sessions`. Ненумерованный путь приёма отклоняется.
>
> Каждая пассивная сессия отвечает с локального адреса и интерфейса, на которые
> пришёл пакет, со своим source port (IPv4 и IPv6).
>
> **Аутентификация (RFC 9468, раздел 6.2)**: при заданном `unsolicited.auth`
> пакет, создающий сессию, должен нести верную подпись Meticulous Keyed SHA1
> настроенного ключа, и сессия подписывает ответы этим ключом. Без
> `unsolicited.auth` пакет с битом A отклоняется (RFC 5880, раздел 6.8.6).
> Развёртывание, не способное использовать Meticulous Keyed SHA1, работает без
> аутентификации; RFC 9468 её только рекомендует.
>
> **Проверка**: при `enabled: true` загрузка завершается ошибкой, если
> интервалы `session_defaults` вне диапазона 1 мкс..2^32-1 мкс,
> `required_min_rx` равен нулю (пассивная сессия, запрещающая пиру отправку,
> не обнаруживает отказ и удерживает слот), `detect_mult` вне 1..255,
> `cleanup_timeout` < 0, ни один интерфейс не включён или элемент
> `allowed_prefixes` не является корректным CIDR.
>
> **Небезопасный preview**: квалификация IPv6 по захвату пакетов и внедрение
> отказов при закрытии lease открыты (`gobfd-qj0.8.2.1.10.1`). Не включайте
> unsolicited BFD на недоверенном интерфейсе.

#### `echo` -- RFC 9747 Unaffiliated BFD Echo

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `echo.enabled` | bool | `false` | Включить echo-сессии BFD |
| `echo.default_tx_interval` | duration | -- | Интервал передачи echo по умолчанию |
| `echo.default_detect_multiplier` | uint32 | -- | Множитель обнаружения echo по умолчанию |
| `echo.peers[].peer` | string | -- | IP удалённой системы (цель echo) |
| `echo.peers[].local` | string | -- | IP локальной системы |
| `echo.peers[].interface` | string | -- | Сетевой интерфейс для `SO_BINDTODEVICE` (опционально) |
| `echo.peers[].tx_interval` | duration | -- | Переопределение `default_tx_interval` для этого пира |
| `echo.peers[].detect_mult` | uint32 | -- | Переопределение `default_detect_multiplier` для этого пира |

Echo-сессии обнаруживают отказы forwarding-path без необходимости запуска BFD на удалённой стороне. Локальная система отправляет BFD Control пакеты на UDP порт 3785 удалённой стороны, которая пересылает их обратно через обычную IP-маршрутизацию.

Echo-пиры реконсилируются при SIGHUP. Ключ сессии: `(peer, local, interface)`.

Создание через API также требует `echo.enabled: true`, реконсилированного Echo
source и готового runtime/receive binding. Иначе `AddEchoSession` возвращает
`FailedPrecondition` до открытия sender и создания сессии. В API-only deployment
можно не задавать `echo.peers`: API получает receive listener до открытия sender.
Валидный SIGHUP обновляет opt-in; отклонённый кандидат его сохраняет. Потеря
готовности отклоняет последующие admission; уже допущенный запрос может завершиться.
List/Delete доступны с прежней management policy. RFC 9747 остаётся preview.

Пример:
```yaml
echo:
  enabled: true
  default_tx_interval: "100ms"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.1"
      local: "10.0.0.2"
      interface: "eth0"
      tx_interval: "50ms"
      detect_mult: 5
```

#### `micro_bfd` -- RFC 7130 Micro-BFD для LAG

Micro-BFD — preview, выключенный по умолчанию. До настройки групп, выбора
`dry-run` или `enforce` либо вызова `AddMicroBFDGroup` задайте
`micro_bfd.enabled: true` или `GOBFD_MICRO_BFD_ENABLED=true`.
Без opt-in конфигурация отклоняется до открытия ресурсов, API возвращает
`FailedPrecondition` без создания группы. List и Delete сохраняются с прежними
проверками авторизации management и владения группой.
Изменение `micro_bfd.enabled` требует перезапуска: SIGHUP отклоняет candidate,
сохраняя работающую generation. При включённом preview группы меняются через reload.
При запуске журнал отмечает включённую возможность как preview. Opt-in не
подтверждает dedicated multicast MAC и передачу через enslaved member.

Это постоянная настройка безопасности deployment, введённая 2026-10-22;
владелец — maintainer, задача `gobfd-qj0.8.2.1.8.1`. Дата удаления не назначена;
graduation preview отслеживается отдельно в `gobfd-qj0.8.2.2.1`.

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `micro_bfd.groups[].lag_interface` | string | -- | Имя логического LAG-интерфейса (напр., "bond0") |
| `micro_bfd.enabled` | bool | `false` | Явное включение неполного RFC 7130 preview; нужен перезапуск |
| `micro_bfd.groups[].member_links` | []string | -- | Имена физических member-линков |
| `micro_bfd.groups[].peer_addr` | string | -- | IP удалённой системы для всех member-сессий |
| `micro_bfd.groups[].local_addr` | string | -- | IP локальной системы |
| `micro_bfd.groups[].desired_min_tx` | duration | `bfd.default_desired_min_tx` | Интервал BFD-таймера для member-сессий |
| `micro_bfd.groups[].required_min_rx` | duration | `bfd.default_required_min_rx` | Минимальный допустимый RX-интервал |
| `micro_bfd.groups[].detect_mult` | uint32 | `bfd.default_detect_multiplier` | Множитель времени обнаружения |
| `micro_bfd.groups[].min_active_links` | int | -- | Минимум Up member-ов для LAG Up (>= 1) |
| `micro_bfd.actuator.mode` | string | `disabled` | `disabled`, `dry-run` или `enforce` |
| `micro_bfd.actuator.backend` | string | `auto` | `auto`, `kernel-bond`, `ovs` или `networkmanager` |
| `micro_bfd.actuator.ovsdb_endpoint` | string | `""` | OVSDB endpoint для `backend: ovs`; пустое значение использует `unix:/var/run/openvswitch/db.sock` |
| `micro_bfd.actuator.owner_policy` | string | `refuse-if-managed` | Политика owner: отказ при внешнем владельце, разрешение внешнего владельца или NetworkManager D-Bus |
| `micro_bfd.actuator.down_action` | string | `remove-member` | Действие после перехода member из Up в non-Up |
| `micro_bfd.actuator.up_action` | string | `add-member` | Действие после возврата member в Up |

Micro-BFD запускает независимые BFD-сессии на каждом member link LAG (UDP порт 6784) с `SO_BINDTODEVICE` на каждый member. Агрегатное состояние LAG = Up, когда `upCount >= min_active_links`.

RFC 7130 enforcement защищён блоком `micro_bfd.actuator`. Режим по умолчанию
`disabled` означает только detect/report. `dry-run` подключает daemon к policy
layer и логирует планируемые действия с member. `enforce` сейчас требует
`owner_policy: allow-external` с `backend: kernel-bond` или `backend: ovs`.
Kernel-bond backend пишет RFC 7130 remove/add команды через Linux bonding
sysfs. OVS backend использует native OVSDB transactions против
`Port.interfaces` на существующем OVS bond port. Задайте
`micro_bfd.actuator.ovsdb_endpoint`, если локальный OVSDB socket отличается от
дефолта `unix:/var/run/openvswitch/db.sock`. `backend: networkmanager`
реализован через NetworkManager D-Bus и требует `owner_policy:
networkmanager-dbus`. Он деактивирует активный bond port profile, когда member
переходит в Down, и активирует remembered или available bond port profile при
восстановлении member. В остальных случаях оставляйте политику отказа при
внешнем владельце для disabled и dry-run режимов.

Группы реконсилируются при SIGHUP. Ключ группы: `lag_interface`.

Группа и её member-сессии применяются как одно целое. Ошибка создания
member-сессии откатывает группы и member-сессии, созданные этим reload;
зафиксированные группы и сессии остаются на месте. Физический member link
принадлежит одному LAG: кандидат, в котором один member link указан в группах
двух LAG-интерфейсов, отклоняется целиком в любом address family; RFC 7130
Section 2 запускает micro-BFD-сессии на каждом member link LAG. Группа
идентифицируется `lag_interface` и содержит один `peer_addr`, поэтому один LAG
использует один address family; вторая micro-BFD-сессия на member link для
другого address family, допустимая по RFC 7130 Section 2.1, не поддерживается. Незаданные `desired_min_tx`, `required_min_rx` и `detect_mult`
наследуют `bfd.default_*`. Группа и все её
member-сессии используют эти значения; к ним применяется проверка RFC 5880:
адреса, wire-границы интервалов и detect multiplier (RFC 7130 Section 2.2).
Изменение списка member, адресов, таймеров или `min_active_links` работающей
группы — конфликт конфигурации, группа остаётся без изменений: удалите группу
в одном reload и добавьте в следующем.

`AddMicroBFDGroup` применяет ту же проверку, отклоняет member link другой
группы с `FAILED_PRECONDITION` и создаёт только агрегатную группу;
`ListMicroBFDGroups` возвращает сохранённые таймеры member-сессий.

Группы следуют модели владения сессиями. Reload владеет созданными им группами
и удаляет только их; группа API переживает reload. Одинаковая группа от API и
от reload разделяется, каждый источник освобождает только свой claim;
расходящаяся группа с тем же `lag_interface` — конфликт, группа reload не может
забрать member link сохраняющейся группы API. `DeleteMicroBFDGroup` освобождает
только claim вызывающего и возвращает `FAILED_PRECONDITION` для группы, которой
он не владеет, например группы reload. Новая группа начинает каждый member с
состояния его существующей member-сессии: member с сессией Up считается Up и
сообщается actuator в порядке dispatch.

Пример:
```yaml
micro_bfd:
  enabled: true
  actuator:
    mode: "dry-run"
    backend: "kernel-bond"
    ovsdb_endpoint: ""
    owner_policy: "refuse-if-managed"
    down_action: "remove-member"
    up_action: "add-member"
  groups:
    - lag_interface: "bond0"
      member_links: ["eth0", "eth1"]
      peer_addr: "10.0.0.2"
      local_addr: "10.0.0.1"
      desired_min_tx: "100ms"
      required_min_rx: "100ms"
      detect_mult: 3
      min_active_links: 1
```

#### `vxlan` -- RFC 8971 BFD для VXLAN

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `vxlan.enabled` | bool | `false` | Включить VXLAN BFD-сессии |
| `vxlan.backend` | string | `userspace-udp` | Overlay backend. Реализован только `userspace-udp`; `kernel`, `ovs`, `ovn`, `cilium`, `calico` и `nsx` зарезервированы и не проходят validation |
| `vxlan.management_vni` | uint32 | -- | Management VNI для BFD control (24 бит, макс. 16777215) |
| `vxlan.default_desired_min_tx` | duration | -- | TX-интервал для VXLAN-сессий по умолчанию |
| `vxlan.default_required_min_rx` | duration | -- | RX-интервал для VXLAN-сессий по умолчанию |
| `vxlan.default_detect_multiplier` | uint32 | -- | Множитель обнаружения по умолчанию |
| `vxlan.peers[].peer` | string | -- | IP удалённого VTEP |
| `vxlan.peers[].local` | string | -- | IP локального VTEP |
| `vxlan.peers[].peer_mac` | string | -- | Обязательный unicast source MAC удалённого VTEP |
| `vxlan.peers[].local_mac` | string | -- | Обязательный unicast source MAC локального VTEP |
| `vxlan.peers[].desired_min_tx` | duration | -- | Переопределение TX-интервала для этого пира |
| `vxlan.peers[].required_min_rx` | duration | -- | Переопределение RX-интервала для этого пира |
| `vxlan.peers[].detect_mult` | uint32 | -- | Переопределение множителя обнаружения для этого пира |

BFD Control пакеты инкапсулируются в VXLAN (внешний UDP порт 4789) с
выделенным Management VNI. Внутренний стек использует настроенный source MAC
исходящего VTEP и назначенный dst MAC `00:52:02:00:00:00`, IPv4 (TTL=255) и UDP (dst 3784) заголовки. Текущий
backend `userspace-udp` владеет `local:4789`; если kernel VXLAN, OVS/OVN,
Cilium, Calico, NSX или другой dataplane уже владеет тем же socket, нужен будущий
owner-specific backend.

> **Небезопасный preview**: поддерживаемый IPv4 Format A профиль валидируется
> и точно привязывается к tunnel identity. Userspace socket ownership по-прежнему
> подходит только для изолированной лаборатории; backend не production-qualified.

Пиры реконсилируются при SIGHUP. Identity включает backend, outer endpoints,
Management VNI, inner endpoints, address family и MAC tuple.

Пример:
```yaml
vxlan:
  enabled: true
  backend: "userspace-udp"
  management_vni: 16777215
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.2"
      local: "10.0.0.1"
      peer_mac: "02:00:00:00:00:02"
      local_mac: "02:00:00:00:00:01"
    - peer: "10.0.0.3"
      local: "10.0.0.1"
      peer_mac: "02:00:00:00:00:03"
      local_mac: "02:00:00:00:00:01"
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 5
```

#### `geneve` -- RFC 9521 BFD для Geneve

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `geneve.enabled` | bool | `false` | Включить Geneve BFD-сессии |
| `geneve.backend` | string | `userspace-udp` | Overlay backend. Реализован только `userspace-udp`; `kernel`, `ovs`, `ovn`, `cilium`, `calico` и `nsx` зарезервированы и не проходят validation |
| `geneve.default_vni` | uint32 | -- | VNI Geneve по умолчанию (24 бит, макс. 16777215) |
| `geneve.default_desired_min_tx` | duration | -- | TX-интервал для Geneve-сессий по умолчанию |
| `geneve.default_required_min_rx` | duration | -- | RX-интервал для Geneve-сессий по умолчанию |
| `geneve.default_detect_multiplier` | uint32 | -- | Множитель обнаружения по умолчанию |
| `geneve.peers[].peer` | string | -- | IP удалённого NVE |
| `geneve.peers[].local` | string | -- | IP локального NVE |
| `geneve.peers[].vni` | uint32 | -- | Переопределение `default_vni` для этого пира (0 = по умолчанию) |
| `geneve.peers[].inner_peer` | string | -- | Обязательный IPv4-адрес remote VAP Format A |
| `geneve.peers[].inner_local` | string | -- | Обязательный IPv4-адрес local VAP Format A |
| `geneve.peers[].peer_mac` | string | -- | Обязательный unicast MAC remote VAP Format A |
| `geneve.peers[].local_mac` | string | -- | Обязательный unicast MAC local VAP Format A |
| `geneve.peers[].desired_min_tx` | duration | -- | Переопределение TX-интервала для этого пира |
| `geneve.peers[].required_min_rx` | duration | -- | Переопределение RX-интервала для этого пира |
| `geneve.peers[].detect_mult` | uint32 | -- | Переопределение множителя обнаружения для этого пира |

BFD Control пакеты инкапсулируются в Geneve (внешний UDP порт 6081) с Format A
(Ethernet payload, Protocol Type 0x6558). По RFC 9521 Section 4: O бит
(control) = 1, C бит (critical) = 0. Текущий backend `userspace-udp` владеет
`local:6081`; если kernel Geneve, OVS/OVN, NSX или другой dataplane уже
владеет тем же socket, нужен будущий owner-specific backend.

> **Небезопасный preview**: настроенные IPv4 Format A VAP identities
> валидируются и привязываются точно. Неполная VAP identity, другие address
> families и другие Geneve payload formats fail closed. Userspace socket ownership остаётся lab-only.

Пиры реконсилируются при SIGHUP. Identity включает backend, outer endpoints,
VNI, оба VAP IP/MAC endpoint и address family.

Пример:
```yaml
geneve:
  enabled: true
  backend: "userspace-udp"
  default_vni: 100
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.2"
      local: "10.0.0.1"
      inner_peer: "192.0.2.2"
      inner_local: "192.0.2.1"
      peer_mac: "02:00:00:00:00:02"
      local_mac: "02:00:00:00:00:01"
    - peer: "10.0.0.3"
      local: "10.0.0.1"
      vni: 200
      inner_peer: "192.0.2.4"
      inner_local: "192.0.2.3"
      peer_mac: "02:00:00:00:00:04"
      local_mac: "02:00:00:00:00:03"
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 5
```

#### `socket` -- Настройка буферов сокетов

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `socket.read_buffer_size` | int | `4194304` (4 МиБ) | Размер `SO_RCVBUF` в байтах для сокетов слушателей. Увеличенные буферы снижают потерю пакетов при большом количестве сессий. `0` = значение ОС по умолчанию. |
| `socket.write_buffer_size` | int | `4194304` (4 МиБ) | Размер `SO_SNDBUF` в байтах для сокетов отправителей. Увеличенные буферы снижают ошибки `sendto` при всплесках нагрузки. `0` = значение ОС по умолчанию. |

Оба значения должны быть неотрицательными целыми числами, представимыми как `int`. `read_buffer_size` применяется к
общим single-hop и multihop control listeners, к привязанным к интерфейсу
wildcard single-hop listeners IPv4 и IPv6 для каждого enabled unsolicited
interface, к Echo listeners и Micro-BFD member listeners. `write_buffer_size`
применяется к отправителям API и декларативных control-сессий, Echo,
Micro-BFD и динамически принятых unsolicited-сессий. Двунаправленные
userspace VXLAN/Geneve-сокеты получают обе настройки. Сокеты управления
HTTP/Unix и dataplane backends без UDP не входят в эту политику.

Значения запрашиваются через `SO_RCVBUF` / `SO_SNDBUF` при создании сокета.
Ноль сохраняет соответствующее значение ОС по умолчанию. Ошибка socket option
прерывает создание сокета и закрывает новый сокет. Политика буферов сохраняется
при запуске, в том числе для отправителей, создаваемых позже. SIGHUP отклоняет
изменение любого значения; требуется перезапуск процесса.

**Рекомендации по размерам:**

- **< 100 сессий**: значений ОС по умолчанию обычно достаточно (установите оба в `0`)
- **100-1000 сессий**: 4 МиБ (по умолчанию) обеспечивает запас для всплесков трафика
- **> 1000 сессий**: рассмотрите 8-16 МиБ; мониторьте `netstat -su` на предмет ошибок приёма UDP

> **Примечание**: Ядро может ограничить эффективный размер буфера значением `net.core.rmem_max` / `net.core.wmem_max`. Для увеличения буферов поднимите эти sysctl-параметры:
> ```bash
> sysctl -w net.core.rmem_max=16777216
> sysctl -w net.core.wmem_max=16777216
> ```

### Переменные окружения

Daemon поддерживает только явные environment overrides из таблицы ниже.
Остальные или неоднозначные имена `GOBFD_` игнорируются; underscores не
интерпретируются универсальным правилом nesting. Environment values перекрывают
YAML, а YAML перекрывает встроенные defaults.

| Переменная | Ключ конфигурации | Пример |
|---|---|---|
| `GOBFD_GRPC_ADDR` | `grpc.addr` | `"127.0.0.1:50052"` (loopback; по умолчанию пусто) |
| `GOBFD_METRICS_ADDR` | `metrics.addr` | `"127.0.0.1:9100"` |
| `GOBFD_METRICS_PATH` | `metrics.path` | `"/metrics"` |
| `GOBFD_LOG_LEVEL` | `log.level` | `"debug"` |
| `GOBFD_LOG_FORMAT` | `log.format` | `"text"` |
| `GOBFD_UNSOLICITED_ENABLED` | `unsolicited.enabled` | `true` |
| `GOBFD_ECHO_ENABLED` | `echo.enabled` | `true` |
| `GOBFD_SOCKET_READ_BUFFER_SIZE` | `socket.read_buffer_size` | `8388608` |
| `GOBFD_SOCKET_WRITE_BUFFER_SIZE` | `socket.write_buffer_size` | `8388608` |
| `GOBFD_VXLAN_ENABLED` | `vxlan.enabled` | `true` |
| `GOBFD_VXLAN_BACKEND` | `vxlan.backend` | `"userspace-udp"` |
| `GOBFD_GENEVE_ENABLED` | `geneve.enabled` | `true` |
| `GOBFD_GENEVE_BACKEND` | `geneve.backend` | `"userspace-udp"` |
| `GOBFD_GOBGP_ENABLED` | `gobgp.enabled` | `true` |
| `GOBFD_GOBGP_ADDR` | `gobgp.addr` | `"127.0.0.1:50051"` |
| `GOBFD_GOBGP_STRATEGY` | `gobgp.strategy` | `"disable-peer"` |
| `GOBFD_GOBGP_TLS_ENABLED` | `gobgp.tls.enabled` | `true` |
| `GOBFD_GOBGP_DAMPENING_ENABLED` | `gobgp.dampening.enabled` | `true` |

### Декларативные сессии

Сессии из секции `sessions:` создаются при старте демона и реконсилируются при
SIGHUP. Adapters base, Micro-BFD members, VXLAN и Geneve передают пустые desired
sets и используют отдельные типизированные reconciliation owners. Поэтому
удаление последней base-записи освобождает только claim базовой конфигурации.
Семантика reconciliation:

- **Новые желаемые записи** -- получают claim конфигурации и создают wire
  session, только если точной сессии ещё нет
- **Удалённые сессии** -- освобождают claim конфигурации; wire session
  уничтожается, только когда других claims не осталось
- **Существующие точные сессии** -- разделяются между claims; конфликт
  эффективных параметров отклоняется без мутации ownership claims и реестров
  сессий manager

До применения любого source при startup или SIGHUP демон компилирует и
проверяет полный объединённый candidate base, Micro-BFD members, VXLAN и
Geneve. Каждый adapter также проверяет полный набор своего source до открытия
sender или изменения session ownership в Manager. Ошибка conversion, session
validation или duplicate отклоняет объединённый candidate.
Конфликт с запущенной точной сессией отклоняется до изменения ownership.
SIGHUP reload в целом остаётся нетранзакционным: изменения log
level, lifecycle sender, transport resources, замена listener/backend и
cross-adapter rollback не входят в эту гарантию.

Ключ уникальности YAML -- кортеж: `(peer, local, interface)`.

| Ключ | Тип | Обязательный | Описание |
|---|---|---|---|
| `peer` | string | Да | IP-адрес удалённой системы |
| `local` | string | Да | IP-адрес локальной системы |
| `interface` | string | Нет | Сетевой интерфейс для `SO_BINDTODEVICE` RX и TX single-hop сессии; для `multi_hop` запрещён (`ErrInvalidSessionMultiHopInterface`) |
| `type` | string | Нет | `single_hop` (по умолчанию) или `multi_hop` |
| `desired_min_tx` | duration | Нет | Переопределение TX-интервала |
| `required_min_rx` | duration | Нет | Переопределение RX-интервала; явное `"0s"` сохраняется |
| `detect_mult` | uint32 | Нет | Переопределение множителя обнаружения |
| `padded_pdu_size` | uint16 | Нет | RFC 9764: дополнение BFD-пакетов до этого размера; ненулевое значение переопределяет `bfd.default_padded_pdu_size`; v1 отклоняет authenticated targets 24/25 и сохраняет более длинный actual PDU |
| `multihop_max_hops` | uint32 | Нет | Только `multi_hop`: политика hops 1-254, по умолчанию 1; принимается TTL/Hop Limit >= 255 - N (1/2/5 -> 254/253/250). Явный `0` и поле на `single_hop` отклоняются (`ErrInvalidSessionMultihopMaxHops`). Только YAML: API-сессии используют значение по умолчанию |
| `auth.type` | string | Нет | Тип аутентификации RFC 5880: `simple_password`, `keyed_md5`, `meticulous_keyed_md5`, `keyed_sha1`, `meticulous_keyed_sha1` |
| `auth.key_id` | uint32 | Если auth включён | Auth Key ID, допустимый диапазон 0-255 |
| `auth.secret` | string | Если auth включён | Секрет: 1-16 байт для Simple Password/MD5, 1-20 байт для SHA1 |

Public API `AddSessionRequest.padded_pdu_size` и CLI `--padded-pdu-size`
передают target в байтах как `uint32`. Пропуск или `0` отключает padding без
наследования `bfd.default_padded_pdu_size`; `0` в YAML-сессии наследует этот
global default. Оба пути используют общие границы target и сохраняют более
длинные authenticated PDU.

Для базовых single-hop и multihop сессий пропущенный или null `required_min_rx`
наследует `bfd.default_required_min_rx`; явное `"0s"` просит пира прекратить
периодические Control packets (RFC 5880 sections 4.1 и 6.8.18). Ноль сохраняется
при включённом `align_intervals`. Generic gRPC `AddSession` также сохраняет
явный нулевой Duration, а пропущенный Duration получает default `1s`.
Локальный передатчик продолжает следовать receive interval пира; локальный ноль
сам по себе не выключает локальную передачу и не включает Demand Mode.
Обновление таймеров через config сохраняет ту же семантику явного нуля.
Правила zero-as-inherit для пиров preview Micro-BFD/VXLAN/Geneve не изменены.
Значение `sessions` должно быть YAML-списком объектов. Для receive intervals
boolean и дробные числовые значения отклоняются; используйте строки вроде `"100ms"`.

При допуске Control-сессии TX должен быть от `1us` до `4294967295us`, а RX —
нулём или в том же диапазоне. Положительные интервалы меньше `1us` отклоняются
с `ErrInvalidWireInterval`: усечение до wire zero даёт зарезервированный TX или
запрашивает отсутствие периодического RX. Интервалы от `1us` локально сохраняют
дробные микросекунды; wire-поля усекаются до целых микросекунд.

Аутентификация настраивается через декларативные YAML-сессии или через поля
gRPC `AddSession`: `auth_type`, `auth_key_id` и `auth_secret`. `auth_key_id`
должен помещаться в однобайтный диапазон RFC 5880. `auth_secret` должен быть
1-16 байт для Simple Password/MD5-режимов и 1-20 байт для SHA1-режимов.
Запросы с key material без включённого `auth_type`, либо с включённой
аутентификацией без секрета, отклоняются.

Типы сессий определяют UDP-порт и обработку TTL:

| Тип | Порт | Исходящий TTL | Проверка входящего TTL |
|---|---|---|---|
| `single_hop` | 3784 | 255 (GTSM) | ОБЯЗАН быть 255 (RFC 5881) |
| `multi_hop` | 4784 | 255 | >= 255 - `multihop_max_hops` (по умолчанию >= 254), проверка в демультиплексированной сессии |

RFC 5883 не задаёт правила для входящего TTL. Проверка multi-hop -- политика
GoBFD по ненормативному RFC 5082 Appendix A (чтение A: N -- число промежуточных
маршрутизаторов, как `minimum-ttl` во FRR). Listener 4784 передаёт все пакеты
сессии; пакет multi-hop никогда не попадает в single-hop сессию и наоборот
(RFC 5881 Section 5). При `multihop_max_hops` больше 1 спуфинг ограничивает
только аутентификация: RFC 5883 Section 6 рекомендует (SHOULD) криптографическую
аутентификацию, предпочтительно `meticulous_keyed_sha1`. Квалификация на реальных
путях из 2 и 5 hops не выполнялась.

Single-hop сессия принимает пакеты только своего интерфейса (RFC 5881 Section 3).
Интерфейс -- это заданный `interface`; без него -- zone link-local адреса `local`
или `peer` (`fe80::2%eth0`); без того и другого -- интерфейс, владеющий `local`.
GoBFD определяет индекс этого интерфейса один раз при создании сессии и затем
сравнивает его с индексом интерфейса из PKTINFO каждого пакета. Zone также
выбирает устройство listener и `SO_BINDTODEVICE` отправителя, поэтому одна пара
link-local адресов на двух линках образует две сессии. Zone, указывающая другой
интерфейс, чем `interface`, или различающаяся у `peer` и `local`, отклоняется
(`ErrInvalidSessionZone`; API `InvalidArgument`). Пересозданный интерфейс
получает новый индекс и требует пересоздания сессии.

YAML-сессия, адрес `local` которой не назначен ни одному или назначен нескольким
интерфейсам, переходит в состояние pending, а не failed: соседние сессии
создаются, receipt реконсиляции учитывает её как pending, повтор выполняется при
следующем событии link интерфейса, resync перечня интерфейсов или SIGHUP. Адрес,
добавленный без события link, учитывается только при этих событиях. Пока сессия
в состоянии pending, реконсиляция считается устаревшей и readiness gRPC health
остаётся `NOT_SERVING`. Адрес, назначенный нескольким интерфейсам, остаётся
неоднозначным при каждом повторе, поэтому сессия остаётся pending, а readiness --
`NOT_SERVING`, пока в конфигурации не задан `interface` (или не удалён
дублирующий адрес) и конфигурация не применена через SIGHUP. API для
того же случая возвращает `FailedPrecondition`. Адрес `local` на `lo` или dummy
интерфейсе определяется как этот интерфейс; пакеты, пришедшие на физический
интерфейс, отклоняются как `demux`/`scope_mismatch`. Для такой схемы задавайте
`interface`.

### Интеграция с GoBGP

При включении изменения состояния BFD передаются в GoBGP через gRPC API (RFC 5882 Section 10.2):

- **BFD Down** --> Отключение BGP-пира (`DisablePeer()`)
- **BFD Up** --> Включение BGP-пира (`EnablePeer()`)

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `gobgp.enabled` | bool | `false` | Включить/выключить интеграцию с GoBGP |
| `gobgp.addr` | string | `"127.0.0.1:50051"` | Адрес gRPC API GoBGP |
| `gobgp.strategy` | string | `"disable-peer"` | `disable-peer` -- единственная реализованная стратегия; `withdraw-routes` распознаётся, но отклоняется как неподдерживаемая |
| `gobgp.action_timeout` | duration | `"5s"` | Максимальное время на один вызов GoBGP API |
| `gobgp.tls.enabled` | bool | `false` | Использовать TLS для GoBGP gRPC соединения |
| `gobgp.tls.ca_file` | string | `""` | Опциональный PEM bundle корневых CA; пустое значение использует системные roots |
| `gobgp.tls.server_name` | string | `""` | Опциональное имя сервера для проверки TLS-сертификата |

Plaintext GoBGP gRPC сохранён для совместимости с типичными localhost
развёртываниями. Для удалённых или non-loopback GoBGP API endpoints включайте
`gobgp.tls.enabled`. Если GoBGP включён без TLS и `gobgp.addr` не указывает на
loopback/localhost, GoBFD пишет предупреждение при старте.

#### Владение пирами

GoBFD включает только пиры, которые отключил сам (решение maintainer,
`gobfd-qj0.8.2.4.1`). Перед `DisablePeer` он читает admin state соседа в
GoBGP; пир, уже административно отключённый (например, оператором), не
трогается ни при BFD Down, ни при последующем BFD Up. GoBGP не сообщает, кто
отключил пир, поэтому эта запись хранится в памяти: после перезапуска GoBFD
пиры, которые он отключил, остаются отключёнными, пока их не включит
оператор (`gobgp neighbor <addr> enable`).

Если обработчик отстаёт от потока событий BFD, он получает маркер разрыва
и восстанавливается по версионированному снимку: сессия, которую он
последней видел в Up и которая теперь в Down, отключает пир; Up включает
пир, отключённый GoBFD; ранее не виденная сессия в Down не вызывает
действий, как при запуске.

#### Идентичность сессии

Соседом GoBGP управляет только single-hop или multi-hop сессия
(`gobfd-qj0.8.2.4.2`). Сессии Micro-BFD member link, VXLAN, Geneve и Echo к
адресу соседа никогда не отключают и не включают BGP-пир. IPv4-mapped адрес
пира (`::ffff:192.0.2.1`) обозначает того же соседа, что и обычный IPv4.

Каждому соседу нужна одна каноническая BFD-сессия. При `gobgp.enabled`
валидация конфигурации (запуск и reload) отклоняет две записи `sessions` к
одному адресу пира (`gobgp peer has more than one BFD session`). Вторая
базовая сессия, созданная во время работы (API или unsolicited), оставляет
соседа без канонической сессии: при BFD Down GoBFD пишет ошибку в лог, не
отключает пир и переводит health-сервис `gobfd.gobgp` в `NOT_SERVING` до
следующего успешного действия GoBGP. Пир, который GoBFD уже отключил, при
BFD Up по-прежнему включается.

#### Совместимость с GoBGP

| Линия GoBFD | Демон GoBGP | gRPC-сервис | Подтверждение |
|---|---|---|---|
| `dev` (линия v1) | v4.10.0 | `api.GoBgpService` | Gates BGP+BFD, RFC, fast-failover и ExaBGP anycast пройдены 2026-10-13 |
| `release/v0.6` | v3.37.0 | `apipb.GobgpApi` | Публичный контракт v0.6 |

GoBFD в `dev` поддерживает только демоны GoBGP v4; gRPC-сервисы GoBGP v3 и v4
несовместимы по wire. Заявление о поддержке покрывает проверенную v4.10.0;
более ранние релизы v4 не проверялись. GoBGP v4.4.0 — первый релиз с
исправлением NEXT_HOP из GHSA-4p9m-8gc4-rw2h, поэтому более старые демоны v4
использовать нельзя.

Обновление развёртывания с GoBGP v3 вместе с GoBFD из `dev`:

1. Обновите `gobgpd` до v4.10.0 в том же изменении, что и GoBFD.
2. Удалите `neighbors.transport.config.ttl` из конфигурации GoBGP; в GoBGP
   v4.0.0 этот параметр удалён.
3. Оставьте нативный BFD GoBGP выключенным (по умолчанию) для каждого пира с
   сессией GoBFD.
4. Запустите GoBFD. Preflight вызывает `GetBgp` и `ListPeer`: endpoint v3 или
   нативный BFD на пире GoBFD прерывают старт с понятной ошибкой, а недоступный
   endpoint повторно проверяется в фоне.

#### Демпфирование flap-ов

Предотвращает чрезмерную осцилляцию BGP-маршрутов при быстрых колебаниях BFD.
Этот penalty-based алгоритм является implementation policy GoBFD: RFC 5882
Section 3.1 разрешает session-state hysteresis, но не стандартизирует данный
алгоритм.

Каждый BFD Down добавляет штраф 1, который убывает с периодом `half_life`.
Демпфирование никогда не задерживает Down: каждый значимый Down сразу
отключает пир (`gobfd-qj0.8.2.4.1`, H-27). Когда штраф достигает
`suppress_threshold`, демпфирование удерживает только восстановление: BFD Up
включает пир после снижения штрафа ниже `reuse_threshold` или по истечении
`max_suppress_time`. Down во время удерживаемого восстановления отменяет
его, и пир остаётся отключённым.

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `gobgp.dampening.enabled` | bool | `false` | Включить демпфирование |
| `gobgp.dampening.suppress_threshold` | float64 | `3` | Штраф, начиная с которого удерживается восстановление |
| `gobgp.dampening.reuse_threshold` | float64 | `2` | Штраф, ниже которого подавление снимается; должен быть > 0 |
| `gobgp.dampening.max_suppress_time` | duration | `"60s"` | Максимальная длительность подавления; должна быть > 0 |
| `gobgp.dampening.half_life` | duration | `"15s"` | Период полураспада штрафа |

### Горячая перезагрузка (SIGHUP)

```bash
# Перезагрузка конфигурации
sudo systemctl reload gobfd
# или
kill -HUP $(pidof gobfd)
```

При перезагрузке:
1. YAML-файл перечитывается и валидируется
2. Под одним coordinator lock candidate сверяется с неизменяемым контрактом
   startup-runtime. Несовпадение отклоняется с ограниченным списком путей полей
   до изменения generation, health, log level, sessions или ресурсов
3. Полный candidate sessions base, Echo, Micro-BFD
   groups и members, VXLAN и Geneve компилируется и валидируется до создания
   sender или мутации Manager
4. Публикуется новое desired generation, а gRPC health для пустого service и
   named services `bfd.v1` становится `NOT_SERVING`; невалидный candidate не меняет generations, health
   и log level
5. После валидации уровень лога обновляется динамически; при последующей ошибке
   source эта настройка не откатывается
6. Source базовых сессий реконсилируется, включая пустой desired set
7. Echo-пиры реконсилируются как изолированный по source desired set, включая
   disabled или пустое состояние; sender leases лениво создаются только для
   новых принятых sessions
8. Группы Micro-BFD и source member sessions реконсилируются, включая пустые
   desired sets
9. VXLAN и Geneve реконсилируются как отдельные sources, включая пустые
   desired sets
10. Один aggregate receipt хранит ограниченные счётчики и коды ошибок всех пяти
   sources. Applied продвигается до desired, только если все пять sources
   converged. Named service возвращается в `SERVING`, когда converged его
   sources, пустой service — когда converged все пять; учитываются также
   runtime и listener компоненты (`07-monitoring.md`)

На Linux существующий, но выключенный настроенный интерфейс приводит к `pending`
после preflight всего source. Link events автоматически повторяют применение
затронутых pending sources в сохранённом generation без второго SIGHUP.
Повторные события объединяются и не применяют converged sources повторно.
Потеря netlink events запрашивает полную сверку интерфейсов. Address events
(`RTM_NEWADDR`/`RTM_DELADDR`, включая завершение IPv6 DAD) и сверка повторяют
применение всех pending sources; они не несут состояния link и не переводят
сессии в Down. Приём этих групп rtnetlink не требует capability. Non-Linux stub
не обеспечивает автоматический retry, поэтому там pending sources повторяет
следующий SIGHUP.

Приёмные listeners входят в generation. Перед применением source base, Echo
или Micro-BFD захватывает UDP listeners, нужные его desired set (3784/4784,
3785, 6784), и открывает недостающие. Apply в Manager выполняется только после
открытия всех захваченных listeners, поэтому ни один source не сходится только
на senders. Listener, интерфейс которого отсутствует, оставляет source в
`pending`, а автоматический interface retry открывает его; любая другая ошибка
открытия оставляет source в `failed`. Ещё не назначенный или IPv6-tentative
local address (`EADDRNOTAVAIL`) при bind listener, sender или overlay backend
считается типизированным недоступным адресом и оставляет source в `pending`.
Listener, который не может выполнить bind, останавливает свой source до apply в
Manager, поэтому старые claims сохраняются; overlay endpoint, который не может
выполнить bind, исключает только свои identities; sender, который не
может выполнить bind, оставляет в `pending` только свой claim, а проход всё равно
освобождает записи, удалённые из desired set. Address event, назначающий адрес,
повторяет применение source. Control- или Echo-сессия, созданная через API, захватывает
свой listener вместе с sender и открывает его при отсутствии; Echo-сессия
использует общий Echo listener своего адреса. Отсутствующий интерфейс или
неназначенный адрес завершает `AddSession` или `AddEchoSession` ошибкой
`Unavailable`, другая ошибка открытия — `FailedPrecondition`, и сессия не
создаётся. Удаление
сессии снимает её claim. После применения source без failed и resource-pending
записей source снимает claims вне desired set; listener закрывается, когда
снимается его последний claim source, unsolicited или API. Неизменённые listeners,
включая startup-owned wildcard listeners unsolicited, не переоткрываются и не
заменяются. Listener с привязкой к устройству, интерфейс которого удалён и
создан заново с тем же именем, перепривязывается на месте при следующем
interface event или проверке раз в 2 с: daemon повторно применяет
`SO_BINDTODEVICE` к тому же сокету. Linux требует `CAP_NET_RAW` для изменения
существующей привязки к устройству; поставляемые systemd unit, Compose-файлы и
Kubernetes manifest выдают его на всё время жизни процесса. Неудачная
перепривязка оставляет binding в `NOT_SERVING` и повторяется. При startup любая
ошибка listener, включая отсутствующий интерфейс, по-прежнему останавливает
запуск. Non-Linux сборки не создают listeners, поэтому такой source `failed`.

Overlay backends тоже входят в generation. Один userspace-udp сокет обслуживает
каждый вид туннеля и outer local address (VXLAN UDP 4789, Geneve UDP 6081).
Перед применением source VXLAN или Geneve захватывает identities своего desired
set: новый local endpoint открывает backend, а identity, добавленная на открытый
endpoint, входит в набор identities этого сокета на месте, поэтому работающие
сессии сохраняют свой сокет. Identity с тем же wire identity, что и захваченная,
например VXLAN peer с изменённым `local_mac`, заменяет её. Каждый local endpoint
захватывается независимо: identities endpoint, который не открывается,
исключаются из применения (`pending` для неназначенного адреса, иначе `failed`),
а остальные identities применяются, включая удаления. После применения без failed и resource-pending
записей каждый backend сужается до desired identities, а backend без identities
закрывается и возвращает inner source port. Неудачное применение сохраняет
расширенный набор. Startup захватывает backends так же, поэтому backend, который
не открывается, переводит свой source в failed или pending, а не останавливает
daemon. Порты непривилегированные, сокеты не привязаны к устройству, поэтому
capability не требуется.

`log.level` применяется сразу после полной валидации candidate. Пять desired
sets реконсилируют membership sessions/groups. Записи control, Echo и Micro-BFD
можно добавлять, удалять или re-key, включая записи, которым нужен новый local
address, interface или session type. Записи VXLAN и Geneve можно добавлять,
удалять или re-key, включая новый local endpoint или новый per-peer VNI Geneve.
BFD defaults и per-entry timers,
padding, authentication, peer и threshold values применяются при создании новой
identity. Базовые сессии, принадлежащие только config, также принимают in-place
изменения `desired_min_tx` и `required_min_rx`. Активная Poll-транзакция сохраняет
объявленные значения; новые reload заменяют только последнее ожидающее изменение.
После активации предложения увеличение TX и уменьшение RX откладывают защищённый
локальный эффект до Final; обратные изменения действуют сразу. До подтверждения приём изменения означает
`pending`, не `updated`. Завершение автоматически обновляет текущее поколение и
readiness без второго SIGHUP. Таймаут или выход из Up завершают receipt ошибкой;
поздний Final не превращает её в успех. Отмена после приёма не откатывает
объявленные значения.
[Контракт транзакций](../superpowers/specs/2026-09-08-gobfd-timer-update-transactions-design.md)
определяет срок согласования и разделение startup/recovery Poll. Live FRR/BIRD
reload qualification ещё впереди; это не полное соответствие Poll/Demand.

Другие изменения effective parameters существующей same-key session или
Micro-BFD group не являются in-place update: reconciliation возвращает
conflict, generation остаётся stale, а оператор должен удалить и затем добавить
identity отдельными reload либо перезапустить процесс. Удаление последней записи
или отключение source поддерживается.

Перезапуска процесса требуют: `grpc.*`, `metrics.*`, `log.format`, `socket.*`,
`unsolicited.*`, `micro_bfd.enabled`, `micro_bfd.actuator.*`, `gobgp.*`, `vxlan.backend`,
`vxlan.management_vni`, `geneve.backend` и `geneve.default_vni`. Оба значения
VNI по умолчанию сразу re-key все identities своего source; runtime-поддержка
этого не реализована, и это ограничение объёма работ, а не граница ядра или
привилегий. Отклонённый reload выводит только эти ограниченные
пути полей, но не значения конфигурации или authentication secrets. Размеры
буферов входят в startup-only границу и применяются к поддерживаемым
UDP-сокетам, описанным в разделе настройки буферов.

Base configuration, Echo, Micro-BFD members, VXLAN и Geneve используют
раздельный ownership sources. Удаление последней записи одного source не
удаляет sessions другого source. Ошибка получения sender для Echo откатывает
новые принятые Echo sessions этого прохода; SIGHUP остаётся нетранзакционным
между sources и для других runtime apply failures.
Автоматическое завершение охватывает принятые транзакции базовых таймеров, не
произвольные ошибки других source operations. Для остальных partial apply
следующий явный SIGHUP компилирует и публикует новое desired generation. Настроенный непустой
identity VXLAN или Geneve, backend которой не открывается, считается failed или
pending для неназначенного local address, а не skipped.
Уже принятые изменения других sources не откатываются.

### Правила валидации

| Правило | Ошибка |
|---|---|
| `log.level`: `debug`, `info`, `warn` или `error` без учёта регистра | `ErrInvalidLogLevel` |
| `log.format`: точно `json` или `text` | `ErrInvalidLogFormat` |
| `socket.read_buffer_size` / `socket.write_buffer_size` должны быть >= 0 | `ErrInvalidSocketReadBufferSize` / `ErrInvalidSocketWriteBufferSize` |
| `grpc.addr` не должен быть пустым | `ErrEmptyGRPCAddr` |
| `bfd.default_detect_multiplier` должен быть >= 1 | `ErrInvalidDetectMultiplier` |
| `bfd.default_desired_min_tx` должен быть > 0 | `ErrInvalidDesiredMinTx` |
| `bfd.default_required_min_rx` должен быть >= 0 | `ErrInvalidRequiredMinRx` |
| `gobgp.addr` не должен быть пустым при включённой интеграции | `ErrEmptyGoBGPAddr` |
| Включённая `gobgp.strategy` не распознана | `ErrInvalidGoBGPStrategy` |
| Включённая `gobgp.strategy` распознана, но не реализована (`withdraw-routes`) | `ErrUnsupportedGoBGPStrategy` |
| `gobgp.action_timeout` должен быть > 0 при включённой интеграции | `ErrInvalidGoBGPActionTimeout` |
| `gobgp.tls.ca_file` / `server_name` требуют `gobgp.tls.enabled: true` | `ErrInvalidGoBGPTLS` |
| `gobgp.dampening.suppress_threshold` должен быть > `reuse_threshold` > 0 | `ErrInvalidDampeningThreshold` |
| `gobgp.dampening.max_suppress_time` должен быть > 0 при включённом демпфировании | `ErrInvalidDampeningMaxSuppressTime` |
| `gobgp.dampening.half_life` должен быть > 0 при включённом демпфировании | `ErrInvalidDampeningHalfLife` |
| `peer` сессии должен быть валидным IP-адресом | `ErrInvalidSessionPeer` |
| `type` сессии должен быть `single_hop` или `multi_hop` | `ErrInvalidSessionType` |
| `detect_mult` сессии должен быть >= 1 | `ErrInvalidSessionDetectMult` |
| Нет дублирующих ключей сессий (peer, local, interface) | `ErrDuplicateSessionKey` |
| `interface` недопустим для `multi_hop` | `ErrInvalidSessionMultiHopInterface` |
| `multihop_max_hops` только для `multi_hop`, диапазон 1-254 | `ErrInvalidSessionMultihopMaxHops` |
| Zone link-local адреса должна совпадать с `interface` и с zone другого адреса | `ErrInvalidSessionZone` |
| Control sessions с одинаковыми parsed local address и типом listener должны использовать один interface | `ErrAmbiguousListenerInterface` |
| Echo `peer` должен быть валидным IP-адресом | `ErrInvalidEchoPeer` |
| Echo `detect_mult` должен быть >= 1 | `ErrInvalidEchoDetectMult` |
| Нет дублирующих ключей echo-сессий | `ErrDuplicateEchoSessionKey` |
| Enabled Echo peers с одинаковым parsed local address должны использовать один interface | `ErrAmbiguousListenerInterface` |
| VXLAN `management_vni` должен быть <= 16777215 (24 бит) | `ErrInvalidVXLANVNI` |
| VXLAN-пиры требуют конкретный IPv4 local endpoint и remote/local unicast source MAC | `ErrInvalidVXLANPeer` / `ErrVXLANEndpointIdentityUnavailable` |
| VXLAN `backend` должен быть `userspace-udp`; зарезервированные non-userspace names fail closed | `ErrInvalidOverlayBackend`, `ErrUnsupportedOverlayBackend` |
| VXLAN `peer` должен быть валидным IP-адресом | `ErrInvalidVXLANPeer` |
| Нет дублирующих ключей VXLAN-сессий | `ErrDuplicateVXLANSessionKey` |
| Geneve `default_vni` и per-peer `vni` должны быть <= 16777215 | `ErrInvalidGeneveVNI` |
| Geneve `backend` должен быть `userspace-udp`; зарезервированные non-userspace names fail closed | `ErrInvalidOverlayBackend`, `ErrUnsupportedOverlayBackend` |
| Geneve `peer` должен быть валидным IP-адресом | `ErrInvalidGenevePeer` |
| Geneve Format A VAP IP/MAC tuple должен быть полным, IPv4 и unicast | `ErrGeneveVAPIdentityUnavailable` |
| Нет дублирующих ключей Geneve-сессий | `ErrDuplicateGeneveSessionKey` |

Идентификатор control listener — `(parsed local address, multi-hop flag)`;
отсутствующий `type` и `single_hop` выбирают один listener. Идентификатор Echo
listener — parsed local address. Эквивалентные записи IPv6 считаются равными;
IPv4-mapped адреса сохраняют mapped identity. Все записи одного listener
должны задавать одинаковый `interface`, включая пустое значение. Конфликт
отклоняется независимо от порядка конфигурации до открытия listeners.
Выключенная конфигурация Echo не создаёт конфликт listener.

Это идентификаторы валидации. Во время работы владелец listeners определяет
сокет ключом `(local address с развёрнутой IPv4-mapped формой, interface, port,
multi-hop flag)`, где port — 3784, 4784, 3785 или 6784. Записи, идентификаторы
валидации которых отличаются только IPv4-mapped написанием и которые задают
одинаковый `interface`, используют один сокет. Echo-сессия, созданная через API,
использует Echo listener своего local address независимо от interface, как и
настроенные Echo peers.

`ListenerBindingConflictError` оборачивает `ErrAmbiguousListenerInterface`.
Метод `FieldID()` указывает `sessions[].interface` или `echo.peers[].interface`;
ошибка не содержит значений адреса или интерфейса.

### Значения по умолчанию

| Ключ | Значение | Обоснование |
|---|---|---|
| `grpc.addr` | пусто | Без TCP-listener; 50051 остаётся за gobgpd |
| `grpc.socket.path` | `/run/gobfd/gobfd.sock` | Сокет управления с принципалами по UID peer |
| `metrics.addr` | `127.0.0.1:9100` | Стандартный порт экспортёра на loopback; endpoint без аутентификации |
| `metrics.path` | `/metrics` | Конвенция Prometheus |
| `log.level` | `info` | Production по умолчанию |
| `log.format` | `json` | Машиночитаемый |
| `bfd.default_desired_min_tx` | `1s` | RFC 5880 Section 6.8.3 медленная скорость |
| `bfd.default_required_min_rx` | `1s` | Консервативная начальная точка |
| `bfd.default_detect_multiplier` | `3` | 3x время обнаружения |
| `gobgp.enabled` | `false` | Opt-in интеграция |
| `gobgp.strategy` | `disable-peer` | Наиболее безопасное действие BGP |
| `gobgp.action_timeout` | `5s` | Ограничивает внешние вызовы GoBGP API |
| `gobgp.tls.enabled` | `false` | Совместимый default для localhost GoBGP |
| `socket.read_buffer_size` | `4194304` (4 МиБ) | Запас для 100+ сессий |
| `socket.write_buffer_size` | `4194304` (4 МиБ) | Запас для 100+ сессий |

### Связанные документы

- [04-cli.md](./04-cli.md) -- Команды CLI для управления сессиями
- [06-deployment.md](./06-deployment.md) -- Развёртывание в production с systemd
- [configs/gobfd.example.yml](../../configs/gobfd.example.yml) -- Аннотированный пример конфигурации

---

*Последнее обновление: 2026-02-24*
