package bfd

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestSessionRejectsReservedRemoteTx(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sess, _, tx, detect := newRemoteTxTestSession(t)
		sess.requiredMinRxInterval = 0
		sess.recvCh = make(chan recvItem, 1)
		pkt := sess.buildControlPacket()
		pkt.DesiredMinTxInterval = 0
		sess.RecvPacket(&pkt)
		sess.handleRecvPacket(t.Context(), <-sess.recvCh, tx, detect)
		if got := sess.calcDetectionTimeHot(); got <= 0 {
			t.Errorf("reserved peer TX produced detection time %s", got)
		}
		if sess.PacketsReceived() != 0 || !sess.LastPacketReceived().IsZero() {
			t.Error("reserved peer TX was recorded as valid reception")
		}
		select {
		case <-detect.C:
			t.Error("reserved peer TX armed an immediately expired detection timer")
		default:
		}
	})
}

func TestRemoteTxPolicy(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		rx         time.Duration
		state      State
		pollActive bool
		wantTX     bool
	}{
		{name: "Demand stops and resumes", rx: 100 * time.Millisecond, state: StateUp},
		{name: "zero RX stops and resumes", state: StateUp},
		{name: "local Poll overrides Demand", rx: 100 * time.Millisecond, state: StateUp, pollActive: true, wantTX: true},
		{name: "zero RX still suppresses local Poll", state: StateUp, pollActive: true},
		{name: "remote leaves Up", rx: 100 * time.Millisecond, state: StateInit, wantTX: true},
		{name: "local leaves Up", rx: 100 * time.Millisecond, state: StateDown, wantTX: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, sender, tx, detect := newRemoteTxTestSession(t)
				sess.pollActive = tt.pollActive
				pkt := sess.buildControlPacket()
				pkt.State, pkt.Demand, pkt.Poll = tt.state, tt.rx != 0 || tt.pollActive, false
				pkt.RequiredMinRxInterval = microsecondsFromDuration(tt.rx)
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				synctest.Sleep(time.Second)
				if fired := fireRemoteTxTimer(sess, tx); fired != tt.wantTX {
					t.Fatalf("periodic timer fired = %t, want %t", fired, tt.wantTX)
				}
				if got := len(sender.packets); (got > 0) != tt.wantTX {
					t.Fatalf("periodic sends = %d, want TX %t", got, tt.wantTX)
				}
				if tt.wantTX {
					if tt.pollActive {
						pkt.Final = true
						sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
						synctest.Sleep(time.Second)
						if fireRemoteTxTimer(sess, tx) {
							t.Fatal("Demand transmission continued after local Poll completed")
						}
					}
					return
				}
				// An incoming Final also ends the existing local Poll exception.
				pkt.Demand, pkt.Final = false, true
				pkt.RequiredMinRxInterval = 100_000
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				synctest.Sleep(100 * time.Millisecond)
				if !fireRemoteTxTimer(sess, tx) || len(sender.packets) != 1 {
					t.Fatal("periodic transmission did not resume")
				}
			})
		})
	}
}

