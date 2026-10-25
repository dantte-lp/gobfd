# Dependency Risk Register

This document tracks external dependencies that carry non-trivial risk due to maintenance status, single-maintainer projects, or upstream changes. Each entry includes a risk assessment and mitigation strategy.

Pins come from [`go.mod`](../../../go.mod); advisory acceptance comes from
[`scripts/vuln-audit.go`](../../../scripts/vuln-audit.go)
and its Beads owner. This register does not replace fresh scans of the final
runtime/tool graphs or establish the current upstream maintainer count.

## reeflective/console — Medium Risk

| Field | Value |
|-------|-------|
| **Module** | `github.com/reeflective/console v0.5.0` |
| **Used in** | `cmd/gobfdctl/commands/shell.go` — interactive REPL shell |
| **Risk level** | Medium |
| **Reason** | Pre-1.0 API and external maintenance dependency |

### Description

The `reeflective/console` library provides the interactive shell for `gobfdctl`. It wraps Cobra commands into a readline-based REPL with tab completion, history, and prompt customization.

### Risk Factors

- **Pre-1.0 API**: The library has not reached v1.0, meaning breaking API changes are possible between minor versions.
- **External maintenance**: Upstream staffing and release activity require a fresh review before an upgrade.
- **Transitive dependencies**: Pulls in readline, completion, and terminal UI modules.
- **No stdlib alternative**: Go's standard library does not include a Cobra-compatible REPL framework.

### Mitigation

1. **Pin version**: The dependency is pinned to `v0.5.0` in `go.mod`.
   [The dependency-refresh design](../../superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md#go-module-closure)
   records the `v0.1.25` to `v0.5.0` migration; that historical record does not
   replace CLI/race qualification of the final candidate.
2. **Vendoring**: If the project becomes unmaintained, vendor the source into `internal/console/`.
3. **Runtime scope**: Non-interactive commands do not start the REPL. The CLI
   binary still depends on the module at build time; it is not an optional build dependency.
4. **Bounded scope**: Product integration is in `shell.go`; the daemon and
   protocol implementation do not import the console package.

---

## osrg/gobgp — High Risk

| Field | Value |
|-------|-------|
| **Module on `dev`** | `github.com/osrg/gobgp/v4 v4.10.0`; only `github.com/osrg/gobgp/v4/api` is imported |
| **Module on `release/v0.6`** | `github.com/osrg/gobgp/v3 v3.37.0` |
| **Used in** | `internal/gobgp/` — optional GoBGP integration |
| **Known advisory** | `GO-2026-4736` / `CVE-2026-30405` / `GHSA-4p9m-8gc4-rw2h` |
| **Risk level** | High |
| **Allowlist owner** | `@dantte-lp` |
| **Expiry (review) date** | `2026-12-31` |

### Description

The acceptance rationale recorded in [`scripts/vuln-audit.go`](../../../scripts/vuln-audit.go)
and [SECURITY.md](../../../SECURITY.md) concerns the BGP NEXT_HOP denial-of-service
advisory. It records GHSA-4p9m-8gc4-rw2h's patched v4.4.0 (upstream commit
`583080a`) and the Go vulnerability database's missing fixed v4 version, scoped
to `fsmHandler.recvMessageloop` in `github.com/osrg/gobgp/v4/pkg/server`.
GoBFD imports the API package instead. The recorded `dev` acceptance covers
module-only govulncheck and OSV findings; a fresh final-candidate scan must
confirm that boundary. This register does not establish current database contents.

GoBGP v3 serves `/apipb.GobgpApi` and v4 serves `/api.GoBgpService`; the two
are wire-incompatible. GoBFD on `dev` requires a GoBGP v4 daemon. The v0.6
acceptance below records the v3.37.0 exposure; it does not establish whether
upstream has since published a fix.

### Mitigation

1. **Bounded exposure**: The GoBGP path is optional and should connect only to a
   GoBGP gRPC endpoint on localhost or a trusted management network.
2. **Controlled CI allowlist on `dev` and stable v1**: by maintainer decisions
   of 2026-10-13 and 2026-10-21 (stable v1, `gobfd-qj0.8.4.1`), the
   `GO-2026-4736` entry is `ModuleOnly` for the exact
   `github.com/osrg/gobgp/v4` module and includes owner `@dantte-lp`, expiry
   (review) date 2026-12-31, reason, and mitigation. Stable v1 also requires
   GHSA/OSV to list a fixed version at or below the pin and the final
   candidate's govulncheck to report the advisory only at module level. A
   reachable symbol, imported affected package, package mismatch, unknown
   scanner, additional advisory, expired entry, or stale entry without a
   matching finding fails CI.
3. **`release/v0.6` risk acceptance**: by maintainer decision of 2026-10-13,
   the v0.6 line accepts the GoBGP v3.37.0 risk until 2026-12-31 as a support
   boundary, with the GoBGP API on localhost or a trusted management network
   only. Delivery to the stable branch is tracked by Beads
   `gobfd-qj0.8.1.16.4`.
4. **Removal trigger**: remove the allowlist entry when the Go vulnerability
   database records a fixed GoBGP v4 version (golang/vulndb#5035, #6562). The
   audit then fails on the entry because it matches no scanner finding.

---

## x/crypto/openpgp — Module-only Advisory

| Field | Value |
|-------|-------|
| **Module** | `golang.org/x/crypto v0.58.0` (indirect) |
| **Affected package** | `golang.org/x/crypto/openpgp` (absent from the build graph) |
| **Known advisory** | `GO-2026-5932` |
| **Allowlist mode** | Exact-module inventory findings only |
| **Expiry (review) date** | `2026-12-31` |

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

The [dependency-refresh design](../../superpowers/specs/2026-08-18-v0.6.2-dependency-refresh-design.md#go-module-closure)
records archival of `gopkg.in/yaml.v3` as the reason for migration.
GoBFD-owned imports use `go.yaml.in/yaml/v3`; the legacy path remains in the
transitive graph. Recheck upstream maintenance and API compatibility before
an upgrade; the migration record does not establish current upstream status.

### Risk Factors

- **Recorded archival**: The legacy path carries an external maintenance risk;
  the migration record is not a promise about future upstream releases.
- **Transitive migration pending**: GoBFD cannot remove the legacy module from
  the complete MVS graph until console/Carapace migrate their internal imports.

### Mitigation

1. **First-party migration complete**: GoBFD code imports only
   `go.yaml.in/yaml/v3`. Re-run `go mod why gopkg.in/yaml.v3` against the
   final module graph before attributing the transitive path to an upstream.
2. **Monitor upstream**: Remove the transitive legacy module when
   console/Carapace complete their migration and `go mod tidy` permits it.
3. **Regression gate**: New first-party imports of `gopkg.in/yaml.v3` are not
   permitted.
4. **Security review**: Scan both exact module graphs before qualification;
   archival status and the successor pin do not establish absence of advisories.

*Reviewed against `dev` source: 2026-10-25 (`706991db9433b13cec47e0bb8ea2549b01e948d9`); upstream status and final-candidate scanner results require separate evidence.*
