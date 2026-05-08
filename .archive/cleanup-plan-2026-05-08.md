# Cleanup and Standardization Plan

Status: Draft. Owner: dantte-lp. Target: prepare repository for community
promotion (GoBGP, Cilium, Talos issue threads) by removing development-process
artifacts from canonical surfaces and enforcing a single documentation and
code template.

The plan is declarative and ordered. Every section lists concrete files,
required actions, and acceptance criteria. Items not on this list are out
of scope.

---

## 1. Findings (audit summary)

### 1.1 Documentation surface is mixed

`docs/en/` contains three classes of files that must not co-exist in the
canonical user-facing surface:

| Class | Examples | Problem |
|---|---|---|
| Canonical reference | `01-architecture.md` ... `16-production-runbooks.md` | Correct surface. Inconsistent header template. |
| Sprint/process logs | `17-scorecard-hardening.md`, `18-s10-*.md` ... `23-s11-*.md`, `implementation-plan.md`, `codebase-consistency-audit.md` | Date-stamped, sprint-tagged, contain agentic-worker directives. Not user-facing. |
| Decision/research notes | `linux-advanced-bfd-applicability.md`, `linux-netlink-ebpf-research.md`, `ovsdb-api-research.md`, `dependency-risk.md` | Useful, but mixed with both other classes. No ADR contract. |

### 1.2 Header template is partial

Canonical docs `01`, `02`, `03`, `04`, `05`, `06`, `07`, `08`, `09`, `10`, `11`
follow the template: title -> badge row -> blockquote summary -> horizontal
rule -> Table of Contents.

Canonical docs `12-benchmarks.md`, `13-competitive-analysis.md`,
`14-performance-analysis.md`, `15-security.md`, `16-production-runbooks.md`
do not have a badge row. `12` and `13`/`14` skip badges entirely. `15` and
`16` skip the blockquote summary too.

### 1.3 Style violations

- Sprint markers leak into prose: `S4`, `S10`, `S10.1`, `S11.3`, `S11.5`
  appear in `linux-netlink-ebpf-research.md`, `implementation-plan.md`,
  `codebase-consistency-audit.md`, and all `18`-`23` files.
- Agentic-worker directives leak into docs: `21`, `22`, `23` open with
  `> **For agentic workers:** REQUIRED: Use superpowers:...`. This is
  build-pipeline metadata, not technical documentation.
- Narrative voice (`we`, `our`) is rare but present in `13` (5 occurrences)
  and `14` (2 occurrences). Production reference docs must be declarative.
- Date stamps in body (`Date: 2026-05-01`) appear in `17`, `codebase-
  consistency-audit.md`, `linux-advanced-bfd-applicability.md`. Static dates
  in living docs cause rot.

### 1.4 RU/EN parity

`docs/ru/` mirrors `docs/en/` 1:1 today. RU translations are 1.0x-1.5x of EN
size, which is normal Russian-vs-English ratio. No orphans.

Implication: every EN file move/rename/delete must be mirrored in RU in the
same commit.

### 1.5 README index leaks process noise

`docs/en/README.md` lists all `18`-`23` sprint logs and lowercase planning
files in the Mermaid documentation map and in the Release Planning table.
A new reader sees `S10.1 Harness Plan`, `S11 Release Blocker Closeout`,
etc. - sprint internals.

### 1.6 Code surface

| Area | Status |
|---|---|
| `internal/config`, `internal/metrics`, `internal/netio`, `internal/server` | `doc.go` present. |
| `internal/bfd` | Package comment in `packet.go`. No `doc.go`. |
| `internal/gobgp` | Package comment in `client.go`. No `doc.go`. |
| `internal/sdnotify` | Package comment in `sdnotify.go`. No `doc.go`. |
| `internal/version` | Package comment in `version.go`. No `doc.go`. |
| `cmd/gobfd`, `cmd/gobfdctl`, `cmd/gobfd-haproxy-agent`, `cmd/gobfd-exabgp-bridge` | No package comment at all. |

