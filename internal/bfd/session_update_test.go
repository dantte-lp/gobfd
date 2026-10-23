package bfd

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

func TestConfigTimerUpdateAdmission(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	cfg := ownershipTestConfig("192.0.2.90")
	desired := []ReconcileConfig{{SessionConfig: cfg, SenderLeaseFactory: ownershipSenderLeaseFactory()}}
	first := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
	if first.Err() != nil || first.Created != 1 {
		t.Fatalf("create: %+v", first)
	}
	desired[0].SessionConfig.DesiredMinTxInterval = 200 * time.Millisecond
	desired[0].SessionConfig.RequiredMinRxInterval = 0
	result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
	if result.Pending != 1 || result.Failed != 0 || result.Created != 0 || result.Err() == nil {
		t.Fatalf("timing-only reload must be pending without replacement: %+v", result)
	}
}

func TestSessionTimerUpdateLatestAndSeparation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, sender, tx, detect := newRemoteTxTestSession(t)
		a := timerTuple{200 * time.Millisecond, 50 * time.Millisecond, 5}
		b := timerTuple{300 * time.Millisecond, 200 * time.Millisecond, 7}
		original := timerTuple{s.desiredMinTxInterval, s.requiredMinRxInterval, 3}
		admit := func(tuple timerTuple, generation uint64) timerUpdateStatus {
			t.Helper()
			status, err := s.admitTimerUpdate(t.Context(), tuple, generation)
			if err != nil {
				t.Fatal(err)
			}
			s.processTimerUpdates(tx, detect)
			return status
		}
		first := admit(a, 1)
		s.sendControl(t.Context())
		synctest.Sleep(10 * time.Millisecond)
		admit(b, 2)
		latest := admit(a, 3)
		if s.waitingUpdate.Present || latest.Requested.Revision != first.Requested.Revision ||
			latest.Requested.Deadline != first.Requested.Deadline {
			t.Fatal("same active intent renewed revision/deadline or retained obsolete waiting intent")
		}
		admit(b, 4)
		admit(original, 5)
		if s.waitingUpdate.Tuple != original {
			t.Fatal("reverse intent was not retained")
		}
		pkt := s.buildControlPacket()
		pkt.Poll, pkt.Final, pkt.RequiredMinRxInterval = true, false, 100_000
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		last := sender.packets[len(sender.packets)-1]
		assertCachedPacketFlags(t, last, false, true)
		var reply ControlPacket
		if err := UnmarshalControlPacket(last, &reply); err != nil {
			t.Fatal(err)
		}
		if reply.DesiredMinTxInterval != microsecondsFromDuration(a.TX) ||
			reply.RequiredMinRxInterval != microsecondsFromDuration(a.RX) || reply.DetectMult != a.DetectMult {
			t.Fatal("crossed Final changed active advertisement")
		}
		synctest.Sleep(10 * time.Millisecond)
		s.sendControl(t.Context()) // A retry must not reset the first-send timestamp.
		pkt.Poll, pkt.Final = false, true
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		s.processTimerUpdates(tx, detect)
		if s.pollActive || s.timerUpdateSnapshot().State != timerUpdatePending {
			t.Fatal("reverse intent bypassed separation")
		}
		synctest.Sleep(20*time.Millisecond - time.Nanosecond)
		s.processTimerUpdates(tx, detect)
		if s.pollActive {
			t.Fatal("separation measured from last Poll instead of first successful Poll")
		}
		synctest.Sleep(time.Nanosecond)
		s.processTimerUpdates(tx, detect)
		if !s.activeUpdate.Present || s.activeUpdate.Tuple != original || s.activeUpdate.Generation != 5 {
			t.Fatal("latest reverse update did not promote after separation")
		}
		s.sendControl(t.Context())
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		if status := s.timerUpdateSnapshot(); status.State != timerUpdateApplied || !status.Changed {
			t.Fatalf("reverse update receipt: %+v", status)
		}
	})
}

