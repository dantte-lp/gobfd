//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type blackholeWindowJSON struct {
	StartNS   int64  `json:"start_ns"`
	FirstTXNS int64  `json:"first_tx_ns"`
	LastTXNS  int64  `json:"last_tx_ns"`
	TXOmitted uint64 `json:"tx_omitted"`
	FirstRXNS int64  `json:"first_rx_ns"`
	LastRXNS  int64  `json:"last_rx_ns"`
	RXOmitted uint64 `json:"rx_omitted"`
}

func decodeBlackholeJournal(t *testing.T, observed PeerSessionObservation) ([]blackholeWindowJSON, uint64) {
	t.Helper()
	data, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Windows []blackholeWindowJSON `json:"blackhole_windows"`
		Omitted uint64                `json:"blackhole_events_omitted"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report.Windows, report.Omitted
}

func blackholePeer(t *testing.T) *peerRuntime {
	t.Helper()
	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.BlackholeEvery, profile.BlackholeFor = 10*time.Millisecond, 3*time.Millisecond
	tx, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	rx, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	return &peerRuntime{tx: tx, rx: rx}
}

func TestPeerBlackholeJournalRecordsActualDirectionalDrops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		peer := blackholePeer(t)
		started := time.Now().Add(-10 * time.Millisecond)
		if err := peer.send(t.Context(), started.Add(10*time.Millisecond), started, false); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := peer.onInput(t.Context(), peerInput{}, started, nil); err != nil {
				t.Fatal(err)
			}
			synctest.Sleep(time.Millisecond)
		}
		for _, elapsed := range []time.Duration{12 * time.Millisecond, 20 * time.Millisecond} {
			if err := peer.send(t.Context(), started.Add(elapsed), started, false); err != nil {
				t.Fatal(err)
			}
		}
		windows, omitted := decodeBlackholeJournal(t, peer.observed)
		want := []blackholeWindowJSON{
			{
				StartNS: 10000000, FirstTXNS: 10000000, LastTXNS: 12000000, TXOmitted: 2,
				FirstRXNS: 10000000, LastRXNS: 11000000, RXOmitted: 2,
			},
			{StartNS: 20000000, FirstTXNS: 20000000, LastTXNS: 20000000, TXOmitted: 1},
		}
		if len(windows) != len(want) || omitted != 0 || peer.observed.BlackholeOmitted != 3 ||
			peer.observed.ReceiveBlackholeOmitted != 2 {
			t.Fatalf("actual omissions have no complete journal: windows=%+v omitted=%d counters=%+v",
				windows, omitted, peer.observed)
		}
		for i := range want {
			if windows[i] != want[i] {
				t.Fatalf("window %d: got %+v want %+v", i, windows[i], want[i])
			}
		}
	})
}

func TestPeerBlackholeJournalUsesAdoptedPhase(t *testing.T) {
	t.Parallel()
	peer := blackholePeer(t)
	phase := &phaseStart{goStart: time.Now().Add(-10 * time.Millisecond)}
	peer.phase, peer.duration = new(atomic.Pointer[phaseStart]), time.Second
	peer.phase.Store(phase)
	at := phase.goStart.Add(11 * time.Millisecond)
	if err := peer.send(t.Context(), at, phase.goStart.Add(-time.Minute), false); err != nil {
		t.Fatal(err)
	}
	windows, omitted := decodeBlackholeJournal(t, peer.observed)
	if len(windows) != 1 || omitted != 0 || windows[0].StartNS != 10000000 ||
		windows[0].FirstTXNS != 11000000 || windows[0].LastTXNS != 11000000 || windows[0].TXOmitted != 1 {
		t.Fatalf("blackhole journal did not use adopted common phase: %+v omitted=%d", windows, omitted)
	}
}

func TestPeerBlackholeJournalFailsClosedOnWindowOverflow(t *testing.T) {
	t.Parallel()
	peer := blackholePeer(t)
	started := time.Now()
	for window := range 256 {
		at := started.Add(time.Duration(window+1) * 10 * time.Millisecond)
		if err := peer.send(t.Context(), at, started, false); err != nil {
			t.Fatal(err)
		}
	}
	err := peer.send(t.Context(), started.Add(2570*time.Millisecond), started, false)
	windows, omitted := decodeBlackholeJournal(t, peer.observed)
	if !errors.Is(err, ErrProfile) || len(windows) != 256 || omitted != 1 || peer.observed.BlackholeOmitted != 257 {
		t.Fatalf("overflow silently accepted or unbounded: error=%v windows=%d omitted=%d drops=%d",
			err, len(windows), omitted, peer.observed.BlackholeOmitted)
	}
}

func TestPeerBlackholeJournalIgnoresRandomLossAndCancellation(t *testing.T) {
	t.Parallel()
	peer := blackholePeer(t)
	peer.tx.lossPermille = 1000
	started := time.Now()
	if err := peer.send(t.Context(), started.Add(time.Millisecond), started, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := peer.send(ctx, started.Add(10*time.Millisecond), started, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled send: %v", err)
	}
	windows, omitted := decodeBlackholeJournal(t, peer.observed)
	if len(windows) != 0 || omitted != 0 || peer.observed.Scheduled != 1 || peer.observed.RandomOmitted != 1 {
		t.Fatalf("non-blackhole activity contaminated the journal: windows=%+v omitted=%d counters=%+v",
			windows, omitted, peer.observed)
	}
}

func TestPeerBlackholeJournalOverflowCannotCompleteAfterCancellation(t *testing.T) {
	t.Parallel()
	peer := blackholePeer(t)
	started := time.Now()
	for window := range 257 {
		at := started.Add(time.Duration(window+1) * 10 * time.Millisecond)
		err := peer.send(t.Context(), at, started, false)
		if window < 256 && err != nil {
			t.Fatal(err)
		}
		if window == 256 && !errors.Is(err, ErrProfile) {
			t.Fatalf("overflow producer did not return the expected failure: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	group := peerGroup{peers: []*peerRuntime{peer}, started: started}
	// Shutdown can discard the worker error; the joined report must reject its omitted evidence independently.
	report, err := group.finish(ctx, PeerObservation{}, true, nil)
	if !errors.Is(err, ErrProfile) || report.Completed || !report.Stopped || report.Error == "" ||
		len(report.Peers) != 1 || report.Peers[0].BlackholeEventsOmitted != 1 {
		t.Fatalf("canceled overflow report: error=%v completed=%t stopped=%t diagnostic=%q peers=%d",
			err, report.Completed, report.Stopped, report.Error, len(report.Peers))
	}
}