`internal/bfd` is monolithic: `manager.go` 1598 lines, `session.go` 1489
lines, `packet.go` 772 lines, `auth.go` 742 lines. Not a refactor blocker
for this plan, but flagged for follow-up.

`golangci-lint` reports 10 outstanding issues:

- 5 `goconst` (string `single-hop`, `multi-hop`, `_uuid` repetitions)
- 5 `nolintlint` (unused `//nolint:gosec` directives in `bfd/manager.go`,
  `netio/rawsock_linux.go`, `netio/sender.go`, `test/interop-rfc/echo-
  reflector/main.go`)
- 1 deprecation warning: `gomodguard` -> `gomodguard_v2` in `.golangci.yml`

---

## 2. Target Layout

### 2.1 docs/ tree

```
docs/
├── README.md                       # bilingual index (kept as is)
├── en/
│   ├── README.md                   # canonical EN index, no sprint links
│   ├── 01-architecture.md          ┐
│   ├── ...                         ├─ canonical numbered series, 01..16 only
│   ├── 16-production-runbooks.md   ┘
│   ├── adr/                        # Architecture Decision Records (living)
│   │   ├── README.md
│   │   ├── 0001-link-state-rtnetlink.md           # was linux-netlink-ebpf-research.md
│   │   ├── 0002-ovs-backend-ovsdb.md              # was ovsdb-api-research.md
│   │   └── 0003-linux-advanced-bfd-applicability.md
│   └── reference/
│       └── dependency-risk.md      # promoted from top-level
└── ru/
    └── (mirror of en/)
```

### 2.2 Sprint logs and dated audits

Move out of `docs/`. Create `.archive/sprints/` at repo root (hidden from
default README index, retained for git-blameable history).

```
.archive/
└── sprints/
    ├── README.md                                  # short index, no further additions
    ├── s9-scorecard-hardening.md                  # was 17-scorecard-hardening.md
    ├── s10-extended-e2e-interop.md                # was 18-...
    ├── s10-1-harness-contract.md                  # was 19-...
    ├── s10-closeout-analysis.md                   # was 20-...
    ├── s10-rfc-evidence-stability.md              # was 22-...
    ├── s11-full-e2e-interop.md                    # was 21-...
    ├── s11-release-blocker-closeout.md            # was 23-...
    ├── implementation-plan.md                     # frozen
    └── codebase-consistency-audit-2026-05-01.md   # date in filename, frozen
```

Acceptance: `grep -r "^S[0-9]" docs/en/` returns zero matches outside
`adr/` (where ADR ID format is `0001-...`, not `S...`).

---

## 3. Document Template (canonical)

Every file under `docs/en/01..16-*.md` MUST follow this header:

```markdown
# <Title>

![Badge1](shields.io/...?style=for-the-badge)
![Badge2](shields.io/...?style=for-the-badge)
...

> One-sentence declarative summary of scope. No "we", no "our", no "this
> document". Subject + verb + object.

---

## Table of Contents

- [Section](#section)
- ...

---

## <Section>

<declarative or imperative prose>

...
```

Rules:

- No date stamps in body. Use Git for change history.
- No sprint identifiers (`S4`, `S10`, etc.).
- No agentic-worker directives.
- No first person (`we`, `our`, `I`). Use the subject (`GoBFD`, `the
  daemon`, `the CLI`, `the operator`).
- No future tense for delivered features. `GoBFD implements X.`, not
  `GoBFD will implement X.`.
- Code blocks: language tag mandatory.
- Tables: column headers mandatory; right-align numerics.
- Mermaid diagrams: subgraph titles in lowercase noun phrases, not full
  sentences.

ADR template (`docs/en/adr/NNNN-slug.md`):

