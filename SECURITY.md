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

The executable audit policy is [scripts/vuln-audit.go](scripts/vuln-audit.go)
and [dependency risk](docs/en/reference/dependency-risk.md). On `release/v1.0`
(`github.com/osrg/gobgp/v4` `v4.11.0`, `golang.org/x/crypto` `v0.59.0`),
`scripts/vuln-audit.go` carries zero runtime, tools, `gopls` or Syft allowlist
entries (`GO-2026-4736` and `GO-2026-5932` are retired) and fails if
`github.com/osrg/gobgp/v4/pkg/server`, `golang.org/x/crypto/openpgp`,
`github.com/docker/distribution` or `rsc.io/pdf` enters a selected package
graph.

The separately audited GoReleaser `v2.20.3` publisher binary retains bounded
`COMPONENT_NOT_PRESENT` and `FIXED_SOURCE_RANGE` dispositions through
`2026-12-31` verified against its compiled ELF `BuildInfo` and binary
`govulncheck` SBOM. The `release/v0.6` GoBGP `v3.37.0` line retains a separate
maintainer disposition; `release/v1.0` does not alter `release/v0.6`. Run a
fresh candidate audit before release acceptance.

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
support boundary. `release/v1.0` couples to GoBGP `v4.11.0` and the v1 public
contracts; `release/v0.6` retains GoBGP `v3.37.0` and its v0.6 contracts.
`dev` and preview profiles do not inherit stable-release acceptance.
Consult [release governance](GOVERNANCE.md) and the [dated status](docs/en/roadmap.md).
