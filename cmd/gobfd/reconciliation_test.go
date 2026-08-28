package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/grpchealth"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestReconciliationCoordinatorValidNoopThenInvalidCandidate(t *testing.T) {
	t.Parallel()

	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(slog.New(slog.DiscardHandler), checker)
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)

	initial := coordinator.Snapshot()
	if initial.DesiredGeneration != 0 || initial.AppliedGeneration != 0 || !initial.Stale ||
		initial.Pending != 0 || initial.Failed != 0 || initial.LastReceipt != (generationReceipt{}) {
		t.Fatalf("initial snapshot = %+v, want stale zero-generation status", initial)
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusNotServing)
	assertNamedHealthServing(t, checker)

	if err := coordinator.reconcile(
		context.Background(), config.DefaultConfig(), mgr,
		newNthFailureDeclarativeSenderFactory(0), &overlayRuntime{}, level,
	); err != nil {
		t.Fatalf("reconcile valid no-op candidate: %v", err)
	}

	snapshot := coordinator.Snapshot()
	if snapshot.DesiredGeneration != 1 || snapshot.AppliedGeneration != 1 {
		t.Fatalf("no-op generations = desired %d applied %d, want 1/1",
			snapshot.DesiredGeneration, snapshot.AppliedGeneration)
	}
	if snapshot.Stale || snapshot.Pending != 0 || snapshot.Failed != 0 {
		t.Errorf("no-op snapshot = %+v, want converged", snapshot)
	}
	if snapshot.LastReceipt.Generation != 1 {
		t.Errorf("receipt generation = %d, want 1", snapshot.LastReceipt.Generation)
	}
	if got := len(snapshot.LastReceipt.Sources); got != sourceCount {
		t.Fatalf("source receipts = %d, want %d", got, sourceCount)
	}
	for i, source := range reconciliationSources() {
		receipt := snapshot.LastReceipt.Sources[i]
		if receipt.Source != source {
			t.Errorf("source receipt %d = %v, want %v", i, receipt.Source, source)
		}
		if receipt.Created != 0 || receipt.Released != 0 || receipt.Pending != 0 || receipt.Failed != 0 {
			t.Errorf("source %v no-op receipt = %+v, want zero counts", source, receipt)
		}
		if receipt.Errors != (reconciliationErrorHistogram{}) {
			t.Errorf("source %v no-op errors = %+v, want empty", source, receipt.Errors)
		}
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusServing)
	assertNamedHealthServing(t, checker)

	invalid := config.DefaultConfig()
	invalid.Sessions = []config.SessionConfig{{
		Peer: "not-an-address", Local: "127.0.0.1", Interface: "lo",
	}}
	if err := coordinator.reconcile(
		context.Background(), invalid, mgr,
		newNthFailureDeclarativeSenderFactory(0), &overlayRuntime{}, level,
	); err == nil {
		t.Fatal("invalid complete candidate succeeded")
	}

	if got := coordinator.Snapshot(); got != snapshot {
		t.Errorf("snapshot changed after invalid candidate: got %+v want %+v", got, snapshot)
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusServing)
	assertNamedHealthServing(t, checker)
}

