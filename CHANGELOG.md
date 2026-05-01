# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.2] - 2026-05-01

### Fixed

- Restored the canonical Apache-2.0 license text so pkg.go.dev can detect the
  module license and display package documentation.

## [0.5.1] - 2026-05-01

### Added

- pkg.go.dev reference badge in README and command package documentation for
  `gobfd`, `gobfdctl`, `gobfd-haproxy-agent`, and `gobfd-exabgp-bridge`.

### Changed

- PR benchmark comparison now targets stable hot-path benchmarks only and uses
  an explicit Go test timeout.

## [0.5.0] - 2026-05-01

### Added

- Repository governance and community-health files:
  `CODE_OF_CONDUCT.md`, `SUPPORT.md`, `GOVERNANCE.md`, `MAINTAINERS.md`,
  `.github/CODEOWNERS`, `.github/pull_request_template.md`, issue forms, and
  `.github/repository-settings.md`.
- Codebase consistency audit in `docs/en/codebase-consistency-audit.md`
  comparing README/docs/API/CLI/config claims against implementation status
  and independent production networking applicability.
- Linux rtnetlink interface monitor for `RTM_NEWLINK` / `RTM_DELLINK` events,
  with immediate BFD `Down` / `Path Down` handling for sessions bound to a
  failed interface.
- S4 Linux netlink vs eBPF research note documenting why rtnetlink is the
  correct default for link-state monitoring.
- Canonical phased implementation plan in `docs/en/implementation-plan.md`
  aligned with Keep a Changelog, SemVer, Conventional Commits, Compose
  Specification, Containerfile, `.containerignore`, and containers.conf.
- Podman-only documentation lint gates: `make lint-md`, `make lint-yaml`,
  `make lint-spell`, `make lint-docs`, and `make lint-commit`.
- Repository-level `.containerignore`, Markdown lint, YAML lint, cspell, and
  commitlint configuration files.
- CI jobs for documentation linting and Conventional Commit validation of pull
  request titles.
- CI documentation spell-check paths now use the canonical `docs/en/` planning
  documents and community-health files.
- `make gopls-check` gate backed by `gopls v0.21.1` in the Podman dev
  container.
- Declarative RFC 5880 authentication wiring for YAML-defined BFD sessions,
  including static key-store validation and API/session snapshots that expose
  the configured auth type.
- gRPC `AddSession` key-management fields for RFC 5880 authentication:
  `auth_type`, `auth_key_id`, and `auth_secret`.
- `gobfdctl session add` authentication flags: `--auth-type`,
  `--auth-key-id`, and `--auth-secret`.
- Public API session type vocabulary for RFC 9747 Echo, RFC 7130 Micro-BFD,
  RFC 8971 VXLAN, and RFC 9521 Geneve sessions.
- Production security policy covering BFD authentication, ConnectRPC exposure,
  GoBGP TLS/localhost boundaries, container privileges, and vulnerability gate
  ownership.
- Linux applicability note for Micro-BFD, VXLAN BFD, and Geneve BFD in
  `docs/en/linux-advanced-bfd-applicability.md`.
- Generic production runbooks in `docs/en/16-production-runbooks.md` and
  `docs/ru/16-production-runbooks.md` covering Kubernetes, BGP failover,
  Prometheus alerts, packet verification, and open production gaps.
- FRR/GoBGP BGP fast-failover runbook with RFC packet checks,
  troubleshooting, and optional public Arista EOS verification notes.
- Micro-BFD actuator hook and guarded `netio.LAGActuator` policy layer for
  Linux LAG enforcement.
- Owner-aware `micro_bfd.actuator` configuration and daemon dry-run wiring for
  kernel bond, OVS, and NetworkManager Micro-BFD enforcement backends.
- Linux kernel-bond Micro-BFD enforcement backend that writes RFC 7130
  remove/add actions through bonding sysfs for explicit `backend: kernel-bond`
  with `owner_policy: allow-external`.
- OVS Micro-BFD enforcement backend that runs `ovs-vsctl del-bond-iface` and
  `ovs-vsctl add-bond-iface` for explicit `backend: ovs` with
  `owner_policy: allow-external`.
- OVSDB API research documenting OVSDB JSON-RPC as the native OVS integration
  path and `libovsdb` as the preferred Go route for the next backend.
- Native OVSDB Micro-BFD enforcement backend for `backend: ovs`, using
  `libovsdb` transactions against `Port.interfaces` and configurable
  `micro_bfd.actuator.ovsdb_endpoint`.
