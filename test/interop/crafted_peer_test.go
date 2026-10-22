//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
)

// The crafted peer reuses the fuzzer address and fixed container name; the
// fuzzer never runs in this topology.
const (
	craftedPeerConfig   = "test/interop/gobfd/crafted-peer.yml"
	craftedBaseInterval = 300000
	craftedReloadTimer  = "500ms"
	craftedReloadMicros = 500000
	// Queued tuples: the second waits behind the first under lost Finals.
	craftedQueuedFirst        = "400ms"
	craftedQueuedFirstMicros  = 400000
	craftedQueuedSecond       = "800ms"
	craftedQueuedSecondMicros = 800000
	craftedQuietMultiple      = 10
	craftedResumeMinimum      = 3
	craftedClockSlack         = 1000
	// craftedStateChangeBound bounds the immediate state-change send after the reset.
	craftedStateChangeBound = 50000
)

var errCraftedEvidence = errors.New("invalid crafted peer evidence")

// craftedFrame extends the timer frame with the Diagnostic and Demand fields.
type craftedFrame struct {
	timerFrame

	Diag, Demand uint64
}

func (frame craftedFrame) fromPeer() bool { return frame.Source == fuzzIP }

func craftedFrameFields() []string {
	return append(timerFrameFields(), "bfd.diag", "bfd.flags.d")
}

func parseCraftedFrames(rows [][]string) ([]craftedFrame, error) {
	base := len(timerFrameFields())
	timerRows := make([][]string, len(rows))
	for index, row := range rows {
		if len(row) != base+2 {
			return nil, fmt.Errorf("crafted frame columns=%d, want %d: %w", len(row), base+2, errCraftedEvidence)
		}
		timerRows[index] = row[:base]
	}
	timerFrames, err := parseTimerFrames(timerRows)
	if err != nil {
		return nil, err
	}
	frames := make([]craftedFrame, len(timerFrames))
	for index, frame := range timerFrames {
		frames[index].timerFrame = frame
		if frames[index].Diag, err = parseTimerField(rows[index][base]); err != nil {
			return nil, fmt.Errorf("frame field bfd.diag: %w", err)
		}
		if frames[index].Demand, err = parseTimerField(rows[index][base+1]); err != nil || frames[index].Demand > 1 {
			return nil, fmt.Errorf("frame field bfd.flags.d=%q: %w", rows[index][base+1], errors.Join(err, errCraftedEvidence))
		}
	}
	return frames, nil
}

// craftedPlan carries the values the capture is checked against.
type craftedPlan struct {
	DetectionMicroseconds uint64 `json:"detection_microseconds"`
	ReloadTX              uint64 `json:"reload_tx"`
	ReloadRX              uint64 `json:"reload_rx"`
	QueuedFirst           uint64 `json:"queued_first"`
	QueuedSecond          uint64 `json:"queued_second"`
}

type craftedRX0Evidence struct {
	Start                     uint64 `json:"start_frame"`
	Final                     uint64 `json:"final_frame"`
	Polls                     int    `json:"polls"`
	InFlight                  int    `json:"in_flight"`
	QuietMicroseconds         int64  `json:"quiet_us"`
	MaxFinalLatencyMicros     int64  `json:"max_final_latency_us"`
	NegotiatedTXMicroseconds  uint64 `json:"negotiated_tx_us"`
	DetectionTimeMicroseconds uint64 `json:"detection_time_us"`
}

type craftedSilenceEvidence struct {
	LastPeer           uint64 `json:"last_peer_frame"`
	Resume             uint64 `json:"resume_frame"`
	ResumeMicroseconds int64  `json:"resume_us"`
	DownTXMicroseconds uint64 `json:"down_tx_us"`
	DownIntervals      int    `json:"down_intervals"`
	ResetMicroseconds  int64  `json:"reset_us"`
}

type craftedDemandEvidence struct {
	Start                 uint64  `json:"start_frame"`
	Final                 uint64  `json:"final_frame"`
	PeerFinal             uint64  `json:"peer_final_frame"`
	Clear                 uint64  `json:"clear_frame"`
	ClearFinal            uint64  `json:"clear_final_frame"`
	Polls                 int     `json:"peer_polls"`
	GoBFDPolls            int     `json:"gobfd_polls"`
	InFlight              int     `json:"in_flight"`
	QuietMicroseconds     int64   `json:"quiet_us"`
	ResumeMicroseconds    int64   `json:"resume_us"`
	ResumedTXMicroseconds uint64  `json:"resumed_tx_us"`
	Intervals             int     `json:"intervals"`
	MeanMicroseconds      float64 `json:"mean_us"`
}

type craftedEvidence struct {
	RX0     craftedRX0Evidence     `json:"rx0"`
	Silence craftedSilenceEvidence `json:"silence"`
	Demand  craftedDemandEvidence  `json:"demand"`
	Queued  craftedQueuedEvidence  `json:"queued"`
}

type craftedQueuedEvidence struct {
	FirstPoll              uint64 `json:"first_poll_frame"`
	FirstPolls             int    `json:"first_polls"`
	CrossPoll              uint64 `json:"cross_poll_frame"`
	CrossFinal             uint64 `json:"cross_final_frame"`
	ResumedPolls           int    `json:"resumed_polls"`
	PeerFinal              uint64 `json:"peer_final_frame"`
	SecondPoll             uint64 `json:"second_poll_frame"`
	SecondPeerFinal        uint64 `json:"second_peer_final_frame"`
	ExchangeMicroseconds   int64  `json:"exchange_us"`
	SeparationMicroseconds int64  `json:"separation_us"`
}

func craftedNext(frames []craftedFrame, from int, match func(craftedFrame) bool) int {
	for index := max(from, 0); index < len(frames); index++ {
		if match(frames[index]) {
			return index
		}
	}
	return -1
}