func TestReconciliationCoordinatorPartialThenExplicitRetry(t *testing.T) {
	t.Parallel()

	logs := new(lockedBuffer)
	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(
		slog.New(slog.NewTextHandler(logs, nil)), checker,
	)
	mgr := bfd.NewManager(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(mgr.Close)
	factory := newNthFailureDeclarativeSenderFactory(1)
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{{
		Peer: "192.0.2.210", Local: "127.0.0.1", Interface: "lo",
	}}

	if err := coordinator.reconcile(
		context.Background(), cfg, mgr, factory, &overlayRuntime{}, level,
	); err != nil {
		t.Fatalf("reconcile valid partial candidate: %v", err)
	}
	partial := coordinator.Snapshot()
	if partial.DesiredGeneration != 1 || partial.AppliedGeneration != 0 || !partial.Stale {
		t.Fatalf("partial generations = desired %d applied %d stale %t, want 1/0/true",
			partial.DesiredGeneration, partial.AppliedGeneration, partial.Stale)
	}
	if partial.Pending != 0 || partial.Failed != 1 {
		t.Errorf("partial aggregate pending/failed = %d/%d, want 0/1",
			partial.Pending, partial.Failed)
	}
	base := receiptForSource(t, partial.LastReceipt, sourceBase)
	if base.Created != 0 || base.Released != 0 || base.Pending != 0 || base.Failed != 1 {
		t.Errorf("base partial receipt = %+v, want 0/0/0/1", base)
	}
	if got := base.Errors.Count(bfd.ReconcileErrorCreate); got != 1 {
		t.Errorf("base create error count = %d, want 1", got)
	}
	sources := reconciliationSources()
	for _, source := range sources[1:] {
		receipt := receiptForSource(t, partial.LastReceipt, source)
		if receipt.Created != 0 || receipt.Released != 0 || receipt.Pending != 0 || receipt.Failed != 0 {
			t.Errorf("unaffected source %v receipt = %+v, want zero", source, receipt)
		}
	}
	partialLogs := logs.String()
	if got := strings.Count(partialLogs, "configuration reconciliation incomplete"); got != 1 {
		t.Errorf("aggregate incomplete log count = %d, want 1; logs=%q", got, logs.String())
	}
	if !strings.Contains(partialLogs, errInjectedSenderCreation.Error()) {
		t.Errorf("aggregate incomplete log does not contain transient cause: %q", partialLogs)
	}
	if strings.Contains(partialLogs, "configuration reconciliation converged") ||
		strings.Contains(partialLogs, "configuration reloaded") {
		t.Errorf("partial logs contain success claim: %q", partialLogs)
	}
	for _, forbidden := range []string{
		"session reconciliation complete",
		"echo session reconciliation complete",
		"micro-BFD group reconciliation complete",
	} {
		if strings.Contains(partialLogs, forbidden) {
			t.Errorf("partial logs contain source success claim %q: %q", forbidden, partialLogs)
		}
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusNotServing)
	assertNamedHealthServing(t, checker)

	// Snapshot values own fixed-size receipt and histogram arrays. Mutating a
	// caller's copy must not mutate the coordinator's retained status.
	mutated := partial
	mutated.LastReceipt.Sources[0].Errors[bfd.ReconcileErrorCreate] = 99
	if fresh := coordinator.Snapshot(); fresh != partial {
		t.Errorf("stored snapshot mutated through caller copy: got %+v want %+v", fresh, partial)
	}

	if err := coordinator.reconcile(
		context.Background(), cfg, mgr, factory, &overlayRuntime{}, level,
	); err != nil {
		t.Fatalf("explicit retry: %v", err)
	}
	converged := coordinator.Snapshot()
	if converged.DesiredGeneration != 2 || converged.AppliedGeneration != 2 || converged.Stale {
		t.Fatalf("retry generations = desired %d applied %d stale %t, want 2/2/false",
			converged.DesiredGeneration, converged.AppliedGeneration, converged.Stale)
	}
	if converged.Pending != 0 || converged.Failed != 0 {
		t.Errorf("retry aggregate pending/failed = %d/%d, want 0/0",
			converged.Pending, converged.Failed)
	}
	base = receiptForSource(t, converged.LastReceipt, sourceBase)
	if base.Created != 1 || base.Released != 0 || base.Pending != 0 || base.Failed != 0 {
		t.Errorf("retry base receipt = %+v, want created=1 and otherwise zero", base)
	}
	if got := strings.Count(logs.String(), "configuration reconciliation converged"); got != 1 {
		t.Errorf("aggregate converged log count = %d, want 1; logs=%q", got, logs.String())
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusServing)
	assertNamedHealthServing(t, checker)
}

func TestReconciliationCoordinatorPreservesCompletedReceiptWhileNextApplyRuns(t *testing.T) {
	t.Parallel()

	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(slog.New(slog.DiscardHandler), checker)
	first := coordinator.apply(
		context.Background(),
		func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
			if source == sourceBase {
				return failedSourceResult(bfd.ReconcileErrorCreate, errInjectedSenderCreation)
			}
			return sourceApplyResult{}
		},
	)
	if first.Failed != 1 || first.LastReceipt.Generation != 1 {
		t.Fatalf("first completed snapshot = %+v, want failed generation 1", first)
	}

	secondEntered := make(chan struct{})
	releaseSecond := make(chan struct{})
	secondDone := make(chan reconciliationSnapshot, 1)
	go func() {
		secondDone <- coordinator.apply(
			context.Background(),
			func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
				if source == sourceBase {
					close(secondEntered)
					<-releaseSecond
				}
				return sourceApplyResult{}
			},
		)
	}()
	<-secondEntered

	inFlight := coordinator.Snapshot()
	if inFlight.DesiredGeneration != 2 || inFlight.AppliedGeneration != 0 || !inFlight.Stale {
		close(releaseSecond)
		t.Fatalf("second in-flight generations = %d/%d stale=%t, want 2/0/true",
			inFlight.DesiredGeneration, inFlight.AppliedGeneration, inFlight.Stale)
	}
	if inFlight.Pending != first.Pending || inFlight.Failed != first.Failed ||
		inFlight.LastReceipt != first.LastReceipt {
		close(releaseSecond)
		t.Fatalf("second in-flight status replaced completed receipt: got %+v want receipt/status from %+v",
			inFlight, first)
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusNotServing)
	assertNamedHealthServing(t, checker)
	close(releaseSecond)
	<-secondDone
}

