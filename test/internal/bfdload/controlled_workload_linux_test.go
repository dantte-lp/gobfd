//go:build linux

package bfdload

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestControlledWorkloadDrainsActualWritesAfterMeasurement(t *testing.T) {
	testControlledWorkload(t, 0, 0)
}

func TestControlledWorkloadRejectsMissingTerminalReceipt(t *testing.T) {
	testControlledWorkload(t, 1, 0)
}

func TestControlledWorkloadWithCPUPressure(t *testing.T) {
	testControlledWorkload(t, 0, 25)
}

func testControlledWorkload(t *testing.T, extraWrites, cpuPercent uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Duration = 1, 400*time.Millisecond
	profile.CPULoadPercent = cpuPercent
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
		t.Fatal(waitErr)
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
	if written == 0 || committed != written || owner.report.Final.Sessions[0].Received != written {
		t.Fatalf("incomplete actual UDP boundary: written=%d committed=%d", written, committed)
	}
	t.Logf("actual UDP common phase: written=%d committed=%d", written, committed)
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