- NetworkManager D-Bus Micro-BFD enforcement backend for `backend:
  networkmanager`, using `GetDeviceByIpIface`, `ActiveConnection`,
  `DeactivateConnection`, `AvailableConnections`, `GetSettings`, and
  `ActivateConnection` to control NM-owned bond port profiles.
- VXLAN/Geneve overlay backend model with explicit `userspace-udp` ownership
  and reserved `kernel`, `ovs`, `ovn`, `cilium`, `calico`, and `nsx` backend names.
- Canonical documentation layout with published English sources in `docs/en/`,
  Russian translations in `docs/ru/`, and only the global index in
  `docs/README.md`.
- Russian translations for S8 planning, consistency audit, Linux advanced BFD,
  Linux netlink/eBPF, and OVSDB API research documents.

### Changed

- Documentation style now uses declarative status tables, official standards,
  RFCs, primary vendor/library references, and no internal validation process
  artifacts in published documents.
- RFC compliance docs, config examples, and code comments now distinguish
  implemented Micro-BFD detection from future Linux bond/team/OVS enforcement,
  and document VXLAN/Geneve userspace socket ownership limits for kernel,
  OVS, Cilium, and NSX dataplanes.
- S7.1 is split into non-destructive actuator config wiring, explicit
  kernel-bond enforcement, transitional OVS CLI fallback, native OVSDB backend,
  and NetworkManager D-Bus backend sprint.
- Overlay sender reconciliation now reuses the runtime VXLAN/Geneve backend
  already serving the receiver, avoiding duplicate binds on UDP 4789/6081.
- `backend: ovs` now selects the native OVSDB implementation; the older
  `OVSLAGBackend` remains available as an explicit CLI fallback type.
- S7 roadmap now targets independent production integration assets instead of
  a site-specific applicability target.
- Kubernetes integration manifests now use consistent app labels, named ports,
  Linux node selection, TCP readiness/liveness probes, and host-network DNS
  policy.
- Observability alert rules now distinguish "no active configured sessions"
  from real Up-to-Down BFD transitions and use transition-count flapping
  detection that matches exported GoBFD metrics.
- `make gopls-check` now scopes diagnostics to the Linux target through
  `go list`, includes project build tags, and fails on any `gopls check`
  diagnostics instead of allowing them to scroll past with exit code 0.
- README RFC status now matches the detailed RFC compliance documents for
  Echo, Micro-BFD, VXLAN, Geneve, Unsolicited BFD, common intervals, and large
  packets.
- `make all` now includes documentation linting; `make verify` is the canonical
  routine gate for build, tests, linters, proto lint, and vulnerability audit.
- Interop Go test Makefile targets now execute through the Podman dev container
  instead of the host Go toolchain.
- Dev container now includes Node.js and Python-based documentation analyzers,
  with Podman socket access fixed via `security_opt: label=disable`.
- CI workflow now uses a workflow-level read-only token policy and named jobs
  aligned with the local quality gates.
- CI and release workflow Go tools now run `gotestsum`, `benchstat`, and
  `golangci-lint` through Go `tool` directives recorded in `go.mod`/`go.sum`;
  Node and Python analyzer installs pin `markdownlint-cli2`, `cspell`,
  `commitlint`, `yamllint`, and `junit2html` and use package-manager controls
  required by supply-chain scanners.
- GitHub Actions pins now track current upstream release tags for checkout,
  cache, setup, CodeQL, GoReleaser, Trivy, gosec, Codecov, Buf, GitHub Script,
  SonarQube, and artifact actions.
- `golangci-lint` now enables 93 validated analyzers under the v2
  configuration model, with `snake_case` JSON/YAML tag policy preserved for
  public API and CLI contracts.
- SonarCloud and Codecov coverage policy now excludes command entry points and
  host-network integration boundaries that are validated by build, lint,
  security, and system/container checks.
- Vulnerability audit now runs `govulncheck v1.3.0`; OSV Scanner remains on
  `v2.3.5` because `v2.3.6` is not usable through `go run` as a versioned tool
  module.
- GoReleaser now publishes immutable release artifacts with Debian `trixie-slim`
  and Oracle Linux `10-slim` OCI image variants for `linux/amd64` and
  `linux/arm64`.
- `gobfdctl` list/show/event formatting now renders advanced session families
  instead of collapsing them to `unknown`.

### Fixed

