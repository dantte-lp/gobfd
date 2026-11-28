# Command-line interface

[Russian translation](../ru/04-cli.md).

`gobfdctl` uses the daemon management API. Commands, flags and formatting are defined in [command sources](../../cmd/gobfdctl/commands/root.go); message fields are defined in [the protocol schema](../../api/bfd/v1/bfd.proto). List success proves management access, not forwarding health.

The command-line interface (CLI) calls the application programming interface (API). TLS certificate options use a certificate authority (CA) and PEM-encoded certificate/key files. TX and RX denote transmit and receive intervals; PDU means protocol data unit. A link aggregation group (LAG) contains member links.

## Global flags

| Flag | Default | Contract |
|:---|:---|:---|
| `--addr` | `unix:///run/gobfd/gobfd.sock` | Unix URL, bare `host:port`, `http://` or `https://` |
| `--tls-ca` | empty | PEM CA for daemon certificate |
| `--tls-cert` | empty | PEM client certificate |
| `--tls-key` | empty | PEM client private key |
| `--format` | `table` | `table`, `json`, `yaml` for structured output |

TLS files require HTTPS. Server-side transport policy controls writes. The CLI does not read daemon `GOBFD_*` overrides. Source: [endpoint transport](../../internal/apiclient/endpoint.go).

The commands below are unexecuted examples. Select one admitted daemon endpoint and readable client credentials before use. Read-only list/capability requests may be repeated; stop on transport or authorization errors before attempting mutation.

```sh
gobfdctl session list
gobfdctl --format json capabilities
gobfdctl --addr https://bfd.example.net:50052 --tls-ca /etc/gobfd/tls/ca.pem --tls-cert /etc/gobfd/tls/client.pem --tls-key /etc/gobfd/tls/client.key session list
```

## Commands

| Command | Arguments/options | Result |
|:---|:---|:---|
| `session list` | none | All Control sessions |
| `session show IDENTIFIER` | Peer address or uint32 discriminator | One Control session |
| `session add` | Flags below | Claim Control session |
| `session delete DISCRIMINATOR` | uint32 | Release caller's Control claim |
| `echo list` | none | All Echo sessions |
| `echo add` | Flags below | Claim preview Echo session |
| `echo delete DISCRIMINATOR` | uint32 | Release caller's Echo claim |
| `micro list` | none | Micro-BFD groups |
| `micro add` | Flags below | Claim preview LAG group |
| `micro delete LAG` | LAG interface | Release caller's group claim |
| `capabilities` | none | Daemon preview catalog |
| `monitor` | `--current` defaults false | Stream Control and Echo events |
| `certificate renew` | Four required flags below | Renew certificate independently of daemon RPC |
| `version` | none | Local CLI build information |
| `shell` | none | Interactive subset |
| `help` | Optional command path | Usage |

