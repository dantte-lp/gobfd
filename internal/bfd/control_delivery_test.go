package bfd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestControlDemuxRejectsFullQueue(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire bool
		peer bool
	}{
		{name: "legacy discriminator"},
		{name: "legacy peer", peer: true},
		{name: "wire discriminator", wire: true},
		{name: "wire configured peer", wire: true, peer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sender := &lifecycleBlockingSender{entered: make(chan struct{}), release: make(chan struct{})}
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				defer close(sender.release)
				cfg := senderLeaseTestConfig("192.0.2.60")
				sess, err := mgr.CreateSession(context.Background(), cfg, NonOwningSenderLeaseFactory(sender))
				if err != nil {
					t.Fatal(err)
				}
				if adminErr := sess.SetAdminDown(t.Context()); adminErr != nil {
					t.Fatal(adminErr)
				}
				<-sender.entered // Hold the owner at its send boundary while filling RX.
				pkt := sess.buildControlPacket()
				pkt.State, pkt.MyDiscriminator = StateDown, 99
				pkt.YourDiscriminator = sess.LocalDiscriminator()
				if tc.peer {
					pkt.YourDiscriminator = 0
				}
				for range cap(sess.recvCh) {
					sess.RecvPacket(&pkt)
				}
				meta := PacketMeta{SrcAddr: cfg.PeerAddr, DstAddr: cfg.LocalAddr, IfName: cfg.Interface, TTL: 255}
				var outcome DeliveryOutcome
				if tc.wire {
					outcome, err = mgr.DemuxWithWire(pkt, meta, nil)
				} else {
					outcome, err = mgr.Demux(&pkt, meta)
				}
				if err == nil {
					t.Error("full receive queue reported successful delivery")
				}
				if outcome != DeliveryQueueFull || !errors.Is(err, ErrDeliveryQueueFull) {
					t.Errorf("full receive queue = %s, %v", outcome, err)
				}
			})
		})
	}
}

type controlDeliveryMetrics struct {
	noopMetrics

	dropped uint64
	auth    uint64
}

func (m *controlDeliveryMetrics) IncPacketsDropped(_, _ netip.Addr) { m.dropped++ }
func (m *controlDeliveryMetrics) IncAuthFailures(_, _ netip.Addr)   { m.auth++ }

func TestControlOwnerAuthRejectionAccounting(t *testing.T) {
	for _, name := range []string{"unexpected", "missing", "failed"} {
		t.Run(name, func(t *testing.T) {
			s, tx, detect := newAuthExpirySession(t)
			item := signedAuthExpiryPacket(t, s, 202)
			switch name {
			case "unexpected":
				s.auth = nil
			case "missing":
				item.pkt.AuthPresent, item.pkt.Auth = false, AuthSection{}
			case "failed":
				item.pkt.Auth.Data[0] ^= 0xff
			}
			metrics := &controlDeliveryMetrics{}
			s.metrics = metrics
			before, received, last := s.Observation(), s.PacketsReceived(), s.LastPacketReceived()
			remoteDiscr, remoteRX := s.RemoteDiscriminator(), s.RemoteMinRxInterval()
			s.handleRecvPacket(t.Context(), item, tx, detect)
			if metrics.dropped != 1 || metrics.auth != 1 {
				t.Errorf("auth rejection counters: dropped=%d auth=%d, want 1 each", metrics.dropped, metrics.auth)
			}
			counts := snapshotOf(s).Counters
			if counts.PacketsDropped != 1 || counts.AuthFailures != 1 {
				t.Errorf("snapshot auth rejection counters = %+v", counts)
			}
			if s.Observation() != before || s.PacketsReceived() != received || s.LastPacketReceived() != last ||
				s.RemoteDiscriminator() != remoteDiscr || s.RemoteMinRxInterval() != remoteRX {
				t.Error("auth rejection changed protocol state or valid receive telemetry")
			}
		})
	}
}

func deliveryCount(t *testing.T, mgr *Manager, source DeliverySource, outcome DeliveryOutcome) uint64 {
	t.Helper()
	for _, row := range mgr.DeliveryCounters() {
		if row.Source == source && row.Outcome == outcome {
			return row.Count
		}
	}
	t.Fatalf("missing delivery pair %s/%s", source, outcome)
	return 0
}

