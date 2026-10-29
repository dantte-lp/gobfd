# Dated design records

These records preserve technical decisions and historical evidence from 2026-08-18 through 2026-10-15. They do not certify the current binary, current upstream versions, dependency safety or release acceptance. [Russian translation](../../ru/reference/design-records.md).

The [production contract](production-contract.md) owns v1 requirements; [timer transactions](timer-transactions.md) owns TX/RX ordering. Current behavior belongs to [RFC coverage](../08-rfc-compliance.md), [interop](../05-interop.md), [security](../15-security.md), [configuration](../03-configuration.md) and the [release guide](../10-changelog.md). Executable work and priorities remain in the project issue tracker.

## Source identities

Every source below is preserved in full in Git snapshot `c5bcbfcb7e058a5a7f22b8a6811c8e4250963d50`. The blob identifies the complete historical original, including superseded decisions and execution metadata omitted from this reference.

| Source date | Subject | Git blob |
|:---|:---|:---|
| 2026-08-18 | Maintenance dependency design and later amendments | `6bfe5c012dcfceeb0faf137955d231359e420964` |
| 2026-08-19 | Independent recommendations, reconciled 2026-08-27 | `fcd3a23c34c50c5437a3b1828678aa9bdb1f5ef2` |
| 2026-08-20 | Holo peer selection and lifecycle | `3395a4cfa81037fc5ae489653b9beda72d4b05d7` |
| 2026-08-27 | Release-line and immutable-publication decision | `0ae382e79def593fb01a405fb810c950932019e0` |
| 2026-10-15 | eBPF evaluation | `4ebb54531097424150563ad8d88264cbc1271226` |
| 2026-10-15 | Management transport decision | `cedaf9c9f3017a3ddfe883e52830405c030fac10` |

The recommendations compare baseline `20eaaacd87f7a27ae446674ffec11dbce8177dc4` with `0376e0f5cd90b8692010586b83854d8d4dc36b51`. Their original external source is Gist revision `9f2ed3b80ee05205e2104356ee7f07d4fa887992`, retrieved 2026-08-19. The 2026-08-27 corpus check recorded 43 RFC text files, all matching RFC Editor SHA-256 responses; RFC 9985/9986 were Experimental. This historical measurement is not a new corpus check.

## Protocol and execution alternatives — 2026-08-19

- A single connected TX socket is rejected because it cannot preserve the RFC 5881 stable per-session source-port contract and recommended uniqueness. A measured socket pool must retain session-to-port affinity.
- A fixed-offset classic BPF (cBPF) TTL filter on the UDP socket is rejected as an unsafe IPv4/IPv6 design. Ancillary TTL/Hop Limit validation remains authoritative unless a complete owner-integrated TC/XDP design replaces it.
- `SO_REUSEPORT` requires discriminator/peer-to-worker affinity and an initial-packet policy while `YourDiscriminator=0`. Sharding, timing wheels, copy-on-write registries, receive batching and io_uring require profile evidence; architecture in FRR/BIRD alone does not justify them in Go.
- Software kernel RX timestamps require explicit conversion between kernel realtime, hardware precision hardware clock (PHC) and Go monotonic domains. Detection remains based on accepted-packet monotonic time until equivalent late-delivery behavior is proven.
- systemd FDSTORE retains descriptors, not BFD finite-state machine (FSM), authentication, Poll state, deadlines or actuator ownership. Warm restart needs a separate state-transfer decision; zero-flap restart is not promised.
- S-BFD reflector/initiator is post-v1 and requires correct Poll/Final, discriminator ownership, access-control lists, rate limits and UDP/7784 port reversal.
- Experimental RFC 9985/9986 require a complete Poll engine, significant-change classification, More/Less Computationally Intensive (MCI/LCI) authentication rules and periodic MCI reauthentication. They are outside stable v1.
- AF_XDP and kernel FSM offload introduce another state owner and remain post-v1 research. Independent TC/XDP attachment can conflict with Cilium cleanup/fallback; a future path needs a versioned Cilium-owned hook/plugin contract.
- All four shipped binaries remain intentional. Dead-path removal needs coverage/usage evidence; speculative module splitting is rejected. Companion state/reconnect/default correctness precedes stable claims.

The detailed Poll, diagnostic, AdminDown, typed-delivery, ownership, secure-management, QoS and scale requirements are retained in the production contract rather than duplicated as historical open tasks.

## Holo peer and lifecycle — 2026-08-20

The selected fourth peer is external Holo v0.9.0 at the official amd64 image identity:

```text
ghcr.io/holo-routing/holo-bundle@sha256:5c1f61475b1623b3eab611921f8319fb0a10492ced3f7da05e656418abb5ca4a
```

