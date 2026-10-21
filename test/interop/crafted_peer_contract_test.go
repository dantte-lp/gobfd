//go:build interop_testcontainers

package interop_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

type craftedTimeline struct {
	frames []craftedFrame
	start  time.Time
}

// add appends one frame at the given millisecond offset from the timeline start.
func (timeline *craftedTimeline) add(fromPeer bool, milliseconds int, mutate func(*craftedFrame)) {
	var frame craftedFrame
	frame.Number = uint64(len(timeline.frames) + 1)
	frame.Epoch = timeline.start.Add(time.Duration(milliseconds) * time.Millisecond)
	frame.Source, frame.Destination, frame.State, frame.Mine, frame.Yours = gobfdIP, fuzzIP, 3, 7, 9
	frame.TX, frame.RX, frame.Multiplier = 300000, 300000, 3
	if fromPeer {
		frame.Source, frame.Destination, frame.Mine, frame.Yours = fuzzIP, gobfdIP, 9, 7
	}
	if mutate != nil {
		mutate(&frame)
	}
	timeline.frames = append(timeline.frames, frame)
}

func craftedPoll(frame *craftedFrame)  { frame.Poll = 1 }
func craftedFinal(frame *craftedFrame) { frame.Final = 1 }

// craftedContractFrames models one RFC-conforming capture: RX0 with two Polls,
// silence, recovery, then Demand with a GoBFD timer Poll and Demand clear.
func craftedContractFrames() []craftedFrame {
	timeline := &craftedTimeline{start: time.Unix(1_800_000_000, 0)}
	for offset := 0; offset < 900; offset += 270 {
		timeline.add(true, offset, nil)
		timeline.add(false, offset+100, nil)
	}
	rx0 := func(mutate func(*craftedFrame)) func(*craftedFrame) {
		return func(frame *craftedFrame) {
			frame.RX = 0
			if mutate != nil {
				mutate(frame)
			}
		}
	}
	for index := range 20 {
		offset := 1000 + index*270
		if index == 0 || index == 10 {
			timeline.add(true, offset, rx0(craftedPoll))
			timeline.add(false, offset+1, craftedFinal)
			continue
		}
		timeline.add(true, offset, rx0(nil))
	}
	// The last RX0 frame is at 6130 ms; Detection Time is 900 ms.
	down := func(frame *craftedFrame) { frame.State, frame.Diag, frame.Yours, frame.TX = 1, 1, 0, 1000000 }
	for _, offset := range []int{7035, 7935, 8835, 9735, 10635} {
		timeline.add(false, offset, down)
	}
	for offset := 11000; offset < 14500; offset += 270 {
		timeline.add(true, offset, nil)
		timeline.add(false, offset+100, nil)
	}
	demand := func(mutate func(*craftedFrame)) func(*craftedFrame) {
		return func(frame *craftedFrame) {
			frame.Demand = 1
			if mutate != nil {
				mutate(frame)
			}
		}
	}
	timeline.add(true, 15000, demand(craftedPoll))
	timeline.add(false, 15001, craftedFinal)
	for offset := 15270; offset < 19500; offset += 270 {
		timeline.add(true, offset, demand(nil))
		if offset == 17160 {
			timeline.add(false, offset+10, func(frame *craftedFrame) { frame.Poll, frame.TX, frame.RX = 1, 500000, 500000 })
			timeline.add(true, offset+11, demand(func(frame *craftedFrame) { frame.Final = 1 }))
		}
	}
	reloaded := func(mutate func(*craftedFrame)) func(*craftedFrame) {
		return func(frame *craftedFrame) {
			frame.TX, frame.RX = 500000, 500000
			if mutate != nil {
				mutate(frame)
			}
		}
	}
	timeline.add(true, 19500, craftedPoll)
	timeline.add(false, 19501, reloaded(craftedFinal))
	offset := 19700
	for index := range 14 {
		timeline.add(false, offset, reloaded(nil))
		timeline.add(true, offset+5, nil)
		offset += []int{400, 475}[index%2]
	}
	return timeline.frames
}

func craftedContractPlan() craftedPlan {
	return craftedPlan{DetectionMicroseconds: 900000, ReloadTX: 500000, ReloadRX: 500000}
}

func craftedFrameAt(frames []craftedFrame, fromPeer bool, milliseconds int) int {
	start := time.Unix(1_800_000_000, 0)
	for index, frame := range frames {
		if (frame.Source == fuzzIP) == fromPeer && frame.Epoch.Sub(start) == time.Duration(milliseconds)*time.Millisecond {
			return index
		}
	}
	return -1
}

func craftedWithout(frames []craftedFrame, fromPeer bool, milliseconds ...int) []craftedFrame {
	return slices.DeleteFunc(frames, func(frame craftedFrame) bool {
		for _, offset := range milliseconds {
			if (frame.Source == fuzzIP) == fromPeer &&
				frame.Epoch.Sub(time.Unix(1_800_000_000, 0)) == time.Duration(offset)*time.Millisecond {
				return true
			}
		}
		return false
	})
}

