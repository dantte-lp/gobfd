package bfd

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type preparedPolicyTestSender struct{ ownershipNoopSender }

func (preparedPolicyTestSender) PaddingPrepared() bool { return true }

func TestConfigSessionPolicyUpdateAdmission(t *testing.T) {
	t.Parallel()
	for _, padding := range []bool{false, true} {
		t.Run(map[bool]string{false: "multihop TTL", true: "prepared padding"}[padding], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				cfg := ownershipTestConfig("192.0.2.90")
				cfg.PaddedPduSize = 128
				if !padding {
					cfg.Type, cfg.MultiHopMaxHops = SessionTypeMultiHop, 1
					cfg.Interface = ""
				}
				desired := []ReconcileConfig{{
					SessionConfig: cfg, DesiredGeneration: 1,
					SenderLeaseFactory: NonOwningSenderLeaseFactory(preparedPolicyTestSender{}),
				}}
				initial := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
				if initial.Err() != nil || initial.Created != 1 {
					t.Fatalf("initial reconcile: %+v", initial)
				}
				discr := mgr.Sessions()[0].LocalDiscr
				original, exists := mgr.LookupByDiscriminator(discr)
				if !exists {
					t.Fatal("missing original session")
				}
				desired[0].DesiredGeneration = 2
				if padding {
					desired[0].SessionConfig.PaddedPduSize = 512
				} else {
					desired[0].SessionConfig.MultiHopMaxHops = 5
				}
				result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
				if result.Pending != 1 || result.Created != 0 || result.Failed != 0 || len(result.TimerUpdates) != 1 {
					t.Fatalf("qualified policy reload must queue on the original session: %+v", result)
				}
				synctest.Wait()
				observed := mgr.ObserveTimerUpdates(result.TimerUpdates)
				after, exists := mgr.LookupByDiscriminator(discr)
				if observed.Failed != 0 || observed.Pending != 0 || observed.Updated != 1 || !exists || after != original {
					t.Fatalf("policy update failed to preserve and commit the original session: %+v", observed)
				}
				wrong := result.TimerUpdates[0]
				if padding {
					wrong.PaddedPduSize++
				} else {
					wrong.MultiHopMinTTL--
				}
				if got := mgr.ObserveTimerUpdates([]TimerUpdateRef{wrong}); got.Failed != 1 {
					t.Fatalf("receipt accepted a different packet policy: %+v", got)
				}
				if padding {
					if after.PaddedPduSize() != 512 {
						t.Fatal("padding policy was not applied")
					}
				} else {
					after.mu.RLock()
					floor := after.minTTL
					after.mu.RUnlock()
					if floor != 250 {
						t.Fatalf("receive TTL floor = %d, want 250", floor)
					}
				}
			})
		})
	}
}

type capturedPreparedPolicySender struct{ *retryCachedPacketSender }

func (capturedPreparedPolicySender) PaddingPrepared() bool { return true }

func newPacketPolicyTestSession(t *testing.T) (*Session, *retryCachedPacketSender, *time.Timer, *time.Timer) {
	t.Helper()
	s, sender, tx, detect := newRemoteTxTestSession(t)
	s.sender = capturedPreparedPolicySender{sender}
	s.sessionType, s.ifName, s.minTTL = SessionTypeMultiHop, "", 254
	s.paddedPduSize, s.cachedPacket = 128, make([]byte, 128)
	return s, sender, tx, detect
}

