# aiobfd to Holo Interop Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove aiobfd and its Python benchmark surface, then preserve the four-peer BFD interoperability gate with immutable Holo v0.9.0 and fresh lifecycle evidence.

**Architecture:** The base Compose topology keeps peer address `172.20.0.50` but assigns it to Holo. `holod` receives a test TOML configuration while a healthy-gated one-shot `holo-config` service applies the IETF BFD YANG commands through `holo-cli`. The legacy runner, guarded `make interop-up`, and routing E2E runner all start Holo in a separate bounded phase, require the loader's inspected exit status to be exactly zero, require its exact-ID log to be whitespace-only, verify the immutable Holo container reports exactly `Holo command-line interface 0.5.0`, and then run `--command 'show running format json'` before starting GoBFD. That semantic result must be exactly one JSON document containing exactly one matching `eth0` interface, one `bfdv1/main` protocol, and one exact session. The gate is mandatory because Holo CLI v0.5.0 reports startup-file parse errors but continues, commits the remaining candidate, and exits zero; podman-compose 1.5.0 and 1.6.0 also reduce `service_completed_successfully` to the `stopped` state without checking the exit code. tshark capture files live only in the exact container writable layer and are copied before cleanup; no mutable named or anonymous capture volume is part of either base or BGP topology. Go tests prove current daemon state and post-event packets rather than accepting stale capture history. Beads issue `gobfd-qj0.8.1.5.3` remains the durable source of task status.

**Tech Stack:** Go 1.27, Go test/race, gopls, golangci-lint v2, Podman,
Docker Compose v5.5.0 selected through `podman compose`, Holo v0.9.0,
RFC 5880/5881, RFC 9314 YANG, tshark.

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
- Create `test/internal/bfdjitter/` and `test/interop/scripts/bfdjitter/`:
  share one native Go parser between the tagged and shell jitter gates.
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
podman compose -f test/interop/compose.yml config
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

Add an execution fixture where exact-ID Podman exec succeeds with numeric tshark
rows on stdout while tshark writes its root-user warning on stderr. The parsed
rows must contain stdout only. A failing exec must retain stderr in the returned
diagnostic and preserve context cancellation identity.

- [ ] **Step 2: Run targeted tests and verify RED**

Run:

```bash
go test -race -count=1 -tags interop \
  -run 'Test(ParseSessionState|FrameBoundary|HoloDownBoundary|LifecycleDeadline|Poll|TsharkQuery)' \
  ./test/interop/
```

Expected: FAIL because the parsing, boundary, lifecycle-deadline, and polling
helpers do not exist and tshark queries do not yet preserve canceled or expired
context identity.

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
poll bounded by the test context. Add helpers for a pre-stop bidirectional Holo
frame baseline, a new GoBFD-originated `Down`/`ControlTimeExpired` frame strictly
after that baseline, and a new Holo-originated `Up` packet strictly after the
proven Down frame.

Keep the shared Podman helper's combined-output behavior for stop, inspect,
logs, and their diagnostics. At the tshark query boundary only, resolve the
owned container to its immutable ID, capture stdout and stderr separately,
return only stdout after a successful exec, and include stderr when exec fails.
This prevents `Running as user "root"...` from becoming a fake frame row while
retaining bounded context and `errors.Is` behavior.

Stop Holo through a narrow exact-ownership helper that resolves
`holo-interop` to its immutable ID and invokes
`podman stop --time 5 <exact-ID>`. Bound that command with a 15-second outer
context so Podman's five-second grace period cannot race an equal outer
deadline. Treat any stop error as a lifecycle failure and preserve its output;
do not silently continue into packet/state assertions.

Live `lifecycle-v3.json` evidence reached the valid pre-stop frame baseline 50,
then failed at about ten seconds with `stop only Holo service: signal: killed`;
best-effort restart consequently found the container in an improper state. The
former ten-second outer context equalled Podman's default ten-second stop grace,
so this explicit nested timeout contract is required before rerunning live.

