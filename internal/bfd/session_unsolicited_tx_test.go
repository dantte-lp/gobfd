package bfd

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type unsolicitedTXSender struct {
	retryCachedPacketSender

	mu sync.Mutex
}

func (s *unsolicitedTXSender) SendPacket(ctx context.Context, wire []byte, peer netip.Addr) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retryCachedPacketSender.SendPacket(ctx, wire, peer)
}

func (s *unsolicitedTXSender) lastPacket() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.packets[len(s.packets)-1]...)
}

func newUnsolicitedTXOwner(t *testing.T, sender PacketSender, initial State) (*Manager, *Session, *ControlPacket) {
	t.Helper()
	policy := admissionPolicy(1)
	policy.CleanupTimeout = 10 * time.Second
	mgr := NewManager(slog.New(slog.DiscardHandler),
		WithUnsolicitedPolicy(policy),
		WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
		WithUnsolicitedSessionSender(nonOwningSessionSender(sender)),
	)
	pkt := ownershipDownPacket(101)
	pkt.State = initial
	pkt.DesiredMinTxInterval = 2_000_000
	if _, err := mgr.tryCreateUnsolicited(pkt, admissionMeta("192.0.2.211", "192.0.2.254"), nil); err != nil {
		t.Fatal(err)
	}
	sess, _ := mgr.LookupByDiscriminator(mgr.Sessions()[0].LocalDiscr)
	synctest.Wait()
	return mgr, sess, pkt
}

// RFC 9468 Section 2: the owner must stop before the delayed manager cleanup.
func TestUnsolicitedOwnerStopsAfterNaturalDown(t *testing.T) {
	for _, failure := range []string{"remote-admin", "remote-down", "path"} {
		t.Run(failure, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
				defer mgr.Close()
				pkt.State = StateInit
				sess.RecvPacket(pkt)
				synctest.Wait()
				if sess.State() != StateUp {
					t.Fatal("session did not establish")
				}
				before := sess.PacketsSent()
				switch failure {
				case "remote-admin":
					pkt.State = StateAdminDown
					sess.RecvPacket(pkt)
				case "remote-down":
					pkt.State = StateDown
					sess.RecvPacket(pkt)
				case "path":
					if sess.SetPathDown() != DeliveryQueued {
						t.Fatal("queue path failure")
					}
				}
				synctest.Wait()
				if sess.State() != StateDown {
					t.Fatal("failure did not commit Down")
				}
				if got := sess.PacketsSent(); got != before {
					t.Errorf("failure sent immediate Control: before=%d after=%d", before, got)
				}
				synctest.Sleep(2 * time.Second)
				if got := sess.PacketsSent(); got != before {
					t.Errorf("natural Down still transmits: before=%d after=%d", before, got)
				}
				if got := sess.RemoteDiscriminator(); got != 101 {
					t.Errorf("learned discriminator=%d, want 101 before 6s RX expiry", got)
				}
			})
		})
	}
}

// Repeated valid Down reception must not postpone the establishment budget.
func TestUnsolicitedOwnerBoundsFailedEstablishment(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		if sess.State() != StateInit {
			t.Fatal("initial Down did not enter Init")
		}
		for range 5 {
			synctest.Sleep(time.Second)
			sess.RecvPacket(pkt)
			synctest.Wait()
		}
		synctest.Sleep(time.Second)
		before := sess.PacketsSent()
		synctest.Sleep(2 * time.Second)
		if got := sess.PacketsSent(); got != before {
			t.Errorf("failed establishment still transmits after 6s budget: before=%d after=%d", before, got)
		}
		if sess.State() != StateInit || sess.LocalDiag() != DiagNone {
			t.Errorf("setup budget changed RFC5880 state or diagnostic: %s/%s", sess.State(), sess.LocalDiag())
		}
		if got := sess.RemoteDiscriminator(); got != 101 {
			t.Errorf("learned discriminator=%d, want 101 before last RX + 6s", got)
		}
		pkt.State, pkt.DesiredMinTxInterval = StateUp, 0
		sess.RecvPacket(pkt)
		synctest.Wait()
		if sess.State() != StateInit || sess.PacketsSent() != before {
			t.Fatal("invalid Up completed an expired establishment attempt")
		}
		pkt.DesiredMinTxInterval = 2_000_000
		sess.RecvPacket(pkt)
		synctest.Wait()
		if sess.State() != StateUp || sess.PacketsSent() <= before {
			t.Fatal("valid Up did not establish a new local attempt from retained Init")
		}
	})
}