func TestRemoteTxDeadline(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                  string
		oldRX, newRX, elapsed time.Duration
		earliest, latest      time.Duration
		failedSend            bool
	}{
		{
			name: "shorter interval uses previous send", oldRX: time.Second,
			newRX: 100 * time.Millisecond, elapsed: 50 * time.Millisecond,
			earliest: 75 * time.Millisecond, latest: 100 * time.Millisecond,
		},
		{
			name: "failed send does not advance deadline", oldRX: time.Second,
			newRX: 100 * time.Millisecond, elapsed: 50 * time.Millisecond,
			earliest: 75 * time.Millisecond, latest: 100 * time.Millisecond, failedSend: true,
		},
		{
			name: "shorter interval already elapsed", oldRX: time.Second,
			newRX: 100 * time.Millisecond, elapsed: 500 * time.Millisecond,
			earliest: 500 * time.Millisecond, latest: 500 * time.Millisecond,
		},
		{
			name: "longer interval replaces old deadline", oldRX: 100 * time.Millisecond,
			newRX: time.Second, elapsed: 50 * time.Millisecond,
			earliest: 750 * time.Millisecond, latest: time.Second,
		},
		{
			name: "unchanged receive preserves deadline", oldRX: 100 * time.Millisecond,
			newRX: 100 * time.Millisecond, elapsed: 50 * time.Millisecond,
			earliest: 100 * time.Millisecond, latest: 100 * time.Millisecond,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, sender, tx, detect := newRemoteTxTestSession(t)
				sess.remoteMinRxInterval = tt.oldRX
				sess.sendControl(t.Context())
				tx.Reset(tt.oldRX)
				synctest.Sleep(tt.elapsed)
				if tt.failedSend {
					sender.failures = 1
					sess.sendControl(t.Context())
				}
				pkt := sess.buildControlPacket()
				pkt.RequiredMinRxInterval = microsecondsFromDuration(tt.newRX)
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if tt.earliest > tt.elapsed {
					synctest.Sleep(tt.earliest - tt.elapsed - time.Nanosecond)
					if fireRemoteTxTimer(sess, tx) {
						t.Fatal("transmitted before the new jitter window")
					}
					synctest.Sleep(tt.latest - tt.earliest + time.Nanosecond)
				}
				if !fireRemoteTxTimer(sess, tx) || len(sender.packets) != 2 {
					t.Fatal("next transmission missed the deadline from the previous send")
				}
			})
		})
	}
}

func TestRemoteTxPollFinalRetry(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		rx       uint32
		failures int
	}{
		{name: "zero RX immediate Final"},
		{name: "zero RX failed Final retry", failures: 1},
		{name: "Demand immediate Final", rx: 100_000},
		{name: "Demand failed Final retry", rx: 100_000, failures: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, sender, tx, detect := newRemoteTxTestSession(t)
				sess.role = RolePassive
				sess.pollActive = tt.rx == 0
				sender.failures = tt.failures
				pkt := sess.buildControlPacket()
				pkt.Demand, pkt.Poll, pkt.RequiredMinRxInterval = true, true, tt.rx
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if sender.attempts != 1 || sess.pendingFinal != (tt.failures > 0) {
					t.Fatal("Poll did not attempt an immediate Final with failure retention")
				}
				if tt.failures > 0 {
					if fireRemoteTxTimer(sess, tx) {
						t.Fatal("failed Final retry would hot-loop")
					}
					synctest.Sleep(100 * time.Millisecond)
					if !fireRemoteTxTimer(sess, tx) || sess.pendingFinal {
						t.Fatal("pending Final was not retried successfully")
					}
				}
				assertCachedPacketFlags(t, sender.packets[0], false, true)
				synctest.Sleep(time.Second)
				if fireRemoteTxTimer(sess, tx) || len(sender.packets) != 1 {
					t.Fatal("suppressed periodic timer remained armed after Final success")
				}
			})
		})
	}
}

