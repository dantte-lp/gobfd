# Командная строка

`gobfdctl` обращается к API управления демона. Команды, флаги и форматирование определены в [исходниках CLI](../../cmd/gobfdctl/commands/root.go), сообщения — в [protobuf-схеме](../../api/bfd/v1/bfd.proto). Успешный list доказывает доступ к управлению, а не исправность пересылки.

<a id="глобальные-flags"></a>

## Глобальные флаги

| Флаг | Значение | Условие |
| :--- | :--- | :--- |
| `--addr` | `unix:///run/gobfd/gobfd.sock` | Unix URL, `host:port`, `http://` или `https://` |
| `--tls-ca` | пусто | CA PEM сертификата демона |
| `--tls-cert` | пусто | Сертификат клиента PEM |
| `--tls-key` | пусто | Закрытый ключ клиента PEM |
| `--format` | `table` | `table`, `json`, `yaml` для структурированного вывода |

TLS-файлы требуют HTTPS. Политика сервера определяет право записи. CLI не читает переопределения `GOBFD_*` демона. Источник: [транспорт конечной точки](../../internal/apiclient/endpoint.go).

```sh title="management-readback.sh"
gobfdctl session list
gobfdctl --format json capabilities
gobfdctl --addr https://bfd.example.net:50052 --tls-ca /etc/gobfd/tls/ca.pem --tls-cert /etc/gobfd/tls/client.pem --tls-key /etc/gobfd/tls/client.key session list
```

## Команды

| Команда | Аргументы/флаги | Результат |
| :--- | :--- | :--- |
| `session list` | нет | Все Control-сессии |
| `session show IDENTIFIER` | IP соседа или дискриминатор uint32 | Одна сессия Control |
| `session add` | Флаги ниже | Создать заявку на владение Control |
| `session delete DISCRIMINATOR` | uint32 | Освободить заявку Control вызывающей стороны |
| `echo list` | нет | Все Echo-сессии |
| `echo add` | Флаги ниже | Создать заявку Echo предварительной реализации |
| `echo delete DISCRIMINATOR` | uint32 | Освободить заявку Echo вызывающей стороны |
| `micro list` | нет | Группы Micro-BFD |
| `micro add` | Флаги ниже | Создать заявку LAG предварительной реализации |
| `micro delete LAG` | Интерфейс LAG | Освободить заявку группы |
| `capabilities` | нет | Каталог предварительных возможностей демона |
| `monitor` | `--current`, по умолчанию false | Поток событий Control и Echo |
| `certificate renew` | Четыре обязательных флага ниже | Обновить сертификат независимо от RPC демона |
| `version` | нет | Локальная сборка CLI |
| `shell` | нет | Интерактивное подмножество |
| `help` | Необязательный путь команды | Справка |

