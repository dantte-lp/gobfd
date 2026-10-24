//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestControlledWorkloadDrainsActualWritesAfterMeasurement(t *testing.T) {
	testControlledWorkload(t, 0, 0, false, false)
}

func TestControlledWorkloadRejectsMissingTerminalReceipt(t *testing.T) {
	testControlledWorkload(t, 1, 0, false, false)
}

func TestControlledWorkloadWithCPUPressure(t *testing.T) {
	testControlledWorkload(t, 0, 25, false, false)
}

func TestControlledWorkloadWithGCPressure(t *testing.T) {
	testControlledWorkload(t, 0, 25, true, false)
}

func TestControlledWorkloadWithSessionChurn(t *testing.T) {
	testControlledWorkload(t, 0, 0, false, true)
}

func TestControlledWorkloadConsumesStartupAndStopBeyondSubscriberBuffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration = 80, 400*time.Millisecond
	directory := privateControlDirectory(t)
	control, err := OpenControl(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := control.Close(); err != nil {
			t.Error(err)
		}
	}()
	cfg := PeerConfig{Profile: profile, SourcePortBase: 55100, ControlDirectory: directory}
	for index := range profile.Sessions {
		peer := netip.AddrFrom4([4]byte{127, 42, 0, byte(index + 1)})
		cfg.Sessions = append(cfg.Sessions, OwnerEndpoint{
			Local: netip.MustParseAddrPort("127.0.0.1:38261"), Peer: netip.AddrPortFrom(peer, 38262),
		})
	}
	type observation struct {
		report OwnerWorkloadObservation
		err    error
	}
	type peerObservation struct {
		report PeerObservation
		err    error
	}
	owners, peers := make(chan observation, 1), make(chan peerObservation, 1)
	logger := slog.New(slog.DiscardHandler)
	go func() { report, err := RunOwner(ctx, cfg, logger); owners <- observation{report, err} }()
	go func() { report, err := RunPeers(ctx, cfg, logger); peers <- peerObservation{report, err} }()
	_, beginErr := control.Begin(ctx)
	waitErr := beginErr
	if waitErr == nil {
		waitErr = control.WaitMeasured(ctx)
	}
	if waitErr != nil {
		cancel()
		owner, peer := <-owners, <-peers
		t.Fatalf("setup did not complete: owner=%v peer=%v control=%v", owner.err, peer.err, waitErr)
	}
	if err := control.StopPeers(); err != nil {
		cancel()
		t.Fatal(err)
	}
	peer := <-peers
	var written uint64
	for _, session := range peer.report.Peers {
		written += session.Written
	}
	if err := control.StopOwner(written); err != nil {
		cancel()
		t.Fatal(err)
	}
	owner := <-owners
	if err := errors.Join(owner.err, peer.err); err != nil || !owner.report.DownJournal.Complete ||
		owner.report.NotificationGaps != 0 || owner.report.DownEventsOmitted != 0 {
		t.Fatalf("normal startup/stop exceeded subscriber capacity: gaps=%d journal=%+v err=%v",
			owner.report.NotificationGaps, owner.report.DownJournal, err)
	}
}

