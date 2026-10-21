# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Bounded Linux interface-event reconciliation: affected sources become pending
  without releasing old claims and retry automatically in the retained generation.
  Notification loss requests an inventory resync. The non-Linux stub provides no
  automatic recovery; absent startup listener/overlay capabilities remain a limit.

- Live FRR/BIRD timer reload qualification in the testcontainers BASE topology:
  `300→100→500→300 ms` and RX0 with generation receipts, Poll/Final packet
  windows, RFC 5880 jitter bounds, unchanged session identity in nonzero phases
  and baseline restoration. Selective-Final loss, crossed Poll and remote Demand
  remain open.

- RFC 9764 padded PDU targets in the public API, session status and
  `gobfdctl session add --padded-pdu-size`. API/CLI zero disables padding;
  YAML session zero inherits the global default. Shared bounds apply before
  narrowing the API value, and padded API senders enable IPv4 DF. RFC 9764 remains partial;
  path MTU and peer-originated padding qualification retain separate scope.

- Control delivery outcomes distinguish demux, admission, owner commit and
  terminal loss. Header reuse cannot alter queued packets; stop closes admission
  and counts abandoned RX/control work once without acknowledging abandoned
  AdminDown. A fixed process-lifetime ledger appears in ListSessions and
  `gobfd_bfd_delivery_outcomes_total`; existing session drop/auth counters reach
  Get/List/Control Watch and Prometheus. Kernel receive overflow remains
  separately tracked; this is development scope.

- Echo RX returns typed full/closed admission outcomes and accounts owner
  commit/stop abandonment. Separate fixed ledger rows cover Echo read/parser/TTL,
  all raw notification writers, stale/accepted/abandoned dispatch, per-recipient
  Watch queued/gapped originals and legacy delivery. Private unsolicited cleanup
  survives raw queue overflow using a coalesced failure hint and one lazy parked
  worker/reused timer per retained physical entry. Existing attempt, AdminDown,
  shared-claim and identity fences remain. Kernel overflow and RFC/stable
  qualification remain separate work.

- Public Control/Echo raw notification loss invalidates live revisioned
  subscribers through a bounded coalesced GAP and authoritative resnapshot,
  without another owner edge or reconnect. Older queued status cannot replace
  the resnapshot in shared Watch clients or GoBGP. Recovery markers do not add
  original-delivery counters or synthetic Micro actions. Private unsolicited
  cleanup and legacy best-effort delivery retain their existing boundaries;
  full RFC, stable and soak qualification remain separate work.

- Control-session Watch publishes registration before the owner starts,
  including passive sessions that stay Down, and freezes full API status for
  additions, observations and removals. Configuration, timers and counters are
  sampled at publication; `observation_version` still covers the causal tuple.
  Zero/false values and absent timestamps replace prior client status.

- Echo Watch adds initial and post-sync Down rows, frozen owner status and
  synchronous versioned deletion for API/config removal and rollback. Control
  and Echo share an observation clock and opaque owner identities; mixed
  snapshots use one registry/revision boundary. Typed Echo RPCs and Control
  List/Get/GoBGP snapshots keep their existing scope. Same-peer mixed sessions
  require explicit companion selectors. RFC 9747 remains preview.

- RFC 9468 owner-local transmission stop after natural failure or an independent
  setup budget, with learned discriminator/authentication lifetimes preserved.
  Final replies and local AdminDown retain RFC 5880 behavior; configured/API
  claims keep ordinary passive operation. Cleanup is qualified by a local
  attempt identity and cannot retire a later attempt. Unsolicited mode remains
  an unsafe preview.

- RFC 9468 interface-bound IPv4/IPv6 single-hop wildcard listeners accept
  unsolicited packets without a configured session on the destination address.
  Configured listeners retain their peer lookup; unmatched ingress indices
  resolve only through the enabled-interface startup snapshot. Empty enabled
  interface names are rejected. Unsolicited mode remains an unsafe preview.

- Negotiated AdminDown drain with owner commit, successful-write confirmation,
  retry through the existing TX timer and a monotonically extended notification
  window. Short budgets return typed `DrainIncompleteError`; shutdown uses one 10s
  BFD budget instead of the fixed 2s sleep. Owning UDP leases interrupt blocked
  writes and join callbacks before owner completion and source-port release.
  Local SIGTERM capture qualified repeated AdminDown writes to FRR and BIRD and
  a notification window of at least three seconds, with sampled BGP uptime/routes
  unchanged. Ordinary `SetAdminDown` remains commit-only; peer receipt remains
  a separate boundary. See [RFC compliance](docs/en/08-rfc-compliance.md).

- Final Control-session claim release drains the captured session before
  cancel/join and sender release. Reconciliation and rollback share one parallel
  ten-second budget; earlier caller cancellation reports incomplete drain and
  still releases resources. Non-final claims preserve wire state. Unsolicited
  failure cleanup skips drain per RFC 9468 Section 2 and checks release errors;
  API deletion preserves deadline/cancel codes.
  `Manager.Close()` keeps its raw cleanup after the daemon's global drain.

- Causal session observations carry a coherent local state/diagnostic pair,
  known remote state/diagnostic, both C-bits, cause and per-row
  `observation_version` through snapshots, Watch and CLI. C-bit reporting
  retains the `hard-fail` policy; it does not implement graceful restart.
  GoBGP preserves local/remote administrative intent across retries and
  gap resync, and checks current session identity and policy before disable.
  Control and Echo Watch rows carry full sampled status
  (`gobfd-qj0.8.2.1.3.8`, `gobfd-qj0.8.2.1.3.10`).

- GoBGP per-configured-peer metrics for effective decayed flap penalty,
  recovery suppression and its maximum bound, and pending desired Up/Down
  actions with their next retry/recheck timestamp (`gobfd-qj0.8.2.4.7`).
  Accepted reloads replace the peer allowlist; scrapes do not change routing
  decisions or create labels from API or unsolicited sessions.
- Versioned session snapshots and gap-aware Watch streams
  (`gobfd-qj0.8.2.1.3.2`; H-29, M-08). `bfd.v1` adds `revision` to
  `ListSessionsResponse`, `GetSessionResponse` and
  `WatchSessionEventsResponse`, and the event types `GAP` and
  `SYNC_COMPLETE`. `WatchSessionEvents` subscribes before its snapshot,
  closes every snapshot with `SYNC_COMPLETE`, and answers a stream that
  fell behind with `GAP` and a fresh snapshot. State-change events carry
  local address, interface, session type and revision; Get and List report
  `remote_min_rx_interval`. `gobfdctl monitor` prints `Gap`, `SyncComplete`
  and `rev=`.
