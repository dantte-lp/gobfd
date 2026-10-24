//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

type detectFrame struct {
	timerFrame

	AuthPresent uint64 `json:"auth_present"`
	AuthType    uint64 `json:"auth_type"`
	Key         uint64 `json:"key"`
	Sequence    uint64 `json:"sequence"`
	Demand      uint64 `json:"demand"`
	UDPLength   uint64 `json:"udp_length"`
	SourcePort  uint64 `json:"source_port"`
	HasSequence bool   `json:"has_sequence"`
}

type detectPhase struct {
	timerPhase

	Before             [2]detectFrame   `json:"before"`
	PreviousMultiplier uint64           `json:"previous_multiplier"`
	Multiplier         uint64           `json:"multiplier"`
	Mode               timerReceiptMode `json:"mode"`
}

type detectWireResult struct {
	timerWireResult

	DerivedPeerDetectionMicroseconds uint64 `json:"derived_peer_detection_us"`
	DerivedPollDetectionMicroseconds uint64 `json:"derived_local_poll_detection_us"`
}

func detectFrameFields() []string {
	// A mandatory last column preserves preceding absent auth fields through TrimSpace.
	return append(timerFrameFields(), "bfd.flags.a", "bfd.auth.type", "bfd.auth.key",
		"bfd.auth.seq_num", "bfd.flags.d", "udp.length", "udp.srcport")
}

func parseDetectFrames(rows [][]string) ([]detectFrame, error) {
	width := len(timerFrameFields())
	baseRows := make([][]string, len(rows))
	for index, row := range rows {
		if len(row) != len(detectFrameFields()) {
			return nil, fmt.Errorf("DetectMult frame columns=%d: %w", len(row), errTimerEvidence)
		}
		baseRows[index] = row[:width]
	}
	base, err := parseTimerFrames(baseRows)
	if err != nil {
		return nil, err
	}
	frames := make([]detectFrame, len(rows))
	for index, frame := range base {
		decoded := detectFrame{timerFrame: frame, HasSequence: rows[index][width+3] != ""}
		fields := []*uint64{
			&decoded.AuthPresent, &decoded.AuthType, &decoded.Key,
			&decoded.Sequence, &decoded.Demand, &decoded.UDPLength, &decoded.SourcePort,
		}
		for field, value := range rows[index][width:] {
			if value == "" && field > 0 && field < 4 {
				continue
			}
			*fields[field], err = parseTimerField(value)
			if err != nil {
				return nil, fmt.Errorf("DetectMult field %s: %w", detectFrameFields()[width+field], err)
			}
		}
		if !validDetectAuth(decoded) || decoded.SourcePort == 0 || decoded.SourcePort > 65535 || decoded.Demand > 1 ||
			decoded.UDPLength < 32 || decoded.UDPLength > 65535 ||
			(decoded.AuthPresent == 1 && (rows[index][width+1] == "" || rows[index][width+2] == "")) ||
			(decoded.AuthPresent == 0 && (rows[index][width+1] != "" || rows[index][width+2] != "")) {
			return nil, fmt.Errorf("invalid DetectMult auth/transport fields: %w", errTimerEvidence)
		}
		frames[index] = decoded
	}
	return frames, nil
}

func validDetectAuth(frame detectFrame) bool {
	if frame.Key > 255 || frame.Sequence > 1<<32-1 {
		return false
	}
	switch frame.AuthType {
	case 0:
		return frame.AuthPresent == 0 && frame.Key == 0 && frame.Sequence == 0 && !frame.HasSequence
	case 1:
		return frame.AuthPresent == 1 && frame.Sequence == 0 && !frame.HasSequence
	case 2, 3, 4, 5:
		return frame.AuthPresent == 1 && frame.HasSequence
	default:
		return false
	}
}

