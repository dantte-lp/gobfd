package bfd

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestDownPollRetainsRemoteDiscriminator(t *testing.T) {
	t.Parallel()
	for _, transition := range []struct {
		local, remote State
	}{
		{StateUp, StateDown},
		{StateUp, StateAdminDown},
		{StateInit, StateAdminDown},
	} {
		for _, role := range []SessionRole{RoleActive, RolePassive} {
			for _, fail := range []bool{false, true} {
				name := transition.local.String() + "/" + transition.remote.String() + "/" + role.String()
				if fail {
					name += "/failed send"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					s, sender, tx, detect := newRemoteTxTestSession(t)
					s.role, s.cachedState = role, transition.local
					s.commitObservation(transition.local, DiagNone, CauseUnknown)
					if fail {
						sender.failures = 1
					}
					pkt := s.buildControlPacket()
					pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = transition.remote, 99, s.localDiscr
					pkt.Poll, pkt.Final = true, false
					s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
					if s.RemoteDiscriminator() != 99 || s.State() != StateDown || s.LocalDiag() != DiagNeighborDown {
						t.Errorf("Down Poll: discriminator=%d observation=%+v", s.RemoteDiscriminator(), s.Observation())
					}
					if sender.attempts != 1 || s.pendingFinal != fail {
						t.Fatalf("immediate Final: attempts=%d pending=%t", sender.attempts, s.pendingFinal)
					}
					if fail {
						if s.PacketsSent() != 0 {
							t.Fatal("failed Final counted as sent")
						}
						s.handleTxTimer(t.Context(), tx)
					}
					if len(sender.packets) != 1 || s.pendingFinal || s.PacketsSent() != 1 {
						t.Fatal("Final did not complete exactly once")
					}
					var reply ControlPacket
					if err := UnmarshalControlPacket(sender.packets[0], &reply); err != nil {
						t.Fatal(err)
					}
					if reply.YourDiscriminator != 99 || reply.Poll || !reply.Final || reply.State != StateDown {
						t.Fatalf("Final reply=%+v", reply)
					}
				})
			}
		}
	}
}

func TestPathDownRetainsRemoteDiscriminatorUntilOwnerExpiry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, tx, detect := newAuthExpirySession(t)
		s.cachedState = StateUp
		s.commitObservation(StateUp, DiagNone, CauseUnknown)
		tx.Stop()
		detection := s.calcDetectionTimeHot()
		detect.Reset(detection)
		s.recvCh = make(chan recvItem, 1)
		s.ctrlCh = make(chan sessionControl, 4)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { s.runLoop(ctx, tx, detect); close(done) }()
		defer func() { cancel(); <-done }()
		synctest.Sleep(detection / 2)
		if s.SetPathDown() != DeliveryQueued {
			t.Fatal("PathDown command rejected")
		}
		synctest.Wait()
		if s.RemoteDiscriminator() != 7 || s.LocalDiag() != DiagPathDown {
			t.Errorf("PathDown: discriminator=%d observation=%+v", s.RemoteDiscriminator(), s.Observation())
		}
		synctest.Sleep(detection / 4)
		if s.SetPathDown() != DeliveryQueued {
			t.Fatal("repeated PathDown command rejected")
		}
		bad := signedAuthExpiryPacket(t, s, 202)
		bad.pkt.Auth.Data[0] ^= 0xff
		s.recvCh <- bad
		synctest.Wait()
		synctest.Sleep(detection/4 - time.Nanosecond)
		if s.RemoteDiscriminator() != 7 {
			t.Error("PathDown discriminator expired before Detection Time")
		}
		synctest.Sleep(time.Nanosecond)
		if s.RemoteDiscriminator() != 0 || s.PacketsReceived() != 1 || s.Observation().Cause != CausePathDown {
			t.Fatalf("owner expiry: discriminator=%d observation=%+v", s.RemoteDiscriminator(), s.Observation())
		}
	})
}

