# E2E target inventory

Targets are source-defined execution paths, not a list of accepted deployments. [Makefile](../../Makefile), [e2erunner](../internal/e2erunner/) and [integrationrunner](../internal/integrationrunner/runner.go) own dispatch. Domain labels do not imply one target invokes another.

## Core and independent interop

| Target | Runtime/input | Evidence | Remaining boundary |
|:---|:---|:---|:---|
| e2e-core / e2e-core-testcontainers | Race-enabled Podman testcontainers; daemon pair, static auth, CLI/metrics/reload; 172.30.10.0/24 | reports/e2e/core/run.*; tests, captures, exact cleanup | Selected process scenarios |
| test-integration | Dev-container in-process Manager/API/packet bridge | Verbose race test output | No daemon/UDP topology |
| interop / interop-testcontainers | Race-enabled four peers, meticulous/crafted/TTL profiles; base 172.20.0.0/24 | Captures, config/receipt sidecars, exact resource cleanup | Final candidate/peer matrix still requires actual run |
| interop-bgp / interop-bgp-testcontainers | Race-enabled GoBGP/FRR/BIRD3/ExaBGP; 172.21.0.0/24 | BFD/BGP/route/capture evidence | Tested scenarios only |
| interop-rfc / interop-rfc-testcontainers | Race-enabled RFC topology; 172.22.0.0/24 | Test/capture/container evidence | 9384 action path is not Cease/10 |

TestSameKeyFRRTestcontainers is a separate FRR-only retirement gate with one API-owned session. It retains old/new wire epochs, claim conflicts, sender lifetime and peer recovery; it does not qualify every peer or native scale.

## Aggregates

The five aggregate launchers build bin/e2ectl in a socket-free pinned compiler and execute it on the host. One checkout directory lock covers build and runtime.

| Target | Runtime/input | Evidence/cleanup | Remaining boundary |
|:---|:---|:---|:---|
| e2e-routing | Compose base/BGP suites, 172.20/21 networks | Joined suite artifacts and merged PCAP; exact owner snapshots | Omits race and some independent profiles |
| e2e-rfc | Guarded Compose with race, 172.22 network | Test JSON/log, required PCAP/CSV, exact container/image cleanup | Fixed container names; 9384 action path is not Cease/10 |
| e2e-overlay | Isolated prebuilt race test, network none, 2 CPU/2 GiB/512 PIDs | JSON/log, synthetic CSV, runtime receipts and verified ID removal | Capture lifecycle remains external |
| e2e-linux | Prebuilt race daemon/tests, isolated netns, 2 CPU/2 GiB/512 PIDs | Link/LAG/pending-lifecycle receipts | Fake sysfs/owner-policy checks do not qualify host integration |
| e2e-vendor | Shared manifest/policy and image evidence | profiles/images/skip JSON; no NOS topology | Five retained-unavailable NOS profiles never execute |
| interop-clab | Independently owned FRR-only Containerlab dual stack | Typed state, captured frames, private exact-ID receipts | Native lifecycle/compiler-engine acceptance open |

Routing cleanup uses interopproject.Controller locks, fixed-name/label validation and exact resource snapshots. Its merge container has unique non-Compose ownership. Containerlab retains foreign-collision checks and historic cleanup receipts; cached NOS images do not enter execution discovery.

Overlay and Linux runtime cleanup in [runtime.go](../internal/e2erunner/runtime.go) verifies `io.gobfd.*` label ownership, records `runtime-plan.json`, `runtime-container.json`, `containers.json` and `containers.log`, removes the exact container ID with `podman rm -f`, verifies absence via `podman container exists` and fails the target on cleanup or readback error. The overlay launcher provides no external capture startup/readiness/join hook; see [overlay wire qualification](../../docs/en/09-development.md#overlay-wire-qualification).

## Integration examples

| Target | Path | Output | Limit |
|:---|:---|:---|:---|
| int-bgp-failover / -testcontainers | Assertion-based Podman gate; GoBFD/GoBGP/FRR/tshark, 172.22 network | Run report, route withdrawal/restoration, capture, exact cleanup | Separate from e2e-routing invocation |
| int-bgp-failover-up/-logs/-down | Operational Compose example, guarded controller | Runtime observations | Requires owned lifecycle |
| int-haproxy-testcontainers | Assertion-based report gate | Health behavior, capture and cleanup | Selected HAProxy path |
| int-observability-testcontainers | Assertion-based report gate | Alert firing and recovery | Selected metric/alert path |
| int-haproxy | Guarded Compose demo via host Go runner | HTTP and `gobfdctl session list` assertions; warning-only capture | No S10 report directory |
| int-observability | Guarded Compose demo via host Go runner | Displayed metrics/alerts | Warning-only HTTP/capture observations; no S10 report |
| int-exabgp-anycast | Guarded Compose, host Go runner | Announce/withdraw/restore assertions | Not invoked by e2e-routing; no S10 report directory |
| int-k8s | Existing k3s, host Go runner and lease-protected `containerd` gRPC import | Active Ready pod, CLI and `gobfd_gobgp_endpoint_compatible` assertions | Existing local single-node cluster only |

Example -up/-logs/-down variants follow [Makefile](../../Makefile); Kubernetes has -up/-down only. `int-k8s-down` deletes namespace `gobfd` while preserving the cluster; require exact ownership and deletion authorization. Guarded Compose lifecycles, including ExaBGP, remove run-owned service and builder-stage images on cleanup.

## CI and results

Use [harness contract](README.md#ci-selection) for exact profile selection. A green workflow cannot establish absent artifacts, ignored cleanup failures, unexecuted vendor profiles or final reference-load acceptance.

[English guide](../../docs/en/05-interop.md), [русское руководство](../../docs/ru/05-interop.md).