func craftedMicros(from, to craftedFrame) int64 { return to.Epoch.Sub(from.Epoch).Microseconds() }

// craftedPairPolls requires exactly one GoBFD Final after each peer Poll and
// none after other peer frames, each before the next peer frame (RFC 5880
// Sections 6.5 and 6.8.7). It returns the Poll count and largest latency.
func craftedPairPolls(frames []craftedFrame, first, last int) (int, int64, error) {
	polls, latency := 0, int64(0)
	for index := first; index <= last; index++ {
		if !frames[index].fromPeer() {
			continue
		}
		finals := 0
		for next := index + 1; next < len(frames) && !frames[next].fromPeer(); next++ {
			if frames[next].Final == 1 {
				finals++
				latency = max(latency, craftedMicros(frames[index], frames[next]))
			}
		}
		if want := int(frames[index].Poll); finals != want {
			return 0, 0, fmt.Errorf("peer frame %d Poll=%d answered by %d GoBFD Finals: %w",
				frames[index].Number, frames[index].Poll, finals, errCraftedEvidence)
		}
		polls += int(frames[index].Poll)
	}
	return polls, latency, nil
}

// evaluateCraftedPeerCapture checks the crafted peer capture against RFC 5880
// Sections 6.8.7 (remote Required Min RX 0), 6.8.18 (reset after silence) and
// 6.6 (remote Demand with a Poll Sequence for a local timer change).
func evaluateCraftedPeerCapture(frames []craftedFrame, plan craftedPlan) (craftedEvidence, error) {
	var evidence craftedEvidence
	for _, frame := range frames {
		toGoBFD := frame.Source == fuzzIP && frame.Destination == gobfdIP
		toPeer := frame.Source == gobfdIP && frame.Destination == fuzzIP
		// RFC 5880 Section 6.5: "A BFD Control packet MUST NOT have both the Poll (P) and Final (F) bits set."
		if frame.Poll == 1 && frame.Final == 1 {
			return evidence, fmt.Errorf("frame %d sets Poll and Final: %w", frame.Number, errCraftedEvidence)
		}
		if !toGoBFD && !toPeer {
			return evidence, fmt.Errorf("frame %d direction %s->%s: %w",
				frame.Number, frame.Source, frame.Destination, errCraftedEvidence)
		}
	}
	last, resume, err := evaluateCraftedRX0(frames, plan, &evidence)
	if err != nil {
		return evidence, err
	}
	recovered, err := evaluateCraftedSilence(frames, last, resume, &evidence)
	if err != nil {
		return evidence, err
	}
	if err := evaluateCraftedDemand(frames, recovered, plan, &evidence); err != nil {
		return evidence, err
	}
	clearFinal := craftedNext(frames, 0, func(frame craftedFrame) bool {
		return frame.Number == evidence.Demand.ClearFinal
	})
	return evidence, evaluateCraftedQueued(frames, clearFinal+1, plan, &evidence.Queued)
}

func evaluateCraftedRX0(frames []craftedFrame, plan craftedPlan, evidence *craftedEvidence) (int, int, error) {
	start := craftedNext(frames, 0, func(frame craftedFrame) bool { return frame.fromPeer() && frame.RX == 0 })
	before := -1
	for index := start - 1; index >= 0; index-- {
		if frames[index].fromPeer() {
			before = index
			break
		}
	}
	if start < 0 || before < 0 || frames[start].Poll != 1 || frames[start].State != 3 {
		return 0, 0, fmt.Errorf("RX0 must start with an Up Poll after peer traffic (RFC 5880 6.8.3): %w", errCraftedEvidence)
	}
	final := craftedNext(frames, start+1, func(frame craftedFrame) bool { return !frame.fromPeer() && frame.Final == 1 })
	last := start
	for index := start; index < len(frames); index++ {
		if frames[index].fromPeer() {
			if frames[index].RX != 0 {
				break
			}
			if frames[index].State != 3 {
				return 0, 0, fmt.Errorf("RX0 peer frame %d left Up: %w", frames[index].Number, errCraftedEvidence)
			}
			last = index
		}
	}
	if final < 0 || final > last {
		return 0, 0, fmt.Errorf("no GoBFD Final for the RX0 Poll (RFC 5880 6.8.7): %w", errCraftedEvidence)
	}
	detection := frames[last].Multiplier * max(frames[final].RX, frames[last].TX)
	if detection != plan.DetectionMicroseconds {
		return 0, 0, fmt.Errorf("wire Detection Time %d us, status %d us (RFC 5880 6.8.4): %w",
			detection, plan.DetectionMicroseconds, errCraftedEvidence)
	}
	resume := craftedNext(frames, last+1, func(frame craftedFrame) bool { return !frame.fromPeer() })
	for index := start + 1; index <= last; index++ {
		frame := frames[index]
		if frame.fromPeer() || frame.Final == 1 {
			continue
		}
		if index > final || evidence.RX0.InFlight == 1 {
			return 0, 0, fmt.Errorf("GoBFD frame %d is periodic under remote Required Min RX 0 (RFC 5880 6.8.7): %w",
				frame.Number, errCraftedEvidence)
		}
		evidence.RX0.InFlight++
	}
	polls, latency, err := craftedPairPolls(frames, start, last)
	if err != nil {
		return 0, 0, err
	}
	interval := max(frames[final].TX, frames[before].RX)
	quiet := craftedMicros(frames[final], frames[last]) + int64(detection)
	if polls < 2 || quiet < craftedQuietMultiple*int64(interval) {
		return 0, 0, fmt.Errorf("RX0 Polls=%d quiet=%d us, want >=2 and >=%d intervals of %d us: %w",
			polls, quiet, craftedQuietMultiple, interval, errCraftedEvidence)
	}
	evidence.RX0.Start, evidence.RX0.Final = frames[start].Number, frames[final].Number
	evidence.RX0.Polls, evidence.RX0.QuietMicroseconds, evidence.RX0.MaxFinalLatencyMicros = polls, quiet, latency
	evidence.RX0.NegotiatedTXMicroseconds, evidence.RX0.DetectionTimeMicroseconds = interval, detection
	evidence.Silence.LastPeer = frames[last].Number
	if resume < 0 {
		return 0, 0, fmt.Errorf("GoBFD sent nothing after the peer went silent (RFC 5880 6.8.18): %w", errCraftedEvidence)
	}
	return last, resume, nil
}

