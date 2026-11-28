//go:build interop_testcontainers

package interop_test

import (
	"encoding/hex"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func ttlContractFrame(t *testing.T, local, poll, final, demand bool, sequence uint32) (ttlFrame, bfd.AuthKeyStore) {
	t.Helper()
	packet := &bfd.ControlPacket{
		Version: 1, State: bfd.StateUp, DetectMult: 3, Poll: poll, Final: final, Demand: demand,
		MyDiscriminator: 11, YourDiscriminator: 22, DesiredMinTxInterval: 300000, RequiredMinRxInterval: 300000,
	}
	size := 128
	if !local {
		size = 52
		packet.MyDiscriminator, packet.YourDiscriminator = 22, 11
	}
	wire, keys := signedContractControl(t, packet, bfd.AuthTypeMeticulousKeyedSHA1, size, sequence)
	frame := ttlFrame{
		TTL: 255, DestinationPort: 4784, DF: 1, Payload: wire, UDPLength: uint64(size + 8),
		Source: gobfdIP, Destination: fuzzIP, SourcePort: 50000, State: 3, Mine: uint64(packet.MyDiscriminator),
		Yours: uint64(packet.YourDiscriminator), TX: 300000, RX: 300000, Multiplier: 3, AuthPresent: 1, AuthType: 5, Key: 7,
		Sequence: uint64(packet.Auth.SequenceNumber), HasSequence: true,
	}
	if !local {
		frame.Source, frame.Destination, frame.SourcePort = fuzzIP, gobfdIP, 49152
	}
	if poll {
		frame.Poll = 1
	}
	if final {
		frame.Final = 1
	}
	if demand {
		frame.Demand = 1
	}
	return frame, keys
}

func ttlContractPhase(t *testing.T, demand bool) ([]ttlFrame, ttlPhase, bfd.AuthKeyStore) {
	t.Helper()
	local, keys := ttlContractFrame(t, true, false, false, false, 100)
	remote, _ := ttlContractFrame(t, false, false, false, demand, 200)
	at := time.Unix(100, 0)
	local.Number, local.Epoch = 1, at
	remote.Number, remote.Epoch = 2, at.Add(time.Millisecond)
	before := paddingCheckpoint{
		Frames: [2]paddingFrame{local.paddingFrame, remote.paddingFrame}, Cursor: 2,
		Auth: [2]bfd.AuthState{
			{Type: bfd.AuthTypeMeticulousKeyedSHA1, RcvAuthSeq: 101, AuthSeqKnown: true},
			{Type: bfd.AuthTypeMeticulousKeyedSHA1, RcvAuthSeq: 201, AuthSeqKnown: true},
		},
	}
	low, _ := ttlContractFrame(t, false, true, false, demand, 201)
	low.Number, low.Epoch, low.TTL = 4, at.Add(50*time.Millisecond), 253
	good := low
	good.Number, good.Epoch, good.TTL = 5, at.Add(250*time.Millisecond), 254
	final, _ := ttlContractFrame(t, true, false, true, false, 101)
	final.Number, final.Epoch = 6, at.Add(251*time.Millisecond)
	replay := remote
	replay.Number, replay.Epoch, replay.TTL = 3, at.Add(20*time.Millisecond), 254
	phase := ttlPhase{
		Before: before, Peer: fuzzIP, Floor: 254, Start: 2, End: 6, CountersBefore: [3]uint64{10, 2, 0},
		CountersAfter: [3]uint64{11, 2, 1},
	}
	if demand {
		phase.Demand = 1
	}
	return []ttlFrame{replay, low, good, final}, phase, keys
}

func TestTimerReloadTTLBoundaryEvidence(t *testing.T) {
	t.Parallel()
	for _, demand := range []bool{false, true} {
		frames, phase, keys := ttlContractPhase(t, demand)
		result, next, err := evaluateTTLPhase(frames, phase, keys)
		if err != nil || result.Replay != 3 || result.Low != 4 || result.Good != 5 || result.Final != 6 ||
			result.DetectionMicroseconds != 900000 || next.Cursor != 6 ||
			next.Auth[1].RcvAuthSeq != 202 || next.Auth[0].RcvAuthSeq != 102 {
			t.Fatalf("TTL phase: %+v next=%+v err=%v", result, next.Auth, err)
		}
	}
}

func TestTimerReloadTTLRejectsRegressions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func([]ttlFrame, *ttlPhase)
	}{
		{"accepted bad TTL", func(_ []ttlFrame, phase *ttlPhase) { phase.CountersAfter[0]++ }},
		{"auth failure", func(_ []ttlFrame, phase *ttlPhase) { phase.CountersAfter[2]++ }},
		{"FSM transition", func(_ []ttlFrame, phase *ttlPhase) { phase.CountersAfter[1]++ }},
		{"wrong floor", func(_ []ttlFrame, phase *ttlPhase) { phase.Floor = 253 }},
		{"forced local Poll", func(frames []ttlFrame, _ *ttlPhase) { frames[3].Poll = 1 }},
		{"missing Final", func(frames []ttlFrame, _ *ttlPhase) { frames[3].Final = 0 }},
		{"wrong destination", func(frames []ttlFrame, _ *ttlPhase) { frames[2].DestinationPort = 3784 }},
		{"expired replay state", func(frames []ttlFrame, _ *ttlPhase) {
			frames[2].Epoch = frames[2].Epoch.Add(2 * time.Second)
			frames[3].Epoch = frames[3].Epoch.Add(2 * time.Second)
		}},
		{"invalid MAC", func(frames []ttlFrame, _ *ttlPhase) { frames[2].Payload[40] ^= 1 }},
		{"checkpoint auth reset", func(_ []ttlFrame, phase *ttlPhase) { phase.Before.Auth[1].AuthSeqKnown = false }},
		{"replayed sequence", func(frames []ttlFrame, _ *ttlPhase) { frames[2].Sequence-- }},
		{"reload reset receive state", func(_ []ttlFrame, phase *ttlPhase) {
			phase.CountersAfter[0]++
			phase.CountersAfter[2]--
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frames, phase, keys := ttlContractPhase(t, false)
			frames[2].Payload = slices.Clone(frames[2].Payload)
			test.change(frames, &phase)
			wire := slices.Clone(frames[2].Payload)
			_, next, err := evaluateTTLPhase(frames, phase, keys)
			if err == nil || !reflect.DeepEqual(next, phase.Before) || !slices.Equal(wire, frames[2].Payload) {
				t.Fatalf("invalid evidence accepted or retry state changed: %v", err)
			}
		})
	}
}

