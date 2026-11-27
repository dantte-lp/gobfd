# Dependency risk and exceptions

[Russian translation](../../ru/reference/dependency-risk.md).

Selected versions belong to [go.mod](../../../go.mod), [tools/go.mod](../../../tools/go.mod) and the separate [Smallstep profile](../../../test/stepca/README.md). The authoritative runtime/tools exception implementation is [vuln-audit.go](../../../scripts/vuln-audit.go). This register records source policy; current advisory databases and the final selected graph require a fresh scan.

## Graph ownership

| Graph | Purpose | Acceptance boundary |
|:---|:---|:---|
| Runtime | Daemon, CLI, bridges and test helpers | Runtime package/symbol reachability |
| Tools | Isolated development/scanning/generation tools | Separate selected tool graph |
| Smallstep qualification | Full external test provider | Independent build/scan/link/runtime receipts |

Inventory success does not establish security. A module finding, imported affected package and reachable symbol are different results.

## Controlled exceptions

`scripts/vuln-audit.go` currently carries zero runtime, tool, `gopls`, or Syft allowlist exceptions. Reachable findings, affected imported packages, module mismatch, unknown scanner, additional advisory, expiry and an exception with no corresponding finding fail the controlled audit. Do not broaden an exception to make a scan green.

### GoBGP

The repository pin is github.com/osrg/gobgp/v4 v4.11.0; handwritten integration imports only `github.com/osrg/gobgp/v4/api`. The historical `GO-2026-4736` (`CVE-2026-30405` / `GHSA-4p9m-8gc4-rw2h`) module-only exception was retired after upstream fix coverage and the Go vulnerability database update eliminated the inventory finding. Keep GoBGP on loopback/trusted management networks and keep `github.com/osrg/gobgp/v4/pkg/server` excluded from the build graph.

The v0.6 line retains GoBGP v3.37.0 and its separately accepted support boundary until 2026-12-31. Verify delivery on that supported branch separately. Dev acceptance does not transfer to that line. The v3/v4 RPC service contracts differ; use the matching daemon.

### OpenPGP

The selected indirect crypto module is v0.59.0, retiring the earlier `GO-2026-5932` module-only exception. Confirm the final build graph continues to exclude `golang.org/x/crypto/openpgp`.

## Other retained dependencies

| Dependency | Product use | Required check |
|:---|:---|:---|
| reeflective/console v0.5.0 | gobfdctl interactive shell | CLI/race compatibility; fresh upstream review before upgrade |
| go.yaml.in/yaml/v3 v3.0.6 | First-party YAML | Preserve decoding/output compatibility |
| gopkg.in/yaml.v3 v3.0.1 | Transitive legacy graph | Recheck exact go mod why chain; no new first-party imports |
| golang.org/x/net v0.63.0 | Transport and selected graphs | Fresh security scan and IPv4 transport regression checks |

Console is a build dependency of the CLI even when noninteractive commands do not start the REPL. Its integration point is [shell.go](../../../cmd/gobfdctl/commands/shell.go); this register does not establish upstream staffing or maintenance status.

The YAML migration records a historical upstream-path decision. Current archival/release status is not inferred from it. Remove the transitive path only when the final graph permits it; do not vendor another implementation pre-emptively.

The retained x/net update addresses [GO-2026-6860](https://vuln.go.dev/ID/GO-2026-6860) and [GO-2026-6866](https://vuln.go.dev/ID/GO-2026-6866), without an allowlist entry. The separate Smallstep profile also selects v0.62.0. A pin is not proof that current scanner data is unchanged.

## Validate a candidate

1. Record exact runtime/tools/provider graph identities.
2. Run `make dependency-inventory-check` and the controlled `make vulncheck` in the pinned environment.
3. Inspect raw scanner results, reachability and exact exception matches.
4. Run impacted transport/CLI/API regressions and provider qualification where applicable.
5. Retain scanner version/data time, source SHA, findings and disposition. Keep supported-line and release acceptance separate.

[Development](../09-development.md), [automation](automation.md), [security policy](../../../SECURITY.md), [supply-chain inventory](../../supply-chain/dependency-inventory.json).
