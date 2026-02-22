# Changelog Guide

![Keep a Changelog](https://img.shields.io/badge/Keep_a_Changelog-1.1.0-E05735?style=for-the-badge)
![SemVer](https://img.shields.io/badge/SemVer-2.0.0-3F4551?style=for-the-badge)

> How to maintain the project changelog following [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

---

### Table of Contents

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

## [1.2.0] - 2026-03-15

### Fixed
- Bug fix description.

[Unreleased]: https://github.com/dantte-lp/gobfd/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/dantte-lp/gobfd/releases/tag/v1.2.0
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

When preparing a release:

1. **Move entries** from `[Unreleased]` to a new version section:

   ```markdown
   ## [Unreleased]

   ## [1.3.0] - 2026-04-01

   ### Added
   - (entries moved from Unreleased)
   ```

2. **Update comparison links** at the bottom of the file:

   ```markdown
   [Unreleased]: https://github.com/dantte-lp/gobfd/compare/v1.3.0...HEAD
   [1.3.0]: https://github.com/dantte-lp/gobfd/compare/v1.2.0...v1.3.0
   [1.2.0]: https://github.com/dantte-lp/gobfd/releases/tag/v1.2.0
   ```

3. **Commit** the changelog update:

   ```bash
   git add CHANGELOG.md
   git commit -m "Prepare release v1.3.0"
   ```

4. **Tag and push**:

   ```bash
   git tag -a v1.3.0 -m "Release v1.3.0"
   git push origin master --tags
   ```

5. **GitHub Actions** automatically:
   - Runs the full test suite.
   - Extracts the release notes from CHANGELOG.md for version 1.3.0.
   - Builds binaries (linux/amd64, linux/arm64), .deb, .rpm packages.
   - Publishes Docker image to `ghcr.io/dantte-lp/gobfd:1.3.0`.
   - Creates a GitHub Release with the changelog content as the release body.

### Semantic Versioning

This project follows [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html): `MAJOR.MINOR.PATCH`.

| Component | Increment When | GoBFD Example |
|---|---|---|
| **MAJOR** | Breaking changes to API, config format, or protocol | Removed deprecated config keys; changed gRPC API response structure. |
| **MINOR** | New features, backward-compatible | Added RFC 5883 multihop support; new `gobfdctl monitor` command. |
| **PATCH** | Bug fixes, documentation, dependency updates | Fixed detection timeout calculation; updated Go dependency. |

Pre-release versions use suffixes: `v1.0.0-rc.1`, `v1.0.0-beta.2`.

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

*Last updated: 2026-02-21*