Live `make interop` lifecycle-v4 evidence subsequently passed the Holo lifecycle
but exposed a false FRR jitter failure: a 4.438-second pause was the intentional
stop/restart and `AdminDown` boundary, while both old consumers discarded every
non-`Up` row before calculating deltas and therefore joined two separate `Up`
segments. Both consumers must instead pass the same complete display-filtered
stream as strict `frame.time_epoch,bfd.sta,bfd.flags.p,bfd.flags.f` TSV to one
native Go analyzer; neither a shell `head` pipeline nor tshark's pre-display
filter packet count may truncate it. Any non-`Up` state resets the previous
ordinary `Up` timestamp, so only packets inside an uninterrupted `Up` segment
contribute samples. Malformed rows and tshark producer failures fail closed, a
continuous-`Up` delta over 400 ms still fails, and fewer than ten `Up` packets
or five eligible samples retains the existing explicit skip. Poll packets stay
in the periodic sequence because RFC 5880 section 6.5 requires Poll Sequence
packets to use scheduled transmissions; therefore an ordinary or Poll 50 ms
`Up` interval remains a failure and advances the baseline. Only a Final flag
set by canonical tshark `True` or the exact numeric compatibility value `1`
receives the section 6.8.7 timer exemption and does not advance the last
periodic `Up` baseline, so it cannot hide a long gap. Flag parsing accepts only
canonical `False`/`True` and exact compatibility `0`/`1`; lowercase, padded,
hex, missing, and other values fail closed. A packet with both Poll and Final
set in any accepted encoding violates section 6.5 and fails closed. The shell
runner invokes the analyzer with `go -C` so direct execution is independent of
the caller's working directory and contains no inline Python jitter logic.

Live `make interop` after commit `2e294f3` reached this gate and then failed
closed on every live tshark 4.4.16 row because its Boolean fields are emitted
as exact `False`/`True`, while the first parser accepted only numeric values.
The retained `interop-full-v2.log` records those Poll diagnostics. Cleanup then
verified zero exact-project containers, networks, and volumes and all fixed
container names absent. This is negative parser evidence, not a successful
interop acceptance run.

Live `interop-full-v3` after the Boolean fix proved that parsing now works:
BIRD3, Holo, and Thoro remained within 0.225--0.301 seconds, while FRR failed
with maximum 0.498653 seconds across 142 samples. The failure is orchestration
contamination, not justification to widen the threshold or discard the gap:
the read-only jitter gate ran after the same capture had recorded FRR
stop/restart, AdminDown/recovery, and a 200 ms transmit-interval change followed
by restoration to 300 ms. Both the shell Phase 2 and tagged RFC test must run
jitter before Session Independence, stop/restart, AdminDown, and parameter
change mutations while preserving the existing explicit insufficient-sample
contract. The live v3 cleanup again verified zero exact-project containers,
networks, and volumes. A fresh full live pass is still required after this
ordering correction.

- [ ] **Step 4: Rename peer-specific tests and add failure/recovery behavior**

Rename constants, helpers, test names, log messages, packet filters,
discriminator maps, and peer tables from aiobfd to Holo. Add a serial Holo
lifecycle subtest that:

1. registers best-effort recovery cleanup before the first mutation;
2. records the current last bidirectional Holo frame as the pre-stop baseline;
3. stops only `holo` with the exact-ID five-second Podman grace period inside
   the 15-second outer bound;
4. waits for current GoBFD state `Down` plus `ControlTimeExpired`;
5. requires a new GoBFD-originated `Down`/diagnostic-1 packet whose frame number
   is strictly greater than the pre-stop baseline and retains that proven Down
   frame as the recovery boundary;
6. starts `holo` and reruns the one-shot `holo-config` service when its persisted
   configuration is unavailable;
7. requires current GoBFD state `Up/Up` and a new Holo-originated `Up` packet
   whose frame number is strictly greater than the proven Down frame.

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
- Modify: `test/interop/runner_startup_contract_test.go`
- Modify: `docs/superpowers/plans/2026-08-21-aiobfd-to-holo-interop.md`

- [ ] **Step 1: Extend the contract test and verify RED**

Add assertions that operational runners and inventories contain `holo` and
`holo-interop`, contain neither removed peer name, and use a deterministic
Compose project. Extend the fake-command runner contract with exact-label
queries for containers, networks, and volumes. Assert that all three preflight
queries precede build and startup, that a collision in any class prevents every
Compose mutation, and that a collision is never cleaned as though this run
owned it. Run the ordinary interop package and confirm the expected failure
against the old runner.

- [ ] **Step 2: Replace peer names and diagnostics mechanically**

Rename the shell constants, handshake functions, tshark filters, discriminator
variables, peer loops, banners, service lists, and E2E container inventory.
When readiness or handshake fails, print `holo`, `holo-config`, and
`/tmp/holod.err` diagnostics. Retain the Task 1 two-phase startup and exact
zero-exit check. Task 3 may add project ownership and broader diagnostics
around it, but must not collapse it back into a single Compose startup or rely
on the declarative dependency.

