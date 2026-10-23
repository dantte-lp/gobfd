//go:build linux

package bfdload

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"
)

func TestLossScheduleDoesNotCouplePeers(t *testing.T) {
	t.Parallel()

	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.Sessions, profile.Seed, profile.LossPermille = 4, 17, 1
	const packets = 10000
	expected := make([][]bool, profile.Sessions)
	replayed := make([]*lossSchedule, profile.Sessions)
	lost := 0
	for peer := range profile.Sessions {
		original, err := newLossSchedule(profile, uint64(peer+1))
		if err != nil {
			t.Fatal(err)
		}
		replayed[peer], err = newLossSchedule(profile, uint64(peer+1))
		if err != nil {
			t.Fatal(err)
		}
		expected[peer] = make([]bool, packets)
		for sequence := range packets {
			decision, err := original.next(time.Duration(sequence) * time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			expected[peer][sequence] = decision.disposition == packetRandomLoss
			if expected[peer][sequence] {
				lost++
			}
		}
	}
	for sequence := range packets {
		for peer := profile.Sessions - 1; peer >= 0; peer-- {
			decision, err := replayed[peer].next(time.Duration(sequence) * time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if decision.sequence != uint64(sequence+1) ||
				(decision.disposition == packetRandomLoss) != expected[peer][sequence] {
				t.Fatalf("loss changed with peer traversal order: peer=%d sequence=%d", peer+1, sequence+1)
			}
		}
	}
	if lost == 0 || slices.Equal(expected[0], expected[1]) {
		t.Fatal("peer identity did not create distinct nonempty loss schedules")
	}
}

func TestLossScheduleBlackholeAccountingAndProgression(t *testing.T) {
	t.Parallel()

	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile.LossPermille = 1000
	profile.BlackholeEvery, profile.BlackholeFor = 10*time.Millisecond, time.Millisecond
	schedule, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		at   time.Duration
		want packetDisposition
	}{
		{0, packetRandomLoss},
		{10*time.Millisecond - time.Nanosecond, packetRandomLoss},
		{10 * time.Millisecond, packetBlackhole},
		{11*time.Millisecond - time.Nanosecond, packetBlackhole},
		{11 * time.Millisecond, packetRandomLoss},
		{20 * time.Millisecond, packetBlackhole},
	} {
		decision, nextErr := schedule.next(tc.at)
		if nextErr != nil || decision.disposition != tc.want || decision.sequence != uint64(i+1) {
			t.Fatalf("blackhole boundary %s: decision=%+v error=%v", tc.at, decision, nextErr)
		}
	}
	profile.LossPermille = 500
	blackholed, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	profile.BlackholeEvery, profile.BlackholeFor = 0, 0
	uninterrupted, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1000 {
		at := time.Duration(i) * time.Millisecond
		withBlackhole, err := blackholed.next(at)
		if err != nil {
			t.Fatal(err)
		}
		withoutBlackhole, err := uninterrupted.next(at)
		if err != nil {
			t.Fatal(err)
		}
		if withBlackhole.disposition != packetBlackhole && withBlackhole != withoutBlackhole {
			t.Fatal("blackhole changed later random-loss decisions or sequence identity")
		}
	}
}

func TestLossScheduleRejectsInvalidProgress(t *testing.T) {
	t.Parallel()

	profile, err := DefaultProfile("fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range []uint64{0, uint64(profile.Sessions) + 1} {
		if _, peerErr := newLossSchedule(profile, peer); !errors.Is(peerErr, ErrProfile) {
			t.Fatalf("invalid peer identity accepted: peer=%d error=%v", peer, peerErr)
		}
	}
	schedule, err := newLossSchedule(profile, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, negativeErr := schedule.next(-time.Nanosecond); !errors.Is(negativeErr, errScheduleInput) {
		t.Fatalf("negative elapsed time accepted: %v", negativeErr)
	}
	first, err := schedule.next(time.Millisecond)
	if err != nil || first.sequence != 1 || first.disposition != packetTransmit {
		t.Fatalf("rejected input changed the next transmission: decision=%+v error=%v", first, err)
	}
	if _, regressionErr := schedule.next(0); !errors.Is(regressionErr, errScheduleInput) {
		t.Fatalf("elapsed time regression accepted: %v", regressionErr)
	}
	second, err := schedule.next(time.Millisecond)
	if err != nil || second.sequence != 2 {
		t.Fatalf("rejected regression advanced the sequence: decision=%+v error=%v", second, err)
	}
	schedule.sequence = math.MaxUint64
	if _, err := schedule.next(time.Millisecond); !errors.Is(err, errScheduleInput) {
		t.Fatalf("exhausted sequence wrapped: %v", err)
	}
}