func TestDownInterruptionReanchorsDiscriminatorExpiry(t *testing.T) {
	t.Parallel()
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "PathDown", true: "remote Down"}[remote], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, _, tx, detect := newRemoteTxTestSession(t)
				s.requiredMinRxInterval = time.Second
				s.remoteDesiredMinTxInterval, s.remoteDetectMult = 100*time.Millisecond, 3
				proposal := timerTuple{100 * time.Millisecond, 100 * time.Millisecond, 3, 0, 0}
				if _, err := s.admitTimerUpdate(t.Context(), proposal, 1); err != nil {
					t.Fatal(err)
				}
				s.processTimerUpdates(tx, detect)
				if !s.activeUpdate.Present || s.requiredMinRxInterval != time.Second {
					t.Fatal("RX decrease was not pending")
				}
				s.recordValidReceivedPacket()
				detect.Reset(s.calcDetectionTimeHot())
				tx.Stop()
				pkt := s.buildControlPacket()
				pkt.State, pkt.Poll, pkt.Final, pkt.MyDiscriminator = StateDown, false, false, 99
				pkt.YourDiscriminator = s.localDiscr
				s.recvCh, s.ctrlCh = make(chan recvItem, 1), make(chan sessionControl, 4)
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() { s.runLoop(ctx, tx, detect); close(done) }()
				defer func() { cancel(); <-done }()
				synctest.Sleep(100 * time.Millisecond)
				remaining, discriminator := 200*time.Millisecond, uint32(7)
				if remote {
					s.recvCh <- recvItem{pkt: pkt}
					remaining, discriminator = 300*time.Millisecond, 99
				} else if s.SetPathDown() != DeliveryQueued {
					t.Fatal("PathDown command rejected")
				}
				synctest.Wait()
				if s.State() != StateDown || s.timerUpdateSnapshot().State != timerUpdateFailed {
					t.Fatal("Down did not interrupt pending RX decrease")
				}
				synctest.Sleep(remaining - time.Nanosecond)
				if s.RemoteDiscriminator() != discriminator {
					t.Fatal("discriminator expired before updated receive deadline")
				}
				synctest.Sleep(time.Nanosecond)
				if s.RemoteDiscriminator() != 0 {
					t.Fatalf("discriminator=%d at updated receive deadline", s.RemoteDiscriminator())
				}
			})
		})
	}
}