The dated source records MIT licensing, RFC 5880/5881/5882/5883 support, RFC 5880 §6.8.6 validation and an in-toto provenance attestation. The then-existing base gate was amd64; the absent multiarch manifest did not narrow it. The recorded live qualification reached local/remote Up at 300 ms TX and 900 ms detection; stopping Holo yielded Down/`ControlTimeExpired` after that interval. These observations do not qualify a later image or topology.

The replacement preserves four independent implementations: FRR, BIRD3, Holo and Thoro/bfd. The old peer and its Python benchmark surfaces are removed from operational consumers; published changelog history remains unchanged. The remaining cross-language benchmark schema contains Go, FRR-style C and BIRD-style C without claims derived from removed inputs. The invalid-vector generator uses Go codecs while retaining the legacy `scapy` service identifier. ExaBGP remains the external Python peer; the decision adds no Rust source, Cargo metadata, toolchain, locally built Holo wrapper or additional Python dependency.

The historical private lab uses Holo `172.20.0.50` and GoBFD `172.20.0.10`; these are recorded lab addresses, not recommended public examples. Holo uses `NET_RAW`/`NET_ADMIN`, the image's `holo` user/database path, read-only TOML daemon configuration, gRPC northbound and stdout logging; file logging and unused gNMI are disabled. A separate read-only YANG command file defines `eth0`, `ietf-bfd-types:bfdv1`, multiplier 3 and TX/RX `300000` microseconds. `holod -c` consumes TOML, not the YANG file. A same-digest one-shot loader waits for daemon gRPC health and applies the YANG file through `holo-cli`; its successful exit is required for readiness. It has no static address and is not another peer.

Lifecycle acceptance uses a capture frame/timestamp boundary before stop/restart. Recovery requires a new Holo Up packet after that boundary and current GoBFD API local/remote Up; failure requires current local Down/`ControlTimeExpired`. A historical Up packet alone is insufficient. Tagged suites require nonzero test input and handshake, independent-session, failure/recovery, discriminator and RFC 5880/5881 checks. Required ordinary race, vet, module, strict lint and documentation gates remain independent.

Failure diagnostics retain daemon and loader logs plus fallback `/tmp/holod.err`. Preflight resolves the exact Compose project and fails closed if any labelled container, network or volume already exists. Cleanup affects only the recorded project-labelled resources, resolves remaining exact IDs after partial failure and verifies their absence on both success and failure. Pulled/built images are shared cache inputs, not implicit run ownership, and are preserved; explicitly unique audit-owned images have separate ownership. Unrelated resources remain untouched.

The alternatives rejected reducing the matrix to three, immature/no-stable-release peers, repository-owned daemon wrappers and platform-specific assumptions; kernel-dataplane peers remain optional research. Cilium/Calico integration proposals and MetalLB's FRR delegation do not supply an additional independent BFD implementation.

