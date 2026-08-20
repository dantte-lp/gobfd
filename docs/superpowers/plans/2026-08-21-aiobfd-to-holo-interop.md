# aiobfd to Holo Interop Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove aiobfd and its Python benchmark surface, then preserve the four-peer BFD interoperability gate with immutable Holo v0.9.0 and fresh lifecycle evidence.

**Architecture:** The base Compose topology keeps peer address `172.20.0.50` but assigns it to Holo. `holod` receives a test TOML configuration while a healthy-gated one-shot `holo-config` service applies the IETF BFD YANG commands through `holo-cli`. The shell runner starts Holo in a separate bounded phase and requires the loader's inspected exit status to be exactly zero before starting GoBFD because podman-compose 1.5.0 and 1.6.0 reduce `service_completed_successfully` to the `stopped` state without checking the exit code. Go tests prove current daemon state and post-event packets rather than accepting stale capture history. Beads issue `gobfd-qj0.8.1.5.3` remains the durable source of task status.

**Tech Stack:** Go 1.27, Go test/race, gopls, golangci-lint v2, Podman, the repository's pinned `podman-compose` provider, Docker Compose semantics, Holo v0.9.0, RFC 5880/5881, RFC 9314 YANG, tshark.

---

## File Map

- Create `test/interop/topology_contract_test.go`: ordinary Go contract tests for the Compose and Holo configuration surfaces.
- Create `test/interop/topology_contract_negative_test.go`: fail-closed malformed Compose and noncanonical Holo configuration cases.
- Create `test/interop/runner_startup_contract_test.go`: controlled fake-command proof that loader failure cannot start GoBFD.
- Create `test/interop/holo/holod.toml`: test-only Holo daemon, logging, and northbound configuration.
- Create `test/interop/holo/holo.startup`: IETF interface and BFD YANG commands applied by `holo-cli`.
- Modify `test/interop/compose.yml`: replace aiobfd with `holo` and one-shot `holo-config` services.
- Modify `test/interop/interop_test.go`: rename peer helpers and add fresh Holo lifecycle/API evidence.
- Modify `test/interop/run.sh`: enforce fail-closed two-phase startup, then retain the legacy shell gate with Holo names and diagnostics.
- Modify `test/e2e/routing/run.sh`, `test/e2e/targets.md`, and `Makefile`: correct service inventories, project ownership, and gate names.
- Delete `test/interop/aiobfd/Containerfile`: remove the abandoned peer build.
- Delete `bench/Containerfile.python` and `bench/python/`: remove repo-owned Python/bitstring benchmarks.
- Modify `bench/compose.yml`, `scripts/gen-report.sh`, and `scripts/report-template.html`: remove deleted benchmark inputs and columns.
- Modify active README, AGENTS, EN/RU interop, architecture, integration, competitive, and performance documents: replace active aiobfd claims with verified Holo facts.
- Modify `CHANGELOG.md` and `CHANGELOG.ru.md`: add a new removal/replacement entry without rewriting published history.
- Modify `.cspell.json`: add Holo terminology while retaining the historical aiobfd spelling entry.

## Commit Safety Rule

The worktree already contains dependency-refresh edits in several files this
plan also touches. For every commit, stage new files directly, use
`git add -p -- <exact paths>` for pre-existing files, and inspect the complete
`git diff --cached` before committing. Stage only the Holo task hunks. A staged
dependency-refresh hunk is a blocker: unstage it and repeat partial staging.
Never use broad directory staging.

### Task 1: Establish and Implement the Holo Topology Contract

**Files:**
- Create: `test/interop/topology_contract_test.go`
- Create: `test/interop/topology_contract_negative_test.go`
- Create: `test/interop/runner_startup_contract_test.go`
- Create: `test/interop/holo/holod.toml`
- Create: `test/interop/holo/holo.startup`
- Modify: `test/interop/compose.yml`
- Modify: `test/interop/run.sh`
- Modify: `docs/superpowers/plans/2026-08-21-aiobfd-to-holo-interop.md`
- Delete: `test/interop/aiobfd/Containerfile`

