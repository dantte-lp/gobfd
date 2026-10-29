# Безопасность

GoBFD обрабатывает недоверенный UDP, предоставляет API управления и открытые метрики, опционально управляет routing/LAG membership. Packet auth, management identity и dataplane ownership — отдельные границы.

## API управления

Default transport — `/run/gobfd/gobfd.sock` с правами `0660`. Unix principal использует UID ядра `SO_PEERCRED`. Пакетный пример задаёт `gobfd-admin`; группа демона читает secrets конфигурации. Давать операторам только admin group.

| Транспорт | Чтение | Изменение | Principal |
| :--- | :--- | :--- | :--- |
| Unix socket | Права сокета | Распознанные peer credentials | UID ядра |
| Открытый loopback TCP | Анонимно | Запрещено по умолчанию | Нет |
| Unsafe direct plaintext loopback | Анонимно | Разрешено; management health unready | Общий local owner |
| mTLS | Проверенный client certificate | URI/DNS identity с учётом writer allowlist | Первый URI SAN, иначе `dns:` первый DNS SAN |

Внешний/wildcard открытый listener отклоняется при запуске. Unsafe mutation требует оба прямых TCP endpoints на loopback без TLS; forwarded headers/URL не дают прав. Local proxy остаётся local process. CA-verified certificate без URI/DNS SAN не допускается к записи даже в unsafe mode.

Непустой `grpc.mutating_identities` оставляет неперечисленные mTLS identities только для чтения. Пространства Unix, TLS URI, TLS DNS различаются; URI `uid:0` не становится Unix root. List/Get/Watch наблюдают весь daemon. Control/Echo/Micro claims принадлежат principal; чужой delete запрещён. Совместимые claims разделяют физическую сессию до последнего release.

Источники: [management transport](../../cmd/gobfd/management_socket.go), [principal/interceptors](../../internal/server), [конфигурация](03-configuration.md), [Echo ADR](adr/0009-echo-principal-claims.md).

## Сертификаты и renewal

Management mTLS требует server certificate, private key, client CA. Файлы перечитываются при каждом handshake. GoBFD проверяет chain/identity без CRL/OCSP. CA revocation запрещает renewal согласно CA policy, но не отменяет текущее API connection и не даёт немедленного отказа нового handshake. Использовать короткоживущие credentials и явную процедуру снятия доступа.

`gobfdctl certificate renew` — независимый outbound CA client. Требуются HTTPS origin без credentials/path/query, explicit roots, текущий certificate/key. Response/chain bounds, проверка возвращённой identity и atomic certificate replacement принадлежат [реализации](../../internal/stepca/client.go). Команда не выполняет enrollment, key rotation, scheduling и не меняет authorization GoBFD. [Disposable actual-provider profile](../../test/stepca/README.md) квалифицирует ограниченный renewal/negative cases, не production CA и все dependencies provider. См. [ADR-0005](adr/0005-smallstep-management-certificates.md).

## Пути к конфигурации

Локальный запуск и перечитывание используют выбранный вызывающей стороной `-config PATH`. Поддерживаются абсолютные пути и относительные компоненты `..`. [Загрузчик](../../internal/config/config.go) открывает `filepath.Dir(PATH)`, следуя символическим ссылкам каталогов, затем разрешает последнее имя через `os.Root`. Поддерживаются относительные файловые ссылки внутри открытого каталога, включая ссылки проецируемых томов; ссылки за его пределы и абсолютные файловые ссылки отклоняются. Корень выбирает вызывающая сторона; отдельного списка разрешённых каталогов конфигурации нет.

Разбор YAML и проверка конфигурации выполняются после чтения файла и не устанавливают границу файлового доступа. Защитить выбранный файл и каталоги правами файловой системы. [Тесты с временными файлами](../../internal/config/config_test.go) разделяют поддерживаемые пути, ограничение последнего имени символической ссылки и ошибки YAML.

## Граница BFD packets

