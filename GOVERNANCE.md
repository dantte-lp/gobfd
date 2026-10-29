# Governance

## Project contract

GoBFD is an independent open-source project under [Apache License 2.0](LICENSE).
The maintainer owns compatibility decisions, security disclosure and release
acceptance. [Maintainers](MAINTAINERS.md) identifies the responsible account.

| Material | Authority |
|:---|:---|
| Release acceptance | Measured evidence for the exact candidate and deployment profile |
| Architecture decisions | [ADR register](docs/en/adr/README.md) |
| Runtime and security behavior | [Manual](docs/en/README.md), [Security policy](SECURITY.md) |
| Dated release projection | [Release status](docs/en/roadmap.md) |
| Published changes | [EN changelog](CHANGELOG.md), [RU changelog](CHANGELOG.ru.md) |

English is the primary documentation and commit/PR language; Russian is the
secondary manual translation. Review technical correspondence and translation
parity in the same change. Keep factual descriptions declarative and
operational procedures imperative.

## Branch roles

| Branch | Responsibility |
|:---|:---|
| `dev` | Integrate the next product line; never tag it as a stable release |
| `master` | Preserve the latest accepted stable state |
| `release/vMAJOR.MINOR` | Maintain a supported product line |
| `release/v0.6` | Retain v0.6 public contracts and GoBGP v3 dependency boundary |

Review source and target contracts before integration. Preserve published
history and source branches unless their deletion is explicitly authorized.
Do not merge a failed or unreviewed candidate. Development acceptance does
not establish delivery to a supported release line.

## Release authority

1. For stable delivery, complete the applicable implementation and qualification on the exact
   release-line commit. Run `make verify VERSION=vX.Y.Z` and the required
   protocol/interoperability gates.
2. Curate both changelogs and review the release commit. Verify identity,
   source retention, required checks and independent review evidence.
3. Confirm the release-branch ruleset before creating a matching branch;
   confirm the tag ruleset before creating a matching tag, including `v0.6.2`.
4. Authorize publication by pushing the exact reviewed annotated SemVer tag.
   Preserve existing `v0.1.0` through `v0.6.1` tags. Never move, reuse or
   delete a release tag.
5. Let the [release workflow](.github/workflows/release.yml) create a draft,
   verify complete assets and evidence, then publish as its final mutation.
6. Bring the accepted stable history and both changelogs to `master` before
   closeout. Assess forward-ports to `dev` separately; record inapplicability.

The maintainer authorizes the experimental `v1.0.0-alpha.N` channel on
2026-10-30 under [ADR-0008](docs/en/adr/0008-experimental-prerelease-boundary.md).
Use a reviewed `release/v1.0` HEAD and positive canonical ordinal. Release
Please creates the release PR only; curate both changelogs and the version
manifest in that PR. After merge and required checks, push its immutable
annotated tag. The release workflow verifies the merged PR, exact candidate,
fresh critical/security/package checks and complete draft evidence before
publishing a GitHub prerelease with `latest=false`. Preserve stable GitHub
latest, OCI aliases and `master`. Alpha does not establish stable readiness;
strict CPU, native A/B, calibrated reference/24-hour and final wire acceptance
remain required for stable v1. Policy acceptance does not certify those
checks or a publication result.

## Standards

Use [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html),
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and
[Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/).
The repository's exact accepted types and scopes are implemented in
[repoquality](test/internal/repoquality/policy.go).
