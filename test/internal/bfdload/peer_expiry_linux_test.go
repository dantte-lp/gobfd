//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// The socket boundary alone is substituted; the peer timer, FSM, loss decision and codec are real.
type expirySender struct{ err error }

func (s expirySender) SendPacket(context.Context, []byte, netip.Addr) error {
	time.Sleep(2 * time.Millisecond)
	return s.err
}

func expiryPeer(t *testing.T) (*peerRuntime, *peerTimers, time.Time) {
	t.Helper()
	peer := blackholePeer(t)
	peer.tx.blackholeEvery, peer.rx.blackholeEvery = 0, 0
	peer.state, peer.sender = fixturePeerState(t), expirySender{}
	peer.jitter = rand.New(rand.NewPCG(17, 1))
	started := time.Now()
	if _, err := peer.state.receive(fixturePeerPacket(bfd.StateInit), started); err != nil {
		t.Fatal(err)
	}
	timers := &peerTimers{transmit: time.NewTimer(time.Hour), detection: time.NewTimer(time.Hour)}
	t.Cleanup(func() { timers.transmit.Stop(); timers.detection.Stop() })
	return peer, timers, started
}

func TestPeerExpiryOmissionCanPrecedeFirstSuccessfulWrite(t *testing.T) {
	t.Parallel()
	for _, blackhole := range []bool{false, true} {
		t.Run(map[bool]string{false: "random", true: "blackhole"}[blackhole], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				peer, timers, started := expiryPeer(t)
				peer.tx.lossPermille = 1000
				if blackhole {
					peer.tx.lossPermille = 0
					peer.tx.blackholeEvery, peer.tx.blackholeFor = 300*time.Millisecond, 5*time.Millisecond
				}
				synctest.Sleep(300 * time.Millisecond)
				if err := peer.onDetection(t.Context(), started, timers); err != nil {
					t.Fatal(err)
				}
				if len(peer.observed.DetectionExpiryEvents) != 1 || peer.observed.DetectionExpiryEvents[0].FirstDownWrite.Known {
					t.Fatal("omitted Down was recorded as a successful write")
				}
				peer.tx.lossPermille = 0
				synctest.Sleep(10 * time.Millisecond)
				for range 2 {
					if err := peer.send(t.Context(), time.Now(), started, false); err != nil {
						t.Fatal(err)
					}
				}
				row := peer.observed.DetectionExpiryEvents[0]
				row.project(started, false)
				if row.PhaseKnown || row.TimingKnown || !row.FirstDownWrite.Known || row.FirstDownWrite.TimingKnown ||
					row.FirstDownWrite.Scheduled != 2 || row.FirstDownWrite.started.Sub(started) != 310*time.Millisecond ||
					row.FirstDownWrite.ended.Sub(started) != 312*time.Millisecond || peer.observed.Written != 2 {
					t.Fatalf("first write lost, overwritten or assigned a common phase: %+v", row)
				}
			})
		})
	}
}

func TestPeerExpiryWriteErrorAndCancellationRemainUnknown(t *testing.T) {
	t.Parallel()
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "write_error", true: "canceled"}[canceled], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				peer, timers, started := expiryPeer(t)
				want := errors.New("socket write failed")
				peer.sender = expirySender{err: want}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if canceled {
					cancel()
					want = context.Canceled
				}
				synctest.Sleep(300 * time.Millisecond)
				if err := peer.onDetection(ctx, started, timers); !errors.Is(err, want) {
					t.Fatalf("write failure lost: %v", err)
				}
				row := peer.observed.DetectionExpiryEvents[0]
				row.project(started, true)
				if peer.observed.DetectionExpirations != 1 || row.FirstDownWrite.Known ||
					row.FirstDownWrite.TimingKnown || peer.observed.Written != 0 {
					t.Fatalf("unsuccessful write received a witness: %+v", row)
				}
			})
		})
	}
}