func evaluateDetectPhase(frames []detectFrame, phase detectPhase) (detectWireResult, error) {
	previous := phase.Before
	if previous[0].SourcePort < 49152 || previous[0].SourcePort > 65535 ||
		previous[1].SourcePort == 0 || previous[1].SourcePort > 65535 ||
		previous[0].UDPLength < 32 || previous[0].UDPLength > 65535 ||
		previous[1].UDPLength < 32 || previous[1].UDPLength > 65535 ||
		previous[0].Demand != 0 || (phase.Mode == timerReceiptImmediate && previous[1].Demand != 0) ||
		(phase.Mode == timerReceiptPoll && previous[1].Demand != 1) ||
		!validDetectAuth(previous[0]) || !validDetectAuth(previous[1]) {
		return detectWireResult{}, fmt.Errorf("invalid DetectMult baseline: %w", errTimerEvidence)
	}
	base := make([]timerFrame, len(frames))
	for index, frame := range frames {
		direction := 0
		if frame.Source == phase.Peer {
			direction = 1
		}
		prior := previous[direction]
		if frame.SourcePort != prior.SourcePort || frame.UDPLength != prior.UDPLength ||
			frame.AuthPresent != prior.AuthPresent ||
			frame.AuthType != prior.AuthType || frame.Key != prior.Key || frame.Demand != prior.Demand ||
			!validDetectAuth(frame) {
			return detectWireResult{}, fmt.Errorf("DetectMult changed transport/auth identity: %w", errTimerEvidence)
		}
		if frame.HasSequence {
			// RFC 5880 sequence arithmetic is modulo 2^32; meticulous packets increment by one.
			delta := (frame.Sequence + 1<<32 - prior.Sequence) % (1 << 32)
			if delta >= 1<<31 || ((frame.AuthType == 3 || frame.AuthType == 5) && delta != 1) {
				return detectWireResult{}, fmt.Errorf("DetectMult auth sequence discontinuity: %w", errTimerEvidence)
			}
		}
		previous[direction] = frame
		base[index] = frame.timerFrame
	}
	return evaluateDetectTimeline(base, phase)
}

