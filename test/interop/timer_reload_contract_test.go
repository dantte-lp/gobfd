//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // The cleanup regression isolates an expected testing.T Fatal in the current test binary.
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	timerPendingJSON = `{"time":"2026-10-20T12:00:00.000000001Z","level":"ERROR",` +
		`"msg":"configuration reconciliation incomplete","desired_generation":2,"applied_generation":1,` +
		`"stale":true,"pending":1,"failed":0,` +
		`"base":{"created":0,"updated":0,"released":0,"pending":1,"timer_pending":1,"failed":0}}`
	timerAppliedJSON = `{"time":"2026-10-20T12:00:00.000000003Z","level":"INFO",` +
		`"msg":"configuration reconciliation converged","desired_generation":2,"applied_generation":2,` +
		`"stale":false,"pending":0,"failed":0,` +
		`"base":{"created":0,"updated":1,"released":0,"pending":0,"timer_pending":0,"failed":0}}`
)

func TestTimerReloadReceipts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, input          string
		wantApplied, wantErr bool
	}{
		{"pending ERROR is not failed", timerPendingJSON, false, false},
		{"pending then applied", timerPendingJSON + "\n" + timerAppliedJSON, true, false},
		{"missing pending", timerAppliedJSON, false, true},
		{"failed", strings.Replace(timerPendingJSON, `"failed":0`, `"failed":1`, 1), false, true},
		{
			"wrong generation",
			strings.ReplaceAll(timerPendingJSON+"\n"+timerAppliedJSON, `generation":2`, `generation":3`),
			false, true,
		},
		{"stale receipt cannot apply", strings.ReplaceAll(timerPendingJSON, `generation":2`, `generation":1`), false, false},
		{"false successful pending", strings.Replace(timerPendingJSON, `"stale":true`, `"stale":false`, 1), false, true},
		{"empty", "", false, true},
		{"malformed", "{", false, true},
		{"no receipts", `{"msg":"startup"}`, false, true},
		{"missing field", strings.Replace(timerPendingJSON, `"pending":1,`, "", 1), false, true},
		{
			"missing publication time",
			timerPendingJSON + "\n" + strings.Replace(timerAppliedJSON, `"time":"2026-10-20T12:00:00.000000003Z",`, "", 1),
			false, true,
		},
		{
			"malformed publication time",
			timerPendingJSON + "\n" + strings.Replace(timerAppliedJSON, `2026-10-20T12:00:00.000000003Z`, `invalid`, 1),
			false, true,
		},
		{
			"null publication time",
			timerPendingJSON + "\n" + strings.Replace(timerAppliedJSON, `"2026-10-20T12:00:00.000000003Z"`, `null`, 1),
			false, true,
		},
		{
			"excess publication precision",
			timerPendingJSON + "\n" + strings.Replace(timerAppliedJSON, `.000000003Z`, `.0000000039Z`, 1),
			false, true,
		},
		{"duplicate member", strings.Replace(timerPendingJSON, `"failed":0`, `"failed":0,"failed":0`, 1), false, true},
		{
			"duplicate nested member",
			strings.Replace(timerPendingJSON, `"updated":0`, `"updated":0,"updated":0`, 1),
			false, true,
		},
		{"invalid UTF8", timerPendingJSON + string([]byte{0xff}), false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			receipts, err := parseTimerReceipts(test.input)
			var applied bool
			if err == nil {
				var published time.Time
				published, err = timerGenerationApplied(receipts, 1)
				applied = !published.IsZero()
			}
			if (err != nil) != test.wantErr || applied != test.wantApplied {
				t.Fatalf("receipt applied=%t error=%v, want %t/error=%t", applied, err, test.wantApplied, test.wantErr)
			}
		})
	}
}

func TestTimerReloadCounters(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, input string
		wantErr     bool
	}{
		{"single", "State Transitions: 4\nAuth Failures: 0\n", false},
		{"missing", "Auth Failures: 0\n", true},
		{"duplicate", "State Transitions: 4\nState Transitions: 4\n", true},
		{"malformed", "State Transitions: 4 unexpected\n", true},
		{"negative", "State Transitions: -1\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseTimerCounter(test.input, "State Transitions")
			if (err != nil) != test.wantErr || (!test.wantErr && got != 4) {
				t.Fatalf("counter=%d error=%v", got, err)
			}
		})
	}
}

