# Changelog and releases

[Russian translation](../ru/10-changelog.md).

[CHANGELOG.md](../../CHANGELOG.md) is the English release history; [CHANGELOG.ru.md](../../CHANGELOG.ru.md) is the Russian translation. The [dated translation corrections](reference/changelog-errata.md) supply four omitted historical v0.5.0 entries without changing published sections. Published entries and tags remain preserved. Branch roles and publication constraints are defined in [AGENTS.md](../../AGENTS.md).

## Maintain an entry

1. Identify the user-visible result: protocol procedure, configuration/default, API, CLI, metrics, security or compatibility.
2. Add a concise entry to `Unreleased` in both changelogs. State resulting behavior and limitations; omit internal task IDs and tracker commands according to the [documentation standard](reference/documentation-standard.md#changelog-and-public-development-status).
3. Use only nonempty Added, Changed, Deprecated, Removed, Fixed or Security groups.
4. Link each change to its real contributing PR where one exists. Do not substitute an unrelated PR or invent a number for unpublished commits.
5. Verify that both entries preserve the same conditions, identifiers, PR links and acceptance scope.
6. Keep internal refactoring and test-only work out of user release history unless they change a supported contract.

The layout follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/): newest entries first and dated releases. Version selection follows [SemVer](https://semver.org/spec/v2.0.0.html). A release entry is curated user history, not raw commit output. An implementation entry does not imply wire, native or production acceptance.

## Branch ownership

| Branch | Role |
|:---|:---|
| `dev` | Next product line; never a stable tag target |
| `master` | Latest accepted stable state |
| `release/vMAJOR.MINOR` | Release line; alpha uses the narrower ADR-0008 scope |
| `fix/vMAJOR.MINOR-*` | Fix targeting the corresponding supported line |

The v0.6 line preserves GoBGP v3.37.0 and its public contracts. Acceptance on `dev` does not deliver a fix to a stable line.

## Prepare a release

1. Select the stable or approved alpha acceptance scope. Complete its exact-candidate checks; retain branch, full commit, build identities and evidence.
2. Verify matching branch/tag rulesets before creating either resource.
3. Select a new unused version allowed by that channel. Bind its annotated tag to the exact reviewed release-branch HEAD; never move, delete or reuse tags.
4. Review the Release Please release PR. Move accepted Unreleased entries into a dated version section in both languages, verify its version manifest and real contributing PR links, and update immutable tag/comparison links. Preserve published sections.
5. Run source, critical functional, security, package and supply-chain checks against that candidate. Stable delivery additionally requires its native, wire and reference-load gates; preserve failed/skipped gates explicitly.
6. Follow the separately authorized release workflow. Verify complete draft assets before immutable publication.
7. Read back published assets, release flags, notes and OCI identities. Deliver accepted stable history and both changelogs to `master` for stable releases; alpha leaves the accepted stable state unchanged. Assess forward-port to `dev` separately.

Existing `v0.1.0` through `v0.6.1` tags remain unchanged. The executable process lives in [.github/workflows/release.yml](../../.github/workflows/release.yml) and [Go-owned automation](reference/automation.md). A workflow definition is not evidence of a successful release. This guide does not authorize push, tag, draft, promotion or publication.

Treat publication steps as state changes, not repeatable checks. After a failure, read back the exact tag, draft, assets and OCI aliases before selecting a recovery or retry. Preserve accepted tags and published history; the [automation contract](reference/automation.md#release-sequence) limits alias rollback and keeps postpublication verification separate.

## Release evidence

The workflow validates preflight identities, derives notes from all maintenance sections since the preceding published stable release, verifies the merged release PR and appends its URL, builds binaries/packages/OCI products, completes the draft and verifies downloaded evidence. Stable releases then promote checked OCI aliases; alpha bypasses that mutation and rechecks versioned OCI identities. Publication is the terminal mutation. The first release in a line uses the preceding published stable release across lines.

The supported OCI product is digest-pinned Debian trixie. Generic RPM generation remains available; Oracle Linux 10 is outside v1 OCI/lifecycle qualification. [Automation reference](reference/automation.md) records asset and verifier boundaries.

After verifying the published release and its assets, replace the release PR's `autorelease: pending` label with `autorelease: tagged` and add `autorelease: published`. Keep the pending label while publication is incomplete. This postpublication bookkeeping completes the [Release Please lifecycle](https://github.com/googleapis/release-please/blob/v17.11.2/docs/customizing.md#release-lifecycle-labels); it is separate from the terminal publication command.

## Prerelease boundary

[ADR-0008](adr/0008-experimental-prerelease-boundary.md) accepts an experimental `v1.0.0-alpha.N` channel, where `N` is a positive decimal integer without leading zeroes. Use `release/v1.0`; never tag `dev` or `master` for this channel. Other prerelease spellings are rejected deliberately.

Release Please prepares the release PR in manifest mode with `skip-github-release: true` and `skip-changelog: true`. Curate both root changelogs in that same PR using `## [1.0.0-alpha.1] - YYYY-MM-DD` with the actual release date. The manifest version and both version headings must agree. Release Please neither creates the tag nor publishes the draft. The [release-please workflow](../../.github/workflows/release-please.yml) proposes releases on `release/v1.0` pushes. Manual dispatch is available only after GitHub registers the workflow from the default branch; the release-line push remains the initial trigger.

Commit the curated English/Russian sections to the release PR source branch using the maintainer identity. Confirm that all required PR checks run and pass; approve workflow runs when GitHub requires it. Do not merge an automation-created PR without those checks.

Merge the reviewed PR only after its required checks. Push the annotated tag to its exact merge commit while it remains the release-branch HEAD. `release-pr-check` rejects a missing or mismatched merged PR and appends the verified release PR URL to the notes. Notes retain links to the contributing PRs from the curated changelog. Do not rely on a `GITHUB_TOKEN` tag mutation to trigger another workflow; [GitHub event rules](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow#triggering-a-workflow-from-a-workflow) govern event recursion.

GoReleaser creates a prerelease draft; the verifier checks its `isPrerelease` state, assets and versioned OCI identities. Publish alpha with `--prerelease --latest=false`. Do not call `release-promote` for alpha; it rejects prerelease tags. Preserve stable `latest` and `debian-trixie` OCI aliases and the GitHub latest release.

Alpha is experimental and does not promise stable API compatibility, supported scale, full RFC conformance or production readiness. Record remaining strict CPU-window, native Control/Echo A/B, calibrated reference/24-hour and final wire qualification in both languages. Functional/security failures within the advertised alpha scope still block its publication; pending stable qualification remains open.

## Diagnose release documentation

| Symptom | Check | Action |
|:---|:---|:---|
| Missing maintenance notes | Previous published stable tag and changelog range | Include every maintenance section in the range |
| EN/RU mismatch | Conditions, versions, negation and acceptance claims | Correct both entries before candidate review |
| Existing version/tag | Host release, draft and OCI identity | Select a new unused version; preserve old objects |
| Green source gate only | Native/wire/load/package evidence | Keep release qualification open |
| Stable history absent from master | Branch ancestry and both changelogs | Complete supported-line closeout |

[Development](09-development.md), [governance](../../GOVERNANCE.md), [security policy](../../SECURITY.md).
