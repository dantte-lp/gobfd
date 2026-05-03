# S10 RFC Evidence Stability Plan

> **For agentic workers:** REQUIRED: Use `superpowers:executing-plans` to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stabilize the `make e2e-rfc` packet-evidence gate without weakening RFC 7419, RFC 9384, RFC 9468, or RFC 9747 assertions.

**Architecture:** The RFC interop suite remains a Podman Compose topology with Go assertions running inside the dev container. The fix belongs in the RFC test harness: packet evidence queries MUST wait for readable pcap data and matching packets before failing. BFD protocol behavior and production code MUST remain unchanged unless a reproducible protocol defect is found.

**Tech Stack:** Go 1.26, Podman, podman-compose, tshark, FRR, GoBGP, GitHub Actions, RFC 5880, RFC 7419, RFC 9384, RFC 9468, RFC 9747.

---

## Evidence

| Item | Value |
|---|---|
| Failed workflow | `E2E Evidence` run `25271088620` |
| Failed job | `Nightly E2E` |
| Failed step | `Run RFC E2E` |
| Failed test | `TestRFC7419_CommonIntervalAlignment` |
| Observed control-plane state | FRR reported BFD session `Up` |
| Observed failure | `no Up packets from GoBFD to FRR captured by tshark` |
| Root cause | Active pcap reads and single live-capture windows were used as immediate assertions while tshark status output and packet flush timing were still nondeterministic. |
| Routing follow-up | Thoro/bfd is optional evidence when the upstream peer panics on its unimplemented RFC 5880 poll-sequence interval update path. |

## Standards Contract

| Standard | Required behavior |
|---|---|
| RFC 5880 | BFD packets provide the wire evidence for session state and negotiated timers. |
| RFC 7419 | `DesiredMinTxInterval` MUST align to the next common interval value when common interval support is enabled. |
| RFC 9384 | BFD Down MUST drive BGP Cease/BFD-Down behavior in the integration scenario. |
| RFC 9468 | Unsolicited BFD MUST auto-create the passive session from received packets. |
| RFC 9747 | Echo packets on UDP/3785 MUST prove echo liveness and failure/recovery. |

## Sprint S10.RFC-STAB

### Task 1: Reproduce and classify the failure

**Files:**
- Read: `test/interop-rfc/rfc_test.go`
- Read: `test/e2e/rfc/run.sh`
- Read: `test/interop-rfc/compose.yml`

- [x] **Step 1: Fetch current CI evidence**

Run:

```bash
gh run view 25271088620 --job 74093533575 --repo dantte-lp/gobfd --log
```

Expected: `TestRFC7419_CommonIntervalAlignment` fails after FRR reports BFD `Up`.

- [x] **Step 2: Run local RFC E2E in Podman**

Run:

```bash
make up
make e2e-rfc
```

Expected: PASS or the same capture-read failure. A local PASS still preserves the CI failure as evidence of a race.

### Task 2: Add a failing helper test for packet evidence waiting

**Files:**
- Create: `test/interop-rfc/tshark_wait_test.go`
- Modify: `test/interop-rfc/rfc_test.go`

- [x] **Step 1: Add a unit test for delayed packet evidence**

Create a test that calls `waitTsharkFields` with a query function returning no rows for the first calls and one row before the deadline.

- [x] **Step 2: Verify RED**

Run:

```bash
make up
podman-compose -p gobfd -f deployments/compose/compose.dev.yml exec -T dev \
  go test -tags interop_rfc -run 'TestWaitTsharkFields' ./test/interop-rfc/
```

Expected: FAIL because `waitTsharkFields` does not exist.

### Task 3: Implement bounded packet-evidence waiting

**Files:**
- Modify: `test/interop-rfc/rfc_test.go`
- Modify: `test/interop-rfc/tshark_wait_test.go`

- [x] **Step 1: Add `waitTsharkFields`**

Implement a helper that retries `tsharkFields` until rows exist or a deadline expires. The helper MUST return the last tshark error, the filter, and the timeout in the final error.

- [x] **Step 2: Replace fixed sleeps in packet-evidence checks**

Use `waitTsharkFields` or `waitTsharkCount` for:

| Test | Evidence |
|---|---|
| `TestRFC7419_CommonIntervalAlignment` | GoBFD-to-FRR Up packets with `DesiredMinTxInterval` |
| `TestRFC9468_UnsolicitedBFD` | GoBFD-to-FRR unsolicited Up packets |
| `TestRFC9747_EchoSession` | echo packets and reflected echo packets |

- [x] **Step 3: Verify GREEN**

Run:

```bash
make up
podman-compose -p gobfd -f deployments/compose/compose.dev.yml exec -T dev \
  go test -tags interop_rfc -run 'TestWaitTsharkFields|TestWaitTsharkCount' ./test/interop-rfc/
```

Expected: PASS.

### Task 4: Update documentation and changelog

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `CHANGELOG.ru.md`
- Modify: `docs/en/README.md`
- Modify: `docs/ru/README.md`
- Modify: `docs/en/22-s10-rfc-evidence-stability-plan.md`
- Modify: `docs/ru/22-s10-rfc-evidence-stability-plan.md`

- [x] **Step 1: Record the fix**

Document the stability fix as test-harness hardening, not protocol behavior change.

- [x] **Step 2: Keep indexes synchronized**

Add the new plan to both English and Russian documentation maps and release-planning tables.

### Task 5: Verification gate

**Commands:**

```bash
make e2e-rfc
make e2e-routing
make e2e-linux
make verify
make lint
make gopls-check
make semgrep
git diff --check
```

**Expected:** All commands pass. All Go code checks run through the project Podman dev container where Makefile targets define containerized execution.

**Status:** Completed on 2026-05-03.

| Command | Result | Evidence |
|---|---|---|
| `make e2e-rfc` | PASS | `reports/e2e/rfc/20260503T130520Z` |
| `make e2e-routing` | PASS | `reports/e2e/routing/20260503T131655Z` |
| `make e2e-linux` | PASS | `reports/e2e/linux/20260503T132255Z` |
| `make verify` | PASS | `verify: build, test, lint, docs, proto, and vulnerability gates passed` |
| `make lint` | PASS | `0 issues.` |
| `make gopls-check` | PASS | `gopls-check: no diagnostics` |
| `make semgrep` | PASS | `110 go rules`, `0 findings` |
| `git diff --check` | PASS | no output |

### Task 6: Commit and PR

**Commands:**

```bash
git status --short
git add CHANGELOG.md CHANGELOG.ru.md docs/en docs/ru test/interop-rfc
git commit -m "test(interop): stabilize rfc packet evidence"
git push -u origin s10/rfc-evidence-stability
gh pr create --base master --head s10/rfc-evidence-stability \
  --title "test(interop): stabilize rfc packet evidence" \
  --body-file /tmp/gobfd-s10-rfc-evidence-stability-pr.md
```

**Expected:** PR checks pass, including `PR-safe E2E`; nightly/manual RFC E2E can be triggered after merge or with `workflow_dispatch`.
