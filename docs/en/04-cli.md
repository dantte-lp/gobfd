# CLI Reference

![Cobra](https://img.shields.io/badge/Cobra-CLI-1a73e8?style=for-the-badge)
![reeflective/console](https://img.shields.io/badge/reeflective%2Fconsole-Interactive-34a853?style=for-the-badge)
![ConnectRPC](https://img.shields.io/badge/ConnectRPC-gRPC-ea4335?style=for-the-badge)
![Formats](https://img.shields.io/badge/Output-table%20%7C%20json%20%7C%20yaml-ffc107?style=for-the-badge)

> Complete reference for `gobfdctl` -- the CLI client for managing BFD sessions, monitoring events, and interacting with the gobfd daemon.

---

### Table of Contents

- [Overview](#overview)
- [Global Flags](#global-flags)
- [Session Commands](#session-commands)
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
| `--addr` | `localhost:50051` | gobfd daemon address (host:port) |
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

#### Delete a session

```bash
# By local discriminator
gobfdctl session delete 42
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
gobfdctl --addr 10.0.0.1:50051 session list

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
