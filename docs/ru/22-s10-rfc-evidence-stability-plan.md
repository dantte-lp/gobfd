# План стабилизации S10 RFC Evidence

> **Для agentic workers:** REQUIRED: использовать `superpowers:executing-plans` для реализации этого плана. Шаги используют синтаксис checkbox (`- [ ]`) для отслеживания.

**Цель:** стабилизировать gate `make e2e-rfc` по packet evidence без ослабления проверок RFC 7419, RFC 9384, RFC 9468 и RFC 9747.

**Архитектура:** RFC interop suite остается Podman Compose топологией с Go assertions внутри dev-контейнера. Исправление относится к RFC test harness: packet evidence queries MUST ожидать читаемые pcap-данные и matching packets перед ошибкой. BFD protocol behavior и production code MUST не изменяться, если не найден воспроизводимый protocol defect.

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
| Root cause | Active pcap reads и single live-capture windows использовались как immediate assertions при nondeterministic tshark status output и packet flush timing. |
| Routing follow-up | Thoro/bfd является optional evidence, если upstream peer падает на unimplemented RFC 5880 poll-sequence interval update path. |

## Standards Contract

| Standard | Required behavior |
|---|---|
| RFC 5880 | BFD packets provide wire evidence для session state и negotiated timers. |
| RFC 7419 | `DesiredMinTxInterval` MUST align to the next common interval value when common interval support is enabled. |
| RFC 9384 | BFD Down MUST drive BGP Cease/BFD-Down behavior in the integration scenario. |
| RFC 9468 | Unsolicited BFD MUST auto-create passive session from received packets. |
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

Expected: PASS or same capture-read failure. Local PASS preserves CI failure as race evidence.

### Task 2: Add a failing helper test for packet evidence waiting

**Files:**
- Create: `test/interop-rfc/tshark_wait_test.go`
- Modify: `test/interop-rfc/rfc_test.go`

- [x] **Step 1: Add a unit test for delayed packet evidence**

Create a test that calls `waitTsharkFields` with a query function returning no rows for the first calls and one row before deadline.

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

Implement helper that retries `tsharkFields` until rows exist or deadline expires. The helper MUST return last tshark error, filter, and timeout in final error.

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

Document stability fix as test-harness hardening, not protocol behavior change.

- [x] **Step 2: Keep indexes synchronized**

Add new plan to English and Russian documentation maps and release-planning tables.

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

**Expected:** All commands pass. All Go code checks run through project Podman dev container where Makefile targets define containerized execution.

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
