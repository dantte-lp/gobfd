# Replace aiobfd with Holo in the Interop Matrix

**Status:** Approved for implementation

**Beads:** `gobfd-qj0.8.1.5.3`

## Goal

Remove the abandoned `aiobfd` peer and its `bitstring` dependency from every
active runtime, test, benchmark, report, and documentation surface. Keep the
four-implementation base interop matrix by replacing it with the latest stable
Holo routing suite.

Published changelog entries remain unchanged. The spelling allowlist may retain
`aiobfd` only to validate those historical entries. This migration design also
remains as a decision record after implementation.

## Decision

The fourth peer is Holo `v0.9.0`, consumed as the official immutable image:

```text
ghcr.io/holo-routing/holo-bundle@sha256:5c1f61475b1623b3eab611921f8319fb0a10492ced3f7da05e656418abb5ca4a
```

The selected release is MIT-licensed, implements RFC 5880, RFC 5881,
RFC 5882, and RFC 5883, and includes packet validation for RFC 5880
Section 6.8.6. Its GitHub release provides an official amd64 image and an
in-toto provenance attestation. The base interop job already targets amd64, so
the lack of a multi-architecture manifest does not narrow the existing gate.

A live Podman qualification established a GoBFD-to-Holo session with both
local and remote state `Up`, a negotiated 300 ms transmit interval, and a
900 ms detection time. Stopping Holo changed the GoBFD session to `Down` with
`ControlTimeExpired` after the negotiated detection interval.

## Alternatives

| Candidate | Decision | Reason |
|---|---|---|
| No replacement | Rejected | Reduces implementation diversity and the base matrix from four peers to three. |
| `xdp-bfd` or `bbdd` | Defer to an optional experimental profile | Both are active 2026 kernel-dataplane projects, but neither has a stable release and both require eBPF/XDP-specific privileges. |
| `WLmutou/gobfd` | Rejected | No stable release or maintained daemon image; adoption would require a repository-owned wrapper. |
| `rustybgp` | Rejected | A BGP suite rather than a focused standalone BFD peer. |
| `maghemite` | Rejected | No stable release and deployment assumptions are specific to Oxide infrastructure. |
| Wren or `keepalived-go` | Rejected | Both are new and insufficiently mature for a mandatory compatibility gate. |

Cilium and Calico still track native BFD integration as open work. MetalLB
delegates BFD to FRR. They therefore do not provide another independent peer
for this matrix.

## Topology

The Holo service replaces the current `aiobfd` service at `172.20.0.50`; the
other addresses and peers remain unchanged. The topology continues to contain
GoBFD, FRR, BIRD3, Holo, Thoro/bfd, tshark, and the optional Scapy fuzzer.

Holo runs `holod` with `NET_RAW` and `NET_ADMIN`. A test-specific read-only TOML
configuration enables the gRPC northbound plugin and stdout logging, disables
file logging and the unused gNMI plugin, and retains the image's `holo` runtime
user and database path. This makes daemon diagnostics available through
Compose logs instead of only `/var/log/holod.log`.

The BFD configuration is a separate read-only YANG command file. It defines
interface `eth0` and one IETF BFD single-hop session:

```text
interfaces interface eth0
 type iana-if-type:ethernetCsmacd
 ipv4
!
routing control-plane-protocols control-plane-protocol ietf-bfd-types:bfdv1 main
 bfd ip-sh sessions session eth0 172.20.0.10
  source-addr 172.20.0.50
  local-multiplier 3
  desired-min-tx-interval 300000
  required-min-rx-interval 300000
 !
!
```

Intervals are microseconds in the IETF YANG model. The values therefore match
the existing 300 ms GoBFD session and produce a 900 ms detection interval.

The YANG command file is not passed to `holod -c`; that option accepts TOML
daemon configuration. The topology follows Holo's official containerlab
bootstrap model:

1. The `holo` service starts `holod` with the mounted TOML configuration and a
   healthcheck that observes its gRPC listener.
2. A one-shot `holo-config` service uses the same pinned bundle image, waits for
   `holo` to become healthy, and runs `holo-cli --address http://holo:50051
   --file /etc/holo.startup`.
3. The runner requires `holo-config` to exit successfully before accepting the
   topology as ready.

The one-shot loader has no static address and does not become a fifth peer. On
failure, diagnostics include `holo-config` output, `holo` Compose logs, and
`/tmp/holod.err` from the daemon container.

## Code and Test Changes

The implementation uses a contract-first test:

1. Add a Go test that requires the Holo service, immutable image digest,
   read-only configuration mount, fixed address, capabilities, and BFD session
   values, and rejects active `aiobfd` or `bitstring` topology content.
