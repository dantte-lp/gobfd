//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

type paddingFrame struct {
	detectFrame

	DF      uint64 `json:"df"`
	Payload []byte `json:"payload"`
}

type paddingCheckpoint struct {
	Frames [2]paddingFrame  `json:"frames"`
	Auth   [2]bfd.AuthState `json:"-"`
	Cursor uint64           `json:"cursor"`
}

type paddingPhase struct {
	timerPhase

	Before       paddingCheckpoint `json:"before"`
	PreviousSize uint64            `json:"previous_size"`
	Size         uint64            `json:"size"`
	RejectedAt   time.Time         `json:"rejected_at"`
}

func paddingGenerationUnchanged(receipts []timerReceipt, before timerReceipt) error {
	for _, receipt := range receipts {
		if receipt.Desired != before.Desired || receipt.Applied != before.Applied || receipt.Stale ||
			receipt.Pending != 0 || receipt.Failed != 0 || receipt.Base != (timerBaseReceipt{}) {
			return fmt.Errorf("rejected candidate changed accepted generation: %w", errTimerEvidence)
		}
	}
	return nil
}

func paddingReadiness(ctx context.Context) (string, error) {
	return projectContainerCommand(ctx, "gobfd-interop", "exec", "/tmp/gobfd-interop.test",
		"-test.v", "-test.count=1", "-test.run=^TestInteropReadinessProbe$")
}

func requirePaddingReadiness(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := paddingReadiness(probeCtx)
	writeTimerArtifact(t, name+"-readiness", "txt", []byte(fmt.Sprintf("%s\nerror=%v\n", output, err)))
	if err != nil {
		t.Fatalf("readiness subprocess: %v", err)
	}
}

// TestInteropReadinessProbe runs only as an explicit child inside the owned daemon container.
func TestInteropReadinessProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("unix:///run/gobfd/gobfd.sock", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("readiness client: %v", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close readiness client: %v", closeErr)
		}
	}()
	for _, service := range []string{"", "bfd.v1.BfdService"} {
		response, checkErr := grpc_health_v1.NewHealthClient(conn).Check(ctx,
			&grpc_health_v1.HealthCheckRequest{Service: service})
		if checkErr != nil {
			t.Fatalf("readiness %q: %v", service, checkErr)
		}
		t.Logf("service=%q status=%s", service, response.GetStatus())
		if response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
			t.Fatalf("readiness %q=%s", service, response.GetStatus())
		}
	}
}

type paddingObserver struct {
	Peer       string
	Checkpoint paddingCheckpoint
	Keys       bfd.AuthKeyStore
}

func readPaddingFrames(ctx context.Context, peer string, after uint64) ([]paddingFrame, error) {
	rows, err := tsharkFields(ctx, frameBoundary(timerTraffic(peer), after), paddingFrameFields(), 0)
	if err != nil {
		return nil, err
	}
	return parsePaddingFrames(rows)
}

