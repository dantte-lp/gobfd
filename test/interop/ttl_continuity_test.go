//go:build interop_testcontainers

package interop_test

import (
	"testing"
)

func TestTimerReloadTTLContinuityAcrossReload(t *testing.T) {
	t.Parallel()
	initial := sessionState{LocalState: "Up", RemoteState: "Up", LocalDiscriminator: 11, RemoteDiscriminator: 22}
	before := [2]uint64{2, 0}
	if err := checkTTLContinuity(initial, initial, before, [2]uint64{3, 1}, 1); err == nil {
		t.Fatal("state transition between phases escaped initial counter baseline")
	}
	if err := checkTTLContinuity(initial, initial, before, [2]uint64{2, 2}, 1); err == nil {
		t.Fatal("reload auth failure escaped intentional-replay accounting")
	}
	if err := checkTTLContinuity(initial, initial, before, [2]uint64{2, 1}, 1); err != nil {
		t.Fatal(err)
	}
}

func TestTimerReloadTTLPreviouslyAcceptedReplay(t *testing.T) {
	t.Parallel()
	frames, phase, keys := ttlContractPhase(t, false)
	result, next, err := evaluateTTLPhase(frames, phase, keys)
	if err != nil || result.Replay != 3 || result.Low != 4 || result.Good != 5 || result.Final != 6 ||
		next.Auth[1].RcvAuthSeq != 202 {
		t.Fatalf("accepted-sequence rejection evidence: result=%+v next=%+v err=%v", result, next.Auth, err)
	}
}
