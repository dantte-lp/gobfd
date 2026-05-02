# Dependency Risk Register

This document tracks external dependencies that carry non-trivial risk due to maintenance status, single-maintainer projects, or upstream changes. Each entry includes a risk assessment and mitigation strategy.

## reeflective/console — Medium Risk

| Field | Value |
|-------|-------|
| **Module** | `github.com/reeflective/console v0.1.25` |
| **Used in** | `cmd/gobfdctl/commands/shell.go` — interactive REPL shell |
| **Risk level** | Medium |
| **Reason** | Pre-1.0 library, single maintainer, 6 transitive dependencies |

### Description

The `reeflective/console` library provides the interactive shell for `gobfdctl`. It wraps Cobra commands into a readline-based REPL with tab completion, history, and prompt customization.

### Risk Factors

- **Pre-1.0 API**: The library has not reached v1.0, meaning breaking API changes are possible between minor versions.
- **Single maintainer**: The project is maintained by a single developer. Bus factor = 1.
- **Transitive dependencies**: Pulls in 6 additional dependencies (readline, completions, etc.).
- **No stdlib alternative**: Go's standard library does not include a Cobra-compatible REPL framework.

### Mitigation

1. **Pin version**: The dependency is pinned to `v0.1.25` in `go.mod`. Upgrades should be tested before adoption.
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
| **Review deadline** | `2026-07-31` |

### Description

GoBGP is affected by a denial-of-service advisory in BGP NEXT_HOP path
attribute handling. As of 2026-05-02, the Go vulnerability database does not
list a fixed version.

### Mitigation

1. **Bounded exposure**: The GoBGP path is optional and should connect only to a
   GoBGP gRPC endpoint on localhost or a trusted management network.
2. **Controlled CI allowlist**: `scripts/vuln-audit.go` allowlists only
   `GO-2026-4736`; the entry includes owner, expiry, reason, and mitigation.
   Any additional `govulncheck` or OSV finding, and any expired allowlist
   entry, fails CI.
3. **Upgrade trigger**: Remove the allowlist entry after upstream publishes a
   fixed GoBGP release and `go mod tidy` moves the module to that version.

---

## gopkg.in/yaml.v3 — Low Risk

| Field | Value |
|-------|-------|
| **Module** | `gopkg.in/yaml.v3 v3.0.1` (direct) |
| **Successor** | `go.yaml.in/yaml/v3 v3.0.4` (already indirect dep) |
| **Used by** | `koanf/parsers/yaml` — YAML configuration parsing |
| **Risk level** | Low |

### Description

The `gopkg.in/yaml.v3` module was archived by its maintainer in April 2025. The successor module `go.yaml.in/yaml/v3` is API-compatible and already present as an indirect dependency through other packages.

### Risk Factors

- **Archived repository**: No further bug fixes or security patches will be released for the `gopkg.in` path.
- **Upstream migration pending**: GoBFD uses yaml.v3 through `koanf/parsers/yaml`. The koanf maintainers have not yet migrated to the successor path.

### Mitigation

1. **No code changes needed**: The successor `go.yaml.in/yaml/v3` is API-compatible. When koanf migrates, GoBFD's `go.mod` will automatically switch during `go mod tidy`.
2. **Already indirect**: GoBFD does not import yaml.v3 directly in application code — it flows through the koanf configuration parser.
3. **Monitor upstream**: Track the koanf issue for yaml.v3 migration. When resolved, run `go mod tidy` to complete the switch.
4. **No security concern**: The archived module `v3.0.1` has no known vulnerabilities. The successor is maintained by the same author under a new module path.