func newPaddingObserver(ctx context.Context, t *testing.T, baseline []byte, peer string,
	authType bfd.AuthType,
) *paddingObserver {
	t.Helper()
	path := writeTimerArtifact(t, "padding-baseline-"+peer, "yml", baseline)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	observer := &paddingObserver{Peer: peer}
	found := false
	for _, spec := range cfg.Sessions {
		if spec.Peer != peer {
			continue
		}
		found = true
		if authType == bfd.AuthTypeNone {
			if spec.Auth.Type != "" && spec.Auth.Type != "none" {
				t.Fatal("unexpected baseline authentication")
			}
		} else {
			if spec.Auth.KeyID != 7 {
				t.Fatal("unexpected fixture key ID")
			}
			observer.Keys, err = bfd.NewStaticAuthKeyStore(bfd.AuthKey{ID: 7, Type: authType, Secret: []byte(spec.Auth.Secret)})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("padding baseline peer missing")
	}
	frames, err := readPaddingFrames(ctx, peer, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		direction := 0
		if frame.Source == peer {
			direction = 1
		}
		observer.Checkpoint.Frames[direction] = frame
		observer.Checkpoint.Cursor = frame.Number
	}
	for direction, frame := range observer.Checkpoint.Frames {
		observer.Checkpoint.Auth[direction].Type = authType
		if frame.Number == 0 || frame.State != 3 || frame.AuthType != uint64(authType) {
			t.Fatalf("invalid padding baseline: %+v", frame)
		}
		if verifyErr := verifyPaddingFrame(frame, uint64(len(frame.Payload)),
			&observer.Checkpoint.Auth[direction], observer.Keys); verifyErr != nil {
			t.Fatal(verifyErr)
		}
	}
	return observer
}

func rejectedPaddingCandidate(ctx context.Context, t *testing.T, candidate, name string) time.Time {
	t.Helper()
	logs, err := projectContainerLogs(ctx, "gobfd-interop")
	if err != nil {
		t.Fatal(err)
	}
	before, err := parseTimerReceipts(logs)
	if err != nil {
		t.Fatal(err)
	}
	previous := before[len(before)-1]
	if previous.Stale || previous.Desired == 0 || previous.Desired != previous.Applied ||
		previous.Pending != 0 || previous.Failed != 0 {
		t.Fatalf("rejection baseline not converged: %+v", previous)
	}
	events, err := paddingRejections(logs)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTimerArtifact(t, name+"-config", "yml", []byte(candidate))
	if copyErr := copyTimerConfig(ctx, "gobfd-interop", path, "/etc/gobfd/gobfd.yml"); copyErr != nil {
		t.Fatal(copyErr)
	}
	if _, signalErr := signalTimerReload(ctx); signalErr != nil {
		t.Fatal(signalErr)
	}
	var lastRaw string
	at, err := pollUntil(ctx, 100*time.Millisecond, func(pollCtx context.Context) (time.Time, bool, error) {
		var logErr error
		lastRaw, logErr = projectContainerLogs(pollCtx, "gobfd-interop")
		if logErr != nil {
			return time.Time{}, false, logErr
		}
		after, parseErr := parseTimerReceipts(lastRaw)
		if parseErr != nil {
			return time.Time{}, false, parseErr
		}
		if len(after) < len(before) {
			return time.Time{}, false, errTimerEvidence
		}
		if generationErr := paddingGenerationUnchanged(after[len(before):], previous); generationErr != nil {
			return time.Time{}, false, generationErr
		}
		rejections, parseErr := paddingRejections(lastRaw)
		if parseErr != nil {
			return time.Time{}, false, parseErr
		}
		if len(rejections) == len(events) {
			return time.Time{}, false, nil
		}
		if len(rejections) != len(events)+1 {
			return time.Time{}, false, errTimerEvidence
		}
		return rejections[len(events)], true, nil
	})
	if evidenceErr := retainTimerPollEvidence(ctx, name+"-rejected", previous.Applied, lastRaw, err); evidenceErr != nil {
		t.Fatal(evidenceErr)
	}
	return at
}

func runPaddingCandidate(ctx context.Context, t *testing.T, candidate, name string,
	size uint64, rejected bool, observers ...*paddingObserver,
) {
	t.Helper()
	phaseCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	requirePaddingReadiness(phaseCtx, t, name+"-before")
	before := make([]sessionState, len(observers))
	counters := make([][2]uint64, len(observers))
	for index, observer := range observers {
		before[index], counters[index] = timerSnapshot(phaseCtx, t, observer.Peer, name+"-"+observer.Peer+"-before")
	}
	receipts := readTimerReceipts(phaseCtx, t, name+"-candidate-before")
	previous := receipts[len(receipts)-1]
	var applied, rejectedAt time.Time
	if rejected {
		rejectedAt = rejectedPaddingCandidate(phaseCtx, t, candidate, name)
	} else {
		applied = reloadControlCandidate(phaseCtx, t, candidate, name, timerReceiptImmediate)
	}
	for _, observer := range observers {
		if observer.Checkpoint.Frames[1].Demand != 1 {
			continue
		}
		id, err := resolveProjectContainerID(phaseCtx, "scapy-interop")
		if err != nil {
			t.Fatal(err)
		}
		output, err := podmanCommand(phaseCtx, "kill", "--signal", "USR2", id)
		writeTimerArtifact(t, name+"-probe", "txt", []byte(fmt.Sprintf("%s\nerror=%v\n", output, err)))
		if err != nil {
			t.Fatalf("current-state Poll probe: %v", err)
		}
	}
	for index, observer := range observers {
		checkpoint := observer.Checkpoint
		local, remote := checkpoint.Frames[0], checkpoint.Frames[1]
		phase := paddingPhase{
			Peer: observer.Peer, Local: before[index].LocalDiscriminator, Remote: before[index].RemoteDiscriminator,
			TX: local.TX, RX: local.RX, RemoteRX: remote.RX, Start: checkpoint.Cursor, AppliedAt: applied,
			Before: checkpoint, PreviousSize: uint64(len(local.Payload)), Size: size, RejectedAt: rejectedAt,
		}
		if rejected {
			phase.Size = phase.PreviousSize
		}
		var frames []paddingFrame
		var result timerWireResult
		var next paddingCheckpoint
		var wireErr error
		_, err := pollUntil(phaseCtx, 200*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
			var queryErr error
			frames, queryErr = readPaddingFrames(pollCtx, observer.Peer, checkpoint.Cursor)
			if queryErr != nil {
				return false, false, queryErr
			}
			if len(frames) == 0 {
				return false, false, nil
			}
			phase.End = frames[len(frames)-1].Number
			result, next, wireErr = evaluatePaddingPhase(frames, phase, observer.Keys)
			return wireErr == nil, wireErr == nil, nil
		})
		err = errors.Join(err, wireErr)
		evidence, marshalErr := json.Marshal(struct {
			Phase  paddingPhase    `json:"phase"`
			Frames []paddingFrame  `json:"frames"`
			Result timerWireResult `json:"result"`
			Error  string          `json:"error"`
		}{phase, frames, result, fmt.Sprint(err)})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		writeTimerArtifact(t, name+"-"+observer.Peer+"-wire", "json", evidence)
		if err != nil {
			t.Fatalf("padding wire evidence: %v", err)
		}
		after, afterCounters := timerSnapshot(phaseCtx, t, observer.Peer, name+"-"+observer.Peer+"-after")
		want := before[index]
		if !rejected {
			want.PaddedPduSize = uint32(size)
		}
		if after != want || after.LocalState != "Up" || after.RemoteState != "Up" || afterCounters != counters[index] {
			t.Fatalf("padding changed session state/counters: before=%+v/%v after=%+v/%v",
				want, counters[index], after, afterCounters)
		}
		observer.Checkpoint = next
	}
	if rejected {
		after := readTimerReceipts(phaseCtx, t, name+"-candidate-after")
		if len(after) < len(receipts) {
			t.Fatal("lost accepted generation history")
		}
		if err := paddingGenerationUnchanged(after[len(receipts):], previous); err != nil {
			t.Fatal(err)
		}
	}
	requirePaddingReadiness(phaseCtx, t, name+"-after")
}

