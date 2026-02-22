# Мониторинг

![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C?style=for-the-badge&logo=prometheus)
![Grafana](https://img.shields.io/badge/Grafana-Dashboard-F46800?style=for-the-badge&logo=grafana)
![slog](https://img.shields.io/badge/log%2Fslog-Structured-00ADD8?style=for-the-badge)
![FlightRecorder](https://img.shields.io/badge/Flight_Recorder-Go_1.26-34a853?style=for-the-badge)

> Метрики Prometheus, дашборд Grafana, структурированное логирование и flight recorder Go 1.26 для наблюдаемости в production.

---

### Содержание

- [Метрики Prometheus](#метрики-prometheus)
- [Дашборд Grafana](#дашборд-grafana)
- [Правила алертинга](#правила-алертинга)
- [Структурированное логирование](#структурированное-логирование)
- [Flight Recorder](#flight-recorder)
- [Endpoints](#endpoints)

### Метрики Prometheus

GoBFD предоставляет метрики Prometheus по настроенному адресу (по умолчанию `:9100/metrics`). Все метрики имеют префикс `gobfd_bfd_`.

| Метрика | Тип | Метки | Описание |
|---|---|---|---|
| `gobfd_bfd_sessions` | Gauge | `peer_addr`, `local_addr`, `session_type` | Текущее количество активных BFD-сессий |
| `gobfd_bfd_packets_sent_total` | Counter | `peer_addr`, `local_addr` | Отправленные BFD Control пакеты |
| `gobfd_bfd_packets_received_total` | Counter | `peer_addr`, `local_addr` | Полученные BFD Control пакеты |
| `gobfd_bfd_packets_dropped_total` | Counter | `peer_addr`, `local_addr` | Отброшенные пакеты (ошибки валидации/буфера) |
| `gobfd_bfd_state_transitions_total` | Counter | `peer_addr`, `local_addr`, `from_state`, `to_state` | Переходы состояний FSM |
| `gobfd_bfd_auth_failures_total` | Counter | `peer_addr`, `local_addr` | Ошибки аутентификации |

#### Примеры запросов

```promql
# Общее количество активных сессий
sum(gobfd_bfd_sessions)

# Flap-ы сессий (переходы Up -> Down за час)
increase(gobfd_bfd_state_transitions_total{from_state="Up", to_state="Down"}[1h])

# Частота отправки пакетов по пирам
rate(gobfd_bfd_packets_sent_total[5m])

# Частота ошибок аутентификации
rate(gobfd_bfd_auth_failures_total[5m])

# Частота потерь пакетов
rate(gobfd_bfd_packets_dropped_total[5m])
```

### Дашборд Grafana

Готовый дашборд расположен в:

```
deployments/compose/configs/grafana/dashboards/bfd.json
```

Панели дашборда:

| Панель | Описание |
|---|---|
| **Обзор сессий** | Количество сессий, распределение по состояниям и типам |
| **Скорость пакетов** | TX/RX по пирам |
| **Переходы состояний** | Временная шкала изменений с аннотациями Up/Down |
| **Ошибки** | Ошибки auth, отброшенные пакеты |

Для импорта:
1. Запустите production-стек: `podman-compose -f deployments/compose/compose.yml up -d`
2. Откройте Grafana: `http://localhost:3000` (admin/admin)
3. Дашборд автоматически провизионируется из JSON-файла

### Правила алертинга

Рекомендуемые правила алертинга:

```yaml
groups:
  - name: gobfd
    rules:
      # Алерт при переходе сессии в Down
      - alert: BFDSessionDown
        expr: increase(gobfd_bfd_state_transitions_total{from_state="Up", to_state="Down"}[5m]) > 0
        for: 0s
        labels:
          severity: critical
        annotations:
          summary: "BFD-сессия {{ $labels.peer_addr }} перешла в Down"

      # Алерт при чрезмерном flapping-е
      - alert: BFDSessionFlapping
        expr: increase(gobfd_bfd_state_transitions_total{from_state="Up", to_state="Down"}[1h]) > 5
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "BFD-сессия {{ $labels.peer_addr }} flapping"

      # Алерт при ошибках аутентификации
      - alert: BFDAuthFailure
        expr: rate(gobfd_bfd_auth_failures_total[5m]) > 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "Ошибки BFD auth от {{ $labels.peer_addr }}"

      # Алерт при высоком уровне потерь пакетов
      - alert: BFDPacketDrops
        expr: rate(gobfd_bfd_packets_dropped_total[5m]) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Потери BFD-пакетов для {{ $labels.peer_addr }}"
```

### Структурированное логирование

GoBFD использует исключительно `log/slog` (stdlib).

#### Уровни логирования

| Уровень | Использование |
|---|---|
| `debug` | Детали согласования таймеров, содержимое пакетов, детали событий FSM |
| `info` | Смена состояния сессии, старт/стоп демона, перезагрузка конфига |
| `warn` | Переход в Down, ошибки auth, невалидные пакеты |
| `error` | Невозможность bind сокета, критические ошибки FSM |

#### Структурированные поля

Каждая запись лога из сессии включает:

```json
{
  "time": "2026-02-21T12:00:00.000Z",
  "level": "INFO",
  "msg": "session state changed",
  "peer": "10.0.0.1",
  "local_discr": 42,
  "old_state": "Down",
  "new_state": "Up",
  "event": "RecvInit",
  "diag": "None"
}
```

#### Изменение уровня лога в runtime

Уровень лога можно изменить в runtime через SIGHUP без перезапуска:

```bash
# Изменить log.level: debug в конфиге
sudo vim /etc/gobfd/gobfd.yml

# Перезагрузить
sudo systemctl reload gobfd
```

### Flight Recorder

Go 1.26 `runtime/trace.FlightRecorder` захватывает скользящее окно трассировок выполнения для post-mortem отладки:

| Параметр | Значение |
|---|---|
| Возраст окна | 500ms |
| Максимальный размер | 2 MiB |

Flight recorder захватывает события планирования, создание горутин, паузы GC и сетевой I/O. При сбое BFD-сессии снимок трассировки может быть сохранён для анализа через `go tool trace`.

### Endpoints

| Endpoint | Порт | Путь | Описание |
|---|---|---|---|
| Метрики Prometheus | `:9100` | `/metrics` | Цель скрапинга Prometheus |
| gRPC API | `:50051` | -- | ConnectRPC/gRPC API |
| gRPC Health | `:50051` | `/grpc.health.v1.Health/Check` | Проверка здоровья gRPC |

### Связанные документы

- [03-configuration.md](./03-configuration.md) -- Конфигурация endpoint-а метрик
- [06-deployment.md](./06-deployment.md) -- Production-стек с Prometheus + Grafana
- [04-cli.md](./04-cli.md) -- Команда мониторинга CLI для живых событий

---

*Последнее обновление: 2026-02-21*
