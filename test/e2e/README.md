# E2E harness

The harness collects target-specific daemon, routing, RFC, overlay, Linux ownership and vendor-policy evidence. Source contracts live in [Makefile](../../Makefile), [e2ectl](../cmd/e2ectl/main.go), [e2erunner](../internal/e2erunner/) and the selected test packages. A runner or report file does not establish a passing candidate.

## Run a gate

1. Read the [target inventory](targets.md) and [interop guide](../../docs/en/05-interop.md). Select the required protocol/runtime boundary.
2. Prepare the pinned Podman environment. Assign a unique COMPOSE_PROJECT_NAME; verify the dev mount belongs to this checkout.
3. Check fixed names, subnets, privileges, limits and test-owned resources.
4. Run the selected full-cycle Make target.
5. Retain named results, explicit skips, artifacts, exact source/image/binary identities and cleanup readback. Do not substitute diagnostics for assertions.

```bash title="Core test environment"
make up
make dev-project
make e2e-core
```

The five routing/rfc/overlay/linux/vendor aggregates compile ignored bin/e2ectl without an engine socket, then run that exact binary on the host. A directory flock covers dev recreation, build and execution, with a 120-second acquisition bound. The compiler has read-only source, writable binary output and limits of 2 CPU/2 GiB/512 PIDs. Other report gates use the pinned dev container. Retained bootstrap/integration entry points can still call host Go directly.

## Artifact layout

The root is reports/e2e/TARGET/RUN_ID. Core/BGP/HAProxy/observability use private mode-0700 run.* directories. Linux/overlay/RFC/vendor use UTC second timestamps; routing adds nanoseconds and PID.

| Common artifact | Meaning |
|:---|:---|
| go-test.json / go-test.log | Producer test stream and readable log |
| containers.json / containers.log | Target-scoped state and bounded logs |
| environment.json | Run/environment identity |
| summary.md | Result and evidence links |
| packets.pcapng / packets.csv | Capture and decoded rows where produced |

Files can be absent after failed startup. Inspect producer exit and actual nonempty contents. Overlay CSV is synthetic; vendor/Linux ownership tests do not promise PCAP.

| Target | Additional artifacts |
|:---|:---|
| core | runtime/gobfd-a.yml, runtime/gobfd-b.yml, captures/bfd.pcapng, containers.err, pcap-summary.tsv, pcap-summary.err, packets.err |
| routing | interop/ and interop-bgp/ JSON, packet captures and CSV; merged top-level evidence |
| rfc | Retained packet PCAP/CSV when capture succeeds |
| overlay | Synthetic packets.csv |
| linux | link-events.json, lag-backends.json, pending-daemon-ipv4/ipv6 JSON/log |
| vendor | vendor-profiles.json, vendor-images.json, skip-summary.json |

## Runtime boundaries

| Target | Runtime behavior | Limit |
|:---|:---|:---|
| core | Podman testcontainers daemon pair, static auth, CLI/API, reload, metrics and capture | Selected daemon scenarios |
| routing | Ownership-checked Compose base/BGP suites and merged evidence | No race; different filters from independent full gates |
| rfc | Retained Compose RFC suite | Fixed names, uncapped builds, no race; best-effort capture/cleanup |
| overlay | Prebuilt race tests in network-none runtime, 2 CPU/2 GiB, report mount only | UDP/codec/lifecycle; CSV is synthetic |
| linux | Prebuilt race daemon/test/test2json in network-none runtime, 2 CPU/2 GiB/512 PIDs, NET_ADMIN/NET_RAW | Disposable netns ownership, not host dataplane acceptance |
| vendor | Manifest/policy checks and admitted image evidence | Retained NOS profiles never execute |

Overlay uses the selected dev image unless E2E_OVERLAY_IMAGE overrides it. Real wire qualification needs bounded external capture correlated with test JSON; see [overlay procedure](../../docs/en/09-development.md#overlay-wire-qualification).

Linux uses E2E_LINUX_BFD_BINARY for the prebuilt daemon. Pending-lifecycle checks cover missing interface/address, active address loss/recovery, down/up, interface recreation, invalid candidate rejection and permanent bind failure requiring reload. Native gRPC health/session calls and IPv4/IPv6 receipts retain binary hash, toolchain/kernel, resources and joined shutdown. These fixtures do not qualify peer wire, reference load or 24 hours.

Vendor policy is owned by [profiles.json](vendor/profiles.json). Five NOS profiles remain retained-unavailable regardless of cache contents. Owned FRR Containerlab execution is a separate [guarded lifecycle](../../docs/en/05-interop.md#containerlab-frr-procedure).

## CI selection

| Profile | Selected targets | Artifact |
|:---|:---|:---|
| PR-safe | core, interop-testcontainers, interop-bgp-testcontainers, interop-rfc-testcontainers, overlay | e2e-pr-safe |
| Nightly | routing, legacy rfc, linux, cictl package-build/package-lifecycle | e2e-nightly |
| Vendor manual | vendor manifest evidence | e2e-vendor |

[Workflow](../../.github/workflows/e2e.yml) selects PR targets master/main/release-v patterns; dev is not a PR trigger. Manual profiles and nightly schedule are separate. Artifacts upload under always(), retain 30 days and warn when missing. Cleanup also runs under always() with continue-on-error: workflow success is not cleanup proof. The hosted runner pin belongs to the workflow.

## Resource ownership and cleanup

Testcontainers and routing controllers assert selected exact cleanup results. Legacy paths can only log cleanup failures. Read the selected owner contract before invoking cleanup.

Preserve foreign containers/networks/images, shared volumes and host interfaces. Project names do not isolate fixed-name containers or overlapping subnets. Use exact IDs/labels and approved owned recovery; do not broaden deletion after a failed target.

The shared [Podman API helper](../internal/podmanapi/client.go) records bounded exec/log/inspect and lifecycle results. It selects Unix PODMAN_HOST, then /run/podman/podman.sock, then XDG_RUNTIME_DIR/podman/podman.sock. Exact-endpoint callers can bind a client directly. Engine socket access grants mutation authority.

## Skips

| Class | Required evidence |
|:---|:---|
| unsupported-host-capability | Capability and namespace/runtime check |
| missing-image | Exact reference and acquisition/inspection failure |
| licensed-vendor-image | Supplied artifact/licensing boundary and retained profile |
| manual-only-image | Operator build/import requirement |
| manual-only | Infrastructure requirement and bounded manual procedure |

Record every skip with its reason and effect on acceptance. A manifest-only pass does not replace unavailable NOS execution. GOBFD_REQUIRE_PODMAN makes required runtime absence fail in the dedicated gates.

## Packet and timing evidence

Control UDP 3784/4784, Echo 3785, VXLAN 4789 and Geneve 6081 require protocol-appropriate bounded captures for wire claims. Prefer decoded assertions; packet counts alone cannot prove acceptance/rejection. Correlate captures with test outcomes and retain checksums.

E2E timings are diagnostics. [Benchmark](../../docs/en/12-benchmarks.md) warning policy and [runtime qualification](../../docs/en/14-performance-analysis.md) remain separate.

No shared HTML renderer is implemented in [scripts/e2e-report](../../scripts/e2e-report/README.md). Existing JSON/log/Markdown artifacts remain canonical producer output.

Russian: [разработка](../../docs/ru/09-development.md), [совместимость](../../docs/ru/05-interop.md), [измерения](../../docs/ru/14-performance-analysis.md).
