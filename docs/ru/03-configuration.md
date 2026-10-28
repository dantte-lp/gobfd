# Конфигурация

Демон принимает `-config PATH` и `-version`. С `-config` применяются встроенные значения, YAML и поддерживаемые переменные среды, по порядку. Без него переменные среды игнорируются. Источники: [схема, defaults, validation, env](../../internal/config/config.go), [загрузчик](../../cmd/gobfd/handlers.go), [комментированный пример](../../configs/gobfd.example.yml).

## Минимальная декларативная конфигурация

```yaml title="gobfd.yml" showLineNumbers
grpc:
  socket:
    path: /run/gobfd/gobfd.sock
    mode: "0660"
    group: gobfd-admin
metrics:
  addr: 127.0.0.1:9100
  path: /metrics
log:
  level: info
  format: json
sessions:
  - peer: 192.0.2.2
    local: 192.0.2.1
    interface: eth0
    type: single_hop
    desired_min_tx: 300ms
    required_min_rx: 300ms
    detect_mult: 3
```

Заменить адреса/интерфейс значениями управляемой среды. Задавать длительности с единицами. Интервалы пакета выражены в микросекундах ([RFC 5880 раздел 4.1](https://www.rfc-editor.org/rfc/rfc5880.html#section-4.1)). YAML использует подчёркивания в именах типов, CLI — дефисы.

## Управление, метрики и журнал

| Ключ YAML | Встроенное значение | Условие |
| :--- | :--- | :--- |
| `grpc.addr` | пусто | TCP отключён; открытый TCP только на loopback; удалённый TCP требует mTLS |
| `grpc.socket.path` | `/run/gobfd/gobfd.sock` | Абсолютный путь; пустое значение отключает сокет |
| `grpc.socket.mode` | `0660` | Восьмеричные права |
| `grpc.socket.group` | пусто | Группа демона; пакетный пример задаёт `gobfd-admin` |
| `grpc.tls.cert_file` | пусто | Сертификат сервера PEM |
| `grpc.tls.key_file` | пусто | Закрытый ключ PEM |
| `grpc.tls.client_ca_file` | пусто | CA PEM для обязательной проверки клиента |
| `grpc.mutating_identities` | `[]` | URI SAN или `dns:NAME` с правом записи; требует TLS |
| `grpc.unsafe_loopback_mutations` | `false` | Анонимная запись через прямой открытый loopback; health управления `NOT_SERVING` |
| `grpc.flight_recorder` | `false` | Защищённый management-only `/debug/flightrecorder` |
| `metrics.addr` | `127.0.0.1:9100` | HTTP без аутентификации |
| `metrics.path` | `/metrics` | Путь Prometheus |
| `log.level` | `info` | `debug`, `info`, `warn`, `error` |
| `log.format` | `json` | `json` или `text`; требует restart |

TLS требует все три файла и TCP-адрес. Файлы перечитываются при handshake: замена содержимого не требует restart, изменение путей требует. Unix principal получает UID от ядра; mTLS использует первый URI SAN, иначе первый DNS SAN. Сохранить хотя бы один транспорт управления. См. [безопасность](15-security.md).

## Control-сессии

| Глобальный ключ | Значение | Условие |
| :--- | :--- | :--- |
| `bfd.default_desired_min_tx` | `1s` | Желаемый TX |
| `bfd.default_required_min_rx` | `1s` | Требуемый RX; явный ноль сохраняется |
| `bfd.default_detect_multiplier` | `3` | 1–255 |
| `bfd.align_intervals` | `false` | Округление вверх до поддерживаемых интервалов RFC 7419 |
| `bfd.default_padded_pdu_size` | `0` | Без дополнения; ненулевой размер 24–9000 байт |
| `sessions` | `[]` | Декларативные claims Control |

| Ключ `sessions[]` | Значение | Условие |
| :--- | :--- | :--- |
| `peer` | обязательно | IP соседа |
| `local`, `interface` | пусто | Локальный IP и single-hop привязка к устройству |
| `type` | `single_hop` | `single_hop` или `multi_hop` |
| `desired_min_tx` | наследуется | Положительный TX |
| `required_min_rx` | наследуется | Неотрицательный RX; явный `0s` остаётся нулём |
| `detect_mult` | наследуется | 1–255 после подстановки |
| `padded_pdu_size` | `0` | Ноль наследует глобальное дополнение; размер включает auth |
| `multihop_max_hops` | `1` | Только multi-hop, 1–254; минимум TTL `255 - value` |
| `auth.type` | пусто | Пусто/`none`, `simple_password`, `keyed_md5`, `meticulous_keyed_md5`, `keyed_sha1`, `meticulous_keyed_sha1` |
| `auth.key_id` | `0` | 0–255 |
| `auth.secret` | пусто | 1–16 байт password/MD5; 1–20 SHA1 |

Single-hop требует TTL/Hop Limit 255. Multi-hop TTL — политика GoBFD, а не требование RFC 5883. Дополнение запрашивает DF и нижнюю границу payload: длина передачи — `max(actual PDU, target)`. Authenticated PDU может превышать target и не усекается. Источник: [формирование пакета](../../internal/bfd/session_packet.go). Ссылки: [RFC 5881](https://www.rfc-editor.org/rfc/rfc5881.html), [RFC 5883 раздел 6](https://www.rfc-editor.org/rfc/rfc5883.html#section-6), [RFC 7419](https://www.rfc-editor.org/rfc/rfc7419.html), [RFC 9764](https://www.rfc-editor.org/rfc/rfc9764.html).

## Буферы UDP и QoS передачи

Все поля `socket` задаются при запуске. QoS относится к обычным BFD sender; внешние overlay-сокеты его не наследуют. Источники: [QoS](../../internal/config/socket_qos.go), [буферы](../../internal/config/socket_buffer.go).

| Ключ `socket` | Значение | Условие |
| :--- | ---: | :--- |
| `read_buffer_size`, `write_buffer_size` | `4194304` каждый | Байты буфера; ноль сохраняет значение ОС |
| `ipv4_dscp` | `48` | DSCP 0–63 без ECN |
| `ipv6_traffic_class` | `192` | Полный октет 0–255 |
| `priority` | `0` | Очередь Linux независимо от DSCP |
| `mark` | `0` | Ноль пропускает routing-mark operation |
| `priority_required`, `mark_required` | `true` | Отказ sender при ошибке запрошенного параметра |

CS6 — эксплуатационная политика, не требование RFC 5881. Проверить действующие буферы и маркировку захватом; запрос настройки не доказывает обработку QoS сетью.

`mark_required: false` разрешает передачу при ошибке запрошенного mark, возможно без нужной routing policy. Сохранить `true`, когда mark выбирает маршрут. Необязательная priority также допускает отправку без запрошенного приоритета; успех маркировки этим не подтверждается.

## Предварительные профили

Все профили отключены по умолчанию. Maturity, enablement и readiness независимы; `ready` не доказывает здоровье соседа, соответствие RFC или применение actuator.

<!-- BEGIN GOBFD PREVIEW CAPABILITIES -->
| Profile | RFC | Maturity |
| :--- | ---: | :--- |
| unsolicited | 9468 | preview; unsafe |
| echo | 9747 | preview; incomplete RFC qualification |
| micro_bfd | 7130 | preview; incomplete L2 owner integration |
| vxlan | 8971 | preview; unsafe/incomplete owner integration |
| geneve | 9521 | preview; unsafe/incomplete IPv4 Format A |
<!-- END GOBFD PREVIEW CAPABILITIES -->

### Unsolicited Control

| Ключ `unsolicited` | Значение | Условие |
| :--- | :--- | :--- |
| `enabled` | `false` | Включение требует restart |
| `max_sessions` | `0` | Не менее 1 при включении |
| `cleanup_timeout` | `0s` | Задержка удаления Down-сессии; ноль удаляет сразу |
| `interfaces` | `{}` | Словарь принимающих интерфейсов |
| `interfaces.NAME.enabled` | `false` | Допуск на интерфейсе |
| `interfaces.NAME.allowed_prefixes` | `[]` | Ограничение источника вместе с подсетью локального адреса |
| `session_defaults.desired_min_tx`, `session_defaults.required_min_rx` | `1s` | Таймеры passive-сессии |
| `session_defaults.detect_mult` | `3` | Множитель passive-сессии |
| `auth` | отключено | Структура Control auth; допустим только `meticulous_keyed_sha1` |

Допуск требует TTL 255 и подходящую локальную подсеть. Общий limiter допускает 10 сессий/с, burst 20. Не включать на анонимном общем сегменте. Источники: [допуск](../../internal/bfd/unsolicited.go), [RFC 9468 раздел 6](https://www.rfc-editor.org/rfc/rfc9468.html#section-6).

### Echo

`echo` содержит `enabled` (false), `default_tx_interval` (ноль), `default_detect_multiplier` (ноль), `peers` (пусто). Поля соседа: `peer`, `local`, `interface`, `tx_interval`, `detect_mult`. Ненулевые значения заменяют defaults профиля; задать положительный TX и множитель 1–255.

Echo использует UDP 3785 без настройки auth. Control auth не защищает Echo. Совместимые API/config claims разделяют физическую сессию; освобождение одного сохраняет остальных владельцев. Источники: [сверка](../../cmd/gobfd/reload.go), [ADR-0009](adr/0009-echo-principal-claims.md), [RFC 9747](https://www.rfc-editor.org/rfc/rfc9747.html).

### Micro-BFD

| Ключ `micro_bfd` | Значение | Варианты |
| :--- | :--- | :--- |
| `enabled` | `false` | Включение требует restart |
| `groups` | `[]` | Группы LAG |
| `actuator.mode` | `disabled` | `disabled`, `dry-run`, `enforce` |
| `actuator.backend` | `auto` | `auto`, `kernel-bond`, `ovs`, `networkmanager` |
| `actuator.ovsdb_endpoint` | пусто | Native OVSDB endpoint |
| `actuator.owner_policy` | `refuse-if-managed` | Также `allow-external`, `networkmanager-dbus` |
| `actuator.down_action` | `remove-member` | `none`, `remove-member`, `add-member` |
| `actuator.up_action` | `add-member` | `none`, `remove-member`, `add-member` |

Группа содержит `lag_interface`, `member_links`, `peer_addr`, `local_addr`, `desired_min_tx`, `required_min_rx`, `detect_mult`, `min_active_links`. Таймеры наследуют BFD defaults; минимум активных — 1…число участников. Enforce требует явный поддерживаемый owner/backend: kernel-bond или OVS с `allow-external`, NetworkManager с `networkmanager-dbus`. Журнал dry-run не доказывает применение. Источники: [схема и проверка](../../internal/config/config.go), [RFC 7130](https://www.rfc-editor.org/rfc/rfc7130.html).

### VXLAN и Geneve

| Профиль | Глобальные поля | Поля соседа |
| :--- | :--- | :--- |
| `vxlan` | `enabled`, `backend`, `management_vni`, `default_desired_min_tx`, `default_required_min_rx`, `default_detect_multiplier`, `peers` | `peer`, `local`, `peer_mac`, `local_mac`, `desired_min_tx`, `required_min_rx`, `detect_mult` |
| `geneve` | `enabled`, `backend`, `default_vni`, `default_desired_min_tx`, `default_required_min_rx`, `default_detect_multiplier`, `peers` | `peer`, `local`, `vni`, `inner_peer`, `inner_local`, `peer_mac`, `local_mac`, `desired_min_tx`, `required_min_rx`, `detect_mult` |

`userspace-udp` — default и единственный реализованный backend. Резервные `kernel`, `ovs`, `ovn`, `cilium`, `calico`, `nsx` отклоняются. Таймеры/VNI профиля по умолчанию нулевые; задать допустимые итоговые значения. Ноль таймера наследует профиль; ноль Geneve VNI наследует `default_vni`. VNI занимает 24 бита. Format A требует явные unicast MAC; Geneve также требует явные unicast inner IPv4. UDP 4789/6081 должны быть свободны на выбранном namespace/address. Источники: [проверка](../../internal/config/config.go), [Format A](../../internal/netio/overlay_inner.go), [RFC 8971](https://www.rfc-editor.org/rfc/rfc8971.html), [RFC 9521](https://www.rfc-editor.org/rfc/rfc9521.html).

## Интеграция GoBGP

| Ключ `gobgp` | Значение | Условие |
| :--- | :--- | :--- |
| `enabled` | `false` | Управление соседями |
| `addr` | `127.0.0.1:50051` | API endpoint |
| `strategy` | `disable-peer` | Единственная реализованная стратегия; `withdraw-routes` отклоняется |
| `action_timeout` | `5s` | Срок одного действия |
| `tls.enabled` | `false` | TLS с проверкой сервера |
| `tls.ca_file`, `tls.server_name` | пусто | CA bundle (системные корни при пустом), замена имени |
| `dampening.enabled` | `false` | Удержание восстановления; Down применяется сразу |
| `dampening.suppress_threshold`, `dampening.reuse_threshold` | `3`, `2` | Пороги подавления/возврата |
| `dampening.max_suppress_time`, `dampening.half_life` | `60s`, `15s` | Максимальное удержание и полураспад penalty |

Реализация `dev` требует GoBGP v4 API и отклоняет конфликт native BFD owner. Несколько base-сессий к одному BGP-адресу запрещают однозначное управление. Административный переход не означает отказ пути. Источники: [preflight/выбор](../../cmd/gobfd/handlers.go), [применение](../../internal/gobgp/handler.go), [RFC 5882 раздел 10.2](https://www.rfc-editor.org/rfc/rfc5882.html#section-10.2).

## Переменные среды

Добавить `GOBFD_` к суффиксу. Поддерживаются только точные соответствия, общего преобразования подчёркиваний в точки нет.

| Суффиксы | Назначение |
| :--- | :--- |
| `GRPC_ADDR` | `grpc.addr` |
| `METRICS_ADDR`, `METRICS_PATH` | `metrics.addr`, `metrics.path` |
| `LOG_LEVEL`, `LOG_FORMAT` | `log.level`, `log.format` |
| `UNSOLICITED_ENABLED`, `ECHO_ENABLED`, `MICRO_BFD_ENABLED` | `.enabled` профиля |
| `VXLAN_ENABLED`, `VXLAN_BACKEND`, `GENEVE_ENABLED`, `GENEVE_BACKEND` | `.enabled`/`.backend` профиля |
| `GOBGP_ENABLED`, `GOBGP_ADDR`, `GOBGP_STRATEGY` | Соответствующие поля `gobgp` |
| `GOBGP_TLS_ENABLED`, `GOBGP_DAMPENING_ENABLED` | `gobgp.tls.enabled`, `gobgp.dampening.enabled` |
| `SOCKET_READ_BUFFER_SIZE`, `SOCKET_WRITE_BUFFER_SIZE` | Буферы `socket` |
| `SOCKET_IPV4_DSCP`, `SOCKET_IPV6_TRAFFIC_CLASS` | Маркировка `socket` |
| `SOCKET_PRIORITY`, `SOCKET_MARK` | `socket.priority`, `socket.mark` |
| `SOCKET_PRIORITY_REQUIRED`, `SOCKET_MARK_REQUIRED` | Политика ошибок параметров |

## Перечитывание SIGHUP

### Проверка и идентичность listener

| Ввод | Условие |
| :--- | :--- |
| `log.level`, `log.format` | Level не зависит от регистра; format — lowercase `json`/`text`; явные пустые/пробельные значения отклоняются |
| Socket scalars | Целые неотрицательные буферы; priority 0–2147483647; mark 0–4294967295; строгая boolean policy |
| Multiplier/padding | Дроби, booleans, overflow и конфликтующие case-folded имена отклоняются до decoding |
| Peer/group collections | Списки mappings без преобразования singleton-map и повторных identities |
| Multi-hop | Без `interface`; `multihop_max_hops` только для `multi_hop` |
| IPv6 zones | Peer/local zones совпадают с effective single-hop interface |
| Unsolicited | Есть enabled непустой interface; prefixes валидны; таймеры 1 мкс…uint32 мкс; cleanup неотрицательный |
| Dampening | Suppress threshold > reuse threshold > 0; half-life/max duration положительны |

YAML identity Control listener — `(parsed local address, multi-hop flag)`; Echo — parsed local address. Записи одного listener должны иметь одинаковый interface, включая пустой. Runtime receive key добавляет interface, destination port и нормализованный адрес. Порты — 3784/4784/3785/6784. Совместимый API claim разделяет точный key; другой device не использует чужой socket. Источники: [проверка](../../internal/config/listener_binding.go), [runtime owner](../../cmd/gobfd/listeners.go).

VXLAN/Geneve требуют конкретные outer IPv4 endpoints; Format A MAC — ненулевые unicast. Минимальный authenticated padding — 26 байт; фактический authenticated PDU не усекается. Это ограничения validation/implementation, не native overlay acceptance.

### Применение и сходимость

SIGHUP загружает тот же путь и среду процесса. Полная проверка предшествует применению. Недопустимый кандидат и изменения startup-полей сохраняют принятую генерацию. Ошибка runtime-применения может оставить pending-ресурсы; проверить health сверки и журнал.

| Перечитывается | Требует restart |
| :--- | :--- |
| `log.level` | Все `grpc`, `metrics`, `socket`, `gobgp`, `unsolicited`; `log.format` |
| Control desired set, таймеры, множитель, padding | Auth сохранённой Control identity |
| Echo enablement, defaults, peers | `micro_bfd.enabled` и actuator policy |
| Micro-BFD groups | VXLAN backend/management VNI; Geneve backend/default VNI |
| VXLAN/Geneve enablement, таймеры, peers | Все startup-поля `startup_contract.go` |

Ключ сверки YAML — `(peer, local, interface)`. Каноническая wire identity также включает тип сессии, семейство адресов, network scope и точный transport scope; их изменение не является обычным обновлением таймера сохранённой identity. Совместимое изменение таймера сохраняет сессию/discriminator. Удаление освобождает config claim и сохраняет чужие claims. Ротация auth требует согласованного restart или release/recreation с перерывом. Источники: [каноническая identity](../../internal/bfd/session_identity.go), [startup contract](../../cmd/gobfd/startup_contract.go), [coordinator](../../cmd/gobfd/reconciliation.go), [ADR-0006](adr/0006-session-preserving-reload-policy.md).

Обновление packet policy на месте требует единственного совместимого config owner. Padding допускает только положительный→положительный размер с подготовленным sender, неизменной auth и Async; zero↔nonzero, неподготовленный sender и Demand-dependent changes отклоняются. MinTTL — локальная multi-hop policy. Poll-dependent timers остаются pending до завершения транзакции; admission не равно completion. См. [owner update](../../internal/bfd/session_update.go), [протокол](02-protocol.md).

Base, Echo, Micro-BFD, VXLAN и Geneve сверяются без общей атомарности. Допустимые независимые изменения могут сохраниться при отказе другого источника. Отсутствие local address/interface сохраняет desired generation/claims, снимает readiness и вызывает retry при восстановлении; проверка каждые две секунды дополняет interface events. Permanent/inventory failures требуют явного SIGHUP. Принятая генерация с pending timers/resources не сошлась. Source-local overlay acquisition сохраняет live/draining backend leases при отказе. Проверить bounded per-source receipts и aggregate/named health; бесшовный rollback и peer continuity не гарантируются.

1. Сохранить принятую конфигурацию и inventory сессий/discriminator.
2. Установить защищённый кандидат по тому же пути.
3. Выполнить `systemctl reload gobfd` для перечитываемых полей.
4. Проверить `journalctl -u gobfd --since '-2 min'`, `gobfdctl session list` и соседа.
5. При расхождении восстановить файл и перечитать; для startup-полей назначить maintenance restart.

Процедура выведена из кода и не является production receipt. См. [регламент](16-production-runbooks.md).