func TestUnsolicitedOwnerResumesAfterValidControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		pkt.State = StateInit
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State = StateAdminDown
		sess.RecvPacket(pkt)
		synctest.Wait()
		before := sess.PacketsSent()
		// Administrative maintenance and invalid traffic are not new initiation.
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State, pkt.DesiredMinTxInterval = StateDown, 0
		sess.RecvPacket(pkt)
		synctest.Wait()
		synctest.Sleep(2 * time.Second)
		if got := sess.PacketsSent(); got != before {
			t.Errorf("maintenance resumed TX: before=%d after=%d", before, got)
		}
		pkt.State, pkt.DesiredMinTxInterval = StateUp, 2_000_000
		sess.RecvPacket(pkt)
		synctest.Wait()
		synctest.Sleep(time.Second)
		if sess.State() != StateDown || sess.PacketsSent() <= before {
			t.Fatal("valid Up did not resume TX on the retained Down FSM")
		}
		before = sess.PacketsSent()
		pkt.State = StateDown
		sess.RecvPacket(pkt)
		synctest.Wait()
		if sess.State() != StateInit || sess.PacketsSent() <= before {
			t.Fatalf("active Down did not resume: state=%s TX=%d", sess.State(), sess.PacketsSent())
		}
	})
}

func TestUnsolicitedOwnerSharedClaimKeepsConfiguredTX(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		cfg := unsolicitedSessionConfig(admissionMeta("192.0.2.211", "192.0.2.254"), mgr.unsolicited.policy)
		shared, err := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory())
		if err != nil {
			t.Fatal(err)
		}
		if shared != sess {
			t.Fatal("matching API claim did not share wire owner")
		}
		pkt.State = StateInit
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State = StateAdminDown
		sess.RecvPacket(pkt)
		synctest.Wait()
		before := sess.PacketsSent()
		synctest.Sleep(2 * time.Second)
		if got := sess.PacketsSent(); got <= before {
			t.Fatal("configured passive claim lost periodic TX")
		}
		if err := mgr.DestroySession(t.Context(), sess.LocalDiscriminator()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		before = sess.PacketsSent()
		synctest.Sleep(2 * time.Second)
		if got := sess.PacketsSent(); got != before {
			t.Errorf("last configured claim release did not stop failed unsolicited owner: before=%d after=%d", before, got)
		}
	})
}

func TestUnsolicitedOwnerFinalRetryAndAdminDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &unsolicitedTXSender{}
		mgr, sess, pkt := newUnsolicitedTXOwner(t, sender, StateDown)
		defer mgr.Close()
		pkt.State = StateInit
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State, pkt.Poll = StateAdminDown, true
		sender.mu.Lock()
		sender.failures = 1
		sender.mu.Unlock()
		before := sess.PacketsSent()
		sess.RecvPacket(pkt)
		synctest.Wait()
		if sess.PacketsSent() != before {
			t.Fatal("failed Final was lost or counted")
		}
		synctest.Sleep(time.Second)
		if sess.PacketsSent() != before+1 {
			t.Fatal("suspended owner did not retry required Final")
		}
		var reply ControlPacket
		if err := UnmarshalControlPacket(sender.lastPacket(), &reply); err != nil {
			t.Fatal(err)
		}
		if !reply.Final || reply.Poll || reply.State != StateDown || reply.YourDiscriminator != 101 {
			t.Fatalf("Final reply=%+v", reply)
		}
		synctest.Sleep(2 * time.Second)
		if sess.PacketsSent() != before+1 {
			t.Fatal("Final retry restarted periodic Control")
		}
		if err := sess.SetAdminDown(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if sess.State() != StateAdminDown || sess.PacketsSent() != before+2 {
			t.Fatal("local administrative disable did not send AdminDown")
		}
		if err := UnmarshalControlPacket(sender.lastPacket(), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.State != StateAdminDown || reply.Diag != DiagAdminDown {
			t.Fatalf("admin reply=%+v", reply)
		}
		mgr.cleanupUnsolicitedSession(t.Context(), sess)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); !ok {
			t.Fatal("old failure cleanup removed a locally disabled session")
		}
	})
}

