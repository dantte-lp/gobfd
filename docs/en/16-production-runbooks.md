# Runbook: configuration reload (Linux systemd)

[Russian translation](../ru/16-production-runbooks.md).

This source-derived procedure replaces declarative configuration on one owned Linux host using the shipped `gobfd.service`. No production execution or elapsed-time receipt is recorded. Prerequisites: installed accepted binaries/unit, root shell, existing healthy service, protected candidate `/etc/gobfd/gobfd.candidate.yml` and independent access for recovery.

## 0. Rules without exceptions

1. Admit one host and an explicit peer/consumer set. Preserve independent console/access before routing or authentication changes.
2. Record exact binary/source identities and accepted configuration before apply. Keep secrets out of logs and shared artifacts.
3. Classify every changed field against [the startup contract](03-configuration.md#hot-reload-sighup). Authentication on retained sessions and startup-owned fields require a maintenance restart.
4. Retain the accepted binary/package for full rollback; this procedure changes configuration only.
5. Stop on candidate rejection, pending reconciliation, peer-state divergence or consumer forwarding failure. A successful signal command does not prove acceptance.

## 1. Pre-flight checks

Run in one root shell; subsequent blocks reuse `backup_dir`.

```sh
backup_dir=$(mktemp -d /var/backups/gobfd.XXXXXXXX)
chmod 0700 "$backup_dir"
install -m 0600 /etc/gobfd/gobfd.yml "$backup_dir/gobfd.yml"
cmp /etc/gobfd/gobfd.yml "$backup_dir/gobfd.yml"
gobfd -version
gobfdctl version
systemctl show gobfd -p ActiveState -p SubState -p MainPID -p NRestarts
systemctl cat gobfd
stat -c '%a %U %G %n' /run/gobfd/gobfd.sock
gobfdctl --format json session list > "$backup_dir/sessions-before.json"
gobfdctl --format json capabilities > "$backup_dir/capabilities-before.json"
journalctl -u gobfd --since '-2 min' --no-pager
test -s /etc/gobfd/gobfd.candidate.yml
test -s "$backup_dir/gobfd.yml"
printf '%s\n' "$backup_dir"
```

Continue only when backup comparison succeeds, service is active/running, expected sessions/consumers are healthy, socket permissions match policy, and the candidate belongs to this change. The daemon has no `-check` flag. Review full YAML/default/environment effects before apply; compare secrets only inside protected processes. Persist the displayed backup path in the restricted change record.

A repeated pre-flight creates another backup directory and reassigns `backup_dir`; record the accepted original path before repeating it. Do not overwrite the original before/after evidence with another attempt. Repeating a restart interrupts sessions again and discards in-memory/API ownership; repeating reload does not restore earlier intent. After any failed apply, use the recorded backup and section 4, then repeat section 3. The [reload policy](adr/0006-session-preserving-reload-policy.md) defines supported reapplication and its limits.

## 2. Install and reload

### 2.1 Reloadable configuration

```sh
install -o root -g gobfd -m 0640 /etc/gobfd/gobfd.candidate.yml /etc/gobfd/gobfd.yml && \
  cmp /etc/gobfd/gobfd.candidate.yml /etc/gobfd/gobfd.yml && \
  systemctl reload gobfd
journalctl -u gobfd --since '-2 min' --no-pager
gobfdctl --format json session list > "$backup_dir/sessions-after.json"
```

Continue only when the installed file matches, logs report converged reconciliation without rejected/pending resources, and affected sessions reach their intended state. A retained compatible timer update preserves its discriminator; removed configuration claims may leave API-owned sessions.

### 2.2 Startup-owned changes

Use an admitted maintenance interval with expected session interruption. Complete section 1 first. When GoBGP integration is enabled, retain peer administrative states and the journal evidence of which peers GoBFD disabled. That ownership map is memory-only and is lost on restart. Do not restart during a failure without a documented peer recovery decision.

```sh
gobgp neighbor > "$backup_dir/gobgp-before.txt"
journalctl -u gobfd --since '-1 hour' --no-pager > "$backup_dir/daemon-before.log"
gobgp neighbor 192.0.2.2
```

The peer address is the example configuration's peer; substitute the admitted neighbor. An administrative Down state alone does not prove GoBFD ownership. Preserve independent operator intent before enabling any peer.

```sh
install -o root -g gobfd -m 0640 /etc/gobfd/gobfd.candidate.yml /etc/gobfd/gobfd.yml && \
  cmp /etc/gobfd/gobfd.candidate.yml /etc/gobfd/gobfd.yml && \
  systemctl restart gobfd
systemctl show gobfd -p ActiveState -p SubState -p MainPID -p NRestarts
journalctl -u gobfd --since '-2 min' --no-pager
gobfdctl session list
```

Continue only when the new process runs, reconciliation converges and each affected peer/consumer recovers. `daemon-reload` is required after changing unit/drop-in files; it does not reload daemon YAML or restart a process. Sources: [shipped ExecReload](../../deployments/systemd/gobfd.service), [systemctl manual](https://www.freedesktop.org/software/systemd/man/latest/systemctl.html).

If an admitted peer remains administratively disabled after BFD returns Up, require proof that GoBFD disabled it and no later operator action requires it to remain disabled. Only then explicitly recover that peer:

```sh
gobgp neighbor 192.0.2.2 enable
gobgp neighbor 192.0.2.2
gobgp global rib
```

Require Established, intended routes and the independent forwarding probe. Never bulk-enable all disabled neighbors. Sources: [in-memory disable ownership](../../internal/gobgp/handler.go), [official GoBGP CLI](https://github.com/osrg/gobgp/blob/master/docs/sources/cli-operations.md).

## 3. Post-check

```sh
gobfdctl --format json session list
gobfdctl --format json capabilities
systemctl show gobfd -p ActiveState -p SubState -p MainPID -p NRestarts
journalctl -u gobfd --since '-2 min' --no-pager
```

Compare the protected before/after inventories, including peer/local/interface identity, discriminator, state, diagnostics and negotiated timers. Verify management writer/read-only policy and intended metrics reachability. Measure consumer-specific route/backend behavior and an independent forwarding/application probe; management access alone does not satisfy acceptance.

| Gate | Required result | Evidence |
|:---|:---|:---|
| Configuration | Accepted intended generation, no pending resources | Restricted journal excerpt |
| Retained wire | Expected identity/discriminator and negotiated state | Before/after JSON |
| Consumer | Intended route/backend action and recovery | Consumer readback |
| Forwarding | Declared probe succeeds on intended path | Timestamped external probe |
| Resources | No unexpected restart/OOM; within admitted budget | Service/cgroup/host measurement |

For an owned disposable FRR topology, the separate BGP gate is:

```sh
make int-bgp-failover-testcontainers
```

It asserts route withdrawal within its 20-second test bound and restoration; it does not prove a 900 ms bound or production forwarding. The observability counterpart is `make int-observability-testcontainers`; it requires scraping, alert firing and recovery. Do not run overlapping fixed-name manual labs concurrently. See [integration qualification](11-integrations.md).

## 4. Rollback

Restore the backup in the same shell; substitute the recorded protected backup path if the shell was lost.

```sh
test -s "$backup_dir/gobfd.yml" && \
  install -o root -g gobfd -m 0640 "$backup_dir/gobfd.yml" /etc/gobfd/gobfd.yml && \
  cmp "$backup_dir/gobfd.yml" /etc/gobfd/gobfd.yml && \
  systemctl reload gobfd
journalctl -u gobfd --since '-2 min' --no-pager
gobfdctl session list
```

For startup-owned rollback, replace reload with `systemctl restart gobfd` and repeat section 3. For a binary/package upgrade, restore the exact previously accepted artifact through its packaging procedure before restart.

Rollback restores desired configuration, not lost session uptime, old discriminator after recreation, transient route withdrawals or external consumer history. API claims may need owner-specific reprovisioning after restart. Preserve evidence and the backup until acceptance closes; cleanup is a separate approved operation.

## 5. Traps that cost time

This procedure is not executed against a production host. The following are source-defined failure checks, not invented incident observations.

| Symptom | Cause | Check/fix |
|:---|:---|:---|
| Reload command succeeds, settings stay old | Rejected candidate/startup-field change | Read rejection fields; restore or schedule restart |
| Session absent after acceptance | Pending address/interface/listener resource | Read source-specific receipt; restore binding and require convergence |
| CLI cannot mutate over TCP | Anonymous/read-only principal | Use permitted socket/mTLS identity |
| BGP does not act | Ambiguous base sessions/native-owner conflict/API incompatibility | Require one owner and compatible GoBGP endpoint |
| Session removal leaves a wire | Another principal/config claim remains | Release only the intended owner's claim |

## 6. Command cheat-sheet

```sh
# Read current session state.
gobfdctl session list
# Read preview maturity and local readiness.
gobfdctl capabilities
# Request reload of reloadable YAML.
systemctl reload gobfd
# Inspect daemon acceptance/rejection.
journalctl -u gobfd --since '-2 min' --no-pager
# Read service process state.
systemctl show gobfd -p ActiveState -p MainPID -p NRestarts
```

## Links

- [Configuration](03-configuration.md): complete defaults, field inventory and restart boundary.
- [Deployment](06-deployment.md): artifacts, service sandbox and Kubernetes prerequisites.
- [Monitoring](07-monitoring.md): signal denominators and alerts.
- [Security](15-security.md): identity, secrets and actuator permissions.
- [BGP example](../../deployments/integrations/bgp-fast-failover/README.md): exact disposable topology and gate.
- [Official vendor references](reference/vendor-bfd.md): Cisco, Arista, Juniper and Huawei platform boundaries.
