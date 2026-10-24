//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestTimerReloadPaddingProbeNamespace(t *testing.T) {
	directory := t.TempDir()
	assignment := installFakeCommand(t, directory, "podman", "owned-container")
	key, value, _ := strings.Cut(assignment, "=")
	t.Setenv(key, value)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop")
	t.Setenv("INTEROP_FAKE_PODMAN_LOG", filepath.Join(directory, "commands"))
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := paddingReadiness(ctx); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(directory, "commands"))
	if err != nil {
		t.Fatal("readiness never reached owned container")
	}
	assertCommandSubsequence(t, string(log), []string{
		"exec immutable-owned-id /tmp/gobfd-interop.test -test.v -test.count=1 -test.run=^TestInteropReadinessProbe$",
	})
}

func TestTimerReloadPaddingRejectedGeneration(t *testing.T) {
	t.Parallel()
	before := timerReceipt{Desired: 7, Applied: 7}
	if err := paddingGenerationUnchanged([]timerReceipt{before}, before); err != nil {
		t.Fatal(err)
	}
	for _, after := range []timerReceipt{
		{Desired: 8, Applied: 8},
		{Desired: 8, Applied: 7},
		{Desired: 7, Applied: 7, Stale: true},
		{Desired: 7, Applied: 7, Pending: 1},
		{Desired: 7, Applied: 7, Failed: 1},
		{Desired: 7, Applied: 7, Base: timerBaseReceipt{Created: 1}},
		{Desired: 7, Applied: 7, Base: timerBaseReceipt{Released: 1}},
		{Desired: 7, Applied: 7, Base: timerBaseReceipt{Updated: 1}},
	} {
		if err := paddingGenerationUnchanged([]timerReceipt{after}, before); err == nil {
			t.Fatalf("accepted rejected-generation mutation: %+v", after)
		}
	}
}

func paddingContractFrame(t *testing.T, authType bfd.AuthType, size int, mine, yours, sequence uint32,
) (paddingFrame, bfd.AuthKeyStore) {
	t.Helper()
	packet := &bfd.ControlPacket{
		Version: 1, State: bfd.StateUp, DetectMult: 3,
		MyDiscriminator: mine, YourDiscriminator: yours, DesiredMinTxInterval: 300000, RequiredMinRxInterval: 300000,
	}
	payload, keys := signedContractControl(t, packet, authType, size, sequence)
	frame := paddingFrame{
		UDPLength: uint64(size + 8), AuthType: uint64(authType), DF: 1, Payload: payload,
		Source: gobfdIP, Destination: bird3IP, SourcePort: 50000, State: 3, Mine: uint64(mine), Yours: uint64(yours),
		TX: 300000, RX: 300000, Multiplier: 3, AuthPresent: 1, Key: 7, Sequence: uint64(packet.Auth.SequenceNumber),
		HasSequence: authType != bfd.AuthTypeSimplePassword,
	}
	if mine == 22 {
		frame.Source, frame.Destination, frame.SourcePort = bird3IP, gobfdIP, 60000
	}
	return frame, keys
}

func signedContractControl(t *testing.T, packet *bfd.ControlPacket, authType bfd.AuthType, size int, sequence uint32,
) ([]byte, bfd.AuthKeyStore) {
	t.Helper()
	keys, err := bfd.NewStaticAuthKeyStore(bfd.AuthKey{ID: 7, Type: authType, Secret: []byte("fixture-key")})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := bfd.NewAuthenticator(authType)
	if err != nil {
		t.Fatal(err)
	}
	state := &bfd.AuthState{Type: authType, XmitAuthSeq: sequence}
	payload := make([]byte, size)
	if err := auth.Sign(state, keys, packet, payload, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := bfd.MarshalControlPacket(packet, payload); err != nil {
		t.Fatal(err)
	}
	return payload, keys
}

func TestTimerReloadPaddingCapture(t *testing.T) {
	t.Parallel()
	frame, _ := paddingContractFrame(t, bfd.AuthTypeKeyedSHA1, 128, 11, 22, 100)
	row := []string{
		"1", "100.1", gobfdIP, bird3IP, "3", "0", "0", "11", "22",
		"300000", "300000", "3", "1", "4", "7", "101", "0", "136", "50000", "1", hex.EncodeToString(frame.Payload),
	}
	frames, err := parsePaddingFrames([][]string{row})
	if err != nil || len(frames) != 1 {
		t.Fatalf("padding capture: frames=%d error=%v", len(frames), err)
	}
	if frames[0].DF != 1 || !slices.Equal(frames[0].Payload, frame.Payload) {
		t.Fatal("padding capture lost wire data")
	}
	for _, test := range []struct {
		name   string
		column int
		value  string
	}{
		{"DF range", 19, "2"}, {"payload hex", 20, "xx"}, {"UDP length", 17, "135"}, {"discriminator mismatch", 7, "33"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed := slices.Clone(row)
			changed[test.column] = test.value
			if _, err := parsePaddingFrames([][]string{changed}); err == nil {
				t.Fatal("accepted malformed padding capture")
			}
		})
	}
}