Apply the same provider-contract gate to the base suite in
`test/e2e/routing/run.sh`: after preflight and build, start only `holo` and
`holo-config`, run bounded `podman wait` and bounded `podman inspect`, require
both results to be numeric, equal, and exactly zero, and only then run
`up -d --no-deps gobfd frr bird3 tshark thoro`. Keep the BGP suite on its
generic startup path. On every base-suite startup or test failure, continue
through artifact collection and save bounded `holo` and `holo-config` logs plus
`/tmp/holod.err` read with `podman exec holo-interop` when the container exists.

- [ ] **Step 3: Define and enforce exact project ownership**

Use `INTEROP_PROJECT_NAME` with a deterministic default in the Make target,
shell runner, tagged Go exact-container helper, and E2E environment. Derive the
Scapy network as `${INTEROP_PROJECT_NAME}_bfdnet` in both Go and shell instead
of retaining `interop_bfdnet`. Before any build or startup command, query
containers, networks, and volumes carrying
`com.docker.compose.project=<project>` and fail if any exist. Only after all
three queries prove the project empty may the runner record ownership, build,
and enter the preserved two-phase Holo startup.

The implementation intentionally deviates from the original Compose-down-first
cleanup contract. Local provider evidence shows that installed
`podman compose down` acts on configured `container_name` values, so even a
successful label revalidation has a name-swap TOCTOU window. Compose is
therefore limited to image build and container creation while the project lock
is held. On every owned-project exit, query all three resource classes and
fail before mutation if a labelled volume exists because Podman exposes only
its mutable name. Otherwise snapshot immutable container and network IDs, then
inspect every initial full container ID before the first mutation. Each inspect
must return that exact ID, the expected project label, a valid mounts array, and
no `Type=volume` mount; this catches anonymous or unlabelled volumes that a
labelled volume query cannot see. Remove only the validated recorded IDs,
re-query all three classes, and fail if any exact-labelled resource remains.
Never invoke Compose `down`, remove an image, or mutate a resource absent from
the snapshot.

Use bounded commands and preserve the original test exit status unless cleanup
itself proves an owned-resource leak.

The fake collision cases for containers, networks, and volumes must reject
every Compose invocation and reject `podman rm`,
`podman network rm`, and `podman volume rm`. A collided project was never
acquired by the run and must not enter either normal or fallback cleanup.

Serialize acquisition of each validated project with a nonblocking `flock`
held from before exact-label and fixed-name preflight through final cleanup
verification. Prefer an owned, writable, non-symlink XDG runtime directory or
`/run/user/$UID`, and require preferred bases to have mode `0700`. Reject either
preferred base when it is group/world writable. A fallback base must either be
owned and not group/world writable, or be root-owned with the sticky bit; its
dedicated lock directory must be owned, mode `0700`, writable, and not a
symlink. Keep the mode `0600` lock file
after release so two contenders can never split onto different inodes. The E2E
runner holds a separate descriptor for each acquired project and closes it only
after that project's exact cleanup. A runner that cannot acquire the lock must
perform no Podman or Compose mutation.

Before startup, reject every configured fixed `container_name` that already
exists, even when the exact project-label query is empty. Container inventory,
diagnostics, and every runtime `logs`, `exec`, `stop`, or `start` operation must
resolve the exact project label to an immutable container ID and must never act
on a foreign container through a fixed name. This includes every tagged BGP
Podman API `exec`, `stop`, `start`, `pause`, `unpause`, and `logs` operation;
the dedicated ownership error must preserve `errors.Is` identity. Direct tagged
test targets use a `lock-run -- <argv>` guard that requires every mandatory
base or BGP container to exist with the exact project label. An arbitrary
labelled container, network, or volume is insufficient; only Scapy is optional
for the base suite. The E2E runner invokes its tagged tests under the lock it
already holds, and the direct BGP target propagates the derived BGP project name
into the dev-container test environment.

Freeze the raw command-line `INTEROP_PROJECT_NAME` with GNU Make `value` before
the first `shell` expansion, reject nested Make function syntax before export,
and then export it. Every base interop and routing target
must depend on one shared validation prerequisite. Recipes pass the exported
value only through quoted shell expansion (`$${INTEROP_PROJECT_NAME}`); they
must never inject `$(INTEROP_PROJECT_NAME)` into shell source. Direct up, down,
logs, capture, and pcap targets use the same lock, fixed-name, and exact-label
guard as the full runners.

