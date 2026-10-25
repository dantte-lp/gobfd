# BGP Fast Failover Integration

This example demonstrates GoBFD driving GoBGP peer control while peering with
FRR. It is a generic lab topology for validating BFD-assisted route withdrawal;
it does not encode vendor-specific or private network assumptions.

This is a lab procedure for current `dev` source, not a fresh execution receipt
or stable-release acceptance. Final-candidate qualification and publication
authorization remain separate. Русские руководства:
[интеграции](../../../docs/ru/11-integrations.md#быстрое-переключение-bgp) и
[эксплуатационная проверка](../../../docs/ru/16-production-runbooks.md#проверка-быстрого-переключения-bgp).

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

FRR 10.7.1 and GoBGP v4.10.0 use the shared Debian trixie recipes in
`test/interop/frr/` and `test/interop/gobgp/`. GoBGP v4.10.0 is built from
upstream commit `da824d99912e02124b53741245ee4186c381adcc` with Go 1.27.3.
GoBFD on `dev` requires a GoBGP v4 daemon; a GoBGP v3 daemon is
wire-incompatible. This example does not enable GoBGP native BFD; GoBFD owns
the BFD session. Builds are limited to 2 CPUs and 2 GiB;
each runtime container is limited to 1 CPU, 256 MiB and 128 PIDs.

The configured management TCP listener is loopback-only `127.0.0.1:50052`;
the default Unix management socket remains available inside GoBFD. Sharing
GoBFD/GoBGP networking does not share the socket filesystem path. The lab
metrics listener is `:9100` without authentication; keep this network isolated.

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

First follow the pinned [Compose-provider setup](../../../docs/en/06-deployment.md#podman-compose),
render the selected Compose file and admit resources. The manual topology uses
fixed container names and `172.22.0.0/24`, which overlaps the RFC interop lab.
Do not run those networks concurrently on the same Podman engine. The selected
build and runtime limits are source contracts, not measured resource admission.

Use the authoritative Go testcontainers gate for automated validation. It
writes packet, container, and JSON test evidence under
`reports/e2e/bgp-fast-failover/run.*/` and removes its exact test-owned
resources:

```bash
make int-bgp-failover
```

The operational Compose example uses the ownership-checked Go controller,
sequential capped builds and startup without implicit builds. Its packet
capture stays in container storage; copy it before `-down` if needed:

```bash
make int-bgp-failover-up
podman exec gobgp-bgp-failover gobgp neighbor
podman exec gobgp-bgp-failover gobgp global rib
podman exec gobfd-bgp-failover gobfdctl session list
# Optional: follow logs in a separate terminal.
# make int-bgp-failover-logs
```

After retaining the required evidence, use `make int-bgp-failover-down` only
for the verified owned topology and with the required deletion approval.

## Failure Drill

The Go testcontainers gate performs this sequence:

1. Build and start the topology.
2. Wait for the GoBGP to FRR eBGP session to establish.
3. Verify that `10.20.0.0/24` is present in the GoBGP RIB.
4. Capture BFD packets with tshark.
5. Pause FRR to stop BFD and BGP control-plane progress.
6. Require the exact route to disappear from the GoBGP RIB within the test's 20-second bound.
7. Unpause FRR and verify route restoration.

Run the same checks manually when debugging:

The gate directly asserts GoBGP neighbor/RIB state; it does not directly query
GoBFD Down or measure a 900 ms detection bound. Packet capture retention also
does not establish forwarding continuity or the full RFC matrix. Source:
[`testcontainers_test.go`](../../../test/e2e/bgp-failover/testcontainers_test.go).
The following fixed waits display observations only. Confirm that all names
belong to the owned manual topology; the exit trap restores a paused FRR after
a failed query.

```bash
(
  set -eu
  trap 'podman unpause frr-bgp-failover' EXIT
  podman pause frr-bgp-failover
  sleep 5
  podman exec gobfd-bgp-failover gobfdctl session list
  podman exec gobgp-bgp-failover gobgp neighbor
  podman exec gobgp-bgp-failover gobgp global rib
)
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
