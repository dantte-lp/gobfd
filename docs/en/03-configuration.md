# Configuration

The daemon accepts only `-config PATH` and `-version`. With `-config`, configuration merges built-in defaults, YAML and supported environment variables, in that order. Without it, built-in defaults apply and environment overrides are ignored. Sources: [schema, defaults, validation and environment mapping](../../internal/config/config.go), [daemon loader](../../cmd/gobfd/handlers.go), [annotated configuration](../../configs/gobfd.example.yml).

## Minimal declarative configuration

```yaml title="gobfd.yml" showLineNumbers
grpc:
  socket:
    path: /run/gobfd/gobfd.sock
    mode: "0660"
    group: gobfd-admin
metrics:
  addr: 127.0.0.1:9100
  path: /metrics
log:
  level: info
  format: json
sessions:
  - peer: 192.0.2.2
    local: 192.0.2.1
    interface: eth0
    type: single_hop
    desired_min_tx: 300ms
    required_min_rx: 300ms
    detect_mult: 3
```

Replace addresses/interface names with owned endpoints. Use duration strings with explicit units. Wire intervals use microseconds ([RFC 5880 §4.1](https://www.rfc-editor.org/rfc/rfc5880.html#section-4.1)). YAML type names use underscores; CLI names use hyphens.

## Management, metrics and logging

| YAML key | Built-in default | Contract |
| :--- | :--- | :--- |
| `grpc.addr` | empty | No TCP listener; plaintext must be loopback; remote TCP requires mTLS |
| `grpc.socket.path` | `/run/gobfd/gobfd.sock` | Absolute Unix path; empty disables socket |
| `grpc.socket.mode` | `0660` | Octal permissions |
| `grpc.socket.group` | empty | Daemon group; packaged example selects `gobfd-admin` |
| `grpc.tls.cert_file` | empty | PEM server certificate |
| `grpc.tls.key_file` | empty | PEM server private key |
| `grpc.tls.client_ca_file` | empty | PEM CA for mandatory client verification |
| `grpc.mutating_identities` | `[]` | URI SAN or `dns:NAME` mTLS writer allowlist; requires TLS |
| `grpc.unsafe_loopback_mutations` | `false` | Anonymous direct plaintext loopback writes; management health becomes `NOT_SERVING` |
| `grpc.flight_recorder` | `false` | Authorized management-only `/debug/flightrecorder` |
| `metrics.addr` | `127.0.0.1:9100` | Unauthenticated HTTP |
| `metrics.path` | `/metrics` | Prometheus path |
| `log.level` | `info` | `debug`, `info`, `warn`, `error` |
| `log.format` | `json` | `json` or `text`; restart-only |

TLS needs all three files and a TCP address. Files are reread at every handshake; replacing contents needs no restart, changing configured paths does. Unix requests carry kernel peer UID; mTLS uses first URI SAN, otherwise first DNS SAN. Keep at least one management transport. See [security](15-security.md).

## Control sessions

| Global key | Default | Contract |
| :--- | :--- | :--- |
| `bfd.default_desired_min_tx` | `1s` | Desired transmit interval |
| `bfd.default_required_min_rx` | `1s` | Receive interval; explicit zero is preserved |
| `bfd.default_detect_multiplier` | `3` | 1–255 |
| `bfd.align_intervals` | `false` | Round upward to supported RFC 7419 common intervals |
| `bfd.default_padded_pdu_size` | `0` | No padding; nonzero target 24–9000 bytes |
| `sessions` | `[]` | Declarative Control claims |

| `sessions[]` key | Default | Contract |
| :--- | :--- | :--- |
| `peer` | required | Peer IP |
| `local`, `interface` | empty | Local IP and single-hop device binding |
| `type` | `single_hop` | `single_hop` or `multi_hop` |
| `desired_min_tx` | inherited | Positive TX interval |
| `required_min_rx` | inherited | Nonnegative RX; explicit `0s` remains zero |
| `detect_mult` | inherited | 1–255 after defaults |
| `padded_pdu_size` | `0` | Zero inherits global padding; size includes authentication |
| `multihop_max_hops` | `1` | Multi-hop only, 1–254; receive TTL minimum `255 - value` |
| `auth.type` | empty | Empty/`none`, `simple_password`, `keyed_md5`, `meticulous_keyed_md5`, `keyed_sha1`, `meticulous_keyed_sha1` |
| `auth.key_id` | `0` | 0–255 |
| `auth.secret` | empty | 1–16 bytes for password/MD5; 1–20 for SHA1 |

Single-hop receives require TTL/Hop Limit 255. Multi-hop TTL admission is GoBFD policy; RFC 5883 does not mandate it. Padding requests DF and a payload-size floor: transmitted length is `max(actual PDU, target)`. An authenticated PDU may exceed the target and is never truncated. Source: [packet construction](../../internal/bfd/session_packet.go). References: [RFC 5881](https://www.rfc-editor.org/rfc/rfc5881.html), [RFC 5883 §6](https://www.rfc-editor.org/rfc/rfc5883.html#section-6), [RFC 7419](https://www.rfc-editor.org/rfc/rfc7419.html), [RFC 9764](https://www.rfc-editor.org/rfc/rfc9764.html).

## UDP buffers and sender QoS

All `socket` fields are startup-owned. QoS applies to ordinary BFD senders; overlay outer sockets do not inherit it. Sources: [QoS validation](../../internal/config/socket_qos.go), [buffer validation](../../internal/config/socket_buffer.go).

| `socket` key | Default | Contract |
| :--- | ---: | :--- |
| `read_buffer_size`, `write_buffer_size` | `4194304` each | Receive/send buffer bytes; zero keeps OS default |
| `ipv4_dscp` | `48` | Six-bit DSCP, 0–63, without ECN |
| `ipv6_traffic_class` | `192` | Full octet, 0–255 |
| `priority` | `0` | Linux queue priority, independent of DSCP |
| `mark` | `0` | Zero skips routing-mark operation |
| `priority_required`, `mark_required` | `true` | Reject sender when the requested option fails |

CS6 is operational policy, not an RFC 5881 requirement. Inspect effective buffers and captured markings after deployment; a configured request does not prove network QoS treatment.

Setting `mark_required: false` permits transmission after a requested mark fails, potentially without the intended routing policy. Keep it `true` when the mark selects a route. Optional priority failure likewise permits sending without that queue priority; no successful marking is implied.

## Preview profiles

All profiles default to disabled. Capability maturity, enablement and local readiness are independent; `ready` does not prove peer health, RFC coverage or actuator enforcement.

<!-- BEGIN GOBFD PREVIEW CAPABILITIES -->
| Profile | RFC | Maturity |
| :--- | ---: | :--- |
| unsolicited | 9468 | preview; unsafe |
| echo | 9747 | preview; incomplete RFC qualification |
| micro_bfd | 7130 | preview; incomplete L2 owner integration |
| vxlan | 8971 | preview; unsafe/incomplete owner integration |
| geneve | 9521 | preview; unsafe/incomplete IPv4 Format A |
<!-- END GOBFD PREVIEW CAPABILITIES -->

### Unsolicited Control

| `unsolicited` key | Default | Contract |
| :--- | :--- | :--- |
| `enabled` | `false` | Restart-only opt-in |
| `max_sessions` | `0` | At least 1 when enabled |
| `cleanup_timeout` | `0s` | Down-session removal delay; zero removes immediately |
| `interfaces` | `{}` | Map by receiving interface |
| `interfaces.NAME.enabled` | `false` | Interface admission |
| `interfaces.NAME.allowed_prefixes` | `[]` | Extra source restriction; receiving-address subnet also applies |
| `session_defaults.desired_min_tx`, `session_defaults.required_min_rx` | `1s` | Passive-session timers |
| `session_defaults.detect_mult` | `3` | Passive-session multiplier |
| `auth` | disabled | Control auth shape; only `meticulous_keyed_sha1` accepted |

Admission requires TTL 255 and eligible local subnet. The global creation limiter allows 10 sessions/s, burst 20. Keep this preview off anonymous shared segments. Sources: [admission implementation](../../internal/bfd/unsolicited.go), [RFC 9468 §6](https://www.rfc-editor.org/rfc/rfc9468.html#section-6).

### Echo

`echo` contains `enabled` (false), `default_tx_interval` (zero), `default_detect_multiplier` (zero) and `peers` (empty). Every peer supports `peer`, `local`, `interface`, `tx_interval`, `detect_mult`. Nonzero peer values override profile defaults; configure a positive effective TX and multiplier 1–255.

Echo uses UDP 3785 and has no authentication setting. Control authentication does not protect it. Compatible API/config claims share one physical session; releasing one claim preserves other owners. Sources: [Echo reconciliation](../../cmd/gobfd/reload.go), [ADR-0009](adr/0009-echo-principal-claims.md), [RFC 9747](https://www.rfc-editor.org/rfc/rfc9747.html).

### Micro-BFD

| `micro_bfd` key | Default | Values |
| :--- | :--- | :--- |
| `enabled` | `false` | Restart-only opt-in |
| `groups` | `[]` | LAG groups |
| `actuator.mode` | `disabled` | `disabled`, `dry-run`, `enforce` |
| `actuator.backend` | `auto` | `auto`, `kernel-bond`, `ovs`, `networkmanager` |
| `actuator.ovsdb_endpoint` | empty | Native OVSDB endpoint |
| `actuator.owner_policy` | `refuse-if-managed` | Also `allow-external`, `networkmanager-dbus` |
| `actuator.down_action` | `remove-member` | `none`, `remove-member`, `add-member` |
| `actuator.up_action` | `add-member` | `none`, `remove-member`, `add-member` |

Each group contains `lag_interface`, `member_links`, `peer_addr`, `local_addr`, `desired_min_tx`, `required_min_rx`, `detect_mult`, `min_active_links`. Timers inherit BFD defaults; minimum active links is 1…member count. Enforce requires an explicit supported owner/backend: kernel-bond or OVS with `allow-external`, NetworkManager with `networkmanager-dbus`. A dry-run log does not prove member enforcement. Sources: [schema and validation](../../internal/config/config.go), [RFC 7130](https://www.rfc-editor.org/rfc/rfc7130.html).

### VXLAN and Geneve

| Profile | Global fields | Peer fields |
| :--- | :--- | :--- |
| `vxlan` | `enabled`, `backend`, `management_vni`, `default_desired_min_tx`, `default_required_min_rx`, `default_detect_multiplier`, `peers` | `peer`, `local`, `peer_mac`, `local_mac`, `desired_min_tx`, `required_min_rx`, `detect_mult` |
| `geneve` | `enabled`, `backend`, `default_vni`, `default_desired_min_tx`, `default_required_min_rx`, `default_detect_multiplier`, `peers` | `peer`, `local`, `vni`, `inner_peer`, `inner_local`, `peer_mac`, `local_mac`, `desired_min_tx`, `required_min_rx`, `detect_mult` |

`userspace-udp` is the default and only implemented overlay backend. Reserved `kernel`, `ovs`, `ovn`, `cilium`, `calico`, `nsx` backends are rejected. Profile timers/VNIs default to zero; supply valid effective values. Peer timer zero inherits the profile; Geneve VNI zero inherits `default_vni`. VNIs use 24 bits. Format A requires explicit unicast MACs; Geneve also needs explicit unicast inner IPv4 identities. UDP 4789/6081 must be free in the selected namespace/address. Sources: [validation](../../internal/config/config.go), [Format A](../../internal/netio/overlay_inner.go), [RFC 8971](https://www.rfc-editor.org/rfc/rfc8971.html), [RFC 9521](https://www.rfc-editor.org/rfc/rfc9521.html).

## GoBGP integration

| `gobgp` key | Default | Contract |
| :--- | :--- | :--- |
| `enabled` | `false` | Peer actuation |
| `addr` | `127.0.0.1:50051` | API endpoint |
| `strategy` | `disable-peer` | Only implemented strategy; `withdraw-routes` rejected |
| `action_timeout` | `5s` | Per-action deadline |
| `tls.enabled` | `false` | Server-authenticated TLS |
| `tls.ca_file`, `tls.server_name` | empty | Root bundle (host roots if empty), verification-name override |
| `dampening.enabled` | `false` | Hold recovery after flaps; Down acts immediately |
| `dampening.suppress_threshold`, `dampening.reuse_threshold` | `3`, `2` | Suppress/reuse penalty thresholds |
| `dampening.max_suppress_time`, `dampening.half_life` | `60s`, `15s` | Maximum hold and penalty half-life |

The `dev` implementation requires GoBGP v4 API and rejects native BFD ownership conflicts. Several base sessions for one BGP address prevent canonical actuation. Administrative transitions do not mean path failure. Sources: [startup preflight and canonical selection](../../cmd/gobfd/handlers.go), [actuation](../../internal/gobgp/handler.go), [RFC 5882 §10.2](https://www.rfc-editor.org/rfc/rfc5882.html#section-10.2).

## Environment overrides

Prefix each suffix below with `GOBFD_`. Only these exact mappings exist; arbitrary underscore-to-dot conversion is absent.

| Suffixes | Target |
| :--- | :--- |
| `GRPC_ADDR` | `grpc.addr` |
| `METRICS_ADDR`, `METRICS_PATH` | `metrics.addr`, `metrics.path` |
| `LOG_LEVEL`, `LOG_FORMAT` | `log.level`, `log.format` |
| `UNSOLICITED_ENABLED`, `ECHO_ENABLED`, `MICRO_BFD_ENABLED` | Profile `.enabled` |
| `VXLAN_ENABLED`, `VXLAN_BACKEND`, `GENEVE_ENABLED`, `GENEVE_BACKEND` | Profile `.enabled`/`.backend` |
| `GOBGP_ENABLED`, `GOBGP_ADDR`, `GOBGP_STRATEGY` | Corresponding `gobgp` fields |
| `GOBGP_TLS_ENABLED`, `GOBGP_DAMPENING_ENABLED` | `gobgp.tls.enabled`, `gobgp.dampening.enabled` |
| `SOCKET_READ_BUFFER_SIZE`, `SOCKET_WRITE_BUFFER_SIZE` | Corresponding `socket` buffers |
| `SOCKET_IPV4_DSCP`, `SOCKET_IPV6_TRAFFIC_CLASS` | Corresponding `socket` markings |
| `SOCKET_PRIORITY`, `SOCKET_MARK` | `socket.priority`, `socket.mark` |
| `SOCKET_PRIORITY_REQUIRED`, `SOCKET_MARK_REQUIRED` | Corresponding option-failure policies |

## Hot reload (SIGHUP)

### Validation and listener identity

| Input | Required condition |
| :--- | :--- |
| `log.level`, `log.format` | Level is case-insensitive; format is lowercase `json`/`text`; explicit empty/whitespace values fail |
| Socket scalars | Nonnegative integer buffers; priority 0–2147483647; mark 0–4294967295; strict boolean failure policy |
| Multiplier and padding scalars | Reject fractions, booleans, overflow and conflicting case-folded field names before decoding |
| Peer/group collections | Lists of mappings; no singleton-map coercion or duplicate identities |
| Multi-hop | No `interface`; `multihop_max_hops` only for `multi_hop` |
| IPv6 zones | Peer/local zones agree with the effective single-hop interface |
| Unsolicited | At least one enabled nonempty interface; valid prefixes; timers 1 µs…uint32 µs; nonnegative cleanup timeout |
| Dampening | Suppress threshold > reuse threshold > 0; positive half-life and maximum duration |

Control YAML listener identity is `(parsed local address, multi-hop flag)`; Echo uses parsed local address. Entries sharing one listener must agree on interface, including an empty interface. Runtime receive keys add interface, destination port and normalized address. Ports are 3784/4784/3785/6784. A compatible API claim shares the exact key; a different device does not reuse another device's socket. Sources: [listener validation](../../internal/config/listener_binding.go), [runtime owner](../../cmd/gobfd/listeners.go).

VXLAN/Geneve currently require concrete IPv4 outer endpoints; Format A MACs are nonzero unicast values. Authenticated padding has minimum target 26 bytes; actual authenticated PDU is never truncated. These are validation/implementation constraints, not native overlay acceptance.

### Apply and convergence

SIGHUP reloads the same path and process environment. Complete validation precedes apply. Invalid candidates and startup-field changes retain the accepted generation. Runtime apply failures may leave pending resources; inspect reconciliation health and logs.

| Reloadable | Restart required |
| :--- | :--- |
| `log.level` | All `grpc`, `metrics`, `socket`, `gobgp`, `unsolicited`; `log.format` |
| Control desired set, timers, multiplier, padding | Auth change on retained Control identity |
| Echo enablement, defaults and peers | `micro_bfd.enabled` and actuator policy |
| Micro-BFD groups | VXLAN backend/management VNI; Geneve backend/default VNI |
| VXLAN/Geneve enablement, timers and peers | All startup fields enumerated by `startup_contract.go` |

The YAML reconciliation key is `(peer, local, interface)`. Canonical wire identity additionally includes session type, address family, network scope and exact transport scope; changing these is not an ordinary retained-identity timer update. Compatible timer updates retain the session/discriminator. Removal releases the configuration claim and preserves other claims. Authentication rotation requires coordinated restart or release/recreation with interruption. Sources: [canonical identity](../../internal/bfd/session_identity.go), [startup contract](../../cmd/gobfd/startup_contract.go), [coordinator](../../cmd/gobfd/reconciliation.go), [ADR-0006](adr/0006-session-preserving-reload-policy.md).

In-place packet-policy updates require a single compatible configuration owner. Padding supports positive→positive resize only with a prepared sender, unchanged authentication and asynchronous operation; zero↔nonzero, unprepared senders and Demand-dependent changes are rejected. MinTTL is local multi-hop policy. Poll-dependent timer changes remain pending until their transaction completes; admission is not completion. See [owner update rules](../../internal/bfd/session_update.go) and [protocol](02-protocol.md).

Reconciliation spans base, Echo, Micro-BFD, VXLAN and Geneve without all-source atomicity. Valid disjoint changes may remain applied when another source fails. Missing local address/interface retains desired generation and claims, marks readiness unready and retries on resource recovery; a periodic two-second check supplements interface events. Permanent/inventory failures require explicit SIGHUP. An accepted generation with pending timers/resources is not converged. Source-local overlay acquisition preserves live/draining backend leases on failure. Inspect bounded per-source receipts and aggregate/named service health; do not promise seamless rollback or peer continuity.

1. Preserve the accepted configuration and session/discriminator inventory.
2. Install the protected candidate at the same path.
3. Run `systemctl reload gobfd` for reloadable fields.
4. Inspect `journalctl -u gobfd --since '-2 min'`, `gobfdctl session list` and peer state.
5. Restore the accepted file and reload on divergence; schedule a maintenance restart for startup-owned changes.

This is a source-derived procedure, not a production execution receipt. See the [operational runbook](16-production-runbooks.md).