func TestSessionPacketPolicyUpdateWireAndGrowth(t *testing.T) {
	t.Parallel()
	for _, authenticated := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "authenticated"}[authenticated], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, sender, tx, detect := newPacketPolicyTestSession(t)
				if authenticated {
					keys, err := NewStaticAuthKeyStore(AuthKey{
						ID: 7, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("policy-test-key"),
					})
					if err != nil {
						t.Fatal(err)
					}
					s.auth, s.authKeys = MeticulousKeyedSHA1Auth{}, keys
					s.authState = &AuthState{Type: AuthTypeMeticulousKeyedSHA1, XmitAuthSeq: 1000}
				}
				receiver := &AuthState{Type: AuthTypeMeticulousKeyedSHA1}
				for i, target := range []uint16{9000, 26} {
					tuple := s.effectiveTimersLocked()
					tuple.MinTTL, tuple.PaddedPduSize = 250, target
					admitted, err := s.admitTimerUpdate(t.Context(), tuple, uint64(i+1))
					if err != nil {
						t.Fatal(err)
					}
					if i == 0 && (len(s.waitingPacket) != 9000 || len(s.cachedPacket) != 128) {
						t.Fatal("admission failed to stage growth without replacing the active buffer")
					}
					s.processTimerUpdates(tx, detect)
					s.sendControl(t.Context())
					status := s.timerUpdateSnapshot()
					if status.State != timerUpdateApplied || status.Confirmed != tuple || status.Effective != tuple ||
						s.pollActive || s.waitingPacket != nil || len(s.cachedPacket) != 9000 {
						t.Fatalf("packet policy was not committed coherently: %+v", status)
					}
					raw := sender.packets[len(sender.packets)-1]
					var packet ControlPacket
					if decodeErr := UnmarshalControlPacket(raw, &packet); decodeErr != nil {
						t.Fatal(decodeErr)
					}
					length := MinPacketSizeNoAuth
					if authenticated {
						length = 52
						if verifyErr := s.auth.Verify(receiver, s.authKeys, &packet, raw, len(raw)); verifyErr != nil {
							t.Fatal(verifyErr)
						}
					}
					if int(packet.Length) != length || len(raw) != max(length, int(target)) || packet.Poll || packet.Final {
						t.Fatalf("policy wire length/flags changed the BFD PDU: %+v, payload %d", packet, len(raw))
					}
					for _, value := range raw[length:] {
						if value != 0 {
							t.Fatal("transmitted nonzero transport padding")
						}
					}
					repeated, err := s.admitTimerUpdate(t.Context(), tuple, uint64(i+1))
					if err != nil || repeated.Requested.Revision != admitted.Requested.Revision ||
						repeated.Requested.Deadline != admitted.Requested.Deadline || s.waitingPacket != nil {
						t.Fatal("repeated policy intent allocated or restarted a transaction")
					}
				}
			})
		})
	}
}

func TestSessionPacketPolicyRejectsUnqualifiedWholeTuple(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"enable", "disable", "unprepared", "Demand", "single-hop TTL", "invalid floor"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, sender, _, _ := newPacketPolicyTestSession(t)
				if kind == "enable" {
					s.paddedPduSize = 0
				}
				if kind == "unprepared" {
					s.sender = sender
				}
				if kind == "Demand" {
					s.remoteDemandMode = true
				}
				if kind == "single-hop TTL" {
					s.sessionType, s.minTTL = SessionTypeSingleHop, 0
				}
				before := s.timerUpdateSnapshot()
				tuple := before.Confirmed
				tuple.TX, tuple.DetectMult, tuple.MinTTL, tuple.PaddedPduSize = 200*time.Millisecond, 5, 250, 512
				if kind == "disable" {
					tuple.PaddedPduSize = 0
				}
				if kind == "invalid floor" {
					tuple.MinTTL = 0
				}
				if err := s.validatePacketPolicyUpdate(tuple); !errors.Is(err, ErrSessionParameterConflict) {
					t.Fatalf("unqualified preflight error = %v", err)
				}
				if _, err := s.admitTimerUpdate(t.Context(), tuple, 1); !errors.Is(err, ErrSessionParameterConflict) {
					t.Fatalf("unqualified admission error = %v", err)
				}
				if after := s.timerUpdateSnapshot(); after != before || s.waitingPacket != nil {
					t.Fatal("rejected packet policy partially changed the tuple or receipt")
				}
			})
		})
	}
}