Prefer a discriminator when several sessions share one address. Delete releases a claim; other owners may retain the physical session. Configuration claims do not belong to the CLI principal. See [authorization](15-security.md#management-api).

## Control creation

| Flag | Default | Contract |
|:---|:---|:---|
| `--peer` | required | Peer IP |
| `--local`, `--interface` | empty | Source IP and device binding |
| `--type` | `single-hop` | `single-hop` or `multi-hop` |
| `--tx-interval`, `--rx-interval` | `1s` each | Duration strings; RX may be zero |
| `--detect-mult` | `3` | 1–255 after server validation |
| `--padded-pdu-size` | `0` | Target bytes; zero disables request padding |
| `--auth-type` | `none` | `none`, `simple-password`, `keyed-md5`, `meticulous-keyed-md5`, `keyed-sha1`, `meticulous-keyed-sha1` |
| `--auth-key-id` | `0` | 0–255 |
| `--auth-secret` | empty | Required with enabled authentication |

Auth normalization accepts case differences and underscores/hyphens. Key material with disabled auth is rejected. No `--multihop-max-hops` flag or `AddSessionRequest` hop-policy field exists; use YAML for a nondefault hop policy. Server validation, binding admission and authorization still apply.

For this Linux example, use an authorized mutation principal, an owned local address/interface and a configured peer with matching parameters. Record the initial session inventory and confirm that the intended claim is absent. Replace the documentation endpoints before execution.

```sh
gobfdctl session add --peer 192.0.2.2 --local 192.0.2.1 --interface eth0 --tx-interval 300ms --rx-interval 300ms --detect-mult 3
gobfdctl session show 192.0.2.2
gobfdctl monitor --current
```

Require Add to return the intended session and discriminator; verify the exact tuple and expected local/remote state through show/monitor and a peer readback. Stop on conflicting parameters or unexpected state. A repeated Control Add by the same principal returns a duplicate error; read back before retrying. For recovery, release only the claim created by this procedure with `session delete DISCRIMINATOR` under the same principal, then verify the initial inventory and peer behavior. Other owners can retain the physical wire. Sources: [claim admission and release](../../internal/bfd/manager_session.go), [API ownership](../../internal/server/server.go).

Runtime claims are not persisted to YAML. Auth flag values may appear in process arguments/history; use protected YAML or a controlled API client for production secrets. Source: [Control commands](../../cmd/gobfdctl/commands/session.go).

## Echo and Micro-BFD creation

| Command | Flags and defaults | Constraint |
|:---|:---|:---|
| `echo add` | `--peer` required; `--local`/`--interface` empty; `--tx-interval=1s`, `--detect-mult=3` | Enabled preview; positive TX; multiplier 1–255 |
| `micro add` | `--lag`/`--members`/`--peer` required; `--local` empty; `--tx-interval=1s`, `--rx-interval=1s`, `--detect-mult=3`, `--min-active=1` | Enabled preview; minimum active 1…member count |

`--members` accepts comma-separated interfaces. Echo/Micro creation prints status lines; `--format` applies to their list output. Creation does not enable a profile or configure its actuator. Sources: [Echo](../../cmd/gobfdctl/commands/echo.go), [Micro-BFD](../../cmd/gobfdctl/commands/micro.go).

Before mutation, verify preview enablement, the owned tuple/member set and the permitted actuator policy. Stop if ownership or effective parameters conflict. An identical Echo Add by the same principal is idempotent while admission remains ready; a repeated Micro-BFD group claim returns a duplicate error. Inspect the corresponding list before retrying, and release only newly created claims with the matching delete command and principal. Verify surviving claims and the recorded member state; releasing a group is not proof of dataplane restoration. Sources: [Echo claims](../../internal/bfd/manager_echo.go), [group claims](../../internal/bfd/manager_micro.go).

## Capabilities and events

`capabilities` reports kind, RFC, maturity, enablement and local readiness. The read-only daemon catalog remains available when forwarding runtime is unready, subject to transport policy. No client fallback invents results. Disabled profiles are `not_ready`; unavailable runtime information is `unknown`. Enabled overlays without peers have no owned backend. API-only Echo may need an admitted claim before its receive listener exists. Readiness is not peer health or RFC qualification, and the catalog is not admission preflight.

`monitor --current` emits Control and Echo snapshots followed by events; `session list/show` remain Control-only and `echo list` reads Echo separately. JSON/YAML output is a sequence of events, not a session database. Controllers must reconcile after reconnect; see the [API source](../../api/bfd/v1/bfd.proto) and [combined Watch snapshot](../../internal/bfd/manager_session.go).

## Certificate renewal

`certificate renew` contacts an external CA using an existing identity, validates the returned certificate and atomically replaces only the certificate. Enrollment, key rotation and scheduling are outside its scope.

File renewal requires Linux. The [platform guard](../../internal/stepca/files_other.go) returns `errors.ErrUnsupported` on other platforms before reading credentials or contacting the CA.

On Linux, use an account that can read the separate certificate, key and trust-root files and replace the certificate in its trusted parent directory. Preserve a protected accepted certificate and an independent management session. Verify the CA origin and current credential identity before running this unexecuted example; stop if the files, ownership or trust roots differ from the accepted inputs.

```sh
gobfdctl certificate renew --ca-url https://ca.example.net --ca-root /etc/gobfd/tls/roots.pem --cert /etc/gobfd/tls/client.pem --key /etc/gobfd/tls/client.key
```

All four flags are mandatory. Output is `certificate renewed` or `certificate unchanged`. A post-replacement error may represent a committed operation; inspect the certificate before retrying. Renewal/revocation is not immediate API-access removal. Sources: [command](../../cmd/gobfdctl/commands/certificate.go), [renewal client](../../internal/stepca/client.go), [ADR-0005](adr/0005-smallstep-management-certificates.md).

Read back the certificate identity, key agreement, chain and expiry, then verify a new authorized management connection. Renewal may issue another certificate on repetition; it is not an unconditional no-op. On failure, stop automated retries and inspect the installed certificate first. Restore the protected prior certificate only if it remains valid and accepted, preserve the key and trust roots, and verify a new connection through the independent recovery session. File and replacement checks are defined in [credential handling](../../internal/stepca/files_linux.go) and [response validation](../../internal/stepca/certificate.go).

## Interactive shell and exit behavior

The shell supports `session`, `capabilities`, `monitor`, `version`, `help`, `exit`/`quit`. Echo, Micro-BFD and certificate commands are noninteractive only. Select endpoint/format before entering the shell. Completion follows the shell command tree; Ctrl-D exits. Source: [shell](../../cmd/gobfdctl/commands/shell.go).

Failures print `Error: …` to stderr and exit 1. Local version output does not report the daemon build or session readiness.
