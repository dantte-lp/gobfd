# Maintainers

## Authority

| Account | Responsibility |
|:---|:---|
| `@dantte-lp` | Repository administration, release authority, security disclosure and final merge decision |

Maintainer changes require explicit approval by an existing maintainer.
Sustained contributions to protocol correctness, operations, security or
documentation establish the review basis; contribution count alone does not
assign release authority.

## Review ownership

| Paths | Review contract |
|:---|:---|
| `api/`, `pkg/bfdpb/` | Public wire/API compatibility and generated binding provenance |
| `internal/bfd/` | State machine, timer transactions, authentication and ownership |
| `internal/netio/` | Linux sockets, metadata, interface and overlay boundaries |
| `internal/server/`, `cmd/` | Principals, validation, CLI and lifecycle |
| `deployments/`, `.github/`, `test/` | Packaging, qualification, resource limits and CI |
| `docs/`, root Markdown | Code correspondence, EN/RU parity, normative references and publication claims |

Before merging, inspect the complete diff and exact source/target commits;
retain independent review evidence and measured checks. Follow
[Governance](GOVERNANCE.md) and [Contributing](CONTRIBUTING.md).
Keep security reports private, release tags immutable and generated files
owned by their generator. Record repository-setting changes in an issue,
pull request or maintainer note.