func TestSessionLocalTTLUpdateUnderDemandNeedsNoPoll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newPacketPolicyTestSession(t)
		s.remoteDemandMode = true
		tuple := s.effectiveTimersLocked()
		tuple.MinTTL = 250
		if _, err := s.admitTimerUpdate(t.Context(), tuple, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		if status := s.timerUpdateSnapshot(); status.State != timerUpdateApplied || s.pollActive {
			t.Fatalf("local receive policy incorrectly initiated a Poll: %+v", status)
		}
		if s.transportOutcome(PacketMeta{MultiHop: true, TTL: 249}) != DeliveryTTLRejected ||
			s.transportOutcome(PacketMeta{MultiHop: true, TTL: 250}) != DeliveryFound {
			t.Fatal("receive path did not apply the new coherent TTL floor")
		}
	})
}

func TestSessionQueuedPaddingDemandRejectsWholeTuple(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newPacketPolicyTestSession(t)
		active := s.effectiveTimersLocked()
		active.TX = 200 * time.Millisecond
		if _, err := s.admitTimerUpdate(t.Context(), active, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		s.sendControl(t.Context())
		waiting := timerTuple{50 * time.Millisecond, 200 * time.Millisecond, 5, 250, 512}
		if _, err := s.admitTimerUpdate(t.Context(), waiting, 2); err != nil {
			t.Fatal(err)
		}
		peer := ControlPacket{
			Version: Version, State: StateUp, Final: true, Demand: true, DetectMult: 3,
			MyDiscriminator: 7, YourDiscriminator: 42,
			DesiredMinTxInterval: 100_000, RequiredMinRxInterval: 100_000,
		}
		s.handleRecvPacket(t.Context(), recvItem{pkt: peer}, tx, detect)
		synctest.Sleep(time.Microsecond)
		s.processTimerUpdates(tx, detect)
		status := s.timerUpdateSnapshot()
		if status.State != timerUpdateFailed || status.Requested.Generation != 2 || status.Confirmed != active ||
			status.Effective != active || s.waitingPacket != nil || len(s.cachedPacket) != 128 || s.pollActive {
			t.Fatalf("queued Demand transition partially applied or confirmed the policy: %+v", status)
		}
	})
}

func TestSessionPacketPolicyMixedPollKeepsActiveAdvertisement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, sender, tx, detect := newPacketPolicyTestSession(t)
		first := timerTuple{200 * time.Millisecond, 50 * time.Millisecond, 5, 252, 512}
		if _, err := s.admitTimerUpdate(t.Context(), first, 1); err != nil {
			t.Fatal(err)
		}
		s.processTimerUpdates(tx, detect)
		s.sendControl(t.Context())
		if s.desiredMinTxInterval != 100*time.Millisecond || s.requiredMinRxInterval != 100*time.Millisecond ||
			s.minTTL != 252 || s.PaddedPduSize() != 512 {
			t.Fatal("mixed update violated protected TX/RX or immediate packet policy")
		}
		latest := timerTuple{300 * time.Millisecond, 200 * time.Millisecond, 7, 250, 9000}
		if _, err := s.admitTimerUpdate(t.Context(), latest, 2); err != nil {
			t.Fatal(err)
		}
		peer := ControlPacket{
			Version: Version, State: StateUp, Poll: true, DetectMult: 3,
			MyDiscriminator: 7, YourDiscriminator: 42,
			DesiredMinTxInterval: 100_000, RequiredMinRxInterval: 100_000,
		}
		s.handleRecvPacket(t.Context(), recvItem{pkt: peer}, tx, detect)
		raw := sender.packets[len(sender.packets)-1]
		var crossed ControlPacket
		if err := UnmarshalControlPacket(raw, &crossed); err != nil {
			t.Fatal(err)
		}
		if len(raw) != 512 || crossed.DesiredMinTxInterval != 200_000 || crossed.RequiredMinRxInterval != 50_000 ||
			crossed.DetectMult != 5 || crossed.Poll || !crossed.Final || s.minTTL != 252 {
			t.Fatalf("queued policy changed a crossed Final: %+v, payload %d", crossed, len(raw))
		}
		synctest.Sleep(time.Microsecond)
		peer.Poll, peer.Final = false, true
		s.handleRecvPacket(t.Context(), recvItem{pkt: peer}, tx, detect)
		s.processTimerUpdates(tx, detect)
		if s.timerUpdateSnapshot().Confirmed != first || s.pollActive || len(s.cachedPacket) != 512 {
			t.Fatal("queued policy bypassed first confirmation or Poll separation")
		}
		synctest.Sleep(time.Microsecond)
		s.processTimerUpdates(tx, detect)
		if s.desiredMinTxInterval != 200*time.Millisecond || s.requiredMinRxInterval != 200*time.Millisecond ||
			s.minTTL != 250 || s.PaddedPduSize() != 9000 || !s.pollActive {
			t.Fatal("latest mixed tuple did not preserve protected TX or commit its packet policy")
		}
		s.sendControl(t.Context())
		raw = sender.packets[len(sender.packets)-1]
		var packet ControlPacket
		if err := UnmarshalControlPacket(raw, &packet); err != nil {
			t.Fatal(err)
		}
		if len(raw) != 9000 || packet.DesiredMinTxInterval != 300_000 || packet.RequiredMinRxInterval != 200_000 ||
			packet.DetectMult != 7 || !packet.Poll || packet.Final {
			t.Fatalf("latest mixed advertisement = %+v, payload %d", packet, len(raw))
		}
		s.handleRecvPacket(t.Context(), recvItem{pkt: peer}, tx, detect)
		status := s.timerUpdateSnapshot()
		if status.State != timerUpdateApplied || status.Requested.Generation != 2 ||
			status.Confirmed != latest || status.Effective != latest || s.LocalDiscriminator() != 42 {
			t.Fatalf("latest mixed transaction failed to confirm the exact policy: %+v", status)
		}
	})
}

