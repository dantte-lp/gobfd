# Changelog Guide

![Keep a Changelog](https://img.shields.io/badge/Keep_a_Changelog-1.1.0-E05735?style=for-the-badge)
![SemVer](https://img.shields.io/badge/SemVer-2.0.0-3F4551?style=for-the-badge)

> How to maintain the project changelog following [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

---

## Table of Contents

- [Format](#format)
- [When to Add Entries](#when-to-add-entries)
- [Section Types](#section-types)
- [Writing Good Entries](#writing-good-entries)
- [Release Process](#release-process)
- [Semantic Versioning](#semantic-versioning)
- [Examples](#examples)

### Format

The changelog file is `CHANGELOG.md` at the repository root. It follows the [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) specification:

```markdown
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- New feature description.

## [0.4.0] - 2026-03-15

### Fixed
- Bug fix description.

[Unreleased]: https://github.com/dantte-lp/gobfd/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/dantte-lp/gobfd/releases/tag/v0.4.0
```

Rules:

- The `[Unreleased]` section is always present at the top.
- Versions are listed in reverse chronological order (newest first).
- Dates use ISO 8601 format: `YYYY-MM-DD`.
- Comparison links at the bottom of the file enable GitHub diff navigation.
- Each version heading is a link: `## [X.Y.Z] - YYYY-MM-DD`.

### When to Add Entries

Every pull request that changes user-visible behavior **must** include a `CHANGELOG.md` entry under `[Unreleased]`.

Add an entry when your PR:

- Adds a new feature, CLI command, API endpoint, or metric.
- Changes existing behavior (config format, default values, protocol handling).
- Fixes a bug that users could encounter.
- Addresses a security vulnerability.
- Deprecates or removes a feature.

Do **not** add entries for:

- Internal refactoring with no user-visible effect.
- Test-only changes.
- CI/CD pipeline adjustments.
- Documentation typo fixes.

### Section Types

| Section | Use When | GoBFD Example |
|---|---|---|
| **Added** | New feature or capability | `Added BFD multihop support per RFC 5883.` |
| **Changed** | Existing behavior modified | `Changed default DetectMultiplier from 3 to 5.` |
| **Deprecated** | Feature marked for future removal | `Deprecated JSON output format in favor of YAML.` |
| **Removed** | Feature deleted | `Removed legacy configuration file format.` |
| **Fixed** | Bug correction | `Fixed authentication sequence number wraparound at 2^32.` |
| **Security** | Vulnerability fix | `Fixed timing side-channel in HMAC-SHA1 comparison (CVE-XXXX-YYYY).` |

Only include sections that have entries. Do not add empty sections.

### Writing Good Entries

Write for **users**, not developers. Focus on **what** changed, not **how**.

| Quality | Example |
|---|---|
| Bad | Refactored FSM event loop to use channel-based dispatch. |
| Good | Improved session convergence time under high peer count. |
| Bad | Fixed nil pointer in `manager.go:142`. |
| Good | Fixed crash when removing a session during reconciliation. |
| Bad | Updated protobuf dependency. |
| Good | Fixed compatibility issue with GoBGP v3.37+ API changes. |

Guidelines:

- Start with a verb: Added, Changed, Fixed, Removed.
- Reference RFC sections when relevant: `Added Echo mode per RFC 5880 Section 6.4.`
- Reference CVEs for security fixes: `Fixed CVE-2026-XXXX.`
- Keep entries concise -- one line per change.
- Group related changes into a single entry when appropriate.

### Release Process

Branch roles remain explicit throughout release preparation:

- `dev` integrates the next product line and is never tagged for a stable
  release.
- `master` is the default branch and contains the latest accepted stable state.
- Supported product lines use `release/vMAJOR.MINOR`. The `release/v0.6` line
  retains GoBGP v3.37.0 and the v0.6 public contracts.

A maintenance patch starts from the applicable release branch on
`fix/vMAJOR.MINOR-*` and returns through a reviewed pull request. After the fix
is accepted, maintainers assess whether a separate reviewed forward-port to
`master` or `dev` is required. Release preparation follows the applicable
supported line.

Release-branch and tag rulesets must exist before matching refs are created.
When preparing v0.6.2 from `release/v0.6`:

1. **Move entries** from `[Unreleased]` to a new version section:

   ```markdown
   ## [Unreleased]

   ## [0.6.2] - YYYY-MM-DD

   ### Fixed
   - (entries moved from Unreleased)
   ```

2. **Update comparison links** at the bottom of the file:

   ```markdown
   [Unreleased]: https://github.com/dantte-lp/gobfd/compare/v0.6.2...HEAD
   [0.6.2]: https://github.com/dantte-lp/gobfd/compare/v0.6.1...v0.6.2
   [0.6.1]: https://github.com/dantte-lp/gobfd/releases/tag/v0.6.1
   ```

3. **Commit** the changelog update:

   ```bash
   git add CHANGELOG.md
   git commit -m "chore(release): prepare v0.6.2"
   ```

4. **Tag the exact reviewed release-branch commit and push explicit refs**:

   ```bash
   release_commit=$(git rev-parse 'release/v0.6^{commit}')
   git tag -a v0.6.2 "$release_commit" -m "Release v0.6.2"
   git push origin release/v0.6
   git push origin refs/tags/v0.6.2:refs/tags/v0.6.2
   ```

   Never move, delete, or reuse a release tag. A failed published release is
   corrected with a new patch version.

5. **GitHub Actions** uses draft-first publication:
   - Runs the full test suite.
   - Extracts the release notes from CHANGELOG.md for version 0.6.2.
   - Builds binaries (linux/amd64, linux/arm64), .deb, .rpm packages.
   - Publishes Debian-based and Oracle Linux-based OCI images to
     `ghcr.io/dantte-lp/gobfd`.
   - Creates a draft GitHub Release and attaches all required assets.
   - Verifies the draft's tag, target commit, notes, assets, checksums,
     SBOM/provenance, packages, OCI manifests, and release report.
   - Publishes the complete draft as the workflow's final GitHub mutation.

### Semantic Versioning

This project follows [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html): `MAJOR.MINOR.PATCH`.

| Component | Increment When | GoBFD Example |
|---|---|---|
| **MAJOR** | Breaking changes to API, config format, or protocol | Removed deprecated config keys; changed gRPC API response structure. |
| **MINOR** | New features, backward-compatible | Added RFC 5883 multihop support; new `gobfdctl monitor` command. |
| **PATCH** | Bug fixes, documentation, dependency updates | Fixed detection timeout calculation; updated Go dependency. |

Pre-release versions use suffixes: `v0.5.0-rc.1`, `v0.5.0-beta.2`.

### Examples

#### Adding a new feature (PR)

Edit `CHANGELOG.md`, add under `[Unreleased]`:

```markdown
## [Unreleased]

### Added
- BFD Echo mode implementation per RFC 5880 Section 6.4.
```

#### Fixing a bug (PR)

```markdown
## [Unreleased]

### Fixed
- Detection timeout not recalculated after remote MinRxInterval change.
```

#### Security fix (PR)

```markdown
## [Unreleased]

### Security
- Enforce constant-time comparison for all authentication digests.
```

### Related Documents

- [CHANGELOG.md](../../CHANGELOG.md) -- The project changelog.
- [09-development.md](./09-development.md) -- Development workflow and contribution process.
- [CONTRIBUTING.md](../../CONTRIBUTING.md) -- Contribution guidelines.

---

*Last updated: 2026-08-27*
