# Changelog and releases

[CHANGELOG.md](../../CHANGELOG.md) is the English release history; [CHANGELOG.ru.md](../../CHANGELOG.ru.md) is its complete Russian counterpart. Published entries and tags remain preserved. Branch roles and publication constraints are defined in [AGENTS.md](../../AGENTS.md).

## Maintain an entry

1. Identify the user-visible result: protocol procedure, configuration/default, API, CLI, metrics, security or compatibility.
2. Add a concise entry to `Unreleased` in both changelogs. State resulting behavior and limitations.
3. Use only nonempty Added, Changed, Deprecated, Removed, Fixed or Security groups.
4. Verify that both entries preserve the same conditions, identifiers and acceptance scope.
5. Keep internal refactoring and test-only work out of user release history unless they change a supported contract.

The layout follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/): newest entries first and dated releases. Version selection follows [SemVer](https://semver.org/spec/v2.0.0.html). A release entry is curated user history, not raw commit output. An implementation entry does not imply wire, native or production acceptance.

## Branch ownership

| Branch | Role |
|:---|:---|
| `dev` | Next product line; never a stable tag target |
| `master` | Latest accepted stable state |
| `release/vMAJOR.MINOR` | Qualified supported line |
| `fix/vMAJOR.MINOR-*` | Fix targeting the corresponding supported line |

The v0.6 line preserves GoBGP v3.37.0 and its public contracts. Acceptance on `dev` does not deliver a fix to a stable line.

## Prepare a release

1. Complete implementation and exact-candidate qualification. Retain branch, full commit, build identities and evidence.
2. Verify matching branch/tag rulesets before creating either resource.
3. Select a new unused stable version. Bind its tag to the reviewed release-branch commit; never move, delete or reuse tags.
4. Move accepted Unreleased entries into a dated version section in both languages. Update immutable tag/comparison links.
5. Run the required source, native, wire, load, package and supply-chain gates against that candidate. Preserve failed/skipped gates explicitly.
6. Follow the separately authorized release workflow. Verify complete draft assets before immutable publication.
7. Read back published assets and OCI identities. Deliver supported stable history and both changelogs to `master`; assess forward-port to `dev` separately.

Existing `v0.1.0` through `v0.6.1` tags remain unchanged. The executable process lives in [.github/workflows/release.yml](../../.github/workflows/release.yml) and [Go-owned automation](reference/automation.md). A workflow definition is not evidence of a successful release. This guide does not authorize push, tag, draft, promotion or publication.

## Release evidence

The workflow validates preflight identities, derives notes from all maintenance sections since the preceding published stable release, builds binaries/packages/OCI products, completes the draft, verifies downloaded evidence, promotes checked OCI aliases and publishes as the terminal mutation. The first release in a line uses the preceding published stable release across lines.

The supported OCI product is digest-pinned Debian trixie. Generic RPM generation remains available; Oracle Linux 10 is outside v1 OCI/lifecycle qualification. [Automation reference](reference/automation.md) records asset and verifier boundaries.

## Prerelease boundary

SemVer allows prerelease identifiers. Current `release-preflight` and `release-notes` accept canonical stable tags. Do not send a prerelease tag through those commands or treat SemVer syntax as implemented release automation. Qualify any future prerelease path separately.

## Diagnose release documentation

| Symptom | Check | Action |
|:---|:---|:---|
| Missing maintenance notes | Previous published stable tag and changelog range | Include every maintenance section in the range |
| EN/RU mismatch | Conditions, versions, negation and acceptance claims | Correct both entries before candidate review |
| Existing version/tag | Host release, draft and OCI identity | Select a new unused version; preserve old objects |
| Green source gate only | Native/wire/load/package evidence | Keep release qualification open |
| Stable history absent from master | Branch ancestry and both changelogs | Complete supported-line closeout |

[Development](09-development.md), [governance](../../GOVERNANCE.md), [security policy](../../SECURITY.md).