func TestSessionSlowToFastPoll(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		state    State
		tx       time.Duration
		remoteRX time.Duration
	}{
		{name: "Down to fast Up", state: StateDown, tx: 100 * time.Millisecond},
		{name: "Init to fast Up", state: StateInit, tx: 100 * time.Millisecond},
		{name: "peer keeps slow rate", state: StateDown, tx: 100 * time.Millisecond, remoteRX: 2 * time.Second},
		{name: "unchanged floor", state: StateDown, tx: time.Second},
		{name: "above floor", state: StateDown, tx: 2 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, sender, tx, detect := newRemoteTxTestSession(t)
				sess.cachedState = tt.state
				sess.mu.Lock()
				sess.localPair.Store(packStateDiag(tt.state, DiagNone))
				sess.observation.State = tt.state
				sess.mu.Unlock()
				sess.desiredMinTxInterval = tt.tx
				pkt := sess.buildControlPacket()
				pkt.State, pkt.Final = StateInit, true // Final predates the new local Poll.
				if tt.remoteRX != 0 {
					pkt.RequiredMinRxInterval = microsecondsFromDuration(tt.remoteRX)
				}
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if sess.State() != StateUp || len(sender.packets) != 1 {
					t.Fatal("Up transition must use exactly its existing immediate send")
				}
				assertCachedPacketFlags(t, sender.packets[0], tt.tx < slowTxInterval, false)
				var sent ControlPacket
				if err := UnmarshalControlPacket(sender.packets[0], &sent); err != nil {
					t.Fatal(err)
				}
				if sent.DesiredMinTxInterval != microsecondsFromDuration(tt.tx) || sent.MyDiscriminator != sess.localDiscr {
					t.Fatalf("Up packet did not retain configured TX and discriminator: %+v", sent)
				}
				if sess.calcTxIntervalHot() != max(tt.tx, tt.remoteRX) {
					t.Fatal("local TX decrease was deferred until Final")
				}
			})
		})
	}
}

func TestSessionSlowToFastPollLifecycle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sess, sender, tx, detect := newRemoteTxTestSession(t)
		sess.cachedState = StateDown
		sess.mu.Lock()
		sess.localPair.Store(packStateDiag(StateDown, DiagNone))
		sess.observation.State = StateDown
		sess.mu.Unlock()
		sender.failures = 1
		pkt := sess.buildControlPacket()
		pkt.State = StateInit
		sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		if !sess.pollActive || sender.attempts != 1 || len(sender.packets) != 0 {
			t.Fatal("failed first Up send did not retain local Poll for periodic retry")
		}
		// Lost Poll/Final packets leave P set on subsequent timer-driven sends.
		for range 2 {
			synctest.Sleep(100 * time.Millisecond)
			if !fireRemoteTxTimer(sess, tx) {
				t.Fatal("local Poll retry timer was not armed")
			}
			assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], true, false)
		}
		pkt.State, pkt.Poll = StateUp, true
		sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], false, true)
		synctest.Sleep(100 * time.Millisecond)
		if !fireRemoteTxTimer(sess, tx) {
			t.Fatal("crossed Poll stopped local Poll retries")
		}
		assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], true, false)
		pkt.Poll, pkt.Final = false, true
		sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		pkt.Final = false
		sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		synctest.Sleep(100 * time.Millisecond)
		if !fireRemoteTxTimer(sess, tx) || sess.pollActive {
			t.Fatal("Final did not end Poll, or unchanged Up restarted it")
		}
		assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], false, false)
		for range 2 {
			// RFC 5880 Section 6.8.7 sends the state change at once; a failed
			// attempt must not confirm the new floor Poll.
			attempts := sender.attempts
			sender.failures = 1
			sess.handleDetectTimer(t.Context(), tx, detect)
			if sess.State() != StateDown || !sess.pollActive || sess.pollSent ||
				sess.buildControlPacket().DesiredMinTxInterval != 1_000_000 {
				t.Fatal("non-Up transition must retire old Poll and initiate the mandatory floor Poll")
			}
			if sender.attempts != attempts+1 {
				t.Fatalf("state-change attempts=%d, want %d", sender.attempts, attempts+1)
			}
			pkt.State = StateInit
			sender.failures = 1
			sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
			pkt.State, pkt.Final = StateUp, true
			sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
			if !sess.pollActive {
				t.Fatal("Final completed new Poll using an obsolete send confirmation")
			}
			pkt.Final = false
			synctest.Sleep(time.Second)
			if !fireRemoteTxTimer(sess, tx) {
				t.Fatal("new Poll was not retried after premature Final")
			}
			assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], true, false)
			pkt.Final = true
			sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
			synctest.Sleep(time.Until(sess.separateUntil))
			sess.processTimerUpdates(tx, detect)
			if !sess.pollActive || sess.buildControlPacket().DesiredMinTxInterval != 100_000 {
				t.Fatal("floor Poll completion and separation did not start fast recovery")
			}
			sess.sendControl(t.Context())
			sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
			synctest.Sleep(time.Until(sess.separateUntil))
			sess.processTimerUpdates(tx, detect)
			pkt.Final = false
		}
	})
}

