package bfd

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestSessionDetectMultiplierReloadPreservesPeerDeadline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                      string
		old, next                 uint8
		initial, nextMin, nextMax time.Duration
	}{
		{
			name: "one to five", old: 1, next: 5,
			initial: 270 * time.Millisecond, nextMin: 300 * time.Millisecond, nextMax: 300 * time.Millisecond,
		},
		{
			name: "five to one", old: 5, next: 1,
			initial: 300 * time.Millisecond, nextMin: 225 * time.Millisecond, nextMax: 270 * time.Millisecond,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, sender, tx, detect := newRemoteTxTestSession(t)
				s.detectMult = tc.old
				s.desiredMinTxInterval, s.requiredMinRxInterval = 300*time.Millisecond, 300*time.Millisecond
				s.remoteMinRxInterval, s.remoteDesiredMinTxInterval = 300*time.Millisecond, 300*time.Millisecond
				s.remoteDetectMult = 3
				s.sendControl(t.Context())
				tx.Reset(tc.initial)
				synctest.Sleep(150 * time.Millisecond)
				// Seed 8 selects zero-percent reduction under the normal jitter policy.
				// Replacing an existing DetectMult=1 deadline would consume all peer slack.
				s.jitterRng.state = 8
				proposal := timerTuple{300 * time.Millisecond, 300 * time.Millisecond, tc.next, 0, 0}
				if _, err := s.admitTimerUpdate(t.Context(), proposal, 1); err != nil {
					t.Fatal(err)
				}
				s.processTimerUpdates(tx, detect)
				synctest.Sleep(75*time.Millisecond - time.Nanosecond)
				select {
				case <-tx.C:
					t.Fatal("DetectMult reload sent before the minimum jitter interval")
				default:
				}
				synctest.Sleep(45*time.Millisecond + time.Nanosecond)
				// The first advertisement retains the previous multiplier's safety margin.
				s.jitterRng.state = 8
				if !fireRemoteTxTimer(s, tx) {
					t.Fatal("DetectMult reload postponed the first advertisement beyond 90% of the interval")
				}
				if len(sender.packets) != 2 {
					t.Fatalf("Control sends = %d, want baseline and first updated advertisement", len(sender.packets))
				}
				var packet ControlPacket
				if err := UnmarshalControlPacket(sender.packets[1], &packet); err != nil {
					t.Fatal(err)
				}
				if packet.DetectMult != tc.next || packet.Poll || packet.Final || packet.State != StateUp {
					t.Fatalf("first updated Control = %+v; want new DetectMult without Poll or state change", packet)
				}
				// After a successful advertisement, the next period uses the new policy.
				synctest.Sleep(tc.nextMin - time.Nanosecond)
				select {
				case <-tx.C:
					t.Fatal("subsequent Control used an interval below the new jitter policy")
				default:
				}
				synctest.Sleep(tc.nextMax - tc.nextMin + time.Nanosecond)
				if !fireRemoteTxTimer(s, tx) {
					t.Fatal("subsequent Control exceeded the new jitter policy")
				}
			})
		})
	}
}
