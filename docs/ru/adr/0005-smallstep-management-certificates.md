# ADR-0005: Внешняя Smallstep CA для renewal сертификатов управления

| Поле | Значение |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-23 |

## Контекст

Daemon читает PEM credentials на новом TLS handshake; clients — при создании transport. Existing-credential renewal сохраняет identity и key ownership. Certificate issuance не даёт Unix-UID authority либо GoBFD mutation permission.

## Решение

Используется external step-ca со standard-library-only adapter `internal/stepca`. CLI обновляет явно заданную existing identity и заменяет только public certificate. Он не обращается к GoBFD RPC endpoint и не меняет/печатает private key.

```bash
gobfdctl certificate renew --ca-url https://ca.example.net --ca-root /etc/gobfd/ca.pem --cert /etc/gobfd/client.pem --key /etc/gobfd/client-key.pem
```

Это параметризованный пример, а не настроенная CA. [Renewal model](https://smallstep.com/docs/step-ca/renewal/) аутентифицирует current certificate через mTLS. Restricted EKU требует clientAuth; server identity также сохраняет serverAuth. Renewal не добавляет отсутствующее purpose.

### Граница доверия

| Граница | Обязательное поведение | Source |
|:---|:---|:---|
| CA transport | Явные HTTPS origin/roots; без system-root, proxy, redirect, insecure TLS и bearer fallback | [client.go](../../../internal/stepca/client.go) |
| Limits | Cancellation, HTTP bound десять секунд, bounded headers/body/chain, certificate-only PEM | [client.go](../../../internal/stepca/client.go) |
| JSON | Отказ duplicate names и malformed UTF-8 на новой JSON v2 boundary | [certificate.go](../../../internal/stepca/certificate.go) |
| Leaf identity | Usable non-CA leaf, прежние key/subject/exact SAN/key usage/EKU; changed chain продлевает expiry | [certificate.go](../../../internal/stepca/certificate.go) |
| Chain | Проверка каждого EKU; unrestricted leaf поддерживает server/client; redundant fields совпадают | [certificate.go](../../../internal/stepca/certificate.go) |
| Files | Rooted no-follow regular inputs, protected key, trusted directory/owners, lock и rechecks | [files_linux.go](../../../internal/stepca/files_linux.go) |
| Commit | Same-directory atomic certificate replace, прежние mode/UID/GID, контроль close/cleanup errors | [files_linux.go](../../../internal/stepca/files_linux.go) |

Directory/inputs принадлежат root либо calling UID, включая root invocation. Linked inputs отклоняются. Bootstrap key разбирается один раз и перепроверяется перед commit. Identity/hash checks не дают compare-and-swap против privileged non-cooperative writer. Response TLS options не меняют daemon policy. TLS URI/DNS и Unix UID owner namespaces различаются.

## Последствия

Четыре binaries, YAML/protobuf и existing PEM tooling сохраняют совместимость. Identical validated chain означает unchanged; pre-commit failure сохраняет прежний certificate. Post-rename error сообщает committed state с uncertain durability, а не rollback.

Existing TLS connections сохраняют negotiated identity. Новый daemon handshake читает replacement; client требует transport recreation/restart. Passive CA revocation запрещает renewal, но не отзывает existing GoBFD connection. CRL/OCSP checks на новых handshakes не добавляются.

Initial enrollment, ACME/provisioners, automatic discovery/root download, unattended scheduling, reconnect и trust-root rollover остаются отдельными. Live CA не обнаруживается и не меняется автоматически. Renewal, principal provenance и unattended lifecycle требуют отдельной qualification.

### Датированная provider qualification

| Дата | Input и результат | Граница |
|:---|:---|:---|
| 2026-10-25 | [Isolated profile](../../../test/stepca/README.md), full upstream command, dependency-only overlay, disposable network-none CA и race-enabled daemon | Provider renewal slice; не production CA |
| 2026-10-28 | Profile `0.30.2+gobfd.qual.2`; пять renewal tests; 152 linked dependencies совпадают со scan; оба child exit zero | Security refresh зависимостей; без upstream source/plugin changes |

Profile сохраняет SPKI/private-key hash и увеличивает serial/expiry для server/operator/monitor renewals. Disabled renewal, passive revocation, expired identity и server-only EKU дают отказ без изменения файлов. Provider scan содержит ноль reachable/affected imported-package findings и три test-only module advisory dispositions из profile. Это историческая local qualification; runtime/tools manifests и final security gates отдельны.

## Источники

- [CLI](../../../cmd/gobfdctl/commands/certificate.go).
- [Adapter](../../../internal/stepca), [file/security regressions](../../../internal/stepca/files_linux_test.go).
- [Actual-provider harness и evidence contract](../../../test/stepca/README.md).
- [Management security](../15-security.md).
