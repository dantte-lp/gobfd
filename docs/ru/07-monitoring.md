# Мониторинг

![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C?style=for-the-badge&logo=prometheus)
![Grafana](https://img.shields.io/badge/Grafana-Dashboard-F46800?style=for-the-badge&logo=grafana)
![slog](https://img.shields.io/badge/log%2Fslog-Structured-00ADD8?style=for-the-badge)
![FlightRecorder](https://img.shields.io/badge/Flight_Recorder-Go_1.27-34a853?style=for-the-badge)

> Метрики Prometheus, дашборд Grafana, структурированное логирование и runtime flight recorder для наблюдаемости в production.

---

## Содержание

- [Метрики Prometheus](#метрики-prometheus)
- [Дашборд Grafana](#дашборд-grafana)
- [Правила алертинга](#правила-алертинга)
- [Структурированное логирование](#структурированное-логирование)
- [Flight Recorder](#flight-recorder)
- [Endpoints](#endpoints)

### Метрики Prometheus

GoBFD предоставляет метрики Prometheus по настроенному адресу (по умолчанию `127.0.0.1:9100/metrics`). Метрики используют namespace `gobfd_`.

Для Control-сессии `gobfd_bfd_packets_dropped_total` учитывает queue full/closed,
owner zero-TX/auth refusals и stop abandonment. `gobfd_bfd_auth_failures_total`
является auth-подмножеством drops. Parser, TTL, unknown-peer и unsolicited
refusals до создания сессии входят только в process-lifetime ledger
`gobfd_bfd_delivery_outcomes_total{source,outcome}` без peer labels. Все
поддерживаемые пары заранее имеют нулевую серию. Каждая стадия имеет отдельный
знаменатель: `queued` не подтверждает owner `committed`, сумма стадий не является
количеством пакетов. `read_error` не доказывает потерю пакета. ListSessions
возвращает те же фиксированные строки `delivery_counters`, включая состояние
после удаления последней сессии; revision не покрывает atomic counter sampling.
Protobuf JSON может опускать нулевое значение count.

Echo использует отдельные sources `echo_read`, `echo_parse`, `echo_ttl`,
`echo_demux`, `echo_admission` и `echo_owner`. Нулевой discriminator учитывается
один раз как parser refusal без demux attempt; обычные closed/canceled reads
не являются read errors. Echo queue refusals и owner abandonment также входят
в существующую packet-drop metric принятой идентичности.

`control_notification`, `unsolicited_notification` и `echo_notification`
учитывают каждую фактическую попытку raw enqueue. `notification_dispatch`
различает accepted, stale и shutdown-abandoned raw events. `watch_recipient`
учитывает исходное событие для каждого получателя как `queued` или `gapped`,
включая повторный overflow при уже поставленном единственном GAP marker.
Marker не считается успешно доставленным исходным событием.
`legacy_notification` учитывает queued/full attempts; пропущенный Added,
закрытый channel и nil sink не являются попытками. Потеря notification не
увеличивает Control packet-drop counters. Raw abandonment учитывается после
join всех producers и Manager workers. Kernel `SO_RXQ_OVFL` остаётся задачей
`gobfd-qj0.8.2.1.6`; ledger не квалифицирует Echo transport/security. Echo Watch
lifecycle реализован отдельно в `gobfd-qj0.8.2.1.3.10`: frozen rows измеряют
фактические Echo counters/timestamps без увеличения observation version только
из-за счётчиков. Drop/auth fields, которыми Echo не владеет, равны нулю и не
заменяют delivery ledger или доказательство отсутствия потерь. Public
Control/Echo raw `queue_full` инвалидирует live revisioned subscribers через
coalesced GAP и authoritative resnapshot. Recovery markers не добавляют
outcomes `notification_dispatch`, `watch_recipient` или `legacy_notification`;
каждый исходный queued raw event сохраняет один accepted/stale/abandoned result.
Private unsolicited failures остаются cleanup-only. Legacy delivery сохраняет
best effort; отмена dispatch не обещает restart/progress.

| Метрика | Тип | Метки | Описание |
|---|---|---|---|
| `gobfd_bfd_sessions` | Gauge | `peer_addr`, `local_addr`, `session_type` | Текущее количество активных BFD-сессий |
| `gobfd_bfd_packets_sent_total` | Counter | `peer_addr`, `local_addr` | Отправленные BFD Control пакеты |
| `gobfd_bfd_packets_received_total` | Counter | `peer_addr`, `local_addr` | Полученные BFD Control пакеты |
| `gobfd_bfd_packets_dropped_total` | Counter | `peer_addr`, `local_addr` | Отброшенные пакеты (ошибки валидации/буфера) |
| `gobfd_bfd_state_transitions_total` | Counter | `peer_addr`, `local_addr`, `from_state`, `to_state` | Переходы состояний FSM |
| `gobfd_bfd_auth_failures_total` | Counter | `peer_addr`, `local_addr` | Ошибки аутентификации |
| `gobfd_bfd_delivery_outcomes_total` | Counter | `source`, `outcome` | Фиксированные решения стадий Control, Echo и notifications после удаления сессии |
| `gobfd_gobgp_rpc_errors_total` | Counter | `rpc` | Неудачные вызовы API GoBGP: `DisablePeer`, `EnablePeer`, `CheckCompatibility` (preflight) |
| `gobfd_gobgp_endpoint_compatible` | Gauge | -- | 1 после успешного preflight совместимости с GoBGP v4, иначе 0 |
| `gobfd_gobgp_dampening_penalty` | Gauge | `peer_addr` | Эффективный flap penalty с decay на момент scrape |
| `gobfd_gobgp_dampening_suppressed` | Gauge | `peer_addr` | 1, если dampening ещё удерживает восстановление |
| `gobfd_gobgp_dampening_suppression_deadline_seconds` | Gauge | `peer_addr` | Unix timestamp максимальной границы текущего suppression; 0 без suppression |
| `gobfd_gobgp_pending_action` | Gauge | `peer_addr`, `action` | 1 для неприменённого желаемого действия (`up` или `down`); иначе 0 |
| `gobfd_gobgp_pending_action_deadline_seconds` | Gauge | `peer_addr`, `action` | Unix timestamp следующего retry или suppression recheck; 0 при отсутствии действия |

Per-peer метрики GoBGP существуют только при включённой интеграции для пиров
из последней валидной конфигурации `sessions`. Allowlist меняется при принятом
reload; удалённые пиры исчезают при следующем scrape, отклонённый кандидат
сохраняет прежний allowlist. IPv4-mapped адреса приводятся к IPv4, дубликаты
объединяются. API и unsolicited sessions не создают новые metric labels.
Для каждого configured peer выдаются семь samples в пяти gauges, включая
нулевые action samples `up` и `down` в отсутствие ожидающего действия.

Penalty и suppression вычисляются на момент scrape по `half_life`,
`reuse_threshold` и `max_suppress_time`. После истечения recovery suppression
метрика penalty равна нулю, как при снятии suppression обработчиком.
Наблюдение не изменяет routing state и не регистрирует flap. Граница
suppression равна `suppressed_since + max_suppress_time`; decay может разрешить
восстановление раньше. Эта граница определяет допустимость восстановления,
а не завершение RPC.

Pending action и его deadline отражают последний завершённый event или
attempt обработчика: retry временной ошибки RPC или удерживаемый recovery.
Action deadline задаёт следующий запланированный attempt или recheck и может
оказаться в прошлом во время выполнения RPC. Это не граница suppression и
не гарантия успешного применения. Успешное применение, отмена, удаление сессии
или остановка обработчика очищают pending action. AdminDown отменяет
ожидающий Down; Up для включения ранее отключённого GoBFD пира сохраняется.

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
1. Запустите production-стек: `podman compose -f deployments/compose/compose.yml up -d`
2. Откройте Grafana: `http://localhost:3000` (admin/admin)
3. Дашборд автоматически провизионируется из JSON-файла

### Правила алертинга

Рекомендуемые правила алертинга:

```yaml
groups:
  - name: gobfd
    rules:
      # Алерт, когда нет сконфигурированных или динамически активных сессий.
      - alert: BFDNoActiveSessions
        expr: sum(gobfd_bfd_sessions) == 0
        for: 10s
        labels:
          severity: warning
        annotations:
          summary: "Нет активных BFD-сессий"

      # Алерт при переходе сессии в Down.
      - alert: BFDSessionDownTransition
        expr: increase(gobfd_bfd_state_transitions_total{from_state="Up",to_state="Down"}[1m]) > 0
        for: 0s
        labels:
          severity: critical
        annotations:
          summary: "BFD-сессия {{ $labels.local_addr }} -> {{ $labels.peer_addr }} перешла в Down"

      # Алерт при чрезмерном flapping-е.
      - alert: BFDSessionFlapping
        expr: sum by (peer_addr, local_addr) (increase(gobfd_bfd_state_transitions_total[5m])) > 3
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "BFD-сессия {{ $labels.local_addr }} -> {{ $labels.peer_addr }} flapping"

      # Алерт при ошибках аутентификации
      - alert: BFDAuthFailures
        expr: rate(gobfd_bfd_auth_failures_total[5m]) > 0
        for: 30s
        labels:
          severity: warning
        annotations:
          summary: "Ошибки BFD auth от {{ $labels.peer_addr }}"

      # Алерт при высоком уровне потерь пакетов
      - alert: BFDPacketDrops
        expr: rate(gobfd_bfd_packets_dropped_total[5m]) > 0
        for: 1m
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

Для каждого валидного startup или SIGHUP candidate coordinator формирует одно
aggregate structured event: INFO event `configuration reconciliation converged`
только после convergence всех шести sources или ERROR event `configuration
reconciliation incomplete` для stale generation. Обычный настроенный logging
threshold продолжает действовать, поэтому INFO event convergence фильтруется
при уровнях WARN и ERROR. Поля включают `desired_generation`,
`applied_generation`, `stale`, aggregate `pending`/`failed`, а также per-source
`created`, `released`, `pending`, `failed` и ограниченные счётчики кодов ошибок.
Raw error text может появиться только в этом непосредственном event; он не
сохраняется в runtime snapshot и не используется как label.

Linux interface events обновляют эти же receipts без смены desired generation.
Недоступные зависимости делают readiness stale и sources pending; успешный
автоматический retry восстанавливает convergence. Одинаковые receipts не
создают повторных events. Переполнение netlink записывает запрос resync в журнал;
заполненный канал сохраняет запрос до доставки. Размер очереди фиксирован и не
зависит от количества событий. Это не доказывает активацию отсутствующего startup
listener или overlay backend. Non-Linux stub не выполняет автоматический retry.

### Flight Recorder

`runtime/trace.FlightRecorder` захватывает скользящее окно трассировок выполнения для post-mortem отладки:

| Параметр | Значение |
|---|---|
| Возраст окна | 500ms |
| Максимальный размер | 2 MiB |

Flight recorder захватывает события планирования, создание горутин, паузы GC и сетевой I/O. При сбое BFD-сессии снимок трассировки может быть сохранён для анализа через `go tool trace`.

HTTP-дамп по умолчанию выключен. С `grpc.flight_recorder: true` он доступен по
`/debug/flightrecorder` на сервере управления только вызывающим с принципалом
(Unix-сокет или mTLS-идентичность, разрешённая `grpc.mutating_identities`);
анонимные клиенты на loopback TCP получают `403` даже при
`grpc.unsafe_loopback_mutations`. Listener метрик не обслуживает endpoint
отладки:

```bash
curl --unix-socket /run/gobfd/gobfd.sock -o trace.out http://localhost/debug/flightrecorder
go tool trace trace.out
```

### Endpoints

| Endpoint | Порт | Путь | Описание |
|---|---|---|---|
| Метрики Prometheus | `127.0.0.1:9100` | `/metrics` | Цель скрапинга Prometheus |
| gRPC API | `unix:///run/gobfd/gobfd.sock` | -- | ConnectRPC/gRPC API; необязательный TCP через `grpc.addr` |
| gRPC Health | `unix:///run/gobfd/gobfd.sock` | `/grpc.health.v1.Health/Check` | Пустой service означает reconciliation readiness; named services означают health процесса/service |

Проверка с пустым service возвращает `NOT_SERVING` до первого converged
generation и пока последний desired generation остаётся stale, затем `SERVING`
после полного convergence. Named checks `grpc.health.v1.Health`,
`bfd.v1.BfdService`, `bfd.v1.EchoService` и `bfd.v1.MicroBFDService` остаются
`SERVING`. `gobfd.management` находится в `NOT_SERVING`, пока
`grpc.unsafe_loopback_mutations` разрешает анонимным клиентам на loopback
изменять состояние, иначе `SERVING`. При `gobgp.enabled` named check
`gobfd.gobgp` находится в
`NOT_SERVING` до успешного preflight GoBGP v4, а также после неудачного
preflight или неудачного `DisablePeer`/`EnablePeer` до следующего успеха,
а также после BFD Down для соседа с несколькими базовыми сессиями
(`03-configuration.md`, «Идентичность сессии»).
systemd `READY=1` продолжает описывать запуск процесса/listeners, а не
convergence конфигурации.

### Связанные документы

- [03-configuration.md](./03-configuration.md) -- Конфигурация endpoint-а метрик
- [06-deployment.md](./06-deployment.md) -- Production-стек с Prometheus + Grafana
- [04-cli.md](./04-cli.md) -- Команда мониторинга CLI для живых событий

---

*Последнее обновление: 2026-02-21*