func TestControlAdmissionCopiesHeader(t *testing.T) {
	t.Parallel()
	s, _, tx, detect := newRemoteTxTestSession(t)
	s.recvCh = make(chan recvItem, 1)
	pkt := s.buildControlPacket()
	pkt.MyDiscriminator, pkt.State = 901, StateAdminDown
	if got := s.RecvPacket(&pkt); got != DeliveryQueued {
		t.Fatalf("admission = %s", got)
	}
	pkt.MyDiscriminator, pkt.State, pkt.DesiredMinTxInterval = 902, StateUp, 0
	s.handleRecvPacket(t.Context(), <-s.recvCh, tx, detect)
	if s.RemoteDiscriminator() != 901 || s.RemoteState() != StateAdminDown || s.PacketsReceived() != 1 {
		t.Fatalf("caller header reuse changed queued packet: %+v", snapshotOf(s))
	}
}

func TestControlCanceledFirstRunAbandonsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 51)
		s.delivery = mgr
		pkt := s.buildControlPacket()
		if s.RecvPacket(&pkt) != DeliveryQueued || s.SetPathDown() != DeliveryQueued {
			t.Fatal("pre-Run admission failed")
		}
		receipt, err := s.enqueueAdminDown(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s.Run(ctx)
		s.Run(ctx)
		select {
		case <-receipt:
			t.Error("abandoned AdminDown falsely acknowledged commit")
		default:
		}
		if len(s.recvCh) != 0 || len(s.ctrlCh) != 0 || s.State() != StateDown {
			t.Fatal("canceled first Run retained queued work or changed state")
		}
		if s.RecvPacket(&pkt) != DeliveryClosed || s.SetPathDown() != DeliveryClosed {
			t.Error("terminal admission accepted more work")
		}
		if got := deliveryCount(t, mgr, DeliveryPacketOwner, DeliveryAbandoned); got != 1 {
			t.Errorf("RX abandoned=%d, want 1", got)
		}
		if got := deliveryCount(t, mgr, DeliveryControlOwner, DeliveryAbandoned); got != 2 {
			t.Errorf("control abandoned=%d, want 2", got)
		}
		if counts := snapshotOf(s).Counters; counts.PacketsDropped != 2 ||
			counts.AuthFailures != 0 || counts.PacketsReceived != 0 {
			t.Errorf("terminal counters=%+v", counts)
		}
	})
}

func TestControlFullAdminDownAdmissionStopsAllWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 52)
		s.delivery = mgr
		for range cap(s.ctrlCh) {
			if s.SetPathDown() != DeliveryQueued {
				t.Fatal("fill control queue")
			}
		}
		const waiters = 12
		results := make(chan error, waiters)
		for range waiters {
			go func() { results <- s.SetAdminDown(t.Context()) }()
		}
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s.Run(ctx)
		for range waiters {
			if err := <-results; !errors.Is(err, ErrSessionStopped) {
				t.Errorf("full-queue waiter returned %v", err)
			}
		}
		if len(s.ctrlCh) != 0 || deliveryCount(t, mgr, DeliveryAdminDownAdmission, DeliveryQueued) != 0 ||
			deliveryCount(t, mgr, DeliveryAdminDownAdmission, DeliveryClosed) != waiters {
			t.Fatal("waiter enqueued after stop drain or lost its terminal admission outcome")
		}
	})
}

func TestControlAdminDownWaitersUseAllFreedCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 53)
		for range cap(s.ctrlCh) {
			s.SetPathDown()
		}
		results := make(chan error, cap(s.ctrlCh))
		for range cap(s.ctrlCh) {
			go func() { _, err := s.enqueueAdminDown(t.Context()); results <- err }()
		}
		synctest.Wait()
		for range cap(s.ctrlCh) {
			<-s.ctrlCh
			s.signalControlSpace()
		}
		synctest.Wait()
		if len(results) != cap(s.ctrlCh) || len(s.ctrlCh) != cap(s.ctrlCh) {
			t.Fatalf("freed slots stranded: admissions=%d queued=%d", len(results), len(s.ctrlCh))
		}
		for range cap(s.ctrlCh) {
			if err := <-results; err != nil {
				t.Error(err)
			}
		}
	})
}