func TestTimerReloadPaddingBoundary(t *testing.T) {
	t.Parallel()
	for _, authType := range []bfd.AuthType{
		bfd.AuthTypeSimplePassword, bfd.AuthTypeKeyedSHA1, bfd.AuthTypeMeticulousKeyedSHA1,
	} {
		t.Run(authType.String(), func(t *testing.T) {
			t.Parallel()
			for _, size := range []int{128, 192} {
				frame, keys := paddingContractFrame(t, authType, size, 11, 22, 100)
				original := slices.Clone(frame.Payload)
				state := &bfd.AuthState{Type: authType}
				if err := verifyPaddingFrame(frame, uint64(size), state, keys); err != nil {
					t.Fatalf("valid padding: %v", err)
				}
				if !slices.Equal(frame.Payload, original) {
					t.Fatal("MAC verification changed retained wire evidence")
				}
			}
		})
	}
}

func TestTimerReloadPaddingRejectsCorruption(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*paddingFrame)
	}{
		{"DF missing", func(f *paddingFrame) { f.DF = 0 }},
		{"UDP length", func(f *paddingFrame) { f.UDPLength-- }},
		{"payload length", func(f *paddingFrame) { f.Payload = f.Payload[:127] }},
		{"declared BFD length", func(f *paddingFrame) { f.Payload[3] = 128 }},
		{"nonzero padding", func(f *paddingFrame) { f.Payload[127] = 1 }},
		{"MAC", func(f *paddingFrame) { f.Payload[32] ^= 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frame, keys := paddingContractFrame(t, bfd.AuthTypeKeyedSHA1, 128, 11, 22, 100)
			test.mutate(&frame)
			state := &bfd.AuthState{Type: bfd.AuthTypeKeyedSHA1, RcvAuthSeq: 100, AuthSeqKnown: true}
			before := *state
			if err := verifyPaddingFrame(frame, 128, state, keys); err == nil {
				t.Fatal("accepted corrupt padding/auth evidence")
			}
			if *state != before {
				t.Fatal("rejected frame changed receive authentication state")
			}
		})
	}
}

func paddingContractPhase(t *testing.T, authType bfd.AuthType) ([]paddingFrame, paddingPhase, bfd.AuthKeyStore) {
	t.Helper()
	at := time.Unix(100, 0).UTC()
	local, keys := paddingContractFrame(t, authType, 128, 11, 22, 100)
	remote, _ := paddingContractFrame(t, authType, 52, 22, 11, 200)
	local.Number, remote.Number, local.Epoch, remote.Epoch = 1, 2, at, at
	checkpoint := paddingCheckpoint{
		Frames: [2]paddingFrame{local, remote}, Cursor: 2,
		Auth: [2]bfd.AuthState{
			{Type: authType, RcvAuthSeq: 101, AuthSeqKnown: true},
			{Type: authType, RcvAuthSeq: 201, AuthSeqKnown: true},
		},
	}
	phase := paddingPhase{
		Peer: bird3IP, Start: 2, End: 28, Local: 11, Remote: 22, TX: 300000, RX: 300000, RemoteRX: 300000,
		AppliedAt: at.Add(time.Millisecond), Before: checkpoint, PreviousSize: 128, Size: 192,
	}
	frames := make([]paddingFrame, 0, 26)
	for index := range 13 {
		peer, _ := paddingContractFrame(t, authType, 52, 22, 11, uint32(201+index))
		outgoing, _ := paddingContractFrame(t, authType, 192, 11, 22, uint32(101+index))
		peer.Number, outgoing.Number = uint64(3+2*index), uint64(4+2*index)
		outgoing.Epoch = at.Add(time.Duration(index+1) * 262500 * time.Microsecond)
		peer.Epoch = outgoing.Epoch.Add(-time.Microsecond)
		frames = append(frames, peer, outgoing)
	}
	return frames, phase, keys
}

func TestTimerReloadPaddingResizeEvidence(t *testing.T) {
	t.Parallel()
	for _, authType := range []bfd.AuthType{bfd.AuthTypeKeyedSHA1, bfd.AuthTypeMeticulousKeyedSHA1} {
		t.Run(authType.String(), func(t *testing.T) {
			t.Parallel()
			frames, phase, keys := paddingContractPhase(t, authType)
			result, next, err := evaluatePaddingPhase(frames, phase, keys)
			if err != nil || result.DetectionMicroseconds != 900000 ||
				result.Intervals != 12 || result.MeanMicroseconds != 262500 ||
				next.Cursor != 28 || next.Auth[0].RcvAuthSeq != 114 || next.Auth[1].RcvAuthSeq != 214 {
				t.Fatalf("padding resize result=%+v cursor=%d auth=%+v error=%v", result, next.Cursor, next.Auth, err)
			}
		})
	}
}

