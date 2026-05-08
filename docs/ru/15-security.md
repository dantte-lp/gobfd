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

RFC 9468 unsolicited BFD остаётся выключенным по умолчанию. При включении он
должен ограничиваться интерфейсом и source prefixes. Не включайте unsolicited
BFD в anonymous shared segment.

RFC 9747 unaffiliated Echo наследует требования RFC 5880/5881 и добавляет риск
spoofing для looped-back Echo-пакетов. Используйте BFD authentication для Echo,
если в развёртывании можно согласовать ключи.

## ConnectRPC API

Демон сейчас обслуживает ConnectRPC через `net/http` с h2c, чтобы gRPC-клиенты
могли подключаться без TLS на localhost. ConnectRPC использует обычные Go HTTP
handlers; TLS или mTLS должны быть реализованы будущей native-TLS настройкой
HTTP-сервера либо локальным sidecar/reverse proxy уже сейчас.

Production-правила:

- по умолчанию привязывайте `grpc.addr` к `127.0.0.1:50051` или локальному
  network namespace;
- если API должен пересекать границу хоста, терминируйте mTLS в локальном
  proxy и публикуйте в сеть только proxy;
- считайте `AddSession`, `DeleteSession` и будущие transport-specific create
  RPC write-sensitive операциями управления сетью;
- не публикуйте control API в недоверенную management-сеть.

## Интеграция с GoBGP

GoBFD подключается к GoBGP через `gobgp.addr`. Plaintext допустим только для
loopback или доверенной management-сети. Для удалённых или non-loopback
endpoints включайте `gobgp.tls.enabled`.

Текущий модуль GoBGP имеет allowlisted advisory `GO-2026-4736`. Митигация:
держать GoBGP API на localhost или в доверенной management-сети, пока upstream
не выпустит исправленную версию. Запись allowlist в `scripts/vuln-audit.go`
содержит owner, expiry, reason и mitigation; после expiry vulnerability gate
падает.

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
