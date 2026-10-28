# Development

Development uses the next-line `dev` branch and the repository-pinned Podman toolchain. The executable contract is [AGENTS.md](../../AGENTS.md); commands are defined in [Makefile](../../Makefile). A successful source check does not qualify a daemon, network or release.

## Prepare a checkout

1. Select the branch for the change. Use `dev` for features and `release/vMAJOR.MINOR` for a supported-line fix; target that same line in the pull request. Preserve the v0.6 GoBGP v3 contract.
2. Install Podman, Git, GNU Make and util-linux `flock`. Configure the pinned Go Compose provider by the [deployment procedure](06-deployment.md#podman-compose).
3. Give concurrent checkouts distinct `COMPOSE_PROJECT_NAME` values. The default is the sanitized directory basename; equal basenames collide.
4. Start the development environment and inspect its project:

```bash title="Development environment"
make up
make dev-project
make dev-ps
```

`go.mod`, `tools/go.mod` and `deployments/docker/Containerfile.dev` pin Go 1.27.3. Build/test/lint commands execute in the dev container. `dev-ensure` validates its `/app` bind mount and can recreate a container whose checkout changed. `make up` can build images; it is not a read-only preflight.

## Daily change procedure

1. Record the change scope, acceptance criteria, dependencies and review ownership.
2. Read callers, tests, configuration and public contracts affected by the change.
3. Change handwritten sources; regenerate public protobuf output instead of editing `pkg/bfdpb/`.
4. Run the focused race test, then the routine gate:

```bash title="Focused and routine checks"
make test-run RUN=TestFSMTransition PKG=./internal/bfd
make lint-docs
make verify
```

5. Run the relevant [interop gate](05-interop.md) for protocol, transport, authentication or wire changes. Run benchmarks for hot-path or toolchain changes.
6. Update both language guides and both changelogs when user-visible behavior changes. Record exact source/binary identities, executed checks, skips and remaining acceptance in the pull request.
7. Request review from the applicable owners in [.github/CODEOWNERS](../../.github/CODEOWNERS). Follow [CONTRIBUTING.md](../../CONTRIBUTING.md) and the [release guide](10-changelog.md).

`make all` runs build, race tests, gopls, lint and documentation checks. `make verify` adds protobuf lint and the controlled vulnerability audit. It excludes live interoperability, package lifecycle, reference load and publication acceptance.

## Lifecycle and build targets

| Target | Behavior |
|:---|:---|
| `up`, `dev-ensure` | Create/start the selected dev stack; verify the source mount |
| `dev-project`, `dev-ps` | Print project identity or stack state |
| `logs` | Follow dev logs |
| `down`, `restart` | Stop, or stop then start the dev stack |
| `build` | Build all four binaries with version, commit and UTC build-time ldflags |
| `all`, `verify` | Run the aggregate checks described above |
| `versions` | Print installed tool identities |

## Test targets

| Target | Scope |
|:---|:---|
| `test`, `test-v` | All default packages, race detector, one uncached run; optional verbose output |
| `test-run RUN=NAME PKG=PATH` | Focused race test |
| `fuzz FUNC=NAME PKG=PATH` | One fuzz target, 60-second fuzz budget |
| `test-integration` | In-process Manager/API/packet-bridge tests under the `integration` tag |
| `testcontainers-smoke` | Required Podman-provider smoke, `testcontainers` tag |
| `coverage`, `coverage-func` | Race coverage; HTML or function summary |
| `test-report` | Race tests and JUnit/JSON/HTML under `reports/tests/` |
| `report-all` | Test report, benchmark snapshot and comparison |
| `benchmark*`, `profile` | [Benchmark collection and profiles](12-benchmarks.md) |

Default `go test` does not execute every build-tagged suite. An in-process bridge is not a UDP or process-lifecycle test. Keep virtual-time `testing/synctest` tests for deterministic timer logic and wall-clock bounded waits for external peers.

Fuzz targets cover Control, VXLAN, Geneve and inner packet codecs. Select the exact function from the package before running the target. Preserve failing corpus entries and the corresponding regression test; do not treat a bounded fuzz run as exhaustive validation.

## Interop and integration targets

| Target family | Entry point and boundary |
|:---|:---|
| Four peers | `interop` aliases `interop-testcontainers`; FRR/BIRD3/Holo/Thoro, crafted cases |
| BGP coupling | `interop-bgp` aliases `interop-bgp-testcontainers` |
| RFC scenarios | `interop-rfc` aliases `interop-rfc-testcontainers` |
| Retained Compose stacks | `interop[-bgp,-rfc]-up/-test/-logs/-down`; different suite/cleanup contracts |
| Containerlab | `interop-clab-bootstrap`, `interop-clab-up/-test/-down`, `interop-clab`; owned FRR-only execution |
| Core | `e2e-core`, `e2e-core-testcontainers` |
| Aggregates | `e2e-routing`, `e2e-rfc`, `e2e-overlay`, `e2e-linux`, `e2e-vendor` |
| Aggregate subtargets | `e2e-routing-test`, `e2e-rfc-test`, `e2e-overlay-test`, `e2e-linux-test`, `e2e-vendor-test` |
| BGP example | `int-bgp-failover[-testcontainers,-up,-logs,-down]` |
| HAProxy example | `int-haproxy[-testcontainers,-up,-logs,-down]` |
| Observability example | `int-observability[-testcontainers,-up,-logs,-down]` |
| ExaBGP example | `int-exabgp-anycast[-up,-logs,-down]` |
| Kubernetes example | `int-k8s[-up,-down]`; existing cluster and namespace ownership required |
| Inspection | `e2e-help`, `interop-capture`, `interop-pcap`, `interop-pcap-summary` |
| Compatibility alias | `integration` aliases `interop` |

The [E2E target inventory](../../test/e2e/targets.md) distinguishes assertion-based gates from retained demos and manifest-only checks. Never infer vendor execution from `e2e-vendor`, nor complete RFC coverage from a named RFC action-path test. Cleanup targets can delete owned resources and require the applicable authorization.

### Overlay wire qualification

1. Run `make e2e-overlay` in an isolated Linux namespace with UDP 4789/6081 available.
2. Retain Go test JSON for both inner tuples on the same outer endpoints/VNI. Assertions cover bidirectional userspace UDP, TTL 255, identity/malformed rejection, cancellation, reuse and Close.
3. Capture loopback UDP 4789/6081 with the existing tshark image in that test namespace. Bound capture with `-a duration:20 -a filesize:1024`.
4. Correlate decoded frames and discriminators with accepted/rejected test outcomes; retain command, image IDs, limits and hashes.
5. Replay the six overlay fuzz targets with race detection, two workers, a 20-second budget and an outer timeout.

The generated `packets.csv` is a codec fixture summary. It is not a PCAP. These checks do not qualify vendor or owner-specific overlay backends.

### Package lifecycle qualification

Execute in the pinned SDK with the verified UPX prerequisite on PATH:

```bash
go run ./test/cmd/cictl package-build
go run ./test/cmd/cictl package-lifecycle
```

`package-build` uses the release GoReleaser pin, test version `v0.0.0` and native RPM lifecycle finalization. It disables publication stages and requires a Git checkout. `package-lifecycle` requires the Podman API and exercises a DEB in systemd-enabled Debian: start, sandbox, watchdog hold, reload with the same PID, restart after SIGKILL, stop with a dead peer, unit verification, enabled/disabled upgrade idempotence and active removal. Upgrade fixtures change only the package control version; they do not qualify upgrades between daemon implementations.

Build limits are 2 CPU/2 GiB. Runtime limits are 1 CPU/1 GiB, no swap, 1024 PIDs and a private cgroup namespace; the test verifies effective limits. SYS_ADMIN, SYS_PTRACE and namespace/security exceptions are specific to this disposable systemd fixture. Debian test packages remain mirror-resolved. Evidence belongs to `reports/e2e/package-lifecycle/`; RPM generation is separate from DEB runtime acceptance.

## Quality, protobuf and dependency targets

| Target | Contract |
|:---|:---|
| `lint`, `lint-ci` | Canonical Go-owned linter with base and build-tag profiles |
| `lint-fix` | Apply linter fixes; inspect the resulting diff |
| `gopls-check` | Bounded diagnostics, including the Darwin dependency-generator profile |
| `lint-md`, `lint-yaml`, `lint-spell`, `lint-docs` | Markdown, YAML, selected English spelling; combined documentation gate |
| `lint-commit MSG='fix(bfd): reject invalid input'` | Validate one Conventional Commit subject |
| `semgrep`, `semgrep-json`, `semgrep-pro` | Selected host Semgrep executable; Pro needs its own engine/access |
| `vulncheck`, `osv-scan` | Controlled govulncheck + OSV audit |
| `vulncheck-strict`, `osv-scan-strict` | Raw scans without project exceptions |
| `proto-gen`, `proto-lint` | Regenerate or lint protobuf definitions |
| `proto-breaking`, `proto-update` | Compare against local master, or update Buf dependencies |
| `dependency-inventory`, `dependency-inventory-check` | Generate the selected inventory or check it offline |
| `tidy`, `download` | Tidy or download runtime dependencies |
| `clean` | Remove daemon/CLI and Go caches; retains bridge binaries |

CI checks protobuf drift and uses the exact fetched PR base commit for breaking checks; local `proto-breaking` uses `master`. `tools/go.mod` owns developer tools. Keep runtime and tools graphs separate. Inventory generation records observations; its offline check proves agreement with the snapshot, not upstream currency, security or release adoption. Advisory exceptions have exact module, scanner, reachability, owner and expiry boundaries; see the [risk register](reference/dependency-risk.md).

## Tool execution boundaries

| Entry point | Compiler/runtime |
|:---|:---|
| Routine Make targets | Go inside selected dev container |
| Five E2E aggregates | Socket-free digest-pinned compiler builds ignored `bin/e2ectl`; host runs that exact binary |
| Five Containerlab launchers | Fresh private controller build without engine socket; separate race-payload compiler and Debian runtime |
| Retained `interopctl`/integration bootstrap and `benchmark-report` | Direct `go run`; require explicit pinned container execution or the exact pinned host compiler |
| `lint-ci` and CI commands | Direct Go entry points intended for the pinned CI container |
| `semgrep*` | Selected host executable |

The E2E aggregate directory lock covers dev recreation, build and execution; acquisition waits 120 seconds. Compiler bounds are 2 CPU/2 GiB/512 PIDs, read-only source, writable binary output. A dry Make run prints delegation without creating that directory. Containerlab help/dry-run/down still build a fresh controller; stale binaries are never fallback.

Repository tooling is Go-owned. Do not add Python environments, repository Bash scripts or shell programs to CI. The sole Python runtime exception is checksum-pinned upstream ExaBGP inside its Debian peer image. Package-owned vendor startup internals are not repository launchers. Use [CI and release reference](reference/automation.md) for command contracts.

## Code and runtime rules

| Area | Requirement |
|:---|:---|
| Errors | Wrap with context and `%w`; use `errors.Is`/`errors.As` |
| Concurrency | Context first, goroutines bound to cancellation, sender closes channels |
| Logging | Structured `log/slog`; preserve sensitive-data boundaries |
| Structure | Small consumer-owned interfaces; standard/external/internal import groups |
| Protocol | [RFC 5880 §6.8.6](../../docs/rfc/rfc5880.txt), microsecond intervals, explicit procedure evidence |
| Memory | Measure the named hot path; no blanket GC or scale claims |
| Unsafe/generated code | No handwritten `unsafe`; regenerate protobuf |
| Containers | Digest-pinned Debian trixie; preserve supported historical contracts |

Keep the standard-version vet check. Use `synctest.Sleep` only inside deterministic test bubbles. Keep JSON compatibility tests, named HTTP header bounds and existing pprof isolation. The project does not install a global GOMAXPROCS override; qualify GOMEMLIMIT and retain default GOGC unless measurements justify another value. Go release APIs and removed compatibility settings are governed by [the pinned baseline contract](../../AGENTS.md), not illustrative snippets.

## Troubleshooting

| Symptom | Check | Action |
|:---|:---|:---|
| Wrong checkout in tests | `make dev-project`, `make dev-ps`, selected mount | Assign a distinct project and run `dev-ensure` |
| Compose fails or builds use Buildx | Provider selection and DOCKER_BUILDKIT | Repeat [provider bootstrap](06-deployment.md#podman-compose) |
| Default tests pass, tagged code fails | Exact build-tag profile | Run the canonical lint wrapper and selected E2E gate |
| Reports disagree with logs | Run ID, revision, timestamps, expected files | Preserve raw failure output; use a fresh evidence directory |
| Dependency scan fails | Reachability, exact exception and expiry | Fix the dependency or obtain a bounded recorded disposition |
| Cleanup is incomplete | Recorded IDs, ownership labels, target logs | Recover through the owning controller; preserve foreign resources |

## Related references

[Interoperability](05-interop.md), [benchmarks](12-benchmarks.md), [runtime measurement](14-performance-analysis.md), [E2E harness](../../test/e2e/README.md), [Smallstep fixture](../../test/stepca/README.md).