func TestPeerExpiryAcceptedInputSeparatesEpochs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		peer, timers, started := expiryPeer(t)
		peer.tx.lossPermille = 1000
		synctest.Sleep(300 * time.Millisecond)
		if err := peer.onDetection(t.Context(), started, timers); err != nil {
			t.Fatal(err)
		}
		input := peerInput{packet: fixturePeerPacket(bfd.StateUp), meta: netio.PacketMeta{TTL: 255}}
		if accepted, _ := peer.accept(input, time.Now()); !accepted || peer.state.state != bfd.StateDown {
			t.Fatal("fixture did not accept input while remaining Down")
		}
		peer.tx.lossPermille = 0
		if err := peer.send(t.Context(), time.Now(), started, false); err != nil {
			t.Fatal(err)
		}
		if peer.observed.DetectionExpiryEvents[0].FirstDownWrite.Known {
			t.Fatal("a new receive epoch supplied the old expiry's write")
		}
		synctest.Sleep(300 * time.Millisecond)
		if err := peer.onDetection(t.Context(), started, timers); err != nil {
			t.Fatal(err)
		}
		if len(peer.observed.DetectionExpiryEvents) != 2 || peer.observed.DetectionExpiryEvents[1].ID != 2 ||
			peer.observed.DetectionExpiryEvents[1].PreviousState != bfd.StateDown ||
			peer.observed.DetectionExpiryEvents[1].FirstDownWrite.Known {
			t.Fatal("an expiry while already Down was lost or attributed a transition write")
		}
	})
}

func TestPeerExpiryOverflowSurvivesCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		peer, timers, started := expiryPeer(t)
		peer.tx.lossPermille = 1000
		for index := range 257 {
			if _, err := peer.state.receive(fixturePeerPacket(bfd.StateInit), time.Now()); err != nil {
				t.Fatal(err)
			}
			synctest.Sleep(300 * time.Millisecond)
			err := peer.onDetection(t.Context(), started, timers)
			if index < 256 && err != nil || index == 256 && !errors.Is(err, ErrProfile) {
				t.Fatalf("expiry %d: %v", index+1, err)
			}
		}
		group := peerGroup{peers: []*peerRuntime{peer}, started: started}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		result, err := group.finish(ctx, PeerObservation{}, true, nil)
		if !errors.Is(err, ErrProfile) || result.Completed || len(result.Peers[0].DetectionExpiryEvents) != 256 ||
			result.Peers[0].DetectionExpiryEventsOmitted != 1 || result.Peers[0].DetectionExpirations != 257 {
			t.Fatalf("overflow lost during cancellation: completed=%v retained=%d error=%v",
				result.Completed, len(result.Peers[0].DetectionExpiryEvents), err)
		}
	})
}

func TestPeerExpiryProjectionRetainsDeadlineAndRejectsUnavailableTiming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		edit       func(*PeerDetectionExpiry)
		wantExpiry bool
		wantWrite  bool
	}{
		{"valid", func(*PeerDetectionExpiry) {}, true, true},
		{"stripped_receive", func(p *PeerDetectionExpiry) { p.lastReceive = p.lastReceive.Round(0) }, false, true},
		{"missing_expiry", func(p *PeerDetectionExpiry) { p.expired = time.Time{} }, false, true},
		{"stripped_expiry", func(p *PeerDetectionExpiry) { p.expired = p.expired.Round(0) }, false, true},
		{"early_expiry", func(p *PeerDetectionExpiry) { p.expired = p.due.Add(-time.Nanosecond) }, false, true},
		{"stripped_write", func(p *PeerDetectionExpiry) {
			p.FirstDownWrite.started = p.FirstDownWrite.started.Round(0)
		}, true, false},
		{"reversed_write", func(p *PeerDetectionExpiry) {
			p.FirstDownWrite.ended = p.FirstDownWrite.started.Add(-time.Nanosecond)
		}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			started := time.Now()
			row := PeerDetectionExpiry{
				lastReceive: started.Add(-100 * time.Millisecond), due: started.Add(200 * time.Millisecond),
				expired: started.Add(250 * time.Millisecond),
				FirstDownWrite: PeerExpiryWrite{
					Known: true, decision: started.Add(250 * time.Millisecond),
					started: started.Add(251 * time.Millisecond), ended: started.Add(253 * time.Millisecond),
				},
			}
			row.project(started, true)
			if !row.TimingKnown || !row.FirstDownWrite.TimingKnown || row.LastReceiveNS != -100000000 ||
				row.DetectionDueNS != 200000000 || row.ExpiredNS != 250000000 ||
				row.FirstDownWrite.DecisionNS != 250000000 || row.FirstDownWrite.StartedNS != 251000000 ||
				row.FirstDownWrite.EndedNS != 253000000 {
				t.Fatalf("deadline, delayed dispatch or write bracket changed: %+v", row)
			}
			tc.edit(&row)
			row.project(started, true)
			if row.TimingKnown != tc.wantExpiry || row.FirstDownWrite.TimingKnown != tc.wantWrite {
				t.Fatalf("unknown or reversed timestamps became usable: %+v", row)
			}
			if tc.wantWrite && row.FirstDownWrite.StartedNS != 251000000 {
				t.Fatal("unavailable expiry erased an independently known write bracket")
			}
			if tc.name == "valid" {
				row.FirstDownWrite.decision, row.FirstDownWrite.started, row.FirstDownWrite.ended = started, started, started
				row.project(started, true)
				if !row.FirstDownWrite.TimingKnown || row.FirstDownWrite.DecisionNS != 0 ||
					row.FirstDownWrite.StartedNS != 0 || row.FirstDownWrite.EndedNS != 0 {
					t.Fatal("measured phase-zero write timestamps became unknown")
				}
			}
		})
	}
}