func TestControlOwnerValidAndZeroTXAccounting(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "authenticated"}[authenticated], func(t *testing.T) {
			mgr := NewManager(slog.New(slog.DiscardHandler))
			defer mgr.Close()
			s, tx, detect := newAuthExpirySession(t)
			s.delivery = mgr
			item := signedAuthExpiryPacket(t, s, 202)
			if !authenticated {
				s.auth, item.pkt.Auth, item.pkt.AuthPresent = nil, AuthSection{}, false
			}
			metrics := &controlDeliveryMetrics{}
			s.metrics = metrics
			before := s.PacketsReceived()
			item.pkt.Poll = !authenticated // Exercise the immediate Final return without invalidating a signature.
			s.handleRecvPacket(t.Context(), item, tx, detect)
			if s.PacketsReceived() != before+1 || metrics.dropped != 0 || metrics.auth != 0 ||
				deliveryCount(t, mgr, DeliveryPacketOwner, DeliveryCommitted) != 1 {
				t.Fatal("valid packet was dropped, counted twice or missed owner commit")
			}
			item.pkt.DesiredMinTxInterval = 0
			s.handleRecvPacket(t.Context(), item, tx, detect)
			if metrics.dropped != 1 || metrics.auth != 0 || s.PacketsReceived() != before+1 ||
				deliveryCount(t, mgr, DeliveryPacketOwner, DeliveryZeroTX) != 1 {
				t.Fatal("zero TX rejection did not record one terminal drop")
			}
		})
	}
}

func newControlOwnerPublicationStep(tb testing.TB, handler slog.Handler) func() (StateChange, uint64) {
	tb.Helper()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	tb.Cleanup(mgr.Close)
	s := newCachedPacketTestSession(MaxPacketSize)
	s.delivery = mgr
	s.logger = slog.New(handler)
	s.role = RolePassive
	s.recvCh = make(chan recvItem, 1)
	ch := make(chan StateChange, 1)
	s.notifyCh = ch
	tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	tb.Cleanup(func() { tx.Stop(); detect.Stop() })
	pkt := s.buildControlPacket()
	pkt.State = StateUp
	ctx := tb.Context()
	return func() (StateChange, uint64) {
		pkt.Diag = Diag((uint8(pkt.Diag) + 1) % 2)
		s.RecvPacket(&pkt)
		s.handleRecvPacket(ctx, <-s.recvCh, tx, detect)
		return <-ch, mgr.delivery[DeliveryPacketOwner][DeliveryCommitted].Load()
	}
}

func BenchmarkControlOwnerDelivery(b *testing.B) {
	for _, tc := range []struct {
		name    string
		handler slog.Handler
	}{
		{name: "disabled", handler: slog.DiscardHandler},
		//nolint:sloglint // Allocation regression requires enabled JSON encoding; DiscardHandler disables that path.
		{name: "enabled_JSON", handler: slog.NewJSONHandler(io.Discard, nil)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			step := newControlOwnerPublicationStep(b, tc.handler)
			// Measure without -race: race mode randomly discards the JSON handler's pooled buffers.
			if allocs := testing.AllocsPerRun(100, func() { step() }); allocs != 0 {
				b.Fatalf("Control admission/owner/publication allocations=%g, want 0", allocs)
			}
			b.ReportAllocs()
			for b.Loop() {
				step()
			}
		})
	}
}

func TestControlOwnerPublication(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{
		{name: "disabled"},
		{name: "enabled_JSON", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := slog.DiscardHandler
			if tc.enabled {
				handler = slog.NewJSONHandler(&logs, nil)
			}
			step := newControlOwnerPublicationStep(t, handler)
			var version uint64
			for i := range 100 {
				change, committed := step()
				status := change.Status
				if status.ObservationVersion <= version || change.ObservationVersion != status.ObservationVersion ||
					!status.RemoteKnown || status.RemoteState != StateUp || status.RemoteDiag != Diag((i+1)%2) ||
					status.Counters.PacketsReceived != uint64(i+1) || status.Counters.PacketsDropped != 0 ||
					status.Counters.AuthFailures != 0 || committed != uint64(i+1) {
					t.Fatalf("publication lost causal facts or committed counters: change=%+v committed=%d", change, committed)
				}
				version = status.ObservationVersion
			}
			wantRecords := 0
			if tc.enabled {
				wantRecords = 100
			}
			if got := bytes.Count(logs.Bytes(), []byte{'\n'}); got != wantRecords {
				t.Errorf("serialized JSON records=%d, want %d", got, wantRecords)
			}
		})
	}
}

