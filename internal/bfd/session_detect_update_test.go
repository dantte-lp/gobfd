package bfd

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestConfigDetectMultiplierUpdateAdmission(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := ownershipTestConfig("192.0.2.90")
		desired := []ReconcileConfig{{
			SessionConfig: cfg, DesiredGeneration: 1, SenderLeaseFactory: ownershipSenderLeaseFactory(),
		}}
		initial := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if initial.Err() != nil || initial.Created != 1 {
			t.Fatalf("initial reconcile: %+v", initial)
		}
		before := mgr.Sessions()
		desired[0].DesiredGeneration = 2
		desired[0].SessionConfig.DetectMultiplier = 5
		result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if result.Pending != 1 || result.Failed != 0 || result.Created != 0 || len(result.TimerUpdates) != 1 {
			t.Fatalf("DetectMult reload must queue on the existing session: %+v", result)
		}
		after := mgr.Sessions()
		if len(after) != 1 || after[0].LocalDiscr != before[0].LocalDiscr {
			t.Fatalf("admission replaced the session: %+v", after)
		}
		ref := result.TimerUpdates[0]
		synctest.Wait()
		observed := mgr.ObserveTimerUpdates(result.TimerUpdates)
		if observed.Failed != 0 || observed.Pending != 0 || observed.Updated != 1 {
			t.Fatalf("committed owner update was not observed: %+v", observed)
		}
		ref.DetectMultiplier = 7
		if wrong := mgr.ObserveTimerUpdates([]TimerUpdateRef{ref}); wrong.Failed != 1 {
			t.Fatalf("receipt accepted a different DetectMult: %+v", wrong)
		}
		revision := ref.session.timerUpdateSnapshot().Requested.Revision
		repeated := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		after = mgr.Sessions()
		if repeated.Failed != 0 || repeated.Pending != 0 || repeated.Created != 0 ||
			len(after) != 1 || after[0].DetectMultiplier != 5 || after[0].LocalDiscr != before[0].LocalDiscr ||
			ref.session.timerUpdateSnapshot().Requested.Revision != revision {
			t.Fatalf("repeated intent replaced or restarted the update: %+v, %+v", repeated, after)
		}
	})
}

func TestSessionDetectMultiplierUpdateWire(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		demand bool
	}{
		{name: "asynchronous"},
		{name: "remote demand", demand: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, sender, tx, detect := newRemoteTxTestSession(t)
				s.remoteDemandMode = tc.demand
				tuple := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, 5, 0, 0}
				admitted, err := s.admitTimerUpdate(t.Context(), tuple, 1)
				if err != nil {
					t.Fatal(err)
				}
				s.processTimerUpdates(tx, detect)
				if s.pollActive != tc.demand {
					t.Errorf("Poll active = %t, want %t", s.pollActive, tc.demand)
				}
				s.sendControl(t.Context())
				var wire ControlPacket
				if decodeErr := UnmarshalControlPacket(sender.packets[len(sender.packets)-1], &wire); decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if wire.DetectMult != 5 || wire.Poll != tc.demand || wire.Final {
					t.Errorf("wire DetectMult/Poll/Final = %d/%t/%t", wire.DetectMult, wire.Poll, wire.Final)
				}
				if tc.demand {
					if got := s.timerUpdateSnapshot().State; got != timerUpdatePending {
						t.Fatalf("Demand update completed before Final: %v", got)
					}
					synctest.Sleep(time.Millisecond)
					peer := ControlPacket{
						Version: Version, State: StateUp, Final: true, Demand: true, DetectMult: 3,
						MyDiscriminator: 7, YourDiscriminator: 42,
						DesiredMinTxInterval: 100_000, RequiredMinRxInterval: 100_000,
					}
					s.handleRecvPacket(t.Context(), recvItem{pkt: peer}, tx, detect)
				}
				status := s.timerUpdateSnapshot()
				if status.State != timerUpdateApplied || status.Confirmed != tuple || status.Effective != tuple ||
					s.DetectMultiplier() != 5 || s.LocalDiscriminator() != 42 || s.State() != StateUp {
					t.Fatalf("update did not preserve and commit the live session: %+v", status)
				}
				repeated, err := s.admitTimerUpdate(t.Context(), tuple, 1)
				if err != nil || repeated.Requested.Revision != admitted.Requested.Revision ||
					repeated.Requested.Deadline != admitted.Requested.Deadline || s.pollActive {
					t.Fatalf("repeated generation restarted the update: %+v, %v", repeated, err)
				}
			})
		})
	}
}

func TestConfigDetectMultiplierUpdateRejectsSharedAndUnsupportedChanges(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsupported role", true: "shared owner"}[shared], func(t *testing.T) {
			t.Parallel()
			mgr := NewManager(slog.New(slog.DiscardHandler))
			t.Cleanup(mgr.Close)
			cfg := ownershipTestConfig("192.0.2.90")
			desired := []ReconcileConfig{{SessionConfig: cfg, SenderLeaseFactory: ownershipSenderLeaseFactory()}}
			initial := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
			if initial.Err() != nil {
				t.Fatal(initial.Err())
			}
			if shared {
				if _, err := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory()); err != nil {
					t.Fatal(err)
				}
			} else {
				desired[0].SessionConfig.Role = RolePassive
			}
			desired[0].SessionConfig.DetectMultiplier = 5
			result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
			if !errors.Is(result.Err(), ErrSessionParameterConflict) || mgr.Sessions()[0].DetectMultiplier != 3 {
				t.Fatalf("unsupported update mutated the shared/current session: %+v", result)
			}
		})
	}
}