func TestSessionTimerUpdateExpiry(t *testing.T) {
	t.Parallel()
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "waiting"}[queued], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, _, tx, detect := newRemoteTxTestSession(t)
				tuple := timerTuple{200 * time.Millisecond, 50 * time.Millisecond, 3}
				first, err := s.admitTimerUpdate(t.Context(), tuple, 1)
				if err != nil {
					t.Fatal(err)
				}
				if !queued {
					s.processTimerUpdates(tx, detect)
					s.sendControl(t.Context())
				}
				synctest.Sleep(time.Until(first.Requested.Deadline))
				s.processTimerUpdates(tx, detect)
				if s.timerUpdateSnapshot().State != timerUpdateFailed {
					t.Fatal("admission budget did not expire")
				}
				status, err := s.admitTimerUpdate(t.Context(), tuple, 1)
				if err != nil || status.State != timerUpdateFailed {
					t.Fatal("same generation rearmed expired proposal")
				}
				if queued {
					if s.activeUpdate.Present || s.waitingUpdate.Present {
						t.Fatal("expired queued proposal became eligible")
					}
					return
				}
				if _, admissionErr := s.admitTimerUpdate(t.Context(), tuple, 2); !errors.Is(admissionErr, ErrTimerUpdateFailed) {
					t.Fatal("expired active revision acquired a fresh generation")
				}
				pkt := s.buildControlPacket()
				pkt.Final, pkt.Poll = true, false
				s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if s.timerUpdateSnapshot().State != timerUpdateFailed || s.activeUpdate.Present {
					t.Fatal("late Final resurrected failed receipt or failed to retire Poll")
				}
				synctest.Sleep(time.Until(s.separateUntil))
				s.processTimerUpdates(tx, detect)
				status, err = s.admitTimerUpdate(t.Context(), tuple, 2)
				if err == nil && status.State != timerUpdateFailed {
					t.Fatal("rejected generation became eligible after a late Final")
				}
				status, err = s.admitTimerUpdate(t.Context(), tuple, 3)
				if err != nil || status.State != timerUpdateApplied || status.Changed {
					t.Fatalf("fresh generation failed to adopt late-confirmed tuple: %+v %v", status, err)
				}
			})
		})
	}
}

func TestSessionTimerUpdateRecovery(t *testing.T) {
	t.Parallel()
	for _, proposedTX := range []time.Duration{200 * time.Millisecond, 2 * time.Second} {
		t.Run(proposedTX.String(), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, sender, tx, detect := newRemoteTxTestSession(t)
				tuple := timerTuple{proposedTX, 0, 3}
				if _, err := s.admitTimerUpdate(t.Context(), tuple, 1); err != nil {
					t.Fatal(err)
				}
				s.processTimerUpdates(tx, detect)
				s.sendControl(t.Context())
				s.handleDetectTimer(t.Context(), tx, detect)
				wire := s.buildControlPacket()
				if wire.DesiredMinTxInterval != microsecondsFromDuration(max(proposedTX, time.Second)) ||
					wire.RequiredMinRxInterval != 0 || s.timerUpdateSnapshot().State != timerUpdateFailed {
					t.Fatal("non-Up did not retain advertised tuple with mandatory floor and failed receipt")
				}
				if s.pollActive != (proposedTX < time.Second) {
					t.Fatal("floor change did not immediately initiate recovery Poll")
				}
				if got := s.timerUpdateSnapshot().Effective.TX; got != max(proposedTX, time.Second) {
					t.Fatalf("non-Up effective TX snapshot = %s, want %s", got, max(proposedTX, time.Second))
				}
				// RFC 5880 Section 6.8.7: the state change is sent at once and
				// carries the recovery Poll; the interrupted update's earlier
				// send must not be what confirms it.
				var down ControlPacket
				if err := UnmarshalControlPacket(sender.packets[len(sender.packets)-1], &down); err != nil {
					t.Fatal(err)
				}
				recoveryPoll := proposedTX < time.Second
				if len(sender.packets) < 2 || down.State != StateDown || down.Poll != recoveryPoll ||
					s.pollSent != recoveryPoll {
					t.Fatalf("state-change packet %+v pollSent=%t, want Down carrying the recovery Poll", down, s.pollSent)
				}
				s.sendControl(t.Context())
				synctest.Sleep(time.Millisecond)
				pkt := s.buildControlPacket()
				pkt.Poll, pkt.Final, pkt.State, pkt.RequiredMinRxInterval = false, false, StateInit, 100_000
				s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if s.State() != StateUp {
					t.Fatal("recovery did not return Up")
				}
				if proposedTX < time.Second && s.buildControlPacket().DesiredMinTxInterval != 1_000_000 {
					t.Fatal("returning Up overwrote outstanding floor Poll")
				}
				if got := s.timerUpdateSnapshot().Effective.TX; got != max(proposedTX, time.Second) {
					t.Fatalf("recovery-held effective TX snapshot = %s, want %s", got, max(proposedTX, time.Second))
				}
				pkt.State, pkt.Final = StateUp, true
				s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if s.pollActive {
					t.Fatal("Final did not complete recovery stage")
				}
				synctest.Sleep(time.Until(s.separateUntil))
				s.processTimerUpdates(tx, detect)
				if proposedTX < time.Second {
					if !s.pollActive || s.buildControlPacket().DesiredMinTxInterval != microsecondsFromDuration(proposedTX) {
						t.Fatal("floor completion did not start retained slow-to-fast recovery")
					}
					s.sendControl(t.Context())
					s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
					synctest.Sleep(time.Until(s.separateUntil))
					s.processTimerUpdates(tx, detect)
				}
				status := s.timerUpdateSnapshot()
				if status.State != timerUpdateFailed || status.Confirmed != tuple || status.Unresolved {
					t.Fatalf("recovery confirmation/terminality: %+v", status)
				}
				status, err := s.admitTimerUpdate(t.Context(), tuple, 2)
				if err != nil || status.State != timerUpdateApplied || status.Changed {
					t.Fatalf("fresh generation did not adopt recovered tuple: %+v %v", status, err)
				}
			})
		})
	}
}

