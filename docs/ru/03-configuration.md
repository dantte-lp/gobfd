# Конфигурация

![YAML](https://img.shields.io/badge/YAML-Config-cb171e?style=for-the-badge&logo=yaml)
![koanf](https://img.shields.io/badge/koanf-v2-00ADD8?style=for-the-badge)
![ENV](https://img.shields.io/badge/ENV-Override-ffc107?style=for-the-badge)
![SIGHUP](https://img.shields.io/badge/SIGHUP-Hot_Reload-34a853?style=for-the-badge)

> Полный справочник конфигурации демона GoBFD: формат YAML-файла, переопределение через переменные окружения, декларативные сессии, интеграция с GoBGP и горячая перезагрузка через SIGHUP.

---

### Содержание

- [Источники конфигурации](#источники-конфигурации)
- [Полный пример конфигурации](#полный-пример-конфигурации)
- [Секции конфигурации](#секции-конфигурации)
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
  addr: ":50051"

metrics:
  addr: ":9100"
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
  groups: []

# VXLAN BFD (RFC 8971)
vxlan:
  enabled: false

# Geneve BFD (RFC 9521)
geneve:
  enabled: false

# Интеграция с GoBGP (RFC 5882 Section 4.3)
gobgp:
  enabled: true
  addr: "127.0.0.1:50052"
  strategy: "disable-peer"   # или "withdraw-routes"
  dampening:
    enabled: true
    suppress_threshold: 3
    reuse_threshold: 2
    max_suppress_time: "60s"
    half_life: "15s"

# Декларативные сессии (реконсилируются при SIGHUP)
sessions:
  - peer: "10.0.0.1"
    local: "10.0.0.2"
    interface: "eth0"
    type: single_hop
    desired_min_tx: "100ms"
    required_min_rx: "100ms"
    detect_mult: 3
  - peer: "10.0.1.1"
    local: "10.0.1.2"
    type: multi_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 5
    padded_pdu_size: 128             # RFC 9764: дополнение PDU per-session
```

### Секции конфигурации

#### `grpc` -- ConnectRPC-сервер

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `addr` | string | `":50051"` | Адрес прослушивания gRPC (ConnectRPC) |

#### `metrics` -- Endpoint Prometheus

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `addr` | string | `":9100"` | HTTP-адрес для метрик |
| `path` | string | `"/metrics"` | URL-путь для скрапинга Prometheus |

#### `log` -- Логирование

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `level` | string | `"info"` | Уровень лога: `debug`, `info`, `warn`, `error` |
| `format` | string | `"json"` | Формат: `json` (production), `text` (разработка) |

Уровень лога можно изменить в runtime через SIGHUP без перезапуска демона.

#### `bfd` -- Параметры BFD по умолчанию

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `default_desired_min_tx` | duration | `"1s"` | Desired Min TX Interval по умолчанию |
| `default_required_min_rx` | duration | `"1s"` | Required Min RX Interval по умолчанию |
| `default_detect_multiplier` | uint32 | `3` | Множитель обнаружения (ОБЯЗАН быть >= 1) |
| `align_intervals` | bool | `false` | RFC 7419: выравнивание таймеров до ближайшего общего интервала |
| `default_padded_pdu_size` | uint16 | `0` | RFC 9764: дополнение BFD-пакетов до этого размера с битом DF (0 = отключено, допустимо: 24-9000) |

При `align_intervals: true` значения `DesiredMinTxInterval` и `RequiredMinRxInterval` округляются вверх до ближайшего общего интервала RFC 7419 (3.3мс, 10мс, 20мс, 50мс, 100мс, 1с). Это предотвращает несовпадения при согласовании с аппаратными реализациями BFD.

> **Примечание**: По RFC 5880 Section 6.8.3, когда сессия не в состоянии Up, TX-интервал принудительно устанавливается не менее 1 секунды.

#### `unsolicited` -- RFC 9468 Unsolicited BFD

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `unsolicited.enabled` | bool | `false` | Включить unsolicited BFD (ОБЯЗАН быть выключен по умолчанию по RFC 9468) |
| `unsolicited.max_sessions` | int | `0` | Макс. динамически созданных сессий (0 = без ограничений) |
| `unsolicited.cleanup_timeout` | duration | `"0s"` | Время ожидания перед удалением Down пассивных сессий |
| `unsolicited.session_defaults.desired_min_tx` | duration | `"1s"` | TX-интервал для автосозданных сессий |
| `unsolicited.session_defaults.required_min_rx` | duration | `"1s"` | RX-интервал для автосозданных сессий |
| `unsolicited.session_defaults.detect_mult` | uint32 | `3` | Множитель обнаружения для автосозданных сессий |
| `unsolicited.interfaces.<name>.enabled` | bool | `false` | Включить unsolicited на этом интерфейсе |
| `unsolicited.interfaces.<name>.allowed_prefixes` | []string | `[]` | Ограничение исходных адресов (CIDR) |

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
    "":
      enabled: true
      allowed_prefixes: ["10.0.0.0/8"]
```

> **Примечание**: Ключ с пустой строкой `""` соответствует слушателю по умолчанию (без привязки к интерфейсу).

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

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `micro_bfd.groups[].lag_interface` | string | -- | Имя логического LAG-интерфейса (напр., "bond0") |
| `micro_bfd.groups[].member_links` | []string | -- | Имена физических member-линков |
| `micro_bfd.groups[].peer_addr` | string | -- | IP удалённой системы для всех member-сессий |
| `micro_bfd.groups[].local_addr` | string | -- | IP локальной системы |
| `micro_bfd.groups[].desired_min_tx` | duration | -- | Интервал BFD-таймера для member-сессий |
| `micro_bfd.groups[].required_min_rx` | duration | -- | Минимальный допустимый RX-интервал |
| `micro_bfd.groups[].detect_mult` | uint32 | -- | Множитель времени обнаружения |
| `micro_bfd.groups[].min_active_links` | int | -- | Минимум Up member-ов для LAG Up (>= 1) |

Micro-BFD запускает независимые BFD-сессии на каждом member link LAG (UDP порт 6784) с `SO_BINDTODEVICE` на каждый member. Агрегатное состояние LAG = Up, когда `upCount >= min_active_links`.

Группы реконсилируются при SIGHUP. Ключ группы: `lag_interface`.

Пример:
```yaml
micro_bfd:
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
| `vxlan.management_vni` | uint32 | -- | Management VNI для BFD control (24 бит, макс. 16777215) |
| `vxlan.default_desired_min_tx` | duration | -- | TX-интервал для VXLAN-сессий по умолчанию |
| `vxlan.default_required_min_rx` | duration | -- | RX-интервал для VXLAN-сессий по умолчанию |
| `vxlan.default_detect_multiplier` | uint32 | -- | Множитель обнаружения по умолчанию |
| `vxlan.peers[].peer` | string | -- | IP удалённого VTEP |
| `vxlan.peers[].local` | string | -- | IP локального VTEP |
| `vxlan.peers[].desired_min_tx` | duration | -- | Переопределение TX-интервала для этого пира |
| `vxlan.peers[].required_min_rx` | duration | -- | Переопределение RX-интервала для этого пира |
| `vxlan.peers[].detect_mult` | uint32 | -- | Переопределение множителя обнаружения для этого пира |

BFD Control пакеты инкапсулируются в VXLAN (внешний UDP порт 4789) с выделенным Management VNI. Внутренний стек включает Ethernet (dst MAC `00:52:02:00:00:00`), IPv4 (TTL=255) и UDP (dst 3784) заголовки.

Пиры реконсилируются при SIGHUP. Ключ сессии: `(peer, local)`.

Пример:
```yaml
vxlan:
  enabled: true
  management_vni: 16777215
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.2"
      local: "10.0.0.1"
    - peer: "10.0.0.3"
      local: "10.0.0.1"
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 5
```

#### `geneve` -- RFC 9521 BFD для Geneve

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `geneve.enabled` | bool | `false` | Включить Geneve BFD-сессии |
| `geneve.default_vni` | uint32 | -- | VNI Geneve по умолчанию (24 бит, макс. 16777215) |
| `geneve.default_desired_min_tx` | duration | -- | TX-интервал для Geneve-сессий по умолчанию |
| `geneve.default_required_min_rx` | duration | -- | RX-интервал для Geneve-сессий по умолчанию |
| `geneve.default_detect_multiplier` | uint32 | -- | Множитель обнаружения по умолчанию |
| `geneve.peers[].peer` | string | -- | IP удалённого NVE |
| `geneve.peers[].local` | string | -- | IP локального NVE |
| `geneve.peers[].vni` | uint32 | -- | Переопределение `default_vni` для этого пира (0 = по умолчанию) |
| `geneve.peers[].desired_min_tx` | duration | -- | Переопределение TX-интервала для этого пира |
| `geneve.peers[].required_min_rx` | duration | -- | Переопределение RX-интервала для этого пира |
| `geneve.peers[].detect_mult` | uint32 | -- | Переопределение множителя обнаружения для этого пира |

BFD Control пакеты инкапсулируются в Geneve (внешний UDP порт 6081) с Format A (Ethernet payload, Protocol Type 0x6558). По RFC 9521 Section 4: O бит (control) = 1, C бит (critical) = 0.

Пиры реконсилируются при SIGHUP. Ключ сессии: `(peer, local)`.

Пример:
```yaml
geneve:
  enabled: true
  default_vni: 100
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.2"
      local: "10.0.0.1"
    - peer: "10.0.0.3"
      local: "10.0.0.1"
      vni: 200
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 5
```

### Переменные окружения

Все ключи конфигурации можно переопределить через переменные окружения с префиксом `GOBFD_`. Правило маппинга: убрать префикс, привести к нижнему регистру, заменить `_` на `.`.

| Переменная | Ключ конфигурации | Пример |
|---|---|---|
| `GOBFD_GRPC_ADDR` | `grpc.addr` | `":50051"` |
| `GOBFD_METRICS_ADDR` | `metrics.addr` | `":9100"` |
| `GOBFD_METRICS_PATH` | `metrics.path` | `"/metrics"` |
| `GOBFD_LOG_LEVEL` | `log.level` | `"debug"` |
| `GOBFD_LOG_FORMAT` | `log.format` | `"text"` |

### Декларативные сессии

Сессии, определённые в секции `sessions:`, создаются при старте демона и реконсилируются при SIGHUP:

- **Новые сессии** (есть в конфиге, нет запущенных) -- создаются
- **Удалённые сессии** (запущены, но нет в конфиге) -- уничтожаются
- **Существующие сессии** (совпадающий ключ) -- остаются без изменений

Ключ сессии -- кортеж: `(peer, local, interface)`.

| Ключ | Тип | Обязательный | Описание |
|---|---|---|---|
| `peer` | string | Да | IP-адрес удалённой системы |
| `local` | string | Да | IP-адрес локальной системы |
| `interface` | string | Нет | Сетевой интерфейс для `SO_BINDTODEVICE` |
| `type` | string | Нет | `single_hop` (по умолчанию) или `multi_hop` |
| `desired_min_tx` | duration | Нет | Переопределение TX-интервала |
| `required_min_rx` | duration | Нет | Переопределение RX-интервала |
| `detect_mult` | uint32 | Нет | Переопределение множителя обнаружения |
| `padded_pdu_size` | uint16 | Нет | RFC 9764: дополнение BFD-пакетов до этого размера (переопределяет `bfd.default_padded_pdu_size`) |

Типы сессий определяют UDP-порт и обработку TTL:

| Тип | Порт | Исходящий TTL | Проверка входящего TTL |
|---|---|---|---|
| `single_hop` | 3784 | 255 (GTSM) | ОБЯЗАН быть 255 (RFC 5881) |
| `multi_hop` | 4784 | 255 | ОБЯЗАН быть >= 254 (RFC 5883) |

### Интеграция с GoBGP

При включении изменения состояния BFD передаются в GoBGP через gRPC API (RFC 5882 Section 4.3):

- **BFD Down** --> Отключение BGP-пира (`DisablePeer()`) или отзыв маршрутов (`DeletePath()`)
- **BFD Up** --> Включение BGP-пира (`EnablePeer()`) или восстановление маршрутов (`AddPath()`)

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `gobgp.enabled` | bool | `false` | Включить/выключить интеграцию с GoBGP |
| `gobgp.addr` | string | `"127.0.0.1:50051"` | Адрес gRPC API GoBGP |
| `gobgp.strategy` | string | `"disable-peer"` | Стратегия: `disable-peer` или `withdraw-routes` |

#### Демпфирование flap-ов (RFC 5882 Section 3.2)

Предотвращает чрезмерную осцилляцию BGP-маршрутов при быстрых колебаниях BFD:

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `gobgp.dampening.enabled` | bool | `false` | Включить демпфирование |
| `gobgp.dampening.suppress_threshold` | float64 | `3` | Штраф, выше которого события подавляются |
| `gobgp.dampening.reuse_threshold` | float64 | `2` | Штраф, ниже которого подавление снимается |
| `gobgp.dampening.max_suppress_time` | duration | `"60s"` | Максимальная длительность подавления |
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
2. Уровень лога обновляется динамически
3. Декларативные сессии реконсилируются (добавление/удаление)
4. Echo-сессии реконсилируются (добавление/удаление)
5. Группы Micro-BFD реконсилируются (добавление/удаление, изменение member-линков)
6. VXLAN-туннельные сессии реконсилируются (добавление/удаление)
7. Geneve-туннельные сессии реконсилируются (добавление/удаление)
8. Ошибки при перезагрузке логируются -- предыдущая конфигурация остаётся в силе

### Правила валидации

| Правило | Ошибка |
|---|---|
| `grpc.addr` не должен быть пустым | `ErrEmptyGRPCAddr` |
| `bfd.default_detect_multiplier` должен быть >= 1 | `ErrInvalidDetectMultiplier` |
| `bfd.default_desired_min_tx` должен быть > 0 | `ErrInvalidDesiredMinTx` |
| `bfd.default_required_min_rx` должен быть > 0 | `ErrInvalidRequiredMinRx` |
| `gobgp.addr` не должен быть пустым при включённой интеграции | `ErrEmptyGoBGPAddr` |
| `gobgp.strategy` должен быть `disable-peer` или `withdraw-routes` | `ErrInvalidGoBGPStrategy` |
| `gobgp.dampening.suppress_threshold` должен быть > `reuse_threshold` | `ErrInvalidDampeningThreshold` |
| `gobgp.dampening.half_life` должен быть > 0 при включённом демпфировании | `ErrInvalidDampeningHalfLife` |
| `peer` сессии должен быть валидным IP-адресом | `ErrInvalidSessionPeer` |
| `type` сессии должен быть `single_hop` или `multi_hop` | `ErrInvalidSessionType` |
| `detect_mult` сессии должен быть >= 1 | `ErrInvalidSessionDetectMult` |
| Нет дублирующих ключей сессий (peer, local, interface) | `ErrDuplicateSessionKey` |
| Echo `peer` должен быть валидным IP-адресом | `ErrInvalidEchoPeer` |
| Echo `detect_mult` должен быть >= 1 | `ErrInvalidEchoDetectMult` |
| Нет дублирующих ключей echo-сессий | `ErrDuplicateEchoSessionKey` |
| VXLAN `management_vni` должен быть <= 16777215 (24 бит) | `ErrInvalidVXLANVNI` |
| VXLAN `peer` должен быть валидным IP-адресом | `ErrInvalidVXLANPeer` |
| Нет дублирующих ключей VXLAN-сессий | `ErrDuplicateVXLANSessionKey` |
| Geneve `default_vni` и per-peer `vni` должны быть <= 16777215 | `ErrInvalidGeneveVNI` |
| Geneve `peer` должен быть валидным IP-адресом | `ErrInvalidGenevePeer` |
| Нет дублирующих ключей Geneve-сессий | `ErrDuplicateGeneveSessionKey` |

### Значения по умолчанию

| Ключ | Значение | Обоснование |
|---|---|---|
| `grpc.addr` | `:50051` | Стандартный порт gRPC |
| `metrics.addr` | `:9100` | Стандартный порт экспортёра |
| `metrics.path` | `/metrics` | Конвенция Prometheus |
| `log.level` | `info` | Production по умолчанию |
| `log.format` | `json` | Машиночитаемый |
| `bfd.default_desired_min_tx` | `1s` | RFC 5880 Section 6.8.3 медленная скорость |
| `bfd.default_required_min_rx` | `1s` | Консервативная начальная точка |
| `bfd.default_detect_multiplier` | `3` | 3x время обнаружения |
| `gobgp.enabled` | `false` | Opt-in интеграция |
| `gobgp.strategy` | `disable-peer` | Наиболее безопасное действие BGP |

### Связанные документы

- [04-cli.md](./04-cli.md) -- Команды CLI для управления сессиями
- [06-deployment.md](./06-deployment.md) -- Развёртывание в production с systemd
- [configs/gobfd.example.yml](../../configs/gobfd.example.yml) -- Аннотированный пример конфигурации

---

*Последнее обновление: 2026-02-23*