func TestTimerReloadTTLDemandContinuity(t *testing.T) {
	t.Parallel()
	_, phase, keys := ttlContractPhase(t, false)
	poll, _ := ttlContractFrame(t, false, true, false, true, 201)
	poll.Number, poll.Epoch = 3, time.Unix(100, 50000000)
	final, _ := ttlContractFrame(t, true, false, true, false, 101)
	final.Number, final.Epoch = 4, time.Unix(100, 51000000)
	frames := []ttlFrame{poll, final}
	next, err := advanceTTLDemand(frames, phase.Before, keys)
	if err != nil || next.Cursor != 4 || next.Frames[1].Demand != 1 || next.Auth[1].RcvAuthSeq != 202 ||
		next.Auth[0].RcvAuthSeq != 102 {
		t.Fatalf("Demand reset continuity: next=%+v err=%v", next.Auth, err)
	}
	frames[1].Payload[40] ^= 1
	if next, err := advanceTTLDemand(frames, phase.Before, keys); err == nil || !reflect.DeepEqual(next, phase.Before) {
		t.Fatal("failed Demand transition committed authentication state")
	}
}

func TestTimerReloadTTLConfigAndEvents(t *testing.T) {
	t.Parallel()
	baseline := "sessions:\n  - peer: \"172.20.0.40\"\n    type: multi_hop\n    multihop_max_hops: 1\n    detect_mult: 3\n"
	candidate, err := ttlConfig(baseline, 5)
	if err != nil || !strings.Contains(candidate, "    multihop_max_hops: 5\n") {
		t.Fatalf("hop policy candidate: %q %v", candidate, err)
	}
	for _, input := range []string{
		baseline + "    multihop_max_hops: 1\n", strings.ReplaceAll(baseline, "multi_hop", "single_hop"),
	} {
		if _, configErr := ttlConfig(input, 5); configErr == nil {
			t.Fatal("ambiguous or single-hop candidate accepted")
		}
	}
	events, err := ttlPeerEvents("{\"time\":\"2026-10-24T05:00:00Z\",\"event\":\"ttl-probe-ready\",\"value\":\"254\"}\n")
	if err != nil || len(events) != 1 || events[0].Event != "ttl-probe-ready" || events[0].Value != "254" {
		t.Fatalf("peer events: %+v %v", events, err)
	}
	bad := "{\"time\":\"bad\",\"event\":\"ttl-probe-ready\",\"event\":\"ttl-probe-final\",\"value\":\"254\"}"
	if _, parseErr := ttlPeerEvents(bad); parseErr == nil {
		t.Fatal("malformed/duplicate peer event accepted")
	}
}

func TestTimerReloadTTLCapture(t *testing.T) {
	t.Parallel()
	frame, _ := paddingContractFrame(t, bfd.AuthTypeMeticulousKeyedSHA1, 128, 11, 22, 100)
	row := []string{
		"1", "100.1", gobfdIP, fuzzIP, "3", "0", "0", "11", "22", "300000", "300000", "3",
		"1", "5", "7", "101", "0", "136", "50000", "1", hex.EncodeToString(frame.Payload), "255", "4784",
	}
	frames, err := parseTTLFrames([][]string{row})
	if err != nil || len(frames) != 1 {
		t.Fatalf("TTL capture: frames=%d err=%v", len(frames), err)
	}
	if frames[0].TTL != 255 || frames[0].DestinationPort != 4784 || !slices.Equal(frames[0].Payload, frame.Payload) {
		t.Fatal("capture lost TTL/port/payload")
	}
	for _, test := range []struct {
		column int
		value  string
	}{{21, "0"}, {21, "256"}, {21, "bad"}, {22, "3784"}, {22, "0"}} {
		changed := slices.Clone(row)
		changed[test.column] = test.value
		if _, err := parseTTLFrames([][]string{changed}); err == nil {
			t.Fatal("invalid multihop TTL/port accepted")
		}
	}
}
