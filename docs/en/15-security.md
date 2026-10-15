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
- The ConnectRPC control API exposed by `grpc.addr`.
- The optional GoBGP gRPC client integration.
- Container, systemd, and Kubernetes privileges needed for raw sockets and
  interface-bound networking.

## RFC Requirements

RFC 5880 and RFC 5881 security considerations apply to all BFD Control and
Echo traffic. Use authentication whenever both peers support it, especially on
multi-access networks, tunnel monitoring, and any segment where packet spoofing
is possible.

RFC 9468 unsolicited BFD remains disabled by default. When enabled, GoBFD admits
a session only from the subnet of the receiving local address, within
`allowed_prefixes`, below `max_sessions` (required, >= 1) and at up to 10 new
sessions per second, and only from single-hop TTL 255 packets. The admission
rate is global, so an on-link host that spoofs sources can delay legitimate
peers; RFC 9468 Section 6.1 allows the feature only where participants are not
anonymous. Set `unsolicited.auth` (Meticulous Keyed SHA1) where both sides can
share a key: the packet that would create a session is then verified first.
Do not enable unsolicited BFD on an anonymous shared segment.

RFC 9747 unaffiliated Echo inherits RFC 5880/5881 considerations and adds a
spoofing concern for looped-back Echo packets. Use BFD authentication for Echo
packets when the deployment can coordinate keys.

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
- Do not enable `grpc.unsafe_loopback_mutations` on shared hosts or with
  Kubernetes `hostNetwork`, where loopback is reachable by every host
  process.

API session claims belong to the caller's principal: one principal cannot
release another's session (`PermissionDenied`, logged), and
`grpc.mutating_identities` makes unlisted mTLS identities read-only. Echo and
Micro-BFD API objects are not yet principal-scoped.

The flight recorder dump (`grpc.flight_recorder`, off by default) is served only on
the management server and only to callers with a principal; the metrics listener
serves no debug endpoint.

## GoBGP Integration

GoBFD can connect to GoBGP through `gobgp.addr`. Plaintext mode is acceptable
only for loopback or trusted management networks. Enable `gobgp.tls.enabled`
for remote or non-loopback GoBGP endpoints.

The current GoBGP module has allowlisted advisory `GO-2026-4736`. The
mitigation is to keep the GoBGP API on localhost or a trusted management
network until upstream ships a fixed release. The allowlist entry in
`scripts/vuln-audit.go` has an owner, expiry, reason, and mitigation; expiry
turns the vulnerability gate into a failure.

CI scans the runtime and isolated tool module manifests separately. Raw
runtime and tools vulnerability reports and their separate CycloneDX SBOMs are
retained in the `dependency-security-reports` artifact so tool-only findings
cannot be attributed to the shipped daemon.

## Secrets

RFC 5880 auth secrets may be supplied through YAML or gRPC `AddSession`.
Production deployments should mount YAML secrets read-only and avoid committing
real key material. Rotate keys by deploying a new config and reloading during a
maintenance window; dynamic key rotation is not implemented yet.

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
