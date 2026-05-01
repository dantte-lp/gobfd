# Governance

## Project Model

GoBFD is an independent open source project maintained in this repository.

| Area | Policy |
|---|---|
| Default branch | `master` |
| License | Apache License 2.0 |
| Versioning | Semantic Versioning 2.0.0; current release line is `0.x` |
| Changelog | Keep a Changelog 1.1.0 |
| Commits | Conventional Commits 1.0.0 |
| Validation | Podman-only Makefile targets |

## Maintainer Responsibilities

- Review pull requests for protocol correctness, security, tests, and
  documentation impact.
- Keep release notes curated in `CHANGELOG.md` and `CHANGELOG.ru.md`.
- Keep public documentation declarative and source-backed.
- Maintain repository settings, branch protection, dependency automation, and
  security scanning.
- Preserve Apache-2.0 licensing terms and attribution requirements.

## Decision Records

| Decision type | Location |
|---|---|
| Implementation roadmap | `docs/en/implementation-plan.md` |
| Codebase consistency audit | `docs/en/codebase-consistency-audit.md` |
| Security posture | `docs/en/15-security.md` |
| Release process | `docs/en/10-changelog.md` |

## Release Authority

Release tags require:

1. `make verify VERSION=vX.Y.Z`.
2. Required interop gates for protocol changes.
3. Updated changelog entries.
4. Conventional Commit release commit.
5. Immutable SemVer tag.

## References

- Semantic Versioning 2.0.0: <https://semver.org/spec/v2.0.0.html>
- Keep a Changelog 1.1.0: <https://keepachangelog.com/en/1.1.0/>
- Conventional Commits 1.0.0: <https://www.conventionalcommits.org/en/v1.0.0/>