Artifact merging uses a collision-resistant per-run
`io.gobfd.e2e.merge-owner` label rather than a Compose project label. Preflight
that label, run `mergecap`, then snapshot the exact labelled container IDs.
Before removal, apply the same full-ID, exact-label, and no-volume-mount inspect
validation used for project containers, then remove only that validated
snapshot and verify absence even when `podman run` times out or fails.
Create the routing report/run ID before acquiring either project lock from UTC
nanoseconds plus the runner PID, and derive the merge ownership value from that
same validated ID so concurrent starts within one second cannot share a report
path or merge label.

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
podman compose -p gobfd-interop -f test/interop/compose.yml config
```

Expected: scripts parse, contract tests pass, and rendered Compose contains the
health-gated one-shot loader.

- [ ] **Step 5: Commit the runner slice**

```bash
git add -p -- Makefile test/interop/run.sh test/interop/interop_test.go test/e2e/routing/run.sh test/e2e/targets.md test/interop/topology_contract_test.go test/interop/runner_startup_contract_test.go docs/superpowers/plans/2026-08-21-aiobfd-to-holo-interop.md
git diff --cached --name-only
git diff --cached
git commit -m "test: harden Holo interop orchestration"
```

Expected staged paths are exactly the eight paths listed above.

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
podman compose -f bench/compose.yml config
go test -race -count=1 ./test/interop/
```

Expected: no Python benchmark service renders, the report script parses, and
the focused benchmark/report reference test passes.

The Go contract test must invoke the Compose provider with the absolute
`filepath.Join(root, "bench", "compose.yml")` path. The development image uses
podman-compose 1.3.0, which does not reliably resolve a relative `-f` path from
`Cmd.Dir`; an executable fake-provider fixture verifies the exact absolute
argument vector without changing the benchmark lifecycle.

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
and this implementation plan. Prefer the Git tracked-file list. When mounted
worktree metadata is unavailable, use a bounded standard-library working-tree
walk that skips only the top-level `.git`, `.beads`, and `reports` metadata or
artifact trees. Both modes reject symlinks, non-regular and oversized entries,
validate exact generated-file markers, skip NUL-bearing binary content, and
fail on every other match. The historical allowlist exempts only the final
removed-reference comparison; it never bypasses path normalization, file type,
size, bounded reading, or generated-marker validation. Open the repository with
Go 1.27 `os.OpenRoot`, perform the initial `Root.Lstat`, read only from a rooted
opened handle through `io.LimitReader(max+1)`, and compare both the opened file
and a second `Root.Lstat` with `os.SameFile`. This rejects in-root symlink swaps
despite `Root.Open` following symlinks and prevents an initial-size check from
leading to an unbounded allocation if the file grows or is replaced.

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
run. Record the same exact-label state for `gobfd-interop-negative`,
`gobfd-interop-bgp`, and the dev project `v062-testcontainers`, plus existing
container, network, volume, and image IDs. Record free space before building
the Go 1.27 dev image. Stop on any project or fixed-name collision; do not
remove unrelated resources.

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

Before the positive gates, execute a live negative case under the unique
`gobfd-interop-negative` project. Create an absolute regular non-symlink
temporary Compose override that replaces the `/etc/holo.startup` mount with an
invalid startup file. Pass it only through the validated
`INTEROP_COMPOSE_OVERRIDE_FILE` environment variable. Bracket the runner with
nanosecond UTC timestamps and save exact Podman events:

