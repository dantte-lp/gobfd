# Configuration

![YAML](https://img.shields.io/badge/YAML-Config-cb171e?style=for-the-badge&logo=yaml)
![koanf](https://img.shields.io/badge/koanf-v2-00ADD8?style=for-the-badge)
![ENV](https://img.shields.io/badge/ENV-Override-ffc107?style=for-the-badge)
![SIGHUP](https://img.shields.io/badge/SIGHUP-Hot_Reload-34a853?style=for-the-badge)

> Complete configuration reference for the GoBFD daemon: YAML file format, environment variable overrides, declarative sessions, GoBGP integration, and hot reload via SIGHUP.

---

## Table of Contents

- [Configuration Sources](#configuration-sources)
- [Full Configuration Example](#full-configuration-example)
- [Preview Capability Status](#preview-capability-status)
- [Configuration Sections](#configuration-sections)
- [Socket Tuning](#socket----socket-buffer-tuning)
- [Environment Variables](#environment-variables)
- [Declarative Sessions](#declarative-sessions)
- [GoBGP Integration](#gobgp-integration)
- [Hot Reload (SIGHUP)](#hot-reload-sighup)
- [Validation Rules](#validation-rules)
- [Defaults](#defaults)

### Configuration Sources

GoBFD loads configuration from three sources, applied in order (later sources override earlier ones):

1. **Defaults** -- sensible production defaults (see [Defaults](#defaults))
2. **YAML file** -- specified via `-config /path/to/gobfd.yml`
3. **Environment variables** -- prefixed with `GOBFD_`

This merge runs only when `-config` names a file. Without `-config`, the
daemon uses built-in defaults directly and does not load environment
overrides. See [`loadConfig`](../../cmd/gobfd/handlers.go) and
[`config.Load`](../../internal/config/config.go).

### Full Configuration Example

```yaml
# GoBFD daemon configuration
# See: configs/gobfd.example.yml

grpc:
  socket:
    path: "/run/gobfd/gobfd.sock"
    group: "gobfd-admin"

metrics:
  addr: "127.0.0.1:9100"
  path: "/metrics"

log:
  level: "info"       # debug, info, warn, error
  format: "json"      # json, text

bfd:
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  align_intervals: false
  default_padded_pdu_size: 0       # RFC 9764: 0 = no padding

# Unsolicited BFD (RFC 9468)
unsolicited:
  enabled: false

# Echo (RFC 9747)
echo:
  enabled: false

# Micro-BFD (RFC 7130)
micro_bfd:
  enabled: false
  groups: []

# VXLAN BFD (RFC 8971)
vxlan:
  enabled: false

# Geneve BFD (RFC 9521)
geneve:
  enabled: false

# GoBGP integration (RFC 5882 Section 10.2)
gobgp:
  enabled: true
  addr: "127.0.0.1:50051"
  strategy: "disable-peer"   # the only implemented strategy
  action_timeout: "5s"
  tls:
    enabled: false            # enable for remote/non-loopback GoBGP API
    ca_file: ""               # optional PEM root CA bundle
    server_name: ""           # optional certificate verification name
  dampening:
    enabled: true
    suppress_threshold: 3
    reuse_threshold: 2
    max_suppress_time: "60s"
    half_life: "15s"

# Socket buffer tuning (reduces packet loss under high session counts)
socket:
  read_buffer_size: 4194304    # SO_RCVBUF: 4 MiB (0 = OS default)
  write_buffer_size: 4194304   # SO_SNDBUF: 4 MiB (0 = OS default)

# Declarative sessions (reconciled on SIGHUP reload)
sessions:
  - peer: "10.0.0.1"
    local: "10.0.0.2"
    interface: "eth0"
    type: single_hop
    desired_min_tx: "100ms"
    required_min_rx: "100ms"
    detect_mult: 3
    auth:
      type: keyed_sha1
      key_id: 7
      secret: "change-me"
  - peer: "10.0.1.1"
    local: "10.0.1.2"
    type: multi_hop
    multihop_max_hops: 2             # minimum received TTL 253 (255 - 2)
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 5
    padded_pdu_size: 128             # RFC 9764: per-session PDU padding
```

### Preview Capability Status

<!-- BEGIN GOBFD PREVIEW CAPABILITIES -->
| Profile | RFC | Maturity |
|---|---|---|
| unsolicited | 9468 | preview; unsafe |
| echo | 9747 | preview; incomplete RFC qualification |
| micro_bfd | 7130 | preview; incomplete L2 owner integration |
| vxlan | 8971 | preview; unsafe/incomplete owner integration |
| geneve | 9521 | preview; unsafe/incomplete IPv4 Format A |
<!-- END GOBFD PREVIEW CAPABILITIES -->

Inspect the daemon with `gobfdctl capabilities` (`--format table`, `json` or
`yaml`). The read-only `bfd.v1.CapabilityService.ListCapabilities` RPC remains
available when forwarding runtime is not ready, under management transport
policy, and exposes no peer addresses, endpoints or secrets. Its daemon-owned
catalog has no client-side fallback.

Maturity is independent of effective enablement and local readiness.
Enablement follows validated `unsolicited.enabled`, `echo.enabled`,
`micro_bfd.enabled`, `vxlan.enabled` and `geneve.enabled` values. An enabled
VXLAN/Geneve profile with zero peers has no owned backend and remains
`not_ready`. Valid reloads update reloadable values; rejected
candidates preserve the retained generation. Changing `micro_bfd.enabled`
still requires a restart.

Missing runtime state produces `unknown` enablement/readiness; a disabled
profile is `not_ready`. `ready` requires the necessary owned receive/backend
path and local runtime, source and binding readiness. It does not prove peer
health, RFC compliance or L2 actuator enforcement. API-only Echo can remain
`not_ready` without a receive listener until API admission claims one; this
coarse catalog is not an admission preflight. Inspection changes no defaults
and activates no preview.

### Configuration Sections

#### `grpc` -- ConnectRPC Server

| Key | Type | Default | Description |
|---|---|---|---|
| `addr` | string | `""` | Optional TCP listen address; empty opens no TCP listener. Plaintext must be loopback; a non-loopback address needs `tls` |
| `mutating_identities` | []string | `[]` | mTLS identities (URI SAN or `dns:<name>`) that may mutate; when set, other mTLS clients are read-only. Requires `tls` |
| `flight_recorder` | bool | `false` | Serves the runtime trace dump at `/debug/flightrecorder` on the management server to callers with a principal |
| `unsafe_loopback_mutations` | bool | `false` | Lets anonymous plaintext loopback TCP clients call mutating RPCs; `gobfd.management` health is `NOT_SERVING` while set |
| `socket.path` | string | `"/run/gobfd/gobfd.sock"` | Absolute path of the management Unix socket; empty disables it |
| `socket.mode` | string | `"0660"` | Octal permission mode of the socket file |
| `socket.group` | string | `""` | Group that owns the socket file; empty keeps the daemon's group |
| `tls.cert_file` | string | `""` | Server certificate (PEM) of the TCP listener; with `key_file` and `client_ca_file` it enables mTLS |
| `tls.key_file` | string | `""` | Server private key (PEM) |
| `tls.client_ca_file` | string | `""` | CA (PEM) that client certificates must chain to |

Requests over the Unix socket carry the kernel-reported peer UID
(`SO_PEERCRED`) as principal; RPC logs record it as `principal=uid:<n>`.
With `grpc.tls` the TCP listener requires a client certificate from
`client_ca_file` and offers HTTP/2 and HTTP/1.1 through ALPN; the principal
is the certificate's first URI SAN (for example a SPIFFE ID) or else
`dns:<first DNS SAN>`. The three files are re-read on every handshake, so
rotation needs no restart; a missing file fails startup. `grpc.tls` requires
`grpc.addr` and suppresses the startup warning for a non-loopback address.
Access control is the socket mode and group, so use a dedicated admin group
rather than the daemon group, which can read `/etc/gobfd`. `grpc.socket` is
restart-only ([transport decision](../superpowers/specs/2026-10-15-management-transport-decision.md)).

Anonymous callers are plain TCP clients without mTLS, which can bind only
loopback. They may read (`List*`, `Get*`, `Watch*`) but receive
`PermissionDenied` for `AddSession`, `DeleteSession`, `AddEchoSession`,
`DeleteEchoSession`, `AddMicroBFDGroup` and `DeleteMicroBFDGroup` unless
`unsafe_loopback_mutations` is set. Socket and mTLS callers carry a principal
and may mutate; with `mutating_identities` set, only the listed mTLS
identities may. A session created through the API belongs to the caller's
principal: another principal gets `PermissionDenied` from `DeleteSession`
(logged as a refused release), and two principals that add the same session
share it until both release it. Anonymous callers under
`unsafe_loopback_mutations` share the owner `local`.

**Upgrading from a TCP-only configuration:** a non-loopback plaintext
`grpc.addr` (for example `":50052"`) now fails startup; bind it to loopback
or add `grpc.tls`. Move mutating clients to the socket (`gobfdctl --addr
unix:///run/gobfd/gobfd.sock`, the new default) and add operators to group
`gobfd-admin`, or set `unsafe_loopback_mutations: true` as a transitional,
visibly unsafe mode. A configured `grpc.addr` keeps its TCP listener.

#### `metrics` -- Prometheus Endpoint

| Key | Type | Default | Description |
|---|---|---|---|
| `addr` | string | `"127.0.0.1:9100"` | HTTP listen address for metrics; unauthenticated, so loopback by default |
| `path` | string | `"/metrics"` | URL path for Prometheus scraping |

#### `log` -- Logging

| Key | Type | Default | Description |
|---|---|---|---|
| `level` | string | `"info"` | Log level: `debug`, `info`, `warn`, `error` |
| `format` | string | `"json"` | Output format: `json` (production), `text` (development) |

Levels are case-insensitive: `debug`, `info`, `warn`, and `error`. Formats must
be exactly lowercase `json` or `text`. Omitted keys retain defaults `info`
and `json`; explicit empty strings, whitespace, and unknown values fail
configuration validation with `ErrInvalidLogLevel` or `ErrInvalidLogFormat`.
Errors identify the field without printing its value. The same validation
applies to YAML and environment overrides at startup and on SIGHUP, before
log level, generation, health, sessions, or sender resources change.

A valid log level can be changed through SIGHUP without restarting the daemon.
Changing the format requires a restart.

#### `bfd` -- Default BFD Parameters

| Key | Type | Default | Description |
|---|---|---|---|
| `default_desired_min_tx` | duration | `"1s"` | Default Desired Min TX Interval |
| `default_required_min_rx` | duration | `"1s"` | Default Required Min RX Interval; zero requests no periodic Control packets |
| `default_detect_multiplier` | uint32 | `3` | Default Detection Multiplier (1–255) |
| `align_intervals` | bool | `false` | RFC 7419: align timers to nearest common interval |
| `default_padded_pdu_size` | uint16 | `0` | RFC 9764: pad BFD packets to this size with DF bit (0 = disabled; v1 targets: 24-9000 without auth, 26-9000 with auth; never truncate the actual PDU) |

These defaults supply omitted values for declarative YAML sessions. The gRPC
`AddSession` boundary separately defaults omitted TX/RX to `1s` and rejects a
zero Detect Multiplier; it does not inherit these configurable defaults.

All detection multiplier fields are checked before numeric decoding, including
Echo, VXLAN, Geneve and Micro-BFD peers and defaults. Values outside 0–255,
fractions and booleans are rejected instead of wrapping or truncating; zero
retains its existing inheritance meaning where supported. The base BFD default
and enabled unsolicited defaults must be positive.

Peer and group collections must be lists of mappings; singleton mappings and
non-mapping list entries are rejected before weak decoding.
Multiplier lookup follows the decoder's case-insensitive matching; conflicting
case aliases for a field are rejected.

With `align_intervals: true`, creation rounds TX/RX up within the RFC 7419
common set (3.3 ms, 10 ms, 20 ms, 50 ms, 100 ms, 1 s); values above 1 s remain
unchanged. Alignment does not establish peer interoperability or timer accuracy.

> **Note**: Per RFC 5880 Section 6.8.3, when the session is not in Up state, the TX interval is enforced to be at least 1 second regardless of the configured value.

#### `gobgp` -- GoBGP Integration

See [GoBGP Integration](#gobgp-integration) for details.

#### `unsolicited` -- RFC 9468 Unsolicited BFD

| Key | Type | Default | Description |
|---|---|---|---|
| `unsolicited.enabled` | bool | `false` | Enable unsolicited BFD (MUST be disabled by default per RFC 9468) |
| `unsolicited.max_sessions` | int | `0` | Max dynamically created sessions; must be >= 1 when `enabled` |
| `unsolicited.cleanup_timeout` | duration | `"0s"` | Wait time before deleting Down passive sessions |
| `unsolicited.session_defaults.desired_min_tx` | duration | `"1s"` | Default TX interval for auto-created sessions |
| `unsolicited.session_defaults.required_min_rx` | duration | `"1s"` | Default RX interval for auto-created sessions |
| `unsolicited.session_defaults.detect_mult` | uint32 | `3` | Default detect multiplier for auto-created sessions |
| `unsolicited.interfaces.<name>.enabled` | bool | `false` | Enable unsolicited on this interface |
| `unsolicited.interfaces.<name>.allowed_prefixes` | []string | `[]` | Further restrict source addresses (CIDR); the connected-subnet check always applies |
| `unsolicited.auth.type` | string | `""` | Empty for no authentication, or `meticulous_keyed_sha1` (RFC 9468 Section 6.2) |
| `unsolicited.auth.key_id` | uint32 | `0` | RFC 5880 Auth Key ID (0-255) |
| `unsolicited.auth.secret` | string | `""` | 1-20 byte SHA1 key |

Example:
```yaml
unsolicited:
  enabled: true
  max_sessions: 10
  cleanup_timeout: "30s"
  session_defaults:
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
  interfaces:
    eth0:
      enabled: true
      allowed_prefixes: ["10.0.0.0/8"]
```

Each enabled interface must have an explicit nonempty name. It opens separate
IPv4 and IPv6 wildcard listeners on UDP 3784 with `SO_BINDTODEVICE`; no configured
session on the receiving address is required. Disabled interfaces open no
unsolicited listener. Configured per-address listeners remain available.

> **Admission (RFC 9468 Sections 2 and 6.1)**: a packet creates a session only
> when it arrives single-hop with TTL 255, its source belongs to a subnet of
> the local address that received it,
> is inside `allowed_prefixes` when that list is set, and is not the local
> address itself. New sessions are admitted at up to 10 per second (burst 20)
> and never beyond `max_sessions`. An unnumbered receive path is rejected.
>
> Each passive session replies from the local address and interface that
> received the triggering packet, with its own source port (IPv4 and IPv6).
>
> **Authentication (RFC 9468 Section 6.2)**: with `unsolicited.auth` set, the
> packet that would create a session must carry a valid Meticulous Keyed SHA1
> signature for the configured key, and the session signs its replies with
> it. Without `unsolicited.auth`, a packet with the A bit set is refused
> (RFC 5880 Section 6.8.6). A deployment that cannot use Meticulous Keyed
> SHA1 runs without authentication; RFC 9468 only recommends it.
>
> **Validation**: with `enabled: true`, loading fails unless
> `session_defaults` intervals are 1µs to 2^32-1µs, `required_min_rx` is
> nonzero (a passive session that asks the peer not to send never detects a
> failure and holds its slot), `detect_mult` is 1 to 255, `cleanup_timeout`
> is >= 0, at least one interface is enabled and every `allowed_prefixes`
> entry is a valid CIDR.
>
> **Unsafe preview**: bounded development qualification covers dual-stack
> packet capture and concurrent sender-lease release (`gobfd-qj0.8.2.1.10.1`).
> These receipts do not establish final-candidate or stable RFC qualification.
> Do not enable unsolicited BFD on an untrusted interface.

#### `echo` -- RFC 9747 Unaffiliated BFD Echo

| Key | Type | Default | Description |
|---|---|---|---|
| `echo.enabled` | bool | `false` | Enable BFD echo sessions |
| `echo.default_tx_interval` | duration | -- | Default echo transmit interval |
| `echo.default_detect_multiplier` | uint32 | -- | Default echo detection multiplier |
| `echo.peers[].peer` | string | -- | Remote system IP (echo target) |
| `echo.peers[].local` | string | -- | Local system IP |
| `echo.peers[].interface` | string | -- | Optional `SO_BINDTODEVICE` binding for both the receive listener and declarative TX sender; an empty value retains kernel route selection |
| `echo.peers[].tx_interval` | duration | -- | Override `default_tx_interval` for this peer |
| `echo.peers[].detect_mult` | uint32 | -- | Override `default_detect_multiplier` for this peer |

Echo sessions detect forwarding-path failures without requiring the remote system to run BFD. The local system sends BFD Control packets to UDP port 3785 on the remote, which forwards them back via normal IP routing.

Echo peers are reconciled on SIGHUP reload. Session key: `(peer, local, interface)`.

Both declarative and API-created Echo send to UDP destination port 3785.
An explicit `interface` applies `SO_BINDTODEVICE` to the receive listener
and TX sender; an empty value leaves device selection to the kernel. These
local transport checks do not establish full RFC 9747 qualification.

API creation also requires `echo.enabled: true`, a reconciled Echo source and
a ready runtime/receive binding. Otherwise `AddEchoSession` returns
`FailedPrecondition` before opening a sender or creating a session. API-only
deployments may omit `echo.peers`: the API claims its receive listener before
opening the sender. Valid SIGHUP reloads update this opt-in; rejected candidates
preserve it. Readiness loss rejects subsequent admission; already admitted
requests may complete. List/Delete remain available under management policy.
This guard does not promote RFC 9747 beyond preview.

Compatible config and API principal claims share one canonical Echo wire.
An admitted same-principal retry retains its sender and discriminator. Removing
`echo.peers[]` releases only the config claim; the physical lease preserves RX/TX
for remaining API claims. Parameter conflicts reject before changing existing
claims, and failed preparation retains the previous config set. See
[ADR-0009](adr/0009-echo-principal-claims.md).

Example:
```yaml
echo:
  enabled: true
  default_tx_interval: "100ms"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.1"
      local: "10.0.0.2"
      interface: "eth0"
      tx_interval: "50ms"
      detect_mult: 5
```

#### `micro_bfd` -- RFC 7130 Micro-BFD for LAG

Micro-BFD is a preview, disabled by default. Explicitly set
`micro_bfd.enabled: true` or `GOBFD_MICRO_BFD_ENABLED=true` before configuring
groups, selecting `dry-run` or `enforce`, or calling `AddMicroBFDGroup`.
Otherwise configuration validation fails before resources open, and the API
returns `FailedPrecondition` without creating a group. List and Delete remain
available subject to the existing management authorization and ownership rules.
Changing `micro_bfd.enabled` requires a restart; SIGHUP rejects the candidate
and preserves the running generation. Group changes remain reloadable when enabled.
Startup logs mark the enabled capability as preview. This opt-in does not
qualify the missing dedicated multicast MAC or enslaved-member TX behavior.

This is a permanent deployment safety setting, owned by the maintainer under
`gobfd-qj0.8.2.1.8.1`, introduced on 2026-10-22. It has no scheduled removal;
preview graduation is tracked separately under `gobfd-qj0.8.2.2.1`.

| Key | Type | Default | Description |
|---|---|---|---|
| `micro_bfd.groups[].lag_interface` | string | -- | Logical LAG interface name (e.g., "bond0") |
| `micro_bfd.enabled` | bool | `false` | Opt into the incomplete RFC 7130 preview; restart required |
| `micro_bfd.groups[].member_links` | []string | -- | Physical member link names |
| `micro_bfd.groups[].peer_addr` | string | -- | Remote system IP for all member sessions |
| `micro_bfd.groups[].local_addr` | string | -- | Local system IP |
| `micro_bfd.groups[].desired_min_tx` | duration | `bfd.default_desired_min_tx` | BFD timer interval for member sessions |
| `micro_bfd.groups[].required_min_rx` | duration | `bfd.default_required_min_rx` | Minimum acceptable RX interval |
| `micro_bfd.groups[].detect_mult` | uint32 | `bfd.default_detect_multiplier` | Detection time multiplier |
| `micro_bfd.groups[].min_active_links` | int | -- | Minimum Up members for LAG Up (>= 1) |
| `micro_bfd.actuator.mode` | string | `disabled` | `disabled`, `dry-run`, or `enforce` |
| `micro_bfd.actuator.backend` | string | `auto` | `auto`, `kernel-bond`, `ovs`, or `networkmanager` |
| `micro_bfd.actuator.ovsdb_endpoint` | string | `""` | OVSDB endpoint for `backend: ovs`; empty uses `unix:/var/run/openvswitch/db.sock` |
| `micro_bfd.actuator.owner_policy` | string | `refuse-if-managed` | Interface owner policy: refuse external ownership, allow external ownership, or use NetworkManager D-Bus |
| `micro_bfd.actuator.down_action` | string | `remove-member` | Action after a member transitions from Up to non-Up |
| `micro_bfd.actuator.up_action` | string | `add-member` | Action after a member transitions back to Up |

Micro-BFD runs independent BFD sessions on each LAG member link (UDP port 6784) with `SO_BINDTODEVICE` per member. The aggregate LAG state is Up when `upCount >= min_active_links`.

RFC 7130 enforcement is guarded by `micro_bfd.actuator`. The default
`disabled` mode is detect/report only. `dry-run` wires the daemon into the
policy layer and logs planned member actions. `enforce` currently requires
`owner_policy: allow-external` with `backend: kernel-bond` or `backend: ovs`.
The kernel-bond backend writes RFC 7130 remove/add commands through Linux
bonding sysfs. The OVS backend uses native OVSDB transactions against
`Port.interfaces` on an existing OVS bond port. Set
`micro_bfd.actuator.ovsdb_endpoint` when the local OVSDB socket differs from
the default `unix:/var/run/openvswitch/db.sock`. `backend: networkmanager` is
implemented through NetworkManager D-Bus and requires `owner_policy:
networkmanager-dbus`. It deactivates the active bond port profile when a member
goes Down and activates the remembered or available bond port profile when the
member recovers. Otherwise keep the default owner refusal policy for disabled
and dry-run modes.

Groups are reconciled on SIGHUP reload. Group key: `lag_interface`.

Each group and its member sessions apply as one unit. A failed member session
rolls back the groups and member sessions created by that reload; committed
groups and sessions stay in place. A physical member link belongs to one LAG: a
candidate that lists the same member link in groups of two LAG interfaces is
rejected as a whole, in either address family; RFC 7130 Section 2 runs
micro-BFD sessions per LAG member link. A group is keyed by `lag_interface` and
holds one `peer_addr`, so one LAG carries one address family; the second
micro-BFD session per member link that RFC 7130 Section 2.1 allows for the
other address family is not supported. Unset
`desired_min_tx`, `required_min_rx` and `detect_mult` inherit `bfd.default_*`.
The group and every member session share these values, and the RFC 5880
address, interval wire-bound and detect multiplier validation applies to them
(RFC 7130 Section 2.2). A changed member
list, address, timer or `min_active_links` of a running group is a
configuration conflict that leaves the group unchanged: remove the group in one
reload and add it in the next.

`AddMicroBFDGroup` applies the same validation, rejects a member link owned by
another group with `FAILED_PRECONDITION`, and creates the aggregate group only;
`ListMicroBFDGroups` returns the stored member timers.

Groups follow the session ownership model. Reload owns the groups it creates
and removes only those; an API group survives reload. An identical group from
the API and from reload is shared, and each source releases only its own
claim; a divergent group with the same `lag_interface` is a conflict, and a
reload group cannot take a member link of a surviving API group.
`DeleteMicroBFDGroup` releases only the caller's claim and returns
`FAILED_PRECONDITION` for a group that the caller does not own, such as a group
owned by reload. A new group starts each member from the state of its existing
member session: a member whose session is Up counts as Up and is reported to
the actuator in dispatch order.

Example:
```yaml
micro_bfd:
  enabled: true
  actuator:
    mode: "dry-run"
    backend: "kernel-bond"
    ovsdb_endpoint: ""
    owner_policy: "refuse-if-managed"
    down_action: "remove-member"
    up_action: "add-member"
  groups:
    - lag_interface: "bond0"
      member_links: ["eth0", "eth1"]
      peer_addr: "10.0.0.2"
      local_addr: "10.0.0.1"
      desired_min_tx: "100ms"
      required_min_rx: "100ms"
      detect_mult: 3
      min_active_links: 1
```

#### `vxlan` -- RFC 8971 BFD for VXLAN

| Key | Type | Default | Description |
|---|---|---|---|
| `vxlan.enabled` | bool | `false` | Enable VXLAN BFD sessions |
| `vxlan.backend` | string | `userspace-udp` | Overlay backend. Only `userspace-udp` is implemented; `kernel`, `ovs`, `ovn`, `cilium`, `calico`, and `nsx` are reserved and fail validation |
| `vxlan.management_vni` | uint32 | -- | Management VNI for BFD control (24-bit, max 16777215) |
| `vxlan.default_desired_min_tx` | duration | -- | Default TX interval for VXLAN sessions |
| `vxlan.default_required_min_rx` | duration | -- | Default RX interval for VXLAN sessions |
| `vxlan.default_detect_multiplier` | uint32 | -- | Default detection multiplier |
| `vxlan.peers[].peer` | string | -- | Remote VTEP IP address |
| `vxlan.peers[].local` | string | -- | Local VTEP IP address |
| `vxlan.peers[].peer_mac` | string | -- | Required remote VTEP unicast source MAC |
| `vxlan.peers[].local_mac` | string | -- | Required local VTEP unicast source MAC |
| `vxlan.peers[].desired_min_tx` | duration | -- | Override default TX interval for this peer |
| `vxlan.peers[].required_min_rx` | duration | -- | Override default RX interval for this peer |
| `vxlan.peers[].detect_mult` | uint32 | -- | Override default detect multiplier for this peer |

BFD Control packets are encapsulated in VXLAN (outer UDP port 4789) with a dedicated Management VNI. The inner packet stack uses the configured originating VTEP source MAC and the assigned destination MAC `00:52:02:00:00:00`, IPv4 (TTL=255), and UDP (dst 3784) headers. The current `userspace-udp` backend owns `local:4789`; use a future owner-specific backend rather than this mode when kernel VXLAN, OVS/OVN, Cilium, Calico, NSX, or another dataplane already owns the same socket.

> **Unsafe preview**: the supported IPv4 Format A profile is validated and
> bound to its exact tunnel identity. The userspace socket ownership model is
> still suitable only for an isolated endpoint; this backend is not production-qualified.

Peers are reconciled on SIGHUP reload. Session identity includes the backend,
outer endpoints, Management VNI, inner endpoints, address family, and MAC tuple.

Example:
```yaml
vxlan:
  enabled: true
  backend: "userspace-udp"
  management_vni: 16777215
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.2"
      local: "10.0.0.1"
      peer_mac: "02:00:00:00:00:02"
      local_mac: "02:00:00:00:00:01"
    - peer: "10.0.0.3"
      local: "10.0.0.1"
      peer_mac: "02:00:00:00:00:03"
      local_mac: "02:00:00:00:00:01"
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 5
```

#### `geneve` -- RFC 9521 BFD for Geneve

| Key | Type | Default | Description |
|---|---|---|---|
| `geneve.enabled` | bool | `false` | Enable Geneve BFD sessions |
| `geneve.backend` | string | `userspace-udp` | Overlay backend. Only `userspace-udp` is implemented; `kernel`, `ovs`, `ovn`, `cilium`, `calico`, and `nsx` are reserved and fail validation |
| `geneve.default_vni` | uint32 | -- | Default Geneve VNI (24-bit, max 16777215) |
| `geneve.default_desired_min_tx` | duration | -- | Default TX interval for Geneve sessions |
| `geneve.default_required_min_rx` | duration | -- | Default RX interval for Geneve sessions |
| `geneve.default_detect_multiplier` | uint32 | -- | Default detection multiplier |
| `geneve.peers[].peer` | string | -- | Remote NVE IP address |
| `geneve.peers[].local` | string | -- | Local NVE IP address |
| `geneve.peers[].vni` | uint32 | -- | Override `default_vni` for this peer (0 = use default) |
| `geneve.peers[].inner_peer` | string | -- | Required remote Format A VAP IPv4 address |
| `geneve.peers[].inner_local` | string | -- | Required local Format A VAP IPv4 address |
| `geneve.peers[].peer_mac` | string | -- | Required remote Format A VAP unicast MAC |
| `geneve.peers[].local_mac` | string | -- | Required local Format A VAP unicast MAC |
| `geneve.peers[].desired_min_tx` | duration | -- | Override default TX interval for this peer |
| `geneve.peers[].required_min_rx` | duration | -- | Override default RX interval for this peer |
| `geneve.peers[].detect_mult` | uint32 | -- | Override default detect multiplier for this peer |

BFD Control packets are encapsulated in Geneve (outer UDP port 6081) with Format A (Ethernet payload, Protocol Type 0x6558). Per RFC 9521 Section 4: O bit (control) is set to 1, C bit (critical) is set to 0. The current `userspace-udp` backend owns `local:6081`; use a future owner-specific backend rather than this mode when kernel Geneve, OVS/OVN, NSX, or another dataplane already owns the same socket.

> **Unsafe preview**: configured IPv4 Format A VAP identities are validated and
> bound exactly. Missing VAP identity, other address families, and other Geneve
> payload formats fail closed. The userspace socket ownership model remains lab-only.

Peers are reconciled on SIGHUP reload. Session identity includes the backend,
outer endpoints, VNI, both VAP IP/MAC endpoints, and address family.

Example:
```yaml
geneve:
  enabled: true
  backend: "userspace-udp"
  default_vni: 100
  default_desired_min_tx: "1s"
  default_required_min_rx: "1s"
  default_detect_multiplier: 3
  peers:
    - peer: "10.0.0.2"
      local: "10.0.0.1"
      inner_peer: "192.0.2.2"
      inner_local: "192.0.2.1"
      peer_mac: "02:00:00:00:00:02"
      local_mac: "02:00:00:00:00:01"
    - peer: "10.0.0.3"
      local: "10.0.0.1"
      vni: 200
      inner_peer: "192.0.2.4"
      inner_local: "192.0.2.3"
      peer_mac: "02:00:00:00:00:04"
      local_mac: "02:00:00:00:00:03"
      desired_min_tx: "300ms"
      required_min_rx: "300ms"
      detect_mult: 5
```

#### `socket` -- Socket Buffer Tuning

| Key | Type | Default | Description |
|---|---|---|---|
| `socket.read_buffer_size` | int | `4194304` (4 MiB) | `SO_RCVBUF` size in bytes for listener sockets. Larger buffers reduce packet loss under high session counts. Set to `0` to use the OS default. |
| `socket.write_buffer_size` | int | `4194304` (4 MiB) | `SO_SNDBUF` size in bytes for sender sockets. Larger buffers reduce `sendto` failures under burst conditions. Set to `0` to use the OS default. |

Both values must be nonnegative integers representable as `int`. `read_buffer_size` applies to the shared
single-hop and multihop control listeners, the interface-bound IPv4 and IPv6
wildcard single-hop listeners opened for each enabled unsolicited interface,
Echo listeners, and Micro-BFD member listeners. `write_buffer_size` applies to
API and declarative control, Echo, Micro-BFD, and dynamically admitted
unsolicited session senders. Userspace VXLAN and Geneve sockets
are duplex and receive both settings. Management HTTP/Unix sockets and
non-UDP dataplane backends are outside this policy.

The values are requested through `SO_RCVBUF` / `SO_SNDBUF` at socket creation.
Zero leaves the corresponding OS default unchanged. A socket-option error
fails that socket construction and closes the new socket. Buffer policy is
captured at startup, including for senders created later; changing either
value through SIGHUP is rejected and requires a process restart.

Qualify buffer sizes using actual burst load, requested/effective kernel
limits and observed socket drops. Session count alone does not establish a
safe size; zero retains OS defaults rather than a tested capacity guarantee.

> **Note**: The kernel may cap the effective buffer size at `net.core.rmem_max` / `net.core.wmem_max`. To allow larger buffers, increase these sysctl values:
> ```bash
> sysctl -w net.core.rmem_max=16777216
> sysctl -w net.core.wmem_max=16777216
> ```

#### `sessions` -- Declarative Sessions

See [Declarative Sessions](#declarative-sessions) for details.

### Environment Variables

The daemon supports the explicit environment overrides below. Other or
ambiguous `GOBFD_` names are ignored; underscores are not interpreted by a
generic nesting rule. Environment values override YAML, which overrides the
built-in defaults.

| Environment Variable | Config Key | Example |
|---|---|---|
| `GOBFD_GRPC_ADDR` | `grpc.addr` | `"127.0.0.1:50052"` (loopback; default empty) |
| `GOBFD_METRICS_ADDR` | `metrics.addr` | `"127.0.0.1:9100"` |
| `GOBFD_METRICS_PATH` | `metrics.path` | `"/metrics"` |
| `GOBFD_LOG_LEVEL` | `log.level` | `"debug"` |
| `GOBFD_LOG_FORMAT` | `log.format` | `"text"` |
| `GOBFD_UNSOLICITED_ENABLED` | `unsolicited.enabled` | `true` |
| `GOBFD_ECHO_ENABLED` | `echo.enabled` | `true` |
| `GOBFD_MICRO_BFD_ENABLED` | `micro_bfd.enabled` | `true` |
| `GOBFD_SOCKET_READ_BUFFER_SIZE` | `socket.read_buffer_size` | `8388608` |
| `GOBFD_SOCKET_WRITE_BUFFER_SIZE` | `socket.write_buffer_size` | `8388608` |
| `GOBFD_VXLAN_ENABLED` | `vxlan.enabled` | `true` |
| `GOBFD_VXLAN_BACKEND` | `vxlan.backend` | `"userspace-udp"` |
| `GOBFD_GENEVE_ENABLED` | `geneve.enabled` | `true` |
| `GOBFD_GENEVE_BACKEND` | `geneve.backend` | `"userspace-udp"` |
| `GOBFD_GOBGP_ENABLED` | `gobgp.enabled` | `true` |
| `GOBFD_GOBGP_ADDR` | `gobgp.addr` | `"127.0.0.1:50051"` |
| `GOBFD_GOBGP_STRATEGY` | `gobgp.strategy` | `"disable-peer"` |
| `GOBFD_GOBGP_TLS_ENABLED` | `gobgp.tls.enabled` | `true` |
| `GOBFD_GOBGP_DAMPENING_ENABLED` | `gobgp.dampening.enabled` | `true` |

### Declarative Sessions

Sessions defined under `sessions:` are created on daemon startup and
reconciled on SIGHUP reload. The base, Micro-BFD member, VXLAN, and Geneve
adapters forward empty desired sets and use distinct typed reconciliation
owners. Removing the final base entry therefore releases only the base
configuration claim. Reconciliation semantics are:

- **New desired entries** acquire a configuration claim and create a wire
  session only when no exact session exists
- **Removed sessions** release their configuration claim; the wire session is
  destroyed only when no other claim remains
- **Existing exact sessions** are shared; conflicting effective parameters are
  rejected without mutating the manager's ownership claims or session
  registries

Each source claims/prepares desired resources before releasing old claims;
release requires zero `Failed` and `ResourcePending`, independently of
`TimerPending`. Fatal preparation rolls back newly created claims; valid
disjoint claims may remain for resource-pending retry. An occupied receive key
permits a prepared SingleHop/MultiHop swap only for an interface-empty base
session with one config owner, no old identity in desired and all other identity
fields unchanged. Shared/API/unsolicited, same-type and other-scope collisions
are rejected before the sender factory; base admission also rejects them before
generation acceptance. Cancellation before replacement publication keeps the
old identity. After publication, the old session performs actual AdminDown/drain
outside Manager/lifecycle mutation locks before lease/discriminator cleanup;
typed incomplete drain is reported. Peer-visible cutover has no hitless-rollback guarantee.

Before startup or SIGHUP applies any control-session source, the daemon
compiles and validates the complete combined base, Micro-BFD member, VXLAN,
and Geneve candidate. Each adapter also validates its complete source set
before opening a sender or mutating Manager session ownership. A conversion,
session-validation, or duplicate error rejects the combined candidate.
Conflicts with a running exact session are rejected before ownership mutation.
The broader SIGHUP reload remains non-transactional:
log-level changes, sender lifecycle, transport resources, listener/backend
replacement, and cross-adapter rollback are outside this guarantee.

The YAML uniqueness key is the tuple: `(peer, local, interface)`.

| Key | Type | Required | Description |
|---|---|---|---|
| `peer` | string | Yes | Remote system's IP address |
| `local` | string | No | Local IP; use an explicit address for a verified receive binding |
| `interface` | string | No | Network interface for `SO_BINDTODEVICE` on single-hop RX and TX; rejected on `multi_hop` (`ErrInvalidSessionMultiHopInterface`) |
| `type` | string | No | `single_hop` (default) or `multi_hop` |
| `desired_min_tx` | duration | No | Override default TX interval |
| `required_min_rx` | duration | No | Override default RX interval; explicit `"0s"` is preserved |
| `detect_mult` | uint32 | No | Override default detect multiplier |
| `padded_pdu_size` | uint16 | No | RFC 9764: pad BFD packets to this size; nonzero overrides `bfd.default_padded_pdu_size`; v1 rejects authenticated targets 24/25 and retains larger actual PDUs |
| `multihop_max_hops` | uint32 | No | `multi_hop` only: hop policy 1-254, default 1; accepts received TTL/Hop Limit >= 255 - N (1/2/5 -> 254/253/250). Explicit `0` and the key on `single_hop` are rejected (`ErrInvalidSessionMultihopMaxHops`). YAML only: API sessions use the default |
| `auth.type` | string | No | RFC 5880 auth type: `simple_password`, `keyed_md5`, `meticulous_keyed_md5`, `keyed_sha1`, `meticulous_keyed_sha1` |
| `auth.key_id` | uint32 | If auth enabled | Auth Key ID, valid range 0-255 |
| `auth.secret` | string | If auth enabled | Secret: 1-16 bytes for Simple Password/MD5, 1-20 bytes for SHA1 |

Public API `AddSessionRequest.padded_pdu_size` and CLI `--padded-pdu-size`
carry a `uint32` target in bytes. Omission or `0` disables padding without
inheriting `bfd.default_padded_pdu_size`; a YAML session's `0` inherits that
global default. Both paths use the same target bounds and retain larger
authenticated PDUs.

For base single-hop and multihop sessions, omitted or null `required_min_rx`
inherits `bfd.default_required_min_rx`; explicit `"0s"` requests that the peer
stop periodic Control packets (RFC 5880 sections 4.1 and 6.8.18). Zero remains
zero with `align_intervals` enabled. Generic gRPC `AddSession` likewise preserves
a present zero Duration, while an omitted Duration defaults to `1s`.
The local transmitter still follows the peer's advertised receive interval;
local zero does not itself disable local transmission or enable Demand Mode.
Config-owned timer reloads preserve the same explicit-zero semantics.
Preview Micro-BFD/VXLAN/Geneve per-peer zero-as-inherit rules are unchanged.
The `sessions` value must be a YAML sequence of mappings. Receive intervals
reject booleans and fractional numeric values; use duration strings such as `"100ms"`.

Control-session admission requires TX from `1us` to `4294967295us` and RX either
zero or within the same range. Positive intervals below `1us` are rejected with
`ErrInvalidWireInterval`: truncating them to wire zero would emit reserved TX zero
or request no periodic RX. Durations of at least `1us` retain fractional microseconds locally;
wire fields truncate to whole microseconds.

Authentication can be configured through declarative YAML sessions or through
gRPC `AddSession` fields: `auth_type`, `auth_key_id`, and `auth_secret`.
`auth_key_id` must fit the RFC 5880 one-octet key ID range. `auth_secret` must
be 1-16 bytes for Simple Password/MD5 modes and 1-20 bytes for SHA1 modes.
Requests that provide key material without an enabled `auth_type`, or enable
auth without a secret, are rejected.

Session types determine the UDP port and TTL handling:

| Type | Port | Outgoing TTL | Incoming TTL Check |
|---|---|---|---|
| `single_hop` | 3784 | 255 (GTSM) | MUST be 255 (RFC 5881) |
| `multi_hop` | 4784 | 255 | >= 255 - `multihop_max_hops` (default >= 254), checked in the demultiplexed session |

RFC 5883 defines no receive TTL rule. The multi-hop check is GoBFD policy based
on the non-normative RFC 5082 Appendix A (reading A: N counts intermediate
routers, as FRR `minimum-ttl` does). The 4784 listener passes every packet to
the session; a multi-hop packet never reaches a single-hop session and vice
versa (RFC 5881 Section 5). With `multihop_max_hops` above 1, only
authentication limits spoofing: RFC 5883 Section 6 says implementations SHOULD
use cryptographic authentication over multihop paths; prefer
`meticulous_keyed_sha1`. Real 2- and 5-hop paths are not qualified.

A single-hop session accepts packets only from its interface (RFC 5881
Section 3). The interface is the configured `interface`; without it, the zone of
a link-local `local` or `peer` address (`fe80::2%eth0`); without either, the
interface that owns `local`. GoBFD resolves the interface index once at session
creation and compares it with the PKTINFO interface index of every packet. The
zone also selects the listener device and the sender `SO_BINDTODEVICE`, so one
link-local pair on two links forms two sessions. A zone that names another
interface than `interface`, or differs between `peer` and `local`, is rejected
(`ErrInvalidSessionZone`; API `InvalidArgument`). A recreated interface gets a
new index and needs the session recreated.

A YAML session whose `local` address no interface or several interfaces own is
pending, not failed: neighbouring sessions are created, the reconciliation
receipt counts it as pending. Linux link events, address events, inventory
resyncs and SIGHUP retry pending sources. Adding the local address triggers
retry without a link-state change. While a session is pending, the reconciliation
is stale and gRPC health readiness stays `NOT_SERVING`. An address owned by
several interfaces stays ambiguous on every retry, so the session stays pending
and readiness stays `NOT_SERVING` until the configuration sets `interface` (or
the duplicate address is removed) and is applied with SIGHUP. The API returns `FailedPrecondition` for the
same case. A `local` address assigned to `lo` or a dummy interface resolves to
that interface; packets that arrive on a physical interface are then refused as
`demux`/`scope_mismatch`. Set `interface` for such deployments.

### GoBGP Integration

When enabled, BFD state changes are propagated to a GoBGP instance via its gRPC API (RFC 5882 Section 10.2):

- **BFD Down** --> Disable BGP peer (`DisablePeer()`)
- **BFD Up** --> Enable BGP peer (`EnablePeer()`)

| Key | Type | Default | Description |
|---|---|---|---|
| `gobgp.enabled` | bool | `false` | Enable/disable GoBGP integration |
| `gobgp.addr` | string | `"127.0.0.1:50051"` | GoBGP gRPC API address |
| `gobgp.strategy` | string | `"disable-peer"` | `disable-peer` is the only implemented strategy; `withdraw-routes` is recognized but rejected as unsupported |
| `gobgp.action_timeout` | duration | `"5s"` | Maximum time allowed for each GoBGP API action |
| `gobgp.tls.enabled` | bool | `false` | Use TLS for the GoBGP gRPC connection |
| `gobgp.tls.ca_file` | string | `""` | Optional PEM root CA bundle; empty uses system roots |
| `gobgp.tls.server_name` | string | `""` | Optional TLS server name override |

Plaintext GoBGP gRPC is retained for compatibility with common localhost
deployments. Enable `gobgp.tls.enabled` for remote or non-loopback GoBGP API
endpoints. When GoBGP is enabled with plaintext and `gobgp.addr` is not
loopback/localhost, GoBFD logs a startup warning.

#### Peer Ownership

GoBFD enables only peers it disabled itself (maintainer decision,
`gobfd-qj0.8.2.4.1`). Before `DisablePeer` it reads the neighbor's GoBGP
admin state; a peer that is already administratively down (for example
disabled by an operator) is left alone on BFD Down and on the following BFD
Up. GoBGP does not expose who disabled a peer, so this record lives in
memory: after a GoBFD restart, peers that GoBFD had disabled stay disabled
until an operator enables them (`gobgp neighbor <addr> enable`).

If the handler falls behind the BFD event stream, it receives a gap marker
and resyncs from a revisioned snapshot: a session it last saw Up and that
is now Down disables the peer, an Up re-enables a peer GoBFD disabled, and a
session it never saw that is Down causes no action, as at startup.

#### Session Identity

Only a single-hop or multi-hop session drives a GoBGP neighbor
(`gobfd-qj0.8.2.4.2`). Micro-BFD member links, VXLAN, Geneve and Echo
sessions to the neighbor address never disable or enable the BGP peer.
IPv4-mapped peer addresses (`::ffff:192.0.2.1`) name the same neighbor as
the plain IPv4 address.

Each neighbor needs one canonical BFD session. With `gobgp.enabled`,
configuration validation (startup and reload) rejects two `sessions`
entries to the same peer address (`gobgp peer has more than one BFD
session`). A second base session created at runtime (API or unsolicited)
leaves no canonical session: on BFD Down GoBFD logs an error, does not
disable the peer and sets the `gobfd.gobgp` health service to
`NOT_SERVING` until the next successful GoBGP action. A peer GoBFD had
already disabled is still re-enabled on BFD Up.

#### GoBGP Compatibility

| GoBFD line | GoBGP daemon | gRPC service | Evidence |
|---|---|---|---|
| `dev` (v1 line) | v4.10.0 | `api.GoBgpService` | BGP+BFD, RFC, fast-failover and ExaBGP anycast gates passed on 2026-10-13 |
| `release/v0.6` | v3.37.0 | `apipb.GobgpApi` | v0.6 public contract |

GoBFD on `dev` supports GoBGP v4 daemons only; GoBGP v3 and v4 gRPC services
are wire-incompatible. The support claim covers the tested v4.10.0; earlier
v4 releases are not tested. GoBGP v4.4.0 is the first release with the
GHSA-4p9m-8gc4-rw2h NEXT_HOP fix, so do not run older v4 daemons.

Upgrading a GoBGP v3 deployment together with `dev` GoBFD:

1. Upgrade `gobgpd` to v4.10.0 in the same change as GoBFD.
2. Remove `neighbors.transport.config.ttl` from the GoBGP configuration;
   GoBGP v4.0.0 removed this option.
3. Keep GoBGP native BFD disabled (the default) for every peer that has a
   GoBFD session.
4. Start GoBFD. Its preflight calls `GetBgp` and `ListPeer`: a v3 endpoint or
   native BFD on a GoBFD peer stops startup with an actionable error, and an
   unreachable endpoint is re-checked in the background.

#### Flap Dampening

Prevents rapid BFD oscillation from causing excessive BGP route churn. This
penalty-based algorithm is GoBFD implementation policy; RFC 5882 Section 3.1
permits session-state hysteresis but does not standardize this algorithm.

Each BFD Down adds a penalty of 1 that decays with `half_life`. Dampening
never holds a Down: every qualifying Down disables the peer at once
(`gobfd-qj0.8.2.4.1`, H-27). Once the penalty reaches `suppress_threshold`,
dampening holds only the recovery: BFD Up re-enables the peer after the
penalty decays below `reuse_threshold` or after `max_suppress_time`. A Down
while the recovery is held cancels it, and the peer stays disabled.

| Key | Type | Default | Description |
|---|---|---|---|
| `gobgp.dampening.enabled` | bool | `false` | Enable flap dampening |
| `gobgp.dampening.suppress_threshold` | float64 | `3` | Penalty from which recovery is held |
| `gobgp.dampening.reuse_threshold` | float64 | `2` | Penalty below which suppression is lifted; must be > 0 |
| `gobgp.dampening.max_suppress_time` | duration | `"60s"` | Maximum suppression duration; must be > 0 |
| `gobgp.dampening.half_life` | duration | `"15s"` | Penalty decay half-life |

### Hot Reload (SIGHUP)

Sending `SIGHUP` to the gobfd process triggers configuration reload:

```bash
# Reload configuration
sudo systemctl reload gobfd
# or
kill -HUP $(pidof gobfd)
```

On reload:
1. The YAML file is re-read and validated
2. Under one coordinator lock, the candidate is checked against the immutable
   startup-runtime contract. A mismatch is rejected with bounded field paths
   before generation, health, log level, sessions, or resources change
3. The complete base, Echo, Micro-BFD group and
   member, VXLAN, and Geneve session candidate is compiled and validated before
   sender creation or Manager mutation
4. A new desired generation is published and gRPC empty-service and `bfd.v1`
   named health become `NOT_SERVING`; an invalid candidate leaves generations, health, and log level
   unchanged
5. Log level is updated dynamically after validation (no restart needed); this
   setting is not rolled back if a later source fails
6. The base session source is reconciled, including an empty desired set
7. Echo peers are reconciled as config claims, including the disabled or empty
   desired set. Compatible API claims share the wire. New claims are prepared
   before stale config claims are released; only new physical wires acquire
   sender leases
8. Micro-BFD groups and the member-session source are reconciled, including
   empty desired sets
9. VXLAN and Geneve are reconciled as distinct sources, including empty
   desired sets
10. One aggregate receipt records bounded counts and error codes for all five
   sources. Applied advances to desired only if all five converge. Each named
   service returns to `SERVING` when its own sources converge, and the empty
   service when all five do; runtime and listener components also apply
   (`07-monitoring.md`)

Each source receipt also retains fixed `interface_pending` and `address_pending`
presence flags. They distinguish unavailable resource classes, not claim counts
or resource identities. A class change publishes a new receipt even if `pending`
is unchanged; repeating the same receipt remains a no-op. Convergence clears the
flags. Permanent failures and admitted timer-only waits do not acquire resource
flags. Raw error text and resource IDs remain transient diagnostics.

`log.level` applies immediately after the complete candidate passes validation.
On Linux, an existing but down configured interface produces `pending` after
whole-source availability preflight. Link events retry affected pending sources
against the retained generation automatically; another SIGHUP is not required.
Repeated events coalesce and do not replay converged sources. Netlink loss
requests a full interface resync. Address events (`RTM_NEWADDR`/`RTM_DELADDR`,
including IPv6 DAD completion) and resyncs retry every pending source; they
carry no link state and never take sessions down. Receiving these rtnetlink
groups needs no capability. The non-Linux stub provides no automatic retry, so
there a later SIGHUP retries pending sources.

Receive listeners are part of the generation. Before the base, Echo, or
Micro-BFD source applies, it claims the UDP listeners its desired set needs
(3784/4784, 3785, 6784) and opens the missing ones. The Manager apply runs only
after every claimed listener is open, so no source converges on senders alone.
A listener whose interface does not exist leaves the source `pending`, and the
automatic interface retry opens it; any other open error leaves the source
`failed`. A local address that is not assigned yet or still IPv6-tentative
(`EADDRNOTAVAIL`) on a listener, sender, or overlay backend bind is a typed
unavailable address that leaves the source `pending`. A listener that cannot
bind stops its source before Manager apply, so old claims stay; an incomplete
overlay acquire likewise holds the whole source. A pending control-session
sender claim retains every old claim of its Manager source, while valid
disjoint new claims may remain tracked. The address
event that assigns the address retries the source. An API-created
control or Echo wire retains its listener with its sender and opens it when
absent; Echo listeners share only an exact address/interface/transport key. An absent
interface or unassigned address fails `AddSession` or `AddEchoSession` with
`Unavailable`, another open error with `FailedPrecondition`, and no session is
created. Retiring the final session claim drops its lease-owned listener
reference. After a source applies without failed or resource-pending entries,
it drops its claims outside the desired set; a listener closes when its last
source, unsolicited, or API claim goes.
Unchanged listeners, including the startup-owned unsolicited wildcard
listeners, are neither reopened nor replaced. A device-bound listener whose
interface is deleted and recreated under the same name is rebound in place on
the next interface event or 2 s recheck: the daemon re-applies
`SO_BINDTODEVICE` to the same socket. Linux requires `CAP_NET_RAW` to change an
existing device binding; the shipped systemd unit, Compose files, and
Kubernetes manifest grant it for the process lifetime. A failed rebind keeps
the binding `NOT_SERVING` and is retried. At startup any listener failure,
including an absent interface, still fails the start. Non-Linux builds cannot
create listeners, so such a source is `failed`.

An existing device-bound UDP sender also retains its socket and source port
through interface recreation. A zero-byte write failure with `ENODEV` or
`ENETUNREACH` revalidates the named device, updates a stale `SO_BINDTODEVICE`
binding and retries the datagram once. After verifying a positive bound ifindex,
a zone matching the device name uses the socket's device scope instead of a
per-datagram zone. Subsequent matching sends retain that scope, avoiding the
forced Go interface-table refresh caused by a cached numeric zone. Explicit
numeric and unrelated zones are preserved. Successful writes do not enter
GoBFD's rebind helper; this is not a guarantee that `SendPacket` allocates no
memory. Corrective task: `gobfd-qj0.8.2.2.6.7`. See
[`SendPacket`](../../internal/netio/sender.go) and
[`rebindDevice`](../../internal/netio/sender_rebind_linux.go).
During recovery, missing devices, denied rebinding and canceled or closed owners remain errors;
the sender never clears its device binding or falls back to another interface.
This recovery requires the existing `CAP_NET_RAW` grant. It does not guarantee
hitless delivery or prevent BFD timeout while the interface/address is absent.

Overlay backends are part of the generation as well. One userspace-udp socket
serves each tunnel kind and outer local address (VXLAN UDP 4789, Geneve UDP
6081). Before the VXLAN or Geneve source applies, it claims the identities of
its desired set: a new local endpoint opens a backend, and an identity added
on an open endpoint joins that socket's identity set in place, so live
sessions keep their socket. Pure whole-source scope preflight precedes the first
backend open or `SetScopes`: an unequal scope sharing a live/draining wire
identity, including a VXLAN `local_mac` change, requires explicit separate
removal/recreation. A never-committed unused preparation can instead be
superseded by a new same-wire scope; duplicate desired wire identities remain
invalid. Successful preflight prunes obsolete unused preparations before new
claims. Failures are classified per endpoint, `pending` for an unassigned
address and `failed` otherwise. Any incomplete acquire returns before Manager
apply or release for the entire overlay source, retaining every old session,
live/draining exact scope and backend. Retained scopes are existing scopes in
current desired plus scopes with physical-session lease refs; the factory
increments refs. Registered sessions retain refs through retirement drain,
then release them on idempotent close; unused/unpublished leases use ordinary
once-only cleanup without drain. Currently desired preparations may remain for
retry. This is source-local apply protection, not global pre-generation
overlay admission or all-source atomicity. After an apply without failed or resource-pending
entries, each backend retains existing desired identities and live/draining
lease refs; an empty backend closes and returns its inner source port. A failed apply keeps the
currently needed set and live/draining refs. Startup claims backends the same way, so a backend that cannot open
fails or pends its source instead of stopping the daemon. The ports are
unprivileged and the sockets are not device-bound, so no capability is needed.

The five desired sets reconcile session/group membership. Control, Echo, and
Micro-BFD entries may be added, removed, or re-keyed, including entries that
need a new local address, interface, or session type. VXLAN and Geneve entries
may be added, removed, or re-keyed, including a new local endpoint or a new
per-peer Geneve VNI. BFD defaults and per-entry timers,
padding, authentication, peer, and threshold values apply when a new identity
is created. Solely config-owned base sessions also accept in-place changes to
`desired_min_tx`, `required_min_rx`, `detect_mult`, the multihop TTL floor and
qualified `padded_pdu_size` without replacing the session or discriminator,
with unchanged authentication. The tuple and generation/revision references,
confirmed/effective receipts carry TX, RX, DetectMult, MinTTL and PaddedPduSize.
A detect-only Async update applies locally
without Poll and appears in the next Control packet (RFC 5880 §6.8.12).
Remote Demand with both ends Up requires Poll/Final (RFC 5880 §6.6), so that
receipt remains `pending` until confirmation. Entering or leaving
`detect_mult: 1` reschedules TX for the applicable jitter window.

Changing `multihop_max_hops` updates the local receive floor `255 - N` under
`Session.mu`; TTL-only updates need no Poll even under remote Demand. This is
GoBFD admission policy, not a normative RFC 5883 receive-TTL rule.
Padding reload permits only positive-to-positive targets on an actually
constructor-DF-prepared sender in Async (`remote D` unset). Immutable
`PaddingPrepared` proves preparation, not socket liveness. Unknown/unprepared
senders, `0` transitions and shared-owner/API base padding updates are rejected
before desired generation, log-level or resource changes. Micro-BFD and overlay
remain outside the qualified profile; their configuration has no padding/minTTL fields.
If Demand activates while a tuple waits, owner revalidation fails the whole
pending tuple before any field mutation. Policy precedes equality shortcuts,
including reversal to a confirmed target while Demand is active.

One active Poll transaction keeps its advertised tuple; newer reloads replace
only the latest waiting proposal. Combined TX/RX/Detect Multiplier changes
retain the timer direction rules: TX increases and RX decreases defer their
protected local effect until Final; the reverse directions take effect immediately.
Poll-dependent admission remains `pending`, not `updated`, until confirmation.
Completion updates the current generation
and readiness automatically without a second SIGHUP. Timeout or loss of Up
fails the receipt; a late Final does not turn that failure into success.
Cancellation after admission does not undo advertised values. The
[transaction contract](../superpowers/specs/2026-09-08-gobfd-timer-update-transactions-design.md)
defines the negotiation budget and startup/recovery separation. Scoped local
FRR/BIRD Async and crafted remote-Demand qualification is recorded in
`gobfd-qj0.8.2.2.7.5.2`; final exact-candidate qualification remains separate.
This does not establish full Poll/Demand compliance.

Unsupported authentication changes on a retained base identity are rejected
before accepting a new generation (`ErrAuthenticationReloadUnsupported`). This
includes authentication enable/disable, algorithm, key IDs/material, selected
TX key and receive-key-set changes. Admission compares immutable fingerprints
against both retained accepted desired entries, even without live sessions,
and live Manager sessions, including API/shared owners. Rejection preserves
the accepted configuration, generation, log level, readiness, configured peers
and existing sessions, before listener or sender resources are applied.
Startup authentication, new/removed identities and semantically identical
fresh key stores remain admissible. No key rotation is enabled. Claims acquired
after acceptance still face normal fail-closed Manager apply; this admission
boundary does not make the whole reload atomic or guarantee seamless rollback.

The coordinator passes only `candidate.base` to `Manager.AdmitSessionReload`;
unsupported stable-base field and ownership transitions reject the complete
candidate before acceptance. Other-source effective changes retain their
existing per-source conflict/stale behavior. API claims
acquired later still face ordinary fail-closed Manager apply. Other modes gain
no in-place packet-policy support; incompatible Micro-BFD group changes still
report reconciliation conflicts. Removing the final entry or disabling a
source remains supported.

Padding capacity growth prepares one replaceable waiting buffer at control
admission; owner promotion swaps it before field commit. No growth allocation
is deferred to packet transmission. The target counts payload bytes excluding
IP/UDP headers; transmission uses `max(declared PDU length, target)`, never
truncates authentication, and zero-fills outside its existing MAC domain.
Authenticated targets still require at least 26 bytes; zero-target DF defaults
are unchanged. Padding enable/disable and dynamic DF preparation remain
after-v1 P2 `gobfd-ebu`.

[ADR-0006](adr/0006-session-preserving-reload-policy.md) records the accepted
policy. Packet-policy child `gobfd-qj0.8.2.2.7.2` is closed on dev;
P0 integration `gobfd-qj0.8.2.2.7.4` with `gobfd-qj0.8.2.2.7.4.1` and
`gobfd-qj0.8.2.2.7.4.2` has bounded local Go qualification and remains in progress
pending documentation checks/local integration. Mandatory P0 actual-wire gate
`gobfd-qj0.8.2.2.7.5` remains open, including RFC 9764 peer/MTU qualification.
Coordinated key-ID rotation remains after-v1 P2 in `gobfd-khr`.

The following settings require a process restart: `grpc.*`, `metrics.*`,
`log.format`, `socket.*`, `unsolicited.*`, `micro_bfd.enabled`, `micro_bfd.actuator.*`, `gobgp.*`,
`vxlan.backend`, `vxlan.management_vni`, `geneve.backend`, and
`geneve.default_vni`. The two VNI defaults re-key every identity of their
source at once; runtime support for that is not implemented, which is a scope
limit rather than a kernel or privilege boundary. A rejected reload logs only
these bounded field paths, never configuration values or authentication
secrets. Socket buffer values are included in this startup-only boundary
and apply to every supported UDP socket described in the socket tuning section.

Changing `grpc.unsafe_loopback_mutations` in either direction rejects the
reload before generation acceptance, log-level changes, peer callbacks or
session/resource application. The running RPC policy and `gobfd.management`
health retain their startup values; changing this policy requires a restart.
Reapplying the unchanged policy remains admissible.

Base configuration, Echo, Micro-BFD members, VXLAN, and Geneve use distinct
source ownership. Removing the final entry for one of these sources does not
delete sessions owned by another source. Echo sender acquisition failure rolls
back only new config claims from that pass and preserves the previous config
claims; surviving API claims retain their wire and RX/TX. SIGHUP remains
non-transactional across sources and for other runtime apply failures.
Automatic completion covers admitted base timer transactions, not arbitrary
failed source operations. Other partial applies still require a later explicit
SIGHUP to compile and publish another desired generation. A configured
non-empty VXLAN or Geneve identity whose backend cannot open is failed, or
pending for an unassigned local address, not skipped. Already accepted source
changes are not rolled back.

### Validation Rules

| Rule | Error |
|---|---|
| `log.level` must be `debug`, `info`, `warn`, or `error` (case-insensitive) | `ErrInvalidLogLevel` |
| `log.format` must be exactly `json` or `text` | `ErrInvalidLogFormat` |
| `socket.read_buffer_size` / `socket.write_buffer_size` must be >= 0 | `ErrInvalidSocketReadBufferSize` / `ErrInvalidSocketWriteBufferSize` |
| At least one of `grpc.addr` or `grpc.socket.path` must be nonempty | `ErrEmptyGRPCAddr` |
| `bfd.default_detect_multiplier` must be >= 1 | `ErrInvalidDetectMultiplier` |
| `bfd.default_desired_min_tx` must be > 0 | `ErrInvalidDesiredMinTx` |
| `bfd.default_required_min_rx` must be >= 0 | `ErrInvalidRequiredMinRx` |
| `gobgp.addr` must not be empty when enabled | `ErrEmptyGoBGPAddr` |
| Enabled `gobgp.strategy` is not recognized | `ErrInvalidGoBGPStrategy` |
| Enabled `gobgp.strategy` is recognized but not implemented (`withdraw-routes`) | `ErrUnsupportedGoBGPStrategy` |
| `gobgp.action_timeout` must be > 0 when enabled | `ErrInvalidGoBGPActionTimeout` |
| `gobgp.tls.ca_file` / `server_name` require `gobgp.tls.enabled: true` | `ErrInvalidGoBGPTLS` |
| `gobgp.dampening.suppress_threshold` must be > `reuse_threshold` > 0 | `ErrInvalidDampeningThreshold` |
| `gobgp.dampening.max_suppress_time` must be > 0 when dampening enabled | `ErrInvalidDampeningMaxSuppressTime` |
| `gobgp.dampening.half_life` must be > 0 when dampening enabled | `ErrInvalidDampeningHalfLife` |
| Session `peer` must be a valid IP address | `ErrInvalidSessionPeer` |
| Session `type` must be `single_hop` or `multi_hop` | `ErrInvalidSessionType` |
| Session `detect_mult` must be >= 1 | `ErrInvalidSessionDetectMult` |
| No duplicate session keys (peer, local, interface) | `ErrDuplicateSessionKey` |
| `interface` is not allowed on `multi_hop` | `ErrInvalidSessionMultiHopInterface` |
| `multihop_max_hops` only on `multi_hop`, range 1-254 | `ErrInvalidSessionMultihopMaxHops` |
| A link-local zone must match `interface` and the zone of the other address | `ErrInvalidSessionZone` |
| Control sessions sharing a parsed local address and listener type must use the same interface | `ErrAmbiguousListenerInterface` |
| Echo `peer` must be a valid IP address | `ErrInvalidEchoPeer` |
| Echo `detect_mult` must be >= 1 | `ErrInvalidEchoDetectMult` |
| No duplicate echo session keys | `ErrDuplicateEchoSessionKey` |
| Enabled Echo peers sharing a parsed local address must use the same interface | `ErrAmbiguousListenerInterface` |
| VXLAN `management_vni` must be <= 16777215 (24-bit) | `ErrInvalidVXLANVNI` |
| VXLAN peers require concrete IPv4 local endpoints and remote/local unicast source MACs | `ErrInvalidVXLANPeer` / `ErrVXLANEndpointIdentityUnavailable` |
| VXLAN `backend` must be `userspace-udp`; reserved non-userspace names fail closed | `ErrInvalidOverlayBackend`, `ErrUnsupportedOverlayBackend` |
| VXLAN `peer` must be a valid IP address | `ErrInvalidVXLANPeer` |
| No duplicate VXLAN session keys | `ErrDuplicateVXLANSessionKey` |
| Geneve `default_vni` and per-peer `vni` must be <= 16777215 | `ErrInvalidGeneveVNI` |
| Geneve `backend` must be `userspace-udp`; reserved non-userspace names fail closed | `ErrInvalidOverlayBackend`, `ErrUnsupportedOverlayBackend` |
| Geneve `peer` must be a valid IP address | `ErrInvalidGenevePeer` |
| Geneve Format A VAP IP/MAC tuple must be complete, IPv4, and unicast | `ErrGeneveVAPIdentityUnavailable` |
| No duplicate Geneve session keys | `ErrDuplicateGeneveSessionKey` |

Control listener identity is `(parsed local address, multi-hop flag)`;
an omitted `type` and `single_hop` select the same listener. Echo listener
identity is the parsed local address. Equivalent IPv6 spellings compare equal;
IPv4-mapped addresses retain their mapped identity. Every entry sharing a
listener must declare the same `interface`, including an empty value.
Conflicts are rejected regardless of configuration order, before opening
listeners. Disabled Echo configuration does not create listener conflicts.

These are validation identities. At runtime the listener owner keys a socket by
`(local address with an IPv4-mapped form unmapped, interface, port, multi-hop
flag)`, where the port is 3784, 4784, 3785, or 6784. Entries whose validation
identities differ only by an IPv4-mapped spelling and declare the same
interface share one socket. An API Echo session shares a listener only when
its complete runtime key matches. A different requested interface, including
an empty value, claims a distinct listener instead of reusing another device's
socket. Configuration-file interface-conflict validation remains unchanged.

`ListenerBindingConflictError` wraps `ErrAmbiguousListenerInterface`.
Its `FieldID()` identifies `sessions[].interface` or `echo.peers[].interface`;
the error contains no address or interface values.

### Defaults

| Key | Default Value | Rationale |
|---|---|---|
| `grpc.addr` | empty | No TCP listener; 50051 stays with gobgpd |
| `grpc.socket.path` | `/run/gobfd/gobfd.sock` | Management socket with peer-UID principals |
| `metrics.addr` | `127.0.0.1:9100` | Standard exporter port on loopback; the endpoint has no authentication |
| `metrics.path` | `/metrics` | Prometheus convention |
| `log.level` | `info` | Production default |
| `log.format` | `json` | Machine-parseable |
| `bfd.default_desired_min_tx` | `1s` | RFC 5880 Section 6.8.3 slow rate |
| `bfd.default_required_min_rx` | `1s` | Conservative starting point |
| `bfd.default_detect_multiplier` | `3` | 3x detection time |
| `gobgp.enabled` | `false` | Opt-in integration |
| `gobgp.strategy` | `disable-peer` | Safest BGP action |
| `gobgp.action_timeout` | `5s` | Bounds external GoBGP API calls |
| `gobgp.tls.enabled` | `false` | Backward-compatible localhost GoBGP default |
| `socket.read_buffer_size` | `4194304` (4 MiB) | Requested receive buffer; qualify effective capacity under deployment load |
| `socket.write_buffer_size` | `4194304` (4 MiB) | Requested send buffer; qualify effective capacity under deployment load |

### Related Documents

- [04-cli.md](./04-cli.md) -- CLI commands for session management
- [06-deployment.md](./06-deployment.md) -- Production deployment with systemd
- [configs/gobfd.example.yml](../../configs/gobfd.example.yml) -- Annotated example configuration

---

*Reviewed against `dev` source: 2026-10-25; baseline `93b8d19867edd3731f1a964d37c2badefd6c6a68`, with sender recovery follow-up `gobfd-qj0.8.2.2.6.7`.*