func craftedInsert(frames []craftedFrame, fromPeer bool, milliseconds int, mutate func(*craftedFrame)) []craftedFrame {
	timeline := &craftedTimeline{start: time.Unix(1_800_000_000, 0)}
	timeline.add(fromPeer, milliseconds, mutate)
	frame := timeline.frames[0]
	index, _ := slices.BinarySearchFunc(frames, frame.Epoch, func(existing craftedFrame, epoch time.Time) int {
		return existing.Epoch.Compare(epoch)
	})
	frames = slices.Insert(frames, index, frame)
	for position := range frames {
		frames[position].Number = uint64(position + 1)
	}
	return frames
}

func TestCraftedPeerEvidenceValid(t *testing.T) {
	t.Parallel()
	evidence, err := evaluateCraftedPeerCapture(craftedContractFrames(), craftedContractPlan())
	if err != nil {
		t.Fatalf("valid capture rejected: %v", err)
	}
	if evidence.RX0.Polls != 2 || evidence.RX0.QuietMicroseconds < 3000000 ||
		evidence.Silence.ResumeMicroseconds != 905000 || evidence.Silence.DownIntervals != 4 ||
		evidence.Demand.GoBFDPolls != 1 || evidence.Demand.Intervals != 13 || evidence.Demand.QuietMicroseconds < 3000000 {
		t.Fatalf("unexpected evidence %+v", evidence)
	}
}

func TestCraftedPeerEvidenceRejectsRFCViolations(t *testing.T) {
	t.Parallel()
	gobfdFrame := false
	peerFrame := true
	for _, test := range []struct {
		name   string
		mutate func([]craftedFrame, *craftedPlan) []craftedFrame
	}{
		{"no RX0 Poll", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			frames[craftedFrameAt(frames, peerFrame, 1000)].Poll = 0
			return frames
		}},
		{"periodic under RX0", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedInsert(frames, gobfdFrame, 3000, nil)
		}},
		{"first RX0 Poll unanswered", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, gobfdFrame, 1001)
		}},
		{"mid RX0 Poll unanswered", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, gobfdFrame, 3701)
		}},
		{"Final without Poll", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedInsert(frames, gobfdFrame, 4500, craftedFinal)
		}},
		{"no resume after silence", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, gobfdFrame, 7035, 7935, 8835, 9735, 10635)
		}},
		{"resume before Detection Time", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			frames[craftedFrameAt(frames, gobfdFrame, 7035)].Epoch = time.Unix(1_800_000_000, 0).Add(6930 * time.Millisecond)
			return frames
		}},
		// RFC 5880 Section 6.8.18 lets GoBFD "transmit at its own rate" after the reset: one slow interval.
		{"resume beyond one slow interval", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, gobfdFrame, 7035, 7935)
		}},
		{"resume without Control Detection Time Expired", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			frames[craftedFrameAt(frames, gobfdFrame, 7035)].Diag = 0
			return frames
		}},
		{"resume keeps Your Discriminator", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			frames[craftedFrameAt(frames, gobfdFrame, 7035)].Yours = 9
			return frames
		}},
		{"duplicate Down send", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedInsert(frames, gobfdFrame, 7037, func(frame *craftedFrame) {
				frame.State, frame.Diag, frame.Yours, frame.TX = 1, 1, 0, 1000000
			})
		}},
		{"too few Down intervals", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, gobfdFrame, 8835, 9735, 10635)
		}},
		{"Detection Time disagrees with status", func(frames []craftedFrame, plan *craftedPlan) []craftedFrame {
			plan.DetectionMicroseconds = 1200000
			return frames
		}},
		{"no Demand Poll", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			frames[craftedFrameAt(frames, peerFrame, 15000)].Poll = 0
			return frames
		}},
		{"periodic under Demand", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedInsert(frames, gobfdFrame, 16000, nil)
		}},
		{"no GoBFD Poll under Demand", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, gobfdFrame, 17170)
		}},
		{"Poll with old timers", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			frames[craftedFrameAt(frames, gobfdFrame, 17170)].TX = 300000
			return frames
		}},
		{"Poll after Final", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedInsert(frames, gobfdFrame, 18000, func(frame *craftedFrame) {
				frame.Poll, frame.TX, frame.RX = 1, 500000, 500000
			})
		}},
		{"no peer Final for GoBFD Poll", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, peerFrame, 17171)
		}},
		{"Demand clear unanswered", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, gobfdFrame, 19501)
		}},
		{"no resume after Demand clear", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return slices.DeleteFunc(frames, func(frame craftedFrame) bool {
				return frame.Source == gobfdIP && frame.Epoch.After(time.Unix(1_800_000_000, 0).Add(19600*time.Millisecond))
			})
		}},
		{"late resume after Demand clear", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedWithout(frames, gobfdFrame, 19700)
		}},
		{"resumed interval below jitter", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return craftedInsert(frames, gobfdFrame, 19900, func(frame *craftedFrame) { frame.TX, frame.RX = 500000, 500000 })
		}},
		{"too few resumed intervals", func(frames []craftedFrame, _ *craftedPlan) []craftedFrame {
			return frames[:len(frames)-6]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frames, plan := craftedContractFrames(), craftedContractPlan()
			frames = test.mutate(frames, &plan)
			if evidence, err := evaluateCraftedPeerCapture(frames, plan); err == nil {
				t.Fatalf("violation accepted: %+v", evidence)
			}
		})
	}
}

