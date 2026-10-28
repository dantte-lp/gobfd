# ADR-0005: External Smallstep CA renewal for management certificates

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-10-23 |

## Context

The daemon loads PEM credentials on each new TLS handshake; clients load them at transport construction. Existing-credential renewal must preserve identity and key ownership. Certificate issuance does not grant Unix-UID authority or GoBFD mutation permission.

## Decision

Use an external step-ca with the standard-library-only `internal/stepca` adapter. The CLI renews one explicit existing identity and replaces only its public certificate. It neither contacts the GoBFD RPC endpoint nor changes/prints the private key.

```bash
gobfdctl certificate renew --ca-url https://ca.example.net --ca-root /etc/gobfd/ca.pem --cert /etc/gobfd/client.pem --key /etc/gobfd/client-key.pem
```

This is a parameterized example, not a configured CA. The [renewal model](https://smallstep.com/docs/step-ca/renewal/) authenticates the current certificate through mTLS. Restricted EKU must allow clientAuth; a server identity must retain serverAuth as well. Renewal cannot add a missing purpose.

### Trust boundary

| Boundary | Required behavior | Source |
|:---|:---|:---|
| CA transport | Explicit HTTPS origin and roots; no system-root, proxy, redirect, insecure TLS or bearer fallback | [client.go](../../../internal/stepca/client.go) |
| Limits | Cancellation, ten-second HTTP bound, bounded headers/body/chain and strict certificate-only PEM | [client.go](../../../internal/stepca/client.go) |
| JSON | Reject duplicate names and malformed UTF-8 at the new JSON v2 boundary | [certificate.go](../../../internal/stepca/certificate.go) |
| Leaf identity | Currently usable non-CA leaf, same key/subject/exact SAN/key usage/EKU; changed chain advances expiry | [certificate.go](../../../internal/stepca/certificate.go) |
| Chain | Verify each declared EKU; unrestricted leaves must support both server/client; redundant fields agree | [certificate.go](../../../internal/stepca/certificate.go) |
| Files | Rooted no-follow regular inputs, protected key, trusted directory/owners, cooperative lock, rechecks | [files_linux.go](../../../internal/stepca/files_linux.go) |
| Commit | Same-directory atomic certificate replacement; preserve mode and UID/GID; check close/cleanup errors | [files_linux.go](../../../internal/stepca/files_linux.go) |

Directory and inputs belong to root or calling UID, including root invocation. Linked inputs fail closed. Parse the bootstrap key once and recheck it before commit. Identity/hash checks are not compare-and-swap against privileged non-cooperative writers. Response TLS options cannot change daemon policy. TLS URI/DNS and Unix UID owner namespaces remain separate.

## Consequences

Four binaries, YAML/protobuf and existing PEM tooling remain compatible. An identical validated chain is unchanged; pre-commit failure preserves the old certificate. Post-rename errors report committed state with uncertain durability, not rollback.

Existing TLS connections retain negotiated identity. A new daemon handshake reads the replacement; clients require transport recreation/restart. Passive CA revocation prevents renewal but does not revoke an existing GoBFD connection. GoBFD does not add CRL/OCSP checks on new handshakes.

Initial enrollment, ACME/provisioners, automatic discovery/root download, unattended scheduling, reconnect and trust-root rollover remain separate. No live CA is discovered or mutated. Renewal, principal provenance and unattended lifecycle require separate qualification.

### Dated provider qualification

| Date | Input and result | Limit |
|:---|:---|:---|
| 2026-10-25 | [Isolated profile](../../../test/stepca/README.md), full upstream command, dependency-only overlay, disposable network-none CA and race-enabled daemon | Bounded provider renewal slice; no production CA |
| 2026-10-28 | Profile `0.30.2+gobfd.qual.2`; five renewal tests; 152 linked dependencies match scan; both children exit zero | Dependency security refresh; no upstream source/plugin change |

The profile preserves SPKI/private-key hash and advances serial/expiry for server, operator and monitor renewals. Disabled renewal, passive revocation, expired identity and server-only EKU reject without file changes. The provider scan reports no reachable/affected imported-package findings, with three test-only module advisory dispositions retained in the profile. This is historical local qualification; runtime/tools manifests and final security gates remain separate.

## References

- [CLI](../../../cmd/gobfdctl/commands/certificate.go).
- [Adapter](../../../internal/stepca), [file/security regressions](../../../internal/stepca/files_linux_test.go).
- [Actual-provider harness and evidence contract](../../../test/stepca/README.md).
- [Management security](../15-security.md).
