# ADR-0014: GoBGP v4 v1.0 release-line graduation and cross-major benchmark baseline

[Russian translation](../../ru/adr/0014-v1-gobgp-v4-release-line-baseline.md).

| Field | Value |
|:---|:---|
| Status | Accepted |
| Date | 2026-11-27 |

## Context

`release/v0.6` (`v0.6.4`) maintains the legacy GoBGP v3 (`v3.37.0`, `/apipb.GobgpApi`) contract and flat `(peer, local, interface)` session lookup. `release/v1.0` introduces an incompatible major architecture coupled to GoBGP v4 (`v4.11.0`, `/api.GoBgpService`), the seven-field canonical `SessionKey` (`type`, `address_family`, `peer`, `local`, `interface`, `network_scope`, `transport_scope`), principal-scoped owner claims, Unix-first management security, and generation-stamped delivery and drop accounting.

Two release-automation constraints blocked graduating `release/v1.0` directly to `v1.0.0`:

1. [release-please-config.json](../../../release-please-config.json) remained pinned to `"versioning": "prerelease"` (`"prerelease": true`, `"prerelease-type": "alpha"`) under [ADR-0008](0008-experimental-prerelease-boundary.md), so automated release proposals could emit only `1.0.0-alpha.N`.
2. In [.github/workflows/release.yml](../../../.github/workflows/release.yml), `cictl release-benchmark-comparison` compares a newly tagged release against the preceding published stable release (`v0.6.4`) using [benchmark_report.go](../../../test/internal/cirunner/benchmark_report.go). Because `v1.0` adds `transport_scope` verification, owner-generation delivery accounting, and typed overload counters to packet admission while preserving `0 B/op` and `0 allocs/op`, `BenchmarkManagerDemux1000Sessions` measures ~180 ns/op on `release/v1.0` versus ~81 ns/op on `v0.6.4` (+124%). Under a uniform `>= 10%` `critical` error gate across major versions, `v0.6.4 -> v1.0.0` fails after immutable tag creation under the active `release-tags` ruleset.

On 2026-11-27, the maintainer authorized graduating `release/v1.0` directly to `1.0.0` as the new major product line coupled to GoBGP v4, keeping preview and experimental BFD extensions explicitly bounded by the [RFC coverage matrix](../08-rfc-compliance.md) and leaving long-horizon soak and hardware NOS campaigns as post-1.0 operational evidence rather than branch publication blockers.

## Decision

### 1. Graduate `release/v1.0` to stable `1.0.0` proposals

Configure [release-please-config.json](../../../release-please-config.json) on `release/v1.0` with `"versioning": "default"` while retaining `"skip-github-release": true` and `"skip-changelog": true`. Use a one-shot `Release-As: 1.0.0` commit footer when merging the graduation change so Release Please updates the open proposal pull request on `release/v1.0` to `1.0.0` without pinning subsequent `1.0.x` maintenance releases. Preserve [ADR-0008](0008-experimental-prerelease-boundary.md) and the historical `v1.0.0-alpha.1` tag (`206d25bccd4039dae746ddc8e85b16f277dd4588`) unchanged.

### 2. Establish a cross-major release benchmark baseline boundary

Update [benchmark_report.go](../../../test/internal/cirunner/benchmark_report.go) (`applyCrossMajorReleasePolicy`) so that when `BenchmarkReleaseContext` compares canonical delivery tags across distinct major versions (`v0` baseline vs `v1` target):

1. All ten interleaved non-race pairs and the bounded `-race` receive preflight execute unchanged against both `v0.6.4` and the `v1.0.0` tag commit using the verified `decode-packet-per-iteration-v1` fixture overlay.
2. Any `critical` `sec/op` delta across the `v0 -> v1` architectural boundary is recorded as `"reported"` in `comparison-vs-baseline.json` and accompanied by an explicit provenance note in `comparison-vs-baseline-notes.txt`, establishing `v1.0.0` as the new `v1` major baseline without failing `release.yml`.
3. Pull-request benchmark checks (`ReleaseContext == nil`), same-major release comparisons (`v0.6.x -> v0.6.y` and `v1.0.0 -> v1.x.y`), and malformed/non-canonical baseline identifiers retain strict `critical` failure (`errCriticalBenchmarkRegression` at `>= 10%`).

### 3. Preserve explicit capability maturity boundaries in `v1.0.0`

`v1.0.0` ships the GoBGP v4 actuator, RFC 5880 core, RFC 5881 numbered multiaccess single-hop, RFC 5883 multihop, RFC 7419 common intervals, and local-first management security. Echo (RFC 9747), Unsolicited BFD (RFC 9468), Micro-BFD (RFC 7130), VXLAN BFD (RFC 8971), and Geneve BFD (RFC 9521) remain preview or experimental as documented in [RFC coverage](../08-rfc-compliance.md). Users requiring GoBGP v3.37.0 remain on `release/v0.6`.

## Alternatives

| Alternative | Reason not selected |
|:---|:---|
| Raise or disable the `10%` `sec/op` threshold globally | Weakens pull-request and `v1.0.x` maintenance regression detection |
| Replace `previousPublishedRelease` with a synthetic unchecked baseline file | Violates measured same-runner paired provenance in `release_benchmark.go` |
| Keep `release/v1.0` locked to `1.0.0-alpha.N` indefinitely | Contradicts the maintainer decision establishing `release/v1.0` as the GoBGP v4 `1.0.0` release line |

## Consequences

- `v1.0.0` compares transparently against `v0.6.4` during its initial `release.yml` run, publishes full `comparison-vs-baseline.*` reports inside `gobfd-1.0.0-reports.tar.gz`, and becomes the strict `critical` baseline automatically for `v1.0.1` and `v1.1.0`.
- Stable `v1.0.0` publication promotes GHCR `:latest` and `:debian-trixie` aliases to the GoBGP v4 line; operators on GoBGP v3 must pin `0.6.4` or `release/v0.6`.
- Extended 24-hour soak and external hardware NOS campaigns remain tracked separately as operational qualification artifacts without blocking `1.0.0` branch publication.

## References

- [Production contract](../reference/production-contract.md#gobgp-v4-actuator).
- [CI and release automation](../reference/automation.md#benchmarks).
- [Changelog and releases](../10-changelog.md).
- [ADR-0008: Experimental prerelease boundary](0008-experimental-prerelease-boundary.md).
- [ADR-0013: Speaker-epoch disable ownership across GoBFD restarts](0013-gobgp-restart-disable-ownership.md).
