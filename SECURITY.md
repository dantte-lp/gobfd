# Security Policy

## Public Policy Files

| File | Purpose |
|---|---|
| [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) | Community behavior and enforcement policy |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Contribution workflow and validation gates |
| [SUPPORT.md](SUPPORT.md) | Non-security support routing |
| [GOVERNANCE.md](GOVERNANCE.md) | Maintainer and release governance |

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
  RFC keyed-hash verification, key management
- **Raw socket operations** (`internal/netio/`): TTL validation (GTSM),
  interface binding, privilege management
- **gRPC API** (`internal/server/`): Input validation, authorization

## Security Measures

- Handwritten runtime code does not use `unsafe`; generated protobuf bindings
  retain the standard generator's descriptor implementation and are never hand-edited
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

## Known Dependency Advisories

### GO-2026-4736 — GoBGP NEXT_HOP denial of service

[GO-2026-4736](https://pkg.go.dev/vuln/GO-2026-4736) (`CVE-2026-30405`,
`GHSA-4p9m-8gc4-rw2h`) is a denial-of-service advisory in GoBGP's handling of
the BGP NEXT_HOP path attribute.

On `dev`, GoBFD uses `github.com/osrg/gobgp/v4 v4.10.0` and imports only
`github.com/osrg/gobgp/v4/api`. GHSA-4p9m-8gc4-rw2h lists v4.4.0 as patched
(upstream commit `583080a`). The Go vulnerability database lists no fixed
GoBGP v4 version but limits the advisory to the symbol
`fsmHandler.recvMessageloop` in `github.com/osrg/gobgp/v4/pkg/server`, which
GoBFD does not import. Strict `govulncheck` reports the advisory only at
module level; module-level `osv-scanner` also reports it. By maintainer
decisions of 2026-10-13 (`dev`) and 2026-10-21 (stable v1, `gobfd-qj0.8.4.1`),
`scripts/vuln-audit.go` accepts only that module-level finding for the exact
`github.com/osrg/gobgp/v4` module (`ModuleOnly`); an imported affected package
or a reachable symbol fails the audit. Stable v1 additionally requires
GHSA/OSV to list a fixed version at or below the pin and a final-candidate
govulncheck run with the advisory at module level only. Owner: `@dantte-lp`;
expiry (review) date: 2026-12-31. Remove the entry once the Go vulnerability
database records a fixed GoBGP v4 version; the audit fails on an entry that
matches no scanner finding.

`release/v0.6` keeps `github.com/osrg/gobgp/v3 v3.37.0`, which has no fixed
GoBGP v3 version. By maintainer decision of 2026-10-13, the v0.6 line accepts
this risk until 2026-12-31 as a support boundary, with the mitigation that the
GoBGP API is exposed only on localhost or a trusted management network.
Delivery of this decision to the stable branch is tracked by Beads
`gobfd-qj0.8.1.16.4` and is not done.

GoBFD uses GoBGP only for the optional GoBGP integration path. Keep the GoBGP
gRPC endpoint bound to localhost or another trusted management network. For
remote GoBGP API endpoints, enable `gobgp.tls.enabled` and configure
`gobgp.tls.ca_file` / `server_name`. Do not expose plaintext GoBGP gRPC to
untrusted peers.

### GO-2026-5932 — x/crypto/openpgp denial of service

`golang.org/x/crypto v0.58.0` is reported at module level for
[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932). The advisory affects the
unmaintained `golang.org/x/crypto/openpgp` package, which is absent from the
runtime and tools build dependency graphs (`go list -deps`).

The module-inventory exception expires on 2026-12-31. It accepts only findings
for the exact `golang.org/x/crypto` module from `govulncheck` or `osv-scanner`.
A reachable symbol, an `openpgp` package finding, a package mismatch, or an
unknown scanner fails the audit. Do not add an `openpgp` import; remove the
exception when the module no longer produces the inventory finding.

CI allowlists only the module-only `GO-2026-4736` and `GO-2026-5932`
exceptions in `scripts/vuln-audit.go`. Any other advisory, expired entry,
stale entry without a matching finding, package mismatch, unknown scanner, or
disallowed reachability reported by the scanners fails the vulnerability
audit.

CI scans `go.mod` and the isolated `tools/go.mod` as separate OSV inputs. It
retains raw runtime/tools vulnerability JSON and separate CycloneDX SBOMs in
the `dependency-security-reports` artifact so tool-only findings are not
attributed to the shipped daemon.

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