func TestTimerReloadPaddingRejectedEvidence(t *testing.T) {
	t.Parallel()
	frames, phase, keys := paddingContractPhase(t, bfd.AuthTypeMeticulousKeyedSHA1)
	phase.Size, phase.RejectedAt, phase.AppliedAt = 128, phase.AppliedAt, time.Time{}
	for index := 1; index < len(frames); index += 2 {
		frames[index].Payload = frames[index].Payload[:128]
		frames[index].UDPLength = 136
	}
	result, next, err := evaluatePaddingPhase(frames, phase, keys)
	if err != nil || result.Intervals != 12 || next.Cursor != 28 {
		t.Fatalf("rejected candidate wire result=%+v error=%v", result, err)
	}
}

func TestTimerReloadPaddingPhaseRejectsRegressions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func([]paddingFrame, *paddingPhase)
	}{
		{"forced Poll", func(f []paddingFrame, _ *paddingPhase) { f[1].Poll = 1 }},
		{"old padding after new", func(f []paddingFrame, _ *paddingPhase) {
			f[3].Payload = f[3].Payload[:128]
			f[3].UDPLength = 136
		}},
		{"late first target", func(f []paddingFrame, _ *paddingPhase) {
			for i := range f {
				f[i].Epoch = f[i].Epoch.Add(time.Second)
			}
		}},
		{"port changed", func(f []paddingFrame, _ *paddingPhase) { f[1].SourcePort++ }},
		{"replay", func(f []paddingFrame, _ *paddingPhase) {
			f[3].Payload = slices.Clone(f[1].Payload)
			f[3].Sequence = f[1].Sequence
		}},
		{"auth state reset", func(_ []paddingFrame, p *paddingPhase) { p.Before.Auth[0].AuthSeqKnown = false }},
		{"non-ephemeral local lease", func(f []paddingFrame, p *paddingPhase) {
			p.Before.Frames[0].SourcePort = 3784
			for i := 1; i < len(f); i += 2 {
				f[i].SourcePort = 3784
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frames, phase, keys := paddingContractPhase(t, bfd.AuthTypeMeticulousKeyedSHA1)
			test.mutate(frames, &phase)
			_, next, err := evaluatePaddingPhase(frames, phase, keys)
			if err == nil {
				t.Fatal("accepted invalid padding phase")
			}
			if next.Auth != phase.Before.Auth || next.Cursor != phase.Before.Cursor {
				t.Fatal("failed attempt advanced checkpoint")
			}
		})
	}
}

func TestTimerReloadPaddingConfig(t *testing.T) {
	t.Parallel()
	baseline, err := os.ReadFile("gobfd/gobfd.yml")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := config.Load("gobfd/gobfd.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		peer string
		size uint64
	}{{frrIP, 192}, {bird3IP, 0}, {holoIP, 128}} {
		t.Run(test.peer, func(t *testing.T) {
			t.Parallel()
			candidate, buildErr := paddingConfig(string(baseline), test.peer, test.size)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			path := filepath.Join(t.TempDir(), "candidate.yml")
			if writeErr := os.WriteFile(path, []byte(candidate), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			actual, loadErr := config.Load(path)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			wanted := slices.Clone(initial.Sessions)
			for index := range wanted {
				if wanted[index].Peer == test.peer {
					wanted[index].PaddedPduSize = uint16(test.size)
				}
			}
			if !slices.Equal(actual.Sessions, wanted) {
				t.Fatal("padding config changed another session field")
			}
		})
	}
	duplicate := strings.Replace(string(baseline), "    padded_pdu_size: 128\n",
		"    padded_pdu_size: 128\n    padded_pdu_size: 128\n", 1)
	if _, duplicateErr := paddingConfig(duplicate, frrIP, 192); duplicateErr == nil {
		t.Fatal("accepted duplicate padding field")
	}
	if _, sizeErr := paddingConfig(string(baseline), frrIP, bfd.MaxPaddedPduSize+1); sizeErr == nil {
		t.Fatal("accepted oversized padding")
	}
}

func TestTimerReloadPaddingRejectionEvents(t *testing.T) {
	t.Parallel()
	const valid = `{"time":"2026-10-24T00:00:00.123456789Z",` +
		`"msg":"invalid control-session candidate, keeping current settings"}`
	events, err := paddingRejections(valid)
	want := time.Date(2026, 10, 24, 0, 0, 0, 123456789, time.UTC)
	if err != nil || len(events) != 1 || !events[0].Equal(want) {
		t.Fatalf("rejection events=%v error=%v", events, err)
	}
	for _, bad := range []string{
		`{"msg":"invalid control-session candidate, keeping current settings"}`,
		strings.Replace(valid, ".123456789Z", ".1234567891Z", 1),
		strings.Replace(valid, "2026-10-24T00:00:00.123456789Z", "bad-time", 1),
		strings.Replace(valid, `"msg":`, `"msg":"other","msg":`, 1),
	} {
		if _, badErr := paddingRejections(bad); badErr == nil {
			t.Fatal("accepted ambiguous rejection evidence")
		}
	}
}