func TestRemoteDiscriminatorExpiryOwnerTimer(t *testing.T) {
	t.Parallel()
	for _, state := range []State{StateDown, StateInit, StateUp, StateAdminDown} {
		for _, rejection := range []string{"invalid interval", "missing authentication", "invalid digest"} {
			t.Run(state.String()+"/"+rejection, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					s, tx, detect := newAuthExpirySession(t)
					s.cachedState = state
					diag, cause := DiagNone, CauseUnknown
					switch state {
					case StateDown:
						diag, cause = DiagControlTimeExpired, CausePathDown
					case StateAdminDown:
						diag, cause = DiagAdminDown, CauseLocalAdmin
					case StateInit, StateUp:
						// Live states begin without a failure diagnostic.
					}
					s.commitObservation(state, diag, cause)
					s.observation.RemoteKnown = true
					s.observation.RemoteState, s.observation.RemoteDiag, s.observation.RemoteC = StateAdminDown, DiagAdminDown, true
					before := s.Observation()
					s.role, s.pendingFinal = RolePassive, true
					s.remoteMinRxInterval = 0
					sender := &retryCachedPacketSender{failures: 1}
					s.sender = sender
					s.sendControl(t.Context())
					s.recvCh = make(chan recvItem, 1)
					notifications := make(chan StateChange, 4)
					s.notifyCh = notifications
					tx.Stop()
					detection := s.calcDetectionTimeHot()
					detect.Reset(detection)
					ctx, cancel := context.WithCancel(t.Context())
					done := make(chan struct{})
					go func() { s.runLoop(ctx, tx, detect); close(done) }()
					defer func() { cancel(); <-done }()
					synctest.Sleep(detection / 2)
					bad := signedAuthExpiryPacket(t, s, 202)
					switch rejection {
					case "invalid interval":
						bad.pkt.DesiredMinTxInterval = 0
					case "missing authentication":
						bad.pkt.AuthPresent = false
					case "invalid digest":
						bad.pkt.Auth.Data[0] ^= 0xff
					}
					s.recvCh <- bad
					synctest.Wait()
					synctest.Sleep(detection/2 - time.Nanosecond)
					if s.RemoteDiscriminator() != 7 || !s.authState.AuthSeqKnown || s.Observation() != before {
						t.Fatal("peer expired before one Detection Time")
					}
					synctest.Sleep(time.Nanosecond)
					if s.RemoteDiscriminator() != 0 || !s.authState.AuthSeqKnown || s.PacketsReceived() != 1 {
						t.Fatalf("one Detection Time: discriminator=%d auth=%+v received=%d",
							s.RemoteDiscriminator(), s.authState, s.PacketsReceived())
					}
					want, edges := before, uint64(0)
					if state == StateInit || state == StateUp {
						want.State, want.LocalDiag, want.Cause = StateDown, DiagControlTimeExpired, CauseDetectionTimeout
						want.ObservationVersion++
						edges = 1
						select {
						case <-notifications:
						default:
							t.Fatal("timeout transition was not published")
						}
					}
					if got := s.Observation(); got != want || s.StateTransitions() != edges || len(s.notifyCh) != 0 {
						t.Fatalf("expiry changed causal history or edges: got=%+v want=%+v edges=%d", got, want, s.StateTransitions())
					}
					synctest.Sleep(detection - time.Nanosecond)
					if !s.authState.AuthSeqKnown {
						t.Fatal("authentication expired before two Detection Times")
					}
					// Order the inspection before the owner's next authentication write.
					s.recvCh <- bad
					synctest.Wait()
					synctest.Sleep(time.Nanosecond)
					if s.authState.AuthSeqKnown || s.authState.RcvAuthSeq != 201 || s.Observation() != want ||
						len(s.notifyCh) != 0 || !s.pendingFinal || s.PacketsSent() != 0 {
						t.Fatalf("two Detection Times: auth=%+v observation=%+v pending Final=%t sent=%d",
							s.authState, s.Observation(), s.pendingFinal, s.PacketsSent())
					}
					synctest.Sleep(time.Second)
					if sender.attempts != 1 || !s.pendingFinal || s.PacketsSent() != 0 {
						t.Fatal("passive unknown peer retried the failed Final after discriminator expiry")
					}
				})
			})
		}
	}
}

func TestPassiveUnknownPeerBlocksEverySendPath(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"direct", "periodic", "FSM action"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			s, sender, tx, detect := newRemoteTxTestSession(t)
			s.role, s.remoteDiscr, s.pendingFinal = RolePassive, 0, true
			s.remoteMinRxInterval = 0
			switch path {
			case "direct":
				s.sendControl(t.Context())
			case "periodic":
				s.handleTxTimer(t.Context(), tx)
			case "FSM action":
				s.executeAction(t.Context(), ActionSendControl, tx, detect)
			}
			if sender.attempts != 0 || !s.pendingFinal || s.PacketsSent() != 0 || !s.lastPacketSent.IsZero() {
				t.Fatalf("passive unknown peer: attempts=%d pending Final=%t sent=%d",
					sender.attempts, s.pendingFinal, s.PacketsSent())
			}
			pkt := s.buildControlPacket()
			pkt.MyDiscriminator = 99
			s.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
			if sender.attempts != 1 || s.pendingFinal || s.PacketsSent() != 1 {
				t.Fatal("valid peer did not release the retained Final with zero remote receive interval")
			}
			assertCachedPacketFlags(t, sender.packets[0], false, true)
		})
	}
}
