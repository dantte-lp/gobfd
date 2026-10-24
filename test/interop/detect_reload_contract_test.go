//go:build interop_testcontainers

package interop_test

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTimerReloadDetectOneJitter(t *testing.T) {
	t.Parallel()
	intervals := make([]int64, 100)
	for index := range intervals {
		intervals[index] = 247500
	}
	mean, err := checkJitteredIntervals(intervals, 300000, 1)
	if err != nil || mean != 247500 {
		t.Fatalf("DetectMult 1 jitter mean=%f error=%v, want 247500 us", mean, err)
	}
	intervals[0] = 285000
	if _, boundErr := checkJitteredIntervals(intervals, 300000, 1); boundErr == nil {
		t.Fatal("DetectMult 1 accepted a 95% interval")
	}
	for _, multiplier := range []uint64{0, 256} {
		if _, modeErr := checkJitteredIntervals(intervals, 300000, multiplier); modeErr == nil {
			t.Fatalf("accepted invalid multiplier %d", multiplier)
		}
	}
}

func detectContractFrames(demand bool) ([]detectFrame, detectPhase) {
	at := time.Unix(100, 0).UTC()
	local := detectFrame{
		Number: 1, Epoch: at, Source: gobfdIP, Destination: frrIP,
		State: 3, Mine: 11, Yours: 22, TX: 300000, RX: 300000, Multiplier: 3,
		AuthPresent: 1, AuthType: 4, Key: 7, Sequence: 100, HasSequence: true, UDPLength: 136, SourcePort: 50000,
	}
	peer := local
	peer.Number, peer.Source, peer.Destination, peer.Mine, peer.Yours = 2, frrIP, gobfdIP, 22, 11
	peer.SourcePort, peer.Sequence = 60000, 200
	phase := detectPhase{
		Peer: frrIP, Start: 2, End: 28, Local: 11, Remote: 22,
		TX: 300000, RX: 300000, RemoteRX: 300000, AppliedAt: at.Add(time.Millisecond),
		Before: [2]detectFrame{local, peer}, PreviousMultiplier: 3, Multiplier: 5, Mode: timerReceiptImmediate,
	}
	if demand {
		local.Destination, peer.Source, peer.Demand = fuzzIP, fuzzIP, 1
		for _, frame := range []*detectFrame{&local, &peer} {
			frame.AuthPresent, frame.AuthType, frame.Key, frame.Sequence, frame.HasSequence = 0, 0, 0, 0, false
		}
		local.TX, local.RX = 500000, 500000
		phase.Peer, phase.TX, phase.RX, phase.End = fuzzIP, 500000, 500000, 10
		phase.Before, phase.Mode = [2]detectFrame{local, peer}, timerReceiptPoll
		phase.AppliedAt = at.Add(300 * time.Microsecond)
		poll, final := local, peer
		poll.Number, poll.Epoch, poll.Poll, poll.Multiplier = 3, at.Add(100*time.Microsecond), 1, 5
		final.Number, final.Epoch, final.Final = 4, at.Add(200*time.Microsecond), 1
		frames := make([]detectFrame, 0, 8)
		frames = append(frames, poll, final)
		for index := range 6 {
			frame := peer
			frame.Number, frame.Epoch = uint64(5+index), at.Add(time.Duration(index+1)*500*time.Millisecond)
			frames = append(frames, frame)
		}
		return frames, phase
	}
	var frames []detectFrame
	for index := range 13 {
		incoming, outgoing := peer, local
		incoming.Number, outgoing.Number = uint64(3+2*index), uint64(4+2*index)
		outgoing.Epoch = at.Add(time.Duration(index+1) * 262500 * time.Microsecond)
		incoming.Epoch = outgoing.Epoch.Add(-time.Microsecond)
		incoming.Sequence, outgoing.Sequence = uint64(201+index), uint64(101+index)
		outgoing.Multiplier = 5
		frames = append(frames, incoming, outgoing)
	}
	return frames, phase
}

