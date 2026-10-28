//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type ttlObserver struct {
	*paddingObserver

	Initial  sessionState
	Counters [2]uint64
	Replays  uint64
}

func checkTTLContinuity(initial, current sessionState, baseline, counters [2]uint64, replays uint64) error {
	if current != initial || current.LocalState != "Up" || current.RemoteState != "Up" ||
		counters[0] != baseline[0] || counters[1] < baseline[1] || counters[1]-baseline[1] != replays {
		return fmt.Errorf("TTL reload changed initial identity/state/counters: %w", errTimerEvidence)
	}
	return nil
}

func readTTLFrames(ctx context.Context, after uint64) ([]ttlFrame, error) {
	rows, err := tsharkFields(ctx, frameBoundary(timerTraffic(fuzzIP), after), ttlFrameFields(), 0)
	if err != nil {
		return nil, err
	}
	return parseTTLFrames(rows)
}

func ttlCounters(ctx context.Context, t *testing.T, name string) [3]uint64 {
	t.Helper()
	text := timerTextSnapshot(ctx, t, fuzzIP, name)
	var counters [3]uint64
	for index, label := range []string{"Packets Received", "State Transitions", "Auth Failures"} {
		value, err := parseTimerCounter(text, label)
		if err != nil {
			t.Fatal(err)
		}
		counters[index] = value
	}
	return counters
}

func signalTTLProbe(ctx context.Context, t *testing.T, peer testcontainers.Container,
	name, event string, floor uint64,
) {
	t.Helper()
	logs, err := projectContainerLogs(ctx, "scapy-interop")
	if err != nil {
		t.Fatal(err)
	}
	before, err := ttlPeerEvents(logs)
	if err != nil {
		t.Fatal(err)
	}
	output, err := podmanCommand(ctx, "kill", "--signal", "USR2", peer.GetContainerID())
	writeTimerArtifact(t, name+"-signal", "txt", []byte(fmt.Sprintf("%s\nerror=%v\n", output, err)))
	if err != nil {
		t.Fatal(err)
	}
	eventCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = pollUntil(eventCtx, 50*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
		var readErr error
		logs, readErr = projectContainerLogs(pollCtx, "scapy-interop")
		if readErr != nil {
			return false, false, readErr
		}
		after, parseErr := ttlPeerEvents(logs)
		if parseErr != nil {
			return false, false, parseErr
		}
		if len(after) < len(before) {
			return false, false, errTimerEvidence
		}
		for _, added := range after[len(before):] {
			if added.Event == event && added.Value == strconv.FormatUint(floor, 10) {
				return true, true, nil
			}
		}
		return false, false, nil
	})
	writeTimerArtifact(t, name+"-events", "jsonl", []byte(logs))
	if err != nil {
		t.Fatalf("peer probe event %s: %v", event, err)
	}
}