func TestSessionTimerUpdateCancellationAndNonUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newRemoteTxTestSession(t)
		tuple := timerTuple{200 * time.Millisecond, 0, 3}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := s.admitTimerUpdate(ctx, tuple, 1); !errors.Is(err, context.Canceled) || s.waitingUpdate.Present {
			t.Fatal("cancellation before admission retained intent")
		}
		setTimerTestState(s, StateDown, DiagNone)
		ctx, cancel = context.WithCancel(t.Context())
		first, err := s.admitTimerUpdate(ctx, tuple, 1)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		s.processTimerUpdates(tx, detect)
		// RFC 5880 Section 6.8.3: outside Up the change is effected immediately
		// and the admitted proposal survives the caller's cancellation.
		if !s.activeUpdate.Present || s.waitingUpdate.Present || !s.pollActive ||
			s.buildControlPacket().RequiredMinRxInterval != 0 || first.State != timerUpdatePending ||
			s.activeUpdate.Deadline != first.Requested.Deadline {
			t.Fatal("outside-Up admission was not effected immediately with its original deadline")
		}
		s.closeTimerUpdates()
		if s.timerUpdateSnapshot().State != timerUpdateFailed || s.activeUpdate.Present || s.waitingUpdate.Present {
			t.Fatal("shutdown claimed success or retained ownership")
		}
	})
}

func TestSessionTimerUpdateMatrix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                             string
		tx, rx, effectiveTX, effectiveRX time.Duration
	}{
		{"increase TX", 200 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond},
		{"decrease TX", 50 * time.Millisecond, 100 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond},
		{"increase RX", 100 * time.Millisecond, 200 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond},
		{"decrease RX", 100 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond},
		{"zero RX", 100 * time.Millisecond, 0, 100 * time.Millisecond, 100 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, sender, tx, detect := newRemoteTxTestSession(t)
				s.remoteMinRxInterval = time.Microsecond
				if _, err := s.admitTimerUpdate(t.Context(), timerTuple{tc.tx, tc.rx, 3}, 1); err != nil {
					t.Fatal(err)
				}
				s.processTimerUpdates(tx, detect)
				pkt := s.buildControlPacket()
				if !pkt.Poll || pkt.DesiredMinTxInterval != microsecondsFromDuration(tc.tx) ||
					pkt.RequiredMinRxInterval != microsecondsFromDuration(tc.rx) {
					t.Fatalf("advertised proposal: %+v", pkt)
				}
				if s.desiredMinTxInterval != tc.effectiveTX || s.requiredMinRxInterval != tc.effectiveRX {
					t.Fatal("incorrect protected effective tuple")
				}
				pkt.Poll, pkt.Final = false, true
				pkt.RequiredMinRxInterval = 1 // Peer policy is independent of our proposed RX.
				s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if !s.pollActive {
					t.Fatal("early Final completed unsent proposal")
				}
				sender.failures = 1
				s.handleTxTimer(t.Context(), tx)
				s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if !s.pollActive {
					t.Fatal("failed send established Poll")
				}
				s.handleTxTimer(t.Context(), tx)
				synctest.Sleep(time.Millisecond)
				s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				status := s.timerUpdateSnapshot()
				if status.State != timerUpdateApplied || s.desiredMinTxInterval != tc.tx || s.requiredMinRxInterval != tc.rx {
					t.Fatalf("Final not applied: %+v", status)
				}
			})
		})
	}
}

