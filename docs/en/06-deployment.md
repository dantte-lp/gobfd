# Deployment

GoBFD runs on Linux with ordinary UDP, raw-socket and interface inventory support. The repository defines archive, DEB/RPM and OCI delivery; these definitions do not establish that a specific candidate is published or accepted. Sources: [release configuration](../../.goreleaser.yml), [application Containerfile](../../deployments/docker/Containerfile), [systemd unit](../../deployments/systemd/gobfd.service).

## Artifacts and installation

| Artifact | Declared contents | Boundary |
| :--- | :--- | :--- |
| Linux amd64/arm64 archive | Four binaries, example YAML, systemd unit | Accounts and host installation are operator-owned |
| DEB/RPM | Four binaries, noreplace config, unit, sysusers/tmpfiles and lifecycle support | Preserve existing configuration on upgrade |
| Debian trixie OCI | Four binaries, release labels/SBOM configuration | Verify exact manifest digest and candidate evidence |
| Development Compose | Source mount and build tools | Development environment; no daemon/file watcher |

The four application binaries are `gobfd`, `gobfdctl`, `gobfd-haproxy-agent` and `gobfd-exabgp-bridge`. Oracle Linux 10 is excluded from v1 OCI qualification; generic RPM packaging remains. Alpine, bookworm and executable scratch runtimes are outside the repository contract. The dependency/toolchain identities come from the [current manifests](../../go.mod) and digest-pinned Containerfiles, not an unqualified “latest” tag.

1. Select an accepted release/candidate and record its source SHA, artifact checksum and architecture.
2. Verify its published checksum/provenance and retain the verification output.
3. Install the package or archive according to the selected artifact; preserve the accepted binary and configuration for rollback.
4. Inspect `gobfd -version` and `gobfdctl version` before service activation.
5. Compare live configuration with [the configuration contract](03-configuration.md); provide explicit peer/local/interface bindings and protect authentication material.

No release download URL or mutable image tag substitutes for candidate verification.

## systemd

The shipped service starts `/usr/local/bin/gobfd -config /etc/gobfd/gobfd.yml`. It runs as `gobfd:gobfd`, joins `gobfd-admin` for the management socket, uses `Type=notify`, a 30-second watchdog and on-failure restart after five seconds. `ExecReload` sends SIGHUP; it does not confirm accepted reconciliation.

| Control | Shipped value | Consequence |
| :--- | :--- | :--- |
| Runtime directory | `/run/gobfd`, mode `0755` | Socket lifecycle is service-owned |
| UMask | `0077` | Restrict newly created files |
| Capabilities | `CAP_NET_RAW CAP_NET_ADMIN` | Networking privileges without full privileged execution |
| Filesystem | `ProtectSystem=strict`, config read-only, private tmp | Runtime directory/private tmp remain writable |
| Kernel/process | Kernel tunables/modules/control groups protected; restricted process visibility | Optional actuators need explicit integration review |
| Address families | Unix, IPv4, IPv6, netlink | Management, UDP/TCP and interface monitor |
| Descriptor limit | `16384` | Source budget `2*sessions+256`; qualify actual workload |

Sources: [unit](../../deployments/systemd/gobfd.service), [accounts](../../deployments/systemd/gobfd.sysusers), [directories](../../deployments/systemd/gobfd.tmpfiles).

Operators join `gobfd-admin`, not the daemon group that can read configuration secrets. Check the effective unit and socket permissions after package installation. Use the [runbook](16-production-runbooks.md) for activation, reload and rollback.

The optional [kernel-bond drop-in](../../deployments/systemd/gobfd-kernel-bond.conf) reopens one bond's sysfs slaves file and adds `CAP_DAC_OVERRIDE`. This broadens readable root-owned files. The bond must exist at startup. Native acceptance requires an actual member write/readback and forwarding failure/recovery test on a host with the bonding module. Dry-run success or the drop-in's presence does not establish enforcement acceptance.

## Podman Compose

The supported path uses `podman compose` and the checksum-pinned Go Compose provider. Python `podman-compose` and direct provider invocation are outside the contract. The bootstrap owns the provider pin/checksum; do not copy a version from an older guide.