```bash
set -euo pipefail
TASK6_ARTIFACT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gobfd-task6.XXXXXXXX")"
INVALID_STARTUP="${TASK6_ARTIFACT_DIR}/invalid-holo.startup"
INVALID_OVERRIDE="${TASK6_ARTIFACT_DIR}/invalid-holo.override.yml"
NEGATIVE_LOG="${TASK6_ARTIFACT_DIR}/negative.log"
NEGATIVE_EVENTS="${TASK6_ARTIFACT_DIR}/negative-events.json"
HOLO_RUNNING_JSON="${TASK6_ARTIFACT_DIR}/holo-running.json"
HOLO_VERSION_TXT="${TASK6_ARTIFACT_DIR}/holo-version.txt"
LIFECYCLE_JSON="${TASK6_ARTIFACT_DIR}/lifecycle.json"
E2E_LOG="${TASK6_ARTIFACT_DIR}/e2e-routing.log"
PODMAN=(timeout 2m podman)
source ./test/interop/project_guard.sh

printf '%s\n' 'this is deliberately invalid Holo CLI input' >"${INVALID_STARTUP}"
printf '%s\n' \
  'services:' \
  '  holo-config:' \
  '    volumes:' \
  "      - ${INVALID_STARTUP}:/etc/holo.startup:ro,z" \
  >"${INVALID_OVERRIDE}"
test -f "${INVALID_OVERRIDE}"
test ! -L "${INVALID_OVERRIDE}"
test -r "${INVALID_OVERRIDE}"

negative_started="$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)"
set +e
INTEROP_PROJECT_NAME=gobfd-interop-negative \
INTEROP_COMPOSE_OVERRIDE_FILE="${INVALID_OVERRIDE}" \
  ./test/interop/run.sh >"${NEGATIVE_LOG}" 2>&1
negative_status="$?"
set -e
negative_finished="$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)"

test "${negative_status}" -ne 0
grep -Fq '=== Holo daemon logs ===' "${NEGATIVE_LOG}"
grep -Fq '=== Holo daemon /tmp/holod.err ===' "${NEGATIVE_LOG}"
grep -Fq '=== Holo configuration loader logs ===' "${NEGATIVE_LOG}"
grep -Fq 'Holo running configuration is missing the required BFD session' \
  "${NEGATIVE_LOG}"
"${PODMAN[@]}" events --stream=false --since "${negative_started}" \
  --until "${negative_finished}" --format json >"${NEGATIVE_EVENTS}"
jq -s -e --arg project gobfd-interop-negative '
  def owned:
    .Type == "container"
    and .Attributes["com.docker.compose.project"] == $project;
  any(.[]; owned and .Name == "holo-interop" and .Status == "create")
  and any(.[]; owned and .Name == "holo-interop" and .Status == "start")
  and any(.[]; owned and .Name == "holo-config-interop" and .Status == "create")
  and any(.[]; owned and .Name == "holo-config-interop" and .Status == "start")
  and any(.[];
    owned
    and .Name == "holo-config-interop" and .Status == "died"
    and (.ContainerExitCode | type) == "number"
    and .ContainerExitCode == 0
  )
  and (any(.[];
    owned
    and .Name == "gobfd-interop"
    and (.Status == "create" or .Status == "start")
  ) | not)
' "${NEGATIVE_EVENTS}"
interop_verify_project_absent gobfd-interop-negative
```

Require the log to contain Holo daemon, loader, `/tmp/holod.err`, and the stable
semantic-configuration diagnostic. The deliberately invalid file is expected
to create and start both Holo containers and produce a numeric loader
`ContainerExitCode` of exactly zero: Holo CLI v0.5.0 treats individual parse
errors as non-fatal and commits the remaining empty candidate. The runner must
therefore fail on the empty running configuration, before any GoBFD create or
start event. Verify the exact negative project has no remaining containers,
networks, or volumes. Absence after cleanup is not evidence that GoBFD never
started; the exact Podman event assertion above supplies that evidence.

Then start the base topology explicitly and run the lifecycle alone under the
project lock so its 180-second lifecycle plus 75-second cleanup reservation
fits honestly within the package timeout. Run this block in a dedicated Bash
subshell so `make interop-down` always executes:

