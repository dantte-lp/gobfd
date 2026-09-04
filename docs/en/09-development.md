# Development

![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![golangci--lint](https://img.shields.io/badge/golangci--lint-v2-1a73e8?style=for-the-badge)
![buf](https://img.shields.io/badge/buf-Protobuf-4353FF?style=for-the-badge)
![Podman](https://img.shields.io/badge/Podman-Dev_Container-892CA0?style=for-the-badge&logo=podman)
![synctest](https://img.shields.io/badge/synctest-Virtual_Time-34a853?style=for-the-badge)

> Development workflow, Make targets, testing strategy, linting, protobuf generation, and contribution guidelines.

---

## Table of Contents

- [Prerequisites](#prerequisites)
- [Development Setup](#development-setup)
- [Branch and Release Workflow](#branch-and-release-workflow)
- [Make Targets](#make-targets)
- [Testing Strategy](#testing-strategy)
- [Linting](#linting)
- [Shell-Free CI Helpers](#shell-free-ci-helpers)
- [Protobuf Workflow](#protobuf-workflow)
- [Go 1.27 Baseline](#go-127-baseline)
- [Code Conventions](#code-conventions)
- [Contributing](#contributing)

### Prerequisites

- **Podman** + **Podman Compose** (all commands run inside containers)
- **Git** for version control
- Go 1.27 (only needed for local IDE support; builds run in containers)

> **Important**: All testing, building, scanning, and linting runs inside Podman containers. No local Go toolchain is required for CI-equivalent builds.

### Development Setup

```bash
# Clone the repository
git clone https://github.com/dantte-lp/gobfd.git
cd gobfd

# Start the development container
make up

# Build all binaries
make build

# Run all tests
make test

# Run linter
make lint

# All at once
make all
```

### Branch and Release Workflow

The branches have distinct product roles:

- `dev` integrates the next product line. Features start from `dev`, target
  `dev`, and are never released by tagging `dev`.
- `master` is the default branch and contains the latest accepted stable state.
- Supported lines use `release/vMAJOR.MINOR`. The `release/v0.6` line retains
  GoBGP v3.37.0 and the v0.6 public contracts.

A v0.6 patch starts from `release/v0.6` on a short-lived `fix/v0.6-*` branch
and returns to `release/v0.6` through a reviewed pull request. After acceptance,
maintainers assess the same defect on `master` and `dev`; an applicable fix is
forward-ported through a separate reviewed pull request. Release-preparation
changes follow the same reviewed path on the applicable supported line.

The release-branch ruleset must be active before each new matching release
branch is created. The tag ruleset must be active before each new matching tag
is created, specifically before `v0.6.2`. Existing `v0.1.0` through `v0.6.1`
tags remain unchanged and are never moved, deleted, or reused. A new stable tag
points to the exact reviewed commit on the applicable release branch and is
also never moved, deleted, or reused. The tag-triggered GitHub Actions workflow
creates a draft GitHub Release, completes and verifies its notes and assets,
and automatically publishes it only as the final mutation.

### Make Targets

Routine build and test targets run Go inside Podman via `podman compose exec`.
The development stack is scoped by `COMPOSE_PROJECT_NAME`, which defaults to
the current checkout directory name. Parallel worktrees use distinct default
project names and can override `COMPOSE_PROJECT_NAME` explicitly.

There is no interactive Make shell target. Use the Make-owned project routing
for a specific non-interactive test, for example:

```bash
make test-run RUN=TestFSMTransition PKG=./internal/bfd
```

#### Lifecycle

| Target | Description |
|---|---|
| `make up` | Start development container |
| `make down` | Stop development container |
| `make restart` | Restart (down + up) |
| `make logs` | Follow development container logs |
| `make dev-project` | Print the active checkout Compose project name |
| `make dev-ps` | Show the active checkout development stack |

#### Build and Test

| Target | Description |
|---|---|
| `make all` | Build + test + lint |
| `make build` | Compile all binaries with version info |
| `make test` | Run all tests with `-race -count=1` |
| `make test-v` | Verbose test output |
| `make test-run RUN=TestFSM PKG=./internal/bfd` | Run specific test |
| `make fuzz FUNC=FuzzControlPacket PKG=./internal/bfd` | Fuzz test (60s) |
| `make test-integration` | Run integration tests |

#### Interoperability Testing

| Target | Description |
|---|---|
| `make interop` | Full cycle: build + start + test + cleanup |
| `make interop-up` | Start 4-peer topology |
| `make interop-test` | Run interop Go tests |
| `make interop-down` | Stop and cleanup |
| `make interop-logs` | Follow interop container logs |
| `make interop-capture` | Live BFD packet capture |
| `make interop-pcap` | Decode captured packets |
| `make interop-pcap-summary` | CSV summary of captures |
| `make interop-bgp` | Full cycle BGP+BFD tests (FRR, BIRD3, ExaBGP) |
| `make interop-bgp-up` | Start BGP+BFD topology |
| `make interop-bgp-test` | Run BGP+BFD Go tests |
| `make interop-bgp-down` | Stop BGP+BFD topology |
| `make interop-clab-bootstrap` | Prepare vendor images and a receipt-owned GoBFD image (`ARGS=...`) |
| `make interop-clab` | Full cycle vendor NOS tests (Nokia, FRR, etc.) |
| `make interop-clab-up` | Deploy vendor NOS topology |
| `make interop-clab-test` | Run vendor interop Go tests |
| `make interop-clab-down` | Destroy recorded vendor resources and the exact owned GoBFD image |

#### Integration Examples

The core, BGP fast-failover, HAProxy health, and observability testcontainers
targets delegate report collection to the Go-owned `test/cmd/e2ectl` runner.
It creates an exclusive report directory, streams the same `go test -json`
output to `go-test.json` and `go-test.log`, and preserves the test exit code.

| Target | Description |
|---|---|
| `make e2e-core-testcontainers` | Core daemon testcontainers gate and `e2ectl` report |
| `make int-bgp-failover-testcontainers` | BGP fast-failover testcontainers gate and `e2ectl` report |
| `make int-haproxy-testcontainers` | HAProxy health testcontainers gate and `e2ectl` report |
| `make int-observability-testcontainers` | Observability testcontainers gate and `e2ectl` report |
| `make int-bgp-failover` | Go testcontainers BGP fast-failover gate; the operational Compose example remains available through `-up`, `-logs`, and `-down` |
| `make int-haproxy` | HAProxy agent-check bridge demo |
| `make int-observability` | Prometheus + Grafana observability stack |
| `make int-exabgp-anycast` | ExaBGP anycast service announcement |
| `make int-k8s` | Kubernetes DaemonSet with GoBGP sidecar |

#### Quality

| Target | Description |
|---|---|
| `make lint` | Run golangci-lint v2 |
| `make lint-fix` | Auto-fix lint issues |
| `make semgrep` | Run local Semgrep OSS scan with `p/golang` rules |
| `make semgrep-json` | Run Semgrep OSS scan and emit JSON |
| `make semgrep-pro` | Run Semgrep with `--pro`; requires Semgrep Pro Engine and `semgrep login` |
| `make vulncheck` | Run the controlled vulnerability audit (`govulncheck` + OSV Scanner) |
| `make osv-scan` | Alias for the controlled vulnerability audit |
| `make vulncheck-strict` | Run raw `govulncheck ./...` without the project allowlist |
| `make osv-scan-strict` | Run raw `osv-scanner scan -r .` without the project allowlist |

The controlled audit treats `go.mod` and `tools/go.mod` as separate OSV
inputs. CI retains the runtime govulncheck/OSV JSON, tools OSV JSON, and
separate runtime/tools CycloneDX SBOMs in the
`dependency-security-reports` artifact.

#### Protobuf

| Target | Description |
|---|---|
| `make proto-gen` | Generate Go code from `.proto` files |
| `make proto-lint` | Lint protobuf definitions |
| `make proto-breaking` | Check for breaking changes |
| `make proto-update` | Update buf dependencies |

#### Dependencies

| Target | Description |
|---|---|
| `make tidy` | Run `go mod tidy` |
| `make download` | Download module dependencies |
| `make clean` | Remove binaries and caches |
| `make versions` | Show tool versions |

### Testing Strategy

#### Unit Tests

- **Table-driven** tests for all packages
- **`t.Parallel()`** where safe (no shared mutable state)
- **Always** run with `-race -count=1`
- **`goleak.VerifyTestMain(m)`** in the six concurrency-heavy packages that
  own the daemon, protocol, network, metrics, configuration, and integration
  lifecycles

#### FSM Tests (`testing/synctest`)

Go 1.27 `testing/synctest` enables deterministic time-based testing and adds
`synctest.Sleep` for the common advance-time-and-settle operation:

```go
func TestFSMDetectionTimeout(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        sess := newTestSession(t, SessionConfig{
            DesiredMinTxInterval:  100 * time.Millisecond,
            RequiredMinRxInterval: 100 * time.Millisecond,
            DetectMultiplier:      3,
        })

        // Bring session to Up state
        sess.injectPacket(controlPacket(StateInit, 0))
        synctest.Wait()
        require.Equal(t, StateUp, sess.State())

        // Detection timeout = 3 x 100ms = 300ms
        synctest.Sleep(350 * time.Millisecond)
        require.Equal(t, StateDown, sess.State())
    })
}
```

Benefits:
- Tests run in virtual time (instant execution)
- Deterministic -- no flaky timer-dependent tests
- Perfect for BFD protocol timers and detection timeouts

#### Fuzz Testing

GoBFD includes fuzz tests for all packet parsers that handle untrusted network input:

```bash
# BFD Control packet codec
make fuzz FUNC=FuzzControlPacket PKG=./internal/bfd

# VXLAN overlay codec (RFC 8971)
make fuzz FUNC=FuzzVXLANHeader PKG=./internal/netio

# Geneve overlay codec (RFC 9521)
make fuzz FUNC=FuzzGeneveHeader PKG=./internal/netio

# Inner packet assembly/disassembly
make fuzz FUNC=FuzzInnerPacket PKG=./internal/netio
```

Each fuzz test has two variants:
- **Round-trip**: verifies `parse(marshal(packet)) == packet` for structured inputs
- **Raw input**: feeds arbitrary bytes to the parser, verifying it never panics

The default fuzz duration is 60 seconds. To run longer:

```bash
make fuzz FUNC=FuzzVXLANHeader PKG=./internal/netio FUZZTIME=300s
```

#### Integration Tests

```bash
make test-integration
```

Uses `testcontainers-go` with Podman backend for testing the full daemon lifecycle.

#### Interoperability Tests

See [05-interop.md](./05-interop.md) for the 4-peer interop testing framework.

### Linting

golangci-lint v2.13.1 with a maximum-by-default configuration:

```bash
make lint
```

The tool is pinned by the `tool` directive in the isolated `tools/go.mod` module.
Local linting is container-only: `make lint` reconciles the dev service and
runs the prebuilt pinned binary inside it. Both CI workflows and the
`make lint-ci` compatibility target call the shell-free `cictl lint` command.
It refuses host execution, fixes `GOMAXPROCS=2` and `GOMEMLIMIT=1500MiB`,
verifies the pinned binary version and 92 enabled nondeprecated linters, and
runs the base plus all 17 build-tag profiles through direct Go argument vectors.
The development image selects its prebuilt `/go/bin/golangci-lint`; minimal CI
Go containers use the same pinned tools module when `GOLANGCI_LINT` is unset.
The dev service defaults to 4 CPUs, an 8 GiB hard memory limit, a 6 GiB Go
runtime soft limit,
no swap beyond that hard limit, and 1,024 PIDs. Go and golangci-lint caches are
kept in the disposable container layer. The image includes the C compiler and
runtime headers required by the Linux Go race detector, so race gates never
install packages at runtime. All commands built with `go install` use the
absolute `GOBIN=/go/bin`, and `/go/bin` is part of `PATH`. Override the limits
only when needed with
`GOBFD_DEV_CPUS`, `GOBFD_DEV_MEMORY_LIMIT`,
`GOBFD_DEV_MEMORY_RESERVATION`, `GOBFD_DEV_GOMEMLIMIT`, and
`GOBFD_DEV_PIDS_LIMIT`.

`.golangci.yml` uses
`linters.default: all`: 92 signal-bearing linters are enabled, while 20
maintained linters with no project inputs, duplicate coverage, or documented
semantic conflicts and two deprecated linters are disabled.
The CI contract validates the v2 schema, the enabled-linter count, the normal
build, and every repository-specific build tag independently. Key checks include:

- `gosec` (with `audit: true`) -- security analysis
- `govet`, `staticcheck`, `errcheck` -- standard Go checks
- `noctx` -- context propagation checks
- `exhaustive` -- exhaustive switch/map checks
- `cyclop`, `gocognit`, `maintidx` -- complexity limits
- `revive`, `wrapcheck`, `gochecknoglobals`, `mnd`, `lll` -- API, error,
  state, and source-discipline checks
- `depguard`, `gomoddirectives` -- dependency hygiene
- `nolintlint` -- quality of `//nolint` directives

### Documentation and Commit Policy

`make lint-md` runs the repository-owned stdlib Go 1.27 checker over a
non-empty, bounded, deterministic Markdown input set. Its fixtures preserve the
36 enabled markdownlint 0.41 rules. `make lint-commit MSG='feat(bfd): add peer'`
checks the preserved Conventional Commit type, scope, case, 100-byte blocking
header limit, 120-byte non-blocking body warning, and default-ignore boundary.
Neither check requires Node.js or npm.

`make lint-spell` runs the pinned Go `misspell` tool over the exact maintained
English documentation set. `make lint-yaml` runs pinned Go `yamlfmt` in lint
mode with the repository's line-preserving `.yamlfmt.yaml` policy.

### Python Absence Contract

The repository owns no Python source, environment, manifest, or lock file.
Vendor image preparation, spelling, YAML policy, and JUnit-to-HTML conversion
are Go-owned. ExaBGP remains an external immutable interop image.

### Shell-Free CI Helpers

The affected GitHub Actions steps contain one Go command each. Repository code
receives only the strict `SONAR_TOKEN_PRESENT` boolean, plus `GITHUB_ACTOR` and
`GITHUB_OUTPUT`; the raw `SONAR_TOKEN` is limited to the pinned SonarSource
scanner action. `cictl sonar-mode` appends `mode=run` for `true` or
`mode=skip-dependabot` only for `false` with the exact `dependabot[bot]` actor.
Invalid or missing values and other missing-token runs fail closed; the step
id and downstream mode conditions stay unchanged.
`cictl sonar-skip-notice` emits the existing Dependabot skip explanation from
repository-owned Go code when that mode is selected.

`cictl build --output /tmp/gobfd-build` validates `GITHUB_SHA`, derives the
eight-character CI version metadata and UTC RFC3339 build time, and invokes
`go build` directly for the four supported binaries. It preserves the pinned
Go 1.27 setup and build flags without embedding a multi-line shell program in
the workflow.

`cictl test-coverage` invokes the pinned `gotestsum` tool directly with the
existing JUnit, JSON, format, race, count, and atomic coverage arguments. Child
stdout, stderr, and failures remain attached to the workflow step. Before the
command starts, the helper validates and truncates only `unit-report.xml`,
`unit-report.json`, `coverage.out`, and `unit-report.html` as regular mode
`0644` files, so a failed run cannot expose stale evidence. The JUnit-to-HTML
command remains unchanged but is expressed as a single workflow command rather
than a folded scalar.

`cictl commit-policy` passes the `PR_TITLE` environment value as one direct
argument to the existing Go repository-quality command. The pull-request-only
job, environment mapping, child streams, and failure behavior remain intact
without shell expansion.

`toolbootstrap podman-runtime` also verifies `jq --version` through its fixed
command allowlist, so the test-tools step remains one Go command and fails if
the required JSON tool is unavailable. The vulnerability audit creates its own
report directory; the workflow delegates to it directly without a separate
directory command.

`cictl sbom --report-dir reports/security` runs the two pinned Syft module-file
scans directly, keeps the runtime and tools CycloneDX reports separate, and
requires each artifact to be a non-empty regular file. Before each scan, the
helper truncates only its expected regular output so stale content cannot pass
validation. It creates the report directory with mode `0755` and normalizes
completed artifacts to mode `0644`; scanner or artifact validation failures
fail the step. The existing
`always()` execution and `dependency-security-reports` artifact contract are
unchanged.

`cictl proto-verify` requires safe absolute repository and `RUNNER_TEMP`
directories, builds both generators from `tools/go.mod` below a dedicated
temporary bin directory, and prepends that directory to `PATH` only for
`buf generate`. It then runs `git diff --exit-code -- pkg/bfdpb`; a generator
build, generation, or generated-code drift failure stops the step.

`cictl buf-fetch-base` and `cictl buf-breaking` read the pull request base only
from the built-in `GITHUB_BASE_REF` and validate it as a branch name. Fetching
uses the exact forced full refspec from `refs/heads/<base>` to
`refs/remotes/origin/<base>`. The breaking check resolves that remote ref to
one 40- or 64-hex commit ID and passes `.git#commit=<sha>` to Buf, so branch
characters cannot be interpreted as Buf source parameters. Both steps retain
their pull-request-only conditions and fail on command errors.

The benchmark job is also Go-owned. `cictl benchmark-run` executes the fixed
package, timeout, sample-count, and memory-report arguments from the repository
root and writes a fresh `new.txt`. `cictl benchmark-base` validates the
`origin/<base>` ref, creates its detached worktree only below `RUNNER_TEMP`, and
uses command-scoped `git -c safe.directory=<root>` configuration. A detached,
bounded cleanup context removes that worktree even when the benchmark command
fails or its parent context is cancelled; repository-global Git configuration
is not changed.

`cictl benchmark-normalize` rewrites only the three exact historical aliases
and requires `RecvDecodeLookupEnqueue`, `RecvDecodeFSM`, and `TxMarshalJitter`
in both raw inputs. `cictl benchmark-report` invokes the pinned `benchstat`
tool for text and CSV output, parses CSV with Go's `encoding/csv`, preserves the
existing warning-only `>=10%` critical/report-only policy, and appends report-only
rows and tool notes to `GITHUB_STEP_SUMMARY`. It atomically publishes mode
`0644` Markdown, escaped HTML, raw CSV/notes, and the versioned
`bench-comparison.json`; stale or non-regular artifacts cannot satisfy the
gate.

The release workflow's test and report jobs use the same shell-free boundary.
The complete workflow forbids explicit `shell:` declarations and multiline or
folded `run` programs; executable steps remain single scalar commands.
`cictl release-build` derives release metadata from `GITHUB_REF_NAME` and
`GITHUB_SHA` and builds the four supported binaries with fixed arguments. Both
tool-install steps call `toolbootstrap podman-runtime` once; that command
already verifies `jq`, so the workflow does not repeat the check.

`cictl release-test-report` runs the pinned `gotestsum` command and renders the
existing JUnit XML, JSON, and HTML paths. `cictl release-benchmarks` retains six
samples for the BFD and netio packages, mirrors each raw result to the job log,
and writes the existing per-package and combined evidence. The metadata mode
writes the version, short commit, UTC date, Go version, and count as JSON.
When a baseline exists, the comparison mode preserves the text and HTML names
and also writes Markdown, CSV, notes, and versioned structured JSON. Release
HTML and JSON comparisons identify the baseline, release version, and UTC
generation time. Fixed benchmark and comparison outputs are prevalidated and
cleared before work starts, including when no baseline exists, so stale files
cannot enter the archive. Finally, `cictl release-reports-archive` copies the
release benchmark evidence into `reports/` and creates the existing
`gobfd-<version>-reports.tar.gz` artifact without shell globbing or archive
commands. Evidence copying uses a repository `os.Root`; archive reads use a
separate Go 1.27 `os.Root` rooted at `reports/`. Both verify the opened regular
file against its expected identity and size.

Before release creation, `cictl release-preflight` validates the canonical
stable SemVer tag, `GITHUB_REPOSITORY`, the checked-out commit, the annotated
tag object, and the exact `release/vMAJOR.MINOR` branch head. It queries GitHub
with fixed `git` and `gh` argument vectors, decodes GraphQL and paginated GHCR
JSON in Go, and refuses an existing release or draft and any of the three
versioned OCI tags. The command writes fresh mode `0644` commit, branch, and tag
object receipts under an `os.Root` anchored at the validated `RUNNER_TEMP`.
`GH_TOKEN` is inherited by `gh` through the process environment; it is never
placed in an argument vector or artifact.

`cictl release-notes` then queries every GitHub Releases page with a fixed
`gh api --paginate --slurp` argument vector. It considers published canonical
stable SemVer tags, preferring the highest previous release in the current
major/minor line and otherwise the highest previous release overall;
structurally valid noncanonical tags are ignored. The command requires real
calendar dates plus categorized entries in the exact current-to-previous
`CHANGELOG.md` range, then atomically writes a bounded regular mode `0644`
`release-notes.md` through a repository-rooted Go 1.27 `os.Root`. `GH_TOKEN`
is inherited by `gh` and never appears in arguments or output.

The commit-pinned `anchore/sbom-action/download-syft` action owns PATH
registration for the exact `v1.51.0` version input. The release workflow does
not add a second shell wrapper or mutable Syft shim after that action.

`cictl release-upx` owns the UPX prerequisite without a shell extractor. It
streams the exact UPX `v4.2.2` amd64 Linux asset from `gh release download`
into a new `RUNNER_TEMP` root, enforces the pinned size and SHA-256, sends the
archive to fixed `xz -d -c -q` arguments, and validates the complete tar entry
set, types, sizes, and modes in Go. Only the verified executable is written
atomically with mode `0755`. Decompression consumes the same open file whose
bytes were hashed, and the version check executes the verified open UPX file;
rooted identity checks reject pathname replacement before publication. Its
first `upx --version` line must be exactly `upx 4.2.2` before the rooted bin
directory is appended as a separate newline-delimited record to the validated
regular `GITHUB_PATH` file. Once the rooted rename commits that record,
publication remains recorded even if post-commit inspection or cleanup fails,
so the referenced UPX root is preserved. `GH_TOKEN` is inherited only by `gh`,
is stripped from `xz` and `upx`, and is never placed in arguments or artifacts.
On failure, recursive cleanup is confined to the still-open root whose directory
identity matches the one created; the final pathname operation can only remove
an empty directory and never recursively deletes a pre-existing or replacement
tree.

GoReleaser creates the draft with `release.skip_upload` and does not upload
primary assets itself. Its nFPM DEB packages execute the package-internal Go
lifecycle binary directly. `go -C tools run ./cmd/packagefinalize` replaces
the two RPMs with equivalent packages whose `POSTINPROG` and `PREUNPROG` tags
point to the packaged native lifecycle binary, then refreshes both
`dist/artifacts.json` and `dist/checksums.txt`. After `cictl release-artifacts`
validates the exact two archives and four packages, `cictl
release-assets-upload` uploads only that receipt-bound matrix and revalidates
its checksums before and after the upload.

After GoReleaser creates the draft, the commit-pinned checkout action creates a
separate `.release-verifier` checkout at the exact workflow SHA with no
persistent credentials. `cictl release-artifacts` runs from that clean source,
revalidates the original workspace HEAD against both `GITHUB_SHA` and the
rooted preflight commit receipt, binds the workspace pathname to its opened
root, then reads the bounded regular `dist/artifacts.json`. Before typed
decoding, a structural JSON pass rejects invalid UTF-8,
duplicate object members, and noncanonical case variants of contract fields.
The typed decoder requires the exact two Linux archives, four Debian/RPM packages, two
archive-matched CycloneDX SBOMs, and `checksums.txt` for the canonical release
version. Every selected path must be the exact relative `dist/<asset>` path;
non-release GoReleaser records such as binaries and OCI metadata are ignored.
Duplicate, missing, extra, misnamed, unsafe-path, or cross-platform release
assets fail closed. The command atomically writes sorted mode `0644`
`expected-checksummed-assets.txt` and `expected-release-assets.txt` receipts
under a validated `RUNNER_TEMP` root; the latter also includes the fixed report
archive, OCI digest receipt, and supplemental checksum receipt names.

`cictl release-oci-evidence` runs from the immutable verifier checkout and
replaces the release workflow's OCI shell loop. It inspects the fixed primary,
Debian Trixie, and Oracle Linux 10 versioned references with direct Buildx
argv, requires exactly `linux/amd64` and
`linux/arm64` plus their linked attestation manifests, and validates each
platform's SPDX document and BuildKit SLSA v1 provenance. Docker receives the
workflow environment without `GH_TOKEN` or `GITHUB_TOKEN`; both payload
queries are pinned to the inspected image-index digest rather than the mutable
version tag. Only after all three images pass does the command atomically publish mode `0644`
`release-image-digests.txt` through the separately rooted original workspace
in fixed order; the primary and Debian digests must be identical.

`cictl release-evidence` also runs from the immutable verifier checkout. It
opens the original workspace as a separate root, rejects missing, empty, linked,
misnamed, changed, or oversized report and OCI digest inputs, and validates the
exact three-line digest receipt again. It atomically writes the two canonical
SHA-256 records to mode `0644` `release-evidence-checksums.txt`, then calls
`gh release upload` once with only an owned staging snapshot of the fixed
report, digest, and checksum paths under the verifier checkout. The command
never uses `--clobber`; source and staging root identity and all three file
contents are revalidated immediately before and after the draft upload, even
when `gh` reports an error.

`cictl release-verify` replaces the release draft verification shell and runs
from the same immutable verifier checkout. It revalidates the checkout,
annotated tag object, target commit, release branch, three versioned OCI index
receipts, draft tag/body, and the exact 12-name asset inventory before any
download. It creates a new mode `0700` directory under the identity-bound
`RUNNER_TEMP` root and invokes the documented fixed
`gh release download <tag> --repo <owner/repository> --dir <directory>` argv
without overwrite or skip flags. Downloaded entries must be the exact bounded
set of nonempty regular files; failure cleanup traverses only the opened root
after `SameFile` ownership proof and never removes a replacement pathname.

The same command parses the canonical main and supplemental SHA-256 record
sets in Go and hashes rooted file snapshots. The two CycloneDX documents and
the reports archive are semantically validated from those same hashed byte
snapshots rather than reopened paths. Downloaded OCI digest and supplemental
checksum receipts must equal the local evidence byte-for-byte. CycloneDX JSON
rejects invalid UTF-8, duplicate or noncanonical contract fields, and trailing
data, and requires nonempty metadata and components. The gzip/tar parser
rejects additional gzip members, trailing data, unsafe or duplicate paths,
links and special entries; it requires an explicit `reports/` directory and
bounded regular descendants. Only `gh` inherits GitHub tokens; Git and Docker
receive an explicitly token-stripped environment.

`cictl release-promote` is the only OCI alias-promotion command in the release
workflow and also runs from the immutable verifier checkout. It revalidates
the annotated tag, target commit, release branch, and exact three-line OCI
digest receipt. Before changing an alias, it snapshots the complete existing
OCI index for `latest`, `debian-trixie`, and `oraclelinux10`. It then promotes
the verified primary index to the first two aliases and the verified Oracle
Linux 10 index to the third, and verifies every resulting index descriptor.
This uses Docker's documented
[`imagetools create`](https://docs.docker.com/reference/cli/docker/buildx/imagetools/create/)
single-source index-copy behavior; no platform manifest is flattened.
Any mutation or verification failure restores and verifies every prior alias
digest under an independent bounded rollback context. Docker never receives a
GitHub token.

`cictl release-publish` is the final release command. It first verifies the
three promoted alias indexes, then revalidates the live Git identity, draft
tag and body, the exact 12 remote asset identities, and every local digest
receipt byte used by the preceding checks. Only after its opened roots have
closed successfully does it execute the single terminal
`gh release edit <tag> --repo <owner/repository> --draft=false --latest`
mutation with the documented
[`gh release edit`](https://cli.github.com/manual/gh_release_edit) flags. The
workflow performs no later release command or fallible step.

### Dependency Inventory

The machine-readable supply-chain snapshot lives in
`docs/supply-chain/dependency-inventory.json`. It covers the complete selected
runtime and isolated-tool module graphs plus repository-declared tools, GitHub
Actions, OCI images, and interop daemons.
Regenerate it only after completing the corresponding release-note, security,
license, archive, and pin review:

```bash
make dependency-inventory
make dependency-inventory-check
```

Generation resolves each exact selected Go module version through the stable
deps.dev v3 API and records exact-commit GitHub evidence where deps.dev has no
license expression.
Immutable OCI records keep registry availability separate from their canonical
build-source commit and hashed license file. The check is offline and fails if
either Go module graph, a declared component, a source location, evidence
binding, or the Go package count drifts from the reviewed snapshot.

Every adopted or retained record with an unresolved release-blocking review
contains its own `review_exception`: exact review dimensions, owner, reason,
tracking Bead, and review date. The offline checker rejects missing or excess
exception coverage; deferred and stale decisions remain separately bounded by
their decision owner and review date.

### Semgrep

Semgrep is used as an additional local SAST pass:

```bash
make semgrep       # Semgrep OSS, p/golang ruleset
make semgrep-json  # same scan, JSON output
make semgrep-pro   # requires Semgrep Pro Engine and semgrep login
```

Per the Semgrep CLI reference, `semgrep scan` is the local scan command and can
run registry rulesets such as `p/golang` without a Semgrep account. `semgrep ci`
uses Semgrep App policies, diff-aware CI behavior, and Pro analysis when the
CLI is logged in. The `--pro` flag enables interfile analysis and requires the
Pro Engine plus authentication.

Current accepted Semgrep warnings are documented in [SECURITY.md](../../SECURITY.md):
MD5 and SHA1 are implemented only for RFC 5880 authentication interoperability.
The matching Sonar `go:S4790` exception is limited by
`sonar-project.properties` to `internal/bfd/auth.go`; the rule remains active
for every other file. Sonar in-code resolution is not used because SonarQube
Cloud supports `sonar-resolve` only for C-family languages, not Go.

### Protobuf Workflow

Protobuf is managed by `buf`:

```bash
# After modifying api/bfd/v1/bfd.proto:
make proto-lint      # Lint definitions
make proto-gen       # Generate Go code to pkg/bfdpb/
make proto-breaking  # Check for breaking changes vs master
```

> **NEVER** edit files in `pkg/bfdpb/` manually -- they are generated by `buf generate`.

### Go 1.27 Baseline

GoBFD uses Go 1.27 while retaining the relevant Go 1.26 safety, performance,
and debugging APIs.

#### `testing/synctest` -- Deterministic Timer Tests

All BFD timer and detection timeout tests use `testing/synctest` for
virtual-time execution. An adjacent `time.Sleep` plus `synctest.Wait` is written
as `synctest.Sleep`; real E2E and interoperability waits remain context-bounded
wall-clock waits. See [FSM Tests](#fsm-tests-testingsynctest) above.

#### `os.Root` -- Sandboxed File Access

Configuration file loading uses `os.OpenRoot` to sandbox filesystem access within the config directory. This prevents path traversal attacks where a malicious config path could read arbitrary files:

```go
root, err := os.OpenRoot(filepath.Dir(path))
if err != nil { return nil, err }
defer root.Close()
f, err := root.Open(filepath.Base(path))
```

Applied in `config.Load` and `gobfd-haproxy-agent` `loadConfig`.

#### `errors.AsType[T]()` -- Type-Safe Error Matching

Server tests use the Go 1.26 generic error matcher instead of the two-step `errors.As` pattern:

```go
// Go 1.26 idiomatic
if connectErr, ok := errors.AsType[*connect.Error](err); ok {
    require.Equal(t, connect.CodeNotFound, connectErr.Code())
}
```

#### Goroutine Leak Diagnostics

Go 1.27 provides the `goroutineleak` runtime profile without an experiment
flag. Automated tests continue to use `go.uber.org/goleak` in six
concurrency-heavy packages. These are separate mechanisms; GoBFD does not
register the complete `net/http/pprof` handler set on its public metrics mux.

#### `runtime/trace.FlightRecorder`

An HTTP endpoint exposes the flight recorder for post-mortem trace capture. When enabled, the daemon continuously records the last N seconds of trace data, which can be dumped on demand for debugging latency spikes or deadlocks.

#### Swiss Tables

Go 1.26 introduced Swiss tables as the default `map` implementation. GoBFD's
discriminator lookup, FSM transition table, and session demuxing benefit from
improved cache locality. Go 1.27 removes the former `noswissmap` diagnostic
experiment, so it must not appear in build or benchmark commands.

#### HTTP and JSON Compatibility

Both public HTTP servers set the same explicit `MaxHeaderValueCount` while
retaining `ReadHeaderTimeout`, and parser-level tests cover the accepted and
rejected boundaries. Go 1.27 backs the existing `encoding/json` API with the v2
implementation; compatibility tests cover duplicate object members and invalid
UTF-8 in the CLI, Podman, FRR, and vulnerability-audit paths without matching
exact error strings.

### Code Conventions

| Rule | Description |
|---|---|
| **Errors** | Always wrap with `%w` and context: `fmt.Errorf("send control packet to %s: %w", peer, err)` |
| **Error matching** | Use `errors.Is`/`errors.As`, never string matching |
| **Context** | First parameter, never stored in struct |
| **Goroutines** | Sender closes channels; tie lifetime to `context.Context` |
| **Logging** | ONLY `log/slog` with structured fields |
| **Naming** | Avoid stutter: `package bfd; type Session` not `BFDSession` |
| **Imports** | stdlib, blank line, external, blank line, internal |
| **Interfaces** | Small, near consumers |
| **Tests** | Table-driven, `t.Parallel()` where safe, always `-race` |
| **FSM** | All transitions MUST match RFC 5880 Section 6.8.6 exactly |
| **Timers** | BFD intervals in MICROSECONDS per RFC -- never confuse with ms |

### Contributing

1. Open an issue to discuss the change before submitting a PR
2. Follow the existing code style (see `AGENTS.md` for conventions)
3. Add tests for new functionality (`go test ./... -race -count=1`)
4. Ensure `make lint` passes
5. Run `buf lint` if proto files are modified
6. Keep commit messages descriptive and concise

```bash
# Development loop
make up           # Start dev environment
# ... make changes ...
make all          # Build + test + lint

# For protocol changes:
make interop      # Verify interop with 4 peers

# For proto changes:
make proto-gen    # Regenerate Go code
make proto-lint   # Lint proto definitions
```

### Related Documents

- [01-architecture.md](./01-architecture.md) -- System architecture and package structure
- [05-interop.md](./05-interop.md) -- Interoperability testing
- [AGENTS.md](../../AGENTS.md) -- Full code conventions and commands

---

*Last updated: 2026-08-27*
