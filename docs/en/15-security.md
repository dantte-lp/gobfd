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

RFC 9468 unsolicited BFD remains disabled by default. When enabled, it must be
restricted by interface and source prefixes. Do not enable unsolicited BFD on an
anonymous shared segment.

RFC 9747 unaffiliated Echo inherits RFC 5880/5881 considerations and adds a
spoofing concern for looped-back Echo packets. Use BFD authentication for Echo
packets when the deployment can coordinate keys.

## ConnectRPC API

The daemon currently serves ConnectRPC over `net/http` with h2c so gRPC clients
can connect without TLS on localhost. ConnectRPC itself uses regular Go HTTP
handlers; TLS or mTLS must be provided by the Go HTTP server in a future
native-TLS change, or by a local sidecar/reverse proxy today.

Production rules:

- Bind `grpc.addr` to `127.0.0.1:50051` or a Unix-local network namespace by
  default.
- If the API must cross a host boundary, terminate mTLS in a local proxy and
  expose only the proxy to the network.
- Treat `AddSession`, `DeleteSession`, and future transport-specific create
  RPCs as write-sensitive network-control operations.
- Do not expose the control API on an untrusted management network.

## GoBGP Integration

GoBFD can connect to GoBGP through `gobgp.addr`. Plaintext mode is acceptable
only for loopback or trusted management networks. Enable `gobgp.tls.enabled`
for remote or non-loopback GoBGP endpoints.

The current GoBGP module has allowlisted advisory `GO-2026-4736`. The
mitigation is to keep the GoBGP API on localhost or a trusted management
network until upstream ships a fixed release. The allowlist entry in
`scripts/vuln-audit.go` has an owner, expiry, reason, and mitigation; expiry
turns the vulnerability gate into a failure.

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
