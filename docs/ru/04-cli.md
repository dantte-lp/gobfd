# Справочник CLI

![Cobra](https://img.shields.io/badge/Cobra-CLI-1a73e8?style=for-the-badge)
![reeflective/console](https://img.shields.io/badge/reeflective%2Fconsole-Interactive-34a853?style=for-the-badge)
![ConnectRPC](https://img.shields.io/badge/ConnectRPC-gRPC-ea4335?style=for-the-badge)
![Formats](https://img.shields.io/badge/Output-table%20%7C%20json%20%7C%20yaml-ffc107?style=for-the-badge)

> Полный справочник `gobfdctl` -- CLI-клиента для управления BFD-сессиями, мониторинга событий и взаимодействия с демоном gobfd.

---

## Содержание

- [Обзор](#обзор)
- [Глобальные флаги](#глобальные-флаги)
- [Команды сессий](#команды-сессий)
- [Команды echo](#команды-echo)
- [Команды micro-BFD](#команды-micro-bfd)
- [Команда мониторинга](#команда-мониторинга)
- [Команда версии](#команда-версии)
- [Интерактивная оболочка](#интерактивная-оболочка)
- [Форматы вывода](#форматы-вывода)

### Обзор

`gobfdctl` взаимодействует с демоном gobfd через ConnectRPC (совместим с протоколами gRPC, Connect и gRPC-Web). Поддерживает как неинтерактивные команды (через Cobra), так и интерактивную оболочку (через reeflective/console) с автодополнением по Tab.

```
gobfdctl [глобальные флаги] <команда> [подкоманда] [флаги]
```

### Глобальные флаги

| Флаг | По умолчанию | Описание |
|---|---|---|
| `--addr` | `unix:///run/gobfd/gobfd.sock` | Endpoint демона gobfd: `host:port`, `http://host:port`, `https://host:port` или `unix:///path` (сокет управления) |
| `--tls-ca` | `""` | Файл CA, подписавшего сертификат демона (endpoint `https://`) |
| `--tls-cert` | `""` | Файл сертификата клиента для mTLS |
| `--tls-key` | `""` | Файл ключа клиента для mTLS |
| `--format` | `table` | Формат вывода: `table`, `json`, `yaml` |

### Команды сессий

#### Список всех сессий

```bash
gobfdctl session list
```

#### Показать конкретную сессию

```bash
# По адресу пира
gobfdctl session show 10.0.0.1

# По локальному дискриминатору
gobfdctl session show 42
```

Если адресу пира соответствует несколько сессий (например, один пир на двух
интерфейсах), запрос завершается с `FAILED_PRECONDITION` и перечисляет
локальные дискриминаторы; сессию нужно запросить по дискриминатору.

#### Создать сессию

```bash
# Single-hop BFD-сессия
gobfdctl session add \
  --peer 10.0.0.1 \
  --local 10.0.0.2 \
  --interface eth0 \
  --type single-hop \
  --tx-interval 100ms \
  --rx-interval 100ms \
  --detect-mult 3

# Multihop-сессия
gobfdctl session add \
  --peer 192.168.1.1 \
  --local 192.168.2.1 \
  --type multi-hop \
  --tx-interval 300ms \
  --detect-mult 5

# Аутентифицированная сессия
gobfdctl session add \
  --peer 10.0.0.1 \
  --local 10.0.0.2 \
  --auth-type keyed-sha1 \
  --auth-key-id 7 \
  --auth-secret api-auth-secret \
  --padded-pdu-size 128
```

| Флаг | Обязательный | По умолчанию | Описание |
|---|---|---|---|
| `--peer` | Да | -- | IP-адрес удалённой системы |
| `--local` | Да | -- | IP-адрес локальной системы |
| `--interface` | Нет | -- | Сетевой интерфейс (для `SO_BINDTODEVICE`) |
| `--type` | Нет | `single-hop` | Тип сессии: `single-hop` или `multi-hop` |
| `--tx-interval` | Нет | `1s` | Desired Min TX Interval |
| `--rx-interval` | Нет | `1s` | Required Min RX Interval |
| `--detect-mult` | Нет | `3` | Множитель обнаружения |
| `--padded-pdu-size` | Нет | `0` | Target PDU в байтах: 0 отключает padding; 24-9000 без auth, 26-9000 с auth |
| `--auth-type` | Нет | `none` | Тип auth: `none`, `simple-password`, `keyed-md5`, `meticulous-keyed-md5`, `keyed-sha1`, `meticulous-keyed-sha1` |
| `--auth-key-id` | Если auth включён | `0` | Auth Key ID RFC 5880, диапазон 0-255 |
| `--auth-secret` | Если auth включён | -- | Секрет: 1-16 байт для Simple Password/MD5, 1-20 байт для SHA1 |

`AddSession` возвращает `InvalidArgument` при некорректной структуре protobuf
TX/RX durations и при отклонении клиентских интервалов проверками ядра.
Пропущенные интервалы получают `1s`; явный required RX interval, равный нулю,
остаётся допустимым.

`session show` отображает `Padded PDU Target` в байтах. JSON/YAML вывод сессий,
включая строки monitor, передаёт настроенный target целым `padded_pdu_size`;
authenticated PDU может быть длиннее. CLI/API `0` отключает padding, а `0`
в YAML-сессии наследует `bfd.default_padded_pdu_size`.

#### Удалить сессию

```bash
gobfdctl session delete 42
```

### Команды echo

Сессии RFC 9747 unaffiliated BFD echo выделены в отдельную группу
команд: их таймерная модель отличается от RFC 5880 control sessions --
TxInterval задаётся локально и не согласуется с peer.

#### Список echo-сессий

```bash
gobfdctl echo list
```

#### Создать echo-сессию

```bash
gobfdctl echo add \
    --peer 10.0.0.1 \
    --local 10.0.0.2 \
    --tx-interval 50ms \
    --detect-mult 3
```

| Флаг | Обязательный | По умолчанию | Описание |
|---|---|---|---|
| `--peer` | Да | -- | IP-адрес echo target |
| `--local` | Нет | -- | Локальный IP (fallback на default route) |
| `--interface` | Нет | -- | Outbound интерфейс для `SO_BINDTODEVICE` |
| `--tx-interval` | Нет | `1s` | Интервал отправки echo (RFC 9747 Section 3.3) |
| `--detect-mult` | Нет | `3` | Detection multiplier; `DetectionTime = mult * TxInterval` |

`AddEchoSession` требует структурно корректный положительный `tx_interval`
в границах BFD wire interval; нарушения возвращают `InvalidArgument`. CLI
передаёт свой default `1s`, а прямой API-запрос должен содержать это поле.

#### Удалить echo-сессию

```bash
gobfdctl echo delete 42
```

### Команды micro-BFD

RFC 7130 micro-BFD использует одну BFD-сессию на каждый member-линк LAG.
CLI управляет ресурсом группы; создание member-сессий — отдельная операция.

#### Список micro-BFD групп

```bash
gobfdctl micro list
```

#### Создать micro-BFD группу

```bash
gobfdctl micro add \
    --lag bond0 \
    --members eth0,eth1,eth2 \
    --peer 10.0.0.1 \
    --local 10.0.0.2 \
    --tx-interval 300ms \
    --rx-interval 300ms \
    --detect-mult 3 \
    --min-active 2
```

| Флаг | Обязательный | По умолчанию | Описание |
|---|---|---|---|
| `--lag` | Да | -- | Имя LAG-интерфейса (`bond0`, `team0`, `port-channel1`) |
| `--members` | Да | -- | Member-линки через запятую |
| `--peer` | Да | -- | IP-адрес peer для всех member-сессий |
| `--local` | Нет | -- | Локальный IP-адрес |
| `--tx-interval` | Нет | `1s` | Desired minimum TX interval |
| `--rx-interval` | Нет | `1s` | Required minimum RX interval |
| `--detect-mult` | Нет | `3` | Detection multiplier (RFC 7130 Section 2.2) |
| `--min-active` | Нет | `1` | Минимум активных members для aggregate Up |

CLI отклоняет `--min-active` вне диапазона `[1, len(--members)]` до
обращения к демону.

`AddMicroBFDGroup` возвращает `InvalidArgument` при некорректной структуре
protobuf TX/RX durations и повторяющихся именах members. Пропущенные интервалы
получают `1s`. Проверка и применение структурно корректных значений таймеров
members остаются в `gobfd-qj0.8.2.2.1` (M09).

#### Удалить micro-BFD группу

```bash
gobfdctl micro delete bond0
```

### Команда мониторинга

Стриминг BFD-событий (изменений состояния) в реальном времени:

```bash
# Только новые события
gobfdctl monitor

# События с начальным снимком всех сессий
gobfdctl monitor --current
```

| Флаг | По умолчанию | Описание |
|---|---|---|
| `--current` | `false` | Включить текущие состояния сессий перед стримингом |

Мониторинг использует gRPC server-side streaming. Каждое событие включает: метку времени, идентификатор сессии, старое и новое состояние, диагностический код и ревизию (`rev=`), возрастающую в пределах одного процесса демона.

Снимок (`--current`) — последовательность событий `SessionAdded`, которую
закрывает `SyncComplete` с ревизией, покрытой снимком. Если мониторинг
отстаёт от демона, он получает `Gap` и затем новый снимок, с `--current` и
без него. Освобождение последнего владельца wire session создаёт
`SessionDeleted` с идентификаторами, последним состоянием и ревизией;
освобождение общей claim не создаёт событие удаления. Последующие снимки
не содержат удалённую сессию. Удаление отменяет её ожидающие действия
GoBGP Down и Up; ранее отключённый GoBFD пир не включается автоматически.

Новая Control- или Echo-сессия создаёт `SessionAdded`, даже оставаясь Down.
Additions, changes и removals передают полный API-статус на границе публикации;
List измеряет телеметрию отдельно. `ObservationVersion` покрывает причинный
кортеж, а не каждый пакет или изменение счётчика. Клиент заменяет строку
целиком, включая zero/false и отсутствующие timestamps. Initial, GAP и reconnect
snapshots включают оба реестра на одной revision boundary. Echo rows передают
локальные TX interval, detection settings, counters и timestamps; remote,
negotiated, drop/auth fields, которыми Echo не владеет, равны нулю и не
доказывают отсутствие потерь. Control List/Get и typed Echo commands сохраняют
прежнюю область. Потеря public Control/Echo raw notification также создаёт
`Gap` и свежий snapshot при работающем dispatch, даже если очередь самого
монитора свободна. Следующий owner edge или reconnect не требуется. Общий Watch
client отклоняет старые queued observations после нового снимка; CLI печатает
revisioned event stream. Private cleanup и legacy subscribers сохраняют
отдельные delivery boundaries.

Нажмите `Ctrl+C` для остановки.

### Команда версии

Все бинарные файлы поддерживают отображение версии с хэшем коммита и датой сборки:

```bash
gobfd --version
gobfdctl version
gobfd-haproxy-agent --version
gobfd-exabgp-bridge --version
```

Пример вывода:

```
gobfdctl v0.1.0
  commit:  abc1234
  built:   2026-02-22T12:00:00Z
```

Информация о версии внедряется при сборке через ldflags (см. [09-development.md](./09-development.md)).

### Интерактивная оболочка

```bash
gobfdctl shell
```

Запускает интерактивную оболочку с:
- **Автодополнением по Tab** для команд и подкоманд
- **Автодополнением readline** через reeflective/console (из дерева команд Cobra)
- Полным доступом ко всем командам gobfdctl без префикса `gobfdctl`

Пример сессии:

```
gobfd> session list
gobfd> session show 10.0.0.1
gobfd> monitor --current
gobfd> exit
```

### Форматы вывода

| Формат | Применение |
|---|---|
| `table` | Человекочитаемый вывод в терминал (по умолчанию) |
| `json` | Машиночитаемый, скриптинг, конвейер к `jq` |
| `yaml` | Структурированный человекочитаемый вывод |

Имена и значения полей JSON образуют машиночитаемый контракт CLI. YAML сохраняет
ту же модель данных, но пробелы, отступы и представление скаляров не являются
побайтово стабильными при совместимых обновлениях сериализатора; автоматизация
должна разбирать YAML, а не сравнивать его отрисованный текст.

Control session detail показывает `Packets Dropped` и `Auth Failures` из тех же
sampled session counters, что Get/List и Control Watch. Auth failures входят в
drops; принятый в очередь пакет может быть отклонён владельцем или abandoned
при остановке. Process-lifetime итоги стадий, включая unknown peers и удалённые
сессии, доступны через API `ListSessions.delivery_counters` и
`gobfd_bfd_delivery_outcomes_total`. Фиксированные строки также различают Echo
RX и raw/dispatch/recipient notification outcomes; notification loss не является
Control packet drop. Session table их не отображает. Знаменатели
и оставшиеся очереди описаны в [мониторинге](07-monitoring.md).

```bash
# JSON-вывод для скриптов
gobfdctl session list --format json

# YAML-вывод
gobfdctl session list --format yaml

# Конвейер JSON к jq
gobfdctl session list --format json | jq '.sessions[].state'
```

### Примеры

```bash
# Подключение к удалённому демону
gobfdctl --addr 10.0.0.1:50052 session list

# Быстрая настройка сессии для тестирования
gobfdctl session add --peer 10.0.0.1 --local 10.0.0.2 --interface eth0 \
  --tx-interval 100ms --detect-mult 3

# Отслеживание изменений состояния в JSON
gobfdctl --format json monitor --current
```

### Связанные документы

- [03-configuration.md](./03-configuration.md) -- Декларативная конфигурация сессий
- [07-monitoring.md](./07-monitoring.md) -- Метрики Prometheus и дашборды Grafana
- [01-architecture.md](./01-architecture.md) -- Архитектура ConnectRPC-сервера

---

*Последнее обновление: 2026-02-21*