func TestUnsolicitedOwnerPreservesAuthenticatedLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := admissionPolicy(1)
		policy.CleanupTimeout = 10 * time.Second
		policy.Auth, policy.AuthKeys = MeticulousKeyedSHA1Auth{}, unsolicitedAuthKeys(t, unsolicitedAuthKeyID)
		mgr := NewManager(slog.New(slog.DiscardHandler),
			WithUnsolicitedPolicy(policy),
			WithUnsolicitedConnectedPrefix(netip.MustParsePrefix("192.0.2.0/24")),
			WithUnsolicitedSessionSender(nonOwningSessionSender(ownershipNoopSender{})),
		)
		defer mgr.Close()
		authState := &AuthState{Type: AuthTypeMeticulousKeyedSHA1, XmitAuthSeq: 100}
		sign := func(state State) (*ControlPacket, []byte) {
			pkt := ownershipDownPacket(101)
			pkt.State = state
			wire := make([]byte, MaxPacketSize)
			if err := policy.Auth.Sign(authState, policy.AuthKeys, pkt, wire, 0); err != nil {
				t.Fatal(err)
			}
			return pkt, wire[:int(wire[3])]
		}
		pkt, wire := sign(StateDown)
		if _, err := mgr.tryCreateUnsolicited(pkt, admissionMeta("192.0.2.211", "192.0.2.254"), wire); err != nil {
			t.Fatal(err)
		}
		sess, _ := mgr.LookupByDiscriminator(mgr.Sessions()[0].LocalDiscr)
		synctest.Wait()
		pkt, wire = sign(StateInit)
		sess.RecvPacket(pkt, wire)
		synctest.Wait()
		pkt, wire = sign(StateAdminDown)
		sess.RecvPacket(pkt, wire)
		synctest.Wait()
		before, received := sess.PacketsSent(), sess.PacketsReceived()
		pkt, wire = sign(StateUp)
		pkt.Auth.Data[0] ^= 0xff
		sess.RecvPacket(pkt, wire)
		synctest.Wait()
		synctest.Sleep(3*time.Second - time.Nanosecond)
		if sess.PacketsSent() != before || sess.RemoteDiscriminator() != 101 {
			t.Fatal("stop changed TX/discriminator/auth before 1RXDT")
		}
		synctest.Sleep(time.Nanosecond)
		if sess.RemoteDiscriminator() != 0 {
			t.Fatal("expected discriminator expiry at1RXDT and auth retention")
		}
		authState.XmitAuthSeq = 10
		pkt, wire = sign(StateDown)
		sess.RecvPacket(pkt, wire)
		synctest.Wait()
		if sess.PacketsReceived() != received {
			t.Fatal("old auth sequence accepted at1RXDT")
		}
		synctest.Sleep(3*time.Second - time.Nanosecond)
		sess.RecvPacket(pkt, wire)
		synctest.Wait()
		if sess.PacketsReceived() != received {
			t.Fatal("auth expired before2RXDT")
		}
		synctest.Sleep(time.Nanosecond)
		if sess.PacketsSent() != before {
			t.Fatal("auth expiry restarted TX")
		}
		sess.RecvPacket(pkt, wire)
		synctest.Wait()
		if sess.PacketsReceived() != received+1 || sess.State() != StateInit {
			t.Fatal("new active packet with restarted auth sequence was rejected at2RXDT")
		}
	})
}

func TestUnsolicitedOwnerCleanupRejectsEarlierAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		go mgr.RunDispatch(t.Context())
		pkt.State = StateInit
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State = StateAdminDown
		sess.RecvPacket(pkt)
		synctest.Wait()
		synctest.Sleep(2 * time.Second)
		pkt.State = StateDown
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State = StateInit
		sess.RecvPacket(pkt)
		synctest.Wait()
		if sess.State() != StateUp {
			t.Fatal("second attempt did not establish")
		}
		synctest.Sleep(time.Second)
		pkt.State = StateAdminDown
		sess.RecvPacket(pkt)
		synctest.Wait()
		// First attempt's cleanup expires at10s; this failure's retention ends13s.
		synctest.Sleep(7 * time.Second)
		if current, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); !ok || current != sess {
			t.Fatal("stale cleanup removed the later failed attempt")
		}
		synctest.Sleep(3 * time.Second)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); ok {
			t.Fatal("later failed attempt missed its own cleanup")
		}
	})
}

