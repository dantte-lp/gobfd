# Config-owned timer update transactions

Status: queue ordering and cancellation policy approved by the maintainer on
2026-09-08; independent document review passed and the maintainer approved
continuation into implementation after reviewing this written contract.
Owner: Beads `gobfd-qj0.8.2.1.1.1.4.1`. Implementation and peer qualification
remain `.4.2` and `.4.3`; this document is not evidence of runtime support.

## Scope and existing path

Extend `reloadConfig` → `applyBaseSessionCandidates` →
`Manager.ReconcileSessionsForOwnerDetailed` → the existing Session event loop.
At the pre-implementation baseline, Manager rejected changed effective parameters,
the pending interval fields had no production writer, and receipts lacked `Updated`.
Do not add a disconnected update API, public RPC, dependency or queue framework.

In-place changes cover Desired Min TX and Required Min RX for an existing
base session held solely by the config owner. The canonical key, discriminator,
sender lease and all non-timer parameters remain unchanged. Shared-owner
conflicts and authentication/identity changes fail before mutation. Reject new
sharing claims while any active, waiting or recovery transaction is unresolved,
even if the new claim matches the last confirmed tuple. Existing create/release
behavior remains unchanged.

## Ownership and bounded state

Each physical session retains at most one active timer proposal and one latest
waiting proposal. A proposal contains both intervals, a monotonic revision and
its owning desired generation. An explicit presence flag distinguishes RX zero
from an absent proposal; no `> 0` sentinel is allowed.

Manager validates and admits desired intent under its existing ownership lock.
Admission is a bounded, non-network operation. The Session loop alone changes
advertised values, effective timer values and Poll state. Never wait for peer
Final, session acknowledgement or a timer while holding Manager ownership or
coordinator apply locks. The pending slot is replaceable; a coalesced wake-up
only signals that durable intent should be inspected and is not a command log.

While Up, both active proposed values remain immutable, including in crossed
Final-only replies. New reloads replace only the waiting proposal. Repeating the
same generation and values is idempotent and does not refresh deadlines. If the
latest desired tuple equals the active tuple, discard any older waiting tuple
and associate completion with the latest generation without another Poll,
inheriting that active revision's original deadline. An expired active revision
cannot acquire another generation: reject the admission as unresolved until
protocol completion and separation. A fresh generation can then adopt the
confirmed tuple; failed revisions and generations remain terminal.
If it equals the last confirmed tuple while another tuple is active, keep it
as a waiting reverse update; do not pretend that the active change was undone.

## Advertisement and effects

Only promote while Up and with no unresolved startup/recovery Poll. Admission
while non-Up retains the proposal in the waiting slot with its original
deadline; it does not change wire values or report application.
Promotion to active changes the advertised tuple and initiates Poll. Reuse
scheduled transmissions; add no Poll-only packet when periodic TX is already
running. Crossed Poll replies remain `P=0,F=1`, followed by the local Poll.
Only a successfully transmitted `P=1,F=0` establishes `pollSent`.

While Up, apply the RFC 5880 §6.8.3 matrix to the active proposal:

| Change | Advertised value | Local effect |
|---|---|---|
| TX increases | New TX | Retain previous effective TX until Final |
| TX decreases | New TX | Use new TX immediately |
| RX decreases, including zero | New RX | Retain previous detection RX until Final |
| RX increases | New RX | Use new detection RX immediately |

Recalculate affected deadlines from the last successful send or valid receive,
not from the update call; an elapsed deadline becomes immediately due. A local
RX zero neither overrides peer RX-zero TX suppression nor disables the local
asynchronous detection timer. Successful timer negotiation must not itself
replace the discriminator or flap Up, but zero RX does not promise permanent
Up if the peer then ceases the traffic required by local failure detection.

## Sequence separation and bounded failure

Communicate each active tuple as one Poll transaction. After its Final, impose
the elapsed-time separation option in RFC 5880 §6.8.3 before promoting the next
tuple. Measure the successful exchange from its first successful Poll send to
the completing Final, including retry delays; wait that duration again after
Final, with a minimum of one microsecond. This also exceeds that observed
round-trip duration since the last Poll send. Do not use an F-clear barrier in
Demand mode, or claim that an observed round trip bounds arbitrary future
network delay. Peer qualification must cover the deployed RTT envelope.

