# Governance

## Project Model

GoBFD is an independent open source project maintained in this repository.

| Area | Policy |
|---|---|
| Default branch | `master` |
| Integration branch | `dev` for the next product line; never a stable release tag source |
| Supported lines | `release/vMAJOR.MINOR`; `release/v0.6` retains GoBGP v3.37.0 and the v0.6 public contracts |
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

Maintainers own supported release lines and immutable publication. Release tags
require:

1. A reviewed commit on the applicable `release/vMAJOR.MINOR` branch.
2. `make verify VERSION=vX.Y.Z` and required interop gates for protocol
   changes on that exact commit.
3. Updated changelog entries and a Conventional Commit release commit.
4. Release-branch and tag rulesets established before the corresponding refs.
5. An immutable SemVer tag pointing to the exact reviewed release-branch
   commit; the tag is never moved, deleted, or reused.
6. GitHub Actions creation of a draft release whose assets and notes are
   complete and verified before a maintainer authorizes immutable publication.

Every fix accepted on a supported line requires an explicit assessment of
whether the defect also exists on `master` or `dev`. Applicable fixes are
forward-ported through a separate reviewed pull request; otherwise the
maintainer records why no forward-port is required.

## References

- Semantic Versioning 2.0.0: <https://semver.org/spec/v2.0.0.html>
- Keep a Changelog 1.1.0: <https://keepachangelog.com/en/1.1.0/>
- Conventional Commits 1.0.0: <https://www.conventionalcommits.org/en/v1.0.0/>
