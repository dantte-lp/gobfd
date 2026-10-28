//go:build interop_testcontainers

package interop_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type ttlFrame struct {
	paddingFrame

	TTL             uint64 `json:"ttl"`
	DestinationPort uint64 `json:"destination_port"`
}

func ttlFrameFields() []string { return append(paddingFrameFields(), "ip.ttl", "udp.dstport") }

func parseTTLFrames(rows [][]string) ([]ttlFrame, error) {
	width := len(paddingFrameFields())
	baseRows := make([][]string, len(rows))
	for index, row := range rows {
		if len(row) != len(ttlFrameFields()) {
			return nil, fmt.Errorf("TTL capture columns: %w", errTimerEvidence)
		}
		baseRows[index] = row[:width]
	}
	base, err := parsePaddingFrames(baseRows)
	if err != nil {
		return nil, err
	}
	frames := make([]ttlFrame, len(base))
	for index, frame := range base {
		decoded := ttlFrame{paddingFrame: frame}
		decoded.TTL, err = parseTimerField(rows[index][width])
		if err != nil {
			return nil, err
		}
		decoded.DestinationPort, err = parseTimerField(rows[index][width+1])
		if err != nil {
			return nil, err
		}
		if decoded.TTL == 0 || decoded.TTL > 255 || decoded.DestinationPort != 4784 {
			return nil, fmt.Errorf("invalid multihop TTL/port: %w", errTimerEvidence)
		}
		frames[index] = decoded
	}
	return frames, nil
}

type ttlPhase struct {
	Before         paddingCheckpoint `json:"before"`
	Peer           string            `json:"peer"`
	Floor          uint64            `json:"floor"`
	Demand         uint64            `json:"demand"`
	Start          uint64            `json:"start"`
	End            uint64            `json:"end"`
	CountersBefore [3]uint64         `json:"counters_before"`
	CountersAfter  [3]uint64         `json:"counters_after"`
}

type ttlResult struct {
	Replay                uint64 `json:"replay"`
	Low                   uint64 `json:"low"`
	Good                  uint64 `json:"good"`
	Final                 uint64 `json:"final"`
	DetectionMicroseconds uint64 `json:"detection_microseconds"`
}

type ttlPeerEvent struct {
	Time         time.Time
	Event, Value string
}

func ttlPeerEvents(input string) ([]ttlPeerEvent, error) {
	var events []ttlPeerEvent
	for line := range strings.SplitSeq(strings.TrimSpace(input), "\n") {
		if line == "" {
			continue
		}
		fields, err := timerJSONObject([]byte(line), "time", "event", "value")
		if err != nil {
			return nil, err
		}
		var event ttlPeerEvent
		event.Time, err = timerPublicationTime(fields["time"])
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(fields["event"], &event.Event); err != nil {
			return nil, fmt.Errorf("peer event: %w", err)
		}
		if err := json.Unmarshal(fields["value"], &event.Value); err != nil {
			return nil, fmt.Errorf("peer event value: %w", err)
		}
		events = append(events, event)
	}
	return events, nil
}

func ttlConfig(baseline string, hops uint64) (string, error) {
	start, end, err := timerPeerBlock(baseline, fuzzIP)
	if err != nil {
		return "", err
	}
	block := baseline[start:end]
	old := "    multihop_max_hops: 1\n"
	if hops < 1 || hops > 254 || strings.Count(block, old) != 1 ||
		strings.Count(block, "    multihop_max_hops:") != 1 || strings.Count(block, "    type: multi_hop\n") != 1 {
		return "", fmt.Errorf("multihop baseline/hop policy: %w", errTimerEvidence)
	}
	block = strings.Replace(block, old, "    multihop_max_hops: "+strconv.FormatUint(hops, 10)+"\n", 1)
	return baseline[:start] + block + baseline[end:], nil
}