func TestTimerReloadDetectAsyncEvidence(t *testing.T) {
	t.Parallel()
	frames, phase := detectContractFrames(false)
	result, err := evaluateDetectPhase(frames, phase)
	if err != nil || result.DetectionMicroseconds != 900000 ||
		result.DerivedPeerDetectionMicroseconds != 1500000 || result.MeanMicroseconds != 262500 {
		t.Fatalf("Async multiplier change result=%+v error=%v", result, err)
	}
}

func TestTimerReloadDetectDemandEvidence(t *testing.T) {
	t.Parallel()
	frames, phase := detectContractFrames(true)
	result, err := evaluateDetectPhase(frames, phase)
	if err != nil || result.Poll != 3 || result.Final != 4 || result.DetectionMicroseconds != 1500000 ||
		result.DerivedPollDetectionMicroseconds != 2500000 || result.DerivedPeerDetectionMicroseconds != 0 {
		t.Fatalf("Demand multiplier change result=%+v error=%v", result, err)
	}
}

func TestTimerReloadDetectCapture(t *testing.T) {
	t.Parallel()
	rows := [][]string{{
		"1", "100.1", gobfdIP, frrIP, "3", "0", "0", "11", "22",
		"300000", "300000", "5", "1", "4", "7", "0xffffffff", "0", "136", "50000",
	}}
	frames, err := parseDetectFrames(rows)
	if err != nil || len(frames) != 1 {
		t.Fatalf("parse DetectMult capture: frames=%+v error=%v", frames, err)
	}
	if frames[0].SourcePort != 50000 || frames[0].AuthType != 4 || frames[0].Key != 7 ||
		frames[0].Sequence != 4294967295 || !frames[0].HasSequence {
		t.Fatalf("capture identity/auth fields lost: %+v", frames[0])
	}
}

func TestTimerReloadDetectRejectsRegressions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		demand bool
		mutate func([]detectFrame, *detectPhase) []detectFrame
	}{
		{"local multiplier", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[3].Multiplier = 4; return f }},
		{"remote multiplier", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[0].Multiplier = 5; return f }},
		{"Async Poll", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].Poll = 1; return f }},
		{"discriminator", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].Mine++; return f }},
		{"UDP port", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].SourcePort++; return f }},
		{"padding length", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].UDPLength--; return f }},
		{"auth key", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].Key++; return f }},
		{"auth type", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].AuthType = 2; return f }},
		{"auth rollback", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].Sequence = 99; return f }},
		{"remote Demand", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[0].Demand = 1; return f }},
		{"remote timer", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[0].TX++; return f }},
		{"local timer", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].RX++; return f }},
		{"Down", false, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].State = 1; return f }},
		{"mode", false, func(f []detectFrame, p *detectPhase) []detectFrame { p.Mode = 0; return f }},
		{"zero multiplier", false, func(f []detectFrame, p *detectPhase) []detectFrame { p.Multiplier = 0; return f }},
		{"oversize multiplier", false, func(f []detectFrame, p *detectPhase) []detectFrame { p.Multiplier = 256; return f }},
		{"baseline remote RX", false, func(f []detectFrame, p *detectPhase) []detectFrame { p.Before[1].RX++; return f }},
		{"baseline source", false, func(f []detectFrame, p *detectPhase) []detectFrame {
			p.Before[0].Source = frrIP
			return f
		}},
		{"missing Final", true, func(f []detectFrame, _ *detectPhase) []detectFrame { f[1].Final = 0; return f }},
		{"Final before Poll", true, func(f []detectFrame, _ *detectPhase) []detectFrame {
			f[0].Poll, f[0].Final, f[0].Source, f[0].Destination = 0, 1, fuzzIP, gobfdIP
			f[0].Mine, f[0].Yours, f[0].Demand, f[0].SourcePort = 22, 11, 1, 60000
			f[0].TX, f[0].RX, f[0].Multiplier = 300000, 300000, 3
			f[1].Final = 0
			return f
		}},
		{"receipt before Final", true, func(f []detectFrame, p *detectPhase) []detectFrame {
			p.AppliedAt = f[1].Epoch
			return f
		}},
		{"periodic under Demand", true, func(f []detectFrame, _ *detectPhase) []detectFrame { f[0].Poll = 0; return f }},
		{"short quiet window", true, func(f []detectFrame, p *detectPhase) []detectFrame {
			p.End = f[2].Number
			return f[:3]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frames, phase := detectContractFrames(test.demand)
			frames = test.mutate(frames, &phase)
			if _, err := evaluateDetectPhase(frames, phase); err == nil {
				t.Fatal("accepted invalid DetectMult evidence")
			}
		})
	}
}

