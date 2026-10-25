# GoBFD E2E Harness Contract

S10 E2E targets collect target-specific Podman evidence for daemon behavior,
protocol interoperability, Linux dataplane ownership, overlay boundaries, and
optional vendor profiles.

This page describes source contracts, not a current passing execution or
release acceptance. `Implemented` means a runner exists for the stated scope;
retain the exact revision, artifacts, skips and cleanup result for each run.
The current documentation work is local-only and does not authorize publication.
Русские руководства: [разработка](../../docs/ru/09-development.md),
[interop](../../docs/ru/05-interop.md) и
[эксплуатационные проверки](../../docs/ru/16-production-runbooks.md).

---

## Runtime Policy

| Rule | Requirement |
|---|---|
| Container runtime | Podman. |
| Go toolchain | Core, BGP fast-failover, HAProxy and observability testcontainers gates use the pinned dev container; aggregate orchestration and some integration targets invoke host `go run` in the current Makefile. |
| Host Go | Required by targets that invoke `go run` directly; use the repository-pinned toolchain or an explicit container invocation. |
| Compose project | Every dev stack uses `COMPOSE_PROJECT_NAME`; no fixed dev `container_name` is allowed. |
| Vendor NOS | `e2e-vendor` checks manifests and image availability only; live `interop-clab` is a separate admitted manual topology. |
| Packet capture | Required when a target claims wire behavior. |
| E2E orchestration | `test/cmd/e2ectl` and `test/internal/e2erunner`; the five aggregate targets do not use shell runners. |

## Target Matrix

| Target | Status | Input |
|---|---|---|
| `make e2e-core` | Implemented S10.2 | Go-owned testcontainers Podman topology, static auth, CLI, metrics, reload, packet capture. The retained `make e2e-core-testcontainers` compatibility target is the underlying gate. |
| `make int-bgp-failover` | Implemented S10.2 | Go-owned testcontainers GoBFD/GoBGP/FRR lifecycle, exact route withdrawal/restoration, packet evidence, and exact cleanup. The operational Compose example remains available through its `-up`, `-logs`, and `-down` targets. |
| `make e2e-routing` | Implemented S10.3 | FRR/BIRD3 BFD interop, GoBGP/ExaBGP BGP+BFD coupling, merged routing artifacts. |
| `make e2e-rfc` | Legacy Compose aggregate | RFC 7419/9384/9468/9747 scenarios; uncapped Compose builds, fixed names, best-effort capture/cleanup and no `-race`. Prefer the separate `make interop-rfc` testcontainers gate for candidate qualification. |
| `make e2e-overlay` | Implemented S10.4 | Isolated race-enabled VXLAN/Geneve userspace UDP identity, framing, lifecycle, and reserved backend checks. |
| `make e2e-linux` | Implemented S10.5 | Isolated rtnetlink/veth, kernel-bond, OVS, NetworkManager ownership checks. |
| `make e2e-vendor` | Implemented manifest evidence | Primary Arista/Nokia/SONiC/VyOS profiles, baseline FRR and deferred Cisco metadata; validates files and runtime declarations, inspects cached image availability, and never deploys vendor NOS. |

The matrix follows [`Makefile`](../../Makefile),
[`e2erunner`](../internal/e2erunner/runner.go) and
[`test_report.go`](../internal/e2erunner/test_report.go). A passing vendor
manifest check or RFC action-path scenario does not establish full vendor or
RFC wire qualification. Routing and legacy RFC aggregate tests also omit
`-race`; their suite selection differs from the full testcontainers gates.

## Artifact Layout

Every implemented S10 target writes artifacts to:

```text
reports/e2e/<target>/<run-id>/
```

Core, BGP fast-failover, HAProxy and observability report wrappers create
exclusive mode-0700 `run.*` directories. Linux, overlay, RFC and vendor runs
use `YYYYMMDDTHHMMSSZ`. Routing uses
`YYYYMMDDTHHMMSSNNNNNNNNNZ-PID` so the same identifier can safely own its
temporary merge container.

Expected common files after the target reaches its evidence-writing stage:

```text
go-test.json
go-test.log
containers.json
containers.log
environment.json
summary.md
```

Captured-packet targets also write:

```text
packets.pcapng
packets.csv
```

The overlay runner writes a synthetic `packets.csv` and no PCAP. Vendor and
isolated Linux ownership checks do not promise packet captures. Inspect errors
and missing files on failed startup; artifact names alone do not prove that
capture, assertions or cleanup completed. The detailed producer contracts live
in the selected test package and `test/internal/e2erunner/`.

The `e2e-core` target also writes:

```text
runtime/gobfd-a.yml
runtime/gobfd-b.yml
captures/bfd.pcapng
containers.err
pcap-summary.tsv
pcap-summary.err
packets.err
```

The `e2e-routing` target also writes:

```text
interop/go-test.json
interop/packets.pcapng
interop/packets.csv
interop-bgp/go-test.json
interop-bgp/packets.pcapng
interop-bgp/packets.csv
```

The `e2e-rfc` target also writes:

```text
packets.pcapng
packets.csv
```

The `e2e-overlay` target also writes:

```text
packets.csv
```