- Revisioned `SESSION_DELETED` events when the last owner releases a wire
  session through the API, reconciliation or unsolicited cleanup
  (`gobfd-qj0.8.2.1.3.6`). Events carry the removed session's identity and
  final state; releasing a shared claim emits no deletion. Snapshots omit
  removed sessions, and slow subscribers recover through `GAP`. The GoBGP
  handler forgets removed sessions and cancels their pending Down and Up
  actions, including when deletion is recovered from a gap snapshot.

- mTLS for the management TCP listener (`grpc.tls.cert_file`, `key_file`,
  `client_ca_file`; `gobfd-330.2`). Clients must present a certificate from
  the client CA; ALPN offers HTTP/2 and HTTP/1.1; the files are re-read on
  every handshake. The principal is the client certificate URI SAN (SPIFFE)
  or `dns:<name>`. `gobfdctl` gains `--tls-ca`, `--tls-cert` and `--tls-key`;
  the companions read `GOBFD_TLS_CA_FILE`, `GOBFD_TLS_CERT_FILE` and
  `GOBFD_TLS_KEY_FILE`. No default changes.
- Optional management Unix socket (`grpc.socket.path`, `mode`, `group`;
  `gobfd-330.1`). Requests over it carry the kernel-reported peer UID as
  principal, logged as `principal=uid:<n>`. `gobfdctl --addr`,
  `gobfd-haproxy-agent` and `gobfd-exabgp-bridge` accept `unix:///path`
  endpoints as well as `http://` and `https://`. `grpc.addr` may be empty
  when the socket is set. No default changes.