func TestSessionPolicyWaitingBufferSupersessionAndCleanup(t *testing.T) {
	t.Parallel()
	for _, finish := range []string{"expiry", "interruption", "retirement"} {
		t.Run(finish, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, _, tx, detect := newPacketPolicyTestSession(t)
				s.pollActive = true
				tuple := s.effectiveTimersLocked()
				tuple.PaddedPduSize = 512
				if _, err := s.admitTimerUpdate(t.Context(), tuple, 1); err != nil {
					t.Fatal(err)
				}
				buffer := &s.waitingPacket[0]
				if _, err := s.admitTimerUpdate(t.Context(), tuple, 1); err != nil || &s.waitingPacket[0] != buffer {
					t.Fatal("repeated waiting intent replaced its prepared buffer")
				}
				tuple.PaddedPduSize = 256
				if _, err := s.admitTimerUpdate(t.Context(), tuple, 2); err != nil {
					t.Fatal(err)
				}
				if len(s.waitingPacket) != 256 || len(s.cachedPacket) != 128 {
					t.Fatal("supersession retained an obsolete waiting buffer or changed active TX")
				}
				switch finish {
				case "expiry":
					synctest.Sleep(time.Until(s.timerUpdateSnapshot().Requested.Deadline))
					s.processTimerUpdates(tx, detect)
				case "interruption":
					s.mu.Lock()
					s.interruptTimerUpdateLocked()
					s.mu.Unlock()
				case "retirement":
					s.closeTimerUpdates()
				}
				if s.waitingPacket != nil || s.PaddedPduSize() != 128 || len(s.cachedPacket) != 128 {
					t.Fatal("abandoned policy buffer remained retained or changed the active policy")
				}
			})
		})
	}
}

func TestSessionPacketPolicyConcurrentSnapshots(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, tx, detect := newPacketPolicyTestSession(t)
		var readers sync.WaitGroup
		readers.Go(func() {
			for range 1000 {
				if size := s.PaddedPduSize(); size != 128 && size != 512 {
					t.Errorf("incoherent padding snapshot: %d", size)
				}
				outcome := s.transportOutcome(PacketMeta{MultiHop: true, TTL: 251})
				if outcome != DeliveryFound && outcome != DeliveryTTLRejected {
					t.Errorf("incoherent TTL policy outcome: %s", outcome)
				}
			}
		})
		for generation := range uint64(1000) {
			tuple := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, 3, 254, 128}
			if generation%2 != 0 {
				tuple.MinTTL, tuple.PaddedPduSize = 250, 512
			}
			if _, err := s.admitTimerUpdate(t.Context(), tuple, generation+1); err != nil {
				t.Fatal(err)
			}
			s.processTimerUpdates(tx, detect)
		}
		readers.Wait()
	})
}