- Graceful drain now routes `SetAdminDown` through the session control channel
  when the session goroutine is running, keeping the goroutine-confined cached
  state aligned with the atomic state and ensuring the transmitted control
  packet carries `AdminDown` / `DiagAdminDown`.
- RFC 9747 Echo receive path now accepts only looped-back packets with
  TTL/Hop Limit 254 while preserving TTL 255 validation for single-hop BFD.
- RFC interop packet capture now includes UDP 3785 Echo packets.
- Session creation now rejects authentication without an auth key store instead
  of panicking during cached packet signing.
- Hash-auth verification now rejects missing raw wire bytes instead of
  panicking when a legacy/internal caller delivers only the parsed packet.
- Authenticated sessions now reset the receive sequence window after 2x
  Detection Time without valid packets, and failed auth packets no longer
  refresh `LastPacketReceived` or `PacketsReceived`.
- The gRPC `AddSession` path now rejects incomplete or unexpected auth key
  material instead of silently creating an unauthenticated session.
- The gRPC `AddSession` path now rejects recognized transport-specific session
  families until dedicated Echo, Micro-BFD, VXLAN, and Geneve APIs are added.
- Vulnerability allowlist entries now require owner, expiry, reason, and
  mitigation metadata; expired entries fail the audit gate.

## [0.4.0] - 2026-02-24

### Added

- Comprehensive test coverage for `cmd/gobfd/main.go` -- 32+ table-driven tests covering `configSessionToBFD`, `buildUnsolicitedPolicy`, `configEchoToBFD`, `configMicroBFDToBFD`, `buildOverlaySessionConfig`, `loadConfig`, `newLoggerWithLevel`.
- Fuzz tests for overlay codecs: `FuzzVXLANHeader`, `FuzzGeneveHeader`, `FuzzInnerPacket` with round-trip and raw-input variants for untrusted network input.
- Overlay codec benchmarks: `BenchmarkBuildInnerPacket`, `BenchmarkStripInnerPacket`, `BenchmarkVXLAN/GeneveHeaderMarshal/Unmarshal` (0 allocs/op).
- Test coverage for `internal/version` -- `Full()` format, default values.
- Test coverage for `gobfd-haproxy-agent` -- `stateMap` concurrency, `handleAgentCheck` with `net.Pipe()`, `loadConfig`, `envOrDefault`.
- Test coverage for `gobfd-exabgp-bridge` -- `handleStateChange` with stdout capture, `envOrDefault`.
- Session scaling benchmarks: `BenchmarkManagerCreate100/1000Sessions`, `BenchmarkManagerDemux1000Sessions` (O(1) demux verification), `BenchmarkManagerReconcile`.
- Configurable socket buffer tuning via `socket.read_buffer_size` and `socket.write_buffer_size` (default 4 MiB each) for `SO_RCVBUF`/`SO_SNDBUF` on listener and sender sockets.
- `os.Root` sandboxed config file access in `config.Load` and `gobfd-haproxy-agent` `loadConfig` (Go 1.26 path traversal protection).
- `GOEXPERIMENT=goroutineleakprofile` in dev container for goroutine leak profiling at runtime.
- `runtime/trace.FlightRecorder` HTTP endpoint for post-mortem debugging.
- PR benchmark comments in CI via `actions/github-script` for regression visibility.
- `internal/sdnotify` package replacing external `go-systemd` dependency.
- Config, server, netio, and GoBGP integration tests (Sprint 1 quality foundation).

### Changed

- Pinned golangci-lint to `v2.1.6` in CI and release workflows (was `@latest`).
- Added `-race` flag to SonarQube test workflow for data race detection.
- CI benchmarks expanded from `./internal/bfd/` to `./...` to cover overlay codec benchmarks.
- Replaced `errors.As` with Go 1.26 `errors.AsType[T]()` in server tests for type-safe error matching.
- Converted 15 timer-dependent tests to `testing/synctest` for deterministic virtual-time execution.
- Replaced `go-systemd` external dependency with `internal/sdnotify` (zero external deps for systemd notify).

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

[Unreleased]: https://github.com/dantte-lp/gobfd/compare/v0.5.2...HEAD
[0.5.2]: https://github.com/dantte-lp/gobfd/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/dantte-lp/gobfd/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/dantte-lp/gobfd/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/dantte-lp/gobfd/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/dantte-lp/gobfd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dantte-lp/gobfd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dantte-lp/gobfd/releases/tag/v0.1.0
