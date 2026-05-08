# ADR-0002: Native OVSDB client for the OVS LAG backend

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

RFC 7130 Micro-BFD enforcement on Open vSwitch bonded ports requires
adding and removing member interfaces from a bond. The first GoBFD
implementation called `ovs-vsctl` from the daemon. `ovs-vsctl` is a
schema-aware CLI client that connects to the OVSDB server and changes
the `ovs-vswitchd` configuration database. It is not the primitive API.

Open vSwitch exposes OVSDB, a JSON-RPC management protocol specified by
RFC 7047 and implemented by `ovsdb-server`. A native OVSDB client avoids
process spawning, shell-injection review exceptions, stdout/stderr
parsing, and PATH dependency.

## Decision

GoBFD uses a native OVSDB client for the default OVS LAG backend.
`ovs-vsctl` is retained only as an explicit fallback or diagnostics path.
The owner-policy for OVS-managed devices must remain explicit until the
backend can prove ownership through OVSDB or a higher-level network
manager.

The production adapter uses `github.com/ovn-org/libovsdb`. The GoBFD
core depends on a narrow local interface so tests do not need a live
OVSDB server.

## Consequences

### Required OVSDB operations

The CLI backend mapped RFC 7130 transitions to these operations:

| Micro-BFD transition | CLI operation | OVSDB intent |
|---|---|---|
| `Up -> non-Up` | `ovs-vsctl --if-exists del-bond-iface <bond> <iface>` | Remove Interface UUID from `Port.interfaces` |
| `non-Up -> Up` | `ovs-vsctl --may-exist add-bond-iface <bond> <iface>` | Insert Interface UUID into `Port.interfaces`; create Interface if needed |

The native backend works against the `Open_vSwitch` schema:

- `Port` table: locate row by `name == lagInterface`.
- `Interface` table: locate or create row by `name == memberInterface`.
- `Port.interfaces`: mutate the set of Interface UUID references.
- Transaction result: check all operation results before reporting success.

### Backend interfaces

```go
type OVSDBLAGBackend struct {
    client OVSDBClient
}

type OVSDBLAGClient interface {
    RemoveBondInterface(ctx context.Context, bond, iface string) error
    AddBondInterface(ctx context.Context, bond, iface string) error
}
```

The backend reuses the validation and RFC 7130 policy already used by
the kernel-bond and CLI-backed OVS paths.

### Configuration and ownership

- The default OVSDB endpoint is `unix:/var/run/openvswitch/db.sock`.
- Distro-specific paths use `micro_bfd.actuator.ovsdb_endpoint`.
- NetworkManager-managed devices use a separate D-Bus backend, because
  NetworkManager owns connection profiles and exposes device state
  through D-Bus. The two backends must not be conflated.

### Risk

- Adding `libovsdb` increases the dependency surface and must pass
  `make verify`, vulnerability audit, and module review before any
  release that ships it as the default.
- `ovs-vsctl` remains in the codebase as a fallback type, not as the
  default backend selected by configuration.

## References

- RFC 7047: <https://datatracker.ietf.org/doc/html/rfc7047>
- Open vSwitch `ovsdb(7)`:
  <https://docs.openvswitch.org/en/stable/ref/ovsdb.7/>
- Open vSwitch `ovs-vsctl(8)`:
  <https://www.openvswitch.org/support/dist-docs/ovs-vsctl.8.html>
- `github.com/ovn-org/libovsdb`:
  <https://pkg.go.dev/github.com/ovn-org/libovsdb>