func TestControlDemuxRejectsStoppedOwner(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire bool
		peer bool
	}{
		{name: "legacy discriminator"},
		{name: "legacy peer", peer: true},
		{name: "wire discriminator", wire: true},
		{name: "wire configured peer", wire: true, peer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				ctx, cancel := context.WithCancel(t.Context())
				cfg := senderLeaseTestConfig("192.0.2.61")
				sess, err := mgr.CreateSession(ctx, cfg, NonOwningSenderLeaseFactory(senderLeaseTestSender{}))
				if err != nil {
					t.Fatal(err)
				}
				cancel()
				mgr.mu.RLock()
				stop := mgr.sessions[sess.LocalDiscriminator()].cancel
				mgr.mu.RUnlock()
				stop()
				<-sess.ownerDone
				pkt := ownershipDownPacket(99)
				if !tc.peer {
					pkt.YourDiscriminator = sess.LocalDiscriminator()
				}
				meta := PacketMeta{SrcAddr: cfg.PeerAddr, DstAddr: cfg.LocalAddr, IfName: cfg.Interface, TTL: 255}
				var outcome DeliveryOutcome
				if tc.wire {
					outcome, err = mgr.DemuxWithWire(*pkt, meta, nil)
				} else {
					outcome, err = mgr.Demux(pkt, meta)
				}
				if outcome != DeliveryClosed || !errors.Is(err, ErrDeliveryClosed) || !errors.Is(err, ErrSessionStopped) {
					t.Errorf("stopped receive = %s, %v", outcome, err)
				}
				if len(sess.recvCh) != 0 || deliveryCount(t, mgr, DeliveryPacketAdmission, DeliveryClosed) != 1 {
					t.Fatal("stopped demux admitted or omitted a rejected packet")
				}
			})
		})
	}
}

func TestControlUnsolicitedActivationRejectsUnavailableQueue(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "full", true: "closed"}[closed], func(t *testing.T) {
			mgr := NewManager(slog.New(slog.DiscardHandler))
			defer mgr.Close()
			s := newAdminDownTestSession(t, senderLeaseTestSender{}, 54)
			s.delivery = mgr
			pkt := ownershipDownPacket(99)
			want, wantErr := DeliveryQueueFull, ErrDeliveryQueueFull
			if closed {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				s.Run(ctx)
				want, wantErr = DeliveryClosed, ErrDeliveryClosed
			} else {
				for range cap(s.recvCh) {
					s.RecvPacket(pkt)
				}
			}
			outcome, err := mgr.activateUnsolicitedSession(s, pkt, admissionMeta("192.0.2.60", "192.0.2.1"), nil)
			if outcome != want || !errors.Is(err, wantErr) {
				t.Errorf("unsolicited activation = %s, %v", outcome, err)
			}
		})
	}
}

func TestControlUnsolicitedQueuedPreservesUnusedLeaseError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		closeFailure := errors.New("unused sender release failed")
		meta := admissionMeta("192.0.2.62", "192.0.2.1")
		policy := admissionPolicy(4)
		cfg := unsolicitedSessionConfig(meta, policy)
		var mgr *Manager
		var sess *Session
		mgr = NewManager(slog.New(slog.DiscardHandler), WithUnsolicitedPolicy(policy),
			WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
			WithUnsolicitedSessionSender(func(PacketMeta) (*SenderLease, error) {
				// Inject a winning registration between the two registry checks.
				var err error
				sess, _, _, err = mgr.claimSession(t.Context(), cfg, NonOwningSenderLeaseFactory(senderLeaseTestSender{}),
					APISessionOwner("race-winner"), false)
				if err != nil {
					return nil, err
				}
				return NewSenderLease(senderLeaseTestSender{}, func() error { return closeFailure }), nil
			}))
		defer mgr.Close()
		outcome, err := mgr.tryCreateUnsolicited(ownershipDownPacket(99), meta, nil)
		if outcome != DeliveryQueued || !errors.Is(err, closeFailure) {
			t.Fatalf("enqueue followed by unused close failure = %s, %v", outcome, err)
		}
		synctest.Wait()
		if sess.PacketsReceived() != 1 || snapshotOf(sess).Counters.PacketsDropped != 0 ||
			deliveryCount(t, mgr, DeliveryPacketAdmission, DeliveryQueued) != 1 ||
			deliveryCount(t, mgr, DeliveryPacketOwner, DeliveryCommitted) != 1 {
			t.Fatal("ancillary close failure lost or counted a queued packet as dropped")
		}
	})
}

