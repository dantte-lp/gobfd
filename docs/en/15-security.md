# Security

[Russian translation](../ru/15-security.md).

GoBFD accepts untrusted UDP traffic, exposes a management API and unauthenticated metrics, and optionally controls routing/LAG membership. Packet authentication, management identity and data-plane ownership are separate boundaries.

## Management API

The default transport is `/run/gobfd/gobfd.sock` with mode `0660`. Unix principals use kernel-reported `SO_PEERCRED` UID. The packaged example selects `gobfd-admin`; daemon group access can expose configuration secrets. Grant operators only the admin group.

| Transport | Read | Mutate | Principal |
|:---|:---|:---|:---|
| Unix socket | Socket permission | Recognized peer credentials | Kernel UID |
| Plaintext loopback TCP | Anonymous | Denied by default | None |
| Unsafe direct plaintext loopback | Anonymous | Allowed; management health unready | Shared local owner |
| mTLS | Verified client certificate | Recognized URI/DNS identity, subject to writer allowlist | First URI SAN, otherwise `dns:` first DNS SAN |

Non-loopback/wildcard plaintext listeners fail startup. Unsafe mutation admission requires both direct TCP endpoints to be loopback and no TLS; forwarded headers/URLs cannot grant it. Local proxies remain local processes. A CA-verified certificate without URI/DNS SAN cannot mutate, even in unsafe mode.

With nonempty `grpc.mutating_identities`, unlisted recognized mTLS identities are read-only. Unix, TLS URI and TLS DNS owner namespaces differ; a TLS URI containing `uid:0` never becomes Unix root. List/Get/Watch observe daemon-wide state. Control, Echo and Micro claims are principal-scoped; deleting another principal's claim is denied. Compatible claims can share a physical session until the last claim is released.

Sources: [management transports](../../cmd/gobfd/management_socket.go), [principal/interceptors](../../internal/server), [configuration](03-configuration.md), [Echo claims ADR](adr/0009-echo-principal-claims.md).

## Certificates and renewal

Management mTLS requires server certificate, private key and client CA files. Files are reread on every handshake. GoBFD checks certificate chains/identity but performs no CRL/OCSP revocation check. CA revocation prevents renewal according to CA policy; it does not cancel an established API connection or immediately deny a fresh handshake. Use short-lived credentials and an explicit access-removal procedure.

`gobfdctl certificate renew` is an independent outbound CA client. It requires an HTTPS origin without credentials/path/query, explicit roots and an existing certificate/key. Response/chain bounds, returned identity validation and atomic certificate replacement are owned by [the renewal implementation](../../internal/stepca/client.go). It does not enroll identities, rotate keys, schedule renewal or change GoBFD authorization. The [disposable actual-provider profile](../../test/stepca/README.md) qualifies bounded renewal/negative cases, not production CA operations or all provider dependencies. See [ADR-0005](adr/0005-smallstep-management-certificates.md).

Stop renewal on a validation or file-ownership error. An identical validated chain leaves the certificate unchanged, but another renewal can issue a different chain; repetition is not a general rollback. If `ErrCommitted` is reported, inspect the current certificate before recovery: replacement has occurred and the old file must not be assumed present. Preserve protected accepted credentials and use an independently authorized certificate/access recovery procedure; do not change trust roots or private keys as an implicit retry. Sources: [file commit](../../internal/stepca/files_linux.go), [CLI result handling](../../cmd/gobfdctl/commands/certificate.go).

## Configuration paths

Local startup and reload use the caller-selected `-config PATH`. Absolute paths and relative `..` components are supported. The [loader](../../internal/config/config.go) opens `filepath.Dir(PATH)`, following directory symlinks, then resolves the basename through `os.Root`. Relative file symlinks that stay beneath the opened directory are supported, including projected-volume links; escaping and absolute file symlinks are rejected. The caller selects the root; no independent configuration-directory allowlist is imposed.

YAML parsing and configuration validation follow the file read and do not establish a file-access boundary. Protect the selected file and directories with filesystem permissions. [Temporary-file tests](../../internal/config/config_test.go) distinguish supported paths, final-name symlink confinement and YAML errors.

## BFD packet boundary