func TestSessionPollRequiresSuccessfulSend(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		failure   string
		confirmed bool
		crossed   bool
	}{
		{name: "first socket failure", failure: "socket"},
		{name: "first marshal failure", failure: "marshal"},
		{name: "first signing failure", failure: "sign"},
		{name: "first authenticated marshal failure", failure: "authenticated marshal"},
		{name: "crossed Final only", crossed: true},
		{name: "successful Poll", confirmed: true},
		{name: "confirmation survives socket failure", confirmed: true, failure: "socket"},
		{name: "confirmation survives crossed Final", confirmed: true, crossed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, sender, tx, detect := newRemoteTxTestSession(t)
				sess.cachedState = StateDown
				sess.mu.Lock()
				sess.localPair.Store(packStateDiag(StateDown, DiagNone))
				sess.observation.State = StateDown
				sess.mu.Unlock()
				pkt := sess.buildControlPacket()
				pkt.State = StateInit
				if tt.confirmed {
					sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
					assertCachedPacketFlags(t, sender.packets[0], true, false)
				}
				switch tt.failure {
				case "socket":
					sender.failures = 1
				case "marshal":
					sess.cachedPacket = make([]byte, HeaderSize-1)
				case "sign":
					sess.auth = failingCachedPacketAuth{}
				case "authenticated marshal":
					sess.auth = oversizedCachedPacketAuth{}
				}
				pkt.Poll, pkt.AuthPresent = tt.crossed, sess.auth != nil
				if tt.confirmed && tt.failure != "" {
					sess.sendControl(t.Context())
				} else {
					sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				}
				wantPackets := 0
				if tt.confirmed {
					wantPackets++
				}
				if tt.crossed {
					wantPackets++
				}
				if len(sender.packets) != wantPackets || !sess.pollActive {
					t.Fatal("unexpected send result before incoming Final")
				}
				if tt.crossed {
					assertCachedPacketFlags(t, sender.packets[wantPackets-1], false, true)
				}
				sess.auth = nil
				sess.cachedPacket = make([]byte, MaxPacketSize)
				pkt.State, pkt.Poll, pkt.Final, pkt.AuthPresent = StateUp, false, true, false
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if sess.pollActive != !tt.confirmed {
					t.Fatalf("Poll active after Final = %t, successful Poll sent = %t", sess.pollActive, tt.confirmed)
				}
				if !tt.confirmed {
					synctest.Sleep(100 * time.Millisecond)
					if !fireRemoteTxTimer(sess, tx) {
						t.Fatal("premature Final stopped Poll retry")
					}
					assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], true, false)
					sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				}
				synctest.Sleep(100 * time.Millisecond)
				if !fireRemoteTxTimer(sess, tx) || sess.pollActive {
					t.Fatal("successful Poll followed by Final did not restore ordinary TX")
				}
				assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], false, false)
			})
		})
	}
}

