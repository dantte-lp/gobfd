# OVSDB API Research для Micro-BFD OVS Backend

Область: native OVS API для RFC 7130 Micro-BFD member enforcement на OVS
bonded ports.

## Решение

Open vSwitch exposes a built-in management API through OVSDB. OVSDB is a
JSON-RPC protocol specified by RFC 7047 and implemented by `ovsdb-server`.
`ovs-vsctl` is a schema-aware CLI client for OVSDB, not the primitive API.

GoBFD production design:

1. `backend: ovs` uses native OVSDB transactions by default.
2. `ovs-vsctl` remains an explicit fallback/diagnostics implementation.
3. `owner_policy: allow-external` remains required for external OVS ownership.
4. The OVSDB endpoint is configured with
   `micro_bfd.actuator.ovsdb_endpoint`.

## Official Sources

- RFC 7047 OVSDB Management Protocol:
  <https://datatracker.ietf.org/doc/html/rfc7047>
- Open vSwitch `ovsdb(7)`:
  <https://docs.openvswitch.org/en/stable/ref/ovsdb.7/>
- Open vSwitch `ovs-vsctl(8)`:
  <https://www.openvswitch.org/support/dist-docs/ovs-vsctl.8.html>
- `github.com/ovn-org/libovsdb` API reference:
  <https://pkg.go.dev/github.com/ovn-org/libovsdb>

## Implementation Status

S7.1e implements the selected design. `backend: ovs` selects
`OVSDBLAGBackend`, which uses `github.com/ovn-org/libovsdb` and the configured
`micro_bfd.actuator.ovsdb_endpoint` to transact against OVSDB. `OVSLAGBackend`
remains available as an explicit CLI fallback type.

## Required OVSDB Operations

| Micro-BFD transition | CLI equivalent | OVSDB intent |
|---|---|---|
| `Up -> non-Up` | `ovs-vsctl --if-exists del-bond-iface <bond> <iface>` | Remove Interface UUID from `Port.interfaces` |
| `non-Up -> Up` | `ovs-vsctl --may-exist add-bond-iface <bond> <iface>` | Insert Interface UUID into `Port.interfaces`, creating Interface if needed |

Required `Open_vSwitch` schema objects:

- `Port` table: row selected by `name == lagInterface`.
- `Interface` table: row selected or created by `name == memberInterface`.
- `Port.interfaces`: set mutation for Interface UUID references.
- Transaction result: every operation result checked before success.

## Local Interface Boundary

The production adapter uses `github.com/ovn-org/libovsdb`. GoBFD tests use a
narrow local interface:

```go
type OVSDBLAGClient interface {
    RemoveBondInterface(ctx context.Context, bond, iface string) error
    AddBondInterface(ctx context.Context, bond, iface string) error
}
```

## Risk Controls

| Risk | Control |
|---|---|
| Process spawning and PATH dependency | Native OVSDB transaction path |
| Shell injection and stdout/stderr parsing | No shell in default backend |
| Distro-specific OVSDB socket path | Configurable `ovsdb_endpoint`; default `unix:/var/run/openvswitch/db.sock` |
| Dependency surface from `libovsdb` | `make verify`, vulnerability audit, module review |
| NetworkManager-owned device conflict | Separate D-Bus backend, no conflation with OVSDB ownership |

## Sprint Impact

| Sprint | Result |
|---|---|
| S7.1d | Transitional OVS CLI fallback backend added. |
| S7.1d2 | OVSDB documented as native OVS integration path. |
| S7.1e | Native OVSDB backend implemented for default `backend: ovs`. |
| S7.1f | NetworkManager D-Bus backend implemented for NM-owned devices. |