func evaluateDetectTimeline(frames []timerFrame, phase detectPhase) (detectWireResult, error) {
	var result detectWireResult
	if len(frames) == 0 || phase.Start >= phase.End || phase.Local == 0 || phase.Remote == 0 ||
		phase.AppliedAt.IsZero() || phase.Multiplier == 0 || phase.Multiplier > 255 ||
		phase.PreviousMultiplier == 0 || phase.PreviousMultiplier > 255 || phase.Multiplier == phase.PreviousMultiplier ||
		phase.TX == 0 || phase.TX > 1<<32-1 || phase.RX == 0 || phase.RX > 1<<32-1 ||
		phase.RemoteRX == 0 || phase.RemoteRX > 1<<32-1 || phase.Before[1].TX == 0 || phase.Before[1].TX > 1<<32-1 ||
		phase.Before[1].Multiplier == 0 || phase.Before[1].Multiplier > 255 ||
		phase.Before[0].Mine != uint64(phase.Local) || phase.Before[0].Yours != uint64(phase.Remote) ||
		phase.Before[1].Mine != uint64(phase.Remote) || phase.Before[1].Yours != uint64(phase.Local) ||
		phase.Before[0].TX != phase.TX || phase.Before[0].RX != phase.RX ||
		phase.Before[1].RX != phase.RemoteRX ||
		phase.Before[0].Source != gobfdIP || phase.Before[0].Destination != phase.Peer ||
		phase.Before[1].Source != phase.Peer || phase.Before[1].Destination != gobfdIP ||
		phase.Before[0].State != 3 || phase.Before[1].State != 3 ||
		phase.Before[0].Multiplier != phase.PreviousMultiplier ||
		(phase.Mode != timerReceiptPoll && phase.Mode != timerReceiptImmediate) {
		return result, fmt.Errorf("invalid DetectMult phase: %w", errTimerEvidence)
	}
	var previous timerFrame
	var intervals []int64
	seen, postReceipt := false, false
	for _, frame := range frames {
		local := frame.Source == gobfdIP && frame.Destination == phase.Peer
		remote := frame.Source == phase.Peer && frame.Destination == gobfdIP
		mine, yours := uint64(phase.Local), uint64(phase.Remote)
		if remote {
			mine, yours = yours, mine
		}
		if (!local && !remote) || frame.Number <= phase.Start || frame.Number > phase.End ||
			frame.State != 3 || frame.Mine != mine || frame.Yours != yours ||
			frame.Poll > 1 || frame.Final > 1 || frame.Poll+frame.Final > 1 {
			return result, fmt.Errorf("DetectMult phase state/identity: %w", errTimerEvidence)
		}
		if remote {
			if frame.TX != phase.Before[1].TX || frame.RX != phase.RemoteRX || frame.Multiplier != phase.Before[1].Multiplier {
				return result, fmt.Errorf("DetectMult changed remote tuple: %w", errTimerEvidence)
			}
			result.RemoteTX = frame.TX
			if frame.Final == 1 && result.Poll != 0 && result.Final == 0 {
				if !phase.AppliedAt.After(frame.Epoch) {
					return result, fmt.Errorf("DetectMult receipt before its Final: %w", errTimerReceiptBeforeFinal)
				}
				result.Final = frame.Number
			}
			continue
		}
		if frame.TX != phase.TX || frame.RX != phase.RX ||
			(frame.Multiplier != phase.Multiplier && (seen || frame.Multiplier != phase.PreviousMultiplier)) {
			return result, fmt.Errorf("DetectMult changed local tuple: %w", errTimerEvidence)
		}
		if phase.Mode == timerReceiptImmediate && !frame.Epoch.Before(phase.AppliedAt) {
			interval := int64(max(phase.TX, phase.RemoteRX))
			age := frame.Epoch.Sub(phase.AppliedAt).Microseconds()
			// Capture may observe one already-sent packet during the bounded egress window.
			if frame.Multiplier != phase.Multiplier && (postReceipt || age > timerSlack(interval)) ||
				!seen && frame.Multiplier == phase.Multiplier && age > interval+timerSlack(interval) {
				return result, fmt.Errorf("DetectMult missed next Control bound: %w", errTimerEvidence)
			}
			postReceipt = true
		}
		seen = seen || frame.Multiplier == phase.Multiplier
		if frame.Poll == 1 {
			if phase.Mode != timerReceiptPoll || frame.Multiplier != phase.Multiplier || !frame.Epoch.Before(phase.AppliedAt) {
				return result, fmt.Errorf("unexpected DetectMult Poll: %w", errTimerEvidence)
			}
			if result.Poll == 0 {
				result.Poll = frame.Number
			}
		}
		if phase.Mode == timerReceiptPoll {
			if frame.Poll == 0 && frame.Final == 0 {
				return result, fmt.Errorf("periodic DetectMult transmission under Demand: %w", errTimerEvidence)
			}
			continue
		}
		if frame.Final != 0 || frame.Multiplier != phase.Multiplier || !frame.Epoch.After(phase.AppliedAt) {
			previous, intervals = timerFrame{}, nil
			continue
		}
		if previous.Number != 0 {
			intervals = append(intervals, frame.Epoch.Sub(previous.Epoch).Microseconds())
		}
		previous = frame
	}
	if !seen || result.RemoteTX == 0 {
		return result, fmt.Errorf("missing DetectMult traffic: %w", errTimerEvidence)
	}
	result.DetectionMicroseconds = phase.Before[1].Multiplier * max(phase.RX, result.RemoteTX)
	if phase.Mode == timerReceiptPoll {
		result.DerivedPollDetectionMicroseconds = phase.Multiplier * max(phase.TX, phase.RemoteRX)
		quiet := frames[len(frames)-1].Epoch.Sub(phase.AppliedAt).Microseconds()
		if result.Poll == 0 || result.Final <= result.Poll || quiet < int64(result.DerivedPollDetectionMicroseconds) {
			return result, fmt.Errorf("incomplete DetectMult Poll/Final or quiet window: %w", errTimerEvidence)
		}
		return result, nil
	}
	result.DerivedPeerDetectionMicroseconds = phase.Multiplier * max(phase.TX, phase.RemoteRX)
	mean, err := checkJitteredIntervals(intervals, int64(max(phase.TX, phase.RemoteRX)), phase.Multiplier)
	result.Intervals, result.MeanMicroseconds = len(intervals), mean
	return result, err
}

func detectConfig(baseline, peer string, multiplier uint64) (string, error) {
	if multiplier == 0 || multiplier > 255 {
		return "", fmt.Errorf("invalid DetectMult: %w", errTimerEvidence)
	}
	start, end, err := timerPeerBlock(baseline, peer)
	if err != nil {
		return "", err
	}
	old := "    detect_mult: 3\n"
	block := baseline[start:end]
	if strings.Count(block, old) != 1 {
		return "", fmt.Errorf("unique baseline DetectMult: %w", errTimerEvidence)
	}
	block = strings.Replace(block, old, "    detect_mult: "+strconv.FormatUint(multiplier, 10)+"\n", 1)
	return baseline[:start] + block + baseline[end:], nil
}

func craftedDetectPoll(frame craftedFrame, phases []detectPhase) bool {
	for _, phase := range phases {
		if frame.Number > phase.Start && frame.Number <= phase.End {
			return true
		}
	}
	return false
}