func TestTimerReloadDetectSequenceWrap(t *testing.T) {
	t.Parallel()
	frames, phase := detectContractFrames(false)
	phase.Before[0].AuthType, phase.Before[0].Sequence = 5, 4294967295
	for index := 1; index < len(frames); index += 2 {
		frames[index].AuthType, frames[index].Sequence = 5, uint64(index/2)
	}
	if _, err := evaluateDetectPhase(frames, phase); err != nil {
		t.Fatalf("modulo wrap rejected: %v", err)
	}
	frames[1].Sequence = 4294967295
	if _, err := evaluateDetectPhase(frames, phase); err == nil {
		t.Fatal("meticulous repeated sequence accepted")
	}
}

func TestTimerReloadDetectNextControl(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		oldCount int
		offset   time.Duration
		delayed  bool
		valid    bool
	}{
		{"one bounded in-flight", 1, time.Microsecond, false, true},
		{"two old packets", 2, time.Microsecond, false, false},
		{"late old packet", 1, 7 * time.Millisecond, false, false},
		{"late first new packet", 0, 0, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frames, phase := detectContractFrames(false)
			combined := make([]detectFrame, 0, len(frames)+test.oldCount)
			for index := range test.oldCount {
				old := phase.Before[0]
				old.Number, old.Epoch = uint64(3+index), phase.AppliedAt.Add(test.offset+time.Duration(index)*time.Microsecond)
				combined = append(combined, old)
			}
			for _, frame := range frames {
				frame.Number += uint64(test.oldCount)
				if test.delayed {
					frame.Epoch = frame.Epoch.Add(time.Second)
				}
				combined = append(combined, frame)
			}
			phase.End += uint64(test.oldCount)
			_, err := evaluateDetectPhase(combined, phase)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}

func TestTimerReloadDetectCaptureRejectsMalformedAuth(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		column int
		value  string
	}{
		{"missing type", 13, ""},
		{"missing key", 14, ""},
		{"missing sequence", 15, ""},
		{"oversize sequence", 15, "0x100000000"},
		{"multiple keys", 14, "7,8"},
		{"unmarked auth", 12, "0"},
		{"bad flag", 12, "2"},
		{"short UDP", 17, "31"},
		{"oversize UDP", 17, "65536"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			row := []string{
				"1", "100.1", gobfdIP, frrIP, "3", "0", "0", "11", "22",
				"300000", "300000", "5", "1", "4", "7", "0xffffffff", "0", "136", "50000",
			}
			row[test.column] = test.value
			if _, err := parseDetectFrames([][]string{row}); err == nil {
				t.Fatal("accepted malformed auth/transport")
			}
		})
	}
	row := []string{
		"1", "100.1", gobfdIP, fuzzIP, "3", "0", "0", "11", "22",
		"300000", "300000", "5", "0", "", "", "", "0", "32", "50000",
	}
	if _, err := parseDetectFrames([][]string{row}); err != nil {
		t.Fatalf("absent auth columns rejected: %v", err)
	}
}