func testControlledWorkload(t *testing.T, extraWrites, cpuPercent uint64, gc, churn bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration = 1, 400*time.Millisecond
	profile.CPULoadPercent = cpuPercent
	profile.GCPressure = gc
	if gc {
		profile.Duration = 1500 * time.Millisecond
	}
	if churn {
		profile.Duration = 6 * time.Second
		if decodeErr := json.Unmarshal([]byte(`{"session_churn":true}`), &profile); decodeErr != nil {
			t.Fatal(decodeErr)
		}
	}
	directory := privateControlDirectory(t)
	control, err := OpenControl(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := control.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	cfg := PeerConfig{
		Profile: profile, SourcePortBase: 55100, ControlDirectory: directory,
		Sessions: []OwnerEndpoint{{
			Local: netip.MustParseAddrPort("127.0.0.1:38171"), Peer: netip.MustParseAddrPort("127.34.0.2:38172"),
		}},
	}
	type ownerResult struct {
		report OwnerWorkloadObservation
		err    error
	}
	type peerResult struct {
		report PeerObservation
		err    error
	}
	owners, peers := make(chan ownerResult, 1), make(chan peerResult, 1)
	logger := slog.New(slog.DiscardHandler)
	go func() {
		report, runErr := RunOwner(ctx, cfg, logger)
		owners <- ownerResult{report, runErr}
	}()
	go func() {
		report, runErr := RunPeers(ctx, cfg, logger)
		peers <- peerResult{report, runErr}
	}()
	phase, err := control.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if waitErr := control.WaitMeasured(ctx); waitErr != nil {
		cancel()
		owner, peer := <-owners, <-peers
		t.Fatalf("measurement phase failed: %v; owner=%v; churn=%+v; peer=%v",
			waitErr, owner.err, owner.report.SessionChurn, peer.err)
	}
	select {
	case stopped := <-owners:
		t.Fatalf("owner stopped before peer writes were accounted: %v", stopped.err)
	default:
	}
	if stopErr := control.StopPeers(); stopErr != nil {
		t.Fatal(stopErr)
	}
	peer := <-peers
	if peer.err != nil || !peer.report.Completed || peer.report.Phase == nil ||
		peer.report.Phase.NativeStartNS != phase.NativeStartNS ||
		peer.report.Phase.TimeNamespaceID != phase.TimeNamespaceID || peer.report.Peers[0].UpTransitions == 0 {
		t.Fatalf("peer lost common phase or completed handshake: %+v: %v", peer.report, peer.err)
	}
	written := peer.report.Peers[0].Written
	if stopErr := control.StopOwner(written + extraWrites); stopErr != nil {
		t.Fatal(stopErr)
	}
	owner := <-owners
	if extraWrites != 0 {
		if !errors.Is(owner.err, context.DeadlineExceeded) || owner.report.Completed || !owner.report.Final.Stopped {
			t.Fatalf("unaccounted write completed=%t stopped=%t: %v",
				owner.report.Completed, owner.report.Final.Stopped, owner.err)
		}
		return
	}
	if owner.err != nil || !owner.report.Completed || !owner.report.Final.Stopped || owner.report.Phase == nil ||
		owner.report.Phase.NativeStartNS != phase.NativeStartNS ||
		owner.report.Phase.TimeNamespaceID != phase.TimeNamespaceID ||
		owner.report.MeasurementFinal.Scope == "" || len(owner.report.MeasurementFinal.Sessions) != len(cfg.Sessions) ||
		owner.report.MeasurementFinal.Stopped {
		t.Fatalf("owner completed=%t stopped=%t phase=%+v measurement_stopped=%t: %v",
			owner.report.Completed, owner.report.Final.Stopped, owner.report.Phase,
			owner.report.MeasurementFinal.Stopped, owner.err)
	}
	committed := deliveryCount(owner.report.Final, bfd.DeliveryPacketOwner, bfd.DeliveryCommitted)
	pressure := owner.report.CPUPressure
	if cpuPercent != 0 && (!pressure.Completed || pressure.CPUTimeNS <= 0 || pressure.WorkerWallTimeNS <= 0 ||
		pressure.RequestedPercent != cpuPercent || !pressure.ScheduledAtUTC.Equal(owner.report.StartedAtUTC)) {
		t.Fatalf("CPU workers lost the common epoch or actual receipt: %+v", pressure)
	}
	if cpuPercent != 0 {
		t.Logf("common-phase CPU pressure receipt: %+v", pressure)
	}
	allocation := owner.report.GCPressure
	if gc && (!allocation.Enabled || !allocation.Completed || allocation.AllocatedBytes == 0 ||
		allocation.ReleasedObjects == 0 || !allocation.ScheduledAtUTC.Equal(owner.report.StartedAtUTC) ||
		owner.report.MeasurementFinal.Environment.Runtime.GCCycles <= owner.report.Initial.Environment.Runtime.GCCycles) {
		t.Fatalf("GC worker lost the common epoch, observed cycles or cleanup: %+v", allocation)
	}
	if gc {
		t.Logf("common-phase GC pressure receipt: %+v", allocation)
	}
	if churn {
		assertChurnWorkload(t, owner.report, peer.report, written, committed)
	} else if written == 0 || committed != written || owner.report.Final.Sessions[0].Received != written {
		t.Fatalf("incomplete actual UDP boundary: written=%d committed=%d", written, committed)
	}
	t.Logf("actual UDP common phase: written=%d committed=%d", written, committed)
}

func assertChurnWorkload(
	t *testing.T, owner OwnerWorkloadObservation, peer PeerObservation, written, committed uint64,
) {
	t.Helper()
	//nolint:musttag // Observation and DeliveryCounter deliberately retain their core JSON names.
	encoded, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	var observed struct {
		Churn *struct {
			Enabled     bool   `json:"enabled"`
			Joined      bool   `json:"joined"`
			Created     uint64 `json:"created"`
			Released    uint64 `json:"released"`
			Recovered   uint64 `json:"recovered"`
			SkippedTail uint64 `json:"skipped_tail_cycles"`
		} `json:"session_churn"`
		Final struct {
			RetiredReceived uint64 `json:"retired_received"`
			TerminalPackets uint64 `json:"terminal_packets"`
		} `json:"final"`
	}
	if err := json.Unmarshal(encoded, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Churn == nil || !observed.Churn.Enabled || !observed.Churn.Joined ||
		observed.Churn.Created == 0 || observed.Churn.Released == 0 || observed.Churn.Recovered == 0 {
		t.Fatalf("real workload did not create, release and recover an admitted session: %+v", observed.Churn)
	}
	if observed.Churn.SkippedTail != 1 {
		t.Fatalf("short tail was not excluded from real drain/recovery: %+v", observed.Churn)
	}
	if observed.Final.RetiredReceived == 0 || observed.Final.TerminalPackets != written ||
		observed.Final.RetiredReceived+owner.Final.Sessions[0].Received != committed ||
		peer.Peers[0].UpTransitions < 2 {
		t.Fatalf("churn lost lifetime accounting or independent peer recovery: written=%d committed=%d retired=%d peer=%+v",
			written, committed, observed.Final.RetiredReceived, peer.Peers[0])
	}
}

func TestControlBeginCanceledBeforePublication(t *testing.T) {
	t.Parallel()
	control, err := OpenControl(privateControlDirectory(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := control.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, beginErr := control.Begin(ctx); !errors.Is(beginErr, context.Canceled) {
		t.Fatalf("canceled control accepted: %v", beginErr)
	}
	if _, found, readErr := control.read(t.Context(), "start"); found || readErr != nil {
		t.Fatalf("canceled controller published start: found=%t error=%v", found, readErr)
	}
}