func evaluatePaddingPhase(frames []paddingFrame, phase paddingPhase, keys bfd.AuthKeyStore,
) (timerWireResult, paddingCheckpoint, error) {
	var result timerWireResult
	before := phase.Before.Frames
	eventAt := phase.AppliedAt
	rejected := !phase.RejectedAt.IsZero()
	if rejected {
		eventAt = phase.RejectedAt
	}
	if len(frames) == 0 || phase.Start != phase.Before.Cursor || phase.Start >= phase.End ||
		phase.End != frames[len(frames)-1].Number || eventAt.IsZero() ||
		(rejected && (!phase.AppliedAt.IsZero() || phase.Size != phase.PreviousSize)) ||
		(!rejected && phase.Size == phase.PreviousSize) ||
		phase.PreviousSize != uint64(len(before[0].Payload)) || phase.Size < 24 || phase.Size > bfd.MaxPaddedPduSize ||
		before[0].SourcePort < 49152 || before[0].SourcePort > 65535 ||
		before[1].SourcePort == 0 || before[1].SourcePort > 65535 ||
		phase.Local == 0 || phase.Remote == 0 || phase.TX == 0 || phase.RX == 0 || phase.RemoteRX == 0 ||
		before[0].Mine != uint64(phase.Local) || before[0].Yours != uint64(phase.Remote) ||
		before[1].Mine != uint64(phase.Remote) || before[1].Yours != uint64(phase.Local) ||
		before[0].TX != phase.TX || before[0].RX != phase.RX || before[1].RX != phase.RemoteRX ||
		before[0].Source != gobfdIP || before[0].Destination != phase.Peer ||
		before[1].Source != phase.Peer || before[1].Destination != gobfdIP || before[0].Demand != 0 ||
		max(before[0].Number, before[1].Number) != phase.Start {
		return result, phase.Before, fmt.Errorf("invalid padding phase: %w", errTimerEvidence)
	}
	suppressed := before[1].Demand == 1
	if suppressed && !rejected {
		return result, phase.Before, fmt.Errorf("accepted padding under Demand: %w", errTimerEvidence)
	}
	for direction, frame := range before {
		state := phase.Before.Auth[direction]
		if frame.State != 3 || frame.Multiplier == 0 || frame.Multiplier > 255 || !validDetectAuth(frame.detectFrame) ||
			state.Type != bfd.AuthType(frame.AuthType) ||
			(frame.HasSequence && (!state.AuthSeqKnown || uint64(state.RcvAuthSeq) != frame.Sequence)) {
			return result, phase.Before, fmt.Errorf("padding checkpoint lost authentication/state: %w", errTimerEvidence)
		}
	}
	work := phase.Before
	publication := controlPublication{At: eventAt, Interval: int64(max(phase.TX, phase.RemoteRX)), TargetSeen: rejected}
	var previous timerFrame
	var intervals []int64
	localAfter := 0
	for _, frame := range frames {
		local := frame.Source == gobfdIP && frame.Destination == phase.Peer
		remote := frame.Source == phase.Peer && frame.Destination == gobfdIP
		direction := 0
		if remote {
			direction = 1
		}
		prior := work.Frames[direction]
		if (!local && !remote) || frame.Number <= phase.Start || frame.Number > phase.End || frame.State != 3 ||
			frame.Mine != prior.Mine || frame.Yours != prior.Yours || frame.TX != prior.TX || frame.RX != prior.RX ||
			frame.Multiplier != prior.Multiplier || frame.Poll > 1 || frame.Final > 1 || frame.Poll+frame.Final > 1 ||
			(local && frame.Poll != 0) {
			return result, phase.Before, fmt.Errorf("padding changed control tuple/identity: %w", errTimerEvidence)
		}
		if err := checkControlContinuity(prior.detectFrame, frame.detectFrame); err != nil {
			return result, phase.Before, err
		}
		size := uint64(len(before[1].Payload))
		target := false
		if local {
			target = uint64(len(frame.Payload)) == phase.Size
			if !target && uint64(len(frame.Payload)) != phase.PreviousSize {
				return result, phase.Before, fmt.Errorf("unexpected padding size: %w", errTimerEvidence)
			}
			if err := publication.observe(frame.timerFrame, target); err != nil {
				return result, phase.Before, err
			}
			if suppressed && frame.Final == 0 {
				return result, phase.Before, fmt.Errorf("periodic padding under Demand: %w", errTimerEvidence)
			}
			size = phase.PreviousSize
			if target {
				size = phase.Size
			}
			if target && frame.Epoch.After(eventAt) {
				localAfter++
			}
		} else {
			result.RemoteTX = frame.TX
		}
		if err := verifyPaddingFrame(frame, size, &work.Auth[direction], keys); err != nil {
			return result, phase.Before, err
		}
		work.Frames[direction] = frame
		if !local {
			continue
		}
		if suppressed || !target || frame.Final != 0 || !frame.Epoch.After(eventAt) {
			previous, intervals = timerFrame{}, nil
			continue
		}
		if previous.Number != 0 {
			intervals = append(intervals, frame.Epoch.Sub(previous.Epoch).Microseconds())
		}
		previous = frame.timerFrame
	}
	if !publication.TargetSeen || result.RemoteTX == 0 || localAfter == 0 {
		return result, phase.Before, fmt.Errorf("missing padding traffic: %w", errTimerEvidence)
	}
	result.DetectionMicroseconds = before[1].Multiplier * max(phase.RX, result.RemoteTX)
	if suppressed {
		if frames[len(frames)-1].Epoch.Sub(eventAt).Microseconds() < craftedQuietMultiple*publication.Interval {
			return result, phase.Before, fmt.Errorf("short rejected-Demand padding window: %w", errTimerEvidence)
		}
	} else {
		mean, err := checkJitteredIntervals(intervals, publication.Interval, before[0].Multiplier)
		if err != nil {
			return result, phase.Before, err
		}
		result.Intervals, result.MeanMicroseconds = len(intervals), mean
	}
	work.Cursor = phase.End
	return result, work, nil
}