func evaluateCraftedSilence(frames []craftedFrame, last, resume int, evidence *craftedEvidence) (int, error) {
	recovered := craftedNext(frames, last+1, craftedFrame.fromPeer)
	if recovered >= 0 && recovered < resume {
		return 0, fmt.Errorf("peer frame %d precedes GoBFD resumption (RFC 5880 6.8.18): %w",
			frames[recovered].Number, errCraftedEvidence)
	}
	detection := int64(evidence.RX0.DetectionTimeMicroseconds)
	first := frames[resume]
	// Section 6.8.18 resets after one Detection Time; Section 6.8.7 sends the
	// changed state at once instead of at the next slow-rate interval.
	elapsed := craftedMicros(frames[last], first)
	if elapsed < detection-craftedClockSlack || elapsed > detection+craftedStateChangeBound+timerSlack(detection) {
		return 0, fmt.Errorf("GoBFD sent the state change %d us after silence, want Detection Time %d us "+
			"plus at most %d us (RFC 5880 6.8.7, 6.8.18): %w",
			elapsed, detection, craftedStateChangeBound, errCraftedEvidence)
	}
	if first.State != 1 || first.Diag != 1 || first.Yours != 0 || first.Final != 0 || first.TX < 1000000 {
		return 0, fmt.Errorf("resumed frame %+v, want Down diag 1 Your 0 at the slow rate (RFC 5880 6.8.1, 6.8.3, 6.8.4): %w",
			first, errCraftedEvidence)
	}
	end := len(frames)
	if recovered >= 0 {
		end = recovered
	}
	var intervals []int64
	previous := first
	for _, frame := range frames[resume+1 : end] {
		if frame.State != 1 || frame.Diag != 1 {
			return 0, fmt.Errorf("silent-phase frame %+v, want Down diag 1: %w", frame, errCraftedEvidence)
		}
		intervals = append(intervals, craftedMicros(previous, frame))
		previous = frame
	}
	if len(intervals) < craftedResumeMinimum {
		return 0, fmt.Errorf("Down intervals=%d, want >=%d: %w", len(intervals), craftedResumeMinimum, errCraftedEvidence)
	}
	if err := checkIntervalBounds(intervals, int64(first.TX)); err != nil {
		return 0, err
	}
	evidence.Silence.Resume, evidence.Silence.ResumeMicroseconds = first.Number, elapsed
	evidence.Silence.DownTXMicroseconds, evidence.Silence.DownIntervals = first.TX, len(intervals)
	if recovered < 0 {
		return 0, fmt.Errorf("peer never recovered after silence: %w", errCraftedEvidence)
	}
	return recovered, nil
}