func TestCompiledCandidateClonesMicroBFDMemberLinks(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
		LAGInterface:   "bond0",
		MemberLinks:    []string{"eth0", "eth1"},
		PeerAddr:       "192.0.2.230",
		LocalAddr:      "192.0.2.231",
		MinActiveLinks: 1,
	}}
	candidate, err := compileControlSessionCandidate(cfg, &overlayRuntime{})
	if err != nil {
		t.Fatalf("compile candidate: %v", err)
	}
	cfg.MicroBFD.Groups[0].MemberLinks[0] = "mutated"

	got := candidate.microGroups[0].Config.MemberLinks
	if len(got) != 2 || got[0] != "eth0" || got[1] != "eth1" {
		t.Fatalf("compiled Micro-BFD members = %v, want immutable [eth0 eth1]", got)
	}
}

func TestReconciliationCoordinatorSerializesStartupBeforeReloadWholeSixSourceApply(t *testing.T) {
	t.Parallel()

	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(slog.New(slog.DiscardHandler), checker)
	firstEntered := make(chan struct{})
	checkSerialization := make(chan struct{})
	serializationChecked := make(chan bool, 1)
	releaseFirst := make(chan struct{})
	secondCalling := make(chan struct{})
	events := make(chan string, sourceCount*2)
	firstDone := make(chan reconciliationSnapshot, 1)
	secondDone := make(chan reconciliationSnapshot, 1)

	go func() {
		firstDone <- coordinator.apply(
			context.Background(),
			func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
				events <- fmt.Sprintf("startup:%s", source)
				if source == sourceBase {
					close(firstEntered)
					<-checkSerialization
					if coordinator.applyMu.TryLock() {
						coordinator.applyMu.Unlock()
						serializationChecked <- false
					} else {
						serializationChecked <- true
					}
					<-releaseFirst
				}
				return sourceApplyResult{}
			},
		)
	}()
	<-firstEntered

	go func() {
		close(secondCalling)
		secondDone <- coordinator.apply(
			context.Background(),
			func(_ context.Context, source reconciliationSource, _ compiledControlSessionCandidate) sourceApplyResult {
				events <- fmt.Sprintf("reload:%s", source)
				return sourceApplyResult{}
			},
		)
	}()
	<-secondCalling
	close(checkSerialization)
	if locked := <-serializationChecked; !locked {
		close(releaseFirst)
		t.Fatal("coordinator apply mutex was not held across the first source barrier")
	}
	inFlight := coordinator.Snapshot()
	if inFlight.DesiredGeneration != 1 || inFlight.AppliedGeneration != 0 || !inFlight.Stale {
		close(releaseFirst)
		t.Fatalf("in-flight snapshot = desired %d applied %d stale %t, want 1/0/true",
			inFlight.DesiredGeneration, inFlight.AppliedGeneration, inFlight.Stale)
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusNotServing)
	assertNamedHealthServing(t, checker)
	close(releaseFirst)

	first := <-firstDone
	second := <-secondDone
	if first.DesiredGeneration != 1 || first.AppliedGeneration != 1 {
		t.Errorf("first generations = %d/%d, want 1/1",
			first.DesiredGeneration, first.AppliedGeneration)
	}
	if second.DesiredGeneration != 2 || second.AppliedGeneration != 2 {
		t.Errorf("second generations = %d/%d, want 2/2",
			second.DesiredGeneration, second.AppliedGeneration)
	}

	for i, source := range reconciliationSources() {
		if got := <-events; got != fmt.Sprintf("startup:%s", source) {
			t.Fatalf("event %d = %q, want startup:%s", i, got, source)
		}
	}
	for i, source := range reconciliationSources() {
		if got := <-events; got != fmt.Sprintf("reload:%s", source) {
			t.Fatalf("event %d = %q, want reload:%s", sourceCount+i, got, source)
		}
	}
}