func evaluateCraftedDetect(frames []craftedFrame, after, before uint64, plan craftedPlan) error {
	last, multiplier := after, uint64(3)
	for _, phase := range plan.DetectUpdates {
		if phase.Start <= after || phase.Start < last || phase.End >= before || phase.Mode != timerReceiptPoll ||
			phase.Peer != fuzzIP || phase.TX != plan.ReloadTX || phase.RX != plan.ReloadRX ||
			phase.PreviousMultiplier != multiplier {
			return fmt.Errorf("invalid crafted DetectMult window: %w", errCraftedEvidence)
		}
		var selected []timerFrame
		var baseline [2]timerFrame
		for _, frame := range frames {
			if frame.Number <= phase.Start {
				direction := 0
				if frame.fromPeer() {
					direction = 1
				}
				baseline[direction] = frame.timerFrame
			} else if frame.Number <= phase.End {
				if frame.Demand != 0 && !frame.fromPeer() || frame.Demand != 1 && frame.fromPeer() {
					return fmt.Errorf("DetectMult window crossed Demand boundary: %w", errCraftedEvidence)
				}
				selected = append(selected, frame.timerFrame)
			}
		}
		if baseline[0] != phase.Before[0].timerFrame || baseline[1] != phase.Before[1].timerFrame {
			return fmt.Errorf("DetectMult window baseline disagrees with capture: %w", errCraftedEvidence)
		}
		if _, err := evaluateDetectTimeline(selected, phase); err != nil {
			return err
		}
		last, multiplier = phase.End, phase.Multiplier
	}
	if multiplier != 3 {
		return fmt.Errorf("DetectMult not restored before Demand clear: %w", errCraftedEvidence)
	}
	return nil
}

func readDetectFrames(ctx context.Context, peer string, after uint64) ([]detectFrame, error) {
	rows, err := tsharkFields(ctx, frameBoundary(timerTraffic(peer), after), detectFrameFields(), 0)
	if err != nil {
		return nil, err
	}
	return parseDetectFrames(rows)
}

func captureFieldRegistry(ctx context.Context, fields []string,
	query func(context.Context, string) (string, error),
) (string, error) {
	var registry strings.Builder
	// Scope metadata before exec buffering; the full registry exhausts bounded race runners.
	for _, scope := range []string{"frame.", "ip.", "bfd.", "udp."} {
		part, err := query(ctx, "fields,"+scope)
		if err != nil {
			return registry.String(), fmt.Errorf("capture registry %s: %w", scope, err)
		}
		registry.WriteString(part)
		registry.WriteByte('\n')
	}
	result := registry.String()
	for _, field := range fields {
		if !strings.HasPrefix(result, field+"\t") && !strings.Contains(result, "\n"+field+"\t") {
			return result, fmt.Errorf("capture field %s unavailable: %w", field, errTimerEvidence)
		}
	}
	return result, nil
}

func requireCaptureFields(ctx context.Context, t *testing.T, fields []string) {
	t.Helper()
	registry, err := captureFieldRegistry(ctx, fields, func(queryCtx context.Context, scope string) (string, error) {
		return projectContainerCommand(queryCtx, "tshark-interop", "exec", "tshark", "-G", scope)
	})
	writeTimerArtifact(t, "capture-field-registry", "tsv", []byte(registry))
	if err != nil {
		t.Fatalf("capture field registry: %v", err)
	}
}