Использовать дискриминатор, когда адрес разделяют несколько сессий. Delete освобождает заявку; другой владелец может сохранить физическую сессию. Заявки конфигурации не принадлежат субъекту доступа CLI. См. [авторизацию](15-security.md#api-управления).

## Создание Control

| Флаг | Значение | Условие |
| :--- | :--- | :--- |
| `--peer` | обязательно | IP соседа |
| `--local`, `--interface` | пусто | Локальный IP и привязка устройства |
| `--type` | `single-hop` | `single-hop` или `multi-hop` |
| `--tx-interval`, `--rx-interval` | `1s` каждый | Строки длительности; RX допускает ноль |
| `--detect-mult` | `3` | 1–255 после проверки сервера |
| `--padded-pdu-size` | `0` | Целевой размер; ноль отключает дополнение запроса |
| `--auth-type` | `none` | `none`, `simple-password`, `keyed-md5`, `meticulous-keyed-md5`, `keyed-sha1`, `meticulous-keyed-sha1` |
| `--auth-key-id` | `0` | 0–255 |
| `--auth-secret` | пусто | Обязательно при включённой аутентификации |

Нормализация аутентификации принимает разный регистр и подчёркивания/дефисы. Материал ключа при отключённой аутентификации отклоняется. Флаг `--multihop-max-hops` и поле политики числа скачков в `AddSessionRequest` отсутствуют: нестандартная политика задаётся только YAML. Проверка сервера, допуск привязки и авторизация сохраняются.

```sh title="control-session.sh"
gobfdctl session add --peer 192.0.2.2 --local 192.0.2.1 --interface eth0 --tx-interval 300ms --rx-interval 300ms --detect-mult 3
gobfdctl session show 192.0.2.2
gobfdctl monitor --current
```

Заявки среды исполнения не сохраняются в YAML. Значения флагов аутентификации могут попасть в аргументы процесса или историю команд; для производственной среды использовать защищённый YAML или контролируемый API-клиент. Источник: [команды Control](../../cmd/gobfdctl/commands/session.go).

## Создание Echo и Micro-BFD

| Команда | Флаги и значения по умолчанию | Ограничение |
| :--- | :--- | :--- |
| `echo add` | `--peer` обязателен; `--local`/`--interface` пусты; `--tx-interval=1s`, `--detect-mult=3` | Предварительный профиль включён; TX положительный; множитель 1–255 |
| `micro add` | `--lag`/`--members`/`--peer` обязательны; `--local` пуст; `--tx-interval=1s`, `--rx-interval=1s`, `--detect-mult=3`, `--min-active=1` | Предварительный профиль включён; минимум активных 1…число участников |

`--members` принимает интерфейсы через запятую. Создание Echo/Micro выводит строку статуса; `--format` действует на list. Создание не включает профиль и не настраивает исполнитель. Источники: [Echo](../../cmd/gobfdctl/commands/echo.go), [Micro-BFD](../../cmd/gobfdctl/commands/micro.go).

<a id="capabilities-и-события"></a>

## Возможности и события

`capabilities` возвращает поля kind (тип), RFC, maturity (зрелость), enablement (включение) и readiness (локальная готовность). Каталог только для чтения доступен при неготовой среде пересылки согласно транспортной политике. Клиент не подставляет резервное значение. Отключённый профиль — `not_ready`; недоступная информация среды исполнения — `unknown`. Включённый оверлей без соседей не владеет реализацией оверлея. Echo, используемый только через API, может требовать допущенную заявку для создания слушающего сокета приёма. Готовность не означает исправность соседа или квалификацию RFC; каталог не заменяет предварительную проверку допуска.

`monitor --current` выдаёт снимки Control/Echo, затем события; `session list/show` остаются только для Control, `echo list` читает Echo отдельно. JSON/YAML — последовательность событий, не база сессий. Контроллер должен выполнять сверку после повторного подключения; см. [API](../../api/bfd/v1/bfd.proto), [объединённый снимок Watch](../../internal/bfd/manager_session.go).

## Обновление сертификата

`certificate renew` обращается к внешнему CA существующей идентичности, проверяет ответ и атомарно заменяет только сертификат. Первичная выдача, ротация ключа и планировщик в область команды не входят.

```sh
gobfdctl certificate renew --ca-url https://ca.example.net --ca-root /etc/gobfd/tls/roots.pem --cert /etc/gobfd/tls/client.pem --key /etc/gobfd/tls/client.key
```

Все четыре флага обязательны. Вывод: `certificate renewed` либо `certificate unchanged`. Ошибка после замены может означать уже применённую операцию; проверить сертификат перед повтором. Обновление или отзыв не удаляет API-доступ немедленно. Источники: [команда](../../cmd/gobfdctl/commands/certificate.go), [клиент](../../internal/stepca/client.go), [ADR-0005](adr/0005-smallstep-management-certificates.md).

## Интерактивная оболочка и завершение

Оболочка поддерживает `session`, `capabilities`, `monitor`, `version`, `help`, `exit`/`quit`. Echo, Micro-BFD и certificate доступны только вне оболочки. Выбрать конечную точку и формат до входа. Автодополнение следует дереву команд оболочки; Ctrl-D завершает работу. Источник: [оболочка](../../cmd/gobfdctl/commands/shell.go).

Ошибка выводит `Error: …` в stderr и код 1. Локальная версия не сообщает сборку демона или готовность сессий.
