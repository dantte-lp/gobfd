# Командная строка

`gobfdctl` обращается к API управления демона. Команды, flags и форматирование определены в [исходниках CLI](../../cmd/gobfdctl/commands/root.go), сообщения — в [protobuf-схеме](../../api/bfd/v1/bfd.proto). Успешный list доказывает доступ к управлению, а не исправность пересылки.

## Глобальные flags

| Flag | Значение | Условие |
| :--- | :--- | :--- |
| `--addr` | `unix:///run/gobfd/gobfd.sock` | Unix URL, `host:port`, `http://` или `https://` |
| `--tls-ca` | пусто | CA PEM сертификата демона |
| `--tls-cert` | пусто | Сертификат клиента PEM |
| `--tls-key` | пусто | Закрытый ключ клиента PEM |
| `--format` | `table` | `table`, `json`, `yaml` для структурированного вывода |

TLS-файлы требуют HTTPS. Политика сервера определяет право записи. CLI не читает `GOBFD_*` overrides демона. Источник: [транспорт endpoint](../../internal/apiclient/endpoint.go).

```sh title="management-readback.sh"
gobfdctl session list
gobfdctl --format json capabilities
gobfdctl --addr https://bfd.example.net:50052 --tls-ca /etc/gobfd/tls/ca.pem --tls-cert /etc/gobfd/tls/client.pem --tls-key /etc/gobfd/tls/client.key session list
```

## Команды

| Команда | Аргументы/flags | Результат |
| :--- | :--- | :--- |
| `session list` | нет | Все Control-сессии |
| `session show IDENTIFIER` | IP соседа или uint32 discriminator | Одна Control-сессия |
| `session add` | Flags ниже | Создать Control claim |
| `session delete DISCRIMINATOR` | uint32 | Освободить Control claim вызывающего |
| `echo list` | нет | Все Echo-сессии |
| `echo add` | Flags ниже | Создать Echo claim preview |
| `echo delete DISCRIMINATOR` | uint32 | Освободить Echo claim вызывающего |
| `micro list` | нет | Группы Micro-BFD |
| `micro add` | Flags ниже | Создать claim LAG preview |
| `micro delete LAG` | Интерфейс LAG | Освободить claim группы |
| `capabilities` | нет | Каталог preview демона |
| `monitor` | `--current`, default false | Поток событий Control и Echo |
| `certificate renew` | Четыре обязательных flags ниже | Обновить сертификат независимо от RPC демона |
| `version` | нет | Локальная сборка CLI |
| `shell` | нет | Интерактивное подмножество |
| `help` | Необязательный путь команды | Справка |

Использовать discriminator, когда адрес разделяют несколько сессий. Delete освобождает claim; другой владелец может сохранить физическую сессию. Config claims не принадлежат principal CLI. См. [авторизацию](15-security.md#api-управления).

## Создание Control

| Flag | Значение | Условие |
| :--- | :--- | :--- |
| `--peer` | обязательно | IP соседа |
| `--local`, `--interface` | пусто | Локальный IP и привязка устройства |
| `--type` | `single-hop` | `single-hop` или `multi-hop` |
| `--tx-interval`, `--rx-interval` | `1s` каждый | Строки длительности; RX допускает ноль |
| `--detect-mult` | `3` | 1–255 после проверки сервера |
| `--padded-pdu-size` | `0` | Целевой размер; ноль отключает padding запроса |
| `--auth-type` | `none` | `none`, `simple-password`, `keyed-md5`, `meticulous-keyed-md5`, `keyed-sha1`, `meticulous-keyed-sha1` |
| `--auth-key-id` | `0` | 0–255 |
| `--auth-secret` | пусто | Обязательно при включённой auth |

Нормализация auth принимает разный регистр и подчёркивания/дефисы. Материал ключа при отключённой auth отклоняется. Flag `--multihop-max-hops` и hop-policy field в `AddSessionRequest` отсутствуют: нестандартная политика задаётся только YAML. Проверка сервера, допуск привязки и авторизация сохраняются.

```sh title="control-session.sh"
gobfdctl session add --peer 192.0.2.2 --local 192.0.2.1 --interface eth0 --tx-interval 300ms --rx-interval 300ms --detect-mult 3
gobfdctl session show 192.0.2.2
gobfdctl monitor --current
```

Runtime claims не сохраняются в YAML. Значения auth flags могут попасть в process arguments/history; для production использовать защищённый YAML или контролируемый API-клиент. Источник: [Control-команды](../../cmd/gobfdctl/commands/session.go).

## Создание Echo и Micro-BFD

| Команда | Flags и defaults | Ограничение |
| :--- | :--- | :--- |
| `echo add` | `--peer` обязателен; `--local`/`--interface` пусты; `--tx-interval=1s`, `--detect-mult=3` | Preview включён; TX положительный; множитель 1–255 |
| `micro add` | `--lag`/`--members`/`--peer` обязательны; `--local` пуст; `--tx-interval=1s`, `--rx-interval=1s`, `--detect-mult=3`, `--min-active=1` | Preview включён; минимум активных 1…число участников |

`--members` принимает интерфейсы через запятую. Создание Echo/Micro выводит строку статуса; `--format` действует на list. Создание не включает профиль и не настраивает actuator. Источники: [Echo](../../cmd/gobfdctl/commands/echo.go), [Micro-BFD](../../cmd/gobfdctl/commands/micro.go).

## Capabilities и события

`capabilities` возвращает kind, RFC, maturity, enablement и локальную readiness. Каталог только для чтения доступен при неготовой forwarding runtime согласно транспортной политике. Клиент не подставляет fallback. Отключённый профиль — `not_ready`; недоступная runtime-информация — `unknown`. Включённый overlay без peers не владеет backend. Echo только через API может требовать допущенный claim для создания receive listener. Readiness не означает peer health или RFC qualification; каталог не заменяет admission preflight.

`monitor --current` выдаёт snapshots Control/Echo, затем события; `session list/show` остаются Control-only, `echo list` читает Echo отдельно. JSON/YAML — последовательность событий, не база сессий. Контроллер выполняет сверку после reconnect; см. [API](../../api/bfd/v1/bfd.proto), [combined Watch snapshot](../../internal/bfd/manager_session.go).

## Обновление сертификата

`certificate renew` обращается к внешнему CA существующей identity, проверяет ответ и атомарно заменяет только сертификат. Enrollment, key rotation и планировщик в область команды не входят.

```sh
gobfdctl certificate renew --ca-url https://ca.example.net --ca-root /etc/gobfd/tls/roots.pem --cert /etc/gobfd/tls/client.pem --key /etc/gobfd/tls/client.key
```

Все четыре flags обязательны. Вывод: `certificate renewed` либо `certificate unchanged`. Ошибка после замены может означать уже применённую операцию; проверить сертификат перед повтором. Renewal/revocation не удаляет API-доступ немедленно. Источники: [команда](../../cmd/gobfdctl/commands/certificate.go), [клиент](../../internal/stepca/client.go), [ADR-0005](adr/0005-smallstep-management-certificates.md).

## Интерактивная оболочка и завершение

Shell поддерживает `session`, `capabilities`, `monitor`, `version`, `help`, `exit`/`quit`. Echo, Micro-BFD и certificate доступны только вне shell. Выбрать endpoint/format до входа. Completion следует дереву shell; Ctrl-D завершает работу. Источник: [shell](../../cmd/gobfdctl/commands/shell.go).

Ошибка выводит `Error: …` в stderr и код 1. Локальная версия не сообщает сборку демона или готовность сессий.