// RFC 5880 Section 6.8.18: "If the remote system does not receive any BFD
// Control packets for a Detection Time, it SHOULD reset
// bfd.RemoteMinRxInterval to its initial value of 1 ... and then can transmit
// at its own rate.". Section 6.8.7: a packet whose contents differ "SHOULD be
// transmitted during the interval between periodic Control packet
// transmissions", so an expiry that changes the state sends at once.
func TestRemoteMinRxResetsAfterDetectionTime(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		local, remote State
		wantState     State
		wantDiag      Diag
		remoteRX      time.Duration
		passive       bool
		immediate     bool
	}{
		{
			name: "Up expires to Down", local: StateUp, remote: StateUp, wantState: StateDown,
			wantDiag: DiagControlTimeExpired, immediate: true,
		},
		// Section 6.8.7: a Passive system with bfd.RemoteDiscr zero stays silent.
		{
			name: "Passive Up expires silently", local: StateUp, remote: StateUp, wantState: StateDown,
			wantDiag: DiagControlTimeExpired, passive: true,
		},
		{
			name: "Down held down by peer", local: StateDown, remote: StateAdminDown, wantState: StateDown,
			wantDiag: DiagControlTimeExpired,
		},
		// Section 6.8.18 also names "an arbitrarily large value" as a hold-down.
		{
			name: "Down held down by large RX", local: StateDown, remote: StateAdminDown, wantState: StateDown,
			wantDiag: DiagControlTimeExpired, remoteRX: 10 * time.Second,
		},
		// Section 6.8.16 AdminDown notification resumes once the obligation ends.
		{
			name: "AdminDown notification resumes", local: StateAdminDown, remote: StateUp, wantState: StateAdminDown,
			wantDiag: DiagAdminDown,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, sender, tx, detect := newRemoteTxTestSession(t)
				if tt.passive {
					sess.role = RolePassive
				}
				if tt.local != StateUp {
					diag := DiagNone
					if tt.local == StateAdminDown {
						diag = DiagAdminDown
					}
					sess.cachedState = tt.local
					sess.mu.Lock()
					sess.localPair.Store(packStateDiag(tt.local, diag))
					sess.observation.State = tt.local
					sess.mu.Unlock()
				}
				pkt := sess.buildControlPacket()
				pkt.State, pkt.Demand = tt.remote, true
				pkt.RequiredMinRxInterval = microsecondsFromDuration(tt.remoteRX)
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				detection := sess.calcDetectionTimeHot()
				synctest.Sleep(detection - time.Nanosecond)
				if fireRemoteTxTimer(sess, tx) || len(sender.packets) != 0 {
					t.Fatal("periodic transmission ignored remote Required Min RX")
				}
				synctest.Sleep(time.Nanosecond)
				select {
				case <-detect.C:
					sess.handleDetectTimer(t.Context(), tx, detect)
				default:
					t.Fatal("detection timer did not expire after one Detection Time")
				}
				if sess.State() != tt.wantState || sess.LocalDiag() != tt.wantDiag {
					t.Fatalf("after expiry state=%s diag=%s, want %s/%s",
						sess.State(), sess.LocalDiag(), tt.wantState, tt.wantDiag)
				}
				// The non-Up slow floor bounds the interval; jitter only shortens it.
				if got := sess.RemoteMinRxInterval(); got != initialRemoteMinRx || sess.remoteDemandMode {
					t.Fatalf("after expiry RemoteMinRx=%s RemoteDemand=%t, want %s/false",
						got, sess.remoteDemandMode, initialRemoteMinRx)
				}
				want := 0
				if tt.immediate {
					want = 1
				}
				if len(sender.packets) != want {
					t.Fatalf("packets at expiry=%d, want %d", len(sender.packets), want)
				}
				// The periodic schedule keeps the slow floor reduced by at most 25%.
				synctest.Sleep(slowTxInterval*3/4 - time.Nanosecond)
				if fireRemoteTxTimer(sess, tx) {
					t.Fatal("periodic transmission before the jittered slow-rate floor")
				}
				synctest.Sleep(slowTxInterval/4 + time.Nanosecond)
				if tt.passive {
					if fireRemoteTxTimer(sess, tx) || len(sender.packets) != 0 {
						t.Fatal("Passive session transmitted with bfd.RemoteDiscr zero")
					}
					return
				}
				if !fireRemoteTxTimer(sess, tx) || len(sender.packets) != want+1 {
					t.Fatal("periodic transmission did not resume after Detection Time")
				}
				for _, payload := range sender.packets {
					var sent ControlPacket
					if err := UnmarshalControlPacket(payload, &sent); err != nil {
						t.Fatal(err)
					}
					if sent.State != tt.wantState || sent.Diag != tt.wantDiag {
						t.Fatalf("resumed packet state=%s diag=%s, want %s/%s",
							sent.State, sent.Diag, tt.wantState, tt.wantDiag)
					}
				}
			})
		})
	}
}

