# CI and release automation

[Russian translation](../../ru/reference/automation.md).

Go commands own workflow execution. [.github/workflows/ci.yml](../../../.github/workflows/ci.yml), [security.yml](../../../.github/workflows/security.yml), [e2e.yml](../../../.github/workflows/e2e.yml) and [release.yml](../../../.github/workflows/release.yml) select the commands. [cictl](../../../test/cmd/cictl/main.go), [cirunner](../../../test/internal/cirunner/) and [toolbootstrap](../../../test/cmd/toolbootstrap/main.go) implement them.

These are source contracts. A command's existence does not establish candidate acceptance or authorize a remote mutation.

| Surface | Pinned tool | Binding |
|:---|:---|:---|
| Podman Compose provider | [Compose 5.6.1](https://github.com/docker/compose/releases/tag/v5.6.1) | Verified bootstrap and development image |
| Release packages | [GoReleaser 2.19.0](https://github.com/goreleaser/goreleaser/releases/tag/v2.19.0) | Release workflow and local `package-build` |
| Module SBOMs | [Syft 1.55.0](https://github.com/anchore/syft/releases/tag/v1.55.0) | Release workflow and `cictl sbom` |
| Filesystem scan | [Trivy 0.76.1](https://github.com/aquasecurity/trivy/releases/tag/v0.76.1) | CI fails on CRITICAL/HIGH findings |

Rebuild the development image after the Compose pin changes. Recheck scanner output, SBOM validation and package lifecycle before release; a version update alone does not qualify their results.

The runtime [module](../../../go.mod), shared Go [tools module](../../../tools/go.mod), isolated [gopls module](../../../tools/gopls/go.mod), isolated [Syft module](../../../tools/syft/go.mod) and managed [GoReleaser consumer](../../../tools/goreleaser/qualified.mod) have separate selected dependency graphs. The development image builds `gopls` from its own graph to retain its matching `golang.org/x/tools` API. Its same-module `golang.org/x/net` replacement selects the patched version without changing the language server's tool API dependency. The dependency inventory includes all five graphs; see [graph definitions](../../../test/internal/dependencyinventory/go_tools.go).

The tools module declares `govulncheck` and `osv-scanner` with Go `tool` directives. The [vulnerability audit](../../../scripts/vuln-audit.go) executes those managed tools through `go tool -modfile=tools/go.mod`; it does not resolve independent command versions. Before each OSV pass, Go resolves the complete selected graph, including replacement artifact paths and versions. OSV receives that selection in a private temporary manifest; `--report-dir` retains the selected input and raw report for each graph. The default runtime/tools/gopls/Syft audit rejects empty or conflicting inputs and all local replacements. The separate `publisher-audit` command accepts only the authenticated `../source` and `../refmt-local` replacements in its prepared GoReleaser workspace; all other local replacements fail closed. The OSV response must cover exactly the selected Go module paths and versions; missing, duplicate or additional packages fail the audit. Coverage comparison accepts an omitted leading `v` without changing raw evidence. Actual package checks reject legacy Distribution imports and the packages excluded by the module-only advisory exceptions. Success of the default four-graph audit does not qualify the publisher binaries or their embedded dependencies.

The shared [Syft installer](../../../test/internal/cirunner/syft.go) builds the isolated consumer with `CGO_ENABLED=0`, upstream Linux tags and metadata from the [source profile](../../../internal/toolprofile/syft.go). It verifies the source checksum and probes the opened executable before atomic installation. Keep symbols for binary vulnerability analysis. Failed builds clean only identity-matched temporary files; substituted paths and unexpected directory contents prevent cleanup.

## Routine commands

| Command | Contract |
|:---|:---|
| `cictl build` | Build workflow binaries with fixed direct arguments |
| `cictl lint` | 92 configured base linters and 18 explicit build-tag profiles |
| `cictl test-coverage` | gotestsum race tests; JUnit, JSON and atomic coverage |
| `cictl commit-policy` | Pass PR_TITLE as one literal repository-quality argument |
| `cictl proto-verify` | Build pinned generators, regenerate, reject tracked/staged/untracked protobuf drift |
| `cictl buf-fetch-base`, `buf-breaking` | Validate GITHUB_BASE_REF, fetch full refspec, resolve exact commit for Buf |
| `cictl sbom` | Eight nonempty module-file reports: runtime/tools/gopls/Syft, each in CycloneDX and SPDX |

Fixed report outputs are validated and cleared before collection. A failed child command keeps its stdout/stderr and fails the step; an older regular artifact cannot substitute for successful execution. SBOM collection records dependency inventory, not symbol reachability.

`toolbootstrap podman-runtime` verifies the pinned Go Compose provider, Podman and jq. `podman-e2e` adds hosted-runner setup and a private rootless API socket. Socket readiness has a 20-second bound; it does not prove API health. An existing socket is reused without ownership. Failed bootstrap kills/joins only a newly owned service; its diagnostic log remains available. GITHUB_ENV/GITHUB_PATH publication is part of readiness. `apparmor-denials` filters diagnostics to denial lines.

CI executable steps remain single commands; no multiline shell programs or explicit shell declarations are required. Keep token values out of command arguments and retained artifacts; the proposal stage uses the temporary credential file described below.

## Release proposal

Invoke `cictl release-please-pr` from the [proposal workflow](../../../.github/workflows/release-please.yml). Run the job in this pinned image and install Go 1.27.3 through the existing official setup action:

```text
docker.io/library/node:26.13.0-trixie-slim@sha256:e4db4c914bf8ad241bd24804fc25d272d89bad97e68cdc8a1414862790a02c39
```

The Slim image supplies Node 26.13.0. The trusted Ubuntu runner must provide the public `/etc/ssl/certs/ca-certificates.crt` file; the workflow mounts it read-only at the same container path and sets `SSL_CERT_FILE` for Go HTTPS downloads. This trust store belongs to the runner platform; its contents are not an immutable image pin. Archive integrity and the dependency lock remain mandatory. Its bundled npm is not executed. [tools/release-please](../../../tools/release-please/) isolates upstream Release Please 17.11.2 and its consumer lock from the runtime and Go quality tools. The lock selects `@xmldom/xmldom` 0.8.15, handlebars 4.7.10, js-yaml 4.3.2, brace-expansion 5.0.12 and minimatch 11.0.3.

The Go-owned [bootstrap installer](../../../test/internal/npmbootstrap/install.go) obtains the three official registry archives fixed by [bootstrap.json](../../../tools/release-please/bootstrap.json): npm 12.2.1, undici 6.29.1 and ip-address 10.7.4. It verifies SHA-512, compressed size, regular-file paths, entry count and expanded size. It replaces the two bundled dependency subtrees and applies the closed [minimatch repair](../../../test/internal/npmbootstrap/repair.go) before execution: backport upstream [77a0f5c4b80bdc641c3c930a1d9edf5ad2db57f8](https://github.com/isaacs/minimatch/commit/77a0f5c4b80bdc641c3c930a1d9edf5ad2db57f8) to both module formats and select the existing `glob/raw` entrypoints. Verify every original and derived file hash; reject missing files or drift. Preserve actual bundled versions and the original archive identity separately from the derived files. The Release Please consumer lock selects minimatch 11.0.3 for git-file-utils 3.2.1; this does not replace npm's bundled matcher. This is an artifact installation contract; npm retains dependency resolution and installation. Package-audit results do not establish zero vulnerabilities in the OCI base.

1. Copy the reviewed manifest, lock, `.npmrc` and bootstrap contract into an owned temporary workspace. Install the verified npm bootstrap before executing Node. Enforce exact Node/npm engines through `engine-strict=true`.
2. Invoke the private npm entrypoint through Node: run `ci --ignore-scripts` against the frozen lock, then `audit`. Compare all four copied input digests before and after execution; reject installation, input drift or audit failures before reading the GitHub token.
3. Create a `0600` token file inside an owned `0700` directory. Pass its existing absolute filename through upstream `--token=<file>`; do not copy the token value into child arguments or child environments.
4. Run upstream `release-pr` for `dantte-lp/gobfd`, target `release/v1.0`, using the checked-in config and manifest. Keep `skip-github-release: true` and `skip-changelog: true`; curate both changelogs in the resulting PR.
5. Keep `NODE_COMPILE_CACHE` inside the owned workspace. Remove the token, compile cache and workspace on success and failure. Retain only non-secret diagnostics; do not suppress child-command or cleanup failures.

Go owns orchestration. Node executes upstream npm and the [Release Please CLI](https://github.com/googleapis/release-please/blob/v17.11.2/docs/cli.md); no repository JavaScript, shell or Python launcher is added. The main Go quality gates remain unchanged. Complete exact-candidate API and OCI qualification before delivery; a successful isolated install/audit/help check does not establish publication readiness.

## Benchmarks

`benchmark-pair` collects ten alternating base/head pairs, one 100 ms non-race sample per invocation for BFD/netio. A package/name needs exactly ten valid samples per side. Each output has a 64 MiB streaming limit; aliased/symlinked outputs are rejected. The detached base worktree is created once below RUNNER_TEMP and removed using a bounded cancellation-safe context. Child pipe waiting is bounded at 30 seconds.

`benchmark-run`, `benchmark-base` and release snapshots retain six non-interleaved runs. `benchmark-normalize` maps only the three historical aliases and requires RecvDecodeLookupEnqueue, RecvDecodeFSM and TxMarshalJitter on both sides. `benchmark-report` validates both raw inputs, parses benchstat CSV and atomically publishes Markdown, escaped HTML, raw CSV/notes and versioned JSON. Named critical time/op regressions of at least 10% fail the command when base or head reaches the 100 ns/op floor; other report-only regressions use 1 ns/op. Reports remain available on a critical failure. Step-summary and warning errors are propagated; a missing warning writer or skipped release summary does not bypass the critical gate. Malformed/missing collection and actual benchstat command failures also fail. See [benchmarks](../12-benchmarks.md).

## Release sequence

```mermaid
flowchart LR
  accTitle: Release proposal, verification and publication
  accDescr: A reviewed release PR and annotated tag precede draft verification; only stable releases promote aliases.
    proposal["Reviewed PR<br/>and annotated tag"]:::mgt --> build["Preflight and<br/>complete draft"]:::srv
    build --> verify["Independent<br/>verification"]:::mgt
    verify -->|"stable"| aliases["Stable OCI<br/>aliases"]:::srv
    aliases --> publish["Publish verified<br/>draft"]:::mgt
    verify -->|"alpha"| publish
    classDef srv fill:#e1bee7,stroke:#7b1fa2,color:#000
    classDef mgt fill:#d7ccc8,stroke:#5d4037,color:#000
```

Release Please creates the proposal only with `skip-github-release: true`; the reviewed PR curates both changelogs and the version manifest. The maintainer pushes its exact annotated tag. The sequence separates build, independent evidence verification and terminal publication. Only stable releases change aliases. Preserve the exact candidate and stop at the first failed stage.

| Command | Validation or mutation |
|:---|:---|
| `release-preflight` | Canonical stable or approved alpha tag, annotation, repository, exact release-branch HEAD; reject existing release/draft/versioned OCI tags |
| `release-notes` | Paginated published stable releases, complete maintenance range, real dates; immutable links |
| `release-pr-check` | After notes: exact merged PR/base/commit, manifest version and both dated changelog headings; append verified PR URL |
| `release-build` | Four binaries using GITHUB_REF_NAME/GITHUB_SHA metadata |
| `release-upx` | Exact archive size/hash, complete tar-entry validation, bound executable version; rooted PATH publication |
| `release-syft` | Build and verify the isolated consumer; `--compiler-container` uses the prebuilt nonroot host controller and pinned compiler image |
| `release-goreleaser` | Build and verify the managed publisher with the already pinned native Go toolchain; package callers require its bound binary |
| `release-source-tools --compiler-container` | Authenticate and build Syft and GoReleaser in separate pinned compiler workers; publish both PATH entries after verification and cleanup |
| `publisher-audit` | Prepare one authenticated publisher workspace, build its artifact and audit the same selected graph before cleanup |
| `release-test-report` | Race tests and existing JUnit/JSON/HTML paths |
| `release-benchmarks`, `release-benchmark-metadata` | Six BFD/netio samples; commit, version, UTC, Go and count |
| `release-benchmark-comparison` | Fresh text/HTML/Markdown/CSV/notes/structured comparison when baseline.txt exists; otherwise clear stale outputs and skip comparison |
| `release-reports-archive` | Rooted evidence copy and reports archive; no aggregate producer size or entry bound |
| `release-artifacts` | Independent source/commit validation and exact expected asset receipts |
| `release-assets-upload` | Upload receipt-bound primary assets without clobber |
| `release-oci-evidence` | Both versioned indexes, amd64/arm64, SPDX and linked SLSA v1 attestations |
| `release-evidence` | Hash report/digest snapshots and upload supplemental evidence |
| `release-verify` | Recheck identities, draft/prerelease state, body and exact 12 assets; download and semantically verify the same hashed bytes |
| `release-promote` | Stable only; reject alpha, snapshot aliases, copy verified index, read back descriptors; bounded rollback on failure |
| `release-publish` | Final identity/asset/digest checks, close roots, terminal publication; alpha uses `--prerelease --latest=false` |

The release job compiles `cictl` inside the pinned [compiler image](../../../internal/toolprofile/compiler.go) before registry login. The nonroot host runs `release-source-tools --compiler-container`. Separate bounded workers build Syft and GoReleaser from authenticated sources. The host verifies the opened binaries, source provenance and version metadata, removes temporary files and publishes both absolute binary directories to `GITHUB_PATH` in one transaction. Installations remain outside `dist`; package builds reuse the bound publisher executable.

The [publisher profile](../../../tools/goreleaser/source.json) binds original GoReleaser and Refmt source checksums, two reviewed import migrations and three manifest pairs. The managed consumer resolves only `../source` and `../refmt-local` as local modules. Inventory and security commands use the same verified workspace layout; reject changed files, foreign replacements and failed cleanup.

Run this bootstrap only on a fresh GitHub-hosted Ubuntu runner. Its compiler deadline is 600 seconds with a 5-second kill grace; the release job has a 60-minute deadline. Hosted VM disposal bounds a bootstrap container left before start after abrupt CLI death; runner temporary-directory cleanup or VM disposal removes a leftover bootstrap file. A persistent runner requires a separate lifecycle owner. These source contracts do not establish hosted publication acceptance.

GoReleaser creates a draft with primary upload disabled, `prerelease: auto` and `make_latest: false`. Stable latest promotion is deferred to the verified final publisher. Alpha leaves stable GitHub latest and OCI aliases unchanged. Native package finalization replaces RPM lifecycle programs and refreshes checksums/manifests. The asset matrix requires two Linux archives, four DEB/RPM packages, two archive-matched CycloneDX SBOMs and checksums; reports, OCI digest and supplemental checksum receipts complete the 12 remote names.

The verifier uses a separate checkout at the exact workflow SHA without persistent credentials. It binds original workspace paths to opened roots and preflight receipts. Strict JSON rejects invalid UTF-8, duplicate/noncanonical contract fields and unsafe paths. Downloads reject missing/extra/linked/empty/oversized files. Archive parsing rejects extra gzip members, trailing data, unsafe/duplicate entries, links and special files.

The [producer](../../../test/internal/cirunner/release.go) clears comparison outputs before checking `testdata/benchmarks/baseline.txt` and packages reports without an aggregate output bound. The downstream verifier limits the [compressed asset](../../../test/internal/cirunner/release_download.go) to 64 MiB and the [reports archive](../../../test/internal/cirunner/release_asset_content.go) to 256 MiB expanded and 4096 entries. These verifier limits do not bound archive creation.

OCI evidence is queried by immutable index digest. Both primary and Debian product digests must match and contain exactly linux/amd64 and linux/arm64 with linked attestations. Git/Docker do not receive GitHub tokens. Alpha publication rechecks versioned OCI identities and does not inspect or require stable aliases to match its digest. Stable alias rollback verifies restored identities; terminal publication has no subsequent fallible release step. Postpublication readback and stable-branch closeout remain separate maintainer obligations.

## Dependency inventory

[dependency-inventory.json](../../supply-chain/dependency-inventory.json) describes selected runtime/tools graphs, declared tools, Actions, OCI and peer sources. Generation queries exact-version license evidence through deps.dev; static overrides and source coordinates remain unverified without their own observation.

The snapshot binds its observation epoch, source/query/result/hash, owner and review date. Advancing the epoch requires fresh collection. The inventory checker rejects graph, package-count, source-binding and declaration drift. Go may download authenticated publisher sources and selected modules when they are absent from the cache; this check is not guaranteed to run offline. Unresolved release reviews require exact dimension coverage in review_exception; deferred/stale decisions keep their own bounds. Neither regeneration nor a successful inventory check supplies current upstream security or stable-line acceptance.

## Failure procedure

1. Preserve the exact command, revision, expected artifact list and raw diagnostic output.
2. Check the producer's exit status before using its files.
3. Reject stale, partial or identity-mismatched evidence.
4. Correct the owning source/command and repeat the failed gate on the final candidate.
5. Preserve an explicit remaining release gate until its actual measurement exists.

[Development](../09-development.md), [release process](../10-changelog.md), [E2E harness](../../../test/e2e/README.md).