func TestTimerReloadDetectConfigPreservesPeer(t *testing.T) {
	t.Parallel()
	const baseline = `sessions:
  - peer: "172.20.0.20"
    detect_mult: 3
    desired_min_tx: 300ms
    auth:
      key_id: 7
    padded_pdu_size: 128
  - peer: "172.20.0.30"
    detect_mult: 3
`
	const expected = `sessions:
  - peer: "172.20.0.20"
    detect_mult: 5
    desired_min_tx: 300ms
    auth:
      key_id: 7
    padded_pdu_size: 128
  - peer: "172.20.0.30"
    detect_mult: 3
`
	actual, err := detectConfig(baseline, "172.20.0.20", 5)
	if err != nil || actual != expected {
		t.Fatalf("DetectMult config lost fields: %q error=%v", actual, err)
	}
	_, err = detectConfig(strings.Replace(baseline, "detect_mult: 3", "detect_mult: 30", 1), "172.20.0.20", 5)
	if !errors.Is(err, errTimerEvidence) {
		t.Fatalf("numeric prefix accepted: %v", err)
	}
}

func TestTimerReloadDetectCraftedWindows(t *testing.T) {
	t.Parallel()
	initial, first := detectContractFrames(true)
	second := first
	second.Start, second.End, second.PreviousMultiplier, second.Multiplier = 10, 16, 5, 3
	second.Before = [2]detectFrame{initial[0], initial[7]}
	second.AppliedAt = first.Before[0].Epoch.Add(3300 * time.Millisecond)
	frames := make([]craftedFrame, 0, 16)
	frames = append(frames,
		craftedFrame{timerFrame: first.Before[0].timerFrame}, craftedFrame{timerFrame: first.Before[1].timerFrame, Demand: 1})
	for _, frame := range initial {
		frames = append(frames, craftedFrame{timerFrame: frame.timerFrame, Demand: frame.Demand})
	}
	poll, final := second.Before[0].timerFrame, second.Before[1].timerFrame
	poll.Number, poll.Epoch, poll.Multiplier = 11, first.Before[0].Epoch.Add(3100*time.Millisecond), 3
	final.Number, final.Epoch, final.Final = 12, first.Before[0].Epoch.Add(3200*time.Millisecond), 1
	frames = append(frames, craftedFrame{timerFrame: poll}, craftedFrame{timerFrame: final, Demand: 1})
	for index := range 4 {
		frame := second.Before[1].timerFrame
		frame.Number = uint64(13 + index)
		frame.Epoch = first.Before[0].Epoch.Add(time.Duration(3500+index*500) * time.Millisecond)
		frames = append(frames, craftedFrame{timerFrame: frame, Demand: 1})
	}
	plan := craftedPlan{ReloadTX: 500000, ReloadRX: 500000, DetectUpdates: []detectPhase{first, second}}
	if err := evaluateCraftedDetect(frames, 0, 17, plan); err != nil {
		t.Fatalf("ordered DetectMult phases rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func([]craftedFrame, *craftedPlan)
	}{
		{"periodic inside window", func(f []craftedFrame, _ *craftedPlan) { f[10].Poll = 0 }},
		{"missing own Final", func(f []craftedFrame, _ *craftedPlan) { f[11].Final = 0 }},
		{"overlap", func(_ []craftedFrame, p *craftedPlan) { p.DetectUpdates[1].Start = 9 }},
		{"outside Demand", func(_ []craftedFrame, p *craftedPlan) { p.DetectUpdates[1].End = 17 }},
		{"not restored", func(_ []craftedFrame, p *craftedPlan) { p.DetectUpdates = p.DetectUpdates[:1] }},
		{"baseline mismatch", func(_ []craftedFrame, p *craftedPlan) { p.DetectUpdates[1].Before[0].Number++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			copyFrames := append([]craftedFrame(nil), frames...)
			copyPlan := plan
			copyPlan.DetectUpdates = append([]detectPhase(nil), plan.DetectUpdates...)
			test.mutate(copyFrames, &copyPlan)
			if err := evaluateCraftedDetect(copyFrames, 0, 17, copyPlan); err == nil {
				t.Fatal("accepted invalid window")
			}
		})
	}
}
