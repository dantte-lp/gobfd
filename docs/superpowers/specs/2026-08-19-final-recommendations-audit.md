# GoBFD Final Recommendations Audit

**Status:** Complete research; decisions incorporated into the revised v0.6.2
and v1 design proposals

**Source:** secret Gist `66675b88a5fe0a66f270240f45307e0f`, revision
`9f2ed3b80ee05205e2104356ee7f07d4fa887992`, fetched 2026-08-19

## Method

Every recommendation was checked independently against the current GoBFD code,
the applicable RFC and errata, current Go and Linux documentation, and pinned
FRR 10.7.0, BIRD 3.3.2, GoBGP v3.37.0/v4.8.0, and Cilium 1.20.1 sources. A
recommendation is `accepted` only when the evidence supports both the problem
and the proposed release placement. `Modified` keeps the goal but changes an
unsafe or unproven mechanism. `Deferred` requires measurement or a later
protocol milestone. `Rejected` is incompatible with the wire contract or the
current attachment model.

## Recommendations 1-18

| # | Disposition | Independent evidence and plan decision |
|---:|---|---|
| 1 | Accepted | Current `BenchmarkFullRecvPath` ends at channel enqueue, not FSM commit. v0.6.2 corrects the 16M packets/s claim; v1 adds processed-packet acknowledgement, kernel-RX-to-FSM latency, timer error, false-Down, and drop accounting. |
| 2 | Modified | Permanent per-session `runtime.LockOSThread` exists and provides neither CPU affinity nor real-time scheduling. Remove it after A/B measurement. Do not add a copy-on-write registry until profiles prove lock contention. |
| 3 | Modified | FRR and BIRD set control-traffic TOS/priority; GoBFD currently sets only TTL/Hop Limit. Add configurable IPv4 DSCP/IPv6 Traffic Class with CS6 operational default and `SO_PRIORITY`; keep `SO_MARK=0` by default. This is operational policy, not an RFC MUST. |
| 4 | Rejected | RFC 5881 requires a stable source UDP port per session and recommends uniqueness. One ordinary connected TX socket cannot preserve that contract. Keep per-session ports for v1; any measured pool must retain stable session-to-port affinity. |
| 5 | Accepted as existing runtime behavior | Go 1.26 already applies container-aware `GOMAXPROCS` unless explicitly overridden. Expose the effective value and throttling evidence; do not build a duplicate scheduler feature. |
| 6 | Deferred | N-shards or a timing wheel may help only after invisible loss, thread pinning, and the end-to-end benchmark are corrected. FRR/BIRD architectures are reference points, not proof that Go needs the same loop. |
| 7 | Deferred | `SO_REUSEPORT` requires discriminator/peer-to-worker affinity and a bootstrap policy while `YourDiscriminator=0`. It is not a v1 core prerequisite. |
| 8 | Modified | Software kernel RX timestamps are useful for measurement. `recvmmsg` remains a benchmarked prototype. Hardware PHC, realtime kernel timestamps, and Go monotonic time cannot be mixed without explicit conversion; detection deadlines therefore remain monotonic accepted-packet deadlines pending proof. |
| 9 | Rejected | A fixed-offset cBPF TTL filter attached to the current UDP socket is not a safe IPv4/IPv6 design. Retain ancillary TTL validation unless a complete owner-integrated TC/XDP design replaces it. |
| 10 | Modified and deferred | An opt-in prefilter may be researched, but independent TC/XDP attachment is not generally Cilium-safe: TCX cleanup and XDP fallback can remove or clobber another program. Future work requires a Cilium-owned hook/plugin contract. |
| 11 | Deferred | `systemd` FDSTORE can retain descriptors, not BFD FSM, authentication, Poll state, deadlines, or actuator ownership. Warm restart requires a separate state-transfer ADR; zero-flap is not promised. |
| 12 | Modified | Poll initiation is a v1 correctness blocker. Local Demand initiation may remain unsupported, but RFC 5880 requires correct behavior when an Up peer sets Demand; store-and-ignore is not acceptable. |
| 13 | Deferred | S-BFD reflector/initiator is post-v1 core and also depends on correct Poll/Final behavior, discriminator ownership, ACLs, rate limits, and UDP/7784 port reversal. |
| 14 | Deferred | RFC 9985 and RFC 9986 are Experimental and require the completed Poll engine, significant-change classification, MCI/LCI rules, and periodic MCI reauthentication. They are not a v1 stable feature. |
| 15 | Modified | Remove obsolete/dead paths only with coverage and usage evidence. Keep all four shipped binaries. Fix bridge state/reconnect/default correctness before calling them stable; do not split modules merely to reduce the daemon dependency graph. |
| 16 | Deferred | io_uring is justified only after syscall cost is measured against batching. It is not a protocol or v1 release blocker. |
| 17 | Deferred | AF_XDP or a kernel FSM creates another state owner and must remain post-v1 R&D until the userspace contract and reconciliation model are stable. |
| 18 | Modified | The present all-interface plaintext listeners and unauthenticated flight-recorder endpoint are unsafe. Fresh v1 installs default to a UID-authenticated Unix control socket; loopback TCP remains an explicit compatibility transport, remote TCP requires authenticated TLS, metrics bind to loopback, and debug is off. |

## P0 findings in the Gist