func TestTimerReloadConfigKeepsOtherPeersAndExplicitZero(t *testing.T) {
	t.Parallel()
	baseline, err := os.ReadFile("gobfd/gobfd.yml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := timerConfig(string(baseline), frrIP, "300ms", "0s")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(string(baseline), `    required_min_rx: "300ms"`, `    required_min_rx: "0s"`, 1)
	if got != want {
		t.Fatal("RX0 changed another field or omitted explicit zero")
	}
	missingRX := strings.ReplaceAll(string(baseline), `    required_min_rx: "300ms"`, "")
	if _, err := timerConfig(missingRX, frrIP, "300ms", "0s"); err == nil {
		t.Fatal("missing baseline RX accepted")
	}
}

func timerContractFrames(tx uint64) ([]timerFrame, timerPhase) {
	phase := timerPhase{
		Peer: frrIP, Start: 10, End: 25, Converged: 12, AppliedAt: time.Unix(1, 11000000),
		Local: 7, Remote: 8, TX: tx, RX: tx, RemoteRX: 100000,
	}
	local := timerFrame{
		Number: 11, Epoch: time.Unix(1, 0), Source: gobfdIP, Destination: frrIP,
		State: 3, Poll: 1, Mine: 7, Yours: 8, TX: tx, RX: tx, Multiplier: 3,
	}
	remote := timerFrame{
		Number: 12, Epoch: time.Unix(1, 10000000), Source: frrIP, Destination: gobfdIP,
		State: 3, Final: 1, Mine: 8, Yours: 7, TX: 300000, RX: 100000, Multiplier: 3,
	}
	frames := make([]timerFrame, 0, 15)
	frames = append(frames, local, remote)
	for index := range 13 {
		frame := local
		frame.Number = uint64(index + 13)
		frame.Epoch = time.Unix(1, 20000000).Add(time.Duration(index) * time.Duration(tx) * time.Microsecond * 90 / 100)
		frame.Poll = 0
		frames = append(frames, frame)
	}
	return frames, phase
}

// timerSpaced re-times the periodic frames with intervals cycling through percents of tx.
func timerSpaced(frames []timerFrame, tx uint64, percents ...uint64) []timerFrame {
	epoch := time.Unix(1, 20000000)
	for index := 2; index < len(frames); index++ {
		frames[index].Epoch = epoch
		epoch = epoch.Add(time.Duration(tx*percents[(index-2)%len(percents)]/100) * time.Microsecond)
	}
	return frames
}

func TestTimerReloadWireEvidence(t *testing.T) {
	t.Parallel()
	for _, tx := range []uint64{100000, 500000} {
		t.Run(strconv.FormatUint(tx, 10), func(t *testing.T) {
			t.Parallel()
			for _, test := range []struct {
				name    string
				mutate  func([]timerFrame, *timerPhase) []timerFrame
				wantErr bool
			}{
				{"valid", func(frames []timerFrame, _ *timerPhase) []timerFrame { return frames }, false},
				{"empty", func(_ []timerFrame, _ *timerPhase) []timerFrame { return nil }, true},
				{"old frame", func(frames []timerFrame, _ *timerPhase) []timerFrame { frames[0].Number = 10; return frames }, true},
				{"future frame", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					frames[len(frames)-1].Number = 26
					return frames
				}, true},
				{"local identity", func(frames []timerFrame, _ *timerPhase) []timerFrame { frames[0].Mine++; return frames }, true},
				{"remote identity", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					frames[1].Yours++
					return frames
				}, true},
				{"local tuple", func(frames []timerFrame, _ *timerPhase) []timerFrame { frames[4].RX++; return frames }, true},
				{"remote tuple", func(frames []timerFrame, _ *timerPhase) []timerFrame { frames[1].RX++; return frames }, true},
				{"flap", func(frames []timerFrame, _ *timerPhase) []timerFrame { frames[4].State = 1; return frames }, true},
				{"direction", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					frames[1].Destination = bird3IP
					return frames
				}, true},
				{"no Poll", func(frames []timerFrame, _ *timerPhase) []timerFrame { frames[0].Poll = 0; return frames }, true},
				{"no Final", func(frames []timerFrame, _ *timerPhase) []timerFrame { frames[1].Final = 0; return frames }, true},
				{"reversed Final", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					frames[0], frames[1] = frames[1], frames[0]
					return frames
				}, true},
				{"P and F", func(frames []timerFrame, _ *timerPhase) []timerFrame { frames[0].Final = 1; return frames }, true},
				{"insufficient", func(frames []timerFrame, _ *timerPhase) []timerFrame { return frames[:len(frames)-1] }, true},
				{"late convergence", func(frames []timerFrame, phase *timerPhase) []timerFrame {
					phase.Converged = 14
					return frames
				}, true},
				{"missing applied timestamp", func(frames []timerFrame, phase *timerPhase) []timerFrame {
					phase.AppliedAt = time.Time{}
					return frames
				}, true},
				{"Final-only excluded", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					frames[3].Final = 1
					return frames
				}, true},
				{"too fast", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					frames[4].Epoch = frames[3].Epoch.Add(time.Duration(tx) * time.Microsecond / 2)
					return frames
				}, true},
				{"too slow", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					frames[len(frames)-1].Epoch = frames[len(frames)-1].Epoch.Add(time.Duration(tx) * time.Microsecond)
					return frames
				}, true},
				{"below RFC jitter", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					return timerSpaced(frames, tx, 65)
				}, true},
				{"above RFC jitter", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					return timerSpaced(frames, tx, 103)
				}, true},
				{"biased mean", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					return timerSpaced(frames, tx, 99)
				}, true},
				{"RFC jitter within slack", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					return timerSpaced(frames, tx, 74, 101)
				}, false},
				{"below RFC slack", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					return timerSpaced(frames, tx, 72, 103)
				}, true},
				{"latency-biased mean", func(frames []timerFrame, _ *timerPhase) []timerFrame {
					return timerSpaced(frames, tx, 95)
				}, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					frames, phase := timerContractFrames(tx)
					frames = test.mutate(frames, &phase)
					result, err := evaluateTimerPhase(frames, phase)
					if (err != nil) != test.wantErr {
						t.Fatalf("wire result=%+v error=%v", result, err)
					}
					if !test.wantErr && (result.Intervals != 12 || result.DetectionMicroseconds != 3*max(tx, uint64(300000))) {
						t.Fatalf("unexpected wire result %+v", result)
					}
				})
			}
		})
	}
}

