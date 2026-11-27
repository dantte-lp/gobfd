# ADR-0013: Speaker-epoch disable ownership across GoBFD restarts

[Russian translation](../../ru/adr/0013-gobgp-restart-disable-ownership.md).

| Field | Value |
|:---|:---|
| Status | Accepted policy; candidate qualification separate |
| Date | 2026-11-27 |

## Context

Under the initial GoBGP v4 actuation contract, GoBFD recorded disabled neighbors only in process memory (`Handler.disabled`). When GoBFD disabled a managed neighbor on BFD Down and subsequently restarted (for example after `SIGKILL` or a service restart) while local `gobgpd` remained running, BFD recovered to Up with a new local discriminator, whereas the GoBGP neighbor stayed permanently `AdminDown/Idle` until manual intervention ([issue #131](https://github.com/dantte-lp/gobfd/issues/131)).

At the same time, configuration classification treated `unix:///path` GoBGP API endpoints as non-loopback hosts and emitted a false plaintext remote-TCP warning ([issue #132](https://github.com/dantte-lp/gobfd/issues/132)).

Inspection of GoBGP v4.10.0 (`pkg/config/oc/util.go`, `pkg/server/server.go`, `pkg/server/fsm.go`) establishes two protocol-boundary constraints:

1. `ListPeer` exposes static `PeerConf.AdminDown` and operational `PeerState.AdminState`, while omitting `DisablePeerRequest.Communication` and `DisconnectReason`.
2. Entering `adminStateDown` zeroes `Timers.State` (`Uptime`, `Downtime`), `Messages`, `Flops` and `EstablishedCount` in `BgpServer`, so a redundant `DisablePeer` issued on an already-down neighbor while GoBFD is offline leaves no distinct marker in `ListPeer`.

## Decision

### 1. Local filesystem Unix IPC classification

Classify `gobgp.addr` values using `unix:///abs/path` or `unix:/abs/path` (clean absolute path, empty authority, no query, fragment, NUL byte or `@` abstract-namespace prefix) as local filesystem inter-process communication (IPC). Suppress the non-loopback plaintext warning for those endpoints while keeping the warning for plaintext remote TCP, `unix-abstract:` targets and malformed targets. Sources: [endpoint parser](../../../internal/gobgp/endpoint/endpoint.go), [configuration classifier](../../../internal/config/config.go).

### 2. Confirmed speaker-epoch ownership receipts

Persist a durable disable ownership receipt in `gobgp.state_file` (default `/var/lib/gobfd/gobgp-ownership.json`, override via `GOBFD_GOBGP_STATE_FILE`; empty `gobgp.state_file` selects memory-only tracking) only after `DisablePeer` succeeds against a provable local Unix GoBGP speaker:

| Field | Source | Verification rule |
|:---|:---|:---|
| `target` | Canonical `gobgp.addr` | Exact match with active client target |
| `peer_pid`, `peer_uid` | Kernel `SO_PEERCRED` on Unix socket | Positive PID and matching UID on active Unix transport |
| `socket_dev`, `socket_ino` | Connected socket `stat` metadata | Matching device and inode when available |
| `boot_id`, `proc_start_time` | `/proc/sys/kernel/random/boot_id`, `/proc/<pid>/stat` | Exact match when readable; mismatch fails closed |
| `asn`, `router_id` | `GetBgp` response | Exact match with live speaker global configuration |

Writes use `os.OpenRoot` on a trusted private parent directory (mode without group/other write bits, owned by `root` or the effective UID), write a `0600` temporary file, `fsync` the file, atomically rename it into place and `fsync` the directory. Existing state files that are symlinks, group/world-writable, owned by another user or malformed fail closed at startup. Sources: [ownership store](../../../internal/gobgp/ownership_store.go), [Unix peer identity](../../../internal/gobgp/client.go), [handler](../../../internal/gobgp/handler.go).

### 3. Fail-closed recovery and revocation

1. Before `DisablePeer`, GoBFD inspects `ListPeer`: if `State.AdminState == ADMIN_STATE_DOWN` or `Conf.AdminDown == true`, GoBFD takes no disable action and records no receipt.
2. An abrupt crash (`SIGKILL`) before `DisablePeer` returns leaves no confirmed receipt; after restart the peer is treated as ambiguously owned and remains administratively disabled.
3. TCP GoBGP endpoints carry no kernel `SO_PEERCRED` speaker-process proof; across a GoBFD restart their receipts are not persisted, so TCP-controlled neighbors remain administratively disabled after a crash during a fault.
4. While connected, GoBFD subscribes to `WatchEvent` peer updates: if a peer with a persisted receipt is observed in `ADMIN_STATE_UP` outside a GoBFD-initiated `EnablePeer`, GoBFD immediately revokes the receipt so any later operator disable is preserved.
5. On BFD Up (or initial snapshot convergence when BFD is already Up at handler startup), GoBFD enables a peer outside current-process memory only when a valid persisted receipt exists, live speaker identity matches every recorded field, `Conf.AdminDown == false`, and `State.AdminState == ADMIN_STATE_DOWN`. Stale or mismatched receipts are discarded without calling `EnablePeer`.

## Alternatives

| Alternative | Reason not selected |
|:---|:---|
| Keep in-memory-only ownership (`Handler.disabled`) | Leaves GoBFD-owned disables permanently `AdminDown` after a controller crash or restart ([issue #131](https://github.com/dantte-lp/gobfd/issues/131)) |
| Persist peer addresses without speaker-epoch identity | Unsafe: enables a neighbor across a `gobgpd` restart or configuration replacement where an operator disabled the peer |
| Infer GoBFD ownership from `DisablePeerRequest.Communication` in `ListPeer` | Unavailable in GoBGP v4.10.0: `NewPeerFromConfigStruct` omits shutdown communication and `adminStateDown` zeroes peer timers and counters |
| Store receipts in `/run/gobfd` (`RuntimeDirectory=gobfd`) | Systemd recreates `RuntimeDirectory=` on `systemctl restart gobfd`, losing receipts across planned restarts |

## Consequences

- `gobfd.service` adds `StateDirectory=gobfd` and `StateDirectoryMode=0700`; container images and `tmpfiles.d` provision `/var/lib/gobfd` with mode `0700` owned by `gobfd:gobfd`.
- Controller restarts over Unix IPC recover provably GoBFD-owned disables automatically once live BFD reaches Up, while preserving pre-existing operator disables, static `Conf.AdminDown = true` configuration, `gobgpd` restarts and TCP ambiguity as administratively down.
- Because GoBGP v4.10.0 zeroes peer timers/counters on `adminStateDown` and omits `Communication` from `ListPeer`, an operator who wants an offline disable while `gobfd` is stopped on the same `gobgpd` PID to survive GoBFD's persisted receipt MUST set `Conf.AdminDown = true` (`UpdatePeer` or `gobgp.toml`) or apply the disable while `gobfd` is running.

## References

- [Production contract](../reference/production-contract.md#gobgp-v4-actuator).
- [Configuration reference](../03-configuration.md#gobgp-integration).
- [Deployment hardening](../06-deployment.md#systemd).
- [Operational runbook](../16-production-runbooks.md#22-startup-owned-changes).
