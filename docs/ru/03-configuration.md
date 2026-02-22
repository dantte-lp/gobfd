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

> **Примечание**: По RFC 5880 Section 6.8.3, когда сессия не в состоянии Up, TX-интервал принудительно устанавливается не менее 1 секунды.

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
3. Декларативные сессии реконсилируются
4. Ошибки при перезагрузке логируются -- предыдущая конфигурация остаётся в силе

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

*Последнее обновление: 2026-02-21*