func advanceTTLDemand(frames []ttlFrame, before paddingCheckpoint, keys bfd.AuthKeyStore) (paddingCheckpoint, error) {
	work := before
	var poll, final uint64
	inflight := 0
	for _, frame := range frames {
		direction := 0
		if frame.Source == fuzzIP {
			direction = 1
		}
		prior := work.Frames[direction]
		if frame.Number <= work.Cursor || frame.Epoch.Before(prior.Epoch) || frame.TTL != 255 {
			return before, fmt.Errorf("invalid Demand capture order/TTL: %w", errTimerEvidence)
		}
		if direction == 1 && frame.Demand != prior.Demand {
			if poll != 0 || prior.Demand != 0 || frame.Demand != 1 || frame.Poll != 1 {
				return before, fmt.Errorf("invalid Demand entry: %w", errTimerEvidence)
			}
			prior.Demand, poll = 1, frame.Number
		} else if frame.Poll != 0 {
			return before, fmt.Errorf("unrelated Demand Poll: %w", errTimerEvidence)
		}
		state, err := verifyTTLControl(frame, prior, work.Auth[direction], keys)
		if err != nil {
			return before, err
		}
		if direction == 0 && poll != 0 {
			if frame.Final == 1 {
				if final != 0 {
					return before, fmt.Errorf("duplicate Demand Final: %w", errTimerEvidence)
				}
				final = frame.Number
			} else {
				inflight++
				if final != 0 || inflight > 1 {
					return before, fmt.Errorf("periodic TX after Demand: %w", errTimerEvidence)
				}
			}
		} else if frame.Final != 0 {
			return before, fmt.Errorf("unexpected Demand Final: %w", errTimerEvidence)
		}
		work.Frames[direction], work.Auth[direction], work.Cursor = frame.paddingFrame, state, frame.Number
	}
	if poll == 0 || final == 0 {
		return before, fmt.Errorf("incomplete Demand exchange: %w", errTimerEvidence)
	}
	return work, nil
}

func verifyTTLControl(frame ttlFrame, prior paddingFrame, state bfd.AuthState,
	keys bfd.AuthKeyStore,
) (bfd.AuthState, error) {
	if frame.Source != prior.Source || frame.Destination != prior.Destination || frame.State != 3 ||
		frame.Mine != prior.Mine || frame.Yours != prior.Yours || frame.TX != prior.TX || frame.RX != prior.RX ||
		frame.Multiplier != prior.Multiplier || frame.DestinationPort != 4784 || frame.TTL == 0 || frame.TTL > 255 ||
		frame.Poll > 1 || frame.Final > 1 || frame.Poll+frame.Final > 1 ||
		(frame.Source == gobfdIP && (frame.TTL != 255 || frame.Poll != 0)) {
		return state, fmt.Errorf("TTL changed Control tuple/identity: %w", errTimerEvidence)
	}
	if err := checkControlContinuity(prior.detectFrame, frame.detectFrame); err != nil {
		return state, err
	}
	if err := verifyPaddingFrame(frame.paddingFrame, uint64(len(prior.Payload)), &state, keys); err != nil {
		return state, err
	}
	return state, nil
}