func paddingFrameFields() []string {
	return append(detectFrameFields(), "ip.flags.df", "udp.payload")
}

func paddingConfig(baseline, peer string, size uint64) (string, error) {
	start, end, err := timerPeerBlock(baseline, peer)
	if err != nil {
		return "", err
	}
	block := baseline[start:end]
	if size > bfd.MaxPaddedPduSize {
		return "", fmt.Errorf("padding size out of range: %w", errTimerEvidence)
	}
	if err := bfd.ValidatePaddedPduSize(uint16(size), strings.Contains(block, "    auth:\n")); err != nil {
		return "", fmt.Errorf("padding candidate: %w", err)
	}
	line := "    padded_pdu_size: " + strconv.FormatUint(size, 10) + "\n"
	old := "    padded_pdu_size: 128\n"
	switch strings.Count(block, "    padded_pdu_size: ") {
	case 0:
		marker := "    detect_mult: 3\n"
		if strings.Count(block, marker) != 1 {
			return "", fmt.Errorf("padding insert marker: %w", errTimerEvidence)
		}
		block = strings.Replace(block, marker, marker+line, 1)
	case 1:
		if strings.Count(block, old) != 1 {
			return "", fmt.Errorf("padding baseline field: %w", errTimerEvidence)
		}
		block = strings.Replace(block, old, line, 1)
	default:
		return "", fmt.Errorf("duplicate padding baseline field: %w", errTimerEvidence)
	}
	return baseline[:start] + block + baseline[end:], nil
}