func TestTimerReloadFrameParsing(t *testing.T) {
	t.Parallel()
	row := []string{"11", "1.123", gobfdIP, frrIP, "0x03", "1", "0", "0x00000007", "0x00000008", "100000", "100000", "3"}
	for _, test := range []struct {
		name    string
		rows    [][]string
		wantErr bool
	}{
		{"valid", [][]string{row}, false},
		{"tshark boolean flags", [][]string{append(append(slices.Clone(row[:5]), "True", "False"), row[7:]...)}, false},
		{"unknown boolean", [][]string{append(append(slices.Clone(row[:5]), "yes", "False"), row[7:]...)}, true},
		{"empty", nil, true},
		{"missing columns", [][]string{row[:5]}, true},
		{"missing field", [][]string{append(slices.Clone(row[:11]), "")}, true},
		{"malformed timestamp", [][]string{append([]string{"11", "NaN"}, row[2:]...)}, true},
		{"duplicate frame", [][]string{row, row}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseTimerFrames(test.rows)
			if (err != nil) != test.wantErr {
				t.Fatalf("parse error=%v", err)
			}
		})
	}
}

func TestTimerReloadRXZeroEvidence(t *testing.T) {
	t.Parallel()
	frames, phase := timerContractFrames(300000)
	phase.RXZero, phase.RX, phase.RemoteRX = true, 0, 300000
	for index := range frames {
		if frames[index].Source == gobfdIP {
			frames[index].RX = 0
		} else {
			frames[index].RX = 300000
		}
	}
	frames[2].State = 1
	if _, err := evaluateTimerPhase(frames[:3], phase); err != nil {
		t.Fatalf("RX0 may go Down after Final: %v", err)
	}
	frames[0].RX = 300000
	if _, err := evaluateTimerPhase(frames[:3], phase); err == nil {
		t.Fatal("RX0 accepted without explicit wire zero Poll")
	}
}

