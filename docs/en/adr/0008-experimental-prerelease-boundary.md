# ADR-0008: Experimental prerelease boundary

| Field | Value |
|:---|:---|
| Status | Proposed; no publication authorization |
| Date | — |

## Context

An experimental `v1.0.0-alpha.1` would have a smaller advertised scope than stable acceptance. Existing [release preflight](../../../test/internal/cirunner/release_preflight.go) and [notes validation](../../../test/internal/cirunner/release_notes.go) accept canonical stable tags. A prerelease cannot reuse that path unchanged.

## Decision

Propose a separate explicit prerelease channel. Keep stable validation and OCI alias promotion unchanged. A future channel requires a reviewed release branch, immutable tag ownership, verified draft assets and dedicated checks; it must not promote stable aliases. Functional/security failures block its advertised scope.

Development remains on `dev` until candidate and checks are complete. The existing remote-delivery hold remains active. This Proposed record authorizes no tag, draft, asset or publication. Prerelease acceptance/delivery and stable qualification remain separate.

Long qualification runs independently of interactive SSH on an owned VM in the authorized `um-gate/netlab2` contour. Provisioning belongs to an isolated `um-iac` Pulumi stack with reviewed preview. Admit actual guest resources, clocks, source and binary before workload execution; retain logs, terminal result and evidence. Final qualification names the exact post-fix source.

## Consequences

Prerelease status proves neither supported scale, stable API compatibility, full RFC coverage nor production readiness. A 25% CPU fixture does not qualify the approved 80% reference workload or 24-hour acceptance; hypervisor capacity is not guest admission.

The original pinned Control/Echo candidate could not meet the 48-thread reference bound with 1000 owners. The maintainer's local consolidation instruction of 2026-10-28 separately authorizes source integration removing permanent owner pins. This does not close native A/B, the 48-thread bound, 24-hour operation or final wire gates. Retain the conservative PID budget from [ADR-0007](0007-workload-phase-accounting.md) until measured revision.

## References

- [Control owner](../../../internal/bfd/session_loop.go), [Echo owner](../../../internal/bfd/echo.go).
- [Workload accounting](0007-workload-phase-accounting.md).
- [Dated roadmap projection](../roadmap.md).