// craftedDetectionReset finds the daemon's first detection-expiry Down for the
// crafted peer after the last peer frame and requires it one Detection Time
// later; the Section 6.8.18 reset runs in the same handler.
func craftedDetectionReset(logs string, lastPeer time.Time, detection uint64) (int64, error) {
	for line := range strings.SplitSeq(logs, "\n") {
		var entry struct {
			Time     time.Time `json:"time"`
			Message  string    `json:"msg"`
			Peer     string    `json:"peer"`
			OldState string    `json:"old_state"`
			NewState string    `json:"new_state"`
			Diag     string    `json:"diag"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Message != "session state changed" ||
			entry.Peer != fuzzIP || entry.OldState != "Up" || entry.NewState != "Down" || !entry.Time.After(lastPeer) {
			continue
		}
		elapsed := entry.Time.Sub(lastPeer).Microseconds()
		if entry.Diag != "Control Detection Time Expired" || elapsed < int64(detection)-craftedClockSlack ||
			elapsed > int64(detection)+timerSlack(int64(detection)) {
			return 0, fmt.Errorf("Down %q %d us after silence, want detection expiry at %d us (RFC 5880 6.8.4, 6.8.18): %w",
				entry.Diag, elapsed, detection, errCraftedEvidence)
		}
		return elapsed, nil
	}
	return 0, fmt.Errorf("no detection-expiry Down after silence: %w", errCraftedEvidence)
}

func evaluateCraftedDemand(frames []craftedFrame, from int, plan craftedPlan, evidence *craftedEvidence) error {
	start := craftedNext(frames, from, func(frame craftedFrame) bool { return frame.fromPeer() && frame.Demand == 1 })
	if start < 0 || frames[start].Poll != 1 || frames[start].State != 3 {
		return fmt.Errorf("Demand must start with an Up Poll (RFC 5880 6.6): %w", errCraftedEvidence)
	}
	clearIndex := craftedNext(frames, start+1, func(frame craftedFrame) bool {
		return frame.fromPeer() && frame.Demand == 0
	})
	final := craftedNext(frames, start+1, func(frame craftedFrame) bool { return !frame.fromPeer() && frame.Final == 1 })
	if clearIndex < 0 || frames[clearIndex].Poll != 1 || final < 0 || final > clearIndex {
		return fmt.Errorf("Demand set/clear Polls and Final missing (RFC 5880 6.6): %w", errCraftedEvidence)
	}
	clearFinalIndex := craftedNext(frames, clearIndex+1, func(frame craftedFrame) bool { return !frame.fromPeer() })
	if clearFinalIndex < 0 || frames[clearFinalIndex].Final != 1 {
		return fmt.Errorf("no GoBFD Final for the Demand clear Poll (RFC 5880 6.8.7): %w", errCraftedEvidence)
	}
	firstPoll, peerFinal := -1, -1
	for index := start + 1; index < clearIndex; index++ {
		frame := frames[index]
		switch {
		case frame.fromPeer():
			if frame.Final == 1 && firstPoll >= 0 && peerFinal < 0 {
				peerFinal = index
			}
		case frame.Poll == 1:
			if peerFinal >= 0 || frame.TX != plan.ReloadTX || frame.RX != plan.ReloadRX || frame.State != 3 {
				return fmt.Errorf("GoBFD Poll %+v, want new timers before the peer Final (RFC 5880 6.5, 6.8.3): %w",
					frame, errCraftedEvidence)
			}
			if firstPoll < 0 {
				firstPoll = index
			}
			evidence.Demand.GoBFDPolls++
		case frame.Final == 0:
			if index > final || evidence.Demand.InFlight == 1 {
				return fmt.Errorf("GoBFD frame %d is periodic under remote Demand (RFC 5880 6.6, 6.8.7): %w",
					frame.Number, errCraftedEvidence)
			}
			evidence.Demand.InFlight++
		}
	}
	if firstPoll < 0 || peerFinal < 0 {
		return fmt.Errorf("no GoBFD Poll Sequence completed under remote Demand (RFC 5880 6.6): %w", errCraftedEvidence)
	}
	polls, _, err := craftedPairPolls(frames, start, clearIndex)
	if err != nil {
		return err
	}
	interval := max(frames[final].TX, frames[start].RX)
	quiet := craftedMicros(frames[final], frames[clearIndex])
	if quiet < craftedQuietMultiple*int64(interval) {
		return fmt.Errorf("Demand quiet=%d us, want >=%d intervals of %d us: %w",
			quiet, craftedQuietMultiple, interval, errCraftedEvidence)
	}
	resumed := max(frames[clearFinalIndex].TX, frames[clearIndex].RX)
	if frames[clearFinalIndex].TX != plan.ReloadTX || frames[clearFinalIndex].RX != plan.ReloadRX {
		return fmt.Errorf("Demand clear Final %+v lacks the reloaded timers: %w", frames[clearFinalIndex], errCraftedEvidence)
	}
	// The resumed window ends at the last peer frame or at the next GoBFD Poll.
	lastPeer := clearIndex
	for index := clearIndex; index < len(frames); index++ {
		if !frames[index].fromPeer() && frames[index].Poll == 1 && index > clearFinalIndex {
			break
		}
		if frames[index].fromPeer() {
			lastPeer = index
		}
	}
	var intervals []int64
	var previous *craftedFrame
	for index := clearFinalIndex + 1; index < lastPeer; index++ {
		frame := frames[index]
		if frame.fromPeer() {
			continue
		}
		if frame.Poll != 0 || frame.Final != 0 || frame.State != 3 || frame.TX != plan.ReloadTX || frame.RX != plan.ReloadRX {
			return fmt.Errorf("resumed frame %+v, want periodic Up with reloaded timers: %w", frame, errCraftedEvidence)
		}
		if previous == nil {
			evidence.Demand.ResumeMicroseconds = craftedMicros(frames[clearFinalIndex], frame)
			if evidence.Demand.ResumeMicroseconds > int64(resumed)+timerSlack(int64(resumed)) {
				return fmt.Errorf("periodic TX resumed %d us after Demand clear, want <= %d us (RFC 5880 6.8.7): %w",
					evidence.Demand.ResumeMicroseconds, resumed, errCraftedEvidence)
			}
		} else {
			intervals = append(intervals, craftedMicros(*previous, frame))
		}
		previous = &frames[index]
	}
	mean, err := checkJitteredIntervals(intervals, int64(resumed))
	if err != nil {
		return err
	}
	evidence.Demand.Start, evidence.Demand.Final = frames[start].Number, frames[final].Number
	evidence.Demand.PeerFinal, evidence.Demand.Clear = frames[peerFinal].Number, frames[clearIndex].Number
	evidence.Demand.ClearFinal, evidence.Demand.Polls = frames[clearFinalIndex].Number, polls
	evidence.Demand.QuietMicroseconds, evidence.Demand.ResumedTXMicroseconds = quiet, resumed
	evidence.Demand.Intervals, evidence.Demand.MeanMicroseconds = len(intervals), mean
	return nil
}

// craftedPeerFinal matches a Final sent by the crafted peer.
func craftedPeerFinal(frame craftedFrame) bool { return frame.fromPeer() && frame.Final == 1 }

// evaluateCraftedQueued checks the lost-Final phase. GoBFD repeats its Poll
// until the peer Final (RFC 5880 Section 6.5), answers the peer's crossing Poll
// with Final only and the active tuple, then resumes its own Poll. A queued tuple
// is advertised only after that Final and after the elapsed-time separation of
// Section 6.8.3, so the two proposals never share one Final.
func evaluateCraftedQueued(frames []craftedFrame, from int, plan craftedPlan, evidence *craftedQueuedEvidence) error {
	first := craftedNext(frames, from, func(frame craftedFrame) bool {
		return !frame.fromPeer() && frame.Poll == 1 && frame.TX == plan.QueuedFirst
	})
	if first < 0 {
		return fmt.Errorf("no GoBFD Poll for the first queued tuple: %w", errCraftedEvidence)
	}
	peerFinal := craftedNext(frames, first+1, craftedPeerFinal)
	crossPoll := craftedNext(frames, first+1, func(frame craftedFrame) bool { return frame.fromPeer() && frame.Poll == 1 })
	if peerFinal < 0 || crossPoll < 0 || crossPoll > peerFinal {
		return fmt.Errorf("no peer crossing Poll before the peer Final: %w", errCraftedEvidence)
	}
	crossFinal := craftedNext(frames, crossPoll+1, func(frame craftedFrame) bool { return !frame.fromPeer() })
	if crossFinal < 0 || crossFinal > peerFinal || frames[crossFinal].Final != 1 ||
		frames[crossFinal].TX != plan.QueuedFirst || frames[crossFinal].RX != plan.QueuedFirst {
		return fmt.Errorf("crossing Poll not answered by a Final with the active tuple (RFC 5880 6.5): %w",
			errCraftedEvidence)
	}
	for index := first; index < peerFinal; index++ {
		frame := frames[index]
		if frame.fromPeer() {
			continue
		}
		if frame.TX != plan.QueuedFirst || frame.RX != plan.QueuedFirst || frame.State != 3 ||
			(index != crossFinal && frame.Poll != 1) {
			return fmt.Errorf("GoBFD frame %+v during the outstanding Poll, want the active tuple with P: %w",
				frame, errCraftedEvidence)
		}
		evidence.FirstPolls += int(frame.Poll)
		if index > crossFinal {
			evidence.ResumedPolls++
		}
	}
	if evidence.FirstPolls < 2 || evidence.ResumedPolls == 0 {
		return fmt.Errorf("Polls=%d resumed=%d, want a repeated Poll that resumes after the crossing Final: %w",
			evidence.FirstPolls, evidence.ResumedPolls, errCraftedEvidence)
	}
	second := craftedNext(frames, peerFinal+1, func(frame craftedFrame) bool {
		return !frame.fromPeer() && frame.Poll == 1
	})
	if second < 0 || frames[second].TX != plan.QueuedSecond || frames[second].RX != plan.QueuedSecond {
		return fmt.Errorf("no separate Poll for the queued tuple after the Final: %w", errCraftedEvidence)
	}
	for _, frame := range frames[peerFinal+1 : second] {
		if !frame.fromPeer() && (frame.TX != plan.QueuedFirst || frame.RX != plan.QueuedFirst) {
			return fmt.Errorf("queued tuple advertised without its own Poll: %+v: %w", frame, errCraftedEvidence)
		}
	}
	exchange := craftedMicros(frames[first], frames[peerFinal])
	separation := craftedMicros(frames[peerFinal], frames[second])
	if separation < exchange-timerSlack(0) {
		return fmt.Errorf("queued Poll %d us after the Final, want at least the %d us exchange (RFC 5880 6.8.3): %w",
			separation, exchange, errCraftedEvidence)
	}
	secondFinal := craftedNext(frames, second+1, craftedPeerFinal)
	if secondFinal < 0 {
		return fmt.Errorf("no peer Final for the queued Poll: %w", errCraftedEvidence)
	}
	evidence.FirstPoll, evidence.CrossPoll, evidence.CrossFinal = frames[first].Number, frames[crossPoll].Number,
		frames[crossFinal].Number
	evidence.PeerFinal, evidence.SecondPoll, evidence.SecondPeerFinal = frames[peerFinal].Number, frames[second].Number,
		frames[secondFinal].Number
	evidence.ExchangeMicroseconds, evidence.SeparationMicroseconds = exchange, separation
	return nil
}

func TestCraftedPeerTestcontainers(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	resources := new(fourPeerResources)

	if !t.Run("remote RX0 silence and Demand", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
		t.Cleanup(cancel)
		root := fourPeerRepositoryRoot(t)
		projectName := fmt.Sprintf("gobfd-interop-crafted-%d", time.Now().UnixNano())
		assertFourPeerNamesAvailable(ctx, t, endpoint)
		t.Setenv("INTEROP_PROJECT_NAME", projectName)
		ensureFourPeerArtifactDirectory(t)
		peer := startCraftedPeerTopology(ctx, t, endpoint, root, projectName, resources)
		registerFourPeerArtifactCleanup(ctx, t, func(artifactCtx context.Context) {
			for name, container := range map[string]string{"daemon": "gobfd-interop", "crafted-peer": "scapy-interop"} {
				logs, err := projectContainerLogs(artifactCtx, container)
				if err != nil {
					t.Errorf("retain %s logs: %v", name, err)
					continue
				}
				writeTimerArtifact(t, name, "jsonl", []byte(logs))
			}
		}, func(artifactCtx context.Context) {
			captureFourPeerPCAP(artifactCtx, t, resources.tshark)
		})
		runCraftedPeerScenario(ctx, t, root, peer)
	}) {
		t.Error("crafted peer testcontainers topology failed")
	}

	for _, containerID := range resources.containerIDs {
		containertest.AssertContainerRemoved(t, endpoint, containerID)
	}
	if resources.networkName != "" {
		containertest.AssertNetworkRemoved(t, resources.networkName)
	}
	for _, imageName := range resources.imageNames {
		containertest.AssertImageRemoved(t, endpoint, imageName)
	}
}

func startCraftedPeerTopology(
	ctx context.Context, t *testing.T, endpoint, root, projectName string, resources *fourPeerResources,
) testcontainers.Container {
	t.Helper()
	buildID := time.Now().UnixNano()
	gobfdImage := buildFourPeerImage(ctx, t, endpoint, prepareFourPeerGoContext(t, root),
		fmt.Sprintf("localhost/gobfd-interop-test:%d", buildID), resources, nil)
	tsharkImage := buildFourPeerImage(ctx, t, endpoint, filepath.Join(root, "test/interop/tshark"),
		fmt.Sprintf("localhost/tshark-interop-test:%d", buildID), resources, nil)
	peerImage := buildFourPeerImage(ctx, t, endpoint, prepareCraftedPeerContext(t, root),
		fmt.Sprintf("localhost/bfd-crafted-peer-test:%d", buildID), resources, nil)
	networkName, labels := startFourPeerNetwork(ctx, t, projectName, "crafted-peer-testcontainers", resources)
	gobfd := startFourPeerGoBFD(ctx, t, gobfdImage, filepath.Join(root, craftedPeerConfig), networkName, labels, resources)
	resources.tshark = startFourPeerTshark(ctx, t, tsharkImage, gobfd, labels, resources)
	// Fail before the scenario when the dissector lacks a queried field.
	code, registry, err := containertest.Exec(ctx, resources.tshark, []string{"tshark", "-G", "fields"})
	for _, field := range craftedFrameFields() {
		if err != nil || code != 0 || !strings.Contains(registry, "\t"+field+"\t") {
			t.Fatalf("tshark field %s unavailable: exit=%d error=%v", field, code, err)
		}
	}
	peer := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:                    peerImage,
		Name:                     "scapy-interop",
		Labels:                   labels,
		Networks:                 []string{networkName},
		Cmd:                      []string{"peer"},
		Env:                      map[string]string{"GOBFD_IP": gobfdIP},
		WaitingFor:               wait.ForLog(`"event":"start"`).WithStartupTimeout(20 * time.Second),
		HostConfigModifier:       addFourPeerCapabilities,
		EndpointSettingsModifier: staticFourPeerIP(networkName, fuzzIP),
	})
	resources.containerIDs = append(resources.containerIDs, peer.GetContainerID())
	return peer
}

// prepareCraftedPeerContext bounds the repository-owned Go tool build to the
// module files, the shared BFD codec and the tool sources.
func prepareCraftedPeerContext(t *testing.T, root string) string {
	t.Helper()
	contextPath := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{"internal", "test/interop/scapy"} {
		subtree, err := fs.Sub(rootFS, sourceDir)
		if err != nil {
			t.Fatalf("open bounded build source %s: %v", sourceDir, err)
		}
		if err := os.CopyFS(filepath.Join(contextPath, sourceDir), subtree); err != nil {
			t.Fatalf("copy bounded build source %s: %v", sourceDir, err)
		}
	}
	for source, destination := range map[string]string{
		"go.mod": "go.mod", "go.sum": "go.sum", "test/interop/scapy/Containerfile": "Containerfile",
	} {
		contents, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			t.Fatalf("read %s for bounded build context: %v", source, err)
		}
		if err := os.WriteFile(filepath.Join(contextPath, destination), contents, 0o600); err != nil {
			t.Fatalf("write %s into bounded build context: %v", destination, err)
		}
	}
	return contextPath
}

func waitCrafted(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait %s: %w", duration, ctx.Err())
	case <-timer.C:
		return nil
	}
}

func waitCraftedUp(ctx context.Context, t *testing.T) {
	t.Helper()
	upCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err := pollUntil(upCtx, 500*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
		output, err := projectContainerCommand(pollCtx, "gobfd-interop",
			"exec", "/bin/gobfdctl", "--addr", "127.0.0.1:50051", "session", "show", fuzzIP, "--format", "json")
		if err != nil {
			return false, false, err
		}
		state, err := parseSessionState([]byte(output), fuzzIP)
		return err == nil, err == nil && state.LocalState == "Up" && state.RemoteState == "Up", err
	})
	if err == nil {
		err = waitTimerQuiet(ctx, fuzzIP, craftedBaseInterval)
	}
	if err != nil {
		t.Fatalf("crafted peer session Up with baseline timers: %v", err)
	}
}

// advanceCrafted moves the peer to its next phase and waits for the GoBFD
// Final that answers the phase's Poll when the phase polls.
func advanceCrafted(ctx context.Context, t *testing.T, peer testcontainers.Container, phase string, final bool) {
	t.Helper()
	boundary, err := lastFrameForFilter(ctx, timerTraffic(fuzzIP))
	if err == nil {
		var output string
		output, err = podmanCommand(ctx, "kill", "--signal", "USR1", peer.GetContainerID())
		writeTimerArtifact(t, "crafted-"+phase+"-signal", "txt", []byte(fmt.Sprintf("%s\nerror=%v\n", output, err)))
	}
	if err == nil && final {
		filter := frameBoundary("bfd && ip.src == "+gobfdIP+" && ip.dst == "+fuzzIP+" && bfd.flags.f == 1", boundary)
		finalCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err = pollUntil(finalCtx, 200*time.Millisecond, func(pollCtx context.Context) (int, bool, error) {
			count, countErr := tsharkCount(pollCtx, filter)
			return count, count > 0, countErr
		})
	}
	if err != nil {
		t.Fatalf("crafted peer phase %s: %v", phase, err)
	}
}

func runCraftedPeerScenario(ctx context.Context, t *testing.T, root string, peer testcontainers.Container) {
	t.Helper()
	baseline, err := os.ReadFile(filepath.Join(root, craftedPeerConfig))
	if err != nil {
		t.Fatalf("read crafted peer daemon baseline: %v", err)
	}
	hold := func(duration time.Duration) {
		if waitErr := waitCrafted(ctx, duration); waitErr != nil {
			t.Fatal(waitErr)
		}
	}
	waitCraftedUp(ctx, t)
	// (a) RFC 5880 Section 6.8.7: remote Required Min RX 0 stops periodic TX; a Poll still gets a Final.
	advanceCrafted(ctx, t, peer, "rx0", true)
	hold(4 * time.Second)
	status, _ := timerSnapshot(ctx, t, fuzzIP, "crafted-rx0")
	detection, err := time.ParseDuration(status.DetectionTime)
	if err != nil || status.LocalState != "Up" || status.RemoteState != "Up" {
		t.Fatalf("RX0 status %+v: %v", status, err)
	}
	advanceCrafted(ctx, t, peer, "poll", true)
	hold(2 * time.Second)
	// (b) RFC 5880 Section 6.8.18: after one silent Detection Time GoBFD transmits again.
	advanceCrafted(ctx, t, peer, "silent", false)
	hold(6 * time.Second)
	advanceCrafted(ctx, t, peer, "recover", false)
	waitCraftedUp(ctx, t)
	// (c) RFC 5880 Section 6.6: remote Demand stops periodic TX; a timer change uses a Poll Sequence.
	receipts := readTimerReceipts(ctx, t, "crafted-demand-before")
	previous := receipts[len(receipts)-1].Applied
	advanceCrafted(ctx, t, peer, "demand", true)
	hold(3 * time.Second)
	appliedAt := reloadCraftedTimers(ctx, t, string(baseline), previous)
	hold(3 * time.Second)
	advanceCrafted(ctx, t, peer, "demand-clear", true)
	hold(8 * time.Second)
	// RFC 5880 Sections 6.5 and 6.8.3: lost Finals, a crossed Poll and a queued
	// tuple that must wait for its own Poll Sequence.
	receipts = readTimerReceipts(ctx, t, "crafted-queued-before")
	previous = receipts[len(receipts)-1].Applied
	advanceCrafted(ctx, t, peer, "final-loss", false)
	boundary := hupCraftedTimers(ctx, t, string(baseline), craftedQueuedFirst, "crafted-queued-first")
	gobfdFrames := "bfd && ip.src == " + gobfdIP + " && ip.dst == " + fuzzIP
	boundary = waitCraftedFrame(ctx, t, gobfdFrames+" && bfd.flags.p == 1 && bfd.desired_min_tx_interval == 400000",
		boundary)
	advanceCrafted(ctx, t, peer, "poll", false)
	boundary = waitCraftedFrame(ctx, t, gobfdFrames+" && bfd.flags.f == 1", boundary)
	hupCraftedTimers(ctx, t, string(baseline), craftedQueuedSecond, "crafted-queued-second")
	waitCraftedFrame(ctx, t, gobfdFrames+" && bfd.flags.p == 1 && bfd.desired_min_tx_interval == 400000", boundary)
	hold(500 * time.Millisecond)
	advanceCrafted(ctx, t, peer, "final-restore", false)
	queuedAt := waitCraftedGeneration(ctx, t, previous, previous+2, "crafted-queued-after")
	hold(2 * time.Second)

	stopTimeout := 10 * time.Second
	if err := peer.Stop(ctx, &stopTimeout); err != nil {
		t.Fatalf("stop crafted peer: %v", err)
	}
	if inspection, err := peer.Inspect(ctx); err != nil || inspection.State == nil || inspection.State.ExitCode != 0 {
		t.Fatalf("crafted peer exit: state=%+v error=%v", inspection, err)
	}
	evaluateCraftedPeerRun(ctx, t, craftedPlan{
		DetectionMicroseconds: uint64(detection / time.Microsecond),
		ReloadTX:              craftedReloadMicros, ReloadRX: craftedReloadMicros,
		QueuedFirst: craftedQueuedFirstMicros, QueuedSecond: craftedQueuedSecondMicros,
	}, appliedAt, queuedAt)
}

// hupCraftedTimers writes the peer timers into the daemon configuration,
// signals SIGHUP and returns the capture boundary taken before the signal.
func hupCraftedTimers(ctx context.Context, t *testing.T, baseline, timer, name string) uint64 {
	t.Helper()
	config, err := timerConfig(baseline, fuzzIP, timer, timer)
	if err != nil {
		t.Fatalf("crafted timer config: %v", err)
	}
	path := writeTimerArtifact(t, name+"-config", "yml", []byte(config))
	if copyErr := copyTimerConfig(ctx, "gobfd-interop", path, "/etc/gobfd/gobfd.yml"); copyErr != nil {
		t.Fatalf("crafted timer config copy: %v", copyErr)
	}
	boundary, err := lastFrameForFilter(ctx, timerTraffic(fuzzIP))
	if err != nil {
		t.Fatalf("crafted timer boundary: %v", err)
	}
	output, err := signalTimerReload(ctx)
	writeTimerArtifact(t, name+"-hup", "txt", []byte(fmt.Sprintf("output=%s\nerror=%v\n", output, err)))
	if err != nil {
		t.Fatalf("crafted timer HUP: %v", err)
	}
	return boundary
}

// waitCraftedFrame returns the first captured frame after boundary that matches filter.
func waitCraftedFrame(ctx context.Context, t *testing.T, filter string, boundary uint64) uint64 {
	t.Helper()
	frameCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	frame, err := pollUntil(frameCtx, 100*time.Millisecond, func(pollCtx context.Context) (uint64, bool, error) {
		// tshark -c limits frames read, not matches, so read the whole capture.
		rows, queryErr := tsharkFields(pollCtx, frameBoundary(filter, boundary), []string{"frame.number"}, 0)
		if queryErr != nil || len(rows) == 0 {
			return 0, false, queryErr
		}
		number, parseErr := strconv.ParseUint(rows[0][0], 10, 64)
		return number, parseErr == nil, parseErr
	})
	if err != nil {
		t.Fatalf("crafted frame %q after %d: %v", filter, boundary, err)
	}
	return frame
}

// waitCraftedGeneration waits until target is applied and no generation after
// previous failed, and returns the converged receipt time.
func waitCraftedGeneration(ctx context.Context, t *testing.T, previous, target uint64, name string) time.Time {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var lastRaw string
	appliedAt, err := pollUntil(waitCtx, 100*time.Millisecond, func(pollCtx context.Context) (time.Time, bool, error) {
		logs, logErr := projectContainerLogs(pollCtx, "gobfd-interop")
		if logs != "" {
			lastRaw = logs
		}
		if logErr != nil {
			return time.Time{}, false, logErr
		}
		receipts, parseErr := parseTimerReceipts(logs)
		if parseErr != nil {
			return time.Time{}, false, parseErr
		}
		for _, receipt := range receipts {
			if receipt.Desired <= previous {
				continue
			}
			if receipt.Failed != 0 || receipt.Base.Failed != 0 || receipt.Desired > target {
				return time.Time{}, false, fmt.Errorf("receipt %+v: %w", receipt, errTimerEvidence)
			}
			if receipt.Desired == target && receipt.Applied == target && !receipt.Stale && receipt.Pending == 0 {
				return receipt.PublishedAt, true, nil
			}
		}
		return time.Time{}, false, nil
	})
	if evidenceErr := retainTimerPollEvidence(ctx, name, target, lastRaw, err); evidenceErr != nil {
		t.Fatalf("crafted timer generation %d: %v", target, evidenceErr)
	}
	return appliedAt
}

// reloadCraftedTimers applies a daemon timer change under remote Demand and
// returns the converged receipt time.
func reloadCraftedTimers(ctx context.Context, t *testing.T, baseline string, previous uint64) time.Time {
	t.Helper()
	hupCraftedTimers(ctx, t, baseline, craftedReloadTimer, "crafted-demand")
	reloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var lastRaw string
	appliedAt, err := pollUntil(reloadCtx, 100*time.Millisecond, func(pollCtx context.Context) (time.Time, bool, error) {
		logs, logErr := projectContainerLogs(pollCtx, "gobfd-interop")
		if logs != "" {
			lastRaw = logs
		}
		if logErr != nil {
			return time.Time{}, false, logErr
		}
		receipts, parseErr := parseTimerReceipts(logs)
		if parseErr != nil {
			return time.Time{}, false, parseErr
		}
		applied, receiptErr := timerGenerationApplied(receipts, previous)
		return applied, !applied.IsZero(), receiptErr
	})
	if evidenceErr := retainTimerPollEvidence(ctx, "crafted-demand-after", previous+1, lastRaw, err); evidenceErr != nil {
		t.Fatalf("crafted timer generation %d under Demand: %v", previous+1, evidenceErr)
	}
	return appliedAt
}

func evaluateCraftedPeerRun(ctx context.Context, t *testing.T, plan craftedPlan, appliedAt, queuedAt time.Time) {
	t.Helper()
	fields := craftedFrameFields()
	rows, err := tsharkFields(ctx, timerTraffic(fuzzIP), fields, 0)
	var raw strings.Builder
	raw.WriteString(strings.Join(fields, "\t") + "\n")
	for _, row := range rows {
		raw.WriteString(strings.Join(row, "\t") + "\n")
	}
	writeTimerArtifact(t, "crafted-frames", "tsv", []byte(raw.String()))
	if err != nil {
		t.Fatalf("read crafted peer capture: %v", err)
	}
	frames, err := parseCraftedFrames(rows)
	var evidence craftedEvidence
	if err == nil {
		evidence, err = evaluateCraftedPeerCapture(frames, plan)
	}
	if err == nil {
		// The daemon log dates the Section 6.8.18 reset; the wire shows only the next send.
		var logs string
		logs, err = projectContainerLogs(ctx, "gobfd-interop")
		if err == nil {
			lastPeer := craftedNext(frames, 0, func(frame craftedFrame) bool {
				return frame.Number == evidence.Silence.LastPeer
			})
			evidence.Silence.ResetMicroseconds, err = craftedDetectionReset(logs, frames[lastPeer].Epoch,
				evidence.RX0.DetectionTimeMicroseconds)
		}
	}
	if err == nil {
		// The converged receipt must follow the peer Final that completed the Poll Sequence.
		peerFinal := craftedNext(frames, 0, func(frame craftedFrame) bool {
			return frame.Number == evidence.Demand.PeerFinal
		})
		queuedFinal := craftedNext(frames, 0, func(frame craftedFrame) bool {
			return frame.Number == evidence.Queued.SecondPeerFinal
		})
		for _, pair := range []struct {
			at    time.Time
			final craftedFrame
		}{{appliedAt, frames[peerFinal]}, {queuedAt, frames[queuedFinal]}} {
			if err == nil && !pair.at.After(pair.final.Epoch) {
				err = fmt.Errorf("applied=%s Final=%s: %w", pair.at.Format(time.RFC3339Nano),
					pair.final.Epoch.Format(time.RFC3339Nano), errTimerReceiptBeforeFinal)
			}
		}
	}
	summary, marshalErr := json.Marshal(struct {
		Plan      craftedPlan     `json:"plan"`
		AppliedAt time.Time       `json:"applied_at"`
		QueuedAt  time.Time       `json:"queued_applied_at"`
		Evidence  craftedEvidence `json:"evidence"`
		Error     string          `json:"error"`
	}{plan, appliedAt, queuedAt, evidence, fmt.Sprint(err)})
	if marshalErr != nil {
		t.Fatalf("crafted peer summary: %v", marshalErr)
	}
	writeTimerArtifact(t, "crafted-summary", "json", summary)
	t.Logf("crafted peer evidence: %s", summary)
	if err != nil {
		t.Fatalf("crafted peer wire evidence: %v", err)
	}
}
