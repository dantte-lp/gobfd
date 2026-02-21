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
- `govulncheck` runs in CI to detect known vulnerabilities in dependencies
- TTL=255 (GTSM, RFC 5082) is enforced on all single-hop BFD packets

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest  | Yes       |

## Acknowledgments

We appreciate responsible disclosure and will acknowledge reporters in
release notes (unless anonymity is requested).
