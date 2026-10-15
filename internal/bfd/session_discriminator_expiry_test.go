package bfd

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

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
						bad.pkt.Auth.Digest[0] ^= 0xff
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
			s.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
			if sender.attempts != 1 || s.pendingFinal || s.PacketsSent() != 1 {
				t.Fatal("valid peer did not release the retained Final with zero remote receive interval")
			}
			assertCachedPacketFlags(t, sender.packets[0], false, true)
		})
	}
}
