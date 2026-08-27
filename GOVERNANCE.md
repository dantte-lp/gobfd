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
4. The release-branch ruleset active before each new matching branch is
   created, and the tag ruleset active before each new matching tag is created,
   specifically before `v0.6.2`. Existing `v0.1.0` through `v0.6.1` tags are
   preserved unchanged and are never moved, deleted, or reused.
5. Maintainer authorization occurs when the exact reviewed annotated SemVer tag
   is pushed. The tag points to the reviewed release-branch commit and is never
   moved, deleted, or reused.
6. GitHub Actions then creates a draft release, builds and verifies all release
   evidence, and automatically publishes the complete draft as its final
   mutation.

Every fix accepted on a supported line requires an explicit assessment of
whether the defect also exists on `master` or `dev`. Applicable fixes are
forward-ported through a separate reviewed pull request; otherwise the
maintainer records why no forward-port is required.

## References

- Semantic Versioning 2.0.0: <https://semver.org/spec/v2.0.0.html>
- Keep a Changelog 1.1.0: <https://keepachangelog.com/en/1.1.0/>
- Conventional Commits 1.0.0: <https://www.conventionalcommits.org/en/v1.0.0/>