```bash
(
  set -euo pipefail
  export INTEROP_PROJECT_NAME=gobfd-interop
  lifecycle_owned=false
  cleanup_lifecycle() {
    if [ "${lifecycle_owned}" = true ]; then
      make interop-down
    fi
  }
  trap cleanup_lifecycle EXIT
  make interop-up
  lifecycle_owned=true
  ./test/interop/projectctl.sh lock-run -- bash -c '
    set -euo pipefail
    PODMAN=(timeout 2m podman)
    source "$1"
    holo_id="$(interop_resolve_project_container_id "$2" holo-interop)"
    holo_version="$("${PODMAN[@]}" exec "${holo_id}" holo-cli --version)"
    test "${holo_version}" = "Holo command-line interface 0.5.0"
    printf "%s\n" "${holo_version}" >"$3"
    "${PODMAN[@]}" exec "${holo_id}" \
      holo-cli --no-colors --no-pager \
      --address http://127.0.0.1:50051 \
      --command '\''show running format json'\''
  ' task6-running-config ./test/interop/project_guard.sh \
    "${INTEROP_PROJECT_NAME}" "${HOLO_VERSION_TXT}" \
    >"${HOLO_RUNNING_JSON}"
  grep -Fxq 'Holo command-line interface 0.5.0' "${HOLO_VERSION_TXT}"
  jq -s -e '
    length == 1
    and (.[0]
      | [
          .["ietf-interfaces:interfaces"].interface[]?
          | select(
              .name == "eth0"
              and .type == "iana-if-type:ethernetCsmacd"
              and (.["ietf-ip:ipv4"] | type) == "object"
            )
        ] as $interfaces
      | [
          .["ietf-routing:routing"]["control-plane-protocols"]
            ["control-plane-protocol"][]?
          | select(.type == "ietf-bfd-types:bfdv1" and .name == "main")
        ] as $protocols
      | [
          $protocols[]?
          | .["ietf-bfd:bfd"]["ietf-bfd-ip-sh:ip-sh"].sessions.session[]?
          | select(
              .interface == "eth0"
              and .["dest-addr"] == "172.20.0.10"
              and .["source-addr"] == "172.20.0.50"
              and .["local-multiplier"] == 3
              and .["desired-min-tx-interval"] == 300000
              and .["required-min-rx-interval"] == 300000
            )
        ] as $sessions
      | ($interfaces | length) == 1
        and ($protocols | length) == 1
        and ($sessions | length) == 1)
  ' "${HOLO_RUNNING_JSON}"
  ./test/interop/projectctl.sh lock-run -- \
    go test -json -tags interop -count=1 -timeout 300s \
      -run '^TestHoloFailureRecoveryLifecycle$' ./test/interop/ \
      >"${LIFECYCLE_JSON}"
  jq -s -e '
    any(.[]; select(.Action == "pass" and .Test == "TestHoloFailureRecoveryLifecycle"))
    and (any(.[]; select(.Action == "skip" and .Test == "TestHoloFailureRecoveryLifecycle")) | not)
  ' "${LIFECYCLE_JSON}"
)
interop_verify_project_absent gobfd-interop
```

Require the targeted invocation to execute and pass, not skip, and verify
exact-label cleanup after the trap. Then run the broader live gates:

```bash
make interop
make e2e-routing | tee "${E2E_LOG}"
E2E_REPORT_DIR="$(sed -n 's/^S10.3 routing E2E artifacts: //p' \
  "${E2E_LOG}" | tail -n 1)"
test -d "${E2E_REPORT_DIR}"
for suite in interop interop-bgp; do
  jq -s -e \
    '([.[] | select(.Action == "pass" and (.Test // "") != "")] | length) > 0' \
    "${E2E_REPORT_DIR}/${suite}/go-test.json"
done
MERGE_OWNER_VALUE="$(jq -er '.run_id' "${E2E_REPORT_DIR}/environment.json")"
```

Expected: both gates pass, the tagged Go output has a non-zero test count,
Holo reaches `Up/Up`, failure becomes `Down/ControlTimeExpired`, and recovery
uses a new post-boundary packet.

The E2E runner's Task 3 guard must reject a zero-test JSON stream inside
`make e2e-routing`; confirm both `interop/go-test.json` and
`interop-bgp/go-test.json` contain a non-zero number of passed named tests and
record the exact counts. Save the E2E run ID so the corresponding
`io.gobfd.e2e.merge-owner` label can be verified absent.

Live `make e2e-routing` report
`reports/e2e/routing/20260821T061833542286517Z-1360406` proved the protocol
tests themselves pass, but the enclosing ordinary contract suite still failed
portability checks. Its mounted worktree `.git` file pointed to an inaccessible
host common directory, so `git ls-files` could not enumerate operational text;
the same development environment's podman-compose 1.3.0 failed to resolve the
relative `-f bench/compose.yml` despite `Cmd.Dir=/app`, while the absolute path
worked. This is negative E2E acceptance evidence. The operational scanner must
fall back only on Git metadata failure to the bounded fail-closed walk above,
and the benchmark Compose contract must use the absolute repository path. A
fresh complete E2E run remains required after this correction.