This CSV is a synthetic codec fixture summary, not captured packets. The
runner compiles in the dev container and executes the race binary in a separate
`--network none` container limited to 2 CPUs and 2 GiB, mounting only its report
directory and ignoring image-defined volumes. `e2e-overlay-test` uses the same
owner. The default test image comes from the active Compose dev service;
`E2E_OVERLAY_IMAGE` can select an explicit compatible image. Wire qualification
also requires a bounded external capture correlated with the test JSON, as
described in [development](../../docs/en/09-development.md#overlay-wire-qualification).

The `e2e-linux` target also writes:

```text
link-events.json
lag-backends.json
```

The `e2e-vendor` target also writes:

```text
vendor-profiles.json
vendor-images.json
skip-summary.json
```

## HTML Report Backlog

Future S10 report generation must add:

- `index.html` in every `reports/e2e/<target>/<YYYYMMDDTHHMMSSZ>/` directory.
- One shared JavaScript renderer for all E2E targets.
- One shared stylesheet aligned with the repository visual identity.
- Target status summary, duration, environment metadata, container state, packet
  evidence tables, artifact links, and collapsible logs.
- Standalone offline rendering without external network dependencies.

These are design requirements only. `scripts/e2e-report/` currently contains
the renderer contract; no shared HTML renderer is implemented there.

## CI Artifact Policy

| Gate | Trigger | Target Set | Artifact Name |
|---|---|---|---|
| PR-safe | `pull_request`, manual `profile=pr-safe` | `make e2e-core`, `make interop-testcontainers`, `make interop-bgp-testcontainers`, `make interop-rfc-testcontainers`, `make e2e-overlay` | `e2e-pr-safe` |
| Nightly | `schedule`, manual `profile=nightly` | `make e2e-routing`, `make e2e-rfc`, `make e2e-linux`, `cictl package-build`, `cictl package-lifecycle` | `e2e-nightly` |
| Vendor | manual `profile=vendor` | `make e2e-vendor` | `e2e-vendor` |

| Property | Requirement |
|---|---|
| Workflow | `.github/workflows/e2e.yml`. |
| Pull-request branches | `master`, `main`, `release/v*`; `dev` is not included. |
| Hosted runner | `ubuntu-26.04`; Ubuntu 24.04 Podman 4.9.3 rejects `ADD --checksum`. |
| Runtime | Podman through testcontainers-go for `e2e-core`; Podman and `podman compose` for retained targets. |
| Report root | `reports/e2e/**`. |
| Upload condition | `if: always()`. |
| Retention | 30 days. |
| Missing artifacts | Warning, not workflow failure. |
| Cleanup | `make down` runs under `always()` after every profile; its failure is ignored, so workflow success does not prove cleanup. |

## Cleanup Policy

| Resource | Requirement |
|---|---|
| Compose stacks | Compose-backed full-cycle targets clean with `down --volumes --remove-orphans`. |
| Containers | Test-owned containers use deterministic names scoped by project or target. |
| Networks | Test-owned networks are removed by the target cleanup phase. |
| Host interfaces | No S10 target may modify a host interface directly. |
| Failure diagnostics | Cleanup runs after diagnostic capture. |

The table states the intended ownership boundary. Testcontainers gates and the
routing controller assert selected cleanup results; legacy RFC/Linux helpers
also contain best-effort cleanup that only logs failures. Compose project names
do not isolate fixed names or overlapping subnets. Inspect ownership and the
target's resource limits before live use; deletion requires the applicable
approval. Do not run unqualified legacy/vendor images merely because a target
exists.

## Skip Policy

| Class | Meaning | Required Evidence |
|---|---|---|
| `unsupported-host-capability` | Kernel, namespace, or Podman capability is absent. | Capability command output. |
| `missing-image` | Required public image is unavailable. | Image reference and pull/inspect failure. |
| `licensed-vendor-image` | Vendor image cannot be redistributed or pulled by CI. | Image name and documented manual profile. |
| `manual-only-image` | Vendor image requires operator-provided build or import. | Image name and documented manual profile. |
| `manual-only` | Scenario requires operator-owned infrastructure. | Reason and manual command. |

Skips must be explicit and retained with the test output. Vendor manifest runs
write `skip-summary.json` and link it from `summary.md`; they do not execute
the topology or turn missing runtime evidence into a passing vendor result.

## Worktree Safety

The dev stack must be scoped by `COMPOSE_PROJECT_NAME`.

```bash
make dev-project
make up
make dev-ps
```

Expected properties:

| Property | Requirement |
|---|---|
| Project name | Defaults to the current checkout directory name. |
| Container naming | Compose-generated name under the selected project. |
| Mount | `/app` points to the active checkout. |
| Parallel worktrees | Separate names isolate dev stacks; fixed-name interop resources and overlapping subnets still require separate admission. |

## Podman API Usage

[`test/internal/podmanapi`](../internal/podmanapi/client.go) implements the
shared REST API helper used by the testcontainers gates. It covers:

| Operation | Requirement |
|---|---|
| Exec | Capture stdout/stderr and exit code. |
| Logs | Capture bounded logs for artifacts. |
| Inspect | Record container state and network data. |
| Pause/unpause | Drive failure/recovery tests. |
| Start/stop | Drive lifecycle tests. |

Environment-based selection checks a Unix `PODMAN_HOST`, then
`/run/podman/podman.sock`, then
`${XDG_RUNTIME_DIR}/podman/podman.sock`. Exact-endpoint callers can construct
a client directly; this helper does not force every legacy runner to use REST.

## Packet Capture Policy

| Protocol | Port | Artifact |
|---|---|---|
| BFD control | UDP 3784 | `packets.pcapng`, `packets.csv`. |
| BFD echo | UDP 3785 | `packets.pcapng`, `packets.csv`. |
| VXLAN | UDP 4789 | `packets.pcapng`, `packets.csv`. |
| Geneve | UDP 6081 | `packets.pcapng`, `packets.csv`. |

Packet assertions must use decoded fields where available and raw packet
counts only when the dissector lacks a field.

## Benchmark Policy

S10 E2E targets collect timing as diagnostic artifacts only. Benchmark
regression gates remain limited to stable hot-path Go benchmarks.

---

*Source review: 2026-10-25; no runtime execution in this documentation review.*