func TestTimerReloadReceiptFinalCausality(t *testing.T) {
	t.Parallel()
	receipts, err := parseTimerReceipts(timerPendingJSON + "\n" + timerAppliedJSON)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := timerGenerationApplied(receipts, 1)
	if err != nil {
		t.Fatal(err)
	}
	final, err := parseTimerEpoch(strconv.FormatInt(applied.Unix(), 10) + ".000000004")
	if err != nil {
		t.Fatal(err)
	}
	if !final.Equal(applied.Add(time.Nanosecond)) {
		t.Fatalf("timestamp precision lost: applied=%s Final=%s", applied, final)
	}
	for _, rxZero := range []bool{false, true} {
		for _, observed := range []uint64{11, 13} {
			t.Run(fmt.Sprintf("RX0=%t/observed=%d", rxZero, observed), func(t *testing.T) {
				t.Parallel()
				frames, phase := timerContractFrames(100000)
				shift := final.Sub(frames[1].Epoch)
				for index := range frames {
					frames[index].Epoch = frames[index].Epoch.Add(shift)
				}
				tail := frames[len(frames)-1]
				tail.Number++
				tail.Epoch = tail.Epoch.Add(90 * time.Millisecond)
				frames = append(frames, tail)
				phase.End, phase.Converged, phase.AppliedAt = tail.Number, observed, applied
				if rxZero {
					phase.RXZero, phase.RX = true, 0
					for index := range frames {
						if frames[index].Source == gobfdIP {
							frames[index].RX = 0
						}
					}
				}
				if _, err := evaluateTimerPhase(frames, phase); !errors.Is(err, errTimerReceiptBeforeFinal) {
					t.Fatalf("premature applied accepted or wrong rejection: %v", err)
				}
				if observed == 13 {
					phase.AppliedAt = final.Add(time.Nanosecond)
					if _, err := evaluateTimerPhase(frames, phase); err != nil {
						t.Fatalf("valid publication after Final rejected: %v", err)
					}
					phase.AppliedAt = final
					if _, err := evaluateTimerPhase(frames, phase); !errors.Is(err, errTimerReceiptBeforeFinal) {
						t.Fatalf("ambiguous equal timestamps accepted: %v", err)
					}
				}
			})
		}
	}
}

func TestTimerReloadExactEpochRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "NaN", "1.", "1.0000000001", "1.-1", "-1.0", "1e9", "0.1"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := parseTimerEpoch(raw); err == nil {
				t.Fatalf("malformed epoch %q accepted", raw)
			}
		})
	}
}

func TestTimerReloadCaptureLag(t *testing.T) {
	t.Parallel()
	for _, rxZero := range []bool{false, true} {
		t.Run(fmt.Sprintf("RX0=%t", rxZero), func(t *testing.T) {
			t.Parallel()
			frames, phase := timerContractFrames(100000)
			phase.Converged = 11
			if rxZero {
				phase.RXZero, phase.RX = true, 0
				for index := range frames {
					if frames[index].Source == gobfdIP {
						frames[index].RX = 0
					}
				}
			}
			if _, err := evaluateTimerPhase(frames, phase); err != nil {
				t.Fatalf("valid Final after lagged capture watermark rejected: %v", err)
			}
		})
	}
}