Review of the first portability correction found that an allowlisted path
returned before every structural check and that `Lstat` followed by
`os.ReadFile` left a replacement/growth window with unbounded allocation.
Git-first and forced-fallback fixtures must therefore prove allowlisted
symlink, non-regular, oversized, and invalid-generated entries still fail.
A deterministic post-`Lstat` replacement with another regular inode must reach
the `os.SameFile` identity mismatch rather than stopping at the symlink branch.
A test-only hook placed after opened-handle `Stat`, the second `Root.Lstat`, and
both identity comparisons must then grow that same opened inode beyond the
limit before reading, proving the `io.LimitReader(max+1)` and post-read size
failure branch. The normal wrapper always supplies a nil hook. This correction
is required before the fresh E2E run; the rejected implementation is not Task
6 acceptance evidence.

Live routing E2E report
`reports/e2e/routing/20260821T065957096714731Z-1606785` proved both protocol
suites green: base interop recorded 257 named passes and zero failures, while
BGP+BFD recorded five named passes and zero failures. Exact cleanup verified
the base project, BGP project, and merge-owner container set empty. The overall
gate still failed during artifact merge because the runner used the mutable,
nonexistent `localhost/interop_tshark:latest` tag even though the exact owned
tshark container exposed its immutable image ID. This is protocol-positive but
artifact-negative evidence, not complete Task 6 acceptance.

Artifact collection must inspect `{{.Image}}` from each exact owned tshark
container ID, accept only one lowercase 64-hex image ID, prove that exact image
exists, and persist it in the suite artifact directory through the exact owned
dev container and Go helper. Merge reads the base ID through that same helper,
rechecks the image, and passes only that ID to the merge-owner-labelled
`podman run`; image-ID and JSON reads/writes use only the rooted helper, so they
do not rely on shell redirection or repeated path reads, and no tag or image
name is a mutation target. Pcap redirection is limited to freshly created,
trusted, unique-`RUN_ID` suite directories; before bind/merge, both capture
paths must independently be regular, non-symlink, and nonempty.
Replace the touched inline Python inventory merge with the stdlib-only
`test/internal/routingartifacts` package and thin routing CLI. Each input is a
bounded, regular, non-symlink file containing exactly one JSON array. Output
rejects symlink/non-regular destinations and is published mode 0600 through a
same-directory temporary file, sync, close, and atomic rename. Invoke the CLI
with exact argv through the exact owned dev container so shell data never
becomes program source. Both JSON and image-ID reads compare the initial
`Lstat`, opened handle, and a second `Lstat` with `os.SameFile` before one
bounded handle read. Image-ID output uses the same atomic writer and requires
exactly 64 lowercase hex characters plus one newline. A fresh E2E run remains
required after this correction.

Spec review of the first artifact correction found two additional fail-open
paths. `run_suite` ignored every `collect_pcap` error, and the copy/decode
commands themselves used `|| true`; later, `merge_artifacts` treated a missing
or empty suite capture as a reason to skip `mergecap` and still returned zero.
Execution RED fixtures must cover image inspect, capture copy, and tshark
decode failures through the complete suite status, plus missing and empty base
and BGP image-ID/capture artifacts through merge cleanup. Collection now
preserves producer stderr, adds a stable diagnostic, and returns nonzero;
`run_suite` records that failure. Merge independently requires both valid suite
image-ID artifacts and both nonempty regular, non-symlink captures before the
exact-ID `mergecap` run. Merge-owner collision performs no mutation, while
mergecap, exact removal, and final absence failures all remain nonzero and the
exact cleanup sequence runs for every post-collision failure.

The helper's security boundary is one trusted, owned, absolute report
directory passed to `os.OpenRoot`. The report-root path itself is explicitly
trusted before opening; `os.OpenRoot` may follow a symlink in that root name,
so the contract does not claim otherwise. Every artifact argument after that
boundary is a clean local relative path. Each internal directory component is
individually `Lstat`-checked as a non-symlink directory, opened as a nested
`os.Root`, identity-checked with `os.SameFile`, retained by descriptor, and
rechecked before bounded reads or atomic rename. Final inputs retain the
initial/opened/second-`Lstat` identity and `LimitReader(max+1)` growth checks.
Outputs use rooted `OpenFile(O_CREATE|O_EXCL)` for a mode-0600 temporary,
sync/close it, reject ancestor or destination replacement observed before the
final snapshot, and use descriptor-relative `Rename` in the pinned directory.
A final-entry symlink introduced after that snapshot is safely replaced by
rename rather than followed; its target stays unchanged. After rename, the
ancestor chain is checked again and final rooted `Lstat` must identify the
inode recorded from the temporary handle. A deterministic post-snapshot
directory swap must therefore return nonzero even if rename reached the pinned
former directory. Tests include traversal, absolute paths,
internal component symlinks, same-inode growth, regular replacement, ancestor
directory replacement, rejected pre-snapshot destination swaps, and safe
post-snapshot final-entry replacement.

