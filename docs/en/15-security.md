# Security Policy

![Scorecard](https://img.shields.io/badge/OpenSSF-Scorecard-1a73e8?style=for-the-badge)
![CodeQL](https://img.shields.io/badge/CodeQL-SAST-34a853?style=for-the-badge)
![govulncheck](https://img.shields.io/badge/govulncheck-Required-ea4335?style=for-the-badge)
![Capabilities](https://img.shields.io/badge/CAP__NET__RAW-Required-ffc107?style=for-the-badge)

> Production security baseline for GoBFD deployments. Each item is a daemon setting, a deployment boundary, or a verification gate.

---

## Scope

GoBFD has four security surfaces:

- BFD packet processing on UDP ports 3784, 3785, 4784, 6784, 4789, and 6081.
- The ConnectRPC control API on the default Unix socket and optional `grpc.addr` TCP listener.
- The optional GoBGP gRPC client integration and explicit certificate-renewal
  client for an external Smallstep CA.
- Container, systemd, and Kubernetes privileges needed for raw sockets and
  interface-bound networking.

## RFC Requirements

RFC 5880 and RFC 5881 security considerations apply to all BFD Control and
Echo traffic. Use authentication whenever both peers support it, especially on
multi-access networks, tunnel monitoring, and any segment where packet spoofing
is possible.

RFC 9468 unsolicited BFD remains disabled by default. When enabled, GoBFD admits
a session only from the subnet of the receiving local address, within
`allowed_prefixes`, below `max_sessions` (required, >= 1), and only from
single-hop TTL 255 packets. Admission uses a global token bucket replenished
at 10 sessions per second with a burst of 20, so an on-link host that spoofs
sources can delay legitimate peers; RFC 9468 Section 6.1 allows the feature
only where participants are not anonymous. Set `unsolicited.auth` (Meticulous Keyed SHA1) where both sides can
share a key: the packet that would create a session is then verified first.
Do not enable unsolicited BFD on an anonymous shared segment.

RFC 9747 unaffiliated Echo adds a spoofing concern for returned packets.
The current Echo preview has no authentication setting and transmits
unauthenticated packets. Control-session authentication does not protect it.
Keep it disabled on untrusted segments and isolate any opt-in lab profile.

## ConnectRPC API

The management API can create and delete sessions. Its boundary follows the
[transport decision](../superpowers/specs/2026-10-15-management-transport-decision.md)
(`gobfd-330`):

- By default the daemon serves only the Unix socket `/run/gobfd/gobfd.sock`
  (mode 0660). The packaged configuration sets group `gobfd-admin`; operators
  join that group, not `gobfd`, which can read `/etc/gobfd` and its BFD
  secrets. Each request carries the kernel-reported peer UID as principal
  (`principal=uid:<n>` in RPC logs).
- `grpc.addr` adds an optional TCP listener. Plaintext TCP must be loopback;
  the daemon refuses to start with a non-loopback or wildcard plaintext
  address. Anonymous loopback clients can read but not mutate unless
  `grpc.unsafe_loopback_mutations` is set, which is logged at startup and
  turns the `gobfd.management` health check `NOT_SERVING`.
- `grpc.tls` turns the TCP listener into mTLS (`RequireAndVerifyClientCert`);
  it is required for a non-loopback address. The principal is the client
  certificate URI SAN (SPIFFE) or DNS SAN. Files are re-read on every
  handshake; revocation is not checked, so use short-lived certificates.

Production rules:

- Keep the default socket and grant access through `gobfd-admin`.
- Use mTLS for any API access across a host boundary; never expose plaintext.
- Treat `AddSession`, `DeleteSession`, `AddEchoSession`,
  `DeleteEchoSession`, `AddMicroBFDGroup` and `DeleteMicroBFDGroup` as
  write-sensitive network-control operations.
- Keep `grpc.unsafe_loopback_mutations` disabled with mTLS, on shared hosts or with
  Kubernetes `hostNetwork`, where loopback is reachable by every host
  process.

API session claims belong to the caller's principal: one principal cannot
release another's session (`PermissionDenied`, logged), and
a nonempty `grpc.mutating_identities` makes unlisted recognized mTLS identities read-only.
With the unsafe opt-in enabled, the current guard also admits CA-verified
clients without URI/DNS SANs on remote TLS listeners, outside that role check.
Keep the option `false`; the source correction is tracked as
`gobfd-qj0.8.2.3.8`. See the [configuration limitation](03-configuration.md#grpc----connectrpc-server).
Control,
Micro-BFD and Echo API claims are principal-scoped. Compatible Echo claims share
one wire with configuration or other principals; release never removes another
owner's claim. An admitted identical Echo retry is idempotent. List/Watch remain
daemon-wide read-only observation. See [ADR-0009](adr/0009-echo-principal-claims.md);
these management rights do not authenticate returned Echo packets.

Transport provenance is independent of the displayed identity. A certificate
URI such as `uid:0` remains a TLS identity subject to the configured role list.
Unix UID, TLS URI and TLS DNS owners occupy distinct namespaces; issuing a
certificate never grants Unix socket privileges.

The flight recorder dump (`grpc.flight_recorder`, off by default) is served only on
the management server to Unix-socket principals or mTLS identities permitted by
`grpc.mutating_identities`; anonymous callers are refused even in unsafe mode.
The metrics listener serves no debug endpoint. Trace files contain sensitive
runtime details; keep analysis HTTP servers on loopback.

## GoBGP Integration

GoBFD can connect to GoBGP through `gobgp.addr`. Plaintext mode is acceptable
only for loopback or trusted management networks. Enable `gobgp.tls.enabled`
for remote or non-loopback GoBGP endpoints.

[`scripts/vuln-audit.go`](../../scripts/vuln-audit.go) records the
`GO-2026-4736` exception for the exact `github.com/osrg/gobgp/v4` module,
with owner `@dantte-lp` and expiry (review) date 2026-12-31. The stable-v1
decision of 2026-10-21 (`gobfd-qj0.8.4.1`) requires a fixed version at or below
the pin in GHSA/OSV and a final-candidate govulncheck finding only at module
level, with no imported affected package or reachable symbol. The repository
imports the GoBGP `api` package; that source fact alone does not establish
the result of a fresh scan. Expired, broadened or stale exceptions fail the
audit. See the [dependency risk register](reference/dependency-risk.md) for
the recorded rationale, other exceptions and removal triggers. This policy
is not a statement of current advisory-database contents or final-candidate
acceptance.

CI scans the runtime and isolated tool module manifests separately. Raw
runtime and tools vulnerability reports and their separate CycloneDX SBOMs are
retained in the `dependency-security-reports` artifact so tool-only findings
cannot be attributed to the shipped daemon.

## Management Certificate Renewal

`gobfdctl certificate renew` implements renewal of an existing identity;
initial enrollment, CA operation and unattended scheduling are outside its
scope. The command uses an explicit CA HTTPS origin, roots and existing
certificate/key, validates the returned identity and atomically replaces only
the certificate. This is a separate outbound mTLS client, not a daemon RPC or
an API authorization grant. The [actual-provider profile](../../test/stepca/README.md)
qualifies bounded renewal and negative cases with a disposable CA; it does not
qualify production CA operation or its complete dependency graph for deployment. See the
[CLI contract](04-cli.md#certificate-renewal) and
[`internal/stepca`](../../internal/stepca/client.go).

CA renewal policy and GoBFD access control are separate. Smallstep passive
revocation prevents renewal; it does not cancel an established GoBFD TLS
connection. GoBFD performs no CRL or OCSP revocation check, including on a new
handshake. Certificate renewal or CA revocation alone is therefore not an
immediate API access-removal mechanism. See
[ADR-0005](adr/0005-smallstep-management-certificates.md).

## Secrets

RFC 5880 auth secrets may be supplied through YAML or gRPC `AddSession`.
Production deployments should mount YAML secrets read-only and avoid committing
real key material. Reload rejects authentication changes on a retained session
identity. Changing deployed keys requires a coordinated maintenance restart
or explicit claim release/recreation with session interruption. Multiple stored
keys alone do not provide qualified live rotation; see
[ADR-0006](adr/0006-session-preserving-reload-policy.md).

## Container And Kubernetes Boundary

GoBFD needs network privileges for raw sockets, socket buffer tuning, and
interface binding:

- Prefer `NET_RAW` and `NET_ADMIN` over privileged containers.
- Use `hostNetwork` only when BFD peers are reachable from the host network
  namespace and document that assumption.
- Mount config and auth material read-only.
- Keep the Podman socket available only in the development container. It is not
  a production runtime dependency.

## Verification Gates

Before release or production rollout, run:

```bash
make verify
make vulncheck
make lint-commit MSG='docs(security): define production hardening policy'
```

All commands must run through the project Podman targets. The vulnerability
audit may report allowlisted findings, but any unallowlisted or expired finding
is a release blocker.

*Reviewed against `dev` source: 2026-10-25 (`706991db9433b13cec47e0bb8ea2549b01e948d9`).*