func TestTimerReloadPostPublicationSamples(t *testing.T) {
	t.Parallel()
	frames, phase := timerContractFrames(100000)
	phase.Converged = 11
	phase.AppliedAt = frames[3].Epoch
	if _, err := evaluateTimerPhase(frames, phase); err == nil {
		t.Fatal("pre-publication and equal-time periodic frames counted toward 12 intervals")
	}
	for range 2 {
		tail := frames[len(frames)-1]
		tail.Number++
		tail.Epoch = tail.Epoch.Add(90 * time.Millisecond)
		frames = append(frames, tail)
	}
	phase.End = frames[len(frames)-1].Number
	result, err := evaluateTimerPhase(frames, phase)
	if err != nil || result.Intervals != 12 {
		t.Fatalf("12 post-publication intervals rejected: result=%+v error=%v", result, err)
	}
}

func TestTimerReloadArtifactCleanup(t *testing.T) {
	if os.Getenv("INTEROP_FAKE_ARTIFACT_CHILD") == "true" {
		directory := t.TempDir()
		assignment := installFakeCommand(t, directory, "podman", "timer-artifact-cleanup")
		installFakeCommand(t, directory, "assertions", "timer-artifact-cleanup")
		key, value, _ := strings.Cut(assignment, "=")
		t.Setenv(key, value)
		t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop")
		t.Setenv("GORACE", "atexit_sleep_ms=0")
		blocked := filepath.Join(directory, "not-a-directory")
		if err := os.WriteFile(blocked, []byte("block artifact directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("INTEROP_TESTCONTAINERS_ARTIFACT_DIR", blocked)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cleanupCancel()
			if _, err := podmanCommand(cleanupCtx, "rm", "--force", "immutable-owned-id"); err != nil {
				t.Errorf("fake exact container cleanup: %v", err)
			}
		})
		registerFourPeerArtifactCleanup(ctx, t, func(artifactCtx context.Context) {
			logs, err := projectContainerLogs(artifactCtx, "gobfd-interop")
			if err != nil {
				t.Fatalf("fake JSON read: %v", err)
			}
			writeTimerArtifact(t, "daemon", "jsonl", []byte(logs))
		}, func(artifactCtx context.Context) {
			if artifactCtx.Err() != nil {
				t.Errorf("PCAP context inherited cancellation: %v", artifactCtx.Err())
			}
			if _, err := podmanCommand(artifactCtx, "exec", "immutable-tshark-id", "retain-pcap"); err != nil {
				t.Errorf("fake PCAP attempt: %v", err)
			}
		})
		err := runFourPeerAssertionBatches(ctx, filepath.Join(directory, "assertions"), "gobfd-interop",
			func(_ int, _ []byte, exitCode int, _ error) {
				if code := fakeAppend("INTEROP_FAKE_COMMAND_LOG", "", fmt.Sprintf("batch-exit=%d", exitCode)); code != 0 {
					t.Errorf("record batch exit: %d", code)
				}
			})
		cancel()
		if err == nil {
			t.Fatal("fake failing batch succeeded")
		}
		t.Fatalf("original assertion failure: %v", err)
	}
	directory := t.TempDir()
	logPath := filepath.Join(directory, "commands")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestTimerReloadArtifactCleanup$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"INTEROP_FAKE_ARTIFACT_CHILD=true", "INTEROP_FAKE_COMMAND_LOG="+logPath, "GORACE=atexit_sleep_ms=0")
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected child assertion/JSON failure, got %v: %s", err, output)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandSubsequence(t, string(commands), []string{
		"batch-exit=17",
		"podman logs immutable-owned-id",
		"podman exec immutable-tshark-id retain-pcap",
		"podman rm --force immutable-owned-id",
	})
}