func TestControlStopAbandonmentSurvivesManagerDeletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &lifecycleBlockingSender{entered: make(chan struct{}), release: make(chan struct{})}
		var release sync.Once
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		defer release.Do(func() { close(sender.release) })
		sess, err := mgr.CreateSession(t.Context(), senderLeaseTestConfig("192.0.2.63"), NonOwningSenderLeaseFactory(sender))
		if err != nil {
			t.Fatal(err)
		}
		if adminErr := sess.SetAdminDown(t.Context()); adminErr != nil {
			t.Fatal(adminErr)
		}
		<-sender.entered
		pkt := ownershipDownPacket(99)
		for range cap(sess.recvCh) {
			if sess.RecvPacket(pkt) != DeliveryQueued {
				t.Fatal("fill blocked owner RX")
			}
		}
		if sess.SetPathDown() != DeliveryQueued {
			t.Fatal("queue PathDown")
		}
		receipt, err := sess.enqueueAdminDown(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { mgr.Close(); close(done) }()
		synctest.Wait()
		release.Do(func() { close(sender.release) })
		<-done
		if len(mgr.Sessions()) != 0 ||
			deliveryCount(t, mgr, DeliveryPacketOwner, DeliveryAbandoned) != uint64(cap(sess.recvCh)) ||
			deliveryCount(t, mgr, DeliveryControlOwner, DeliveryAbandoned) != 2 {
			t.Fatal("global abandonment disappeared with the last removed session")
		}
		if err := sess.waitAdminDown(t.Context(), receipt); !errors.Is(err, ErrSessionStopped) {
			t.Fatalf("abandoned AdminDown commit=%v", err)
		}
	})
}

func TestControlAdmissionRacesCanceledFirstRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		s := newAdminDownTestSession(t, senderLeaseTestSender{}, 55)
		s.delivery = mgr
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		const attempts = 64
		var wg sync.WaitGroup
		for range attempts {
			wg.Go(func() {
				pkt := ownershipDownPacket(99)
				s.RecvPacket(pkt)
				s.SetPathDown()
			})
		}
		wg.Go(func() { s.Run(ctx) })
		wg.Wait()
		queued := deliveryCount(t, mgr, DeliveryPacketAdmission, DeliveryQueued)
		full := deliveryCount(t, mgr, DeliveryPacketAdmission, DeliveryQueueFull)
		closed := deliveryCount(t, mgr, DeliveryPacketAdmission, DeliveryClosed)
		if queued+full+closed != attempts || deliveryCount(t, mgr, DeliveryPacketOwner, DeliveryAbandoned) != queued ||
			snapshotOf(s).Counters.PacketsDropped != attempts || len(s.recvCh) != 0 || len(s.ctrlCh) != 0 {
			t.Fatal("RX admission/stop race lost, duplicated or retained work")
		}
		controlQueued := deliveryCount(t, mgr, DeliveryPathDownAdmission, DeliveryQueued)
		controlFull := deliveryCount(t, mgr, DeliveryPathDownAdmission, DeliveryQueueFull)
		controlClosed := deliveryCount(t, mgr, DeliveryPathDownAdmission, DeliveryClosed)
		if controlQueued+controlFull+controlClosed != attempts ||
			deliveryCount(t, mgr, DeliveryControlOwner, DeliveryAbandoned) != controlQueued {
			t.Fatal("control admission/stop race lost or duplicated work")
		}
	})
}

func TestControlUnsolicitedPreSessionReasons(t *testing.T) {
	for _, tc := range []struct {
		name string
		want DeliveryOutcome
	}{
		{"policy", DeliveryPolicyRejected},
		{"capacity", DeliveryCapacityRejected},
		{"rate", DeliveryRateRejected},
		{"unexpected auth", DeliveryAuthUnexpected},
		{"missing auth", DeliveryAuthMissing},
		{"failed auth", DeliveryAuthFailed},
		{"sender unavailable", DeliverySenderUnavailable},
		{"create failed", DeliveryCreateFailed},
		{"closed", DeliveryClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				policy := admissionPolicy(4)
				if tc.want == DeliveryAuthMissing || tc.want == DeliveryAuthFailed {
					policy.Auth, policy.AuthKeys = MeticulousKeyedSHA1Auth{}, unsolicitedAuthKeys(t, unsolicitedAuthKeyID)
				}
				mgr := NewManager(slog.New(slog.DiscardHandler), WithUnsolicitedPolicy(policy),
					WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
					WithUnsolicitedSessionSender(nonOwningSessionSender(senderLeaseTestSender{})))
				defer mgr.Close()
				pkt, meta := ownershipDownPacket(99), admissionMeta("192.0.2.64", "192.0.2.1")
				var wire []byte
				switch tc.want {
				case DeliveryPolicyRejected:
					meta.TTL = 254
				case DeliveryCapacityRejected:
					mgr.unsolicited.sessionCount.Store(4)
				case DeliveryRateRejected:
					mgr.unsolicited.admitTokens = 0
				case DeliveryAuthUnexpected:
					pkt.AuthPresent = true
				case DeliveryAuthFailed:
					pkt, wire = signedTrigger(t, policy.AuthKeys)
					wire[len(wire)-1] ^= 0xff
					if err := UnmarshalControlPacket(wire, pkt); err != nil {
						t.Fatal(err)
					}
				case DeliverySenderUnavailable:
					mgr.unsolicitedSessionSender = nil
				case DeliveryCreateFailed:
					mgr.unsolicitedSessionSender = func(PacketMeta) (*SenderLease, error) {
						return nil, errors.New("sender create failed")
					}
				case DeliveryClosed:
					mgr.Close()
				case DeliveryAuthMissing:
					// The unsigned packet exercises the already-configured authenticator.
				default:
					t.Fatalf("unsupported pre-session test outcome %s", tc.want)
				}
				outcome, err := mgr.tryCreateUnsolicited(pkt, meta, wire)
				if err == nil || outcome != tc.want || deliveryCount(t, mgr, DeliveryUnsolicitedAdmission, tc.want) != 1 ||
					len(mgr.Sessions()) != 0 || deliveryCount(t, mgr, DeliveryPacketAdmission, DeliveryQueued) != 0 {
					t.Fatalf("pre-session reason=%s err=%v rows=%+v", outcome, err, mgr.DeliveryCounters())
				}
			})
		})
	}
}

