# GoBFD E2E Harness Contract

S10 E2E targets define reproducible Podman-only evidence for daemon behavior,
protocol interoperability, Linux dataplane ownership, overlay boundaries, and
optional vendor profiles.

---

## Runtime Policy

| Rule | Requirement |
|---|---|
| Container runtime | Podman. |
| Go toolchain | Go commands run through the dev container or a target-specific Go container. |
| Host Go | Not valid as S10 evidence. |
| Compose project | Every dev stack uses `COMPOSE_PROJECT_NAME`; no fixed dev `container_name` is allowed. |
| Vendor NOS | Optional/manual unless public images and licenses allow CI execution. |
| Packet capture | Required when a target claims wire behavior. |

## Target Matrix

| Target | Status | Input |
|---|---|---|
| `make e2e-core` | Implemented S10.2 | GoBFD-to-GoBFD Podman topology, static auth, CLI, metrics, reload, packet capture. |
| `make e2e-routing` | Implemented S10.3 | FRR/BIRD3 BFD interop, GoBGP/ExaBGP BGP+BFD coupling, merged routing artifacts. |
| `make e2e-rfc` | Planned S10.4 | `make interop-rfc`. |
| `make e2e-overlay` | Planned S10.4 | VXLAN/Geneve backend-boundary tests. |
| `make e2e-linux` | Planned S10.5 | rtnetlink, kernel-bond, OVSDB, NetworkManager isolated tests. |
| `make e2e-vendor` | Planned S10.6 | `make interop-clab` and vendor-specific profiles. |

## Artifact Layout

Every implemented S10 target writes artifacts to:

```text
reports/e2e/<target>/<YYYYMMDDTHHMMSSZ>/
```

Required files:

```text
go-test.json
go-test.log
containers.json
containers.log
environment.json
summary.md
```

Wire-behavior targets also write:

```text
packets.pcapng
packets.csv
```

The `e2e-core` target also writes:

```text
runtime/gobfd-a.yml
runtime/gobfd-b.yml
captures/bfd.pcapng
pcap-summary.tsv
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

## Cleanup Policy

| Resource | Requirement |
|---|---|
| Compose stacks | Full-cycle targets clean with `down --volumes --remove-orphans`. |
| Containers | Test-owned containers use deterministic names scoped by project or target. |
| Networks | Test-owned networks are removed by the target cleanup phase. |
| Host interfaces | No S10 target may modify a host interface directly. |
| Failure diagnostics | Cleanup runs after diagnostic capture. |

## Skip Policy

| Class | Meaning | Required Evidence |
|---|---|---|
| `unsupported-host-capability` | Kernel, namespace, or Podman capability is absent. | Capability command output. |
| `missing-image` | Required public image is unavailable. | Image reference and pull/inspect failure. |
| `licensed-vendor-image` | Vendor image cannot be redistributed or pulled by CI. | Image name and documented manual profile. |
| `manual-only` | Scenario requires operator-owned infrastructure. | Reason and manual command. |

Skips must be explicit and visible in `summary.md`.

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
| Parallel worktrees | Separate `COMPOSE_PROJECT_NAME` values can run independently. |

## Podman API Usage

Go tests that control containers should use a shared Podman REST API helper.
The helper should cover:

| Operation | Requirement |
|---|---|
| Exec | Capture stdout/stderr and exit code. |
| Logs | Capture bounded logs for artifacts. |
| Inspect | Record container state and network data. |
| Pause/unpause | Drive failure/recovery tests. |
| Start/stop | Drive lifecycle tests. |

The helper should support `/run/podman/podman.sock` and rootless
`${XDG_RUNTIME_DIR}/podman/podman.sock`.

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

*Last updated: 2026-05-01*