func TestUnsolicitedOwnerSetupCleanupDoesNotPublishFalseState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		go mgr.RunDispatch(t.Context())
		synctest.Wait()
		for len(mgr.publicNotifyCh) > 0 {
			<-mgr.publicNotifyCh
		}
		for range 5 {
			synctest.Sleep(time.Second)
			sess.RecvPacket(pkt)
			synctest.Wait()
		}
		synctest.Sleep(time.Second)
		if sess.State() != StateInit || len(mgr.publicNotifyCh) != 0 {
			t.Fatal("setup expiry published a false FSM transition")
		}
		before := sess.PacketsSent()
		synctest.Sleep(10 * time.Second)
		if _, ok := mgr.LookupByDiscriminator(sess.LocalDiscriminator()); ok {
			t.Fatal("failed Init attempt was not cleaned up")
		}
		if sess.PacketsSent() != before {
			t.Fatal("setup cleanup resumed Control TX")
		}
		if len(mgr.publicNotifyCh) == 0 {
			t.Fatal("cleanup did not publish deletion")
		}
		for len(mgr.publicNotifyCh) > 0 {
			if event := <-mgr.publicNotifyCh; !event.Deleted && event.OldState == StateInit && event.NewState == StateInit {
				t.Fatal("private failure reached subscribers")
			}
		}
	})
}

func TestUnsolicitedOwnerRetirementFencesResumption(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		pkt.State = StateInit
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State = StateAdminDown
		sess.RecvPacket(pkt)
		synctest.Wait()
		sess.mu.RLock()
		attempt := sess.unsolicitedAttempt
		sess.mu.RUnlock()
		mgr.ownershipMu.Lock()
		retired := mgr.retireUnsolicitedAttempt(sess, attempt)
		mgr.ownershipMu.Unlock()
		if !retired {
			t.Fatal("matching failed attempt was not fenced")
		}
		before := sess.PacketsSent()
		for _, state := range []State{StateDown, StateInit} {
			pkt.State = state
			sess.RecvPacket(pkt)
			synctest.Wait()
		}
		if sess.State() != StateDown || sess.PacketsSent() != before {
			t.Fatal("fenced owner resumed before conditional detach")
		}
	})
}

func TestUnsolicitedOwnerSetupBudgetCoversNegotiatedDetection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		synctest.Sleep(4 * time.Second)
		pkt.DesiredMinTxInterval = 4_000_000
		for range 8 {
			sess.RecvPacket(pkt)
			synctest.Wait()
			before := sess.PacketsSent()
			synctest.Sleep(time.Second)
			if sess.PacketsSent() <= before {
				t.Fatal("setup budget ended before 12s local RX Detection Time")
			}
		}
		before := sess.PacketsSent()
		synctest.Sleep(2 * time.Second)
		if sess.PacketsSent() != before {
			t.Fatal("negotiated setup budget was refreshed by repeated Down")
		}
	})
}

func TestUnsolicitedOwnerRepeatedUpDoesNotRefreshDownSetup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		pkt.State = StateInit
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State = StateAdminDown
		sess.RecvPacket(pkt)
		synctest.Wait()
		pkt.State = StateUp
		for range 6 {
			sess.RecvPacket(pkt)
			synctest.Wait()
			if sess.State() != StateDown {
				t.Fatal("Down plus valid Up changed the RFC FSM")
			}
			synctest.Sleep(time.Second)
		}
		before := sess.PacketsSent()
		synctest.Sleep(2 * time.Second)
		if sess.PacketsSent() != before {
			t.Fatal("repeated valid Up refreshed the Down setup deadline")
		}
	})
}

func TestUnsolicitedOwnerInitialAdminDownHasSetupBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateAdminDown)
		defer mgr.Close()
		for range 5 {
			synctest.Sleep(time.Second)
			sess.RecvPacket(pkt)
			synctest.Wait()
		}
		if sess.State() != StateDown || sess.PacketsSent() == 0 {
			t.Fatal("initial valid AdminDown did not start Control TX on the Down FSM")
		}
		synctest.Sleep(time.Second)
		before := sess.PacketsSent()
		for range 2 {
			sess.RecvPacket(pkt)
			synctest.Wait()
			synctest.Sleep(time.Second)
		}
		if sess.PacketsSent() != before {
			t.Fatal("AdminDown maintenance refreshed or resumed the expired setup attempt")
		}
	})
}