func TestSessionDetectMultiplierOneReschedulesJitter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, sender, tx, detect := newRemoteTxTestSession(t)
		s.sendControl(t.Context())
		tx.Reset(100 * time.Millisecond)
		tuple := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, 1, 0, 0}
		if _, err := s.admitTimerUpdate(t.Context(), tuple, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		synctest.Sleep(75*time.Millisecond - time.Nanosecond)
		if fireRemoteTxTimer(s, tx) {
			t.Fatal("DetectMult=1 transmitted before 75% of the interval")
		}
		synctest.Sleep(15*time.Millisecond + time.Nanosecond)
		if !fireRemoteTxTimer(s, tx) || len(sender.packets) != 2 {
			t.Fatal("DetectMult=1 retained a deadline later than 90% of the interval")
		}
	})
}

func TestSessionDetectMultiplierUpdateFailureAndRetirement(t *testing.T) {
	t.Parallel()
	for _, retire := range []bool{false, true} {
		t.Run(map[bool]string{false: "Poll timeout", true: "retirement"}[retire], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, _, tx, detect := newRemoteTxTestSession(t)
				s.remoteDemandMode = true
				tuple := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, 5, 0, 0}
				if _, err := s.admitTimerUpdate(t.Context(), tuple, 1); err != nil {
					t.Fatal(err)
				}
				s.processTimerUpdates(tx, detect)
				s.sendControl(t.Context())
				if retire {
					s.closeTimerUpdates()
					if _, err := s.admitTimerUpdate(t.Context(), tuple, 2); !errors.Is(err, ErrTimerUpdateFailed) {
						t.Fatalf("retired session accepted another generation: %v", err)
					}
				} else {
					synctest.Sleep(time.Until(s.timerUpdateSnapshot().Requested.Deadline))
					s.processTimerUpdates(tx, detect)
				}
				status := s.timerUpdateSnapshot()
				if status.State != timerUpdateFailed || status.Confirmed.DetectMult != 3 || s.LocalDiscriminator() != 42 {
					t.Fatalf("failed update claimed confirmation or replaced the session: %+v", status)
				}
			})
		})
	}
}

func TestSessionDetectMultiplierConcurrentSnapshots(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newRemoteTxTestSession(t)
		var readers sync.WaitGroup
		readers.Go(func() {
			for range 1000 {
				if got := s.DetectMultiplier(); got != 3 && got != 5 {
					t.Errorf("incoherent multiplier snapshot: %d", got)
				}
			}
		})
		for generation := range uint64(1000) {
			mult := uint8(3 + 2*(generation%2))
			tuple := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, mult, 0, 0}
			if _, err := s.admitTimerUpdate(t.Context(), tuple, generation+1); err != nil {
				t.Fatal(err)
			}
			s.processTimerUpdates(tx, detect)
		}
		readers.Wait()
	})
}

func TestSessionDetectMultiplierPreservesAuthentication(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, sender, tx, detect := newRemoteTxTestSession(t)
		keys, err := NewStaticAuthKeyStore(AuthKey{
			ID: 7, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("detect-update-test"),
		})
		if err != nil {
			t.Fatal(err)
		}
		auth := MeticulousKeyedSHA1Auth{}
		state := &AuthState{
			Type: AuthTypeMeticulousKeyedSHA1, XmitAuthSeq: 1000, RcvAuthSeq: 500, AuthSeqKnown: true,
		}
		s.auth, s.authKeys, s.authState = auth, keys, state
		s.sendControl(t.Context())
		tuple := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, 5, 0, 0}
		if _, err = s.admitTimerUpdate(t.Context(), tuple, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		s.sendControl(t.Context())
		if len(sender.packets) != 2 || s.authState != state || s.authKeys != keys ||
			state.RcvAuthSeq != 500 || !state.AuthSeqKnown {
			t.Fatal("DetectMult update reset authentication or failed to transmit")
		}
		receiver := &AuthState{Type: AuthTypeMeticulousKeyedSHA1}
		var previous uint32
		for i, raw := range sender.packets {
			var packet ControlPacket
			if err = UnmarshalControlPacket(raw, &packet); err != nil {
				t.Fatal(err)
			}
			if err = auth.Verify(receiver, keys, &packet, raw, len(raw)); err != nil {
				t.Fatal(err)
			}
			if packet.Auth.KeyID != 7 || packet.DetectMult != uint8(3+2*i) ||
				(i != 0 && packet.Auth.SequenceNumber != previous+1) {
				t.Fatalf("signed DetectMult update lost key or sequence continuity: %+v", packet)
			}
			previous = packet.Auth.SequenceNumber
		}
	})
}