func (expirySender) Close() error { return nil }

func TestPeerExpiryRetainsOldDeadlineAndSuccessfulDownWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		peer := blackholePeer(t)
		peer.tx.blackholeEvery = 0
		peer.state = fixturePeerState(t)
		peer.sender = expirySender{}
		peer.jitter = rand.New(rand.NewPCG(17, 1))
		started := time.Now()
		if _, err := peer.state.receive(fixturePeerPacket(bfd.StateInit), started); err != nil {
			t.Fatal(err)
		}
		timers := peerTimers{transmit: time.NewTimer(time.Hour), detection: time.NewTimer(time.Hour)}
		defer timers.transmit.Stop()
		defer timers.detection.Stop()
		synctest.Sleep(300*time.Millisecond - time.Nanosecond)
		if err := peer.onDetection(t.Context(), started, &timers); err != nil {
			t.Fatal(err)
		}
		if peer.observed.DetectionExpirations != 0 {
			t.Fatal("peer expired before the negotiated deadline")
		}
		synctest.Sleep(time.Nanosecond)
		for range 2 {
			if err := peer.onDetection(t.Context(), started, &timers); err != nil {
				t.Fatal(err)
			}
		}
		raw := peer.observed.DetectionExpiryEvents[0]
		if !raw.lastReceive.Equal(started) || !raw.due.Equal(started.Add(300*time.Millisecond)) ||
			!raw.expired.Equal(raw.due) || raw.FirstDownWrite.started.Sub(started) != 300*time.Millisecond ||
			raw.FirstDownWrite.ended.Sub(started) != 302*time.Millisecond {
			t.Fatalf("actual expiry or successful write timestamps were lost: %+v", raw)
		}
		group := peerGroup{peers: []*peerRuntime{peer}, started: started}
		group.phase.Store(&phaseStart{goStart: started.Add(100 * time.Millisecond)})
		report, err := group.finish(t.Context(), PeerObservation{}, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(report.Peers[0])
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Expiries []struct {
				ID                  uint64    `json:"id"`
				PreviousState       bfd.State `json:"previous_state"`
				LocalDiscriminator  uint32    `json:"local_discriminator"`
				RemoteDiscriminator uint32    `json:"remote_discriminator"`
				TimingKnown         bool      `json:"timing_known"`
				PhaseKnown          bool      `json:"phase_known"`
				LastReceiveNS       int64     `json:"last_receive_ns"`
				DetectionDueNS      int64     `json:"detection_due_ns"`
				ExpiredNS           int64     `json:"expired_ns"`
				Write               struct {
					Known             bool      `json:"known"`
					Scheduled         uint64    `json:"scheduled"`
					DecisionNS        int64     `json:"decision_ns"`
					StartedNS         int64     `json:"started_ns"`
					EndedNS           int64     `json:"ended_ns"`
					MyDiscriminator   uint32    `json:"my_discriminator"`
					YourDiscriminator uint32    `json:"your_discriminator"`
					State             bfd.State `json:"state"`
					Diag              bfd.Diag  `json:"diag"`
				} `json:"first_down_write"`
			} `json:"detection_expiry_events"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Expiries) != 1 {
			t.Fatalf("actual expiry lost or duplicated: got %d events, want 1", len(got.Expiries))
		}
		row := got.Expiries[0]
		if row.ID != 1 || row.PreviousState != bfd.StateUp || row.LocalDiscriminator != 11 ||
			row.RemoteDiscriminator != 77 || row.TimingKnown || !row.PhaseKnown ||
			row.LastReceiveNS != 0 || row.DetectionDueNS != 0 || row.ExpiredNS != 0 {
			t.Fatalf("old identity or monotonic phase projection lost: %+v", row)
		}
		write := row.Write
		if !write.Known || write.Scheduled != 1 || write.DecisionNS != 0 ||
			write.StartedNS != 0 || write.EndedNS != 0 ||
			write.MyDiscriminator != 11 || write.YourDiscriminator != 0 ||
			write.State != bfd.StateDown || write.Diag != bfd.DiagControlTimeExpired {
			t.Fatalf("successful write bracket or expired wire identity lost: %+v", write)
		}
	})
}