func BenchmarkUnsolicitedOwner(b *testing.B) {
	for _, state := range []State{StateUp, StateDown} {
		b.Run(state.String(), func(b *testing.B) {
			s := newCachedPacketTestSession(MaxPacketSize)
			s.sender, s.role, s.remoteDiscr = ownershipNoopSender{}, RolePassive, 101
			s.cachedState = state
			s.localPair.Store(packStateDiag(state, DiagNone))
			s.observation.State, s.observation.RemoteState = state, state
			s.unsolicitedOnly, s.unsolicitedOnlyWanted = true, true
			s.unsolicitedClaimWanted = true
			s.unsolicitedStopped = state == StateDown
			s.unsolicitedAttempt, s.unsolicitedStarted = 1, time.Now()
			s.remoteMinRxInterval = 100 * time.Millisecond
			packet := s.buildControlPacket()
			if state == StateDown {
				packet.State = StateAdminDown
			}
			tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
			defer tx.Stop()
			defer detect.Stop()
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s.processTimerUpdates(tx, detect)
				s.handleRecvPacket(ctx, recvItem{pkt: packet}, tx, detect)
				s.handleTxTimer(ctx, tx)
			}
		})
	}
}

func TestUnsolicitedOwnerInitClaimCyclePreservesAttempt(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "expired"}[expired], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
				defer mgr.Close()
				cfg := unsolicitedSessionConfig(admissionMeta("192.0.2.211", "192.0.2.254"), mgr.unsolicited.policy)
				claim := func() {
					shared, err := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory())
					if err != nil {
						t.Fatal(err)
					}
					if shared != sess {
						t.Fatal("API claim replaced retained Init")
					}
					synctest.Wait()
				}
				release := func() {
					if err := mgr.DestroySession(t.Context(), sess.LocalDiscriminator()); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
				}
				for second := 1; second <= 5; second++ {
					synctest.Sleep(time.Second)
					sess.RecvPacket(pkt)
					synctest.Wait()
					if !expired && second == 2 {
						claim()
					}
					if !expired && second == 3 {
						release()
					}
				}
				if expired {
					synctest.Sleep(2 * time.Second)
					claim()
					synctest.Sleep(time.Second)
					release()
				} else {
					synctest.Sleep(time.Second)
				}
				before := sess.PacketsSent()
				synctest.Sleep(2 * time.Second)
				if sess.PacketsSent() != before {
					t.Fatal("ownership cycle refreshed or resumed the Init setup attempt without RX")
				}
				if sess.State() != StateInit || sess.RemoteDiscriminator() != 101 {
					t.Fatal("ownership cycle changed retained Init or learned discriminator")
				}
			})
		})
	}
}

func TestUnsolicitedOwnerSharedAttemptKeepsGreatestDetection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, sess, pkt := newUnsolicitedTXOwner(t, ownershipNoopSender{}, StateDown)
		defer mgr.Close()
		for range 5 {
			synctest.Sleep(time.Second)
			sess.RecvPacket(pkt)
			synctest.Wait()
		}
		synctest.Sleep(2 * time.Second)
		cfg := unsolicitedSessionConfig(admissionMeta("192.0.2.211", "192.0.2.254"), mgr.unsolicited.policy)
		if _, err := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		sess.RecvPacket(pkt)
		synctest.Wait()
		synctest.Sleep(time.Second)
		pkt.DesiredMinTxInterval = 4_000_000
		sess.RecvPacket(pkt)
		synctest.Wait()
		sess.mu.RLock()
		started, deadline, attempt := sess.unsolicitedStarted, sess.unsolicitedDeadline, sess.unsolicitedAttempt
		sess.mu.RUnlock()
		if attempt != 2 || deadline.Sub(started) != 12*time.Second {
			t.Fatal("new shared attempt lost its greatest local Detection Time")
		}
		synctest.Sleep(time.Second)
		pkt.DesiredMinTxInterval = 1_000_000
		sess.RecvPacket(pkt)
		synctest.Wait()
		synctest.Sleep(time.Second)
		if err := mgr.DestroySession(t.Context(), sess.LocalDiscriminator()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		for range 8 {
			synctest.Sleep(time.Second)
			sess.RecvPacket(pkt)
			synctest.Wait()
		}
		sess.mu.RLock()
		unchanged := sess.unsolicitedAttempt == attempt &&
			sess.unsolicitedStarted.Equal(started) && sess.unsolicitedDeadline.Equal(deadline)
		sess.mu.RUnlock()
		if !unchanged {
			t.Fatal("shared RX or claim restore shortened/refreshed the pending attempt")
		}
		synctest.Sleep(time.Second)
		before := sess.PacketsSent()
		synctest.Sleep(2 * time.Second)
		if sess.PacketsSent() != before {
			t.Fatal("greatest Detection Time did not bound the restored pure attempt")
		}
	})
}