Sources: [Holo v0.9.0](https://github.com/holo-routing/holo/releases/tag/v0.9.0), [tagged BFD source](https://github.com/holo-routing/holo/tree/v0.9.0/holo-bfd), [daemon TOML](https://github.com/holo-routing/holo/blob/v0.9.0/holo-daemon/holod.toml), [RFC 9314 YANG](https://www.rfc-editor.org/rfc/rfc9314.html).

## Dependency and evidence boundaries — 2026-08-18 to 2026-08-27

The maintenance design preserves `bfd.v1`, YAML and GoBGP v3.37.0 wire contracts and adds no BFD runtime behavior. The later v1 GoBGP migration is separate. The original priority of maintenance before v1 is superseded by the production contract's 2026-10-20 amendment. Historical Python locks, quality tools and Oracle Linux selections in the original are not current requirements: [AGENTS.md](../../../AGENTS.md) owns the current no-Python and Debian trixie policies; generic RPM generation and historical v0.6 artifacts remain distinct from v1 OCI acceptance.

Dependency inventory disposition covers the entire selected runtime and isolated tool graphs, OCI, Actions, generators, peers and repository toolchains. Newest available does not imply compatible. Each unresolved adopted/retained release-blocking dimension needs an exact reviewed exception with owner, reason and review date; deferred/stale records do not inherit another record's exception. Runtime and tool modules retain separate scanner JSON and CycloneDX SBOMs; a tool directive alone does not isolate Minimal Version Selection.

The historical schema-v2 inventory records 196 runtime and 388 tool modules (584 total), 21 declared tools and 40 registry-backed locked Python packages. Per-record review separates repository archive state, artifact availability and release-line end of life. Canonical source/package-URL/commit/digest coordinates identify each record. A structured evidence record binds subject and review field to source, command/query, tool/version, observation time, result and result hash. The offline checker rejects duplicate/dangling evidence, incomplete graphs, stale locations, tag-only immutable claims and missing/extra/stale exception coverage.

Exact Go-module license evidence uses deps.dev v3 exact-version responses or exact-commit license files. Python evidence binds normalized package name, exact version, PyPI and a locked artifact SHA-256; PEP 639 or a closed legacy mapping is accepted, ambiguous BSD requires exact source-commit/license hash. OCI evidence binds digest to build-source repository, exact commit, SPDX/precise LicenseRef and license-file SHA-256. Registry availability does not prove source, activity or licensing; labels are discovery hints only. Shared profiles cannot assign one unrelated license/repository result to many records. Bounded generation fails closed on transport, HTTP, size, JSON-stream or identity errors; the checker remains offline.

The historical tool graph selects `github.com/tenntenn/text/transform@v0.0.0-20250402111347-ba836492e880`: that revision adds MIT license/README with no package-source change. `github.com/chzyer/logex@v1.1.10` contributes no package to the distributed tool closure despite remaining in the selected build list. The archived `github.com/ovn-org/libovsdb v0.7.0` moves to active `github.com/ovn-kubernetes/libovsdb v0.8.1` after owner/API/transaction/license/release-impact review; the low-level `Transact` and exact `Port.interfaces` fixtures preserve RFC 7130/config/ownership semantics.

Historical Go-owned lifecycle requirements retain immutable namespace/container IDs, bounded evidence, private unique report paths, exact ownership ledgers, LIFO cleanup and final absence checks. A successful cleanup request alone does not prove absence. Container inspection rejects empty/oversized/partial evidence; per-stream diagnostics are bounded. Ownership ledgers use rooted same-directory mode-0600 temporary files, sync/close/atomic rename; reports use 0700 directories and 0600 files. Context-bounded readiness rejects error-bearing observations. New producer or local receipts do not replace live peer/package/release qualification.

The dated HAProxy receipt `reports/e2e/haproxy-health/20260824T195549Z/` records Go 1.27.0, Podman 5.8.2, testcontainers-go 0.44.0, HAProxy 3.4.3, 97 decoded BFD packets, nine mountless containers, srv1 Down/recovery while srv2 stayed healthy, and exact owned-resource absence. A uniquely identified dangling builder was separately removed without global prune. This is historical evidence, not a new run.

The exact, time-limited historical vulnerability exceptions do not authorize renewal or broadening. Management TLS does not mitigate the BGP NEXT_HOP parser denial of service on TCP/179; trusted peers, control-plane ACLs/GTSM and gobgpd monitoring/restart are separate mitigations. Current advisory conditions belong to the production contract and [dependency risk](dependency-risk.md).

## Release line and failed cuts — 2026-08-27

Product branches use `release/vMAJOR.MINOR`, with `release/v0.6` preserving GoBGP v3.37.0 and v0.6 compatibility. `dev` integrates the next product line and is not a release source. Stable acceptance identifies branch, exact commit and evidence; a closed implementation on `dev` does not qualify a stable branch. Applicable fixes reach maintained stable history and changelogs before closeout; future v1 changes never merge backward. The live ruleset, tag and asset checks remain required; a document is not proof of host configuration.

The historical `v0.6.2` tag at `48ef04e158dbc923173f44932a4686747bf873ee` failed in run `33083358370` before draft/assets/OCI: smoke binaries polluted the checkout and the runner lacked required uv/Podman/Compose prerequisites. `v0.6.3` then created complete draft assets but failed closed on an empty/mismatching release body; GoReleaser v2.18 skipped notes when its changelog pipe was disabled. Neither failed tag is moved or reused.

Recovery run `33101133019` succeeded on its second attempt after a transient OSV download failure was independently reproduced successfully. Immutable `v0.6.4` points to `b1c0bcd7d2e9abed00368b2082e34f521084c087`; Release `378036743` was published, non-prerelease, immutable, with 12 assets, verified checksums, two CycloneDX SBOMs and report/API digests. The then-qualified indexes were Debian trixie `sha256:6deccf688ef862da35c35fa3c8343ed6499fbf0d31d4b58226786dedc8b911e1` and Oracle Linux 10 `sha256:eae22d81c6c8c31b9e7c969456203621d1f75b6fa1f17845b868208dbe4b4cef`, each with amd64/arm64 and linked attestations. These historical Oracle artifacts do not expand the current OCI policy.

PR #63 integrated reviewed `2e6335cb310f8654a74ca348916386a47cf33d87` after 20 contexts; the protected default was `48ef04e158dbc923173f44932a4686747bf873ee`. The original narrow v0.6.4 notes omitted unpublished v0.6.2/v0.6.3 maintenance, and `master` changelogs lagged at v0.6.2. PR #67/#68 corrected cumulative notes and synchronized all three branches without changing immutable tag/assets. Publication acceptance is distinct from later stable applicability and qualification gaps.

The reusable publication policy completes artifacts in a draft before publication: exact annotated tag/branch-head validation, nonempty cumulative changelog notes, checksum/SBOM/provenance/package/OCI content checks, one runnable descriptor per platform, actual SPDX and SLSA v1 payload parsing, then digest-verified alias promotion and final publication. Duplicate draft/assets/versioned OCI refs fail closed; no silent replacement or `--clobber`. A failed cut is fixed forward. Required checks compare Buf against the actual PR target, and rulesets protect release branches and immutable tags before creation. See the current release guide for the supported procedure.

## eBPF decision — 2026-10-15

The evaluation rejects cilium/ebpf integration in v1. It records v0.22.0, MIT, active/unarchived, Go 1.25.0 module floor, Linux amd64/arm64, no 32-bit support and pre-1.0 API churn; these are dated observations, not current recommendations. Release review across v0.19.0–v0.22.0 found no listed security advisory, but v0.21.0/v0.22.0 broke APIs and Linux 7.1 BTF required v0.22.0. The README used for some requirements was `main`, not the tagged release. Pure-Go/CGO compatibility was inferred, not built.

At the assessment date, no committed end-to-end receive-latency or syscall-pressure baseline existed; additional privileges or build complexity were not justified. `bpf2go` adds clang/LLVM, libbpf/kernel headers; Go `asm` avoids C compilation but is harder to review. Loading needs `CAP_BPF` (or older `CAP_SYS_ADMIN`), `bpf()` and AF_XDP address-family permission where applicable. BPF tokens require runtime delegation. Dedicated-interface generic/driver prototypes follow a measured syscall bottleneck that batching cannot remove and an explicit dataplane-owner contract.

Kernel Echo reflection is rejected: RFC 9747 §2 uses ordinary remote forwarding; the lab reflector is a test tool. Per-socket loss uses `SO_RXQ_OVFL` without eBPF. Socket filtering is not itself a reason to add this library; ancillary GTSM already exists. Full in-kernel FSM is outside v1. `IP_MINTTL` UDP behavior was not verified and is not accepted by this record.

Sources retained by the original include [cilium/ebpf v0.22.0](https://github.com/cilium/ebpf/tree/v0.22.0), [socket(7)](https://man7.org/linux/man-pages/man7/socket.7.html), [capabilities(7)](https://man7.org/linux/man-pages/man7/capabilities.7.html) and [RFC 9747 §2](https://www.rfc-editor.org/rfc/rfc9747.html#section-2).

## Management transport decision — 2026-10-15

[Public issue #87](https://github.com/dantte-lp/gobfd/issues/87) records the selection of Unix socket/kernel peer UID for local clients and authenticated TLS for remote TCP. Anonymous loopback cannot identify a host user or host-network pod. A bearer token is rejected as weaker shared identity with more secret handling; mandatory local mTLS is rejected for operational cost; systemd socket activation is deferred.

The accepted local socket is `/run/gobfd/gobfd.sock`, mode 0660, owner `gobfd`, group `gobfd-admin`. The daemon group also accesses mode-0750 `root:gobfd` secret configuration and therefore cannot serve as the operator-admin group. Packaging uses an admin sysusers group, daemon supplementary membership and `RuntimeDirectory=gobfd`; configured path/mode/group permit a shared container `emptyDir`. Principal uses peer UID as seen in the daemon's user namespace, never PID. Credentials are captured before wrappers can obscure the Unix connection. A stale path is removable only if it is a socket with no answering server; serving starts only after restrictive creation/chmod.

Empty `grpc.addr` disables optional TCP. Loopback reads are compatible; anonymous mutation needs explicit `grpc.unsafe_loopback_mutations`, startup visibility and `NOT_SERVING` management health. Non-loopback plaintext fails before serving. TLS needs cert/key/client CA, HTTP/1.1 plus HTTP/2 ALPN and handshake-time file reload through `GetConfigForClient`. The source's wire observation found grpc-go fails without `h2` with `tls: no application protocol`; this listener uses its own `tls.Config`, not `ServeTLS`. Unix/TLS protocol and real-wire tests are independent gates; h2c and TLS use separate listeners.

Clients accept `unix:///path`, `http://host:port`, `https://host:port`, TLS CA/cert/key flags or matching configuration; bare `host:port` retains HTTP meaning. mTLS URI SAN (SPIFFE), otherwise first DNS SAN, supplies identity with explicit roles; cross-principal claims remain isolated. Kubernetes uses shared socket ownership and an exec health probe when TCP is off. Existing configured listeners retain migration notes. Browsers over Unix sockets are outside scope. Certificate reload does not imply revocation checking; short-lived renewal and optional CRL/OCSP are separate concerns. The current security reference owns runtime behavior and corrected transport-principal isolation.