// RFC 5880 Section 6.8.7: a state change sends at once unless a transmit
// prohibition applies; zero remote Required Min RX still forbids it.
func TestReceivedDownTransmitsStateChangeImmediately(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		remote   State
		remoteRX time.Duration
		wantSent bool
	}{
		{name: "remote Down", remote: StateDown, remoteRX: 100 * time.Millisecond, wantSent: true},
		{name: "remote AdminDown", remote: StateAdminDown, remoteRX: 100 * time.Millisecond, wantSent: true},
		{name: "remote Down with zero RX", remote: StateDown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, sender, tx, detect := newRemoteTxTestSession(t)
				pkt := sess.buildControlPacket()
				pkt.State, pkt.Poll, pkt.Final = tt.remote, false, false
				pkt.RequiredMinRxInterval = microsecondsFromDuration(tt.remoteRX)
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if sess.State() != StateDown || sess.LocalDiag() != DiagNeighborDown {
					t.Fatalf("state=%s diag=%s, want Down/NeighborDown", sess.State(), sess.LocalDiag())
				}
				if sent := len(sender.packets) == 1; sent != tt.wantSent || len(sender.packets) > 1 {
					t.Fatalf("packets after state change=%d, want sent=%t", len(sender.packets), tt.wantSent)
				}
				if tt.wantSent {
					var sent ControlPacket
					if err := UnmarshalControlPacket(sender.packets[0], &sent); err != nil {
						t.Fatal(err)
					}
					if sent.State != StateDown || sent.Diag != DiagNeighborDown || sent.Final {
						t.Fatalf("state-change packet %+v, want Down/NeighborDown without Final", sent)
					}
				}
				synctest.Sleep(slowTxInterval*3/4 - time.Nanosecond)
				if fireRemoteTxTimer(sess, tx) {
					t.Fatal("periodic transmission before the jittered slow-rate floor")
				}
			})
		})
	}
}

// RFC 5880 Section 6.8.3: "timing parameter changes MUST be effected
// immediately (changing the transmission rate and/or the Detection Time).".
func TestRemoteTimerChangeUpdatesDetectionImmediately(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name               string
		oldTX, newTX       time.Duration
		oldMult, newMult   uint8
		wantOld, wantNewDT time.Duration
	}{
		{
			name: "remote TX increase", oldTX: 100 * time.Millisecond, newTX: time.Second,
			oldMult: 3, newMult: 3, wantOld: 300 * time.Millisecond, wantNewDT: 3 * time.Second,
		},
		{
			name: "remote TX decrease", oldTX: time.Second, newTX: 100 * time.Millisecond,
			oldMult: 3, newMult: 3, wantOld: 3 * time.Second, wantNewDT: 300 * time.Millisecond,
		},
		{
			name: "remote Detect Mult increase", oldTX: 100 * time.Millisecond, newTX: 100 * time.Millisecond,
			oldMult: 3, newMult: 5, wantOld: 300 * time.Millisecond, wantNewDT: 500 * time.Millisecond,
		},
		{
			name: "local RX dominates remote TX", oldTX: 100 * time.Millisecond, newTX: 10 * time.Millisecond,
			oldMult: 3, newMult: 3, wantOld: 300 * time.Millisecond, wantNewDT: 150 * time.Millisecond,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, _, tx, detect := newRemoteTxTestSession(t)
				sess.requiredMinRxInterval = 50 * time.Millisecond
				pkt := sess.buildControlPacket()
				pkt.DesiredMinTxInterval, pkt.DetectMult = microsecondsFromDuration(tt.oldTX), tt.oldMult
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if got := sess.calcDetectionTimeHot(); got != tt.wantOld {
					t.Fatalf("initial Detection Time = %s, want %s", got, tt.wantOld)
				}
				synctest.Sleep(10 * time.Millisecond)
				pkt.DesiredMinTxInterval, pkt.DetectMult = microsecondsFromDuration(tt.newTX), tt.newMult
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				synctest.Sleep(tt.wantNewDT - time.Nanosecond)
				select {
				case <-detect.C:
					t.Fatal("detection fired before the new Detection Time")
				default:
				}
				synctest.Sleep(time.Nanosecond)
				select {
				case <-detect.C:
				default:
					t.Fatal("detection did not fire at the new Detection Time")
				}
			})
		})
	}
}