// runDetectGeneration reuses the saved config, daemon control and namespace capture.
func runDetectGeneration(ctx context.Context, t *testing.T, baseline, peer, name string,
	multiplier uint64, mode timerReceiptMode,
) detectPhase {
	t.Helper()
	phaseCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	before, counters := timerSnapshot(phaseCtx, t, peer, name+"-before")
	initial, err := readDetectFrames(phaseCtx, peer, 0)
	if err != nil {
		t.Fatalf("DetectMult baseline capture: %v", err)
	}
	phase := detectPhase{
		Peer: peer, Local: before.LocalDiscriminator,
		Remote:             before.RemoteDiscriminator,
		PreviousMultiplier: uint64(before.DetectMultiplier), Multiplier: multiplier, Mode: mode,
	}
	for _, frame := range initial {
		direction := 0
		if frame.Source == peer {
			direction = 1
		}
		phase.Before[direction], phase.Start = frame, frame.Number
	}
	if before.LocalState != "Up" || before.RemoteState != "Up" || phase.PreviousMultiplier == 0 ||
		phase.Before[0].Multiplier != phase.PreviousMultiplier || phase.Before[1].Number == 0 ||
		(before.PaddedPduSize != 0 && phase.Before[0].UDPLength != uint64(before.PaddedPduSize)+8) {
		t.Fatalf("DetectMult requires stable established baseline: %+v", before)
	}
	phase.TX, phase.RX, phase.RemoteRX = phase.Before[0].TX, phase.Before[0].RX, phase.Before[1].RX
	current := readTimerReceipts(phaseCtx, t, name+"-before")
	previous := current[len(current)-1]
	if previous.Stale || previous.Desired == 0 || previous.Desired != previous.Applied || previous.Failed != 0 {
		t.Fatalf("DetectMult requires converged generation: %+v", previous)
	}
	candidate, err := detectConfig(baseline, peer, multiplier)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTimerArtifact(t, name+"-config", "yml", []byte(candidate))
	if copyErr := copyTimerConfig(phaseCtx, "gobfd-interop", path, "/etc/gobfd/gobfd.yml"); copyErr != nil {
		t.Fatal(copyErr)
	}
	if _, reloadErr := signalTimerReload(phaseCtx); reloadErr != nil {
		t.Fatal(reloadErr)
	}
	var lastRaw string
	phase.AppliedAt, err = pollUntil(phaseCtx, 100*time.Millisecond,
		func(pollCtx context.Context) (time.Time, bool, error) {
			logs, logErr := projectContainerLogs(pollCtx, "gobfd-interop")
			lastRaw = logs
			if logErr != nil {
				return time.Time{}, false, logErr
			}
			receipts, parseErr := parseTimerReceipts(logs)
			if parseErr != nil {
				return time.Time{}, false, parseErr
			}
			applied, receiptErr := timerGenerationApplied(receipts, previous.Applied, mode)
			return applied, !applied.IsZero(), receiptErr
		})
	evidenceErr := retainTimerPollEvidence(phaseCtx, name+"-after", previous.Applied+1, lastRaw, err)
	if evidenceErr != nil {
		t.Fatal(evidenceErr)
	}
	if mode == timerReceiptPoll {
		quiet := time.Duration(craftedQuietMultiple*max(phase.TX, phase.RemoteRX)) * time.Microsecond
		if waitErr := waitCrafted(phaseCtx, quiet); waitErr != nil {
			t.Fatal(waitErr)
		}
	}
	var frames []detectFrame
	var result detectWireResult
	var wireErr error
	_, err = pollUntil(phaseCtx, 200*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
		var queryErr error
		frames, queryErr = readDetectFrames(pollCtx, peer, phase.Start)
		if queryErr != nil {
			return false, false, queryErr
		}
		if len(frames) == 0 {
			return false, false, nil
		}
		phase.End = frames[len(frames)-1].Number
		result, queryErr = evaluateDetectPhase(frames, phase)
		wireErr = queryErr
		return queryErr == nil, queryErr == nil, nil
	})
	err = errors.Join(err, wireErr)
	evidence, marshalErr := json.Marshal(struct {
		Phase  detectPhase      `json:"phase"`
		Frames []detectFrame    `json:"frames"`
		Result detectWireResult `json:"result"`
		Error  string           `json:"error"`
	}{phase, frames, result, fmt.Sprint(err)})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	writeTimerArtifact(t, name+"-wire", "json", evidence)
	if err != nil {
		t.Fatalf("DetectMult wire evidence: %v", err)
	}
	after, afterCounters := timerSnapshot(phaseCtx, t, peer, name+"-after")
	localDetection, parseErr := time.ParseDuration(after.DetectionTime)
	if parseErr != nil || localDetection != time.Duration(result.DetectionMicroseconds)*time.Microsecond ||
		after.LocalState != "Up" || after.RemoteState != "Up" || after.LocalDiscriminator != before.LocalDiscriminator ||
		after.RemoteDiscriminator != before.RemoteDiscriminator || after.DetectMultiplier != uint32(multiplier) ||
		after.PaddedPduSize != before.PaddedPduSize ||
		after.DesiredMinTx != before.DesiredMinTx || after.RequiredMinRx != before.RequiredMinRx ||
		afterCounters != counters {
		t.Fatalf("DetectMult changed identity, timers, auth/state counters or receive detection: before=%+v/%v after=%+v/%v",
			before, counters, after, afterCounters)
	}
	return phase
}