func TestSessionTimerUpdateDeadlines(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newRemoteTxTestSession(t)
		s.remoteMinRxInterval = time.Microsecond
		s.remoteDesiredMinTxInterval, s.remoteDetectMult = time.Microsecond, 3
		s.recordValidReceivedPacket()
		s.sendControl(t.Context())
		tx.Reset(100 * time.Millisecond)
		detect.Reset(300 * time.Millisecond)
		synctest.Sleep(50 * time.Millisecond)
		// TX increase and RX decrease defer both effects until Final:
		// merely advertising them must not re-jitter existing deadlines.
		if _, err := s.admitTimerUpdate(t.Context(), timerTuple{200 * time.Millisecond, 0, 3}, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		synctest.Sleep(50*time.Millisecond - time.Nanosecond)
		select {
		case <-tx.C:
			t.Fatal("unchanged effective TX deadline was resampled")
		default:
		}
		synctest.Sleep(time.Nanosecond)
		select {
		case <-tx.C:
		default:
			t.Fatal("unchanged effective TX deadline was postponed")
		}
		// A new effective RX increase uses the previous valid receive,
		// not the admission instant (no event loop runs in this unit case).
		s2, _, tx2, detect2 := newRemoteTxTestSession(t)
		s2.remoteDesiredMinTxInterval, s2.remoteDetectMult = time.Microsecond, 3
		s2.recordValidReceivedPacket()
		synctest.Sleep(50 * time.Millisecond)
		proposal := timerTuple{100 * time.Millisecond, 200 * time.Millisecond, 3}
		if _, err := s2.admitTimerUpdate(t.Context(), proposal, 1); err != nil {
			t.Fatal(err)
		}
		s2.processTimerUpdates(tx2, detect2)
		synctest.Sleep(550 * time.Millisecond)
		select {
		case <-detect2.C:
		default:
			t.Fatal("RX deadline was extended from admission rather than valid receive")
		}
	})
}

func TestConfigTimerUpdateOwnershipAndRetirement(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	cfg := ownershipTestConfig("192.0.2.90")
	desired := []ReconcileConfig{{
		SessionConfig: cfg, DesiredGeneration: 1, SenderLeaseFactory: ownershipSenderLeaseFactory(),
	}}
	initial := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
	if initial.Err() != nil {
		t.Fatal(initial.Err())
	}
	desired[0].DesiredGeneration = 2
	desired[0].SessionConfig.DesiredMinTxInterval = 200 * time.Millisecond
	result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
	if result.Pending != 1 || len(result.TimerUpdates) != 1 {
		t.Fatalf("admission: %+v", result)
	}
	_, claimErr := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory())
	if !errors.Is(claimErr, ErrSessionParameterConflict) {
		t.Fatal("API shared a pending timer transaction")
	}
	_, _, reconcileErr := mgr.ReconcileSessionsForOwner(
		t.Context(), MicroBFDReconciliationOwner(), []ReconcileConfig{{SessionConfig: cfg}},
	)
	if !errors.Is(reconcileErr, ErrSessionParameterConflict) {
		t.Fatal("declarative owner shared a pending timer transaction")
	}
	ref := result.TimerUpdates[0]
	// White-box identity check: even exact discriminator/revision/generation
	// reuse cannot make a retired session's notification apply to a replacement.
	mgr.mu.Lock()
	entry := mgr.sessions[ref.LocalDiscriminator]
	original := entry.session
	replacement := newCachedPacketTestSession(MaxPacketSize)
	replacement.timerUpdate = original.timerUpdateSnapshot()
	entry.session = replacement
	mgr.mu.Unlock()
	observed := mgr.ObserveTimerUpdates(result.TimerUpdates)
	mgr.mu.Lock()
	entry.session = original
	mgr.mu.Unlock()
	if observed.Failed != 1 || observed.Pending != 0 || observed.Updated != 0 {
		t.Fatal("replacement satisfied predecessor's transaction reference")
	}
}

