# Production Integration Runbooks

> Generic runbooks for deploying and validating GoBFD in independent Linux,
> Kubernetes, BGP, and overlay-network environments.

---

## Evidence Baseline

These runbooks are based on:

- RFC 5880 detection-time rules: in Asynchronous mode, local detection time is
  the remote Detect Mult multiplied by the negotiated receive interval.
- RFC 5881 single-hop encapsulation: UDP destination port 3784, source port in
  the dynamic range, and TTL/Hop Limit 255.
- RFC 7130 Micro-BFD: independent sessions per LAG member on UDP port 6784.
- RFC 8971 VXLAN BFD: Management VNI, inner UDP destination port 3784, and
  inner TTL/Hop Limit 255.
- RFC 9521 Geneve BFD: Geneve BFD for point-to-point Geneve tunnels with inner
  TTL/Hop Limit 255 and traffic-managed rate assumptions.
- MCP validation: Kubernetes `hostNetwork: true` uses
  `dnsPolicy: ClusterFirstWithHostNet`; Arista EOS exposes public BFD BGP and
  RFC 7130/VXLAN verification commands, but GoBFD examples remain
  vendor-neutral by default.

## Deployment Profiles

| Profile | Use case | Assets |
|---|---|---|
| BGP failover | Linux routing host or lab pair with FRR/GoBGP | `deployments/integrations/bgp-fast-failover/` |
| Kubernetes daemon | One GoBFD instance per node with host networking | `deployments/integrations/kubernetes/` |
| Observability | Prometheus alerts and Grafana dashboard | `deployments/integrations/observability/` |
| Overlay endpoint | Dedicated VXLAN/Geneve management endpoint | `docs/04-linux-advanced-bfd-applicability.md` |

## Kubernetes Host-Network Daemon

Use this pattern when BFD peers are reachable from the node network namespace.
The DaemonSet is intentionally generic: it does not encode site topology,
private addresses, or vendor-specific assumptions.

Required properties:

- `hostNetwork: true`
- `dnsPolicy: ClusterFirstWithHostNet`
- Linux nodes only
- `NET_RAW` for BFD packet sockets
- `NET_ADMIN` for socket options and future link-state integration
- TCP readiness/liveness probes for the gRPC and metrics ports

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
- Keep the GoBFD API on localhost or a trusted management network unless mTLS
  is configured.

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
- GoBGP disables or withdraws the affected peer according to configured
  strategy.
- Route reachability recovers after the BFD session returns Up.

## Prometheus Alert Validation

Start the observability stack:

```bash
make int-observability-up
```

Check targets and rules:

```bash
curl -fsS http://localhost:9090/api/v1/targets
curl -fsS http://localhost:9090/api/v1/rules
```

Trigger a down transition:

```bash
podman stop frr-observability
sleep 5
curl -fsS http://localhost:9090/api/v1/alerts
podman start frr-observability
```

Alert intent:

| Alert | Meaning |
|---|---|
| `BFDNoActiveSessions` | GoBFD has no configured active sessions. |
| `BFDSessionDownTransition` | A session transitioned from Up to Down. |
| `BFDSessionFlapping` | A session changed state more than 3 times in 5 minutes. |
| `BFDAuthFailures` | RFC 5880 authentication verification failed. |
| `BFDPacketDrops` | Packet validation, demux, or buffer drops occurred. |

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

GoBFD examples should stay vendor-neutral. Vendor snippets can be added as
optional public interop notes when validated against primary vendor docs or MCP.

Published optional notes:

- [Arista EOS BFD verification note](../../deployments/integrations/bgp-fast-failover/vendor-notes/arista-eos.md)

Arista EOS public behavior validated through Arista MCP:

- `neighbor bfd` enables BFD for a BGP neighbor or peer group.
- If a BFD session goes down, the associated BGP session is changed to down.
- `show bfd peers` displays BFD state and session type, including RFC7130 and
  Vxlan where supported.
- `show port-channel <N> detail` can show members inactive due to RFC 7130
  micro-session state.

## Open Production Gaps

| Gap | Planned sprint |
|---|---|
| Dedicated API/CLI create flows for Echo, Micro-BFD, VXLAN, and Geneve | S5b |
| NetworkManager D-Bus backend for Micro-BFD enforcement | S7.1e |
| VXLAN/Geneve backend model for kernel/OVS dataplane coexistence | S7.2 |
| pkg.go.dev polish and v0.5.0 release dry-run | S8 |

---

*Last updated: 2026-05-01*