func TestReconciliationCoordinatorMissingOverlayBackendIsFailed(t *testing.T) {
	t.Parallel()

	checker := newDaemonHealthChecker()
	coordinator := newReconciliationCoordinator(slog.New(slog.DiscardHandler), checker)
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	cfg := config.DefaultConfig()
	cfg.VXLAN.Enabled = true
	cfg.VXLAN.DefaultDesiredMinTx = time.Second
	cfg.VXLAN.DefaultRequiredMinRx = time.Second
	cfg.VXLAN.DefaultDetectMultiplier = 3
	cfg.VXLAN.Peers = []config.VXLANPeerConfig{{
		Peer: "192.0.2.220", Local: "192.0.2.221",
	}}

	if err := coordinator.reconcile(
		context.Background(), cfg, mgr,
		newNthFailureDeclarativeSenderFactory(0), &overlayRuntime{}, level,
	); err != nil {
		t.Fatalf("reconcile valid VXLAN candidate: %v", err)
	}
	snapshot := coordinator.Snapshot()
	if snapshot.DesiredGeneration != 1 || snapshot.AppliedGeneration != 0 || !snapshot.Stale {
		t.Fatalf("missing-backend generations = %d/%d stale=%t, want 1/0/true",
			snapshot.DesiredGeneration, snapshot.AppliedGeneration, snapshot.Stale)
	}
	vxlan := receiptForSource(t, snapshot.LastReceipt, sourceVXLAN)
	if vxlan.Created != 0 || vxlan.Released != 0 || vxlan.Pending != 0 || vxlan.Failed != 1 {
		t.Errorf("missing-backend VXLAN receipt = %+v, want failed=1", vxlan)
	}
	if got := vxlan.Errors.Count(bfd.ReconcileErrorCreate); got != 1 {
		t.Errorf("missing-backend create error count = %d, want 1", got)
	}
	assertHealthStatus(t, checker, "", grpchealth.StatusNotServing)
	assertNamedHealthServing(t, checker)
}

func TestBufferedReloadStartsOnlyAfterStartupReconciliation(t *testing.T) {
	t.Parallel()

	bufferedReload := make(chan struct{}, 1)
	bufferedReload <- struct{}{}
	events := make(chan string, 2)

	startReloadAfterStartup(
		func() { events <- "startup-applied" },
		func() {
			<-bufferedReload
			events <- "reload-consumed"
		},
	)

	if got := <-events; got != "startup-applied" {
		t.Fatalf("first event = %q, want startup-applied", got)
	}
	if got := <-events; got != "reload-consumed" {
		t.Fatalf("second event = %q, want reload-consumed", got)
	}
}

func receiptForSource(
	t *testing.T,
	receipt generationReceipt,
	source reconciliationSource,
) sourceReceipt {
	t.Helper()
	for _, got := range receipt.Sources {
		if got.Source == source {
			return got
		}
	}
	t.Fatalf("receipt has no source %v: %+v", source, receipt)
	return sourceReceipt{}
}

func assertHealthStatus(
	t *testing.T,
	checker *grpchealth.StaticChecker,
	service string,
	want grpchealth.Status,
) {
	t.Helper()
	response, err := checker.Check(context.Background(), &grpchealth.CheckRequest{Service: service})
	if err != nil {
		t.Fatalf("check health for %q: %v", service, err)
	}
	if response.Status != want {
		t.Errorf("health for %q = %s, want %s", service, response.Status, want)
	}
}

func assertNamedHealthServing(t *testing.T, checker *grpchealth.StaticChecker) {
	t.Helper()
	for _, service := range []string{
		grpchealth.HealthV1ServiceName,
		bfdServiceName,
		echoServiceName,
		microBFDServiceName,
	} {
		assertHealthStatus(t, checker, service, grpchealth.StatusServing)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
