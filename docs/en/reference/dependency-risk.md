# Dependency Risk Register

This document tracks external dependencies that carry non-trivial risk due to maintenance status, single-maintainer projects, or upstream changes. Each entry includes a risk assessment and mitigation strategy.

## reeflective/console — Medium Risk

| Field | Value |
|-------|-------|
| **Module** | `github.com/reeflective/console v0.5.0` |
| **Used in** | `cmd/gobfdctl/commands/shell.go` — interactive REPL shell |
| **Risk level** | Medium |
| **Reason** | Pre-1.0 library and single-maintainer bus-factor risk |

### Description

The `reeflective/console` library provides the interactive shell for `gobfdctl`. It wraps Cobra commands into a readline-based REPL with tab completion, history, and prompt customization.

### Risk Factors

- **Pre-1.0 API**: The library has not reached v1.0, meaning breaking API changes are possible between minor versions.
- **Single maintainer**: The project is maintained by a single developer. Bus factor = 1.
- **Transitive dependencies**: Pulls in readline, completion, and terminal UI modules.
- **No stdlib alternative**: Go's standard library does not include a Cobra-compatible REPL framework.

### Mitigation

1. **Pin version**: The dependency is pinned to `v0.5.0` in `go.mod`. The
   `v0.1.25` to `v0.5.0` release-note range and CLI/race tests were reviewed
   before adoption.
2. **Vendoring**: If the project becomes unmaintained, vendor the source into `internal/console/`.
3. **Graceful degradation**: The non-interactive CLI (`gobfdctl <command>`) works without this dependency. The interactive shell is a convenience feature only.
4. **Bounded scope**: The dependency is isolated to a single file (`shell.go`) and does not affect the daemon or protocol implementation.

---

## osrg/gobgp/v3 — High Risk

| Field | Value |
|-------|-------|
| **Module** | `github.com/osrg/gobgp/v3 v3.37.0` |
| **Used in** | `internal/gobgp/` — optional GoBGP integration |
| **Known advisory** | `GO-2026-4736` / `CVE-2026-30405` / `GHSA-4p9m-8gc4-rw2h` |
| **Risk level** | High |
| **Allowlist owner** | `maintainers` |
| **Review deadline** | `2026-09-30` |

### Description

GoBGP is affected by a denial-of-service advisory in BGP NEXT_HOP path
attribute handling. As of 2026-08-20, the Go vulnerability database does not
list a fixed version.

### Mitigation

1. **Bounded exposure**: The GoBGP path is optional and should connect only to a
   GoBGP gRPC endpoint on localhost or a trusted management network.
2. **Controlled CI allowlist**: the `GO-2026-4736` entry includes owner,
   expiry, reason, and mitigation and accepts only findings under the exact
   GoBGP v3 module path. Any package mismatch, unknown scanner, additional
   advisory, or expired allowlist entry fails CI.
3. **Upgrade trigger**: Remove the allowlist entry after upstream publishes a
   fixed GoBGP release and `go mod tidy` moves the module to that version.

---

## x/crypto/openpgp — Module-only Advisory

| Field | Value |
|-------|-------|
| **Module** | `golang.org/x/crypto v0.55.0` (indirect) |
| **Affected package** | `golang.org/x/crypto/openpgp` (absent from the build graph) |
| **Known advisory** | `GO-2026-5932` |
| **Allowlist mode** | Exact-module inventory findings only |
| **Review deadline** | `2026-09-30` |

### Mitigation

1. **No affected import**: `go list -deps ./...` must not contain
   `golang.org/x/crypto/openpgp`.
2. **Fail-closed reachability**: the audit accepts only non-reachable findings
   for the exact `golang.org/x/crypto` module from `govulncheck` or
   `osv-scanner`. A reachable symbol, affected subpackage, package mismatch, or
   unknown scanner fails CI even when the advisory ID matches.
3. **Removal trigger**: remove the exception when the module no longer produces
   the inventory finding; never introduce an `openpgp` import.

---

## YAML v3 module-path transition — Low Risk

| Field | Value |
|-------|-------|
| **First-party module** | `go.yaml.in/yaml/v3 v3.0.5` (direct) |
| **Legacy module** | `gopkg.in/yaml.v3 v3.0.1` (transitive only) |
| **Used by** | CLI output, HAProxy configuration, integration tests, and transitive console code |
| **Risk level** | Low |

### Description

The `gopkg.in/yaml.v3` module was archived by its maintainer in April 2025.
All GoBFD-owned imports now use its maintained, API-compatible successor,
`go.yaml.in/yaml/v3`. The legacy path remains only in the transitive
`reeflective/console`/Carapace graph.

### Risk Factors

- **Archived repository**: No further bug fixes or security patches will be released for the `gopkg.in` path.
- **Transitive migration pending**: GoBFD cannot remove the legacy module from
  the complete MVS graph until console/Carapace migrate their internal imports.

### Mitigation

1. **First-party migration complete**: GoBFD code imports only
   `go.yaml.in/yaml/v3`; `go mod why` identifies console/Carapace as the sole
   remaining path to the archived module.
2. **Monitor upstream**: Remove the transitive legacy module when
   console/Carapace complete their migration and `go mod tidy` permits it.
3. **Regression gate**: New first-party imports of `gopkg.in/yaml.v3` are not
   permitted.
4. **No known advisory**: The archived module `v3.0.1` has no known
   vulnerability; the maintained successor is pinned to `v3.0.5`.
