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
- [Команда capabilities](#команда-capabilities)
- [Команды сессий](#команды-сессий)
- [Команды echo](#команды-echo)
- [Команды micro-BFD](#команды-micro-bfd)
- [Команда мониторинга](#команда-мониторинга)
- [Команда версии](#команда-версии)
- [Обновление сертификата](#обновление-сертификата)
- [Интерактивная оболочка](#интерактивная-оболочка)
- [Форматы вывода](#форматы-вывода)

### Обзор

`gobfdctl` взаимодействует с демоном gobfd через ConnectRPC (совместим с протоколами gRPC, Connect и gRPC-Web). Поддерживает как неинтерактивные команды (через Cobra), так и интерактивную оболочку (через reeflective/console) с автодополнением по Tab.

```text
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

### Команда capabilities

```bash
gobfdctl capabilities
gobfdctl capabilities --format json
gobfdctl capabilities --format yaml
```

Вызывает read-only RPC `bfd.v1.CapabilityService.ListCapabilities`. Демон
возвращает пять профилей в порядке `unsolicited`, `echo`, `micro_bfd`, `vxlan`,
`geneve`. Колонки таблицы: `CAPABILITY`, `RFC`, `MATURITY`, `ENABLEMENT`,
`READINESS`; JSON и YAML возвращают массив объектов с полями `kind`, `rfc`,
`maturity`, `enablement`, `readiness`.

| Поле | Значения и смысл |
|---|---|
| `maturity` | `preview`; готовность runtime не подтверждает RFC qualification |
| `enablement` | `disabled`, `enabled` либо `unknown`, когда runtime state недоступен |
| `readiness` | `not_ready`, `ready` либо `unknown`, когда runtime state недоступен |

Выключенные профили имеют `not_ready`. При отсутствии runtime source все пять
preview-записей сохраняются с `unknown` enablement и readiness. CLI отображает
unspecified enum как `unknown`; ошибка RPC возвращается без подстановки
локального каталога или определения статуса по клиентским флагам.

`ready` отражает необходимый receive/backend path владельца и локальную
готовность runtime, source и bindings. Значение не доказывает здоровье peers,
соответствие RFC или применение L2 actuator. API-only Echo deployment может
возвращать `not_ready`, пока последующий `echo add` не получит receive listener.
Status RPC остаётся доступен при неготовом forwarding runtime в рамках
management transport policy и не передаёт адреса peers, endpoints или секреты.

### Команды сессий

#### Список всех сессий

```bash
gobfdctl session list
```

Таблица показывает peer/local identity, текущее состояние, type и discriminators.
Поля таймеров доступны в `session show` или structured JSON/YAML output.

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
| `--local` | Нет | -- | Local IP; задайте явно для квалификации receive binding |
| `--interface` | Нет | -- | Привязка RX/TX single-hop к устройству; для `multi-hop` запрещён |
| `--type` | Нет | `single-hop` | Тип сессии: `single-hop` или `multi-hop` |
| `--tx-interval` | Нет | `1s` | Desired Min TX Interval |
| `--rx-interval` | Нет | `1s` | Required Min RX Interval |
| `--detect-mult` | Нет | `3` | Множитель обнаружения |
| `--padded-pdu-size` | Нет | `0` | Target PDU в байтах: 0 отключает padding; 24-9000 без auth, 26-9000 с auth |
| `--auth-type` | Нет | `none` | Тип auth: `none`, `simple-password`, `keyed-md5`, `meticulous-keyed-md5`, `keyed-sha1`, `meticulous-keyed-sha1` |
| `--auth-key-id` | Нет | `0` | Auth Key ID RFC 5880, диапазон 0-255; ноль допустим |
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

`echo add` требует `echo.enabled: true` у демона и готовый Echo receive path.
Выключенный preview, pending/failed reconciliation или потеря runtime/binding
readiness возвращают `FailedPrecondition` без открытия sender и создания сессии.
List/Delete остаются доступны для очистки. RFC 9747 остаётся preview.

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
| `--local` | Нет | -- | Локальный IP; задайте явно для проверки привязки приёма |
| `--interface` | Нет | -- | Сохраняется в метаданных сессии; текущий API sender не применяет `SO_BINDTODEVICE` |
| `--tx-interval` | Нет | `1s` | Интервал отправки echo (RFC 9747 Section 3.3) |
| `--detect-mult` | Нет | `3` | Detection multiplier; `DetectionTime = mult * TxInterval` |

`AddEchoSession` требует структурно корректный положительный `tx_interval`
в границах BFD wire interval; нарушения возвращают `InvalidArgument`. CLI
передаёт свой default `1s`, а прямой API-запрос должен содержать это поле.

Echo-сессии через API сейчас используют Control destination port 3784 вместо
3785. `WithDstPort(PortEcho)` применяется только к декларативным Echo-сессиям.
Ограничение отслеживается в `gobfd-qj0.8.2.1.17`; пример команды не подтверждает
работоспособный Echo dataplane.

#### Удалить echo-сессию

```bash
gobfdctl echo delete 42
```

### Команды micro-BFD

RFC 7130 micro-BFD использует одну BFD-сессию на каждый member-линк LAG.
CLI управляет ресурсом группы; создание member-сессий — отдельная операция.
Создание требует `micro_bfd.enabled: true` у демона; иначе возвращается
`FailedPrecondition`. В CLI нет команды создания member-сессий; используйте
declarative group configuration для совместного создания группы и её members.

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
protobuf TX/RX durations, повторяющихся или пустых именах members и значениях
member-сессии вне границ RFC 5880 (нулевые или выходящие за диапазон интервалы,
нулевой detect multiplier). Пропущенные интервалы получают `1s`. RPC групп
Micro-BFD возвращают:

| Условие | Код |
|---|---|
| Вызывающий уже владеет claim на группу | `AlreadyExists` |
| Существует группа с тем же `lag_interface` и другой конфигурацией | `FailedPrecondition` |
| Member link принадлежит группе другого LAG | `FailedPrecondition` |
| `DeleteMicroBFDGroup` для группы, которой вызывающий не владеет, например группы reload | `FailedPrecondition` |
| `DeleteMicroBFDGroup` для неизвестного LAG | `NotFound` |

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

```text
gobfdctl v0.1.0
  commit:  abc1234
  built:   2026-02-22T12:00:00Z
```

Информация о версии внедряется при сборке через ldflags (см. [09-development.md](./09-development.md)).

### Обновление сертификата

Обновите существующий сертификат управления через явно доверенный внешний
Smallstep CA:

```bash
gobfdctl certificate renew \
  --ca-url https://ca.example \
  --ca-root /etc/gobfd/root.pem \
  --cert /etc/gobfd/management.pem \
  --key /etc/gobfd/management-key.pem
```

| Флаг | Обязателен | Описание |
|---|---|---|
| `--ca-url` | Да | HTTPS origin доверенного CA |
| `--ca-root` | Да | Явные PEM-корни для endpoint CA и возвращённой цепочки |
| `--cert` | Да | Заменяемый PEM-файл существующего действующего сертификата |
| `--key` | Да | PEM-файл существующего соответствующего закрытого ключа |

Локальные флаги не имеют подстановки из окружения. Неинтерактивная команда
выполняет нативный mTLS-запрос `POST /renew` с HTTP-таймаутом 10 секунд и отменой
через контекст вызова, SIGINT или SIGTERM. Инициализация daemon RPC обходится;
`--addr` и флаги управления `--tls-*` не задают доступ к CA.

Входные файлы должны быть обычными, без символических ссылок, с единственной
жёсткой ссылкой и доверенными родительскими каталогами, недоступными для записи
группе и остальным. Каталоги и входные файлы принадлежат root или вызывающему
UID. Закрытый ключ имеет режим `0400` или `0600`. JSON-ответ сохраняет открытый ключ, subject, точный
SAN и профиль key usage/EKU; новый сертификат должен продлевать срок действия.
Идентичная проверенная цепочка не изменяет файл.

Атомарно заменяется только файл открытого сертификата с сохранением режима и
UID/GID; закрытый ключ не изменяется и не выводится. Успех выводит
`certificate renewed` или `certificate unchanged` независимо от `--format`.
Ошибки до замены сохраняют прежний сертификат. Ошибка `ErrCommitted` означает,
что замена уже произошла: долговечность записи может быть неопределённой,
откат не гарантируется. Этот признак сохраняется и при ошибке вывода после замены.

Первичный выпуск и автоматическое расписание не входят в команду. Доверенный
CA может выпускать идентификаторы, выбранные правилами ролей API; сам выпуск
не разрешает доступ к API. Квалификация с настоящим step-ca ожидается в
`gobfd-qj0.8.2.3.7.4`. Границы перечитывания сертификатов описаны в
[развёртывании](06-deployment.md#обновление-сертификатов-управления) и
[ADR-0005](adr/0005-smallstep-management-certificates.md).

### Интерактивная оболочка

```bash
gobfdctl shell
```

Запускает интерактивную оболочку с:
- **Автодополнением по Tab** для команд и подкоманд
- **Автодополнением readline** через reeflective/console (из дерева команд Cobra)
- Командами session, monitor, capabilities и version без префикса `gobfdctl`;
  обновление сертификата доступно только в неинтерактивном CLI

Пример сессии:

```text
gobfd> session list
gobfd> session show 10.0.0.1
gobfd> capabilities
gobfd> monitor --current
gobfd> exit
```

### Форматы вывода

Session list/show/add, echo list, micro list, monitor и capabilities поддерживают
три формата через `--format`. Echo/micro add и delete, session delete, version
и certificate renew выводят текст независимо от `--format`:

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
gobfdctl session list --format json | jq '.[].local_state'
```

### Примеры

```bash
# Подключение к удалённому демону
gobfdctl --addr https://gobfd.example.net:50052 \
  --tls-ca /etc/gobfd/ca.crt --tls-cert /etc/gobfd/client.crt \
  --tls-key /etc/gobfd/client.key session list

# Быстрая настройка сессии для тестирования
gobfdctl session add --peer 10.0.0.1 --local 10.0.0.2 --interface eth0 \
  --tx-interval 100ms --detect-mult 3

# Отслеживание изменений состояния в JSON
gobfdctl --format json monitor --current

# Проверка состояния в скрипте
if gobfdctl session show 10.0.0.1 --format json | jq -e '.local_state == "Up"' > /dev/null; then
  echo "BFD session is Up"
fi
```

### Связанные документы

- [03-configuration.md](./03-configuration.md) -- Декларативная конфигурация сессий
- [07-monitoring.md](./07-monitoring.md) -- Метрики Prometheus и дашборды Grafana
- [01-architecture.md](./01-architecture.md) -- Архитектура ConnectRPC-сервера

---

*Сверено с исходниками `dev`: 2026-10-24.*
