# ADR-0002: Native OVSDB client for the OVS LAG backend

[Russian translation](../../ru/adr/0002-ovs-backend-ovsdb.md).

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-05-01 |

## Context

Micro-BFD enforcement changes a bonded port's member set. Spawning a configuration CLI adds process, PATH and output-parsing dependencies to the daemon. [RFC 7047](https://www.rfc-editor.org/rfc/rfc7047.html) defines the OVSDB management protocol.

## Decision

Select native `OVSDBLAGBackend` for configured OVS enforcement. Retain `OVSLAGBackend` as a directly constructed CLI fallback/diagnostic type; daemon configuration does not select it. Keep explicit owner policy until ownership can be proved by the actual manager.

The core consumes `BondLAGClient` with context-bounded `RemoveBondInterface` and `AddBondInterface`. The [production adapter](../../../internal/netio/lag_ovsdb.go) owns the external client; the [runtime manifest](../../../go.mod) owns its pin.

| Operation | Database intent | Validation |
|:---|:---|:---|
| Locate bond | Find `Port` by requested name | Require existing unambiguous target |
| Member Down | Remove Interface UUID from `Port.interfaces` | Check transaction and affected rows |
| Member Up | Find/create named `Interface`, insert its UUID | Check UUID shape and all operation results |

## Consequences

The default endpoint is `unix:/var/run/openvswitch/db.sock`; override it through `micro_bfd.actuator.ovsdb_endpoint`. Validate interface names before both operations. Backend construction reuses the LAG policy and input validation.

NetworkManager-owned profiles use the separate D-Bus backend. OVSDB access alone does not prove authority to mutate an externally managed bond. Dependency review, vulnerability checks, local transaction tests and actual OVS owner/member-wire qualification remain distinct release requirements.

## References

- [Backend selection](../../../internal/netio/lag_backend.go).
- [Native adapter and transaction validation](../../../internal/netio/lag_ovsdb.go).
- [OVSDB tests](../../../internal/netio/lag_ovsdb_test.go).
- [Linux applicability](0003-linux-advanced-bfd-applicability.md).
