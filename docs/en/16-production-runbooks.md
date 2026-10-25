# Production Integration Runbooks

![Linux](https://img.shields.io/badge/Linux-Hosts-FCC624?style=for-the-badge&logo=linux&logoColor=black)
![Kubernetes](https://img.shields.io/badge/Kubernetes-DaemonSet-326CE5?style=for-the-badge&logo=kubernetes&logoColor=white)
![BGP](https://img.shields.io/badge/BGP-Fast_Failover-00ADD8?style=for-the-badge)
![Overlay](https://img.shields.io/badge/Overlay-Lab_Only-ea4335?style=for-the-badge)

> Runbooks for deploying and validating GoBFD in Linux, Kubernetes, BGP, and overlay-network environments.

These are operator procedures for the current `dev` source, not fresh execution
receipts or stable-release acceptance. Run drills only against an admitted,
owned lab and retain the exact source/image identities and observed results.
The current documentation work is local-only; publication remains on hold.

---

## Evidence Baseline

- RFC 5880 detection-time rules: in Asynchronous mode, local detection time is
  the remote Detect Mult multiplied by the negotiated receive interval.
- RFC 5881 single-hop encapsulation: UDP destination port 3784, source port in
  the dynamic range, and TTL/Hop Limit 255.
- RFC 7130 Micro-BFD: independent sessions per LAG member on UDP port 6784.
- RFC 8971 VXLAN BFD: Management VNI, inner UDP destination port 3784, and
  inner TTL/Hop Limit 255.
- RFC 9521 Geneve BFD: Geneve BFD for point-to-point Geneve tunnels with inner
  TTL/Hop Limit 255 and traffic-managed rate assumptions.
- The VXLAN/Geneve userspace IPv4 Format A paths have local packet-validation
  and identity checks. Bounded wire evidence does not qualify owner-specific
  dataplane integration, vendor forwarding or production deployment. These
  remain opt-in incomplete previews; see [Development](09-development.md#overlay-wire-qualification)
  and [`internal/netio/overlay_inner.go`](../../internal/netio/overlay_inner.go).
- Kubernetes host-network pods require `dnsPolicy: ClusterFirstWithHostNet`
  when `hostNetwork: true` is set.
- Vendor examples remain optional and must cite public vendor documentation.

## Deployment Profiles

| Profile | Use case | Assets |
|---|---|---|
| BGP failover | Linux routing host or lab pair with FRR/GoBGP | `deployments/integrations/bgp-fast-failover/` |
| Kubernetes daemon | One GoBFD instance per node with host networking | `deployments/integrations/kubernetes/` |
| Observability | Prometheus alerts and Grafana dashboard | `deployments/integrations/observability/` |
| Overlay lab | Isolated VXLAN/Geneve validation only | [ADR-0003](./adr/0003-linux-advanced-bfd-applicability.md) |

## Kubernetes Host-Network Daemon

Use this pattern when BFD peers are reachable from the node network namespace.
The DaemonSet is intentionally generic: it does not encode site topology,
private addresses, or vendor-specific assumptions.

Required properties:

- `hostNetwork: true`
- `dnsPolicy: ClusterFirstWithHostNet`
- Linux nodes only
- `NET_RAW` for BFD packet sockets
- `NET_ADMIN` for enabled socket options and interface operations
- Exec `gobfdctl` readiness over the Unix socket; metrics TCP liveness

Before applying manifests, provide `gobfd:local` and `gobgp:local` in every
eligible node's image store: the manifest sets `imagePullPolicy: Never`.
The example runner imports into its local k3s store only and retains a pod-label
mismatch and obsolete probes (`gobfd-qj0.8.2.8.7.1.4.1`). These are separate
from the direct manifest commands below.

Deploy:

```bash
kubectl apply -f deployments/integrations/kubernetes/manifests/
kubectl -n gobfd rollout status daemonset/gobfd --timeout=120s
kubectl -n gobfd get pods -o wide
```

Validate host networking and probes:

```bash
kubectl -n gobfd describe daemonset/gobfd
kubectl -n gobfd get endpoints gobfd
kubectl -n gobfd logs -l app.kubernetes.io/name=gobfd -c gobfd --tail=100
```

Operational notes:

- For static environments, commit BFD sessions in `configmap-gobfd.yml`.
- For dynamic environments, create sessions through `gobfdctl` or a controller
  after node or peer discovery.
- Keep the default Unix socket. Remote TCP management requires mTLS; a trusted
  network does not permit a non-loopback plaintext listener.
- The shipped ConfigMap exposes unauthenticated metrics on `:9100` in the host
  network. Restrict reachability at the node boundary before using the example.
- The GoBGP sidecar also exposes its plaintext API on node TCP/50051 because
  its arguments do not override the pinned upstream API listener or enable
  TLS. Restrict access before applying the manifest. The existing GoBGP TCP
  probes need a compatible replacement if the API is bound to loopback;
  `gobfd-qj0.8.2.8.7.1.4.1.1` tracks the source fix and live qualification.

The Kubernetes image import and live-cluster qualification remain incomplete.
These manifests and probes are examples, not an accepted cluster deployment.
Readiness demonstrates management access, not session or route convergence.

## BGP Fast-Failover Drill

Run the generic FRR/GoBGP integration:

```bash
make int-bgp-failover-up
# Optional: follow logs in a separate terminal.
# make int-bgp-failover-logs
```

Full example documentation:
[`deployments/integrations/bgp-fast-failover/README.md`](../../deployments/integrations/bgp-fast-failover/README.md).

Baseline checks:

```bash
podman exec gobgp-bgp-failover gobgp neighbor
podman exec gobgp-bgp-failover gobgp global rib
podman exec gobfd-bgp-failover gobfdctl session list
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd
```

Confirm BFD Up and route `10.20.0.0/24` present before the failure drill.
The commands below display observations; `make int-bgp-failover` performs the
separate assertion-based withdrawal/restoration gate.

Failure drill:

Confirm that the fixed container names below are the owned Compose topology.
The subshell restores the paused peer on exit, including a failed RIB query.
These fixed waits display observations; they do not assert a timing bound.

```bash
(
  set -eu
  trap 'podman unpause frr-bgp-failover' EXIT
  podman pause frr-bgp-failover
  sleep 2
  podman exec gobgp-bgp-failover gobgp global rib
)
sleep 5
podman exec gobgp-bgp-failover gobgp global rib
```

Expected result:

- BFD transitions from Up to Down before the normal BGP hold timer expires.
- GoBGP disables the affected peer.
- Route `10.20.0.0/24` returns to the GoBGP RIB after BFD Up; verify forwarding separately.

## Prometheus Alert Validation

Use an isolated host: this lab publishes host ports 9090/3000 and has default
Grafana credentials, as described in [Integrations](11-integrations.md#observability).
The drill below is a local procedure; this documentation edit does not record
a new execution. It requires `curl`, `jq`, `timeout` and a shell.

Start the observability stack:

```bash
make int-observability-up
```

Check targets and rules:

```bash
curl -fsS http://localhost:9090/api/v1/targets
curl -fsS http://localhost:9090/api/v1/rules
```

Confirm that the GoBFD target is healthy, the FRR session is Up and at least two
successful scrapes are recorded before the drill. The configured scrape and
evaluation intervals are both 15 seconds. Trigger a down transition and poll
the specific alert for at most 60 seconds; HTTP 200 alone is insufficient:

```bash
(
  set -eu
  trap 'podman start frr-observability' EXIT
  podman stop frr-observability
  timeout 60s sh -c '
    until curl --max-time 3 -fsS http://localhost:9090/api/v1/alerts |
      jq -e '\''any(.data.alerts[]; .labels.alertname == "BFDSessionDownTransition" and .state == "firing")'\''
    do
      sleep 2
    done
  '
)
```

Exit 0 confirms an observed firing alert; timeout is a failed drill. The trap
restarts the peer on both outcomes. Recheck BFD Up and healthy targets after
restart, then confirm alert clearance after the one-minute increase window
and a subsequent evaluation. Response fields follow the Prometheus
[alerts API](https://prometheus.io/docs/prometheus/latest/querying/api/#alerts).

Alert intent:

| Alert | Meaning |
|---|---|
| `BFDNoActiveSessions` | No active session objects; the shipped rule misses fresh-empty exporters (`gobfd-qj0.8.2.8.7.2.8`). |
| `BFDSessionDownTransition` | A session transitioned from Up to Down. |
| `BFDSessionFlapping` | A session changed state more than 3 times in 5 minutes. |
| `BFDAuthFailures` | RFC 5880 authentication verification failed. |
| `BFDPacketDrops` | Admitted peer packet drops increased; pre-session parser/demux outcomes use the separate delivery ledger. |

## Packet Verification

Use packet captures to verify RFC-visible behavior rather than relying only on
logs:

```bash
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd \
  -T fields \
  -e frame.time_relative \
  -e ip.src \
  -e ip.dst \
  -e udp.srcport \
  -e udp.dstport \
  -e ip.ttl \
  -e bfd.sta \
  -e bfd.detect_time_multiplier \
  -E header=y \
  -E separator=,
```

Expected single-hop values:

| Field | Expected |
|---|---|
| UDP destination port | 3784 |
| UDP source port | 49152-65535 |
| TTL/Hop Limit | 255 |
| BFD version | 1 |
| State progression | Down -> Init -> Up |

## Public Vendor Notes

GoBFD examples stay vendor-neutral. Vendor snippets require primary vendor
documentation.

Published optional notes:

- [Arista EOS BFD verification note](../../deployments/integrations/bgp-fast-failover/vendor-notes/arista-eos.md)

Use that note's primary references for the exact vendor release and profile.
An optional vendor command example does not qualify hardware offload, Micro-BFD
member enforcement or forwarding continuity in GoBFD.

## Open Production Gaps

| Gap | Status |
|---|---|
| Echo and Micro-BFD API/CLI creation | Implemented preview; transport/owner/RFC qualification remains separate |
| Dedicated VXLAN/Geneve create API/CLI | Not implemented; use opt-in declarative profiles |
| Kernel/OVS/OVN/Cilium/Calico/NSX overlay backends beyond `userspace-udp` | Future |
| OCI SBOM/provenance and release checksums | Workflow implemented; verify the exact candidate before publication; no signing step is configured |

---

*Reviewed against `dev` source: 2026-10-25; commands were inspected, not executed.*