func paddingRejections(input string) ([]time.Time, error) {
	var events []time.Time
	for line := range strings.SplitSeq(strings.TrimSpace(input), "\n") {
		if line == "" {
			continue
		}
		fields, err := timerJSONObject([]byte(line), "msg")
		if err != nil {
			return nil, err
		}
		var message string
		if decodeErr := json.Unmarshal(fields["msg"], &message); decodeErr != nil {
			return nil, fmt.Errorf("rejection message: %w", decodeErr)
		}
		if message != "invalid control-session candidate, keeping current settings" {
			continue
		}
		at, err := timerPublicationTime(fields["time"])
		if err != nil {
			return nil, err
		}
		events = append(events, at)
	}
	return events, nil
}

func parsePaddingFrames(rows [][]string) ([]paddingFrame, error) {
	width := len(detectFrameFields())
	baseRows := make([][]string, len(rows))
	for index, row := range rows {
		if len(row) != len(paddingFrameFields()) {
			return nil, fmt.Errorf("padding frame columns=%d: %w", len(row), errTimerEvidence)
		}
		baseRows[index] = row[:width]
	}
	base, err := parseDetectFrames(baseRows)
	if err != nil {
		return nil, err
	}
	frames := make([]paddingFrame, len(base))
	for index, frame := range base {
		decoded := paddingFrame{detectFrame: frame}
		decoded.DF, err = parseTimerField(rows[index][width])
		if err != nil {
			return nil, fmt.Errorf("padding DF: %w", err)
		}
		if decoded.DF > 1 {
			return nil, fmt.Errorf("padding DF: %w", errTimerEvidence)
		}
		decoded.Payload, err = hex.DecodeString(strings.ReplaceAll(rows[index][width+1], ":", ""))
		if err != nil {
			return nil, fmt.Errorf("padding payload hex: %w", err)
		}
		if uint64(len(decoded.Payload))+8 != frame.UDPLength {
			return nil, fmt.Errorf("padding UDP length disagrees with payload: %w", errTimerEvidence)
		}
		var packet bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(decoded.Payload, &packet); err != nil {
			return nil, fmt.Errorf("padding packet: %w", err)
		}
		if !paddingPacketMatches(frame, &packet) {
			return nil, fmt.Errorf("padding decoded metadata disagrees with wire: %w", errTimerEvidence)
		}
		frames[index] = decoded
	}
	return frames, nil
}