2. Run the test against the current tree and confirm the expected failure.
3. Replace the service and rename all peer-specific Go and shell test helpers,
   packet filters, discriminator checks, failure-recovery assertions, container
   inventories, and diagnostics from aiobfd to Holo.
4. Run the contract test and tagged interop compilation to green.
5. Run both the legacy shell gate through `make interop` and the authoritative
   tagged Go suite through `make e2e-routing`. Confirm the tagged runner reports
   a non-zero test count and covers handshake, independent-session,
   failure-detection, recovery, discriminator, RFC 5880, and RFC 5881 checks.

Lifecycle assertions do not accept an `Up` packet merely because it exists in
the capture. Before a stop or restart, the test records a capture frame or
timestamp boundary. Recovery requires a new Holo `Up` packet after that
boundary and a current GoBFD control-API result with local and remote state
`Up`. Failure requires a current GoBFD control-API result with local state
`Down` and diagnostic `ControlTimeExpired`. Holo packet state plus GoBFD API
state provides independent wire and daemon evidence without adding a Holo gNMI
client to the repository.

The Holo image remains external. The repository does not add Rust source,
Cargo metadata, a Rust toolchain, or a locally built Holo wrapper.

## Python and Benchmark Removal

Remove the aiobfd container build, `bench_aiobfd.py`, `bitstring` requirements,
the Python benchmark container, and the Python benchmark service. The generic
`bench_struct.py` comparison is also repository-owned Python rather than an
external interop peer, so it leaves with this benchmark surface. Cross-language
benchmark reports retain Go, FRR-style C, and BIRD-style C results; their
aiobfd and Python-struct inputs, columns, legends, and claims are removed.

ExaBGP remains the sole required external Python interop peer and is handled by
the separately tracked uv migration. This change does not introduce another
Python runtime or dependency.

The optional Scapy fuzzer and the Python embedded in the current report
generator are existing migration debt covered by the separate Go-first tooling
work. Their presence is not an endorsement of permanent Python surfaces and
does not expand this peer-replacement change.

## Documentation and Contract

Update active English and Russian documentation, `README.md`, `AGENTS.md`, the
Make targets, and E2E target inventories to describe FRR, BIRD3, Holo, and
Thoro/bfd accurately. Replace active competitive-analysis sections and badges
with Holo facts verified from its release and source. Remove performance claims
that depended on the deleted aiobfd benchmark.

Historical changelog entries are not rewritten. New changelog entries record
the removal and replacement.

## Failure Handling and Cleanup

The interop runner must print Holo daemon, loader, and fallback stderr logs when
the Holo handshake fails. Before startup, the runner resolves the exact Compose
project and fails closed if containers, networks, or volumes with that project
label already exist. Cleanup removes only resources carrying that recorded
project label, then verifies that its containers, networks, and volumes are
absent.

Pulled and built images are cache inputs, not ephemeral task ownership. The gate
does not delete them because they may predate the run or be shared by another
worktree. Audit-only images created under an explicit unique label remain the
responsibility of the audit that created them.

## Acceptance

- Operational source, configuration, tests, benchmarks, reports, and reference
  documentation contain no `aiobfd` or `bitstring` reference. A repository scan
  may match only `CHANGELOG.md`, `CHANGELOG.ru.md`, `.cspell.json`, and this
  migration design; every other match fails the gate.
- Holo is pinned by the exact approved digest and exposes no floating tag.
- The Holo configuration matches the RFC/YANG microsecond interval contract.
- The tagged Go interop tests compile and strict golangci-lint receives the
  interop package as non-empty input.
- `make interop` and `make e2e-routing` both pass with fresh post-event evidence;
  the tagged Go runner reports a non-zero test count.
- The exact preflight-recorded Compose project has no remaining containers,
  networks, or volumes after success or failure; unrelated images and resources
  are untouched.
- Ordinary race tests, vet, module integrity, strict lint, documentation lint,
  and repository diff checks remain green.

## Primary Sources

- [Holo v0.9.0 release](https://github.com/holo-routing/holo/releases/tag/v0.9.0)
- [Holo BFD implementation](https://github.com/holo-routing/holo/tree/v0.9.0/holo-bfd)
- [Holo daemon configuration](https://github.com/holo-routing/holo/blob/v0.9.0/holo-daemon/holod.toml)
- [Holo containerlab startup loader](https://github.com/holo-routing/containerlab-topologies/blob/master/scripts/start.sh)
- [RFC 5880](https://www.rfc-editor.org/rfc/rfc5880)
- [RFC 5881](https://www.rfc-editor.org/rfc/rfc5881)
- [IETF BFD YANG model, RFC 9314](https://www.rfc-editor.org/rfc/rfc9314)
- [Cilium BFD control-plane proposal](https://github.com/cilium/cilium/issues/22394)
- [Calico BFD request](https://github.com/projectcalico/calico/issues/4607)
