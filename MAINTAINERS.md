# Maintainers

## Current Maintainers

| GitHub user | Role | Scope |
|---|---|---|
| `@dantte-lp` | Owner | Repository administration, releases, security advisories, final merge authority |

## Review Ownership

| Path | Primary review focus |
|---|---|
| `api/`, `pkg/bfdpb/` | Public API compatibility, protobuf generation, Buf gates |
| `cmd/` | CLI behavior, daemon entrypoints, package documentation |
| `internal/bfd/` | RFC 5880/5881 protocol correctness, timers, authentication |
| `internal/netio/` | Linux sockets, overlay transports, interface monitoring |
| `internal/server/` | ConnectRPC/gRPC API, request validation |
| `deployments/`, `.github/` | Packaging, CI, security scans, repository automation |
| `docs/`, `README.md`, `CHANGELOG*.md` | Declarative documentation, release notes, source references |

## Maintainer Rules

- Repository settings changes require an issue, pull request, or recorded
  maintainer note.
- Security reports remain private until disclosure is coordinated.
- Release tags are immutable.
- Generated files are updated only through the documented generator targets.

## Becoming a Maintainer

Maintainer status requires sustained contributions in protocol correctness,
security, operations, or documentation and explicit approval from an existing
maintainer.