func TestTimerUpdateExistingOwnerContract(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	cfg := ownershipTestConfig("192.0.2.90")
	key, err := sessionKeyFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := normalizeEffectiveSessionConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	session := newCachedPacketTestSession(MaxPacketSize)
	session.desiredMinTxInterval, session.requiredMinRxInterval = cfg.DesiredMinTxInterval, cfg.RequiredMinRxInterval
	session.pollActive = true // Existing owner, unresolved startup/recovery Poll.
	owner := compatibilityAPISessionOwner()
	mgr.sessionsByKey[key] = &sessionEntry{
		session: session, effective: effective, owners: map[SessionOwner]struct{}{owner: {}},
	}
	if _, _, err := mgr.claimExisting(key, effective, owner, false); !errors.Is(err, ErrDuplicateSession) {
		t.Fatalf("existing API claim lost duplicate contract: %v", err)
	}
	if _, _, err := mgr.claimExisting(key, effective, owner, true); err != nil {
		t.Fatalf("existing idempotent claim lost ownership: %v", err)
	}
	_, _, claimErr := mgr.claimExisting(key, effective, ConfigReconciliationOwner(), true)
	if !errors.Is(claimErr, ErrSessionParameterConflict) {
		t.Fatal("new sharing claim accepted while Poll unresolved")
	}
}

func BenchmarkSessionTimerUpdate(b *testing.B) {
	b.Run("steady", func(b *testing.B) {
		s := newCachedPacketTestSession(MaxPacketSize)
		s.sender = ownershipNoopSender{}
		s.cachedState = StateUp
		s.mu.Lock()
		s.localPair.Store(packStateDiag(StateUp, DiagNone))
		s.observation.State = StateUp
		s.mu.Unlock()
		s.observation.RemoteState = StateUp
		s.remoteMinRxInterval = 100 * time.Millisecond
		packet := s.buildControlPacket()
		tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
		defer tx.Stop()
		defer detect.Stop()
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			s.handleRecvPacket(ctx, recvItem{pkt: packet}, tx, detect)
			s.handleTxTimer(ctx, tx)
		}
	})
	b.Run("admission", func(b *testing.B) {
		s := newCachedPacketTestSession(MaxPacketSize)
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, err := s.admitTimerUpdate(ctx, timerTuple{time.Second, time.Second, 3}, s.timerRevision+1)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("status", func(b *testing.B) {
		s := newCachedPacketTestSession(MaxPacketSize)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_ = s.timerUpdateSnapshot()
		}
	})
}

// RFC 5880 Section 6.8.3: the deferrals for TX increases and RX decreases
// apply only "when bfd.SessionState is Up"; "In any case other than those
// explicitly called out above, timing parameter changes MUST be effected
// immediately". A restore admitted after a local Required Min RX zero
// hold-down drove the session Down must reach the wire before Up, otherwise a
// peer obeying the zero stays silent and the session never recovers.
func TestTimerUpdateAppliesWhileNotUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, sender, tx, detect := newRemoteTxTestSession(t)
		s.requiredMinRxInterval = 0
		pkt := s.buildControlPacket()
		pkt.RequiredMinRxInterval = 100_000
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		synctest.Sleep(s.calcDetectionTimeHot())
		<-detect.C
		s.handleDetectTimer(t.Context(), tx, detect)
		if s.State() != StateDown {
			t.Fatalf("state after silent peer = %s, want Down", s.State())
		}
		restore := timerTuple{300 * time.Millisecond, 300 * time.Millisecond, 3}
		if _, err := s.admitTimerUpdate(t.Context(), restore, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		synctest.Sleep(slowTxInterval)
		if !fireRemoteTxTimer(s, tx) {
			t.Fatal("floor Poll was not transmitted in Down")
		}
		assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], true, false)
		// The peer leaves Up on our Down and answers the floor Poll.
		pkt.State, pkt.Final = StateDown, true
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		if s.State() != StateInit {
			t.Fatalf("state after peer Down Final = %s, want Init", s.State())
		}
		synctest.Sleep(time.Until(s.separateUntil))
		s.processTimerUpdates(tx, detect)
		synctest.Sleep(slowTxInterval)
		if !fireRemoteTxTimer(s, tx) {
			t.Fatal("periodic transmission stopped in Init")
		}
		last := sender.packets[len(sender.packets)-1]
		assertCachedPacketFlags(t, last, true, false)
		var sent ControlPacket
		if err := UnmarshalControlPacket(last, &sent); err != nil {
			t.Fatal(err)
		}
		if sent.RequiredMinRxInterval != microsecondsFromDuration(restore.RX) {
			t.Fatalf("Init packet Required Min RX = %d, want restored %s", sent.RequiredMinRxInterval, restore.RX)
		}
		pkt.State = StateUp
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		if status := s.timerUpdateSnapshot(); s.State() != StateUp || status.State != timerUpdateApplied ||
			status.Confirmed != restore {
			t.Fatalf("after peer Up Final: state=%s receipt=%+v", s.State(), status)
		}
	})
}