// RFC 5880 Section 6.6: "If Demand mode is active on either or both systems,
// a Poll Sequence MUST be initiated whenever the contents of the next BFD
// Control packet to be sent would be different".
func TestTimerPollUnderRemoteDemand(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sess, sender, tx, detect := newRemoteTxTestSession(t)
		pkt := sess.buildControlPacket()
		pkt.State, pkt.Demand = StateUp, true
		sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		synctest.Sleep(time.Second)
		if fireRemoteTxTimer(sess, tx) || len(sender.packets) != 0 {
			t.Fatal("periodic transmission continued under remote Demand")
		}
		tuple := timerTuple{200 * time.Millisecond, 200 * time.Millisecond, 3, 0, 0}
		if _, err := sess.admitTimerUpdate(t.Context(), tuple, 1); err != nil {
			t.Fatal(err)
		}
		sess.processTimerUpdates(tx, detect)
		if !sess.pollActive {
			t.Fatal("timer update did not start a Poll Sequence")
		}
		synctest.Sleep(100 * time.Millisecond)
		if !fireRemoteTxTimer(sess, tx) || len(sender.packets) != 1 {
			t.Fatal("Poll was not transmitted while remote Demand stopped periodic TX")
		}
		assertCachedPacketFlags(t, sender.packets[0], true, false)
		pkt.Final = true
		sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		if status := sess.timerUpdateSnapshot(); status.State != timerUpdateApplied || !status.Changed {
			t.Fatalf("timer update receipt after Final: %+v", status)
		}
		synctest.Sleep(time.Second)
		if fireRemoteTxTimer(sess, tx) || len(sender.packets) != 1 {
			t.Fatal("periodic transmission continued under remote Demand after Final")
		}
	})
}

// RFC 5880 Section 6.8.7: "A system MUST NOT periodically transmit BFD
// Control packets if bfd.RemoteMinRxInterval is zero." A Poll that cannot be
// sent fails at its deadline instead of looping.
func TestTimerPollUnderRemoteZeroRxExpires(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sess, sender, tx, detect := newRemoteTxTestSession(t)
		pkt := sess.buildControlPacket()
		pkt.RequiredMinRxInterval = 0
		sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		tuple := timerTuple{200 * time.Millisecond, 200 * time.Millisecond, 3, 0, 0}
		status, err := sess.admitTimerUpdate(t.Context(), tuple, 1)
		if err != nil {
			t.Fatal(err)
		}
		sess.processTimerUpdates(tx, detect)
		// Keep the session alive with unchanged remote packets until the deadline.
		for time.Until(status.Requested.Deadline) > 0 {
			synctest.Sleep(min(100*time.Millisecond, time.Until(status.Requested.Deadline)))
			if fireRemoteTxTimer(sess, tx) {
				t.Fatal("Poll was transmitted under remote Required Min RX zero")
			}
			sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		}
		sess.processTimerUpdates(tx, detect)
		if got := sess.timerUpdateSnapshot().State; got != timerUpdateFailed {
			t.Fatalf("timer update state at deadline = %v, want failed", got)
		}
		if sender.attempts != 0 {
			t.Fatalf("send attempts = %d, want 0", sender.attempts)
		}
	})
}