func paddingPacketMatches(frame detectFrame, packet *bfd.ControlPacket) bool {
	if uint64(packet.State) != frame.State || packet.Poll != (frame.Poll == 1) || packet.Final != (frame.Final == 1) ||
		packet.Demand != (frame.Demand == 1) || packet.AuthPresent != (frame.AuthPresent == 1) ||
		uint64(packet.MyDiscriminator) != frame.Mine || uint64(packet.YourDiscriminator) != frame.Yours ||
		uint64(packet.DesiredMinTxInterval) != frame.TX || uint64(packet.RequiredMinRxInterval) != frame.RX ||
		uint64(packet.DetectMult) != frame.Multiplier {
		return false
	}
	return packet.Auth == nil || uint64(packet.Auth.Type) == frame.AuthType && uint64(packet.Auth.KeyID) == frame.Key &&
		uint64(packet.Auth.SequenceNumber) == frame.Sequence
}

func verifyPaddingFrame(frame paddingFrame, size uint64, state *bfd.AuthState, keys bfd.AuthKeyStore) error {
	declared := bfd.HeaderSize
	switch state.Type {
	case bfd.AuthTypeNone:
	case bfd.AuthTypeSimplePassword:
		declared += 3 + len(keys.CurrentKey().Secret)
	case bfd.AuthTypeKeyedMD5, bfd.AuthTypeMeticulousKeyedMD5:
		declared = 48
	case bfd.AuthTypeKeyedSHA1, bfd.AuthTypeMeticulousKeyedSHA1:
		declared = 52
	default:
		return fmt.Errorf("padding authentication type %d: %w", state.Type, errTimerEvidence)
	}
	if uint64(len(frame.Payload)) != size || frame.UDPLength != size+8 || frame.DF > 1 ||
		(size > uint64(declared) && frame.DF != 1) ||
		!validInteropControlPayload(frame.Payload, declared, byte(state.Type)) {
		return fmt.Errorf("padding Control payload: %w", errTimerEvidence)
	}
	if state.Type == bfd.AuthTypeNone {
		return nil
	}
	// Verify replaces digest bytes with the key; retain the original capture unchanged.
	wire := slices.Clone(frame.Payload)
	defer clear(wire)
	var packet bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(wire, &packet); err != nil {
		return fmt.Errorf("padding Control decode: %w", err)
	}
	auth, err := bfd.NewAuthenticator(state.Type)
	if err != nil {
		return fmt.Errorf("padding authenticator: %w", err)
	}
	if err := auth.Verify(state, keys, &packet, wire, len(wire)); err != nil {
		return fmt.Errorf("padding authentication: %w", err)
	}
	return nil
}