func TestControlAdminDownAdmissionContextReason(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[deadline], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				s := newAdminDownTestSession(t, senderLeaseTestSender{}, 56)
				s.delivery = mgr
				for range cap(s.ctrlCh) {
					s.SetPathDown()
				}
				ctx, cancel := context.WithCancel(t.Context())
				want, wantErr := DeliveryCanceled, context.Canceled
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), time.Second)
					want, wantErr = DeliveryDeadline, context.DeadlineExceeded
				} else {
					cancel()
				}
				defer cancel()
				result := make(chan error, 1)
				go func() { result <- s.SetAdminDown(ctx) }()
				synctest.Wait()
				if deadline {
					synctest.Sleep(time.Second)
				}
				if err := <-result; !errors.Is(err, wantErr) {
					t.Errorf("request error=%v", err)
				}
				if deliveryCount(t, mgr, DeliveryAdminDownAdmission, want) != 1 ||
					deliveryCount(t, mgr, DeliveryAdminDownAdmission, DeliveryQueued) != 0 || len(s.ctrlCh) != cap(s.ctrlCh) {
					t.Fatal("full-queue retries counted multiple admission outcomes or admitted canceled work")
				}
			})
		})
	}
}

func TestControlOwnerPathDownIgnoredAfterAdminCommit(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	s, _, tx, detect := newRemoteTxTestSession(t)
	s.delivery = mgr
	receipt := make(chan struct{})
	s.handleControlCommand(t.Context(), sessionControl{kind: controlAdminDown, receipt: receipt}, tx, detect)
	before := s.Observation()
	s.handleControlCommand(t.Context(), sessionControl{kind: controlPathDown}, tx, detect)
	if s.Observation() != before || deliveryCount(t, mgr, DeliveryControlOwner, DeliveryCommitted) != 1 ||
		deliveryCount(t, mgr, DeliveryControlOwner, DeliveryIgnored) != 1 {
		t.Fatal("PathDown changed AdminDown or was not recorded as ignored")
	}
}

func TestControlDeliveryRejectsInvalidPairs(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	for _, pair := range []DeliveryCounter{
		{Source: DeliverySource(255), Outcome: DeliveryAccepted},
		{Source: DeliveryWireParse, Outcome: DeliveryOutcome(255)},
		{Source: DeliveryWireParse, Outcome: DeliveryAbandoned},
		{Source: DeliverySourceUnspecified, Outcome: DeliveryAccepted},
		{Source: DeliveryWireParse, Outcome: DeliveryOutcomeUnspecified},
	} {
		mgr.RecordDelivery(pair.Source, pair.Outcome)
	}
	for source := range deliverySourceCount {
		for outcome := range deliveryOutcomeCount {
			if mgr.delivery[source][outcome].Load() != 0 {
				t.Fatal("invalid pair changed the delivery matrix")
			}
		}
	}
	if DeliverySource(255).String() != "unspecified" || DeliveryOutcome(255).String() != "unspecified" {
		t.Fatal("invalid enum did not use bounded fallback label")
	}
}
