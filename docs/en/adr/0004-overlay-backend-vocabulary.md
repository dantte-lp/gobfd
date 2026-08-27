# ADR-0004: Shared overlay backend vocabulary

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-08-28 |

## Context

The configuration and network-I/O packages independently defined the same
seven VXLAN and Geneve backend names. They also normalized the input at
different boundaries: configuration validation trimmed surrounding whitespace,
while the backend constructor required the exact stored value. A whitespace-
padded value could therefore pass startup validation and fail later while the
daemon created its socket.

Neither package is a suitable owner for the shared vocabulary. Importing
`internal/netio` from `internal/config` would couple configuration to Linux I/O,
and importing `internal/config` from `internal/netio` would pull configuration
policy and Koanf dependencies into the socket adapter.

## Decision

`internal/overlay` owns the backend type, the seven wire/configuration
spellings, normalization, recognition, and implementation status. It is a leaf
package that imports only the Go standard library. Both `internal/config` and
`internal/netio` import it and keep their existing exported compatibility names
and package-specific sentinel errors.

The shared parser preserves the external configuration behavior: it trims
surrounding whitespace, maps an empty value to `userspace-udp`, treats spelling
as case-sensitive, and recognizes the six reserved backend names. Only
`userspace-udp` is implemented. The network-I/O constructor now applies the
same normalization as startup validation.

## Consequences

- YAML and environment field names, values, defaults, and configuration types
  do not change.
- `config` continues to distinguish invalid names from recognized but
  unsupported backends through its existing errors.
- `netio` retains its own invalid, unsupported, and invalid-input errors and
  constructor context.
- Adding a backend spelling or changing implementation status has one source
  of truth.
- This decision does not implement any reserved backend or change protobuf
  contracts.

## References

- [Go 1.27 specification: Import declarations](https://go.dev/ref/spec#Import_declarations)
- [ADR-0003: Linux applicability of Micro-BFD, VXLAN BFD, and Geneve BFD](./0003-linux-advanced-bfd-applicability.md)
