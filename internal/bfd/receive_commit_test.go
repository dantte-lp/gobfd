package bfd

import (
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"
)

type receiveCommitMetrics struct {
	noopMetrics

	observations []ReceiveTimingOutcome
	latency      time.Duration
}

func (m *receiveCommitMetrics) ObserveReceiveTiming(outcome ReceiveTimingOutcome, latency time.Duration) {
	m.observations = append(m.observations, outcome)
	m.latency = latency
}

func TestReceiveTimingOnlyAfterOwnerCommit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		reject bool
	}{
		{"accepted", false},
		{"rejected", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newCachedPacketTestSession(MaxPacketSize)
			metrics := &receiveCommitMetrics{}
			s.metrics = metrics
			base := time.Now().Add(-time.Second)
			clock := ReceiveClock{anchor: base, unixNano: base.UnixNano()}
			wall := base.UnixNano()
			timing := clock.capture(wall+int64(time.Millisecond), base.Add(2*time.Millisecond), wall+int64(2*time.Millisecond))
			pkt := s.buildControlPacket()
			pkt.State = StateUp
			if tc.reject {
				pkt.DesiredMinTxInterval = 0
			}
			tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
			defer tx.Stop()
			defer detect.Stop()
			s.handleRecvPacket(t.Context(), recvItem{pkt: pkt, timing: timing}, tx, detect)
			if tc.reject {
				if len(metrics.observations) != 0 {
					t.Fatal("rejected packet counted as committed latency")
				}
				return
			}
			if len(metrics.observations) != 1 || metrics.observations[0] != ReceiveTimingValid || metrics.latency <= 0 {
				t.Fatalf("owner commit timing=%+v latency=%v", metrics.observations, metrics.latency)
			}
		})
	}
}

func TestReceiveTimingQueueRefusalIsNotCommit(t *testing.T) {
	t.Parallel()
	s := newCachedPacketTestSession(MaxPacketSize)
	s.recvCh = make(chan recvItem, 1)
	metrics := &receiveCommitMetrics{}
	s.metrics = metrics
	pkt := s.buildControlPacket()
	pkt.State = StateUp
	if outcome := s.RecvPacket(&pkt); outcome != DeliveryQueued {
		t.Fatal(outcome)
	}
	if outcome := s.RecvPacket(&pkt); outcome != DeliveryQueueFull {
		t.Fatal(outcome)
	}
	if len(metrics.observations) != 0 {
		t.Fatal("admission recorded a commit")
	}
	tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	defer tx.Stop()
	defer detect.Stop()
	s.handleRecvPacket(t.Context(), <-s.recvCh, tx, detect)
	if len(metrics.observations) != 1 || metrics.observations[0] != ReceiveTimingUnavailable {
		t.Fatalf("legacy commit outcomes=%+v, want one unavailable", metrics.observations)
	}
}

func TestReceiveTimingClockStepLeavesDetectionMonotonic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newCachedPacketTestSession(MaxPacketSize)
		metrics := &receiveCommitMetrics{}
		s.metrics = metrics
		timing := ReceiveTiming{outcome: ReceiveTimingClockStep}
		pkt := s.buildControlPacket()
		pkt.State = StateUp
		pkt.DesiredMinTxInterval = 100_000
		tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
		defer tx.Stop()
		defer detect.Stop()
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt, timing: timing}, tx, detect)
		if len(metrics.observations) != 1 || metrics.observations[0] != ReceiveTimingClockStep || s.PacketsReceived() != 1 {
			t.Fatal("invalid clock sample changed packet acceptance")
		}
		synctest.Sleep(299 * time.Millisecond)
		select {
		case <-detect.C:
			t.Fatal("timestamp changed monotonic detection deadline")
		default:
		}
		synctest.Sleep(time.Millisecond)
		select {
		case <-detect.C:
		default:
			t.Fatal("negotiated detection deadline did not fire at300ms")
		}
	})
}

func TestReceiveTimingPanicIsNotCommit(t *testing.T) {
	t.Parallel()
	s := newCachedPacketTestSession(MaxPacketSize)
	metrics := &receiveCommitMetrics{}
	s.metrics = metrics
	pkt := s.buildControlPacket()
	tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	defer tx.Stop()
	defer detect.Stop()
	defer func() {
		if recover() == nil {
			t.Fatal("missing sender did not propagate panic")
		}
		if len(metrics.observations) != 0 {
			t.Fatal("partial owner processing counted as committed")
		}
	}()
	// The fixture has no sender; a Down->Init send must panic before commit.
	s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
}

func TestReceiveTimingUnsolicitedAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		metrics := &receiveCommitMetrics{}
		mgr := NewManager(slog.New(slog.DiscardHandler), WithManagerMetrics(metrics),
			WithUnsolicitedPolicy(admissionPolicy(4)),
			WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
			WithUnsolicitedSessionSender(nonOwningSessionSender(senderLeaseTestSender{})))
		defer mgr.Close()
		meta := admissionMeta("192.0.2.64", "192.0.2.1")
		meta.ReceiveTiming = ReceiveTiming{outcome: ReceiveTimingClockStep}
		outcome, err := mgr.DemuxWithWire(*ownershipDownPacket(99), meta, nil)
		if err != nil || outcome != DeliveryQueued {
			t.Fatalf("unsolicited admission=%s error=%v", outcome, err)
		}
		synctest.Wait()
		if len(metrics.observations) != 1 || metrics.observations[0] != ReceiveTimingClockStep {
			t.Fatalf("unsolicited timing lost: %v", metrics.observations)
		}
		outcome, err = mgr.Demux(ownershipDownPacket(99), meta)
		if err != nil || outcome != DeliveryQueued {
			t.Fatalf("legacy peer admission=%s error=%v", outcome, err)
		}
		synctest.Wait()
		if len(metrics.observations) != 2 || metrics.observations[1] != ReceiveTimingClockStep {
			t.Fatalf("legacy peer timing lost: %v", metrics.observations)
		}
	})
}
