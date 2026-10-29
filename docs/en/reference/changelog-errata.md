# Historical changelog translation corrections

Correction recorded on **2026-10-29** for the `v0.5.0` section dated
2026-05-01 in the [English changelog](../../../CHANGELOG.md).
The [Russian changelog](../../../CHANGELOG.ru.md) omitted the four Changed
entries reproduced below. Published sections remain unchanged; this record
supplies their translation without treating historical versions as current
dependencies or accepted v1 artifacts.

## v0.5.0 omissions

| Entry in the English record | Historical content |
|:---|:---|
| GitHub Actions pins | Pins followed upstream release tags for checkout, cache, setup, CodeQL, GoReleaser, Trivy, gosec, Codecov, Buf, GitHub Script, SonarQube and artifact actions. |
| Go lint configuration | `golangci-lint` enabled 93 validated analyzers with its v2 configuration, retaining `snake_case` JSON/YAML tags in public API and CLI contracts. |
| Vulnerability tools | The record selected `govulncheck v1.3.0` and retained OSV Scanner `v2.3.5`, because `v2.3.6` was not usable through `go run` as a versioned tool module. |
| OCI release variants | GoReleaser declared Debian `trixie-slim` and Oracle Linux `10-slim` variants for `linux/amd64` and `linux/arm64`. |

These statements describe the retained release record. Current compiler,
tool, analyzer and container policy comes from [Development](../09-development.md)
and [Deployment](../06-deployment.md). Oracle Linux 10 is excluded from v1
OCI qualification. This correction does not establish that any historical
asset was independently revalidated.

The [Russian correction](../../ru/reference/changelog-errata.md) carries
the same entries and historical boundary.