- GoBGP integration failures are visible (GitHub #86): metrics
  `gobfd_gobgp_rpc_errors_total{rpc}` and `gobfd_gobgp_endpoint_compatible`,
  and the named health check `gobfd.gobgp`, which is `NOT_SERVING` until the
  GoBGP v4 preflight passes and after a failed preflight or peer action.
- RFC 9468 unsolicited authentication (Section 6.2): `unsolicited.auth`
  accepts only `meticulous_keyed_sha1`, the strongest mechanism GoBFD
  supports. The packet that would create a session is verified before the
  session exists, and the session signs its replies with the key. Without
  `unsolicited.auth`, packets with the A bit set are refused (RFC 5880
  Section 6.8.6). A change to `unsolicited.auth` requires a restart.
- Documented GoBGP compatibility in the configuration reference: `dev`
  supports the tested GoBGP v4.10.0 only, with upgrade steps from GoBGP v3.
- The GoBGP preflight rejects GoBGP v4 native BFD for peers of configured
  GoBFD sessions, so one peer never has two BFD owners.
- Added a GoBGP capability preflight: startup rejects an endpoint without
  the GoBGP v4 API (for example a GoBGP v3 daemon) with an actionable error,
  and an unreachable endpoint is re-checked in the background until the
  result is conclusive.
- Added a checksum-pinned Debian trixie FRR 10.7.1 peer recipe with local
  amd64 startup/configuration/stop smoke. BASE, BGP and RFC consumers passed local
  interop qualification; remaining Alpine-backed targets stay blocked.
- Added bounded in-place TX/RX timer reloads for solely config-owned base
  sessions, with Poll/Final confirmation, explicit RX zero and automatic
  generation/readiness receipts. Failed transactions remain failed after late
  replies; live FRR/BIRD reload qualification remains pending.
- Added race-enabled VXLAN/Geneve userspace UDP qualification with exact
  tunnel isolation, malformed-packet rejection, and capture-correlatable logs.
- Added the package-internal Go lifecycle owner and declarative systemd
  sysusers/tmpfiles inputs for shellless DEB/RPM lifecycle hooks, without
  changing the four public binaries.

### Changed

- Stable v1 permits the module-only `GO-2026-4736` exception for GoBGP v4.10.0
  by maintainer decision of 2026-10-21 (`gobfd-qj0.8.4.1`) while GHSA/OSV list
  a fixed version at or below the pin and the final candidate's govulncheck
  reports the advisory only at module level (no imported package or reachable
  symbol). The allowlist entry and the dependency inventory decision name owner
  `@dantte-lp` and the expiry (review) date 2026-12-31; the entry is removed
  once the Go vulnerability database records a fixed v4 version.
- `scripts/vuln-audit.go` fails when an allowlist entry matches no scanner
  finding and asks to remove the stale entry.
- The BIRD interoperability peer moved from 3.3.2 to 3.4.0 with the
  checksum-pinned upstream fix `7cf75587` for CVE-2026-112112
  (`gobfd-qj0.8.6.6.2`). The recipe records source and fix hashes and OCI
  labels; a FlowSpec regression test checks that BIRD marks a nonzero IPv6
  destination offset invalid. BASE and BGP+BFD topologies use the same recipe.
- Updated pinned upload/download-artifact Actions to v7.0.2/v8.0.2 for HTTP 429
  retries with `Retry-After`, GoReleaser Action to v7.3.0 for runner tool cache
  reuse, and Scorecard Action to v2.5.0 for the Scorecard v5.6.0 engine refresh
  (`gobfd-qj0.8.6.4.2`).
- Dependency inventory generation now uses an explicit `dev` observation epoch
  and deferred release-line decisions (`gobfd-qj0.8.2.8.7.6`). It records actual
  exact-version license responses, rejects legacy observation reuse, and leaves
  uncollected external reviews and security unverified.
- Updated `dev` module pins to Prometheus `client_golang` v1.25.0 and Moby
  client v0.6.2 (`gobfd-qj0.8.6.2`). Metrics responses now include negotiated
  `Vary` headers; the container integration client fixes cancellation and
  deadlines during HTTP upgrade. The dbus update remains pending its
  2026-10-20 eligibility date.
- **Breaking:** GoBGP flap dampening never holds a BFD Down
  (`gobfd-qj0.8.2.4.1`, H-27, v1 dampening contract). Every qualifying Down
  disables the peer at once; once suppressed, dampening holds only the
  recovery Up, and a Down cancels a held recovery. Validation rejects
  `gobgp.dampening.reuse_threshold <= 0` and `max_suppress_time <= 0`
  (`ErrInvalidDampeningMaxSuppressTime`).
- **Breaking:** `GetSession` by a peer address that matches more than one
  session fails with `FAILED_PRECONDITION` and lists the local
  discriminators, instead of returning the first match (M-07).
- The GoBGP handler resyncs after lost BFD state changes
  (`gobfd-qj0.8.2.4.1`): on a stream gap it rebuilds Down and Up edges from
  a revisioned snapshot and its last seen state per session, so a peer is
  no longer left enabled over a failed path or disabled after recovery.
- **Breaking:** the GoBGP handler acts only on the canonical single-hop or
  multi-hop session of a BGP neighbor (`gobfd-qj0.8.2.4.2`, H-31). Micro-BFD
  member links, VXLAN, Geneve and Echo sessions to the neighbor address no
  longer disable or enable the peer; IPv4-mapped peer addresses are
  unmapped. With `gobgp.enabled`, configuration validation rejects two
  sessions to the same peer address; with a second base session created at
  runtime, BFD Down does not disable the peer and sets `gobfd.gobgp` to
  `NOT_SERVING`, while BFD Up still re-enables a peer GoBFD disabled.
- The GoBGP handler enables only peers it disabled itself
  (`gobfd-qj0.8.2.4.1`). It reads a neighbor's admin state before
  `DisablePeer` and leaves operator-disabled peers alone on BFD Down and Up.
  The record is in memory: after a restart, peers GoBFD had disabled stay
  disabled until an operator enables them.
- **Breaking:** the flight recorder dump `/debug/flightrecorder` is no
  longer served on the unauthenticated metrics listener
  (`gobfd-qj0.8.2.3.2`). With the new `grpc.flight_recorder: true` it is
  served on the management server to callers with a principal (Unix socket
  or an mTLS identity allowed by `grpc.mutating_identities`); anonymous
  loopback callers get `403`. The recorder still runs for the dump on
  shutdown.
- API session claims belong to the caller's principal (`gobfd-330.4`,
  GitHub #87): the Unix peer UID, the mTLS identity, or `local` for anonymous
  loopback callers under `grpc.unsafe_loopback_mutations`. `DeleteSession`
  by another principal returns `PermissionDenied` and is logged; two
  principals that add the same session share it. New
  `grpc.mutating_identities` (requires `grpc.tls`) limits mutating RPCs to
  the listed mTLS identities; other mTLS clients can only read.
- **Breaking:** the management API defaults to the Unix socket
  `/run/gobfd/gobfd.sock` and opens no TCP listener (`gobfd-330.3`,
  GitHub #87). Plaintext `grpc.addr` on a non-loopback or wildcard address
  fails startup; use loopback or `grpc.tls`. Anonymous loopback TCP clients
  can read but get `PermissionDenied` for `AddSession`, `DeleteSession`,
  `AddEchoSession`, `DeleteEchoSession`, `AddMicroBFDGroup` and
  `DeleteMicroBFDGroup` unless `grpc.unsafe_loopback_mutations: true`, which
  is logged and makes the new `gobfd.management` health check `NOT_SERVING`.
  `gobfdctl --addr`, `gobfd-haproxy-agent` and `gobfd-exabgp-bridge` default
  to `unix:///run/gobfd/gobfd.sock`. Packages add group `gobfd-admin`
  (`gobfd.sysusers`), and the systemd unit gains `RuntimeDirectory=gobfd` and
  `SupplementaryGroups=gobfd-admin`; the packaged configuration sets the
  socket group `gobfd-admin`. Container images create `/run/gobfd` and drop
  `EXPOSE 50051`. The Kubernetes example uses the socket with an exec
  readiness probe. Migration: bind TCP to loopback or add mTLS, move mutating
  clients to the socket and add operators to `gobfd-admin`.
- **Breaking:** listeners without authentication default to loopback
  (GitHub #87). `grpc.addr` defaults to `127.0.0.1:50052` and
  `metrics.addr` to `127.0.0.1:9100`, also in `configs/gobfd.example.yml`,
  which packages install as `/etc/gobfd/gobfd.yml`. Port 50051 is the gobgpd
  default and the default `gobgp.addr`, so GoBFD and GoBGP now start side by
  side with defaults, and the API default matches the default of
  `gobfd-haproxy-agent` and `gobfd-exabgp-bridge`; `gobfdctl --addr` defaults
  to `127.0.0.1:50052`. `gobfd-haproxy-agent` binds agent-check listeners to
  `127.0.0.1` unless the new `listen_addr` key names another address.
  Startup logs a warning for an API or metrics listener reachable beyond
  loopback. API authentication and the Unix-socket default of the v1 design
  follow in `gobfd-330`.
- **Breaking for Go consumers of `pkg/bfdpb`:** ConnectRPC moved to
  connect-go v2.0.0 and grpchealth v2.0.0. `bfdv1connect` is regenerated
  with `protoc-gen-connect-go` v2.0.0: clients take a `*connect.Client`
  (`connect.NewClient(connecthttp.NewTransport(httpClient, baseURL))`) and
  handlers register on a `*connect.Server`. The `bfd.v1` wire protocol is
  unchanged; a test pins grpc-go, `grpc.health.v1`, Connect and gRPC-Web calls.
  The daemon, `gobfdctl` and both bridges now apply the connect-go v2
  default 4 MiB per-message read limit instead of no limit; one
  `ListSessions` response holds about 28,000 sessions at 147 bytes each.
  Logging and recovery interceptors now also cover streaming calls; a
  client closing `WatchSessionEvents` logs `rpc canceled` at Info.
- **Breaking:** RFC 9468 unsolicited admission is bounded and checked
  against the connected subnet (audit findings H-17, H-18). A packet creates
  a session only when it arrives single-hop with TTL 255 (not on the
  multi-hop listener), its source belongs to the subnets of the local address
  that received it, is inside `allowed_prefixes` when set and is not the
  local address; an unnumbered receive path is rejected. `max_sessions` must
  be >= 1 when `unsolicited.enabled` is true (configuration validation
  rejects 0, which used to mean unlimited), and new sessions are admitted at
  up to 10 per second with a burst of 20.
- **Breaking:** an enabled RFC 9468 unsolicited policy is validated at load
  (audit finding H-19): `session_defaults` intervals must be 1µs to
  2^32-1µs with a nonzero `required_min_rx`, `detect_mult` 1 to 255,
  `cleanup_timeout` >= 0, at least one interface enabled and every
  `allowed_prefixes` entry a valid CIDR. Omitted `session_defaults` now take
  the documented 1s, 1s and 3; they used to be zero, and every auto-created
  session failed.
- Updated GitHub Actions within the release-age rule to peeled commit SHAs:
  `anchore/sbom-action` v0.24.3, `bufbuild/buf-action` v1.6.0,
  `codecov/codecov-action` v7.1.1, `docker/setup-buildx-action` v4.4.1,
  `docker/setup-qemu-action` v4.4.0, `github/codeql-action` v4.38.2 and
  `SonarSource/sonarqube-scan-action` v8.3.0. `dependabot.yml` routes version
  updates to `dev`; GitHub reads it from the default branch only, so it takes
  effect once it reaches `master`. Security updates stay on the default branch.
- Observability peers run on Debian trixie: Prometheus v3.15.0 and Grafana
  13.2.4 (eight CVE fixes in 13.2.1-13.2.3 and four in 13.2.4) from
  checksum-pinned upstream archives in `test/interop/prometheus` and
  `test/interop/grafana`, and FRR from the shared trixie recipe, replacing
  the Grafana Alpine, Prometheus BusyBox and FRR Alpine images. Grafana starts
  through an exec-form entrypoint without the upstream shell wrapper.
- The Kubernetes example runs GoBGP v4.10.0 as `gobgp:local`, built from
  the shared recipe and imported into k3s by `integrationctl`, instead of
  `docker.io/jauderho/gobgp:v3.37.0`. No repository path uses that image any
  more. The example has not been run on a live k3s cluster.
- The Containerlab GoBFD image builds its GoBGP sidecar as v4.10.0 from the
  shared source pin instead of copying it from
  `docker.io/jauderho/gobgp:v3.37.0`; a contract test keeps the pins equal.
  Vendor NOS interoperability on GoBGP v4 is not qualified yet.
- Refreshed base images: `debian:trixie-slim` index `a29215f6` (Debian 13.7,
  `trixie-20261005`) for every first-party recipe, `haproxy:3.4.6-trixie`
  (3.4.4-3.4.6 bug fixes, including BUG/MAJOR QUIC, H3 and HTX fixes) and
  `nginx:1.31.6-trixie` (CVE-2026-90439 HTTP/3 fix). `oraclelinux:10-slim`
  follows from 2026-10-19.
- ExaBGP Anycast uses the shared GoBGP v4.10.0 recipe and a Debian trixie
  ExaBGP 5.0.13 image with `gobfd-exabgp-bridge` instead of
  `docker.io/jauderho/gobgp:v3.37.0` and `ghcr.io/exa-networks/exabgp`. Its
  gate runs through the capped project controller and now fails when the
  route is not announced, withdrawn after a receiver-side BFD failure, or
  restored. The previous scenario paused the GoBFD instance that feeds the
  bridge and only logged warnings.
- **Breaking:** the GoBGP integration client moved from
  `github.com/osrg/gobgp/v3` v3.37.0 to `github.com/osrg/gobgp/v4` v4.10.0.
  GoBGP v3 `/apipb.GobgpApi` and v4 `/api.GoBgpService` are wire-incompatible:
  GoBFD from `dev` requires a GoBGP v4 daemon. The shared GoBGP peer recipe
  builds v4.10.0 for the BGP, RFC, BGP fast-failover E2E and integration
  consumers; the local amd64 BGP+BFD and BGP fast-failover testcontainers
  gates pass, and the RFC testcontainers gate passes all four race scenarios
  in 162.12 s; manual Compose paths are not rerun.
  ExaBGP anycast, Kubernetes and Containerlab still use GoBGP v3.37.0 images
  and are incompatible until migrated. `release/v0.6` keeps GoBGP v3.37.0.
- BGP fast-failover Compose and testcontainers reuse the shared trixie
  FRR 10.7.1 and GoBGP recipes (v3.37.0 at qualification; v4.10.0 since the
  GoBGP v4 migration above) with capped builds/runtime. Manual
  startup uses the existing ownership-checked controller and forbids implicit
  builds; captures are container-local. The local amd64 race lifecycle passes
  route withdrawal/restoration, packet evidence and exact resource cleanup.
- RFC Compose and testcontainers reuse the shared trixie FRR 10.7.1 and
  GoBGP recipes (v3.37.0 at qualification; now v4.10.0) with explicit
  BFD/BGP daemon selection, bounded
  builds and all eight runtime containers, host revision metadata and
  pre-build image cleanup. The complete local amd64 testcontainers gate passes
  all four race scenarios without skips and verifies owned-resource cleanup;
  RFC 9384 wire subcode 10 and unsolicited expiry remain unqualified.
- BGP Compose and testcontainers definitions use the shared Debian FRR 10.7.1
  recipe with explicit BGP daemons and build/runtime resource caps. Manual
  lifecycle reuses the ownership-checked capped builder. GoBGP (then v3.37.0) and
  ExaBGP 5.0.13 now use checksum-pinned upstream source on trixie, with a
  narrow system-Python runtime exception for external ExaBGP and no Python
  development tooling. Manual and full testcontainers BGP gates pass locally
  on amd64: all four race-enabled tests per path, no skips, verified cleanup.
- Base four-peer Compose and testcontainers definitions build the Debian FRR
  peer locally and bound their runtime resources. Manual BASE builds,
  testcontainers image builds and the invalid-vector generator also have
  CPU/RAM limits; manual startup forbids implicit builds. Both BASE gates
  pass locally on amd64 with explicit RFC skips and verified cleanup;
  the other FRR consumers remain pending.
- Replaced all 16 shell-shebang fixtures embedded in Go tests with guarded
  Go helper processes while preserving argv, streams, exit, cancellation, and
  owned-resource cleanup contracts.
- Replaced the final two tracked nFPM shell hooks with native Go executables.
  Release packaging now rewrites RPM lifecycle program tags, refreshes the
  GoReleaser artifact manifest and checksums, validates the exact package
  matrix, and uploads it to the draft only after validation.
- Reconciled the public roadmap, release-branch design, and executable plan
  with the published immutable v0.6.4 evidence after resolving and accepting
  all three P1 maintenance-review findings in Beads.

### Security

- Updated the first-party Go toolchain from `go1.27.2` to `go1.27.3` in
  `go.mod`, `tools/go.mod`, CI and release workflows, Containerfiles and test
  harnesses. Every `golang:1.27.3-trixie` consumer uses one index digest.
  Fixes reachable
  [GO-2026-6809 / CVE-2026-111943](https://pkg.go.dev/vuln/GO-2026-6809)
  in `crypto/x509` `Certificate.Verify`. The maintainer adopted `go1.27.3`
  on 2026-10-21 as a security exception to the seven-day release-age rule.

### Fixed

- Apply a config timer update immediately outside Up, including AdminDown,
  with its Poll confirming the receipt (RFC 5880 Section 6.8.3). A restore
  admitted after a local zero Required Min RX drove the session Down previously
  waited for Up; a peer obeying the zero stayed silent, so the update failed and
  reconciliation stayed incomplete. A receipt is Applied only when the effective
  tuple equals the confirmed one; an outside-Up Poll without Final fails its
  receipt without blocking later generations; entering Up through a Final keeps
  the one-second TX floor until a recovery Poll (`gobfd-qj0.8.2.1.1.2`).

- Reset `bfd.RemoteMinRxInterval` to 1 microsecond and `bfd.RemoteDemandMode`
  to 0, their RFC 5880 Section 6.8.1 initial values, after one Detection Time
  without packets in every state (Section 6.8.18, `gobfd-qj0.8.2.1.1.2`). When
  the reset changes the TX interval or permission, the next packet is scheduled
  from the previous successful send. A peer that advertised zero or a large
  Required Min RX and went silent no longer holds periodic TX; an Active
  AdminDown drain now sends its Section 6.8.16 notification after one silent
  Detection Time and can complete within the drain budget, while a Passive
  session stays silent. FRR
  10.7.1 cannot advertise zero RX and has no Demand; BIRD 3.4.0 has no Demand
  and its zero RX support is unverified. Wire evidence is tracked in
  `gobfd-qj0.8.2.1.1.2.3`.

- Return `InvalidArgument` for malformed protobuf durations in generic, Echo
  and Micro-BFD requests, core-rejected client intervals and duplicate Micro-BFD
  members (`gobfd-qj0.8.2.1.3.5`). Omitted generic/Micro intervals keep `1s`,
  explicit generic RX zero remains valid, and Echo requires a positive interval.
  Internal Manager errors retain their classification; well-formed Micro-BFD
  member timer validation and application remain under `gobfd-qj0.8.2.2.1` (M09).

- Reject positive configured Control TX/RX intervals below one microsecond with
  `ErrInvalidWireInterval` before session creation or reconciliation
  (`gobfd-qj0.8.2.1.1.3`). Explicit RX zero and fractional microseconds above
  the minimum retain their existing semantics.

- Preserve the learned peer discriminator across valid Down/AdminDown and local
  PathDown transitions (`gobfd-qj0.8.2.1.2.4`). Poll replies and failed-send Final
  retries retain the correct `YourDiscriminator`, including in the passive role;
  the owner detection timer still clears it after one Detection Time of silence.

- Clear `RemoteDiscr` after one Detection Time in every session state
  (`gobfd-qj0.8.2.1.2.3`), independently of two-Detection-Time authentication
  sequence expiry. Preserve the last observed remote administrative facts and
  causal version when only the wire discriminator changes. Passive sessions
  with an unknown discriminator retain pending Final replies without sending,
  including direct FSM sends and retries.

- AdminDown commits exclusively in the session owner loop and returns a
  context-bounded commit receipt, including for pre-Run and full-queue requests
  (`gobfd-qj0.8.2.1.3.3`, H-09 prerequisite). `Run` has one owner for its
  lifetime. Manager drain fans out to captured session identities outside its
  locks and joins errors; shutdown preserves those errors. Receipts precede TX
  and do not prove delivery. H-12 retries, negotiated drain and bounded I/O
  remain open; the fixed two-second notification window is unchanged.

- Verify Keyed MD5/SHA1 over the declared BFD `Length`, including PDU
  extensions and excluding transport padding (`gobfd-qj0.8.2.1.5.2`).
  Independent wire vectors cover all five authentication types. The approved
  v1 configuration policy rejects authenticated padding targets 24/25;
  target 26 retains larger PDUs. YAML defaults and overrides reject numeric
  wraparound, fractions and boolean conversion before decoding. Authenticated
  padded TX is qualified with FRR Simple Password and BIRD Keyed SHA1 at
  128 bytes (`gobfd-qj0.8.2.1.5`), including zero padding and IPv4 DF.
  Peer RX qualification uses ordinary authenticated PDUs; path MTU and
  public API padding remain separate.

- Receive complete BFD transport payloads through 9000 bytes without changing
  the 64-byte codec bound (`gobfd-qj0.8.2.1.5.1`). Linux rejects truncated UDP
  datagrams; listeners reject invalid read sizes and return borrowed buffers.
  Userspace VXLAN/Geneve include maximum supported framing around the same
  9000-byte inner payload limit, on RX and TX. Authenticated wire ownership
  is preserved; authenticated peer interoperability remains a separate gate.

- Authentication sequence expiry uses monotonic last-valid-receive timing
  (`gobfd-qj0.8.2.1.2.2`, M-04). `AuthSeqKnown` clears at twice the Detection
  Time in every session state, including before verification when RX wins
  over a ready timeout; `RcvAuthSeq` is preserved until successful reception.
  Invalid authentication does not postpone expiry.

- Validate the configured log vocabulary before startup or SIGHUP applies
  changes (`gobfd-8f3.9`): levels accept case-insensitive `debug`, `info`,
  `warn`, `error`; formats require lowercase `json` or `text`. Explicit empty,
  whitespace and unknown values return bounded typed field errors. Invalid
  reloads preserve generations, health, log level, sessions and sender resources.

- Wire `socket.read_buffer_size` / `socket.write_buffer_size` into control,
  Echo, Micro-BFD and unsolicited UDP paths, and duplex userspace VXLAN/Geneve
  sockets (`gobfd-8f3.7`). Zero preserves OS defaults; invalid nonnegative integer values fail
  configuration validation with bounded field errors. Startup buffer policy
  also covers later-created senders and remains immutable across SIGHUP.

- Correct the socket buffer reference (`gobfd-8f3`): `read_buffer_size` also
  applies to the interface-bound IPv4 and IPv6 wildcard single-hop listeners
  opened for each enabled unsolicited interface. The reference previously said
  unsolicited reception reused the existing control listeners.

- Configuration rejects conflicting interfaces for one control listener
  (parsed local address and single-hop/multi-hop type), or one enabled Echo
  listener (parsed local address), regardless of declaration order
  (`gobfd-8f3.8`). Typed errors identify only the bounded configuration field;
  listener ownership is unchanged.
- Configuration rejects detection multiplier overflow and fractional or boolean
  values before weak decoding for base, Echo, VXLAN, Geneve and Micro-BFD fields.

- Authentication key stores reject duplicate key IDs, including identical
  rotation keys, so transmit and receive key selection cannot disagree.

- HAProxy and ExaBGP companions reconnect Watch after errors and EOF, invalidate
  state on disconnect or `GAP`, and commit snapshots at `SYNC_COMPLETE`.
  Session deletion withdraws health and routes. Optional local/interface/type
  selectors separate same-peer sessions; ambiguous matches fail closed.
  ExaBGP reconciles routes across reconnects and process restarts and exits on
  failed or short stdout writes. HAProxy backend ports must be unique.

- `AddSession` returns the same Manager snapshot fields as Get and List,
  including the initial `remote_min_rx_interval`.

- The production Compose stack shares the host network so Prometheus can
  scrape GoBFD's loopback metrics and Grafana can reach its Prometheus data
  source (`gobfd-6de`). Prometheus and Grafana now bind only to host loopback
  on ports 9090 and 3000; remote access requires a tunnel or reverse proxy.
- The remaining RFC 5882 Section 4.3 (Graceful Restart) citations for the
  GoBGP reaction in code, configuration examples and the RFC compliance
  matrix now cite Section 10.2 (Interactions with BGP) (H-27).
- RFC 9468 passive sessions reply from the local address and interface that
  received the triggering packet, each with its own RFC 5881 source port from
  the daemon's shared allocator, for IPv4 and IPv6 (audit finding C-07). The
  shared IPv4 wildcard sender is gone; an IPv6 link-local reply is scoped to
  the receiving interface.
- `MarshalControlPacket` validates the authentication section before it
  writes any byte (audit finding H-03). The A bit and the section must
  agree; Auth Len must equal 24 for Keyed MD5, 28 for Keyed SHA1 and 3 plus
  the password length for Simple Password; digests must be 16 or 20 bytes,
  passwords 1 to 16 bytes; reserved and unknown auth types are rejected
  (RFC 5880 Sections 4.2-4.4). A rejected packet leaves the buffer
  unchanged; marshal stays at 0 allocations.
- The GoBGP handler keeps the newest unapplied DisablePeer or EnablePeer
  action for each peer. Transient GoBGP failures are retried with backoff
  from 1 s to 30 s without a new BFD event; InvalidArgument, Unimplemented,
  PermissionDenied, Unauthenticated and a closed client are not retried. A
  Down held by flap dampening is applied once suppression lifts (RFC 5882
  Section 3.1). A pending Down is dropped when the session goes AdminDown
  (RFC 5882 Section 4.2); a pending Up is kept on AdminDown. Removing the
  session cancels both pending directions. Handler
  comments no longer attribute the BGP reaction to RFC 5882 Section 4.3.
- Unauthenticated cached Control packet construction no longer allocates a
  packet object per transmission through the unused authentication branch.
- A peer Final cannot complete a local Poll before a Poll packet is
  successfully sent. Failed sends and crossed Final-only replies do not
  confirm transmission, and a new sequence cannot reuse an old confirmation.
- The slow-to-fast Up transition initiates a local Poll when the advertised
  transmit interval decreases below one second, retaining existing Final retry
  and P/F exclusion without adding an extra Poll-only transmission.
- Initial base BFD sessions preserve an explicit zero receive interval in YAML
  and the generic API, while omitted fields retain their defaults. Zero requests
  peer periodic-control suppression without disabling the local transmitter.
- Peer receive-interval changes now reschedule control transmission from the
  last successful send. Periodic TX stops for zero peer receive intervals or
  active remote Demand without a local Poll; Final replies remain retryable.
- Final replies retain their pending state through signing, encoding, and
  send failures. Only a successful send clears it; Final suppresses Poll
  without cancelling the local Poll sequence, and retries bypass periodic
  transmission suppression.
- Overlay listeners release their allocated inner source port exactly once on
  close. VXLAN/Geneve sends and receives honor context cancellation without
  invalidating the shared listener or subsequent operations.
- Updated `golang.org/x/crypto` to `v0.56.0` in the runtime and tools module
  graphs, fixing the reachable SSH denial-of-service vulnerabilities
  GO-2026-6354 and GO-2026-6355.
- Updated the isolated tools graph to `github.com/ulikunitz/xz v0.5.16` and
  `github.com/klauspost/compress v1.18.7`, removing GO-2025-3922 and
  GO-2026-5841 from release packaging dependencies.
- Release notes now include every maintenance entry since the previous
  published stable release in the same line, plus tag-bound changelog and
  comparison links. Release closeout also requires the accepted stable history
  and bilingual changelogs to reach `master` before completion.
- Updated the first-party Go toolchain from `go1.27.0` to `go1.27.2` and
  `golang.org/x/net` from `v0.58.0` to `v0.61.0` in the runtime and tools
  module graphs, fixing twelve reachable advisories: GO-2026-6599, 6600, 6603,
  6604, 6605, 6607, 6608, 6610, 6611, 6612, 6613 and 6617. Every
  `golang:1.27.2-trixie` consumer uses one index digest. GO-2026-4736 is
  handled by the GoBGP v4 migration and its module-level allowlist entry.
- Updated runtime modules within the seven-day release-age rule:
  `google.golang.org/grpc` v1.84.0, `github.com/knadh/koanf/v2` v2.3.8,
  `github.com/prometheus/client_model` v0.6.3, `github.com/moby/moby/api`
  v1.56.1, `github.com/moby/moby/client` v0.6.1 and the protovalidate
  generated module 20260825. client_golang v1.25.0, moby client v0.6.2 and dbus
  v5.2.4 follow from their eligibility dates.
- Updated developer and release tools within the release-age rule:
  golangci-lint v2.14.0 (92 linters, no new findings), gosec v2.29.0,
  govulncheck v1.8.0, osv-scanner v2.6.0, buf v1.73.0, the checksum-pinned
  Docker Compose provider 5.6.0, syft v1.54.1, trivy v0.75.0 and GoReleaser
  v2.18.2. govulncheck v1.9.0, osv-scanner v2.7.0 and GoReleaser v2.18.3
  follow from their eligibility dates.

## [0.6.4] - 2026-08-27

### Fixed

- Keep the GoReleaser changelog pipe enabled so `--release-notes` loads the
  exact dated CHANGELOG section into the draft body before the fail-closed
  draft verification. The immutable `v0.6.3` tag and its complete but
  unpublished draft remain the failed recovery cut; `v0.6.4` fixes forward
  without moving or reusing that tag.

## [0.6.3] - 2026-08-27

> Release status: the immutable tag, complete draft, assets, and versioned OCI
> images were created, but the draft remained unpublished because its release
> notes were empty. The failed cut is preserved and superseded by the published
> `v0.6.4` release.

### Fixed

- Release qualification now writes its four smoke-build binaries outside the
  checkout and installs the checksum-pinned Podman Compose and uv test tools in
  both full-race jobs, while strict Go lint remains in its digest-pinned Debian
  Trixie container. This prevents generated binaries and missing runner CLIs
  from invalidating the release gate before artifact creation. The immutable
  `v0.6.2` tag remains the failed first cut without a GitHub Release or assets;
  `v0.6.3` is its fix-forward recovery release.

## [0.6.2] - 2026-08-27

> Release status: the immutable tag was created, but workflow run `33083358370`
> failed before draft or asset creation. The failed cut is preserved; the
> subsequent `v0.6.3` recovery cut also remained unpublished, and both are
> superseded by the published `v0.6.4` release.

### Added

- Go 1.27 compatibility coverage for duplicate JSON object members, invalid
  UTF-8 replacement, the HTTP header-value boundary, and real Podman build
  contexts.

### Changed

- Quick Start uses the tracked `configs/gobfd.example.yml`, and the EN/RU
  roadmap remains In progress as a dated projection of Beads. The legacy S12
  status was reconciled to record the delivered Echo/Micro CRUD and absent
  Overlay API instead of reporting the whole sprint as pending.
- Graceful-shutdown documentation now matches the fixed two-second drain in
  code and describes AdminDown delivery as best effort rather than guaranteed.
- Public RFC and performance documentation now reports RFC 9384 wire subcode
  10 as not implemented with GoBGP v3, marks incomplete RFC capabilities as
  partial or preview, and limits allocation and throughput statements to the
  exact benchmark stages measured. Enqueue-only RX and socket-free TX
  benchmarks now use stage-specific names.
- The native OVS LAG backend now uses active
  `github.com/ovn-kubernetes/libovsdb` v0.8.1 instead of the archived
  `ovn-org` module, preserving the existing RFC 7130 transaction contract.
- Repository Markdown and PR-title quality gates now use a stdlib-only Go 1.27
  checker, while spelling uses uv-locked codespell 2.4.3. Node.js, npm,
  markdownlint-cli2, CSpell, and commitlint are removed from first-party
  development and CI quality tooling.
- Alpine Linux is no longer used by first-party build, runtime, benchmark,
  smoke, interop, or integration images. Those paths now use immutable Debian
  trixie OCI indexes; the only other permitted OS baseline is Oracle Linux 10.
- `make int-bgp-failover` now runs the Go 1.27 testcontainers Podman gate for
  GoBFD, GoBGP v3.37.0, and FRR 10.7.0, retaining packet/container evidence and
  proving exact cleanup; the operational Compose example remains available.
- The optional BFD invalid-vector gate now uses repository Go packet codecs for
  its preserved 18 groups and 1055 invalid packets. The legacy `scapy` service
  identifier remains for topology compatibility, but Scapy and its Python
  runtime are no longer dependencies.
- Cross-implementation benchmark HTML is now rendered by a tested Go 1.27
  command instead of an inline Python heredoc. Inputs are bounded and
  identity-checked, metadata is exactly one JSON document, and the mode-0600
  report is published only by same-directory atomic replacement.
- Containerlab vendor-image bootstrap orchestration now runs through a bounded
  Go 1.27 command. Python remains only for VyOS ISO/rootfs preparation and
  operator-supplied Arista/Cisco archive import behind three explicit frozen-uv
  subcommands.
- The mandatory four-peer BFD interoperability matrix now uses FRR 10.7.0,
  BIRD 3.3.2, immutable Holo 0.9.0, and Thoro/bfd. Holo is configured by a
  healthy-gated one-shot YANG loader, and lifecycle tests require fresh packet
  and daemon-state evidence for failure and recovery.
- The first-party toolchain baseline is Go 1.27.0 across `go.mod`, GitHub
  Actions, development and release images, and test harness builders. Compiler
  images are pinned to immutable multi-platform OCI indexes. Go 1.27 includes
  the `net/http` `ReadHeaderTimeout` fix also released in Go 1.26.7.
- Timer tests use `testing/synctest.Sleep`, and HTTP tests use the Go 1.27
  `httptest.NewTestServer(t, handler)` helper where no custom listener is
  required.
- `golangci-lint` keeps the strict linter set under Go 1.27 through the
  upstream `honnef.co/go/tools` compatibility update; `golangci-lint` is now
  `v2.13.1`, stable Staticcheck 2026.2 is used, and `staticcheck` remains
  enabled.
- Runtime modules are refreshed to the current compatible releases, including
  ConnectRPC `v1.20.0`, gRPC health `v1.5.0`, koanf `v2.3.6`, Prometheus client
  `v1.24.1`, console `v0.5.0`, and protobuf `v1.36.12`. First-party YAML
  imports now use the maintained `go.yaml.in/yaml/v3 v3.0.5` module path.
- Development tooling is reproducibly pinned: Buf `v1.72.0`, gopls `v0.23.0`,
  govulncheck `v1.7.0`, gotestsum `v1.13.0`, and uv-managed Python lint, type,
  security, YAML, and report tools. One
  Python 3.14.7 `uv.lock` replaces pip bootstrap and independent tool
  environments across CI and development images.
  Containerlab installs use verified `v0.79.0` release archives, and CI uses
  Docker Compose `v5.5.0` as the checksum-pinned Go provider for
  `podman compose`, Syft `v1.51.0`, and GoReleaser `v2.18.0`.
- The isolated lint-tool graph selects the MIT-licensed
  `github.com/tenntenn/text/transform` revision without changing runtime
  dependencies. Dependabot now monitors both the root and `tools/` Go modules.
  CI publishes distinct runtime and tools vulnerability JSON and CycloneDX SBOM
  artifacts.
- The machine-readable dependency inventory now binds all 40 registry packages
  from `uv.lock` to exact PyPI release and artifact-hash license evidence, and
  records per-version license evidence for all 21 declared tools. Build commit
  and source-checksum variables are no longer misclassified as tools. Each
  adopted or retained record now carries an explicit, review-dated exception
  for every unresolved release-blocking assessment.
- GitHub Actions are updated to their current releases and immutable commit
  SHAs. Interop and integration peers now use digest-pinned FRR `10.7.0`,
  GoBGP `v3.37.0`, ExaBGP `5.0.13`, Prometheus `v3.14.0`, and Grafana `13.2.0`.
  GoBGP v4 remains a separate compatibility migration for v1.0.0.
- OSV Scanner is updated from `v2.3.5` to `v2.5.1`, whose Go analysis stack
  understands Go 1.27 syntax.
- The temporary GoBGP v3 NEXT_HOP denial-of-service exception is renewed only
  through 2026-09-30; the v4 migration remains the tracked remediation.
- Security fixes update gRPC to `v1.83.2`, `moby/go-archive` to `v0.3.3`,
  `klauspost/compress` to `v1.19.2`, and `x/mod` to `v0.40.0`. The module-only
  `x/crypto/openpgp` advisory is time-bounded because that package is not in
  the build graph.

### Fixed

- Independent v0.6.2 review follow-ups now guard the vendor Containerlab
  lifecycle with a lock, run labels, and exact-ID receipts; create core and
  BGP evidence in private unique directories; read the digest-pinned FRR image
  from the tracked topology; require all renamed benchmark stages on both PR
  sides; remove unsupported scheduler-isolation and GC-pause claims; and keep
  RFC support status authoritative in the compliance matrix.
- Bound repeated HTTP header values on both the metrics and ConnectRPC servers
  and verify the parser rejects the 129th value with HTTP 431.
- Make vulnerability allowlist matching fail closed on scanner, package, and
  reachable-symbol mismatches; module-only exceptions cannot admit affected
  package imports.
- Removed the obsolete `goroutineleakprofile` and `noswissmap` experiments,
  which no longer exist in Go 1.27.

### Removed

- Removed the abandoned aiobfd peer, its bitstring dependency, and the
  repository-owned Python benchmark service and comparisons. Cross-language
  benchmark reports now contain GoBFD, FRR-style C, and BIRD-style C results.
- `.archive/` directory removed from the repository. Sprint planning
  records, the cleanup plan, and promo drafts are no longer tracked.
  The directory remains in `.gitignore` so a maintainer can keep
  scratch artifacts locally without re-introducing them to git.
  Commit history (`git log -- .archive/`) preserves the prior content
  for audit.

## [0.6.1] - 2026-05-09

### Fixed

- Bump pinned `github.com/golangci/golangci-lint/v2` from `v2.11.4` to
  `v2.12.1` in `go.mod` so the release workflow (`go tool golangci-lint`)
  recognizes the `gomodguard_v2` linter introduced for `v0.6.0`. Without
  this bump, the release CI on tag `v0.6.0` failed at the lint step.

## [0.6.0] - 2026-05-09

### Added

- gRPC `EchoService` (RFC 9747) with `AddEchoSession`,
  `DeleteEchoSession`, and `ListEchoSessions`. Server enforces
  positive `TxInterval` and `DetectMultiplier` in `[1, 255]`.
  Registered on the daemon's HTTP/2 ConnectRPC mux at
  `/bfd.v1.EchoService/...` and advertised via `grpc.health.v1`.
- gRPC `MicroBFDService` (RFC 7130) with `AddMicroBFDGroup`,
  `DeleteMicroBFDGroup`, and `ListMicroBFDGroups`. Server enforces
  the RFC 7130 invariant `1 <= min_active_links <= len(member_links)`.
- `gobfdctl echo {add, list, delete}` and
  `gobfdctl micro {add, list, delete}` subcommands with table, JSON,
  and YAML output formats.
- Roadmap document at `docs/en/roadmap.md` describing the S12-S20
  waterfall plan toward `v1.0.0`.
- End-to-end test targets `make e2e-core`, `make e2e-routing`, `make e2e-rfc`,
  `make e2e-overlay`, `make e2e-linux`, and `make e2e-vendor` with
  Podman-only execution, packet captures, and standardized artifacts.
- `make e2e-help` and `make gopls-check` for E2E build-tag coverage.
- `make interop-clab` Podman-only path for vendor NOS interop with Arista
  cEOS and FRRouting IPv4/IPv6 BFD evidence.
- Public-image vendor profile set for Arista cEOS, Nokia SR Linux, SONiC-VS,
  and VyOS. Cisco XRd remains an operator-provided optional profile.
- Checkout-safe development Compose automation: generated container names,
  `COMPOSE_PROJECT_NAME` scoping, `make dev-project`, and `make dev-ps`.
- GoReleaser snapshot pipeline produces `linux/amd64` and `linux/arm64`
  OCI images on Debian trixie and Oracle Linux 10, deb/rpm packages, and
  Syft SBOMs.
- Vendored `buf/validate/validate.proto` from `bufbuild/protovalidate`
  `v1.2.0` so `buf lint` runs without Buf Schema Registry connectivity.
- Reusable GitHub Actions Podman installer pinning `podman-compose` `1.5.0`.
- E2E evidence GitHub Actions workflow with PR-safe, nightly, and manual
  vendor gates and 30-day report artifact retention.

### Changed

- Documentation reorganized: canonical reference docs under `docs/en/01..16-*.md`
  only; sprint planning records moved to `.archive/sprints/`; architecture
  decision records introduced under `docs/en/adr/`; auxiliary references
  under `docs/en/reference/`. RU mirrors EN file-for-file.
- Header template unified across `docs/en/12..16-*.md`: badge row,
  declarative summary, level-2 Table of Contents heading.
- Per-package `doc.go` files added across `internal/` packages (`bfd`,
  `gobgp`, `sdnotify`, `version`); existing inline package comments
  removed from logic files.
- `.golangci.yml` migrated from deprecated `gomodguard` to `gomodguard_v2`
  with the new module-list schema.

### Security

- Bump Go toolchain to `1.26.3` across `go.mod`, dev container, runtime
  containers, and GitHub Actions workflows. Closes
  [GO-2026-4986 / CVE-2026-39820](https://pkg.go.dev/vuln/GO-2026-4986)
  in `html/template`.

### Fixed

- New `dev-ensure` Make target rebuilds the dev container when its
  bind-mount source no longer matches `$(CURDIR)`. Eliminates
  `crun: getcwd: Operation not permitted` errors that surfaced after
  worktree teardown of a stale dev container.
- `lint-spell` Make target no longer references documents archived
  under `.archive/sprints/`; now lints the canonical `roadmap.md`.
- `lint-yaml` excludes `.archive/` and `.serena/` directories.
- Bump dev container `golangci-lint` to `v2.12.1` to match the host
  configuration introduced with the `gomodguard_v2` migration.
- Linux rtnetlink interface monitor shutdown is now bounded by a receive
  timeout, so cancellation exits deterministically even when closing the
  netlink file descriptor does not interrupt the receive syscall
  immediately.
- Authenticated BFD sessions now serialize the authentication section into
  the cached transmit packet before sending, so declarative RFC 5880 auth
  sessions can establish with peers that require authentication.
- `goconst` violations resolved by extracting `single-hop`/`multi-hop`
  literals into `cmd/gobfdctl/commands` constants and `_uuid` into
  `internal/netio.ovsdbUUIDColumn`.
- Stale `//nolint:gosec` directives removed from `internal/bfd/manager.go`,
  `internal/netio/rawsock_linux.go`, `internal/netio/sender.go`, and
  `test/interop-rfc/echo-reflector/main.go`.

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

[Unreleased]: https://github.com/dantte-lp/gobfd/compare/v0.6.4...HEAD
[0.6.4]: https://github.com/dantte-lp/gobfd/compare/v0.6.3...v0.6.4
[0.6.3]: https://github.com/dantte-lp/gobfd/compare/v0.6.2...v0.6.3
[0.6.2]: https://github.com/dantte-lp/gobfd/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/dantte-lp/gobfd/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/dantte-lp/gobfd/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/dantte-lp/gobfd/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/dantte-lp/gobfd/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/dantte-lp/gobfd/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/dantte-lp/gobfd/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/dantte-lp/gobfd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dantte-lp/gobfd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dantte-lp/gobfd/releases/tag/v0.1.0