Use one admission deadline per distinct desired proposal: three times the
larger of the current and proposed local Poll Detection Times, captured at
admission. A local Poll Detection Time here is local DetectMult multiplied by
`max(effective Desired TX, peer Required RX)`, including the non-Up slow floor.
The proposed calculation substitutes proposed TX. All values are validated
wire intervals; arithmetic must remain checked and positive. This is a product
negotiation budget, not a replacement for RFC failure detection. Queue wait,
separation and first-send failures all consume the same budget; retries and
unrelated received packets do not renew it.

At expiry, publish `failed`, not `applied`, for that revision. If already active,
retain the advertised tuple and protected effective values, keep the outstanding
Poll retryable, and do not promote its waiting successor until Final separates
the transactions or the wire session is destroyed. A late Final may finish the
protocol transaction but cannot retroactively mark its failed generation as
successful. A fresh desired generation may adopt the resulting confirmed tuple.
Waiting proposals have independent deadlines and can fail while the old Poll
remains unresolved. Remove an expired waiting proposal from eligibility; only
a fresh desired generation can admit it again. Memory remains bounded even if
the peer never sends Final.

An Up→non-Up transition fails outstanding update receipts and discards their
waiting proposal. Retain the last advertised configured tuple, not a waiting
tuple or an automatic rollback. Outside Up, apply its values immediately and
advertise/effectively use TX of at least one second as RFC 5880 §6.8.3 requires;
this state-dependent floor overrides Up-only advertisement immutability.
Retire the interrupted Poll without confirming its revision. If the floor
changes advertised TX, immediately initiate a recovery Poll while non-Up:
the RFC requirement to Poll on a Desired TX change is not restricted to Up.
If Up returns before that Poll and its separation finish, retain the floor
until they do; then initiate the slow-to-fast Poll for the retained configured
tuple. Do not overwrite the outstanding floor Poll on entering Up. When the
floor does not change advertised TX, initiate the recovery Poll on returning
Up even if no slow-to-fast decrease is needed. Each recovery stage requires a
successful Poll send, Final and the same separation before another stage or
new proposal. A further departure from Up restarts this failure handling;
mandatory non-Up floor changes never wait behind a transaction. Recovery never
changes a failed receipt into success. Startup Polls use the same serialization
and completion/separation gate.
No state transition may overwrite a live Up update Poll. Clear pending
ownership on physical destruction; no completion from a retired session may
update a replacement session or a newer generation. Do not automatically tear
down a session just to make reload succeed.

## Cancellation and receipts

Cancellation before admission prevents the proposal. After admission, Manager
owns durable intent; cancelling a caller stops its wait, not the wire protocol.
Reverting an advertised tuple is another validated desired generation, not a
rollback claim. Shutdown ends the session-owned work and reports incomplete
transactions without claiming successful application.

Expose requested revision/tuple, active advertised tuple, effective timer
values, confirmed revision and bounded pending/failed reason in internal status.
Add `Updated` through Manager results, source results, receipts and logs. Count
each confirmed changed session once per generation, never once per observation.
An unchanged, already confirmed tuple does not increment it. A superseded
generation never advances the coordinator's AppliedGeneration.

Until every session in the current desired generation is confirmed, keep its
receipt pending/failed and readiness stale. Compatibility callers must not log
convergence merely because no permanent error is present. Completion requires
a production wake-up path: one coalesced Manager notification wakes a
context-owned coordinator worker, which rereads revision-qualified snapshots.
No peer wait occurs under apply locks; dropped duplicate wake-ups cannot lose
completion because the snapshots are the source of truth. Initial inspection
after subscription closes the subscribe/completion race. Do not depend on the
existing interface-only retry entrypoints or on receiving another SIGHUP.

## Acceptance and sources

Reuse existing Session sender/timer helpers for focused race-enabled cases:
the four-way matrix, explicit RX zero, active plus superseded waiting reloads,
reverse updates, crossed Poll, failed first sends, early/late Final, expiry,
cancellation, ownership conflicts, non-Up admission/recovery and shutdown.
One daemon-level regression
must prove generation/readiness convergence without a second reload. Existing
Go-owned FRR/BIRD tests must then prove real config reload, wire flags, timing,
stable discriminator and truthful receipts. H-05 remains partial until both
implementation and peer qualification pass.

Normative sources: [RFC 5880 §§6.5, 6.6, 6.8.3–6.8.4](../../rfc/rfc5880.txt)
and the [production contract](2026-08-18-gobfd-v1-production-contract-design.md).
Context7 Go documentation was consulted; the pinned
[Go 1.27 context source](https://github.com/golang/go/blob/go1.27.0/src/context/context.go)
confirms that cancellation does not wait for work to stop. It is not a protocol
completion acknowledgement or a rollback guarantee.