func runTTLProbe(ctx context.Context, t *testing.T, peer testcontainers.Container,
	observer *ttlObserver, name, candidate string, floor uint64, rejected bool,
) {
	t.Helper()
	phaseCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	requirePaddingReadiness(phaseCtx, t, name+"-before")
	before, counters := timerSnapshot(phaseCtx, t, fuzzIP, name+"-before")
	if err := checkTTLContinuity(observer.Initial, before, observer.Counters, counters, observer.Replays); err != nil {
		t.Fatal(err)
	}
	signalTTLProbe(phaseCtx, t, peer, name+"-pause", "ttl-probe-ready", floor)
	phase := ttlPhase{
		Before: observer.Checkpoint, Peer: fuzzIP, Floor: floor,
		Demand: observer.Checkpoint.Frames[1].Demand, Start: observer.Checkpoint.Cursor,
		CountersBefore: ttlCounters(phaseCtx, t, name+"-paused-before"),
	}
	if candidate != "" {
		if rejected {
			rejectedPaddingCandidate(phaseCtx, t, candidate, name)
		} else {
			reloadControlCandidate(phaseCtx, t, candidate, name, timerReceiptImmediate)
		}
	}
	signalTTLProbe(phaseCtx, t, peer, name+"-emit", "ttl-probe-final", floor)
	phase.CountersAfter = ttlCounters(phaseCtx, t, name+"-paused-after")
	signalTTLProbe(phaseCtx, t, peer, name+"-resume", "ttl-probe-resume", floor)
	var frames []ttlFrame
	_, err := pollUntil(phaseCtx, 100*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
		var queryErr error
		frames, queryErr = readTTLFrames(pollCtx, phase.Start)
		if queryErr != nil {
			return false, false, queryErr
		}
		lowSeen := false
		for index, frame := range frames {
			if frame.Source == fuzzIP && frame.TTL < floor {
				lowSeen = true
			}
			if lowSeen && frame.Source == gobfdIP && frame.Final == 1 {
				frames = frames[:index+1]
				phase.End = frame.Number
				return true, true, nil
			}
		}
		return false, false, nil
	})
	var result ttlResult
	next := phase.Before
	if err == nil {
		result, next, err = evaluateTTLPhase(frames, phase, observer.Keys)
	}
	evidence, marshalErr := json.Marshal(struct {
		Phase  ttlPhase   `json:"phase"`
		Frames []ttlFrame `json:"frames"`
		Result ttlResult  `json:"result"`
		Error  string     `json:"error"`
	}{phase, frames, result, fmt.Sprint(err)})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	writeTimerArtifact(t, name+"-wire", "json", evidence)
	if err != nil {
		t.Fatalf("TTL boundary wire evidence: %v", err)
	}
	after, afterCounters := timerSnapshot(phaseCtx, t, fuzzIP, name+"-after")
	if continuityErr := checkTTLContinuity(observer.Initial, after,
		observer.Counters, afterCounters, observer.Replays+1); continuityErr != nil {
		t.Fatalf("TTL changed session identity/state: before=%+v/%v after=%+v/%v", before, counters, after, afterCounters)
	}
	observer.Replays++
	observer.Checkpoint = next
	requirePaddingReadiness(phaseCtx, t, name+"-after")
}

func runTTLScenario(ctx context.Context, t *testing.T, baseline []byte, peer testcontainers.Container) {
	t.Helper()
	waitCraftedUp(ctx, t, 3000000)
	initial, counters := timerSnapshot(ctx, t, fuzzIP, "ttl-initial")
	observer := &ttlObserver{
		paddingObserver: newPaddingObserver(ctx, t, baseline, fuzzIP, bfd.AuthTypeMeticulousKeyedSHA1),
		Initial:         initial, Counters: counters,
	}
	for _, mode := range []string{"async", "remote-demand"} {
		if mode == "remote-demand" {
			advanceCrafted(ctx, t, peer, "demand", true)
			frames, err := readTTLFrames(ctx, observer.Checkpoint.Cursor)
			if err != nil {
				t.Fatal(err)
			}
			next, err := advanceTTLDemand(frames, observer.Checkpoint, observer.Keys)
			if err != nil {
				t.Fatal(err)
			}
			observer.Checkpoint = next
			evidence, marshalErr := json.Marshal(frames)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			writeTimerArtifact(t, "ttl-demand-entry-wire", "json", evidence)
			current, observed := timerSnapshot(ctx, t, fuzzIP, "ttl-demand-entry")
			if err := checkTTLContinuity(initial, current, counters, observed, observer.Replays); err != nil {
				t.Fatal(err)
			}
		}
		for index, hops := range []uint64{1, 5, 1} {
			name := fmt.Sprintf("ttl-%s-%d-hops-%d", mode, index, hops)
			var candidate string
			if index != 0 {
				var err error
				candidate, err = ttlConfig(string(baseline), hops)
				if err != nil {
					t.Fatal(err)
				}
			}
			runTTLProbe(ctx, t, peer, observer, name, candidate, 255-hops, false)
		}
	}
	receipts := readTimerReceipts(ctx, t, "ttl-rejection-before")
	previous := receipts[len(receipts)-1]
	candidate, err := ttlConfig(string(baseline), 5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(candidate, "      key_id: 7\n") != 1 {
		t.Fatal("ambiguous authenticated TTL fixture")
	}
	candidate = strings.Replace(candidate, "      key_id: 7\n", "      key_id: 8\n", 1)
	runTTLProbe(ctx, t, peer, observer, "ttl-rejected-auth-and-hops", candidate, 254, true)
	after := readTimerReceipts(ctx, t, "ttl-rejection-after")
	if len(after) < len(receipts) {
		t.Fatal("lost TTL generation history")
	}
	if err := paddingGenerationUnchanged(after[len(receipts):], previous); err != nil {
		t.Fatal(err)
	}
}