func TestManagerUnqualifiedPolicyDoesNotReleaseStaleClaims(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		desired := make([]ReconcileConfig, 0, 2)
		for _, peer := range []string{"192.0.2.90", "192.0.2.91"} {
			cfg := ownershipTestConfig(peer)
			cfg.PaddedPduSize = 128
			desired = append(desired, ReconcileConfig{
				SessionConfig:      cfg,
				SenderLeaseFactory: NonOwningSenderLeaseFactory(preparedPolicyTestSender{}),
			})
		}
		initial := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if initial.Err() != nil || initial.Created != 2 {
			t.Fatalf("seed packet policies: %+v", initial)
		}
		before := mgr.Sessions()
		desired = desired[:1]
		desired[0].SessionConfig.PaddedPduSize = 0
		result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if !errors.Is(result.Err(), ErrSessionParameterConflict) || result.Released != 0 || result.Created != 0 ||
			len(mgr.Sessions()) != 2 {
			t.Fatalf("unqualified policy changed source ownership: %+v", result)
		}
		for _, previous := range before {
			current, exists := mgr.LookupByDiscriminator(previous.LocalDiscr)
			if !exists || current.Ref() != previous.Ref || current.PaddedPduSize() != 128 {
				t.Fatal("unqualified policy changed an existing physical session")
			}
		}
	})
}

func TestManagerReversePolicyToConfirmedUnderDemandIsRejected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := ownershipTestConfig("192.0.2.90")
		cfg.Type, cfg.Interface, cfg.MultiHopMaxHops, cfg.PaddedPduSize = SessionTypeMultiHop, "", 1, 128
		desired := []ReconcileConfig{{
			SessionConfig: cfg, DesiredGeneration: 1,
			SenderLeaseFactory: NonOwningSenderLeaseFactory(preparedPolicyTestSender{}),
		}}
		initial := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if initial.Err() != nil || initial.Created != 1 {
			t.Fatalf("seed policy: %+v", initial)
		}
		s, exists := mgr.LookupByDiscriminator(mgr.Sessions()[0].LocalDiscr)
		if !exists {
			t.Fatal("missing original session")
		}
		peer := ControlPacket{
			Version: Version, DetectMult: 3, MyDiscriminator: 7, YourDiscriminator: s.LocalDiscriminator(),
			DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
		}
		for _, state := range []State{StateDown, StateInit} {
			peer.State = state
			if s.RecvPacket(&peer) != DeliveryQueued {
				t.Fatal("session did not admit establishment packet")
			}
			synctest.Wait()
		}
		if s.State() != StateUp {
			t.Fatal("fixture did not establish the live session")
		}
		desired[0].DesiredGeneration = 2
		desired[0].SessionConfig.DesiredMinTxInterval = 2 * time.Second
		desired[0].SessionConfig.DetectMultiplier = 5
		desired[0].SessionConfig.MultiHopMaxHops, desired[0].SessionConfig.PaddedPduSize = 5, 512
		pending := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if pending.Err() == nil || pending.Pending != 1 || pending.Failed != 0 {
			t.Fatalf("mixed policy did not await its Poll: %+v", pending)
		}
		synctest.Wait()
		peer.State, peer.Demand = StateUp, true
		if s.RecvPacket(&peer) != DeliveryQueued {
			t.Fatal("session did not admit the remote Demand transition")
		}
		synctest.Wait()
		before := s.timerUpdateSnapshot()
		if before.Effective.PaddedPduSize != 512 || before.Confirmed.PaddedPduSize != 128 ||
			before.State != timerUpdatePending {
			t.Fatalf("fixture did not preserve split effective/confirmed policy: %+v", before)
		}
		desired[0].SessionConfig, desired[0].DesiredGeneration = cfg, 3
		result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if !errors.Is(result.Err(), ErrSessionParameterConflict) || s.timerUpdateSnapshot() != before {
			t.Fatalf("reverse policy was silently accepted or changed the active transaction: %+v", result)
		}
	})
}
