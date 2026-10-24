# Production Integration Runbooks

![Linux](https://img.shields.io/badge/Linux-Hosts-FCC624?style=for-the-badge&logo=linux&logoColor=black)
![Kubernetes](https://img.shields.io/badge/Kubernetes-DaemonSet-326CE5?style=for-the-badge&logo=kubernetes&logoColor=white)
![BGP](https://img.shields.io/badge/BGP-Fast_Failover-00ADD8?style=for-the-badge)
![Overlay](https://img.shields.io/badge/Overlay-Lab_Only-ea4335?style=for-the-badge)

> Runbooks for deploying and validating GoBFD in Linux, Kubernetes, BGP, and overlay-network environments.

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
- The current VXLAN/Geneve userspace receive paths are unsafe/incomplete
  previews: inner-packet validation and complete tunnel-session identity
  binding are not production-qualified.
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

Deploy:

```bash
kubectl apply -f deployments/integrations/kubernetes/manifests/
kubectl -n gobfd rollout status daemonset/gobfd
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

The Kubernetes image import and live-cluster qualification remain incomplete.
These manifests and probes are examples, not an accepted cluster deployment.
Readiness demonstrates management access, not session or route convergence.

## BGP Fast-Failover Drill

Run the generic FRR/GoBGP integration:

```bash
make int-bgp-failover-up
make int-bgp-failover-logs
```

Full example documentation:
[`deployments/integrations/bgp-fast-failover/README.md`](../../deployments/integrations/bgp-fast-failover/README.md).

Baseline checks:

```bash
podman exec gobgp-bgp-failover gobgp neighbor
podman exec gobgp-bgp-failover gobgp global rib
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd -c 5
```

Failure drill:

```bash
podman pause frr-bgp-failover
sleep 2
podman exec gobgp-bgp-failover gobgp global rib
podman unpause frr-bgp-failover
sleep 5
podman exec gobgp-bgp-failover gobgp global rib
```

Expected result:

- BFD transitions from Up to Down before the normal BGP hold timer expires.
- GoBGP disables the affected peer.
- Route reachability recovers after the BFD session returns Up.

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
| `BFDNoActiveSessions` | GoBFD has no configured active sessions. |
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

*Reviewed against `dev` source: 2026-10-24.*