| Профиль | Поверхность | Решение deployment |
| :--- | :--- | :--- |
| Single-hop Control | UDP 3784, TTL/Hop Limit 255 | Согласовать peer/local/interface; shared-key auth при поддержке |
| Multi-hop Control | UDP 4784, настроенная hop policy | Аутентифицировать недоверенный/routed path |
| Echo preview | UDP 3785, нет auth setting | Отключить на недоверенных сегментах |
| Micro-BFD preview | UDP 6784, LAG member ownership | Независимо проверить wire identity/native actuator |
| VXLAN/Geneve preview | UDP 4789/6081, explicit Format A identity | Изолировать lab; проверить владельца порта/dataplane |
| Unsolicited preview | Динамический допуск сессий | Ограничить participants, subnet/prefixes, count и auth |

Ссылки: [RFC 5880 раздел 9](https://www.rfc-editor.org/rfc/rfc5880.html#section-9), [RFC 5881 раздел 5](https://www.rfc-editor.org/rfc/rfc5881.html#section-5), [RFC 5883 раздел 6](https://www.rfc-editor.org/rfc/rfc5883.html#section-6), [RFC 9468 раздел 6](https://www.rfc-editor.org/rfc/rfc9468.html#section-6), [RFC 9747](https://www.rfc-editor.org/rfc/rfc9747.html).

Unsolicited требует `max_sessions >= 1`, подсеть принимающего local address, prefix restrictions и TTL 255. Общий limiter — 10 сессий/с, burst 20; spoofing может задержать легитимный допуск. При включённой unsolicited auth принимается только Meticulous Keyed SHA1. Источник: [допуск](../../internal/bfd/unsolicited.go).

Control поддерживает Simple Password, keyed/meticulous MD5 и keyed/meticulous SHA1. Это механизмы совместимости протокола, не современное transport encryption. Хранить secrets в защищённом read-only config или контролируемом API provisioning; не помещать значения в Git, captures, logs, history. Reload отклоняет auth changes сохранённой identity. Согласовать изменение ключей с restart/recreation и перерывом; несколько внутренних ключей не доказывают live rotation. См. [ADR-0006](adr/0006-session-preserving-reload-policy.md).

## Routing и actuator credentials

GoBGP actuation требует совместимый v4 API и одного BFD owner на neighbor. `gobgp.tls.enabled` включает server-authenticated TLS; CA/name проверяют endpoint. Client-certificate config отсутствует. Внешний plaintext выдаёт warning; warning не ограничивает доступ. Оставить API на loopback либо защитить remote TLS/network controls. Источники: [client](../../internal/gobgp/client.go), [startup handler](../../cmd/gobfd/handlers.go).

Micro-BFD enforce меняет native member state. Disabled/dry-run не квалифицируют enforcement. До `enforce` выбрать фактического kernel/OVS/NetworkManager owner и проверенные permissions. Kernel-bond drop-in добавляет широкий `CAP_DAC_OVERRIDE`; native-host qualification остаётся отдельной. Источник: [drop-in](../../deployments/systemd/gobfd-kernel-bond.conf).

## Runtime и диагностика

1. Сохранить только заявленные capabilities; не использовать privileged containers.
2. Монтировать config/keys read-only; явно учитывать filesystem namespace management socket.
3. Ограничить metrics/agent-check listeners: аутентификации нет.
4. Рассматривать host networking как общую host-local trust boundary.
5. Оставить Podman sockets в development вне application runtime.
6. Считать traces, profiles, goroutine details чувствительными артефактами.

`grpc.flight_recorder` по умолчанию отключён. Management-only endpoint требует Unix principal или mTLS identity, разрешённую writer policy; anonymous unsafe callers запрещены. Metrics не содержит debug handlers.

## Dependency и release gates

[Реестр рисков](reference/dependency-risk.md) и [Go-owned audit](../../scripts/vuln-audit.go) задают exact findings, owners, expiry и removal criteria. Raw reports/SBOM runtime и tools хранятся отдельно. Import или историческое исключение не доказывают актуальную недостижимость либо состояние advisory.

```sh title="security-gates.sh"
make verify
make vulncheck
make lint-commit MSG='docs(security): define access boundaries'
```

Выполнять через repository development environment. Unallowed findings, expired/stale exceptions и scanner/parser failure блокируют приёмку. Audit сохраняет все разобранные unallowed findings; allowlist failures дают код 2, остальные unallowed — 1. Успешный локальный gate не квалифицирует vendor devices, production privileges или опубликованный release.