func TestTimerReloadPollFailureEvidence(t *testing.T) {
	directory := t.TempDir()
	assignment := installFakeCommand(t, directory, "podman", "timer-artifact-cleanup")
	key, value, _ := strings.Cut(assignment, "=")
	t.Setenv(key, value)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop")
	t.Setenv("INTEROP_FAKE_EVIDENCE_READ_FAIL", "true")
	t.Setenv("INTEROP_FAKE_COMMAND_LOG", filepath.Join(directory, "commands"))
	t.Setenv("INTEROP_TESTCONTAINERS_ARTIFACT_DIR", directory)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	original := errors.New("original generation failure marker")
	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(1, 0))
	defer cancel()
	cleaned := false
	t.Run("expired phase", func(t *testing.T) {
		t.Cleanup(func() { cleaned = true })
		err := retainTimerPollEvidence(ctx, "expired", 2, timerPendingJSON, errors.Join(ctx.Err(), original))
		var readErr *exec.ExitError
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, original) ||
			!errors.As(err, &readErr) || readErr.ExitCode() != 23 {
			t.Errorf("poll/read causes lost: %v", err)
		}
	})
	if !cleaned {
		t.Fatal("evidence failure prevented cleanup")
	}
	paths, err := filepath.Glob(filepath.Join(directory, "expired-last-poll-*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("last raw evidence paths=%v error=%v", paths, err)
	}
	last, err := os.ReadFile(paths[0])
	if err != nil || string(last) != timerPendingJSON {
		t.Fatalf("last raw response lost: %q/%v", last, err)
	}
	paths, err = filepath.Glob(filepath.Join(directory, "expired-generation-*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("generation summary paths=%v error=%v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var summary struct {
		Generation uint64 `json:"generation"`
		PollError  string `json:"poll_error"`
		ReadError  string `json:"read_error"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Generation != 2 || summary.PollError != errors.Join(ctx.Err(), original).Error() ||
		summary.ReadError == "" || summary.Error == "" {
		t.Fatalf("missing error summary: %+v", summary)
	}
}

func TestTimerReloadOwnedMutation(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "commands")
	assignment := installFakeCommand(t, directory, "podman", "owned-container")
	key, value, _ := strings.Cut(assignment, "=")
	t.Setenv(key, value)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop")
	t.Setenv("INTEROP_FAKE_PODMAN_LOG", logPath)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := copyTimerConfig(ctx, "gobfd-interop", "/owned.yml", "/etc/gobfd/gobfd.yml"); err != nil {
		t.Fatal(err)
	}
	if _, err := signalTimerReload(ctx); err != nil {
		t.Fatal(err)
	}
	err := copyTimerConfig(ctx, "foreign-interop", "/owned.yml", "/etc/gobfd/gobfd.yml")
	if !errors.Is(err, errForeignProjectContainer) {
		t.Fatalf("foreign mutation error=%v", err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	assertCommandSubsequence(t, string(log), []string{
		"cp /owned.yml immutable-owned-id:/etc/gobfd/gobfd.yml",
		"kill --signal HUP immutable-owned-id",
	})
	if strings.Contains(string(log), "foreign-id:") || strings.Contains(string(log), "cp /owned.yml gobfd-interop:") {
		t.Fatalf("unsafe command: %s", log)
	}
}

func TestTimerReloadAssertionBatches(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			directory := t.TempDir()
			assignment := installFakeCommand(t, directory, "assertions", "timer-batches")
			key, value, _ := strings.Cut(assignment, "=")
			t.Setenv(key, value)
			t.Setenv("INTEROP_FAKE_COMMAND_LOG", filepath.Join(directory, "commands"))
			t.Setenv("INTEROP_FAKE_TIMER_FAIL", strconv.FormatBool(fail))
			t.Setenv("GORACE", "atexit_sleep_ms=0")
			var events []string
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := runFourPeerAssertionBatches(ctx, filepath.Join(directory, "assertions"), "gobfd-interop",
				func(index int, output []byte, exitCode int, runErr error) {
					events = append(events, fmt.Sprintf("artifact-%d", index))
					if index == 1 && fail &&
						(exitCode != 17 || runErr == nil || !strings.Contains(string(output), "timer phase failed")) {
						t.Errorf("lost original subprocess failure: %d/%v/%s", exitCode, runErr, output)
					}
				})
			if (err != nil) != fail {
				t.Fatalf("batch error=%v", err)
			}
			log, readErr := os.ReadFile(filepath.Join(directory, "commands"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			lines := strings.Split(strings.TrimSpace(string(log)), "\n")
			want := 3
			if fail {
				want = 2
			}
			if len(lines) != want || len(events) != want || !strings.Contains(lines[0], "TestRFCCompliance") ||
				!strings.Contains(lines[1], "-test.run=^(TestTimerReload)$") {
				t.Fatalf("batch commands=%s events=%v", log, events)
			}
		})
	}
}
