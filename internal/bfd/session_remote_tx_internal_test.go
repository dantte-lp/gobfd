package bfd

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

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