func evaluateTTLPhase(frames []ttlFrame, phase ttlPhase, keys bfd.AuthKeyStore) (ttlResult, paddingCheckpoint, error) {
	var result ttlResult
	before := phase.Before.Frames
	if len(frames) == 0 || phase.Start != phase.Before.Cursor || phase.Start >= phase.End ||
		phase.End != frames[len(frames)-1].Number || phase.Floor < 2 || phase.Floor > 254 || phase.Demand > 1 ||
		before[0].Source != gobfdIP || before[0].Destination != phase.Peer ||
		before[1].Source != phase.Peer || before[1].Destination != gobfdIP ||
		before[0].Demand != 0 || before[1].Demand != phase.Demand ||
		before[0].SourcePort < 49152 || before[0].SourcePort > 65535 || before[1].SourcePort != 49152 ||
		max(before[0].Number, before[1].Number) != phase.Start ||
		phase.CountersAfter[0] <= phase.CountersBefore[0] || phase.CountersAfter[0]-phase.CountersBefore[0] != 1 ||
		phase.CountersAfter[1] != phase.CountersBefore[1] || phase.CountersAfter[2] <= phase.CountersBefore[2] ||
		phase.CountersAfter[2]-phase.CountersBefore[2] != 1 {
		return result, phase.Before, fmt.Errorf("invalid TTL phase/counters: %w", errTimerEvidence)
	}
	for direction, frame := range before {
		state := phase.Before.Auth[direction]
		if frame.State != 3 || frame.TX == 0 || frame.RX == 0 || frame.Multiplier == 0 || frame.Multiplier > 255 ||
			frame.Mine == 0 || frame.Yours != before[1-direction].Mine || frame.AuthType != 5 || frame.Key != 7 ||
			!frame.HasSequence || !state.AuthSeqKnown || state.Type != bfd.AuthTypeMeticulousKeyedSHA1 ||
			uint64(state.RcvAuthSeq) != frame.Sequence {
			return result, phase.Before, fmt.Errorf("invalid TTL checkpoint: %w", errTimerEvidence)
		}
	}
	result.DetectionMicroseconds = before[1].Multiplier * max(before[0].RX, before[1].TX)
	work := phase.Before
	var low ttlFrame
	lastNumber := phase.Start
	lastTime := before[0].Epoch
	if before[1].Epoch.After(lastTime) {
		lastTime = before[1].Epoch
	}
	for _, frame := range frames {
		local := frame.Source == gobfdIP && frame.Destination == phase.Peer
		remote := frame.Source == phase.Peer && frame.Destination == gobfdIP
		direction := 0
		if remote {
			direction = 1
		}
		prior := work.Frames[direction]
		if (!local && !remote) || frame.Number <= lastNumber || frame.Number > phase.End || frame.Epoch.Before(lastTime) ||
			(local && phase.Demand == 1 && frame.Final == 0) {
			return result, phase.Before, fmt.Errorf("TTL changed Control tuple/identity: %w", errTimerEvidence)
		}
		replay := remote && result.Replay == 0 && result.Low == 0 && frame.TTL == phase.Floor &&
			bytes.Equal(frame.Payload, prior.Payload)
		state := work.Auth[direction]
		if replay {
			// Validate the old signed payload without advancing the accepted observer checkpoint.
			state.AuthSeqKnown = false
			prior.Sequence = (frame.Sequence + 1<<32 - 1) % (1 << 32)
		}
		state, err := verifyTTLControl(frame, prior, state, keys)
		if err != nil {
			return result, phase.Before, err
		}
		lastNumber, lastTime = frame.Number, frame.Epoch
		if replay {
			result.Replay = frame.Number
			continue
		}
		if remote && frame.TTL < phase.Floor {
			if result.Replay == 0 || result.Low != 0 || frame.TTL+1 != phase.Floor || frame.Poll != 1 || frame.Final != 0 {
				return result, phase.Before, fmt.Errorf("invalid below-floor Poll: %w", errTimerEvidence)
			}
			low, result.Low = frame, frame.Number
			// A transport rejection must not advance the accepted receive authentication state.
			continue
		}
		if remote {
			if result.Low != 0 {
				if result.Good != 0 || frame.TTL != phase.Floor || !bytes.Equal(frame.Payload, low.Payload) ||
					!frame.Epoch.After(low.Epoch) ||
					frame.Epoch.Sub(prior.Epoch).Microseconds() >= int64(result.DetectionMicroseconds) {
					return result, phase.Before, fmt.Errorf("missing same-sequence boundary retry: %w", errTimerEvidence)
				}
				result.Good = frame.Number
			} else if frame.Poll != 0 || frame.Final != 0 {
				return result, phase.Before, fmt.Errorf("unrelated peer Poll/Final: %w", errTimerEvidence)
			}
		} else if frame.Final != 0 {
			if result.Good == 0 || result.Final != 0 {
				return result, phase.Before, fmt.Errorf("Final without accepted boundary Poll: %w", errTimerEvidence)
			}
			result.Final = frame.Number
		}
		work.Auth[direction], work.Frames[direction] = state, frame.paddingFrame
	}
	if result.Replay == 0 || result.Low == 0 || result.Good == 0 || result.Final == 0 {
		return result, phase.Before, fmt.Errorf("incomplete TTL boundary evidence: %w", errTimerEvidence)
	}
	work.Cursor = phase.End
	return result, work, nil
}
