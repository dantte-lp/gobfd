# Security policy

## Private disclosure

Report vulnerabilities through [GitHub private security advisories](https://github.com/dantte-lp/gobfd/security/advisories/new).
Do not publish exploit details, credentials or packet captures containing
secrets in a public issue. Include the affected commit/version, enabled
profile, deployment boundary, reproducible input, impact and mitigation.

The maintainer coordinates disclosure. Reporter acknowledgement follows the
reporter's disclosure preference. [Support](SUPPORT.md) handles non-security
defects; [Maintainers](MAINTAINERS.md) identifies release and security ownership.

## Security boundaries

| Boundary | Required control | Reference |
|:---|:---|:---|
| Untrusted BFD packets | Validate framing, authentication, sequence window and transport metadata | [Protocol](docs/en/02-protocol.md), [packet parser](internal/bfd/packet.go) |
| Local management | Authorize the peer principal; restrict the Unix socket | [Security manual](docs/en/15-security.md) |
| Remote management | Configure mTLS and recognized principal policy | [Management configuration](docs/en/03-configuration.md) |
| Metrics and diagnostics | Keep unauthenticated metrics scoped; protect debug data | [Monitoring](docs/en/07-monitoring.md) |
| Optional GoBGP API | Restrict its network exposure and configure TLS for remote access | [Integrations](docs/en/11-integrations.md) |
| Build dependencies | Scan runtime and tools independently; retain raw results | [Dependency risk](docs/en/reference/dependency-risk.md) |

Handwritten runtime code does not use `unsafe`; generated protocol bindings
retain their generator's implementation. Regenerate bindings rather than
editing them. Fuzz tests and static-analysis checks complement runtime
qualification; their presence alone does not establish security acceptance.

## Dependency exceptions

The executable allowlist is [scripts/vuln-audit.go](scripts/vuln-audit.go).
These entries record scoped maintainer decisions, not a live statement about
an external advisory database. Run the final-candidate audit before acceptance.

| Advisory | Exact module | Allowed scope | Review expiry |
|:---|:---|:---|:---|
| [GO-2026-4736](https://pkg.go.dev/vuln/GO-2026-4736) | `github.com/osrg/gobgp/v4` | Module-only; affected server package is outside the accepted import graph | 2026-12-31 |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | `golang.org/x/crypto` | Module-only; `openpgp` remains absent from runtime and tools build graphs | 2026-12-31 |

The GoBGP decision records the upstream fix and the vulnerability-database
version discrepancy. Stable v1 also requires fresh GHSA/OSV fixed-version
and final-candidate reachability evidence.
The `release/v0.6` GoBGP v3 risk has a separate maintainer decision and
undelivered stable-line disposition; the `dev` v4
exception does not complete that work.

Reject reachable affected symbols, disallowed package findings, unexpected
modules, unknown scanners, expired entries and stale entries without a
matching finding. Remove an exception when its original module-only finding
no longer exists. Keep runtime and tools scanner output and SBOMs separate.

## Protocol cryptography

RFC 5880 authentication interoperates with its MD5/SHA1 algorithms. Treat
these as protocol compatibility mechanisms, not modern general-purpose
cryptography or encryption. Match the peer's supported mode and key ID;
prefer the tighter meticulous sequence policy where compatible. Protect
session keys and retain an independently trusted network boundary.

[Authentication rules](docs/en/02-protocol.md) define packet acceptance;
[deployment security](docs/en/15-security.md) defines principal and secret
handling. A TTL/Hop Limit check does not authenticate an on-link sender.

## Supported lines

Use the latest published release unless its line has a separately documented
support boundary. `release/v0.6` retains its public and dependency contracts.
`dev` and experimental profiles do not inherit stable-release acceptance.
Consult [release governance](GOVERNANCE.md) and the [dated status](docs/en/roadmap.md).