- [ ] **Step 1: Write the failing topology contract test**

Add untagged `interop_test` contract files. Resolve the repository root by
walking upward from the working directory to a validated `go.mod`, including
under `go test -trimpath`. Decode the selected Compose nodes strictly with the
existing `go.yaml.in/yaml/v3` dependency and assert:

```go
const holoImage = "ghcr.io/holo-routing/holo-bundle@sha256:5c1f61475b1623b3eab611921f8319fb0a10492ced3f7da05e656418abb5ca4a"

func TestHoloTopologyContract(t *testing.T) {
	t.Parallel()

	compose := loadCompose(t)
	holo, ok := compose.Services["holo"]
	if !ok {
		t.Fatal("holo service is missing")
	}
	if holo.Image != holoImage {
		t.Fatalf("holo image = %q, want immutable %q", holo.Image, holoImage)
	}
	if _, ok := compose.Services["aiobfd"]; ok {
		t.Fatal("obsolete aiobfd service remains")
	}
	// Assert NET_RAW, NET_ADMIN, read-only holod.toml and holo.startup
	// mounts, 172.20.0.50, healthcheck, and the mandatory successful
	// holo-config dependency.
}
```

Read the two Holo files and assert the exact peer addresses, multiplier `3`,
interval values `300000`, stdout logging, disabled file/gNMI logging, and no
floating Holo tag.

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test -race -count=1 ./test/interop/
```

Expected: FAIL with `holo service is missing` against the current aiobfd
topology. Confirm at least one test executed.

- [ ] **Step 3: Add the Holo daemon and YANG configuration**

Create `holod.toml` from Holo v0.9.0 defaults with these deliberate changes:

```toml
user = "holo"
group = "holo"
database_path = "/var/opt/holo/holo.db"

[logging]
  [logging.journald]
    enabled = false
  [logging.file]
    enabled = false
    dir = "/var/log/"
    name = "holod.log"
    rotation = "never"
    style = "full"
    colors = false
    show_thread_id = false
    show_source = false
  [logging.stdout]
    enabled = true
    style = "full"
    colors = false
    show_thread_id = false
    show_source = false

[event_recorder]
  enabled = false
  dir = "/var/opt/holo"

[plugins]
  [plugins.grpc]
    enabled = true
    address = "0.0.0.0:50051"
    [plugins.grpc.tls]
      enabled = false
      certificate = "/etc/ssl/private/holo.pem"
      key = "/etc/ssl/certs/holo.key"
  [plugins.gnmi]
    enabled = false
    address = "0.0.0.0:10161"
    [plugins.gnmi.tls]
      enabled = false
      certificate = "/etc/ssl/private/holo.pem"
      key = "/etc/ssl/certs/holo.key"
```

Create `holo.startup` exactly as approved in the design: interface `eth0`, BFD
control-plane protocol `ietf-bfd-types:bfdv1`, source `172.20.0.50`, destination
`172.20.0.10`, multiplier `3`, and both intervals `300000` microseconds.

- [ ] **Step 4: Replace the Compose service**

Use the same immutable image for both services. `holo` mounts `holod.toml` at
`/etc/holod.toml:ro,z`, receives `NET_RAW` and `NET_ADMIN`, uses address
`172.20.0.50`, and has a bounded listener healthcheck:

```yaml
healthcheck:
  test: ["CMD-SHELL", "netstat -ltn | grep -q ':50051 '"]
  interval: 1s
  timeout: 1s
  retries: 15
  start_period: 2s
