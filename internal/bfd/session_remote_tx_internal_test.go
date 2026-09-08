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
				sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
						sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
				sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
				sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
				sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
				sess.state.Store(uint32(tt.state))
				sess.desiredMinTxInterval = tt.tx
				pkt := sess.buildControlPacket()
				pkt.State, pkt.Final = StateInit, true // Final predates the new local Poll.
				if tt.remoteRX != 0 {
					pkt.RequiredMinRxInterval = microsecondsFromDuration(tt.remoteRX)
				}
				sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
		sess.state.Store(uint32(StateDown))
		sender.failures = 1
		pkt := sess.buildControlPacket()
		pkt.State = StateInit
		sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
		sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
		assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], false, true)
		synctest.Sleep(100 * time.Millisecond)
		if !fireRemoteTxTimer(sess, tx) {
			t.Fatal("crossed Poll stopped local Poll retries")
		}
		assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], true, false)
		pkt.Poll, pkt.Final = false, true
		sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
		pkt.Final = false
		sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
		synctest.Sleep(100 * time.Millisecond)
		if !fireRemoteTxTimer(sess, tx) || sess.pollActive {
			t.Fatal("Final did not end Poll, or unchanged Up restarted it")
		}
		assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], false, false)
		for range 2 {
			sess.handleDetectTimer(t.Context(), tx, detect)
			if sess.State() != StateDown || !sess.pollActive || sess.pollSent ||
				sess.buildControlPacket().DesiredMinTxInterval != 1_000_000 {
				t.Fatal("non-Up transition must retire old Poll and initiate the mandatory floor Poll")
			}
			pkt.State = StateInit
			sender.failures = 1
			sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
			pkt.State, pkt.Final = StateUp, true
			sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
			sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
			synctest.Sleep(time.Until(sess.separateUntil))
			sess.processTimerUpdates(tx, detect)
			if !sess.pollActive || sess.buildControlPacket().DesiredMinTxInterval != 100_000 {
				t.Fatal("floor Poll completion and separation did not start fast recovery")
			}
			sess.sendControl(t.Context())
			sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
				sess.state.Store(uint32(StateDown))
				pkt := sess.buildControlPacket()
				pkt.State = StateInit
				if tt.confirmed {
					sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
					sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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
				sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
				if sess.pollActive != !tt.confirmed {
					t.Fatalf("Poll active after Final = %t, successful Poll sent = %t", sess.pollActive, tt.confirmed)
				}
				if !tt.confirmed {
					synctest.Sleep(100 * time.Millisecond)
					if !fireRemoteTxTimer(sess, tx) {
						t.Fatal("premature Final stopped Poll retry")
					}
					assertCachedPacketFlags(t, sender.packets[len(sender.packets)-1], true, false)
					sess.handleRecvPacket(t.Context(), recvItem{pkt: &pkt}, tx, detect)
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

func newRemoteTxTestSession(t *testing.T) (*Session, *retryCachedPacketSender, *time.Timer, *time.Timer) {
	t.Helper()
	sess := newCachedPacketTestSession(MaxPacketSize)
	sender := &retryCachedPacketSender{}
	sess.sender = sender
	sess.cachedState = StateUp
	sess.state.Store(uint32(StateUp))
	sess.remoteState.Store(uint32(StateUp))
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
