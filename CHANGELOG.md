# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-02-24

### Added

- RFC 7419 common interval support for BFD session timer negotiation.
- RFC 9468 unsolicited BFD mode for sessionless applications with passive listener.
- RFC 9747 unaffiliated BFD echo function with echo receiver and reflector.
- RFC 7130 Micro-BFD for LAG interfaces with per-member-link sessions and aggregate state.
- RFC 8971 BFD for VXLAN tunnels with overlay-aware packet handling.
- RFC 9521 BFD for Geneve tunnels with option-C encapsulation.
- RFC 9384 BGP Cease NOTIFICATION subcode 10 (BFD Down) via GoBGP integration.
- Vendor interop lab bootstrap script (`test/interop-clab/bootstrap.py`): automated image preparation for Nokia SR Linux, SONiC-VS, FRRouting, VyOS, Arista cEOS, Cisco XRd.
- RFC-specific interop test suite (`test/interop-rfc/`): dedicated tests for unsolicited BFD, echo function, and BGP Cease notification.
- Cisco XRd vendor interop support with XR configuration and PID limit handling.
- SONiC-VS interop improvements with robust BGP/BFD configuration script.

### Changed

- Vendor interop `run.sh` gracefully skips vendors that fail initialization instead of aborting.

## [0.2.0] - 2026-02-23

### Added

- IPv6 dual-stack BFD testing in vendor interop suite (RFC 5881 Section 5): Arista cEOS, Nokia SR Linux, FRRouting tested with ULA fd00::/8 addresses and /127 prefixes per RFC 6164.
- SonarCloud integration for continuous code quality analysis.
- Codecov integration for test coverage tracking.
- CodeQL and gosec SARIF workflows for deep security analysis.
- Dependabot configuration for automated dependency updates (Go, Docker, GitHub Actions).
- Changelog documentation guide (docs/en/10-changelog.md, docs/ru/10-changelog.md).
- `osv-scanner` vulnerability scanning in CI and Makefile (`make osv-scan`).
- `gofumpt` and `golines` (max-len: 120) formatters in golangci-lint.
- BGP+BFD full-cycle interop tests: GoBFD+GoBGP ↔ FRR, BIRD3, ExaBGP (3 scenarios with route verification).
- Containerlab vendor BFD interop tests: Nokia SR Linux, FRRouting, Arista cEOS (available); Cisco XRd, SONiC-VS, VyOS (defined, skip if image absent).
- Arista cEOS 4.35.2F support: `start_arista_ceos()` with 8 mandatory env vars, `wait_arista_ceos()` boot health check, protocol-triggered BFD via BGP.
- Nokia SR Linux BFD timer fix: bounce subinterface after config commit to negotiate at 300ms.
- netlab integration documented as future direction for VM-based vendor testing.
- Integration example: GoBFD + GoBGP + FRR (BGP fast failover with route withdrawal demo).
- Integration example: GoBFD + HAProxy (agent-check backend health with sub-second failover).
- Integration example: GoBFD + Prometheus + Grafana (observability with 4 alert rules).
- Integration example: GoBFD + ExaBGP (anycast service announcement via BFD-controlled process API).
- Integration example: GoBFD DaemonSet in Kubernetes (k3s with GoBGP sidecar and host networking).
- New binary: `gobfd-haproxy-agent` — HAProxy agent-check bridge for BFD health monitoring.
- New binary: `gobfd-exabgp-bridge` — ExaBGP process API bridge for BFD-controlled route announcements.
- tshark packet verification sidecar in all integration stacks.
- Integration documentation (docs/en/11-integrations.md, docs/ru/11-integrations.md).
- Makefile targets for all integration examples (`int-bgp-failover`, `int-haproxy`, `int-observability`, `int-exabgp-anycast`, `int-k8s`).
- Version display (`--version`) for all binaries with commit hash and build date.
- Shared version package (`internal/version`) with ldflags injection.
- Version injection in Makefile, CI, GoReleaser, and all Containerfiles.

### Changed

- `make build` now injects version, commit hash, and build date via ldflags for all 4 binaries.
- Replaced `c-bata/go-prompt` with `reeflective/console` for interactive shell.
- Expanded golangci-lint from 39 to 68 linters with strict security-focused configuration.
- Split CI workflow into parallel jobs (build-and-test, lint, vulnerability-check, sonarcloud, buf).
- Enhanced release workflow to extract release notes from CHANGELOG.md.
- Renamed Prometheus gauge metric `gobfd_bfd_sessions_total` to `gobfd_bfd_sessions` (convention fix).

## [0.1.0] - 2026-02-21

### Added

- BFD Control packet codec with round-trip fuzz testing.
- Table-driven FSM matching RFC 5880 Section 6.8.6.
- Five authentication modes: Simple Password, Keyed MD5/SHA1, Meticulous MD5/SHA1.
- Raw socket abstraction for Linux (UDP 3784/4784, TTL=255 GTSM).
- Session manager with discriminator allocation and detection timeout.
- ConnectRPC/gRPC API server with recovery and logging interceptors.
- `gobfdctl` CLI with Cobra commands and interactive shell.
- GoBGP integration with BFD flap dampening (RFC 5882 Section 3.2).
- Prometheus metrics collector and Grafana dashboard.
- systemd integration (Type=notify, watchdog, SIGHUP hot reload).
- YAML configuration with environment variable overlay (koanf/v2).
- 4-peer interoperability test framework (FRR, BIRD3, aiobfd, Thoro/bfd).
- Debian and RPM packages via GoReleaser nfpms.
- Docker image published to ghcr.io/dantte-lp/gobfd.
- CI pipeline: build, test, lint, govulncheck, buf lint/breaking.
- Bilingual documentation (English and Russian).

[Unreleased]: https://github.com/dantte-lp/gobfd/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/dantte-lp/gobfd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dantte-lp/gobfd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dantte-lp/gobfd/releases/tag/v0.1.0