func TestCraftedPeerEvidenceAllowsResumeAtNextSlowTick(t *testing.T) {
	t.Parallel()
	frames := craftedWithout(craftedContractFrames(), false, 7035)
	evidence, err := evaluateCraftedPeerCapture(frames, craftedContractPlan())
	if err != nil {
		t.Fatalf("resumption at the next slow-rate tick rejected: %v", err)
	}
	if evidence.Silence.ResumeMicroseconds != 1805000 || evidence.Silence.DownIntervals != 3 {
		t.Fatalf("unexpected silence evidence %+v", evidence.Silence)
	}
}

func TestCraftedPeerEvidenceDetectionReset(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 10, 21, 21, 49, 3, 649705091, time.UTC)
	line := func(at time.Time, peer, oldState, newState, diag string) string {
		return fmt.Sprintf(`{"time":%q,"level":"INFO","msg":"session state changed","peer":%q,`+
			`"old_state":%q,"new_state":%q,"diag":%q}`, at.Format(time.RFC3339Nano), peer, oldState, newState, diag)
	}
	expired := "Control Detection Time Expired"
	valid := strings.Join([]string{
		`not json`,
		line(last.Add(-time.Second), fuzzIP, "Up", "Down", expired),
		line(last.Add(900*time.Millisecond), frrIP, "Up", "Down", expired),
		line(last.Add(900400*time.Microsecond), fuzzIP, "Up", "Down", expired),
	}, "\n")
	elapsed, err := craftedDetectionReset(valid, last, 900000)
	if err != nil || elapsed != 900400 {
		t.Fatalf("reset elapsed=%d error=%v, want 900400", elapsed, err)
	}
	for name, logs := range map[string]string{
		"missing":    line(last.Add(-time.Second), fuzzIP, "Up", "Down", expired),
		"early":      line(last.Add(800*time.Millisecond), fuzzIP, "Up", "Down", expired),
		"late":       line(last.Add(950*time.Millisecond), fuzzIP, "Up", "Down", expired),
		"wrong diag": line(last.Add(900*time.Millisecond), fuzzIP, "Up", "Down", "Neighbor Signaled Session Down"),
	} {
		if _, err := craftedDetectionReset(logs, last, 900000); err == nil {
			t.Fatalf("%s reset accepted", name)
		}
	}
}

func TestCraftedPeerEvidenceAllowsOneInFlightPeriodic(t *testing.T) {
	t.Parallel()
	inFlight := func(count int) []craftedFrame {
		frames := craftedContractFrames()
		poll := craftedFrameAt(frames, true, 15000)
		periodic := frames[craftedFrameAt(frames, false, 14340)]
		periodic.Epoch = frames[poll].Epoch
		frames = slices.Insert(frames, poll+1, slices.Repeat([]craftedFrame{periodic}, count)...)
		for position := range frames {
			frames[position].Number = uint64(position + 1)
		}
		return frames
	}
	if _, err := evaluateCraftedPeerCapture(inFlight(1), craftedContractPlan()); err != nil {
		t.Fatalf("one in-flight periodic frame before the Demand Final rejected: %v", err)
	}
	if _, err := evaluateCraftedPeerCapture(inFlight(2), craftedContractPlan()); err == nil {
		t.Fatal("two periodic frames after the Demand Poll accepted")
	}
}

func TestCraftedPeerEvidenceFrameParsing(t *testing.T) {
	t.Parallel()
	row := []string{
		"11", "1.123", gobfdIP, fuzzIP, "0x01", "False", "False", "0x00000007", "0x00000000",
		"1000000", "300000", "3", "0x01", "True",
	}
	frames, err := parseCraftedFrames([][]string{row})
	if err != nil {
		t.Fatal(err)
	}
	if frames[0].Diag != 1 || frames[0].Demand != 1 || frames[0].State != 1 || frames[0].TX != 1000000 {
		t.Fatalf("parsed frame %+v", frames[0])
	}
	for _, broken := range [][]string{row[:12], append(slices.Clone(row[:13]), "maybe")} {
		if _, err := parseCraftedFrames([][]string{broken}); err == nil {
			t.Fatalf("malformed row accepted: %q", broken)
		}
	}
}
