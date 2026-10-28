# ADR-0008: Experimental prerelease boundary

| Field | Value |
|---|---|
| Status | Proposed; no publication authorization |
| Date | — |

## Context

The requested `v1.0.0-alpha.1` is an experimental delivery milestone. Stable
acceptance includes unfinished implementation and qualification, beyond the
24-hour run. Current release preflight and release notes deliberately accept
canonical stable tags only; a prerelease cannot use that path unchanged.

## Decision

Propose a separate prerelease channel with explicit experimental scope and
known limitations. Prerelease status does not establish supported scale,
stable API compatibility, complete RFC coverage or production readiness.
Functional and security failures remain blocking within the advertised alpha
scope. Beads `gobfd-qj0.8.2.6.4` owns the executable acceptance and delivery
plan; stable gates remain in their existing tasks.

Keep stable tag validation and stable OCI alias promotion unchanged. A future
prerelease path must identify its channel explicitly, retain reviewed release
branch and immutable tag ownership, verify draft assets before publication,
and avoid promoting stable aliases. Continue development on `dev` until the
alpha candidate and its dedicated checks are complete. The existing remote
delivery hold remains in force; this proposal creates no tag, draft or asset.

Run long qualification independently of an interactive SSH session on an
owned VM in the authorized `um-gate/netlab2` contour. VM provisioning belongs
to `um-iac` through an isolated Pulumi stack with a reviewed preview. Actual
guest resource, clock, source and binary admission precedes workload execution;
hypervisor capacity alone is insufficient. Retain logs, terminal status and
evidence after completion. Final qualification names the exact post-fix source;
a baseline run on an older candidate is diagnostic evidence.

## Consequences

The original pinned Control and Echo candidate retained its OS threads. At least
1,000 Control threads cannot meet the stable reference bound of 48 threads.
Moving that candidate to a VM does not resolve this implementation gap.
An alpha must disclose any retained limitations; a 25% CPU fixture is not
evidence for the approved 80% reference workload or 24-hour acceptance.

### Local source integration — 2026-10-28

The maintainer requested consolidation of local changes into `dev`. Permanent
Control/Echo owner pins are removed in that source integration. Native A/B,
the 48-thread reference bound, 24-hour operation and final wire qualification
remain open in Beads. The conservative baseline PID budget remains unchanged.

## References

- [Control owner loop](../../../internal/bfd/session_loop.go)
- [Echo owner loop](../../../internal/bfd/echo.go)
- [Stable release preflight](../../../test/internal/cirunner/release_preflight.go)
- [Stable release notes validation](../../../test/internal/cirunner/release_notes.go)
- [SemVer prerelease semantics](https://semver.org/spec/v2.0.0.html#spec-item-9)
- [Workload accounting boundary](./0007-workload-phase-accounting.md)
- [Dated status projection](../roadmap.md)
