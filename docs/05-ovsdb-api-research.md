# OVSDB API Research for Micro-BFD OVS Backend

Scope: answer whether GoBFD should call `ovs-vsctl` or use a native OVS API for
RFC 7130 Micro-BFD member enforcement on OVS bonded ports.

## Conclusion

Open vSwitch has a built-in management API: OVSDB. OVSDB is a JSON-RPC protocol
specified by RFC 7047 and implemented by `ovsdb-server`. The `ovs-vsctl` utility
is not the primitive API; it is a schema-aware CLI client that connects to the
OVSDB server and changes the `ovs-vswitchd` configuration database.

For GoBFD, the preferred production design is:

1. Use an OVSDB client from Go for the default OVS backend.
2. Keep `ovs-vsctl` only as an explicit fallback or diagnostics path.
3. Keep `owner_policy: allow-external` explicit until a backend can prove
   ownership through OVSDB or a higher-level network manager.

## Sources

- RFC 7047 defines the OVSDB management protocol and its JSON-RPC wire model:
  <https://datatracker.ietf.org/doc/html/rfc7047>
- Open vSwitch `ovsdb(7)` describes OVSDB as a network-accessible database with
  ACID transactions and monitor support:
  <https://docs.openvswitch.org/en/stable/ref/ovsdb.7/>
- Open vSwitch `ovs-vsctl(8)` documents that `ovs-vsctl` connects to
  `ovsdb-server` and also documents `add-bond-iface` / `del-bond-iface`:
  <https://www.openvswitch.org/support/dist-docs/ovs-vsctl.8.html>
- `github.com/ovn-org/libovsdb` provides a Go OVSDB client with `Connect`,
  `Monitor`, `Mutate`, and `Transact` APIs:
  <https://pkg.go.dev/github.com/ovn-org/libovsdb>

## Implementation Status

S7.1e implements the preferred production design. `backend: ovs` now selects
`OVSDBLAGBackend`, which uses `github.com/ovn-org/libovsdb` and the configured
`micro_bfd.actuator.ovsdb_endpoint` to transact against OVSDB. The previous
`OVSLAGBackend` remains in the codebase as an explicit CLI fallback type, not
as the default backend selected by configuration.

## Required OVSDB Operations

The current `ovs-vsctl` backend maps RFC 7130 state transitions to these OVS
operations:

| Micro-BFD transition | CLI operation | OVSDB intent |
|---|---|---|
| `Up -> non-Up` | `ovs-vsctl --if-exists del-bond-iface <bond> <iface>` | Remove Interface UUID from `Port.interfaces` |
| `non-Up -> Up` | `ovs-vsctl --may-exist add-bond-iface <bond> <iface>` | Insert Interface UUID into `Port.interfaces`, creating Interface if needed |

The native backend must work against the `Open_vSwitch` schema:

- `Port` table: locate row by `name == lagInterface`.
- `Interface` table: locate or create row by `name == memberInterface`.
- `Port.interfaces`: mutate the set of Interface UUID references.
- Transaction result: check all operation results before reporting success.

## Design Direction

Add a new OVSDB-backed implementation behind the existing `LAGActuatorBackend`
interface:

```go
type OVSDBLAGBackend struct {
    client OVSDBClient
}
```

The production adapter can use `github.com/ovn-org/libovsdb`, but the GoBFD
core should depend on a narrow local interface so tests do not need a live OVSDB
server:

```go
type OVSDBLAGClient interface {
    RemoveBondInterface(ctx context.Context, bond, iface string) error
    AddBondInterface(ctx context.Context, bond, iface string) error
}
```

The backend can then reuse the same validation and RFC 7130 policy already used
by the kernel-bond and CLI-backed OVS paths.

## Risk Notes

- Direct OVSDB is better than shelling out because it avoids process spawning,
  shell-injection review exceptions, stdout/stderr parsing, and PATH dependency.
- OVSDB still needs a configured endpoint. The normal local endpoint is the
  Open vSwitch runtime socket. GoBFD defaults to
  `unix:/var/run/openvswitch/db.sock` and exposes
  `micro_bfd.actuator.ovsdb_endpoint` for distro-specific paths.
- Adding `libovsdb` increases dependency surface, so it must pass
  `make verify`, vulnerability audit, and module review before replacing the
  fallback.
- NetworkManager-managed devices use a separate D-Bus backend because
  NetworkManager owns connection profiles and exposes device state through
  D-Bus. That backend should not be conflated with direct OVSDB ownership.

## Sprint Impact

| Sprint | Result |
|---|---|
| S7.1d | Existing OVS backend is useful as a transitional CLI fallback. |
| S7.1d2 | Document OVSDB as the native OVS integration path. |
| S7.1e | Done: replace default OVS enforcement with a native OVSDB backend. |
| S7.1f | Done: add optional NetworkManager D-Bus backend for NM-owned devices. |
