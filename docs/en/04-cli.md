# CLI Reference

![Cobra](https://img.shields.io/badge/Cobra-CLI-1a73e8?style=for-the-badge)
![reeflective/console](https://img.shields.io/badge/reeflective%2Fconsole-Interactive-34a853?style=for-the-badge)
![ConnectRPC](https://img.shields.io/badge/ConnectRPC-gRPC-ea4335?style=for-the-badge)
![Formats](https://img.shields.io/badge/Output-table%20%7C%20json%20%7C%20yaml-ffc107?style=for-the-badge)

> Complete reference for `gobfdctl` -- the CLI client for managing BFD sessions, monitoring events, and interacting with the gobfd daemon.

---

## Table of Contents

- [Overview](#overview)
- [Global Flags](#global-flags)
- [Session Commands](#session-commands)
- [Echo Commands](#echo-commands)
- [Micro-BFD Commands](#micro-bfd-commands)
- [Monitor Command](#monitor-command)
- [Version Command](#version-command)
- [Interactive Shell](#interactive-shell)
- [Output Formats](#output-formats)

### Overview

`gobfdctl` communicates with the gobfd daemon via ConnectRPC (compatible with gRPC, Connect, and gRPC-Web protocols). It provides both non-interactive commands (via Cobra) and an interactive shell (via reeflective/console) with tab completion.

```
gobfdctl [global flags] <command> [subcommand] [flags]
```

### Global Flags

| Flag | Default | Description |
|---|---|---|
| `--addr` | `unix:///run/gobfd/gobfd.sock` | gobfd daemon endpoint: `host:port`, `http://host:port`, `https://host:port` or `unix:///path` (management socket) |
| `--tls-ca` | `""` | CA file that signed the daemon certificate (`https://` endpoints) |
| `--tls-cert` | `""` | Client certificate file for mTLS |
| `--tls-key` | `""` | Client key file for mTLS |
| `--format` | `table` | Output format: `table`, `json`, `yaml` |

### Session Commands

#### List all sessions

```bash
gobfdctl session list
```

Shows all BFD sessions with their current state, peer address, timers, and discriminators.

#### Show a specific session

```bash
# By peer address
gobfdctl session show 10.0.0.1

# By local discriminator
gobfdctl session show 42
```

A peer address that matches more than one session (for example the same
peer on two interfaces) fails with `FAILED_PRECONDITION` and lists the local
discriminators; show the session by discriminator instead.

#### Create a session

```bash
# Single-hop BFD session
gobfdctl session add \
  --peer 10.0.0.1 \
  --local 10.0.0.2 \
  --interface eth0 \
  --type single-hop \
  --tx-interval 100ms \
  --rx-interval 100ms \
  --detect-mult 3

# Multihop session
gobfdctl session add \
  --peer 192.168.1.1 \
  --local 192.168.2.1 \
  --type multi-hop \
  --tx-interval 300ms \
  --detect-mult 5

# Authenticated session
gobfdctl session add \
  --peer 10.0.0.1 \
  --local 10.0.0.2 \
  --auth-type keyed-sha1 \
  --auth-key-id 7 \
  --auth-secret api-auth-secret \
  --padded-pdu-size 128
```

| Flag | Required | Default | Description |
|---|---|---|---|
| `--peer` | Yes | -- | Remote system IP address |
| `--local` | Yes | -- | Local system IP address |
| `--interface` | No | -- | Network interface (for `SO_BINDTODEVICE`) |
| `--type` | No | `single-hop` | Session type: `single-hop` or `multi-hop` |
| `--tx-interval` | No | `1s` | Desired Min TX Interval |
| `--rx-interval` | No | `1s` | Required Min RX Interval |
| `--detect-mult` | No | `3` | Detection Multiplier |
| `--padded-pdu-size` | No | `0` | Target PDU size in bytes: 0 disables padding; 24-9000 without auth, 26-9000 with auth |
| `--auth-type` | No | `none` | Auth type: `none`, `simple-password`, `keyed-md5`, `meticulous-keyed-md5`, `keyed-sha1`, `meticulous-keyed-sha1` |
| `--auth-key-id` | If auth enabled | `0` | RFC 5880 Auth Key ID, range 0-255 |
| `--auth-secret` | If auth enabled | -- | Auth secret: 1-16 bytes for Simple Password/MD5, 1-20 bytes for SHA1 |

`AddSession` returns `InvalidArgument` for malformed protobuf TX/RX durations
and client intervals rejected by core validation. Omitted intervals default to
`1s`; an explicit required RX interval of zero remains valid.

`session show` displays `Padded PDU Target` in bytes. JSON/YAML session output,
including monitor rows, exposes integer `padded_pdu_size` as the configured
target; an authenticated PDU may be larger. CLI/API `0` disables padding,
whereas YAML session `0` inherits `bfd.default_padded_pdu_size`.

#### Delete a session

```bash
# By local discriminator
gobfdctl session delete 42
```

### Echo Commands

RFC 9747 unaffiliated BFD echo sessions live under a separate command
group because their timer model differs from RFC 5880 control sessions:
TxInterval is locally provisioned and not negotiated with the peer.

#### List echo sessions

```bash
gobfdctl echo list
```

#### Create an echo session

```bash
gobfdctl echo add \
    --peer 10.0.0.1 \
    --local 10.0.0.2 \
    --tx-interval 50ms \
    --detect-mult 3
```

| Flag | Required | Default | Description |
|---|---|---|---|
| `--peer` | Yes | -- | Echo target IP address |
| `--local` | No | -- | Local IP address (fallback to default route) |
| `--interface` | No | -- | Outbound interface for `SO_BINDTODEVICE` |
| `--tx-interval` | No | `1s` | Echo transmit interval (RFC 9747 Section 3.3) |
| `--detect-mult` | No | `3` | Detection multiplier; `DetectionTime = mult * TxInterval` |

`AddEchoSession` requires a structurally valid, positive `tx_interval` within
the BFD wire interval bound; violations return `InvalidArgument`. The CLI
supplies its `1s` default, but direct API requests must include the field.

#### Delete an echo session

```bash
gobfdctl echo delete 42
```

### Micro-BFD Commands

RFC 7130 micro-BFD uses one BFD session per LAG member link. The CLI manages
the group resource; member-session creation is a separate operation.

#### List micro-BFD groups

```bash
gobfdctl micro list
```

#### Create a micro-BFD group

```bash
gobfdctl micro add \
    --lag bond0 \
    --members eth0,eth1,eth2 \
    --peer 10.0.0.1 \
    --local 10.0.0.2 \
    --tx-interval 300ms \
    --rx-interval 300ms \
    --detect-mult 3 \
    --min-active 2
```

| Flag | Required | Default | Description |
|---|---|---|---|
| `--lag` | Yes | -- | LAG interface name (`bond0`, `team0`, `port-channel1`) |
| `--members` | Yes | -- | Comma-separated member link names |
| `--peer` | Yes | -- | Peer IP address shared by every member session |
| `--local` | No | -- | Local IP address |
| `--tx-interval` | No | `1s` | Desired minimum TX interval |
| `--rx-interval` | No | `1s` | Required minimum RX interval |
| `--detect-mult` | No | `3` | Detection multiplier (RFC 7130 Section 2.2) |
| `--min-active` | No | `1` | Minimum active members for aggregate Up |

The CLI rejects `--min-active` outside `[1, len(--members)]` before
contacting the daemon.

`AddMicroBFDGroup` returns `InvalidArgument` for malformed protobuf TX/RX
durations, duplicate or empty member names, and member session values outside
the RFC 5880 bounds (zero or out-of-range intervals, zero detect multiplier).
Omitted intervals default to `1s`. Micro-BFD group RPCs return:

| Condition | Code |
|---|---|
| The caller already holds a claim on the group | `AlreadyExists` |
| A group with the same `lag_interface` and a different configuration exists | `FailedPrecondition` |
| A member link belongs to a group of another LAG | `FailedPrecondition` |
| `DeleteMicroBFDGroup` for a group the caller does not own, such as a reload group | `FailedPrecondition` |
| `DeleteMicroBFDGroup` for an unknown LAG | `NotFound` |

#### Delete a micro-BFD group

```bash
gobfdctl micro delete bond0
```

### Monitor Command

Stream live BFD events (state changes) from the daemon:

```bash
# Stream only new events
gobfdctl monitor

# Stream events with initial snapshot of all sessions
gobfdctl monitor --current
```

| Flag | Default | Description |
|---|---|---|
| `--current` | `false` | Include current session states before streaming |

The monitor uses gRPC server-side streaming. Each event includes:
- Timestamp
- Session identifier (peer address, local discriminator)
- Old state and new state
- Diagnostic code
- Event trigger
- Revision (`rev=`), increasing within one daemon process

A snapshot (`--current`) is a run of `SessionAdded` events closed by
`SyncComplete`, which carries the revision the snapshot covers. If the
monitor falls behind the daemon, it receives `Gap` followed by a fresh
snapshot, with or without `--current`. Releasing the last owner of a wire
session emits `SessionDeleted` with its identity, final state and revision;
releasing a shared claim emits no deletion. Later snapshots omit the removed
session. Removal cancels its pending GoBGP Down and Up actions; it does not
enable a peer GoBFD previously disabled.

New Control and Echo sessions emit `SessionAdded` even if they remain Down.
Additions, changes and removals carry full API status sampled at publication;
List samples telemetry independently. `ObservationVersion` covers the causal
tuple, not each packet or counter change. Clients replace the entire row,
including zero/false values and absent timestamps. Initial, GAP and reconnect
snapshots include both registries at one revision boundary. Echo rows map
their locally provisioned TX interval, detection settings, counters and
timestamps; unowned remote/negotiated/drop/auth fields are zero, not loss
evidence. Control List/Get and typed Echo commands retain their existing scope.
Public Control/Echo raw notification loss also produces `Gap` and a fresh
snapshot while dispatch runs, even if this monitor's own queue has room.
No next owner edge or reconnect is needed. Shared Watch clients reject older
queued observations after a newer snapshot; the CLI prints the revisioned
event stream. Private cleanup and legacy subscribers retain their separate
delivery boundaries.

Press `Ctrl+C` to stop monitoring.

### Version Command

All binaries support version display with commit hash and build date:

```bash
gobfd --version
gobfdctl version
gobfd-haproxy-agent --version
gobfd-exabgp-bridge --version
```

Example output:

```
gobfdctl v0.1.0
  commit:  abc1234
  built:   2026-02-22T12:00:00Z
```

Version information is injected at build time via ldflags (see [09-development.md](./09-development.md)).

### Interactive Shell

```bash
gobfdctl shell
```

Launches an interactive shell with:
- **Tab completion** for commands and subcommands
- **Readline autocomplete** via reeflective/console (derived from Cobra command tree)
- Full access to all gobfdctl commands without the `gobfdctl` prefix

Example session:

```
gobfd> session list
gobfd> session show 10.0.0.1
gobfd> monitor --current
gobfd> exit
```

### Output Formats

All commands support three output formats via the `--format` flag:

| Format | Use Case |
|---|---|
| `table` | Human-readable terminal output (default) |
| `json` | Machine-parseable, scripting, piping to `jq` |
| `yaml` | Human-readable structured output |

JSON field names and values form the machine-readable CLI contract. YAML keeps
the same data model, but whitespace, indentation, and scalar presentation are
not byte-stable across compatible serializer updates; automation should parse
YAML rather than compare its rendered bytes.

Control session detail reports `Packets Dropped` and `Auth Failures` from the
same sampled session counters as Get/List and Control Watch. Auth failures are
a subset of drops; admitted packets can still fail owner validation or be
abandoned on stop. Process-lifetime stage totals, including unknown peers and
deleted sessions, are available through API `ListSessions.delivery_counters`
and `gobfd_bfd_delivery_outcomes_total`, not the session table. The fixed rows
also distinguish Echo RX and raw/dispatch/recipient notification outcomes;
notification loss is not a Control packet drop. See
[monitoring](07-monitoring.md) for denominators and remaining queue boundaries.

```bash
# JSON output for scripting
gobfdctl session list --format json

# YAML output
gobfdctl session list --format yaml

# Pipe JSON to jq
gobfdctl session list --format json | jq '.sessions[].state'
```

### Examples

```bash
# Connect to remote daemon
gobfdctl --addr 10.0.0.1:50052 session list

# Quick session setup for testing
gobfdctl session add --peer 10.0.0.1 --local 10.0.0.2 --interface eth0 \
  --tx-interval 100ms --detect-mult 3

# Watch for state changes in JSON
gobfdctl --format json monitor --current

# Scripted health check
if gobfdctl session show 10.0.0.1 --format json | jq -e '.state == "Up"' > /dev/null; then
  echo "BFD session is Up"
fi
```

### Related Documents

- [03-configuration.md](./03-configuration.md) -- Declarative session configuration
- [07-monitoring.md](./07-monitoring.md) -- Prometheus metrics and Grafana dashboards
- [01-architecture.md](./01-architecture.md) -- ConnectRPC server architecture

---

*Last updated: 2026-02-21*
