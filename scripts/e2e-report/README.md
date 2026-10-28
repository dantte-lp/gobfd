# E2E Report Renderer Contract

This is a proposed renderer contract, not an implemented command or proof of
generated reports. This directory contains no renderer implementation; current
producers are under `test/internal/e2erunner/` and the selected E2E test packages.
Русское руководство по проверкам и существующим артефактам:
[разработка](../../docs/ru/09-development.md).

## Scope

| Field | Requirement |
|---|---|
| Status | Backlog. |
| Input root | `reports/e2e/<target>/<run-id>/`; current producers use `run.*`, second timestamps or nanosecond timestamps plus PID. |
| Output file | `index.html`. |
| Runtime | Offline static HTML, CSS, and JavaScript. |
| Network access | Not required. |
| External assets | Not allowed. |

## Input Artifacts

The table defines future renderer inputs. Current producers do not enforce one
uniform manifest: overlay CSV is synthetic, vendor manifest checks have no
PCAP, and failed startup may leave only diagnostics. Follow the actual
[harness artifact contract](../../test/e2e/README.md#artifact-layout) and
report absent evidence explicitly rather than treating it as a passing result.

| Artifact | Requirement |
|---|---|
| `summary.md` | Required. |
| `environment.json` | Required. |
| `go-test.json` | Required. |
| `go-test.log` | Required. |
| `containers.json` | Required. |
| `containers.log` | Required. |
| `packets.csv` | Required for wire-behavior targets. |
| `packets.pcapng` | Required for packet-capture targets. |
| Target-specific JSON | Optional, linked from the artifact index. |

## UI Contract

| Component | Requirement |
|---|---|
| Status summary | Target, run ID, exit code, duration, and timestamp. |
| Environment panel | Runtime, compose project, image references, and capability notes. |
| Test panel | Parsed Go test package/action/result rows. |
| Packet panel | CSV-backed packet table and pcap metadata link. |
| Container panel | Container state table and collapsible logs. |
| Artifact index | Links to all files in the report directory. |

## Styling Contract

| Property | Requirement |
|---|---|
| Layout | Dense engineering report layout. |
| Theme | Repository visual identity. |
| Cards | Only repeated report items. |
| Accessibility | Keyboard-readable links and sufficient contrast. |
| Dependencies | No remote fonts, CDNs, or external JavaScript. |
