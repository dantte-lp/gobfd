# BGP Fast Failover Integration

This example demonstrates GoBFD driving GoBGP peer control while peering with
FRR. It is a generic lab topology for validating BFD-assisted route withdrawal;
it does not encode vendor-specific or private network assumptions.

## Topology

| Component | Address | Role |
|---|---|---|
| GoBFD | `172.22.0.10` | BFD daemon and GoBGP integration client |
| GoBGP | `172.22.0.10` | eBGP speaker, AS 65001, shared network namespace with GoBFD |
| FRR | `172.22.0.20` | eBGP peer, AS 65002, `bgpd` + `bfdd` |
| tshark | shared with GoBFD | Packet capture for RFC-visible verification |

FRR announces `10.20.0.0/24` to GoBGP. GoBFD monitors FRR with a single-hop
BFD session and uses the GoBGP integration strategy `disable-peer` when the
session goes Down.

## RFC Baseline

The packet-level expectations are intentionally small and observable:

| Check | Expected value | Source |
|---|---|---|
| UDP destination port | `3784` | RFC 5881 single-hop BFD |
| UDP source port | `49152-65535` | RFC 5881 dynamic source port range |
| TTL / Hop Limit | `255` | RFC 5881 GTSM-style single-hop protection |
| Detect multiplier | `3` | Example configuration |
| Desired min TX / required min RX | `300ms / 300ms` | Example configuration |
| Detection target | about `900ms` plus scheduling jitter | RFC 5880 detection-time calculation |

The demo pauses the FRR container, so the observed withdrawal time also includes
container scheduling, GoBFD event handling, GoBGP gRPC handling, and BGP RIB
update latency.

## Run

Use the repository Make targets so the example stays consistent with the
project's Podman-only validation workflow:

```bash
make int-bgp-failover
```

For manual inspection:

```bash
make int-bgp-failover-up
podman exec gobgp-bgp-failover gobgp neighbor
podman exec gobgp-bgp-failover gobgp global rib
make int-bgp-failover-logs
make int-bgp-failover-down
```

## Failure Drill

The full script performs this sequence:

1. Build and start the topology.
2. Wait for the GoBGP to FRR eBGP session to establish.
3. Verify that `10.20.0.0/24` is present in the GoBGP RIB.
4. Capture BFD packets with tshark.
5. Pause FRR to stop BFD and BGP control-plane progress.
6. Verify that GoBFD reports Down and GoBGP withdraws the route.
7. Unpause FRR and verify route restoration.

Run the same checks manually when debugging:

```bash
podman pause frr-bgp-failover
sleep 5
podman exec gobgp-bgp-failover gobgp neighbor
podman exec gobgp-bgp-failover gobgp global rib

podman unpause frr-bgp-failover
sleep 15
podman exec gobgp-bgp-failover gobgp neighbor
podman exec gobgp-bgp-failover gobgp global rib
```

## Packet Verification

Inspect the capture rather than relying only on log messages:

```bash
podman exec tshark-bgp-failover tshark -r /captures/bfd.pcapng -Y bfd \
  -T fields \
  -e frame.time_relative \
  -e ip.src \
  -e ip.dst \
  -e udp.srcport \
  -e udp.dstport \
  -e ip.ttl \
  -e bfd.version \
  -e bfd.diag \
  -e bfd.sta \
  -e bfd.detect_time_multiplier \
  -e bfd.desired_min_tx_interval \
  -e bfd.required_min_rx_interval \
  -E header=y \
  -E separator=,
```

Expected single-hop values:

- `udp.dstport` is `3784`.
- `udp.srcport` is in `49152-65535`.
- `ip.ttl` is `255`.
- `bfd.version` is `1`.
- The state progresses from Down/Init to Up, then back to Down during failure.

## Troubleshooting

| Symptom | Check |
|---|---|
| BGP never establishes | `podman exec gobgp-bgp-failover gobgp neighbor` and FRR logs |
| Route is missing before failure | FRR `network 10.20.0.0/24` and static Null0 route |
| BFD packets are absent | GoBFD config, `NET_RAW` capability, and tshark capture sidecar |
| Route remains after failure | GoBFD GoBGP address, `disable-peer` strategy, and GoBGP gRPC reachability |
| Recovery is slow | FRR unpause status, BFD Up transition, and BGP neighbor state |

## Public Vendor Notes

Optional vendor verification notes are kept outside the runnable topology:

- [Arista EOS verification note](vendor-notes/arista-eos.md)

The example itself remains FRR/GoBGP based so it can run locally without access
to vendor hardware or a dedicated lab.
