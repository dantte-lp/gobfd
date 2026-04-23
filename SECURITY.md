# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in GoBFD, please report it
responsibly. **Do not open a public GitHub issue for security vulnerabilities.**

Instead, please use [GitHub Security Advisories](https://github.com/dantte-lp/gobfd/security/advisories/new)
to report the vulnerability privately.

When reporting, please include:

- Description of the vulnerability
- Steps to reproduce (if applicable)
- Affected versions
- Potential impact

## Scope

GoBFD is a network protocol daemon that processes untrusted input from the
network. Security-relevant areas include:

- **BFD packet parsing** (`internal/bfd/packet.go`): Buffer handling,
  length validation, malformed packet handling
- **Authentication** (`internal/bfd/auth.go`): Sequence number validation,
  HMAC verification, key management
- **Raw socket operations** (`internal/netio/`): TTL validation (GTSM),
  interface binding, privilege management
- **gRPC API** (`internal/server/`): Input validation, authorization

## Security Measures

- The `unsafe` package is never used
- All packet parsing uses bounds-checked operations via `encoding/binary`
- Fuzz testing is implemented for the BFD packet parser
- `gosec` linter runs with `audit: true` in CI
- Semgrep OSS can be run locally with `make semgrep`; Pro scans require
  `semgrep login` and the Semgrep Pro Engine (`make semgrep-pro`)
- `scripts/vuln-audit.go` runs `govulncheck` and `osv-scanner` in CI to detect
  known vulnerabilities in dependencies
- TTL=255 (GTSM, RFC 5082) is enforced on all single-hop BFD packets
- Packet receive paths use bounded packet buffers and release pooled buffers
  explicitly after demultiplexing
- VXLAN/Geneve decapsulation drops expected malformed or non-management packets
  at debug level to avoid warning-log amplification from untrusted tunnel traffic
- HAProxy agent-check connections have bounded concurrency and write deadlines
- Dynamic unsolicited BFD sessions reserve and release quota atomically, and
  passive Down sessions are cleaned up after `unsolicited.cleanup_timeout`
- Optional GoBGP integration bounds each external API action with
  `gobgp.action_timeout`
- Optional GoBGP integration supports TLS via `gobgp.tls.enabled`,
  `gobgp.tls.ca_file`, and `gobgp.tls.server_name`; plaintext is intended only
  for loopback or trusted management networks, and plaintext non-loopback
  endpoints emit a startup warning

## Known Dependency Advisory

### GO-2026-4736 — GoBGP NEXT_HOP denial of service

`github.com/osrg/gobgp/v3 v3.37.0` is affected by
[GO-2026-4736](https://pkg.go.dev/vuln/GO-2026-4736), a denial-of-service
advisory in GoBGP's handling of the BGP NEXT_HOP path attribute. As of
2026-04-22, the advisory does not list a fixed version.

GoBFD uses GoBGP only for the optional GoBGP integration path. Until an upstream
fix is available, operators should keep the GoBGP gRPC endpoint bound to
localhost or another trusted management network. For remote GoBGP API endpoints,
enable `gobgp.tls.enabled` and configure `gobgp.tls.ca_file` / `server_name`.
Do not expose plaintext GoBGP gRPC to untrusted peers.

CI allowlists only `GO-2026-4736` in `scripts/vuln-audit.go`. Any additional
advisory reported by `govulncheck` or `osv-scanner` fails the vulnerability
audit. Remove the allowlist entry after upgrading GoBGP to a fixed release.

## Accepted Protocol Exceptions

### MD5 and SHA1 in BFD authentication

GoBFD implements Keyed MD5, Meticulous Keyed MD5, Keyed SHA1, and Meticulous
Keyed SHA1 because they are the hash-based authentication types defined by RFC
5880 Sections 6.7.3 and 6.7.4. Static analyzers such as Gosec and Semgrep flag
these primitives as weak cryptography, which is correct for new protocol
designs, but replacing them would break RFC interoperability.

Operational guidance:

- Prefer SHA1 authentication over MD5 when peer compatibility allows.
- Prefer meticulous authentication modes when a tighter sequence-number replay
  window is required.
- Keep BFD sessions inside an already trusted routing/control-plane boundary;
  BFD authentication does not provide encryption.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest  | Yes       |

## Acknowledgments

We appreciate responsible disclosure and will acknowledge reporters in
release notes (unless anonymity is requested).