```markdown
# ADR-<NNNN>: <Title>

| Field | Value |
|---|---|
| Status | Accepted | Superseded by ADR-NNNN | Deprecated |
| Date | YYYY-MM-DD (immutable, set on Accepted) |

## Context
## Decision
## Consequences
## References
```

---

## 4. Code Standardization

### 4.1 Package documentation

For every Go package under `cmd/` and `internal/`, package documentation MUST
live in `doc.go`. No mixing of package comment with logic files.

Files to create:

- `cmd/gobfd/doc.go`
- `cmd/gobfdctl/doc.go`
- `cmd/gobfd-haproxy-agent/doc.go`
- `cmd/gobfd-exabgp-bridge/doc.go`
- `internal/bfd/doc.go` (move comment from `packet.go`)
- `internal/gobgp/doc.go` (move comment from `client.go`)
- `internal/sdnotify/doc.go` (move comment from `sdnotify.go`)
- `internal/version/doc.go` (move comment from `version.go`)

`doc.go` template:

```go
// Package <name> <one-sentence-declarative-summary>.
//
// <Optional 2-3 sentence elaboration: scope, contracts, RFC references.>
package <name>
```

For binaries (`package main`), the comment documents the binary, not the
package:

```go
// Command <name> <one-sentence-declarative-summary>.
//
// Usage: <invocation pattern>.
package main
```

### 4.2 Linter cleanup

Acceptance: `make lint` returns 0 issues.

- Replace `single-hop` and `multi-hop` literals in
  `cmd/gobfdctl/commands/format.go` and `cmd/gobfdctl/commands/session.go`
  with constants. Likely location: a new `cmd/gobfdctl/commands/types.go`
  or reuse of `internal/bfd` enum strings.
- Replace `_uuid` literal in `internal/netio/lag_ovsdb.go` with a constant.
- Remove unused `//nolint:gosec` directives at:
  - `internal/bfd/manager.go:456`
  - `internal/bfd/manager.go:1008`
  - `internal/netio/rawsock_linux.go:277`
  - `internal/netio/sender.go:184`
  - `test/interop-rfc/echo-reflector/main.go:66`
- `.golangci.yml`: rename `gomodguard` -> `gomodguard_v2` per linter
  deprecation message; copy `blocked` configuration block as suggested.

### 4.3 Monolith follow-up (out of this plan's scope)

Track as separate ADRs:

- `internal/bfd/manager.go` (1598 lines) split candidates: session
  registry, echo session registry, gRPC event fan-out, runtime snapshots.
- `internal/bfd/session.go` (1489 lines) split candidates: control-packet
  loop, FSM-driven callbacks, timer block.

Do not start the split before the doc/style cleanup lands. Keep changesets
focused.

---

## 5. Execution Order

Do steps in order. Each step is a single PR or a small batch.

### Step 1 - Inventory freeze (no functional changes)
- [ ] Land this `PLAN.md` at repo root.
- [ ] Open one tracking issue with the section list as checkboxes.

### Step 2 - Sprint log relocation (mechanical)
- [ ] Create `.archive/sprints/` and `.archive/sprints/README.md`.
- [ ] `git mv` files per section 2.2 (both EN and RU in the same commit).
- [ ] Drop `S<N>` slug from filenames during the move (filenames lose
      sprint identifier, file content keeps it as historical record).
- [ ] Update `docs/README.md`, `docs/en/README.md`, `docs/ru/README.md` to
      remove all references to moved files.
- [ ] Update Mermaid map in `docs/en/README.md` and `docs/ru/README.md` -
      remove `Release Planning` subgraph entirely.
- [ ] Verify with `grep -r "implementation-plan\|codebase-consistency\|s10\|s11\|17-scorecard\|18-s10" docs/` - must be empty.

### Step 3 - ADR extraction
- [ ] Create `docs/en/adr/`, `docs/en/adr/README.md` (template + index).
- [ ] Convert `linux-netlink-ebpf-research.md` ->
      `docs/en/adr/0001-link-state-rtnetlink.md` (rewrite header, drop
      `S4`, set `Status: Accepted`, set `Date` to first-commit date).