func setTimerTestState(s *Session, state State, diag Diag) {
	s.mu.Lock()
	s.localPair.Store(packStateDiag(state, diag))
	s.observation.State = state
	s.mu.Unlock()
	s.cachedState = state
}

// An Up RX decrease Poll interrupted by loss of Up leaves the unconfirmed tuple
// effective. A restore equal to the confirmed tuple must reach the wire with a
// Poll; it is not Applied while the wire still carries the interrupted value.
func TestTimerRestoreAfterInterruptedPollIsNotFalselyApplied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, sender, tx, detect := newRemoteTxTestSession(t)
		if _, err := s.admitTimerUpdate(t.Context(), timerTuple{100 * time.Millisecond, 0, 3}, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		s.sendControl(t.Context()) // The Poll is sent; its Final is lost.
		s.handleDetectTimer(t.Context(), tx, detect)
		if s.State() != StateDown || s.requiredMinRxInterval != 0 {
			t.Fatalf("interrupted Poll: state=%s effective RX=%s", s.State(), s.requiredMinRxInterval)
		}
		restore := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, 3}
		if _, err := s.admitTimerUpdate(t.Context(), restore, 2); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		synctest.Sleep(slowTxInterval)
		if !fireRemoteTxTimer(s, tx) {
			t.Fatal("floor Poll was not transmitted")
		}
		pkt := s.buildControlPacket()
		pkt.MyDiscriminator, pkt.State, pkt.Final = 7, StateDown, true
		pkt.RequiredMinRxInterval = 100_000 // The peer itself does not suppress our TX.
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		synctest.Sleep(time.Until(s.separateUntil))
		s.processTimerUpdates(tx, detect)
		if status := s.timerUpdateSnapshot(); status.State != timerUpdatePending || !s.activeUpdate.Present || !s.pollActive {
			t.Fatalf("restore receipt before its Poll Final: %+v", status)
		}
		synctest.Sleep(slowTxInterval)
		if !fireRemoteTxTimer(s, tx) {
			t.Fatal("restore Poll was not transmitted")
		}
		last := sender.packets[len(sender.packets)-1]
		assertCachedPacketFlags(t, last, true, false)
		var sent ControlPacket
		if err := UnmarshalControlPacket(last, &sent); err != nil {
			t.Fatal(err)
		}
		if sent.RequiredMinRxInterval != 100_000 {
			t.Fatalf("restore Poll Required Min RX = %d, want 100000", sent.RequiredMinRxInterval)
		}
	})
}

// RFC 5880 Section 6.8.3: "If either bfd.DesiredMinTxInterval is changed or
// bfd.RequiredMinRxInterval is changed, a Poll Sequence MUST be initiated".
// Entering Up from an update applied outside Up keeps the one-second floor
// until a Poll carries the faster Desired Min TX.
func TestTimerUpdateOutsideUpKeepsFloorUntilUpPoll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, sender, tx, detect := newRemoteTxTestSession(t)
		setTimerTestState(s, StateDown, DiagNone)
		fast := timerTuple{50 * time.Millisecond, 50 * time.Millisecond, 3}
		if _, err := s.admitTimerUpdate(t.Context(), fast, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		s.sendControl(t.Context())
		pkt := s.buildControlPacket()
		pkt.MyDiscriminator, pkt.State, pkt.Final = 7, StateInit, true
		s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		if s.State() != StateUp {
			t.Fatalf("state = %s, want Up", s.State())
		}
		var sent ControlPacket
		if err := UnmarshalControlPacket(sender.packets[len(sender.packets)-1], &sent); err != nil {
			t.Fatal(err)
		}
		if sent.DesiredMinTxInterval != 1_000_000 || s.advertisedTimers().TX != slowTxInterval {
			t.Fatalf("Up entry advertised TX %d without a Poll", sent.DesiredMinTxInterval)
		}
		synctest.Sleep(time.Until(s.separateUntil))
		s.processTimerUpdates(tx, detect)
		if !s.pollActive {
			t.Fatal("slow-to-fast Poll was not started after Up entry")
		}
		s.sendControl(t.Context())
		last := sender.packets[len(sender.packets)-1]
		assertCachedPacketFlags(t, last, true, false)
		if err := UnmarshalControlPacket(last, &sent); err != nil {
			t.Fatal(err)
		}
		if sent.DesiredMinTxInterval != 50_000 {
			t.Fatalf("slow-to-fast Poll Desired Min TX = %d, want 50000", sent.DesiredMinTxInterval)
		}
	})
}