// RFC 5880 Section 6.8.7: the Final reply is sent "as soon as practicable";
// the larger remote Required Min RX then governs the next periodic packet.
func TestRemoteRxIncreaseWhileFinalPending(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sess, sender, tx, detect := newRemoteTxTestSession(t)
		pkt := sess.buildControlPacket()
		pkt.Poll, pkt.RequiredMinRxInterval = true, 1_000_000
		sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
		if len(sender.packets) != 1 {
			t.Fatal("Final was not sent immediately")
		}
		assertCachedPacketFlags(t, sender.packets[0], false, true)
		synctest.Sleep(750*time.Millisecond - time.Nanosecond)
		if fireRemoteTxTimer(sess, tx) {
			t.Fatal("periodic packet preceded the new jitter window")
		}
		synctest.Sleep(250*time.Millisecond + time.Nanosecond)
		if !fireRemoteTxTimer(sess, tx) || len(sender.packets) != 2 {
			t.Fatal("periodic packet missed the new interval")
		}
	})
}

func newRemoteTxTestSession(t *testing.T) (*Session, *retryCachedPacketSender, *time.Timer, *time.Timer) {
	t.Helper()
	sess := newCachedPacketTestSession(MaxPacketSize)
	sender := &retryCachedPacketSender{}
	sess.sender = sender
	sess.cachedState = StateUp
	sess.mu.Lock()
	sess.localPair.Store(packStateDiag(StateUp, DiagNone))
	sess.observation.State = StateUp
	sess.mu.Unlock()
	sess.observation.RemoteState = StateUp
	sess.remoteDiscr = 7
	sess.remoteMinRxInterval = 100 * time.Millisecond
	tx, detect := time.NewTimer(100*time.Millisecond), time.NewTimer(time.Hour)
	t.Cleanup(func() { tx.Stop(); detect.Stop() })
	return sess, sender, tx, detect
}

func fireRemoteTxTimer(sess *Session, tx *time.Timer) bool {
	select {
	case <-tx.C:
		sess.handleTxTimer(context.Background(), tx)
		return true
	default:
		return false
	}
}

// RFC 5880 Section 6.8.3: the local system "cannot wait longer than the new
// interval between the previous packet transmission and the next one". A
// remote Demand clear resumes periodic TX from the previous successful send.
func TestRemoteDemandClearResumesFromPreviousSend(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                      string
		elapsed, earliest, latest time.Duration
	}{
		{
			name: "within interval", elapsed: 50 * time.Millisecond,
			earliest: 75 * time.Millisecond, latest: 100 * time.Millisecond,
		},
		{
			name: "interval already elapsed", elapsed: 500 * time.Millisecond,
			earliest: 500 * time.Millisecond, latest: 500 * time.Millisecond,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sess, sender, tx, detect := newRemoteTxTestSession(t)
				sess.sendControl(t.Context())
				pkt := sess.buildControlPacket()
				pkt.Demand = true
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				synctest.Sleep(tt.elapsed)
				if fireRemoteTxTimer(sess, tx) {
					t.Fatal("periodic transmission continued under remote Demand")
				}
				pkt.Demand = false
				sess.handleRecvPacket(t.Context(), recvItem{pkt: pkt}, tx, detect)
				if tt.earliest > tt.elapsed {
					synctest.Sleep(tt.earliest - tt.elapsed - time.Nanosecond)
					if fireRemoteTxTimer(sess, tx) {
						t.Fatal("transmitted before the jitter window from the previous send")
					}
					synctest.Sleep(tt.latest - tt.earliest + time.Nanosecond)
				}
				if !fireRemoteTxTimer(sess, tx) || len(sender.packets) != 2 {
					t.Fatal("Demand clear did not resume TX from the previous send")
				}
			})
		})
	}
}
