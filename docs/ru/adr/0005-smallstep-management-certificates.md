# ADR-0005: Внешний Smallstep CA для обновления management-сертификатов

| Поле | Значение |
|---|---|
| Status | Accepted |
| Date | 2026-10-23 |

## Контекст

Management server читает PEM при каждом новом handshake; клиенты загружают
credentials при создании transport. Внешний step-ca
поддерживает `POST /renew` с аутентификацией существующим сертификатом.
Выдача сертификата не определяет права GoBFD: TLS URI не получает привилегии
Unix UID и не разделяет его owner namespace.

Первый срез обновляет существующие credentials. Адрес CA, root и issuance
policy не заданы; адаптер использует только явные локальные параметры
оператора и не изменяет работающую CA.

## Решение

Использовать внешний step-ca и адаптер `internal/stepca` только на стандартной
библиотеке. Команда `gobfdctl certificate renew --ca-url <https-origin>
--ca-root <file> --cert <file> --key <file>` не обращается к RPC GoBFD.
Она заменяет только публичный сертификат; ключ не изменяется и не выводится.

Протокольная база — исходники и release notes `smallstep/certificates`
v0.30.2. Это не новая Go-зависимость и не квалифицированный образ CA.
Встроенная CA, SDK dependency graph и запуск step CLI не используются.

Граница доверия:

- Явные HTTPS origin и root; без system-root, proxy, redirect, insecure TLS
  и bearer-token fallback. Private CA адресуется из конфигурации оператора,
  URL не поступает из RPC.
- Context cancellation, конечный HTTP timeout, лимиты ответа и цепочки,
  strict JSON с отклонением duplicate names и invalid UTF-8, certificate-only
  PEM. `encoding/json/v2` Go 1.27 применяется только к новому protocol boundary;
  существующие JSON v1 consumers не меняются.
- Проверенный действующий non-CA leaf, прежние public key, subject, точный SAN
  и профиль key usage/EKU. Изменённый сертификат продлевает срок; идентичная
  проверенная цепочка возвращает unchanged. Дублирующие поля ответа согласованы;
  цепочка проверяется для каждого объявленного EKU отдельно. Leaf без ограничений
  должен оставаться пригодным для serverAuth и clientAuth.
  Ответ CA не меняет TLS policy демона.
- Защищённый regular private-key input, rooted no-follow доступ и атомарная
  замена сертификата в том же каталоге с сохранением mode и UID/GID.
  Требуются доверенный каталог и cooperative renewal lock. Каталог и все входы
  проверяются повторно. Владельцы — root или UID вызывающего процесса, в том
  числе при запуске от root. Повторные проверки identity/hash выявляют изменения,
  но не гарантируют compare-and-swap против
  privileged non-cooperative writer. Linked inputs отклоняются. Bootstrap key
  разбирается один раз и повторно проверяется перед commit, не перезаписывается.
  Временные файлы и ошибки закрытия учитываются.
- Trusted transport provenance в авторизации; прежние URI/DNS role identities
  сохраняются, TLS URI/DNS и Unix UID получают разные owner namespaces.

## Последствия

Четыре binaries, YAML и protobuf остаются совместимыми. Обычный PEM mode и
внешние средства обновления сохраняются. Действующее TLS-соединение использует
прежнюю identity до reconnect; новый handshake читает заменённый сертификат.
Клиентам требуется пересоздание transport или restart.
Повтор команды не создаёт BFD session или новый ключ. Идентичная замена —
no-op; ошибка до атомарного commit сохраняет прежний файл. Ошибка после rename
сообщает committed result с неопределённой durability, а не обещает rollback.

Первичная выдача, ACME/provisioners, unattended scheduler, recycling соединений
и root rollover требуют отдельных решений. Обнаружение CA и загрузка root
по сети не вводятся. Локальный TLS fixture не заменяет квалификацию step-ca.
План — Beads `gobfd-qj0.8.2.3.7`; provenance — `gobfd-qj0.8.2.3.5`.

## Источники

- [step-ca renewal](https://smallstep.com/docs/step-ca/renewal/)
- [Renew API](https://github.com/smallstep/certificates/blob/v0.30.2/api/renew.go)
- [Response schema](https://github.com/smallstep/certificates/blob/v0.30.2/api/sign.go)
- [Release notes](https://github.com/smallstep/certificates/releases/tag/v0.30.2)
- [Go specification](https://go.dev/ref/spec)
- [Go 1.27 release notes](https://go.dev/doc/go1.27)