| Profile | Exposure | Required deployment decision |
|:---|:---|:---|
| Single-hop Control | UDP 3784, TTL/Hop Limit 255 | Match peer/local/interface scope; enable shared-key auth where supported |
| Multi-hop Control | UDP 4784; configured receive hop policy | Authenticate untrusted/routed paths |
| Echo preview | UDP 3785; no auth setting | Keep disabled on untrusted segments |
| Micro-BFD preview | UDP 6784, LAG-member ownership | Qualify wire identity and native actuator independently |
| VXLAN/Geneve preview | UDP 4789/6081 and explicit Format A identity | Isolate lab; verify existing port/dataplane ownership |
| Unsolicited preview | Dynamic session admission | Restrict known participants, receiving subnet/prefixes, count and auth |

References: [RFC 5880 section 9](https://www.rfc-editor.org/rfc/rfc5880.html#section-9), [RFC 5881 section 5](https://www.rfc-editor.org/rfc/rfc5881.html#section-5), [RFC 5883 section 6](https://www.rfc-editor.org/rfc/rfc5883.html#section-6), [RFC 9468 section 6](https://www.rfc-editor.org/rfc/rfc9468.html#section-6), [RFC 9747](https://www.rfc-editor.org/rfc/rfc9747.html).

Unsolicited admission requires `max_sessions >= 1`, the receiving local subnet, configured prefix restrictions and TTL 255. The global limiter admits 10 sessions/s, burst 20; source spoofing can still delay legitimate participants. Only Meticulous Keyed SHA1 is accepted when unsolicited authentication is enabled. Source: [admission](../../internal/bfd/unsolicited.go).

Control supports Simple Password, keyed/meticulous MD5 and keyed/meticulous SHA1. These are protocol compatibility mechanisms, not modern transport encryption. Store secrets in protected read-only configuration or controlled API provisioning; never put real values in Git, captures, logs or command history. Reload rejects auth changes on retained identity. Coordinate peer key changes with restart/recreation and expected interruption; multiple internal keys alone do not establish qualified live rotation. See [ADR-0006](adr/0006-session-preserving-reload-policy.md).

## Routing and actuator credentials

GoBGP actuation requires compatible v4 API and one BFD owner per neighbor. `gobgp.tls.enabled` enables server-authenticated TLS; configured CA roots/name verify the endpoint. The GoBGP client has no client-certificate configuration. Non-loopback plaintext logs a warning; this warning is not an access-control boundary. Keep the API loopback or protect remote access with explicit TLS/network controls. Sources: [client](../../internal/gobgp/client.go), [startup handler](../../cmd/gobfd/handlers.go).

Micro-BFD enforcement changes native member state. Disabled/dry-run modes do not qualify enforcement. Select the real kernel/OVS/NetworkManager owner and reviewed permissions before enabling `enforce`. The optional kernel-bond systemd drop-in adds broad `CAP_DAC_OVERRIDE` and retains native-host qualification gaps. Source: [drop-in](../../deployments/systemd/gobfd-kernel-bond.conf).

## Runtime and diagnostics

1. Retain only declared network capabilities; avoid privileged containers.
2. Mount configuration/key material read-only and keep the management socket filesystem namespace explicit.
3. Restrict metrics/agent-check listeners; neither provides authentication.
4. Review host networking as a shared host-local trust boundary.
5. Keep Podman sockets in development, outside the application runtime.
6. Treat traces, profiles and goroutine details as sensitive operational artifacts.

`grpc.flight_recorder` is off by default. Its management-only endpoint requires a Unix principal or mTLS identity authorized by the writer policy; anonymous unsafe-mode callers remain denied. Metrics exposes no debug handlers.

## Dependency and release gates

The [risk register](reference/dependency-risk.md) and [Go-owned audit](../../scripts/vuln-audit.go) define exact allowed findings, owners, expiry and removal criteria. Runtime/tool-module raw reports and separate SBOMs remain separate evidence. A module import or historical exception does not prove current non-reachability or advisory state.

```sh
make verify
make vulncheck
make lint-commit MSG='docs(security): define access boundaries'
```

Run through the repository development environment. Unallowed findings, expired/stale exceptions and scanner/parser failures block acceptance. The audit retains all parsed unallowed findings; allowlist failures return 2, otherwise unallowed findings return 1. A green local gate does not qualify vendor devices, production privileges or a published release.