Spec re-review also proved that `Root.OpenFile(..., 0600)` alone does not
guarantee exact mode because the process umask is still applied. Running the
artifact test binary under umask `0777` produced a mode-000 merged artifact.
The opened temporary handle must therefore be `Chmod(0600)` before any write,
with close and rooted removal errors preserved if chmod fails. Post-rename
validation additionally requires the published inode mode to be exactly 0600.
The restrictive-umask regression must wrap test-binary execution through
`go test -exec`; applying the umask only while compiling would not exercise
runtime artifact creation.

Quality review requires that restrictive-umask proof to be an automated Linux
test rather than only a recorded command. The parent test resolves its current
test executable and launches a separately named, environment-guarded child
through exact `sh -c 'umask 0777; exec "$@"'` argv. The child performs the
merge, requires mode 0600, and emits a marker that the parent must observe;
the separate test name prevents recursion. Non-Linux platforms report an
explicit skip while Linux treats missing shell, child failure, missing marker,
or wrong mode as failure. Bounded input reading also preserves simultaneous
read and close errors with contextual wrappers joined through `errors.Join`;
a local injected `io.ReadCloser` test requires both causes through `errors.Is`
without mutable production hooks.

Quality re-review found the child setup itself was root-dependent: umask 0777
also reduced `t.TempDir` and fixture-input permissions to mode 000, which root
could traverse and read but an unprivileged test process could not. The child
must immediately `Chmod` its owned fixture directory to 0700, create the input,
`Chmod` that file to 0600, and assert both modes before calling `Merge`. It must
also assert that the output path is absent before merge, so the exact-0600
result can only come from runtime artifact creation. These executable
preconditions remove reliance on root permission bypass without mutating users
or requiring a privileged/container topology test.

- [ ] **Step 4: Verify post-run cleanup**

Query containers, networks, and volumes by every exact preflight-recorded
Compose project label: `gobfd-interop-negative`, `gobfd-interop`,
`gobfd-interop-bgp`, and `v062-testcontainers`. Expected: none. Use the shared
exact-label cleanup helper only for a project proven empty before this run and
acquired by this task; never use Compose down. Query the recorded
`io.gobfd.e2e.merge-owner` value and require zero containers. Stop and remove
the exact dev container and other dev project resources created by the Go 1.27
gates. Confirm all unrelated preflight resource and image IDs remain; do not
prune or remove shared images.

Use the already loaded exact-label helpers for the fail-closed postcondition;
do not substitute name matching or an unlocked Compose lifecycle:

```bash
interop_cleanup_project_resources v062-testcontainers
for project in \
  gobfd-interop-negative gobfd-interop gobfd-interop-bgp v062-testcontainers
do
  interop_verify_project_absent "${project}"
done
interop_verify_labelled_containers_absent \
  io.gobfd.e2e.merge-owner "${MERGE_OWNER_VALUE}"
printf 'Task 6 retained evidence in %s\n' "${TASK6_ARTIFACT_DIR}"
```

Project cleanup snapshots immutable container and network identifiers exactly
once. It refuses to mutate anything if the initial exact-project query finds a
labelled volume, because Podman exposes only its mutable name. Before mutation,
the generic container-snapshot validator inspects every initial full container
ID and requires the inspected ID, requested label key/value, and mounts schema
to match while rejecting every
`Type=volume` mount, including anonymous or unlabelled volumes invisible to
`podman volume ls --filter label=...`; guarded projects must use container
storage or bind mounts. Project and merge-owner cleanup both invoke this
validator before their first removal. Project cleanup removes the validated
container snapshot in bounded repeated passes so a child deleted later in the
initial order can unblock its parent, then removes only the original network
snapshot. A non-zero container removal is not sticky when
`podman container exists <exact-ID>` returns 1 afterward; any other
existence-check error fails closed. A pass with no absent exact ID fails without
touching networks, and no later label query may introduce a new mutation target.
There is no `podman volume rm` cleanup path.

Retain that exact temporary directory until its commands, versions, test
counts, event proof, and cleanup proof are appended to Beads. Remove only the
printed `TASK6_ARTIFACT_DIR` path during the final user-authorized cleanup;
never use a wildcard or delete it before the evidence is recorded.

- [ ] **Step 5: Run final repository gates**

```bash
make lint-docs
make proto-lint
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
