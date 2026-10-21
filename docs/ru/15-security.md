# Политика безопасности

![Scorecard](https://img.shields.io/badge/OpenSSF-Scorecard-1a73e8?style=for-the-badge)
![CodeQL](https://img.shields.io/badge/CodeQL-SAST-34a853?style=for-the-badge)
![govulncheck](https://img.shields.io/badge/govulncheck-Required-ea4335?style=for-the-badge)
![Capabilities](https://img.shields.io/badge/CAP__NET__RAW-Required-ffc107?style=for-the-badge)

> Production baseline безопасности для развёртываний GoBFD. Каждый пункт -- настройка демона, граница развёртывания либо проверка.

---

## Область

У GoBFD четыре поверхности безопасности:

- обработка BFD-пакетов на UDP-портах 3784, 3785, 4784, 6784, 4789 и 6081;
- ConnectRPC control API, опубликованный через `grpc.addr`;
- опциональная интеграция с GoBGP gRPC API;
- привилегии контейнера, systemd и Kubernetes для raw sockets и сетевой
  привязки к интерфейсам.

## Требования RFC

Security considerations из RFC 5880 и RFC 5881 применяются ко всем BFD Control
и Echo-пакетам. Используйте аутентификацию везде, где оба пира её
поддерживают, особенно в multi-access сегментах, tunnel monitoring и любой
сети, где возможен spoofing пакетов.

RFC 9468 unsolicited BFD остаётся выключенным по умолчанию. При включении GoBFD
допускает сессию только из подсети локального адреса приёма, в пределах
`allowed_prefixes`, ниже `max_sessions` (обязателен, >= 1), не чаще 10 новых
сессий в секунду и только из single-hop пакетов с TTL 255. Лимит скорости
глобальный: хост в сегменте, подделывающий source, может задержать допуск
легитимных пиров; RFC 9468, раздел 6.1, допускает режим только там, где
участники не анонимны. Задавайте `unsolicited.auth` (Meticulous Keyed SHA1),
где стороны могут согласовать ключ: тогда пакет, создающий сессию,
проверяется заранее. Не включайте unsolicited BFD в anonymous shared
segment.

RFC 9747 unaffiliated Echo наследует требования RFC 5880/5881 и добавляет риск
spoofing для looped-back Echo-пакетов. Используйте BFD authentication для Echo,
если в развёртывании можно согласовать ключи.

## ConnectRPC API

API управления может создавать и удалять сессии. Его граница следует
[решению о транспорте](../superpowers/specs/2026-10-15-management-transport-decision.md)
(`gobfd-330`):

- по умолчанию демон обслуживает только Unix-сокет `/run/gobfd/gobfd.sock`
  (права 0660). Конфигурация из пакета задаёт группу `gobfd-admin`; операторы
  вступают в неё, а не в `gobfd`, которая читает `/etc/gobfd` с BFD-секретами.
  Каждый запрос несёт UID peer от ядра как принципал (`principal=uid:<n>` в
  логах RPC);
- `grpc.addr` добавляет необязательный TCP-listener. Plaintext TCP только на
  loopback; с non-loopback или wildcard адресом без TLS демон не запускается.
  Анонимные клиенты на loopback могут читать, но не изменять, если не задан
  `grpc.unsafe_loopback_mutations`; этот режим логируется при запуске и
  переводит health check `gobfd.management` в `NOT_SERVING`;
- `grpc.tls` превращает TCP-listener в mTLS (`RequireAndVerifyClientCert`) и
  обязателен для non-loopback адреса. Принципал — URI SAN (SPIFFE) или DNS SAN
  сертификата клиента. Файлы читаются при каждом handshake; отзыв не
  проверяется, поэтому используйте короткоживущие сертификаты.

Production-правила:

- оставляйте сокет по умолчанию и выдавайте доступ через `gobfd-admin`;
- для доступа к API через границу хоста используйте mTLS, plaintext не
  публикуйте;
- считайте `AddSession`, `DeleteSession`, `AddEchoSession`,
  `DeleteEchoSession`, `AddMicroBFDGroup` и `DeleteMicroBFDGroup`
  write-sensitive операциями управления сетью;
- не включайте `grpc.unsafe_loopback_mutations` на общих хостах и с
  Kubernetes `hostNetwork`, где loopback доступен всем процессам хоста.

Claims сессий API принадлежат принципалу вызвавшего: один принципал не может
освободить сессию другого (`PermissionDenied`, с записью в лог), а
`grpc.mutating_identities` делает неперечисленные mTLS-идентичности
доступными только для чтения. Объекты Echo и Micro-BFD через API пока не
привязаны к принципалам.

Дамп flight recorder (`grpc.flight_recorder`, по умолчанию выключен) доступен
только на сервере управления и только вызывающим с принципалом; listener
метрик не обслуживает endpoint отладки.

## Интеграция с GoBGP

GoBFD подключается к GoBGP через `gobgp.addr`. Plaintext допустим только для
loopback или доверенной management-сети. Для удалённых или non-loopback
endpoints включайте `gobgp.tls.enabled`.

Текущий модуль GoBGP имеет allowlisted advisory `GO-2026-4736`. Upstream
исправил его в v4.4.0 (GHSA-4p9m-8gc4-rw2h), и закреплённая v4.10.0 содержит
исправление, но база уязвимостей Go не указывает исправленную версию v4.
Advisory относится к `github.com/osrg/gobgp/v4/pkg/server`; GoBFD импортирует
только пакет `api`, поэтому govulncheck сообщает находку на уровне модуля.
Решение мейнтейнера для stable v1 допускает эту module-only запись, пока
сканирование на уровне символов чистое; запись удаляется, когда база укажет
исправленную версию. Держите GoBGP API на localhost или в доверенной
management-сети. Запись allowlist в `scripts/vuln-audit.go` содержит
названного owner, expiry, reason и mitigation; после expiry vulnerability gate
падает.

CI проверяет runtime manifest и manifest изолированного tool-модуля раздельно.
Raw runtime/tools vulnerability reports и их раздельные CycloneDX SBOM
сохраняются в артефакте `dependency-security-reports`, поэтому tool-only
findings не приписываются поставляемому daemon.

## Секреты

RFC 5880 auth secrets можно передавать через YAML или gRPC `AddSession`.
Production-развёртывания должны монтировать YAML secrets read-only и не
коммитить реальные ключи. Ротация выполняется через новый конфиг и reload в
maintenance window; dynamic key rotation пока не реализована.

## Граница Container And Kubernetes

GoBFD нужны сетевые привилегии для raw sockets, socket buffer tuning и
interface binding:

- предпочитайте `NET_RAW` и `NET_ADMIN` вместо privileged containers;
- используйте `hostNetwork` только когда BFD-пиры достижимы из host network
  namespace, и документируйте это допущение;
- монтируйте конфиг и key material read-only;
- Podman socket нужен только dev-контейнеру и не является production runtime
  dependency.

## Проверки

Перед релизом или production rollout выполняйте:

```bash
make verify
make vulncheck
make lint-commit MSG='docs(security): define production hardening policy'
```

Все команды должны идти через Podman targets проекта. Vulnerability audit может
показывать allowlisted findings, но любая unallowlisted или expired finding
блокирует релиз.