| Finding | Verdict | Required release action |
|---|---|---|
| Packet delivery can be silently dropped | Confirmed | Return typed demux/enqueue outcomes and count parser, auth, TTL, demux, queue-full, and closed-session reasons. |
| State events can be dropped at three queue boundaries | Confirmed | Use versioned snapshots and generation gaps for observers; drive route actions through desired/applied level reconciliation. |
| Diagnostic is published after the state transition | Confirmed | Produce one transition result and update diagnostic before packet, log, metric, and immutable event publication. |
| `SetAdminDown` has an atomic-only fallback | Confirmed | Replace with request/ack and explicit timeout/error; never report a partial state change as success. |
| GoBGP actuation has no retry/reconcile loop | Confirmed | Add bounded retry, desired/applied state, process-epoch ownership receipts, and never enable a peer GoBFD did not disable. |
| Public performance claims measure enqueue | Confirmed | Correct claims in v0.6.2; make UDP-to-FSM commit and false-Down evidence the v1 qualification source. |
| GoBGP vulnerability exception is expired | Confirmed with release distinction | v0.6.2 remains red until a separately approved exact exception is recorded. Stable v1 waits for a compatible fixed GoBGP v4 release. |
| Multihop TTL 254 is hard-coded as if RFC-required | Confirmed | Expose a hop/security policy (`min_rx_ttl` or `max_hops`), document 254 as local GTSM policy, and test IPv4/IPv6 across 1/2/5 hops. |

## Additional blockers found independently

- RFC 5880 timer changes need a four-way immediate/deferred matrix; the Gist's
  blanket "apply new intervals after Final" rule is wrong.
- Outgoing crossed Poll can currently produce prohibited `P=1,F=1`.
- Held RFC 5880 Erratum 5205 requires a detection timeout while already Down to
  update the diagnostic to Control Detection Time Expired.
- RFC 9468 unsolicited admission accepts off-subnet sources when
  `allowed_prefixes` is empty; this precedes any XDP optimization.
- RFC 9747 echo currently has the wrong startup rate, state progression, and
  initial/established demultiplexing semantics; it remains preview.
- RFC 7130 code does not prove the mandatory dedicated destination MAC and
  per-member L2 behavior; it remains experimental.
- RFC 9764 configuration accepts 24-byte authenticated padding although the
  minimum is 26, and receive authentication currently hashes the padded wire
  length while transmit hashes the BFD PDU length.
- Config reload and API ownership can delete or conflict with sessions owned by
  another source; owner claims remain a v1 P0.
- GoBGP native BFD and external GoBFD can contend for the same peer and
  UDP/3784; v1 preflight must reject dual ownership.
- The ExaBGP bridge forgets announced state on Watch reconnect and can leave a
  stale route; the HAProxy bridge exits on Watch failure and binds all
  interfaces; both default to port 50052 while the daemon defaults to 50051.

## Release placement

`v0.6.2` remains a dependency, reproducibility, vulnerability-policy, and
documentation-truth maintenance release on GoBGP v3.37.0. It gains no new BFD
runtime behavior and makes no production-readiness claim.

`v1.0.0-alpha.1` starts with delivery accounting, generation-safe state,
RFC 5880 Poll/timer/diagnostic/Demand corrections, ownership, and removal of
thread pinning. `beta.1` adds secure management, GoBGP v4 reconciliation, QoS,
and companion correctness. `rc.1` supplies current pinned interop, the 24-hour
1,000-session qualification, upgrade/rollback evidence, and packaging. Stable
v1 remains blocked by `GO-2026-4736` until a compatible fixed GoBGP v4 exists.

## Independent review reconciliation

The final diff review found and the plans now include five omissions: an
event-loop acknowledgement and saturation test for `SetAdminDown`, the RFC 5883
multihop Echo prohibition and authentication guidance, packet-capture proof of
repeated AdminDown transmission for the negotiated Detection Time, the full RFC
5881 source-port range/stability/uniqueness gate, and a non-exhaustive v0.6.2
RFC 5880 partial-status statement. No remaining conflicting stable claims were
reported.

## Primary sources

- [Gist revision](https://gist.github.com/dantte-lp/66675b88a5fe0a66f270240f45307e0f)
- [RFC 5880 and errata](https://www.rfc-editor.org/errata/rfc5880)
- [RFC 5881](https://www.rfc-editor.org/rfc/rfc5881.html)
- [RFC 5883](https://www.rfc-editor.org/rfc/rfc5883.html)
- [RFC 9985](https://www.rfc-editor.org/rfc/rfc9985.html)
- [RFC 9986](https://www.rfc-editor.org/rfc/rfc9986.html)
- [Linux timestamping](https://docs.kernel.org/networking/timestamping.html)
- [systemd file-descriptor store](https://systemd.io/FILE_DESCRIPTOR_STORE/)
- [Go container-aware GOMAXPROCS](https://go.dev/blog/container-aware-gomaxprocs)
- [GO-2026-4736](https://pkg.go.dev/vuln/GO-2026-4736)
- [FRR 10.7.0 BFD](https://github.com/FRRouting/frr/blob/frr-10.7.0/doc/user/bfd.rst)
- [BIRD 3.3.2 BFD](https://github.com/CZ-NIC/bird/tree/v3.3.2/proto/bfd)
- [GoBGP v4.8.0 BFD](https://github.com/osrg/gobgp/blob/v4.8.0/docs/sources/bfd.md)
- [Cilium 1.20.1 TC loader](https://github.com/cilium/cilium/blob/v1.20.1/pkg/datapath/loader/tc.go)