```sh title="compose-provider.sh"
go run ./test/cmd/toolbootstrap compose --install-dir "$HOME/.local/bin"
export PODMAN_COMPOSE_PROVIDER="$HOME/.local/bin/docker-compose"
export PODMAN_COMPOSE_WARNING_LOGS=false
export DOCKER_BUILDKIT=0
podman compose version
podman compose -f deployments/compose/compose.yml config
```

Bootstrap runs on the host with the repository toolchain. It requires a real, safely owned install-directory chain and verifies the provider before atomic publication. `DOCKER_BUILDKIT=0` keeps Podman-backed builds on the supported API path. Sources: [bootstrap](../../test/cmd/toolbootstrap), [Makefile](../../Makefile).

Development targets:

```sh title="development.sh"
make up
make build
make test-run RUN=TestFSMTransition PKG=./internal/bfd
```

The [production Compose specimen](../../deployments/compose/compose.yml) uses host networking, read-only configuration, `NET_RAW`/`NET_ADMIN` and loopback Prometheus/Grafana listeners. It is a source specimen, not a site deployment receipt. Inspect image identities, socket group availability, host port conflicts and effective permissions before activation. Existing fixed names or shared volumes require explicit ownership review.

Host networking does not share filesystem paths. This Compose file does not mount `/run/gobfd` onto the host; inspect its default management socket inside the container:

```sh
podman exec gobfd gobfdctl session list
```

Host CLI access requires a separately reviewed socket-directory mount/permissions or explicitly configured TCP management transport.

## Kubernetes

The [DaemonSet specimen](../../deployments/integrations/kubernetes/manifests/daemonset.yml) selects Linux, `hostNetwork: true`, `ClusterFirstWithHostNet`, runtime-default seccomp and `NET_RAW`/`NET_ADMIN`. Both containers use local images with `imagePullPolicy: Never`; distribute images to every eligible node's runtime before apply. Node-local port conflicts and unauthenticated host-network metrics need explicit network policy outside a pod-only assumption.

Readiness executes `gobfdctl session list` over the socket; liveness connects to metrics TCP. Those probes do not prove BFD Up or BGP convergence. GoBGP API/probes use loopback. Before acceptance, require successful image import on every selected node, correct runner label/probe selection, rollout and failure/recovery under declared CPU/memory limits in the actual cluster. The specimen does not supply that live-cluster receipt.

After admitting the target cluster/context and importing both local images on every selected node:

```sh title="kubernetes-rollout.sh"
kubectl apply -f deployments/integrations/kubernetes/manifests/
kubectl -n gobfd rollout status daemonset/gobfd --timeout=120s
kubectl -n gobfd get daemonset gobfd
kubectl -n gobfd get pods -l app.kubernetes.io/name=gobfd -o wide
kubectl -n gobfd describe daemonset/gobfd
kubectl -n gobfd logs -l app.kubernetes.io/name=gobfd -c gobfd --tail=100
```

Require desired/current/ready counts to match and one intended pod placement per eligible node. Inspect each pod's restart/OOM and container status. For every listed pod, substitute its exact name in the readback:

```sh
kubectl -n gobfd exec POD_NAME -c gobfd -- /bin/gobfdctl session list
```

Require the declared peer state and a separate route/forwarding check per node. Preserve the former ConfigMaps/DaemonSet and image identities for rollback; do not delete shared namespace/resources as a rollback shortcut. Command reference: [Kubernetes DaemonSet procedure](https://kubernetes.io/docs/tasks/manage-daemon/create-daemon-set/).

## Resource and acceptance gates

Set `GOMEMLIMIT` from measured resident memory, packet rate, session count and container limit. Keep default `GOGC` unless measurements justify a change. A heap target does not cap RSS; descriptor, kernel buffer, goroutine, trace and peer-process costs remain.

Before production acceptance, retain:

1. Exact source/artifact identities, configuration and rollback copy.
2. Management permission and unauthenticated-listener reachability checks.
3. Negotiated peer state/timers and packet-capture checks.
4. Consumer route/backend response and independent forwarding probes under failure and recovery.
5. Resource/OOM/restart behavior at the declared workload.
6. Relevant [integration evidence](11-integrations.md), [security gates](15-security.md) and remaining preview limitations.

Local tests, an accepted configuration and a healthy HTTP listener each prove only their measured boundary.