```

`holo-config` mounts `holo.startup`, depends on `holo` with
`condition: service_healthy`, overrides entrypoint with `holo-cli`, and runs:

```yaml
command: ["--address", "http://holo:50051", "--file", "/etc/holo.startup"]
```

Make `gobfd` depend on `holo-config` with
`condition: service_completed_successfully` for standards-compliant Compose
providers. podman-compose 1.5.0 and 1.6.0 translate that condition to `stopped`
and do not inspect the exit code, so the repository runner must not treat the
declarative edge as a success gate. It builds first, starts only `holo` and
`holo-config`, waits with a bounded `podman wait`, parses and cross-checks the
exact status from bounded `podman inspect`, and prints both services'
diagnostics on any wait, parse, mismatch, or non-zero failure. Only an exact
zero permits a second `up --no-deps` command to start GoBFD and the remaining
peers.

Delete the aiobfd Containerfile with `apply_patch`; do not use filesystem-wide
delete commands.

- [ ] **Step 5: Render Compose and verify GREEN**

Run:

```bash
bash -n test/interop/run.sh
podman-compose -f test/interop/compose.yml config
go test -race -count=1 ./test/interop/
go test -trimpath -race -count=1 ./test/interop/
```

Expected: the runner parses, Compose renders both Holo services, and both Go
contract variants pass.

- [ ] **Step 6: Commit the topology slice**

```bash
git add test/interop/holo/holod.toml test/interop/holo/holo.startup test/interop/topology_contract_test.go
git add -p -- test/interop/compose.yml test/interop/aiobfd/Containerfile
git diff --cached --name-only
git diff --cached
git commit -m "test: replace aiobfd topology with Holo"
```

Expected staged paths are only the five topology paths above.

- [ ] **Step 7: Commit the provider-contract correction**

```bash
git add test/interop/topology_contract_negative_test.go test/interop/runner_startup_contract_test.go
git add -p -- test/interop/topology_contract_test.go test/interop/run.sh docs/superpowers/plans/2026-08-21-aiobfd-to-holo-interop.md
git diff --cached --name-only
git diff --cached
git commit -m "test: fail closed on Holo configuration"
```

Expected staged paths are the two new contract files plus the three partially
staged correction files. The existing dependency-refresh edits in `run.sh`
remain unstaged.

### Task 2: Add Fresh Holo Lifecycle Evidence to the Go Suite

**Files:**
- Modify: `test/interop/interop_test.go`

- [ ] **Step 1: Write failing unit tests for current-state and frame-boundary helpers**

Add table-driven tests for parsing `gobfdctl session show --format json` and for
constructing a tshark filter that requires `frame.number > baseline`. The JSON
fixture must cover `Up/Up` and `Down/ControlTimeExpired`.

- [ ] **Step 2: Run targeted tests and verify RED**

Run:

```bash
go test -race -count=1 -tags interop -run 'Test(ParseSessionState|FrameBoundary)' ./test/interop/
```

Expected: FAIL because the parsing and boundary helpers do not exist.

- [ ] **Step 3: Implement minimal helpers**

Add a typed view matching current CLI JSON:

```go
type sessionState struct {
	PeerAddress     string `json:"peer_address"`
	LocalState      string `json:"local_state"`
	RemoteState     string `json:"remote_state"`
	LocalDiagnostic string `json:"local_diagnostic"`
}
```

Run `/bin/gobfdctl --addr 127.0.0.1:50051 session show <peer> --format json`
inside `gobfd-interop`, wrap command and JSON errors with context, and keep every
poll bounded by the test context. Add helpers for the last captured frame and a
new Holo `Up` packet strictly after that frame.

- [ ] **Step 4: Rename peer-specific tests and add failure/recovery behavior**

Rename constants, helpers, test names, log messages, packet filters,
discriminator maps, and peer tables from aiobfd to Holo. Add a serial Holo
lifecycle subtest that:

1. stops only `holo`;
2. waits for GoBFD `Down` plus `ControlTimeExpired`;
3. records the last Holo packet frame after `Down` and immediately before restart;
4. starts `holo` and reruns the one-shot `holo-config` service when its persisted configuration is unavailable;
5. requires GoBFD `Up/Up` and a new Holo `Up` packet after the post-Down frame;
6. registers best-effort recovery cleanup before the first mutation.

- [ ] **Step 5: Run unit, tagged compile, gopls, and tagged lint gates**

```bash
go test -race -count=1 ./test/interop/
go test -race -count=1 -run '^$' -tags interop ./test/interop/
make gopls-check
make lint-ci
```

Expected: ordinary contract tests pass, tagged package compiles with non-empty
input, gopls reports no diagnostics, and golangci-lint reports `0 issues`.

- [ ] **Step 6: Commit the Go lifecycle slice**

```bash
git add -p -- test/interop/interop_test.go
git diff --cached --name-only
git diff --cached
git commit -m "test: verify Holo BFD lifecycle"
```

Expected staged path is only `test/interop/interop_test.go`.

### Task 3: Update the Legacy Runner and Exact Podman Ownership

**Files:**
- Modify: `test/interop/run.sh`
- Modify: `test/interop/interop_test.go`
- Modify: `test/e2e/routing/run.sh`
- Modify: `test/e2e/targets.md`
- Modify: `Makefile`
- Modify: `test/interop/topology_contract_test.go`

- [ ] **Step 1: Extend the contract test and verify RED**

Add assertions that operational runners and inventories contain `holo` and
`holo-interop`, contain neither removed peer name, and use a deterministic
Compose project. Run the ordinary interop package and confirm the expected
failure against the old runner.

- [ ] **Step 2: Replace peer names and diagnostics mechanically**

Rename the shell constants, handshake functions, tshark filters, discriminator
variables, peer loops, banners, service lists, and E2E container inventory.
When readiness or handshake fails, print `holo`, `holo-config`, and
`/tmp/holod.err` diagnostics. Retain the Task 1 two-phase startup and exact
zero-exit check. Task 3 may add project ownership and broader diagnostics
around it, but must not collapse it back into a single Compose startup or rely
on the declarative dependency.

- [ ] **Step 3: Define and enforce exact project ownership**

Use `INTEROP_PROJECT_NAME` with a deterministic default in the Make target,
shell runner, tagged Go `podmanCompose` helper, and E2E environment. Derive the
Scapy network as `${INTEROP_PROJECT_NAME}_bfdnet` in both Go and shell instead
of retaining `interop_bfdnet`. Before startup, query containers, networks, and volumes carrying
`com.docker.compose.project=<project>` and fail if any exist. On every exit:

1. run `compose down --volumes --remove-orphans` for that exact project;
2. query those three resource classes again;
3. resolve remaining exact IDs from that label and remove only those labelled
   containers, networks, or volumes if Compose cleanup partially failed;
4. verify the label query is empty and fail cleanup if a resource remains;
5. never remove images or resources without the recorded project label.

Use bounded commands and preserve the original test exit status unless cleanup
itself proves an owned-resource leak.

After each tagged suite writes its JSON stream, enforce a non-zero test count
inside `test/e2e/routing/run.sh`:

```bash
jq -s -e '[.[] | select(.Action == "pass" and has("Test"))] | length > 0' \
  "${suite_dir}/go-test.json"
