# ADR-0008: Experimental prerelease boundary

[Russian translation](../../ru/adr/0008-experimental-prerelease-boundary.md).

| Field | Value |
|:---|:---|
| Status | Accepted policy; candidate qualification and publication separate |
| Date | 2026-10-30 |

## Context

An experimental `v1.0.0-alpha.1` exposes implemented behavior before stable v1 qualification is complete. The maintainer authorizes this prerelease channel on 2026-10-30. The authorization covers its reviewed delivery path and does not waive stable acceptance or authorize unrelated remote changes.

[SemVer §9](https://semver.org/spec/v2.0.0.html#spec-item-9) identifies a prerelease as unstable. A version suffix does not establish protocol conformance, supported scale or deployment compatibility. The [RFC matrix](../08-rfc-compliance.md) and exact-candidate evidence define those boundaries.

## Decision

Accept only canonical `v1.0.0-alpha.N` tags for the experimental channel, where `N` is a positive decimal integer without leading zeroes. Bind each annotated, immutable tag to the exact reviewed HEAD of `release/v1.0`. Keep stable tag validation and qualification requirements unchanged.

| Stage | Owner and requirement |
|:---|:---|
| Release proposal | Release Please creates a release PR only; `skip-github-release: true` prevents a second tag/release owner |
| Release record | The reviewed PR updates the version manifest and both curated, dated changelog sections; preserve published history and link real contributing PRs |
| Tag authorization | The maintainer pushes the reviewed annotated tag after merge and required checks; never move, delete or reuse it |
| Draft creation | GoReleaser builds versioned products and a GitHub prerelease draft; primary upload remains disabled until package finalization |
| Independent verification | Validate exact source, merged release PR, notes, 12 assets, checksums, SBOMs and versioned OCI evidence before publication |
| Publication | Publish with `--prerelease --latest=false`; leave stable GitHub latest and OCI aliases unchanged |

[Release Please](https://github.com/googleapis/release-please-action/blob/45996ed1f6d02564a971a2fa1b5860e934307cf7/README.md) owns the proposal, not publication. The maintainer tag push triggers the existing release workflow; a tag written with `GITHUB_TOKEN` is not assumed to trigger another workflow. [GitHub's event rules](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow#triggering-a-workflow-from-a-workflow) define this boundary.

The alpha path shares stable artifact and identity checks. `release-pr-check` validates the merged release PR's base, merge commit, manifest version and both changelog headings, then appends its verified URL to release notes. `release-promote` rejects alpha. Alpha publication rechecks versioned OCI identities instead of requiring stable aliases to point to the candidate.

Functional or security failures in the advertised alpha scope block publication. Require exact-candidate builds, critical protocol/authentication/reload/management regressions, a fresh dependency-security disposition, package lifecycle checks and complete verified draft evidence. Record remaining limitations in both changelogs and the release body; acceptance of this policy is not a passing result.

## Alternatives

Retaining a stable-only path prevents an experimental release while final qualification continues. Making Release Please create the release as well as GoReleaser creates competing tag/draft ownership. Publishing alpha through stable alias promotion changes the stable delivery channel. The separate PR-only proposal and verified prerelease publication avoid those conflicts.

## Consequences

Alpha does not establish stable API compatibility, supported session scale, complete RFC coverage or production readiness. Strict CPU-window acceptance, native Control/Echo A/B, calibrated reference distributions, the 24-hour run and final wire qualification remain separate stable conditions. A short fixture or an aggregate CPU percentage cannot substitute for those measurements.

The original pinned Control/Echo candidate could not meet the 48-thread reference bound with 1000 owners. The maintainer's local consolidation instruction of 2026-10-28 separately authorizes source integration removing permanent owner pins. This does not close native A/B, the 48-thread bound, 24-hour operation or final wire gates. Retain the conservative PID budget from [ADR-0007](0007-workload-phase-accounting.md) until measured revision.

Run long qualification independently of interactive SSH in the separately authorized environment. Bind guest resources, clocks, source and binary to retained logs and the terminal result. Alpha delivery neither closes that work nor changes the latest accepted stable state on `master`.

## References

- [Release guide](../10-changelog.md), [automation contract](../reference/automation.md).
- [GoReleaser release configuration](https://github.com/goreleaser/goreleaser/blob/main/www/content/customization/publish/scm/_index.md).
- [Control owner](../../../internal/bfd/session_loop.go), [Echo owner](../../../internal/bfd/echo.go).
- [Workload accounting](0007-workload-phase-accounting.md), [dated roadmap projection](../roadmap.md).
