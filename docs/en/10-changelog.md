# Changelog and Release Guide

User-visible history lives in [CHANGELOG.md](../../CHANGELOG.md) and
[CHANGELOG.ru.md](../../CHANGELOG.ru.md). Keep both versions semantically
aligned. This guide describes the process; it does not duplicate the changing
`Unreleased` entries.

## Changelog format

Follow [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Keep `Unreleased` first, followed by dated release sections in reverse order.
Dates use `YYYY-MM-DD`; version headings link to immutable tags or comparisons.
Preserve published history. Add only nonempty `Added`, `Changed`, `Deprecated`,
`Removed`, `Fixed` or `Security` sections.

Record changes to user-visible behavior: configuration, defaults, protocol
procedures, API, CLI, metrics, security and compatibility. Internal refactoring,
test changes and typo corrections do not need release entries on their own.
Describe the resulting behavior and its limits. Do not paste commit logs or
claim a timing improvement without measurements.

```markdown
## [Unreleased]

### Fixed
- Reject unsupported authentication changes before accepting a reload generation.
```

Use the actual behavior and acceptance scope in entries. An illustrative
example is not a declaration that another RFC procedure is implemented.

## Branch ownership

| Branch | Role |
|---|---|
| `dev` | Integrates the next product line; never tagged for a stable release |
| `master` | Default branch with the latest accepted stable state |
| `release/vMAJOR.MINOR` | Reviewed and qualified supported product line |
| `fix/vMAJOR.MINOR-*` | Maintenance fix targeting its supported line |

`release/v0.6` retains GoBGP v3.37.0 and the v0.6 contracts. A change accepted
on `dev` does not establish delivery to that line or to `master`.

After publishing a maintenance patch, deliver its accepted stable history and
both changelogs to `master` before release closeout. Forward-port applicable
changes to `dev` separately. Do not rewrite existing tags or reuse failed cuts.

## Release process

Release preparation starts only after implementation and final qualification
are complete. Beads owns the executable plan and approval gates. Current
prepublication documentation reconciliation is owned by `gobfd-qj0.8.2.8.7.2`.

Before creating a release branch or tag, verify that the matching branch and
tag rulesets are active. A tag identifies the exact reviewed release-branch
commit. Existing tags remain immutable, including failed or unpublished cuts.
Choose a new unused version rather than copying an old tagging example.

Update both changelogs for the actual candidate, move accepted `Unreleased`
entries into its dated section, and update comparison links. Run the required
local and release qualification against that commit. Remote writes remain
subject to the current publication authorization; these instructions do not
remove a delivery hold.

The current documentation work is local-only: no push, release branch, tag,
draft, OCI alias promotion or publication is authorized by this guide.

The source workflow in [release.yml](../../.github/workflows/release.yml)
uses Go-owned `cictl` preflight, notes, verification and publication steps:

1. Refuse existing releases, drafts and versioned OCI tags for the candidate.
2. Extract every maintenance section since the preceding published stable
   release in the same line; the first cut in a line uses the preceding stable
   release across lines. Bind notes and comparison links to the immutable tag.
3. Build binaries, DEB/RPM packages and Debian trixie OCI products. Oracle
   Linux 10 is outside the v1 OCI and lifecycle qualification scope; generic
   RPM generation remains available.
4. Attach all required assets to a draft and verify checksums, notes, target
   commit, SBOM/provenance, packages, OCI evidence and the release report.
5. Promote the verified OCI aliases, then publish the complete verified draft.
   Independent postpublication
   readback and supported-branch closeout are separate maintainer steps;
   `release-publish` is the workflow's terminal mutation.

Workflow implementation is not evidence that the current candidate has passed
these steps. Never run publication commands just to validate documentation.

## Experimental prereleases

SemVer permits a prerelease suffix such as `v1.0.0-alpha.1`. Current
`cictl release-preflight` and `release-notes` accept canonical **stable** tags
only. They do not provide an alpha delivery path.
[ADR-0008](adr/0008-experimental-prerelease-boundary.md) remains proposed;
Beads `gobfd-qj0.8.2.6.4` owns a separately qualified experimental channel.
Stable aliases and stable acceptance must not be promoted by an alpha.

## Related documents

- [Development](09-development.md) — local checks and branch workflow.
- [Contributing](../../CONTRIBUTING.md) — contribution and review policy.
- [RFC compliance](08-rfc-compliance.md) — exact support boundaries.
- [Roadmap](roadmap.md) — dated status, not release approval.
