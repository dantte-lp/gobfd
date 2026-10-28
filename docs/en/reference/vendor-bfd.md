# Equipment peers and normative references

Configure the same BFD profile on both endpoints. A shared RFC number does
not establish compatibility for a particular ASIC, NOS release, interface,
address family or authentication mode. Record these parameters in the
interoperability result; keep the packet capture and client-side route result.

## Protocol contract

| Profile | Contract | GoBFD reference |
|:---|:---|:---|
| Asynchronous Control | [RFC 5880 §§6.8–6.8.7](https://datatracker.ietf.org/doc/html/rfc5880#section-6.8): state, timers, Poll/Final | [Protocol](../02-protocol.md) |
| IP single-hop | [RFC 5881 §§4–5](https://datatracker.ietf.org/doc/html/rfc5881#section-4): UDP/3784, TTL/Hop Limit 255, demultiplexing | [RFC matrix](../08-rfc-compliance.md) |
| IP multihop | [RFC 5883 §§3–5](https://datatracker.ietf.org/doc/html/rfc5883#section-3): UDP/4784, authenticated and scoped path | [Configuration](../03-configuration.md) |
| BGP reaction | [RFC 5882 §3.2](https://datatracker.ietf.org/doc/html/rfc5882#section-3.2): client owns reaction and administrative-state policy | [Integrations](../11-integrations.md) |
| Per-member LAG | [RFC 7130](https://datatracker.ietf.org/doc/html/rfc7130): member sessions and forwarding eligibility | [Micro-BFD boundary](../08-rfc-compliance.md) |
| Anycast service health | [RFC 4786 §4.4.1](https://datatracker.ietf.org/doc/html/rfc4786#section-4.4.1): service availability and route policy | [Status](../roadmap.md) |

### Timers and units

GoBFD configuration uses duration strings such as `300ms`; BFD wire interval
fields use microseconds. Vendor CLI units are command-specific. Normalize
them before comparing packet fields.

For an asynchronous session in Up state, calculate each direction separately:

```text title="asynchronous-timer-equations"
TX_A = max(A.desired_min_tx, B.required_min_rx)
DETECTION_A = B.detect_mult * max(A.required_min_rx, B.desired_min_tx)
DETECTION_B = A.detect_mult * max(B.required_min_rx, A.desired_min_tx)
```

| Endpoint | Desired TX | Required RX | Multiplier |
|:---|---:|---:|---:|
| A | 100 ms | 300 ms | 5 |
| B | 200 ms | 500 ms | 3 |

The example produces `TX_A=500ms`, `DETECTION_A=900ms` and
`DETECTION_B=2500ms`. These protocol deadlines exclude client action latency
and route convergence. Poll/Final transitions, startup pacing and transmit
jitter have separate rules in RFC 5880. Do not translate a nominal interval
into a hard end-to-end failover guarantee.

## Vendor documents

These are configuration references, not GoBFD qualification receipts.
Resolve the exact device and NOS support before copying a configuration.

| Family and document | Inspect before enabling | Read-only inspection |
|:---|:---|:---|
| [Cisco Nexus 9000 / NX-OS 10.5(x), BFD](https://www.cisco.com/c/en/us/td/docs/dcn/nx-os/nexus9000/105x/configuration/interfaces/cisco-nexus-9000-series-nx-os-interfaces-configuration-guide-release-105x/m_configuring_bidirectional_forwarding_detection_93x.html) | Echo policy, BGP binding, interface and multihop restrictions | `show bfd neighbors details` |
| [Cisco IOS XE, BFD troubleshooting](https://www.cisco.com/c/en/us/support/docs/ip/ip-routing/220364-troubleshoot-bidirectional-forwarding-de.html) | Use IOS XE procedures; NX-OS syntax is a separate contract | Inspect the documented session and interface diagnostics |
| [Arista EOS, BFD manual](https://www.arista.com/en/um-eos/eos-bidirectional-forwarding-detection) | Interface timers, BGP `neighbor bfd`, independent Control/Echo offload | `show bfd peers detail` |
| [Juniper Junos, BFD for BGP](https://www.juniper.net/documentation/us/en/software/junos/bgp/topics/topic-map/bfd-for-bgp-session.html) | Platform behavior, client binding, authentication and restart interaction | `show bfd session extensive` |
| [Junos BGP CLI reference](https://www.juniper.net/documentation/us/en/software/junos/cli-reference/topics/ref/statement/bfd-liveness-detection-edit-protocols-bgp.html) | Explicit session mode, intervals, multiplier and hold-down | Inspect the effective `bfd-liveness-detection` hierarchy |
| [Huawei, BFD establishment](https://info.support.huawei.com/info-finder/encyclopedia/en/detail?action=queryEntityDetail&keyword=BFD) | Static versus dynamically learned discriminators | Record the establishment model |
| [Huawei Cloud Direct Connect, BFD FAQ](https://support.huaweicloud.com/intl/en-us/dc_faq/dc_07_0212.html) | Service-specific parameters and discriminator checks | `display bfd session all` |

The EOS manual uses milliseconds for `bfd interval`. The NX-OS guide lists
Echo enabled by default, while EOS documents it disabled. Treat that as a
platform-specific default; explicitly select the common Control-only profile
when qualifying a GoBFD Control session. RFC 9747 Unaffiliated Echo is a
separate GoBFD capability, not evidence of a vendor's affiliated Echo contract.

Huawei Cloud parameters apply to that service. They do not establish the
defaults of every Huawei VRP device. Junos platform and session-mode settings
require the referenced platform-support check.

## Isolated qualification

```mermaid
flowchart LR
  host["Linux host / GoBFD"]:::srv
  path["Selected interface and VRF path"]:::vrf
  nos["One NOS / platform profile"]:::leaf
  client["Routing client"]:::brt
  host <-->|"Control"| path
  path <-->|"Control"| nos
  nos -.-> client
  classDef srv fill:#e1bee7,stroke:#7b1fa2,color:#000
  classDef vrf fill:#ede7f6,stroke:#512da8,color:#000
  classDef leaf fill:#e3f2fd,stroke:#1976d2,color:#000
  classDef brt fill:#fff3e0,stroke:#bf360c,color:#000
```

1. Record GoBFD commit, NOS release, device model, interface type, network
   namespace/VRF, local/remote addresses and packet-capture point.
2. Select IPv4 or IPv6, single-hop or multihop, Control or a separately
   supported extension. Match timer units, algorithm, key ID and discriminator
   establishment model. Preserve a copy of the effective configuration.
3. Bind the routing client explicitly. Check negotiated timer fields, both
   discriminators, local/remote state and authentication in the packet capture.
4. Inject one failure in the isolated path. Measure packet loss, owner Down,
   client action and route withdrawal as separate timestamps.
5. Restore the original path. Verify session recovery and the intended route
   state. Remove the test configuration using the device's reviewed rollback.
6. Retain commands, terminal status, raw capture and resource envelope with
   the result. Repeat for every claimed profile and final candidate.

For a cloud path, include security-group/ACL rules, MTU, asymmetric routing,
source-address validation and virtual-NIC scheduling in the environment
record. These are qualification inputs, not assumed cloud-provider features.
Do not infer physical member steering, hardware offload or whole-device
failure detection from a VM or container session.

GoBFD's retained harnesses and their admission rules are described in
[Interoperability](../05-interop.md). A manual link, a topology file or a
historical PASS does not populate the current support matrix.