```

Treat a false result as a suite failure before artifact collection.

- [ ] **Step 4: Verify script and contract gates**

```bash
bash -n test/interop/run.sh test/e2e/routing/run.sh
go test -race -count=1 ./test/interop/
podman-compose -p gobfd-interop -f test/interop/compose.yml config
```

Expected: scripts parse, contract tests pass, and rendered Compose contains the
health-gated one-shot loader.

- [ ] **Step 5: Commit the runner slice**

```bash
git add -p -- Makefile test/interop/run.sh test/interop/interop_test.go test/e2e/routing/run.sh test/e2e/targets.md test/interop/topology_contract_test.go
git diff --cached --name-only
git diff --cached
git commit -m "test: harden Holo interop orchestration"
```

Expected staged paths are exactly the six paths listed above.

### Task 4: Remove the aiobfd Python Benchmark and Report Surface

**Files:**
- Delete: `bench/Containerfile.python`
- Delete: `bench/python/bench_aiobfd.py`
- Delete: `bench/python/bench_struct.py`
- Delete: `bench/python/requirements.txt`
- Modify: `bench/compose.yml`
- Modify: `scripts/gen-report.sh`
- Modify: `scripts/report-template.html`
- Modify: `Makefile`
- Modify: `test/interop/topology_contract_test.go`

- [ ] **Step 1: Add the failing operational-reference test**

Add a focused benchmark/report scan that rejects case-insensitive removed peer
or packet-library names under `bench/`, `scripts/gen-report.sh`, and
`scripts/report-template.html`. Confirm it reports the current benchmark and
report matches without keeping the whole ordinary test suite red through the
next documentation task.

- [ ] **Step 2: Remove the Python benchmark files with apply_patch**

Delete the complete `bench/python` surface and `bench/Containerfile.python`.
Remove the `bench-python` service and update Compose comments to the remaining
Go and C benchmark services.

- [ ] **Step 3: Remove report inputs and presentation columns**

Delete the removed environment variables, inline-Python dictionaries, merge
loops, headline card, feature fields, table columns, chart series, legends,
colors, and explanatory text. Keep Go, FRR-style C, and BIRD-style C behavior
unchanged.

- [ ] **Step 4: Verify the narrowed benchmark/report pipeline**

```bash
bash -n scripts/gen-report.sh
podman-compose -f bench/compose.yml config
go test -race -count=1 ./test/interop/
```

Expected: no Python benchmark service renders, the report script parses, and
the focused benchmark/report reference test passes.

Also remove `$(BENCH_DC) run --rm bench-python` from `benchmark-cross`; the
target must invoke only services that remain in `bench/compose.yml`.

- [ ] **Step 5: Commit the benchmark removal**

```bash
git add -p -- bench/Containerfile.python bench/compose.yml bench/python/bench_aiobfd.py bench/python/bench_struct.py bench/python/requirements.txt scripts/gen-report.sh scripts/report-template.html test/interop/topology_contract_test.go Makefile
git diff --cached --name-only
git diff --cached
git commit -m "bench: remove aiobfd Python comparisons"
```

Expected staged paths are exactly the paths listed above; unstage and stop on
any unrelated dependency-refresh path.

### Task 5: Update Active Documentation and the Repository Contract

**Files:**
- Modify: `AGENTS.md`
- Modify: `README.md`
- Modify: `Makefile`
- Modify: `test/interop/gobfd/gobfd.yml`
- Modify: `docs/en/README.md`, `docs/ru/README.md`
- Modify: `docs/en/01-architecture.md`, `docs/ru/01-architecture.md`
- Modify: `docs/en/05-interop.md`, `docs/ru/05-interop.md`
- Modify: `docs/en/11-integrations.md`, `docs/ru/11-integrations.md`
- Modify: `docs/en/13-competitive-analysis.md`, `docs/ru/13-competitive-analysis.md`
- Modify: `docs/en/14-performance-analysis.md`, `docs/ru/14-performance-analysis.md`
- Modify: `CHANGELOG.md`, `CHANGELOG.ru.md`
- Modify: `.cspell.json`
- Modify: `test/interop/topology_contract_test.go`

- [ ] **Step 1: Update the source contract and topology documentation**

Describe the four peers as FRR 10.7.0, BIRD 3.3.2, Holo 0.9.0, and Thoro/bfd.
Document the Holo one-shot YANG loader, fixed address, exact RFC coverage, and
amd64-only mandatory interop gate. Keep GoBGP and ExaBGP BGP-interoperability
claims separate from the base peer matrix.

- [ ] **Step 2: Replace competitive and performance claims**

Replace the active aiobfd competitor section and badge with Holo facts verified
from the v0.9.0 release/source. Remove all deleted Python benchmark tables,
file examples, and head-to-head claims; do not invent Holo performance numbers.
Keep EN/RU structure and facts equivalent.

- [ ] **Step 3: Add new changelog entries without rewriting history**

Under the current unreleased section, record removal of the abandoned Python
peer/bitstring benchmark and adoption of immutable Holo. Leave existing release
entries untouched.

- [ ] **Step 4: Extend the reference test to all operational surfaces**

Extend the focused scan to tracked operational text. The exact allowlist is
`CHANGELOG.md`, `CHANGELOG.ru.md`, `.cspell.json`, the approved migration design,
and this implementation plan. The test skips Git/Beads metadata and binary or
generated artifacts, and fails on every other match.

- [ ] **Step 5: Make the operational-reference test GREEN**

```bash
go test -race -count=1 ./test/interop/
rg -n -i 'aiobfd|bitstring' . --hidden --glob '!.git/**'
```

Expected: the Go test passes. `rg` returns only the two changelogs,
`.cspell.json`, the approved migration design, and this implementation plan.

- [ ] **Step 6: Run documentation and contract gates**

```bash
make lint-docs
git diff --check
```

Expected: Markdown, YAML, spelling, and whitespace gates pass.

- [ ] **Step 7: Commit the documentation slice**

```bash
git add -p -- .cspell.json AGENTS.md README.md Makefile test/interop/gobfd/gobfd.yml test/interop/topology_contract_test.go docs/en/README.md docs/ru/README.md docs/en/01-architecture.md docs/ru/01-architecture.md docs/en/05-interop.md docs/ru/05-interop.md docs/en/11-integrations.md docs/ru/11-integrations.md docs/en/13-competitive-analysis.md docs/ru/13-competitive-analysis.md docs/en/14-performance-analysis.md docs/ru/14-performance-analysis.md CHANGELOG.md CHANGELOG.ru.md
git diff --cached --name-only
git diff --cached
git commit -m "docs: document Holo interoperability"
```

Expected staged paths are exactly the paths listed above. Because the worktree
already contains dependency-refresh edits, never use broad `git add docs` or
`git add bench` staging.

### Task 6: Run Live Gates, Clean Up, and Close Beads

**Files:**
- Modify only if a gate exposes a defect in an already changed file.
- Update: Beads issue `gobfd-qj0.8.1.5.3`.

- [ ] **Step 1: Record preflight state and confirm no project collision**

Record Podman version, Compose provider/version, socket endpoint, and resources
for the exact interop project label. Stop if matching resources predate this
run. Do not remove unrelated resources.

- [ ] **Step 2: Run the complete static Go gate**

```bash
go version
go mod tidy -diff
go mod verify
go build ./...
go test ./... -race -count=1
go vet ./...
make gopls-check
make lint-ci
go run ./scripts/vuln-audit.go
```

Expected: Go `1.27.0`, clean module graph, all discovered packages tested, no
gopls/vet/lint findings, and only the repository's documented fail-closed
vulnerability policy is accepted.

- [ ] **Step 3: Prove both live interop gates**

```bash
make interop
make e2e-routing
```

Expected: both gates pass, the tagged Go output has a non-zero test count,
Holo reaches `Up/Up`, failure becomes `Down/ControlTimeExpired`, and recovery
uses a new post-boundary packet.

The E2E runner's Task 3 guard must reject a zero-test JSON stream inside
`make e2e-routing`; confirm the guard executed from the saved suite artifacts.

Before accepting the successful interop run, execute a live negative case with
a temporary Compose override that mounts an invalid Holo startup file. Require
the runner to exit non-zero, verify `gobfd-interop` was never created or
started, capture Holo and loader diagnostics, and clean up the exact test
project. This validates the real Podman lifecycle in addition to the
deterministic fake-command contract test.

- [ ] **Step 4: Verify post-run cleanup**

Query containers, networks, and volumes by the exact preflight-recorded Compose
project label. Expected: none. Confirm unrelated resources and images were not
removed. Remove only uniquely labelled audit resources created by this task.

- [ ] **Step 5: Run final repository gates**

```bash
make lint-docs
buf lint
git diff --check
git status --short --branch
bd preflight
```

Expected: all gates green; status contains only the intended broader
dependency-refresh work plus this task's committed slices.

- [ ] **Step 6: Update and close the durable issue**

Append exact versions, commands, test counts, lifecycle evidence, cleanup
evidence, and commit SHAs to `gobfd-qj0.8.1.5.3`. Close it only after every
acceptance condition is proven.

- [ ] **Step 7: Final review and local commit hygiene**

Dispatch whole-change spec and code-quality reviews. Fix and re-run gates until
both approve. Do not push or sync remote refs.