- [ ] Convert `ovsdb-api-research.md` ->
      `docs/en/adr/0002-ovs-backend-ovsdb.md`.
- [ ] Convert `linux-advanced-bfd-applicability.md` ->
      `docs/en/adr/0003-linux-advanced-bfd-applicability.md`.
- [ ] Mirror in `docs/ru/adr/`.

### Step 4 - Reference promotion
- [ ] Create `docs/en/reference/` (and `docs/ru/reference/`).
- [ ] Move `dependency-risk.md` -> `docs/en/reference/dependency-risk.md`
      (mirror in RU).
- [ ] Update README indices.

### Step 5 - Header template normalization
- [ ] Add badge row + blockquote summary to `12-benchmarks.md`,
      `13-competitive-analysis.md`, `14-performance-analysis.md`,
      `15-security.md`, `16-production-runbooks.md`. Mirror in RU.
- [ ] Verify all `01..16-*.md` open with: H1 -> badges -> `>` summary -> `---` -> ToC.
- [ ] Remove first-person voice from `13-competitive-analysis.md`
      (5 hits) and `14-performance-analysis.md` (2 hits). Mirror in RU.

### Step 6 - Code package documentation
- [ ] Create `doc.go` for the 8 packages listed in section 4.1.
- [ ] Move existing inline package comments into `doc.go`; delete the
      original `// Package ...` line from the source file.
- [ ] Run `go vet ./...` and `make lint` to confirm no regression.

### Step 7 - Linter zero
- [ ] Apply fixes from section 4.2.
- [ ] `make lint` returns exit 0.

### Step 8 - README index final pass
- [ ] `docs/en/README.md` lists only `01..16` + `adr/` + `reference/`.
- [ ] No mention of S<N>, sprint, harness, closeout in the index.
- [ ] `*Last updated*` footer dropped (Git is the source of truth).

### Step 9 - Promotion gate
- [ ] All previous steps merged on `master`.
- [ ] CI green.
- [ ] Tag `v0.6.0` (or `v0.5.3` if treated as documentation-only).
- [ ] Proceed with community promotion (separate plan).

---

## 6. Acceptance Criteria

The cleanup is complete when ALL of the following hold:

1. `ls docs/en/*.md | wc -l` returns 17 (16 numbered + `README.md`).
2. `ls docs/en/adr/*.md | wc -l` returns at least 4 (3 ADRs + README).
3. `ls docs/en/reference/*.md | wc -l` returns at least 1.
4. `grep -rE "\bS[0-9]+(\.[0-9]+)?\b|sprint|closeout|harness contract" docs/en/` returns no matches outside `adr/` history sections.
5. `grep -E "^Date:" docs/en/*.md docs/en/**/*.md` returns no matches outside ADR `Date` fields.
6. `grep -nE "\b(we|our|us)\b" docs/en/*.md` returns no matches outside quoted text and code samples.
7. Every `01..16-*.md` file opens with H1 -> badge row -> `>` blockquote -> `---` -> `## Table of Contents`.
8. Every Go package under `cmd/` and `internal/` has a `doc.go` with a
   `// Package <name>` or `// Command <name>` comment.
9. `make lint` returns exit 0.
10. `docs/ru/` mirrors `docs/en/` file-for-file.

---

## 7. Out of Scope

The following are tracked separately, not blocked by this plan:

- Splitting `internal/bfd/manager.go` and `internal/bfd/session.go`.
- Adding remaining RFC backends (kernel/OVS/OVN/Cilium/Calico/NSX owner
  integrations for VXLAN/Geneve BFD).
- Community promotion comments on GoBGP, Cilium, Talos issue threads.
  Drafts are tracked separately; this plan only prepares the repository
  surface they will link to.
