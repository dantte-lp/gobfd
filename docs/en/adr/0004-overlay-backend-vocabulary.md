# ADR-0004: Shared overlay backend vocabulary

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-08-28 |

## Context

Startup validation and network-I/O construction must accept the same backend spellings and normalization. A dependency from configuration to Linux I/O, or from sockets to configuration loaders, would cross the package boundary.

## Decision

[`internal/overlay`](../../../internal/overlay/backend.go) owns the backend type, seven spellings, parser and implementation status. It imports only the standard library. Configuration and network I/O consume it while retaining their compatibility aliases and package-specific sentinel errors.

| Input | Result |
|:---|:---|
| Surrounding whitespace | Trim |
| Empty value | `userspace-udp` |
| Case variant | Reject; names are case-sensitive |
| `userspace-udp` | Recognized and implemented |
| `kernel`, `ovs`, `ovn`, `cilium`, `calico`, `nsx` | Recognized, unsupported; fail closed |
| Other name | Invalid |

## Consequences

YAML/environment field names, defaults, values and public configuration types remain unchanged. Both startup validation and backend construction apply the parser. Invalid, unsupported and invalid-input errors retain package context. Adding a spelling or implementation status has one owner; recognition never creates a dataplane backend or changes protobuf.

## References

- [Shared parser](../../../internal/overlay/backend.go).
- [Constructor admission](../../../internal/netio/overlay_backend.go).
- [Configuration validation](../../../internal/config/config.go).
- [Linux applicability](0003-linux-advanced-bfd-applicability.md).
