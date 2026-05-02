# Protovalidate Proto Vendor

| Field | Value |
|---|---|
| Upstream | `bufbuild/protovalidate` |
| Version | `v1.2.0` |
| File | `proto/protovalidate/buf/validate/validate.proto` |
| License | Apache-2.0 |
| Purpose | Local Buf workspace import resolution for `buf/validate/validate.proto` |

The vendored proto file keeps `buf lint` independent from Buf Schema Registry
availability while preserving the upstream `go_package` used by generated Go
code.
