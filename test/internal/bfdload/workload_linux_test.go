//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestOwnerWorkloadActualUDPReportAndCleanup(t *testing.T) {
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions = 1
	profile.CPULoadPercent = 25
	profile.GCPressure = true
	profile.SchedulerPressure = true
	cfg := PeerConfig{Profile: profile, SourcePortBase: 55000, Sessions: []OwnerEndpoint{
		{Local: netip.MustParseAddrPort("127.0.0.1:38061"), Peer: netip.MustParseAddrPort("127.33.0.2:38062")},
	}}
	logger := slog.New(slog.DiscardHandler)
	type ownerResult struct {
		report OwnerWorkloadObservation
		err    error
	}
	done := make(chan ownerResult, 1)
	go func() {
		report, runErr := RunOwner(t.Context(), cfg, logger)
		done <- ownerResult{report, runErr}
	}()
	peers, peerErr := RunPeers(t.Context(), cfg, logger)
	owner := <-done
	if joinedErr := errors.Join(peerErr, owner.err); joinedErr != nil {
		t.Fatal(joinedErr)
	}
	report := owner.report
	//nolint:musttag // The core report deliberately retains Observation and DeliveryCounter names.
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Scheduler *struct {
			Enabled   bool   `json:"enabled"`
			Rounds    uint64 `json:"rounds"`
			Yields    uint64 `json:"yields"`
			Completed bool   `json:"completed"`
		} `json:"scheduler_pressure"`
		GC *struct {
			Enabled        bool   `json:"enabled"`
			AllocatedBytes uint64 `json:"allocated_bytes"`
			Objects        uint64 `json:"objects"`
			Batches        uint64 `json:"batches"`
			Completed      bool   `json:"completed"`
		} `json:"gc_pressure"`
		Pressure *struct {
			RequestedPercent uint64 `json:"requested_percent"`
			Workers          int    `json:"workers"`
			CPUTimeNS        int64  `json:"cpu_time_ns"`
			Windows          uint64 `json:"windows"`
			Completed        bool   `json:"completed"`
		} `json:"cpu_pressure"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Scheduler == nil || !decoded.Scheduler.Enabled || !decoded.Scheduler.Completed ||
		decoded.Scheduler.Rounds == 0 || decoded.Scheduler.Yields == 0 {
		t.Fatalf("scheduler pressure has no actual joined activity receipt: %+v", decoded.Scheduler)
	}
	t.Logf("scheduler pressure receipt: %+v", report.SchedulerPressure)
	if decoded.GC == nil || !decoded.GC.Enabled || decoded.GC.AllocatedBytes == 0 || decoded.GC.Objects == 0 ||
		decoded.GC.Batches == 0 || !decoded.GC.Completed {
		t.Fatalf("GC pressure has no actual joined allocation receipt: %+v", decoded.GC)
	}
	if report.Final.Environment.Runtime.GCCycles <= report.Initial.Environment.Runtime.GCCycles {
		t.Fatal("allocation workload observed no actual process GC cycle")
	}
	var pauses uint64
	for _, count := range report.GCPauseWindow.Counts {
		pauses += count
	}
	if pauses == 0 {
		t.Fatal("allocation workload observed no actual process GC pause")
	}
	t.Logf("GC pressure receipt: %+v; process_gc_cycles=%d process_gc_pauses=%d", report.GCPressure,
		report.Final.Environment.Runtime.GCCycles-report.Initial.Environment.Runtime.GCCycles, pauses)
	if decoded.Pressure == nil || decoded.Pressure.RequestedPercent != 25 || decoded.Pressure.Workers == 0 ||
		decoded.Pressure.CPUTimeNS <= 0 || decoded.Pressure.Windows == 0 || !decoded.Pressure.Completed {
		t.Fatalf("requested CPU pressure has no actual joined worker receipt: %+v", decoded.Pressure)
	}
	t.Logf("CPU pressure receipt: %+v", report.CPUPressure)
	if report.Scope != "production-core-owner-workload" || !report.Completed || !report.Final.Stopped ||
		report.ReferenceQualified || report.Final.TimingUncertaintyNS != nil || report.Final.ClockEpoch == nil ||
		report.Resources.Samples < 2 || report.Resources.PeakRSSBytes == 0 {
		t.Fatalf("workload report lacks measured scope, cleanup, or unknown precision: %+v", report)
	}
	committed := deliveryCount(report.Final, bfd.DeliveryPacketOwner, bfd.DeliveryCommitted)
	var written uint64
	for _, peer := range peers.Peers {
		written += peer.Written
	}
	if committed == 0 || committed > written || len(report.Final.Sessions) != 1 {
		t.Fatalf("actual UDP accounting committed=%d written=%d sessions=%d", committed, written, len(report.Final.Sessions))
	}
	for _, session := range report.Final.Sessions {
		if session.Received == 0 {
			t.Fatalf("no actual committed input for %s", session.Endpoint.Peer)
		}
	}
}

func TestWorkloadPreCanceledReportsRetainFailure(t *testing.T) {
	t.Parallel()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions = 1
	cfg := PeerConfig{Profile: profile, SourcePortBase: 55000, Sessions: []OwnerEndpoint{{
		Local: netip.MustParseAddrPort("192.0.2.1:3784"), Peer: netip.MustParseAddrPort("192.0.2.2:3784"),
	}}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	peers, peerErr := RunPeers(ctx, cfg, slog.New(slog.DiscardHandler))
	owner, ownerErr := RunOwner(ctx, cfg, slog.New(slog.DiscardHandler))
	if !errors.Is(peerErr, context.Canceled) || peers.Error == "" || peers.Completed || peers.ReferenceQualified {
		t.Fatalf("pre-start peer cancellation lost from report: %+v: %v", peers, peerErr)
	}
	if !errors.Is(ownerErr, context.Canceled) || owner.Error == "" || owner.Completed || owner.ReferenceQualified {
		t.Fatalf("pre-start owner cancellation lost from report: %+v: %v", owner, ownerErr)
	}
}

func TestOwnerWorkloadRejectsReferenceBeforeBinding(t *testing.T) {
	if runtime.GOMAXPROCS(0) == 8 {
		t.Skip("requires a runtime outside the reference envelope")
	}
	profile, err := DefaultProfile("reference")
	if err != nil {
		t.Fatal(err)
	}
	endpoints := make([]OwnerEndpoint, profile.Sessions)
	for i := range endpoints {
		local, peer := netip.MustParseAddr("192.0.2.1"), netip.AddrFrom4([4]byte{10, 0, byte(i / 256), byte(i % 256)})
		if i >= 500 {
			local = netip.MustParseAddr("2001:db8::1")
			peer = netip.AddrFrom16([16]byte{0x20, 1, 0xd, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, byte(i / 256), byte(i % 256)})
		}
		endpoints[i] = OwnerEndpoint{
			Local: netip.AddrPortFrom(local, 3784), Peer: netip.AddrPortFrom(peer, 3784), Interface: "lo",
		}
	}
	started := time.Now()
	report, runErr := RunOwner(t.Context(), PeerConfig{Profile: profile, Sessions: endpoints, SourcePortBase: 49152},
		slog.New(slog.DiscardHandler))
	if !errors.Is(runErr, ErrAdmission) || len(report.Final.Sessions) != 0 || report.ReferenceQualified {
		t.Fatalf("reference failed after network setup or falsely qualified: %+v: %v", report, runErr)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("reference resource rejection was not bounded")
	}
}

func TestResourceWindowUsesFirstAndFinalSixHours(t *testing.T) {
	t.Parallel()
	var window ResourceWindow
	for _, sample := range []struct {
		elapsed time.Duration
		rss     uint64
	}{
		{0, 100},
		{5 * time.Hour, 200},
		{6 * time.Hour, 900},
		{17 * time.Hour, 800},
		{18 * time.Hour, 300},
		{23 * time.Hour, 400},
	} {
		window.observe(Environment{Process: ProcessSnapshot{RSSBytes: sample.rss}}, sample.elapsed, 24*time.Hour)
	}
	if window.Samples != 6 || window.PeakRSSBytes != 900 || window.FirstRSS.Samples != 2 ||
		window.FirstRSS.MeanBytes != 150 || window.LastRSS.Samples != 2 || window.LastRSS.MeanBytes != 350 {
		t.Fatalf("wrong RSS window boundaries, denominator, mean or peak: %+v", window)
	}
}

func TestOwnerDownEventsExcludeSetupAndDrain(t *testing.T) {
	t.Parallel()
	started := time.Unix(1000, 0)
	observed := OwnerWorkloadObservation{Config: PeerConfig{Profile: Profile{Duration: time.Second}}}
	for _, input := range []struct {
		discriminator uint32
		at            time.Time
	}{
		{1, started.Add(-time.Nanosecond)},
		{2, started},
		{3, started.Add(500 * time.Millisecond)},
		{4, started.Add(time.Second)},
	} {
		observed.observeChange(bfd.StateChange{
			Timestamp: input.at, LocalDiscr: input.discriminator,
			OldState: bfd.StateUp, NewState: bfd.StateDown, Diag: bfd.DiagControlTimeExpired,
		}, started)
	}
	if len(observed.DownEvents) != 2 || observed.DownEvents[0].LocalDiscriminator != 2 ||
		observed.DownEvents[0].ElapsedNS != 0 || observed.DownEvents[1].LocalDiscriminator != 3 ||
		observed.DownEvents[1].ElapsedNS != 500000000 {
		t.Fatalf("setup/drain contaminated the measured Down denominator: %+v", observed.DownEvents)
	}
}