// An update applied outside Up whose Poll never receives a Final fails at its
// deadline without blocking later generations.
func TestTimerUpdateOutsideUpExpiresWithoutFinal(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, sender, tx, detect := newRemoteTxTestSession(t)
		setTimerTestState(s, StateDown, DiagNone)
		s.role, s.remoteDiscr = RolePassive, 0
		first, err := s.admitTimerUpdate(t.Context(), timerTuple{200 * time.Millisecond, 200 * time.Millisecond, 3}, 1)
		if err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		synctest.Sleep(time.Until(first.Requested.Deadline))
		s.processTimerUpdates(tx, detect)
		if s.timerUpdateSnapshot().State != timerUpdateFailed || sender.attempts != 0 {
			t.Fatal("unsendable outside-Up Poll did not fail at its deadline")
		}
		status, err := s.admitTimerUpdate(t.Context(), timerTuple{300 * time.Millisecond, 300 * time.Millisecond, 3}, 2)
		if err != nil || status.State != timerUpdatePending {
			t.Fatalf("later generation after outside-Up expiry: %+v %v", status, err)
		}
	})
}

// RFC 5880 Section 6.8.3: an RX decrease outside Up changes the Detection Time
// immediately, while its Poll is still active.
func TestTimerUpdateOutsideUpRXDecreaseIsImmediate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newRemoteTxTestSession(t)
		setTimerTestState(s, StateDown, DiagNone)
		s.remoteDesiredMinTxInterval, s.remoteDetectMult = 10*time.Millisecond, 3
		decrease := timerTuple{100 * time.Millisecond, 50 * time.Millisecond, 3}
		if _, err := s.admitTimerUpdate(t.Context(), decrease, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		if !s.pollActive || s.requiredMinRxInterval != 50*time.Millisecond ||
			s.calcDetectionTimeHot() != 150*time.Millisecond {
			t.Fatalf("outside-Up RX decrease: Poll=%t RX=%s DT=%s",
				s.pollActive, s.requiredMinRxInterval, s.calcDetectionTimeHot())
		}
	})
}

// Outside Up, a promotion waits only for an active Poll or its separation;
// AdminDown follows the same Section 6.8.3 rule.
func TestTimerUpdateOutsideUpWaitsForPollAndSeparation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		state          State
		diag           Diag
		poll, separate bool
	}{
		{name: "Down with floor Poll", state: StateDown, poll: true},
		{name: "Down with separation only", state: StateDown, separate: true},
		{name: "AdminDown", state: StateAdminDown, diag: DiagAdminDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, _, tx, detect := newRemoteTxTestSession(t)
				setTimerTestState(s, tc.state, tc.diag)
				s.pollActive = tc.poll
				if tc.separate {
					s.separateUntil = time.Now().Add(10 * time.Millisecond)
				}
				tuple := timerTuple{200 * time.Millisecond, 300 * time.Millisecond, 3}
				if _, err := s.admitTimerUpdate(t.Context(), tuple, 1); err != nil {
					t.Fatal(err)
				}
				s.processTimerUpdates(tx, detect)
				if tc.poll || tc.separate {
					if s.activeUpdate.Present || s.requiredMinRxInterval != 100*time.Millisecond {
						t.Fatal("promotion bypassed an active Poll or separation")
					}
					if tc.poll {
						return
					}
					synctest.Sleep(10 * time.Millisecond)
					s.processTimerUpdates(tx, detect)
				}
				if !s.activeUpdate.Present || !s.pollActive || s.requiredMinRxInterval != tuple.RX {
					t.Fatalf("%s: update was not effected immediately", tc.name)
				}
			})
		})
	}
}
