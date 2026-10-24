//go:build interop_testcontainers

package interop_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var (
	errTimerEvidence           = errors.New("invalid timer reload evidence")
	errTimerReceiptBeforeFinal = errors.New("applied receipt does not follow completing Final")
)

type timerBaseReceipt struct {
	Created      int `json:"created"`
	Updated      int `json:"updated"`
	Released     int `json:"released"`
	Pending      int `json:"pending"`
	TimerPending int `json:"timer_pending"`
	Failed       int `json:"failed"`
}

type timerReceipt struct {
	PublishedAt time.Time        `json:"time"`
	Message     string           `json:"msg"`
	Desired     uint64           `json:"desired_generation"`
	Applied     uint64           `json:"applied_generation"`
	Stale       bool             `json:"stale"`
	Pending     int              `json:"pending"`
	Failed      int              `json:"failed"`
	Base        timerBaseReceipt `json:"base"`
}

// Receipt evidence is stricter than CLI compatibility decoding: ambiguous input cannot qualify a generation.
func timerJSONObject(data []byte, required ...string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("JSON UTF-8: %w", errTimerEvidence)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("JSON object opening: %w", errors.Join(err, errTimerEvidence))
	}
	values := make(map[string]json.RawMessage)
	for decoder.More() {
		token, tokenErr := decoder.Token()
		key, ok := token.(string)
		if tokenErr != nil || !ok {
			return nil, fmt.Errorf("JSON object key: %w", errors.Join(tokenErr, errTimerEvidence))
		}
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("duplicate JSON member %q: %w", key, errTimerEvidence)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("JSON member %s: %w", key, err)
		}
		if bytes.HasPrefix(bytes.TrimSpace(value), []byte("{")) {
			if _, err := timerJSONObject(value); err != nil {
				return nil, err
			}
		}
		values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("JSON object closing: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("JSON trailing input: %w", errTimerEvidence)
	}
	for _, key := range required {
		if value, ok := values[key]; !ok || bytes.Equal(value, []byte("null")) {
			return nil, fmt.Errorf("JSON missing %s: %w", key, errTimerEvidence)
		}
	}
	return values, nil
}

func parseTimerReceipts(input string) ([]timerReceipt, error) {
	var receipts []timerReceipt
	for line := range strings.SplitSeq(strings.TrimSpace(input), "\n") {
		fields, err := timerJSONObject([]byte(line), "msg")
		if err != nil {
			return nil, fmt.Errorf("daemon JSONL: %w", err)
		}
		var message string
		if err := json.Unmarshal(fields["msg"], &message); err != nil {
			return nil, fmt.Errorf("daemon message: %w", err)
		}
		if message != "configuration reconciliation incomplete" && message != "configuration reconciliation converged" {
			continue
		}
		if _, err := timerJSONObject([]byte(line),
			"time", "desired_generation", "applied_generation", "stale", "pending", "failed", "base"); err != nil {
			return nil, err
		}
		if _, err := timerJSONObject(fields["base"],
			"created", "updated", "released", "pending", "timer_pending", "failed"); err != nil {
			return nil, err
		}
		var timestamp string
		if err := json.Unmarshal(fields["time"], &timestamp); err != nil {
			return nil, fmt.Errorf("receipt publication time: %w", err)
		}
		if start := strings.IndexAny(timestamp, ".,"); start >= 0 {
			fraction := timestamp[start+1:]
			if end := strings.IndexAny(fraction, "Z+-"); end >= 0 {
				fraction = fraction[:end]
			}
			if timestamp[start] != '.' || len(fraction) == 0 || len(fraction) > 9 {
				return nil, fmt.Errorf("receipt publication precision %q: %w", timestamp, errTimerEvidence)
			}
		}
		var receipt timerReceipt
		if err := json.Unmarshal([]byte(line), &receipt); err != nil {
			return nil, fmt.Errorf("decode reconciliation receipt: %w", err)
		}
		if receipt.PublishedAt.IsZero() {
			return nil, fmt.Errorf("missing receipt publication time: %w", errTimerEvidence)
		}
		receipts = append(receipts, receipt)
	}
	if len(receipts) == 0 {
		return nil, fmt.Errorf("no reconciliation receipts: %w", errTimerEvidence)
	}
	return receipts, nil
}

// The phase declares receipt semantics; captured Poll/Final is checked separately.
type timerReceiptMode uint8

const (
	timerReceiptPoll timerReceiptMode = iota + 1
	timerReceiptImmediate
)

func timerGenerationApplied(receipts []timerReceipt, previous uint64, mode timerReceiptMode) (time.Time, error) {
	if mode != timerReceiptPoll && mode != timerReceiptImmediate {
		return time.Time{}, fmt.Errorf("invalid reload receipt mode %d: %w", mode, errTimerEvidence)
	}
	pending := false
	var applied time.Time
	for _, receipt := range receipts {
		if receipt.Desired <= previous {
			continue
		}
		if receipt.Desired != previous+1 || receipt.Failed != 0 || receipt.Base.Failed != 0 ||
			receipt.Base.Created != 0 || receipt.Base.Released != 0 {
			return time.Time{}, fmt.Errorf("unexpected timer generation receipt %+v: %w", receipt, errTimerEvidence)
		}
		if receipt.Stale {
			if receipt.Message != "configuration reconciliation incomplete" || receipt.Applied != previous ||
				receipt.Pending != 1 || receipt.Base.Pending != 1 || receipt.Base.TimerPending != 1 ||
				receipt.Base.Updated != 0 || !applied.IsZero() {
				return time.Time{}, fmt.Errorf("unexpected pending receipt %+v: %w", receipt, errTimerEvidence)
			}
			pending = true
			continue
		}
		if receipt.Message != "configuration reconciliation converged" || receipt.Applied != previous+1 ||
			receipt.Pending != 0 || receipt.Base.Pending != 0 || receipt.Base.TimerPending != 0 || receipt.Base.Updated != 1 ||
			(mode == timerReceiptPoll && !pending) {
			return time.Time{}, fmt.Errorf("unexpected applied receipt %+v: %w", receipt, errTimerEvidence)
		}
		if applied.IsZero() || receipt.PublishedAt.Before(applied) {
			applied = receipt.PublishedAt
		}
	}
	return applied, nil
}

func parseTimerCounter(input, label string) (uint64, error) {
	var value uint64
	count := 0
	for line := range strings.SplitSeq(input, "\n") {
		key, raw, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key != label {
			continue
		}
		count++
		parsed, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", label, err)
		}
		value = parsed
	}
	if count != 1 {
		return 0, fmt.Errorf("%s rows=%d, want 1: %w", label, count, errTimerEvidence)
	}
	return value, nil
}

func timerConfig(baseline, peer, tx, rx string) (string, error) {
	start, end, err := timerPeerBlock(baseline, peer)
	if err != nil {
		return "", err
	}
	block := baseline[start:end]
	for _, field := range []struct{ name, value string }{{"desired_min_tx", tx}, {"required_min_rx", rx}} {
		old := "    " + field.name + ": \"300ms\""
		if strings.Count(block, old) != 1 {
			return "", fmt.Errorf("unique baseline %s for %s: %w", field.name, peer, errTimerEvidence)
		}
		block = strings.Replace(block, old, "    "+field.name+": \""+field.value+"\"", 1)
	}
	return baseline[:start] + block + baseline[end:], nil
}

func timerPeerBlock(baseline, peer string) (int, int, error) {
	marker := "  - peer: \"" + peer + "\"\n"
	if strings.Count(baseline, marker) != 1 {
		return 0, 0, fmt.Errorf("unique peer block %s: %w", peer, errTimerEvidence)
	}
	start := strings.Index(baseline, marker)
	end := len(baseline)
	if next := strings.Index(baseline[start+len(marker):], "\n  - peer:"); next >= 0 {
		end = start + len(marker) + next
	}
	return start, end, nil
}

type timerFrame struct {
	Number                                              uint64
	Epoch                                               time.Time
	Source, Destination                                 string
	State, Poll, Final, Mine, Yours, TX, RX, Multiplier uint64
}

func timerFrameFields() []string {
	return []string{
		"frame.number", "frame.time_epoch", "ip.src", "ip.dst", "bfd.sta", "bfd.flags.p", "bfd.flags.f",
		"bfd.my_discriminator", "bfd.your_discriminator", "bfd.desired_min_tx_interval",
		"bfd.required_min_rx_interval", "bfd.detect_time_multiplier",
	}
}

func parseTimerEpoch(raw string) (time.Time, error) {
	seconds, fraction, fractional := strings.Cut(raw, ".")
	if seconds == "" || (fractional && fraction == "") || len(fraction) > 9 ||
		strings.Trim(seconds+fraction, "0123456789") != "" {
		return time.Time{}, fmt.Errorf("frame epoch %q: %w", raw, errTimerEvidence)
	}
	whole, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil || whole <= 0 {
		return time.Time{}, fmt.Errorf("frame epoch seconds %q: %w", raw, errors.Join(err, errTimerEvidence))
	}
	nanoseconds, err := strconv.ParseInt(fraction+strings.Repeat("0", 9-len(fraction)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("frame epoch fraction %q: %w", raw, err)
	}
	return time.Unix(whole, nanoseconds), nil
}

// parseTimerField parses a decimal, hexadecimal or tshark boolean field.
func parseTimerField(field string) (uint64, error) {
	switch field {
	case "True":
		field = "1"
	case "False":
		field = "0"
	}
	base := 10
	if strings.HasPrefix(field, "0x") {
		base = 0
	}
	value, err := strconv.ParseUint(field, base, 64)
	if err != nil {
		return 0, fmt.Errorf("parse capture field %q: %w", field, err)
	}
	return value, nil
}

func parseTimerFrames(rows [][]string) ([]timerFrame, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty phase capture: %w", errTimerEvidence)
	}
	fieldNames := timerFrameFields()
	frames := make([]timerFrame, 0, len(rows))
	for _, row := range rows {
		if len(row) != len(fieldNames) {
			return nil, fmt.Errorf("frame columns=%d, want %d: %w", len(row), len(fieldNames), errTimerEvidence)
		}
		frame := timerFrame{Source: row[2], Destination: row[3]}
		var err error
		frame.Epoch, err = parseTimerEpoch(row[1])
		if err != nil {
			return nil, err
		}
		values := []*uint64{
			&frame.Number, &frame.State, &frame.Poll, &frame.Final, &frame.Mine, &frame.Yours,
			&frame.TX, &frame.RX, &frame.Multiplier,
		}
		columns := []int{0, 4, 5, 6, 7, 8, 9, 10, 11}
		for index, column := range columns {
			*values[index], err = parseTimerField(row[column])
			if err != nil {
				return nil, fmt.Errorf("frame field %s=%q: %w", fieldNames[column], row[column], err)
			}
		}
		if frame.Number == 0 || frame.Poll > 1 || frame.Final > 1 || frame.Poll+frame.Final > 1 ||
			(len(frames) != 0 && (frame.Number <= frames[len(frames)-1].Number ||
				frame.Epoch.Before(frames[len(frames)-1].Epoch))) {
			return nil, fmt.Errorf("invalid frame order/flags %+v: %w", frame, errTimerEvidence)
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

type timerPhase struct {
	Peer      string    `json:"peer"`
	Start     uint64    `json:"start"`
	End       uint64    `json:"end"`
	Converged uint64    `json:"converged"`
	Local     uint32    `json:"local"`
	Remote    uint32    `json:"remote"`
	TX        uint64    `json:"tx"`
	RX        uint64    `json:"rx"`
	RemoteRX  uint64    `json:"remote_rx"`
	RXZero    bool      `json:"rx_zero"`
	AppliedAt time.Time `json:"applied_at"`
}

type timerWireResult struct {
	Poll                  uint64  `json:"poll"`
	Final                 uint64  `json:"final"`
	Intervals             int     `json:"intervals"`
	MeanMicroseconds      float64 `json:"mean_microseconds"`
	RemoteTX              uint64  `json:"remote_tx"`
	DetectionMicroseconds uint64  `json:"detection_microseconds"`
}

func evaluateTimerPhase(frames []timerFrame, phase timerPhase) (timerWireResult, error) {
	var result timerWireResult
	var previous timerFrame
	var intervals []int64
	if phase.Start >= phase.End || phase.Local == 0 || phase.Remote == 0 || len(frames) == 0 || phase.AppliedAt.IsZero() {
		return result, fmt.Errorf("invalid/empty phase %+v: %w", phase, errTimerEvidence)
	}
	for _, frame := range frames {
		if frame.Number <= phase.Start || frame.Number > phase.End {
			return result, fmt.Errorf("frame %d outside (%d,%d]: %w", frame.Number, phase.Start, phase.End, errTimerEvidence)
		}
		local := frame.Source == gobfdIP && frame.Destination == phase.Peer
		remote := frame.Source == phase.Peer && frame.Destination == gobfdIP
		mine, yours := uint64(phase.Local), uint64(phase.Remote)
		if remote {
			mine, yours = yours, mine
		}
		if (!local && !remote) || frame.Poll > 1 || frame.Final > 1 || frame.Poll+frame.Final > 1 {
			return result, fmt.Errorf("phase direction/flags %+v: %w", frame, errTimerEvidence)
		}
		if !phase.RXZero || result.Final == 0 {
			if frame.State != 3 || frame.Mine != mine || frame.Yours != yours {
				return result, fmt.Errorf("phase state/identity %+v: %w", frame, errTimerEvidence)
			}
		}
		if remote && frame.State == 3 {
			if frame.RX != phase.RemoteRX || frame.TX != 300000 || frame.Multiplier != 3 ||
				(result.RemoteTX != 0 && result.RemoteTX != frame.TX) {
				return result, fmt.Errorf("remote timer tuple %+v: %w", frame, errTimerEvidence)
			}
			result.RemoteTX = frame.TX
		}
		if local && frame.Poll == 1 && frame.Final == 0 && frame.TX == phase.TX && frame.RX == phase.RX && result.Poll == 0 {
			result.Poll = frame.Number
		}
		if remote && frame.Final == 1 && frame.Poll == 0 && result.Poll != 0 && result.Final == 0 {
			// TC daemon JSON and namespace capture share the same host realtime clock.
			if frame.Epoch.IsZero() || !phase.AppliedAt.After(frame.Epoch) {
				return result, fmt.Errorf("applied=%s Final=%s: %w: %w",
					phase.AppliedAt.Format(time.RFC3339Nano), frame.Epoch.Format(time.RFC3339Nano),
					errTimerReceiptBeforeFinal, errTimerEvidence)
			}
			result.Final = frame.Number
		}
		if local && result.Poll != 0 && (!phase.RXZero || result.Final == 0) &&
			(frame.TX != phase.TX || frame.RX != phase.RX || frame.Multiplier != 3) {
			return result, fmt.Errorf("local timer tuple %+v: %w", frame, errTimerEvidence)
		}
		// The observed capture watermark only excludes samples; actual publication time establishes convergence.
		if phase.RXZero || !local || frame.Number <= max(result.Final, phase.Converged) || result.Final == 0 ||
			!frame.Epoch.After(phase.AppliedAt) {
			continue
		}
		if frame.Poll != 0 || frame.Final != 0 {
			previous = timerFrame{}
			intervals = nil
			continue
		}
		if previous.Number != 0 {
			intervals = append(intervals, frame.Epoch.Sub(previous.Epoch).Microseconds())
		}
		previous = frame
	}
	if result.Poll == 0 || result.Final <= result.Poll || result.RemoteTX == 0 {
		return result, fmt.Errorf("missing ordered Poll/Final/remote tuple %+v: %w", result, errTimerEvidence)
	}
	result.DetectionMicroseconds = 3 * max(phase.RX, result.RemoteTX)
	if phase.RXZero {
		return result, nil
	}
	mean, err := checkJitteredIntervals(intervals, int64(max(phase.TX, phase.RemoteRX)), 3)
	if err != nil {
		return result, err
	}
	result.Intervals = len(intervals)
	result.MeanMicroseconds = mean
	return result, nil
}

// timerSlack covers timer wakeup and capture latency for an interval in microseconds.
func timerSlack(expected int64) int64 {
	return max(2000, expected/50)
}

// RFC 5880 section 6.8.7 caps DetectMult 1 at 90%, other multipliers at 100%.
func checkIntervalBounds(intervals []int64, expected int64, multiplier uint64) error {
	if expected <= 0 || expected > 1<<32-1 || multiplier == 0 || multiplier > 255 {
		return fmt.Errorf("invalid jitter target %d multiplier %d: %w", expected, multiplier, errTimerEvidence)
	}
	slack := timerSlack(expected)
	low, high := expected*3/4-slack, expected+slack
	if multiplier == 1 {
		high = expected*9/10 + slack
	}
	for _, interval := range intervals {
		if interval < low || interval > high {
			return fmt.Errorf("interval %d outside [%d,%d] us: %w", interval, low, high, errTimerEvidence)
		}
	}
	return nil
}

// checkJitteredIntervals requires at least 12 bounded intervals and returns their mean.
func checkJitteredIntervals(intervals []int64, expected int64, multiplier uint64) (float64, error) {
	if len(intervals) < 12 {
		return 0, fmt.Errorf("stable intervals=%d, want at least 12: %w", len(intervals), errTimerEvidence)
	}
	if err := checkIntervalBounds(intervals, expected, multiplier); err != nil {
		return 0, err
	}
	var sum int64
	for _, interval := range intervals {
		sum += interval
	}
	// Conservative standard deviations bound uniform 75..100% and 75..90% draws.
	mean, sigma := 0.875, 0.075
	if multiplier == 1 {
		mean, sigma = 0.825, 0.045
	}
	n := int64(len(intervals))
	deviation := float64(sum) - mean*float64(n*expected)
	bound := 3 * sigma * float64(expected) * math.Sqrt(float64(n))
	// Timer rearm adds wakeup latency, so only the upper side carries slack.
	if deviation < -bound || deviation > bound+float64(n*timerSlack(expected)) {
		return 0, fmt.Errorf("mean interval %d us deviates from %g*%d us beyond 3 standard errors (n=%d): %w",
			sum/n, mean, expected, n, errTimerEvidence)
	}
	return float64(sum) / float64(n), nil
}

func writeTimerArtifact(t *testing.T, name, extension string, data []byte) string {
	t.Helper()
	path, err := persistTimerArtifact(name, extension, data)
	if err != nil {
		t.Fatalf("write timer artifact: %v", err)
	}
	return path
}

func persistTimerArtifact(name, extension string, data []byte) (string, error) {
	directory := strings.TrimSpace(os.Getenv("INTEROP_TESTCONTAINERS_ARTIFACT_DIR"))
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("absolute INTEROP_TESTCONTAINERS_ARTIFACT_DIR required, got %q: %w",
			directory, errTimerEvidence)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create timer artifact directory: %w", err)
	}
	path := filepath.Join(directory, fmt.Sprintf("%s-%d.%s", name, time.Now().UnixNano(), extension))
	for _, artifact := range []struct {
		path string
		data []byte
	}{
		{path, data}, {path + ".sha256", []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(data), filepath.Base(path)))},
	} {
		file, err := os.OpenFile(artifact.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", fmt.Errorf("create artifact %s: %w", artifact.path, err)
		}
		_, writeErr := file.Write(artifact.data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return "", fmt.Errorf("write artifact %s: %w", artifact.path, err)
		}
	}
	return path, nil
}

func copyTimerConfig(ctx context.Context, name, source, destination string) error {
	id, err := resolveProjectContainerID(ctx, name)
	if err != nil {
		return err
	}
	output, err := podmanCommand(ctx, "cp", source, id+":"+destination)
	if err != nil {
		return fmt.Errorf("copy test-owned config into %s: %w: %s", id, err, output)
	}
	return nil
}

func signalTimerReload(ctx context.Context) (string, error) {
	id, err := resolveProjectContainerID(ctx, "gobfd-interop")
	if err != nil {
		return "", err
	}
	output, err := podmanCommand(ctx, "kill", "--signal", "HUP", id)
	if err != nil {
		return output, fmt.Errorf("signal HUP to %s: %w", id, err)
	}
	return output, nil
}

func timerTraffic(peer string) string {
	return "bfd && ((ip.src == " + gobfdIP + " && ip.dst == " + peer + ") || " +
		"(ip.src == " + peer + " && ip.dst == " + gobfdIP + "))"
}

func timerSnapshot(ctx context.Context, t *testing.T, peer, name string) (sessionState, [2]uint64) {
	t.Helper()
	output, err := projectContainerCommand(ctx, "gobfd-interop",
		"exec", "/bin/gobfdctl", "--addr", "127.0.0.1:50051", "session", "show", peer, "--format", "json")
	writeTimerArtifact(t, name+"-status", "json", []byte(output))
	if err != nil {
		t.Fatalf("timer snapshot: %v", err)
	}
	state, err := parseSessionState([]byte(output), peer)
	if err != nil {
		t.Fatalf("parse timer snapshot: %v", err)
	}
	text, err := projectContainerCommand(ctx, "gobfd-interop",
		"exec", "/bin/gobfdctl", "--addr", "127.0.0.1:50051", "session", "show", peer)
	writeTimerArtifact(t, name+"-status", "txt", []byte(text))
	if err != nil {
		t.Fatalf("timer text counters: %v", err)
	}
	var counters [2]uint64
	for index, label := range []string{"State Transitions", "Auth Failures"} {
		counters[index], err = parseTimerCounter(text, label)
		if err != nil {
			t.Fatalf("timer counters: %v", err)
		}
	}
	return state, counters
}

func readTimerReceipts(ctx context.Context, t *testing.T, name string) []timerReceipt {
	t.Helper()
	logs, err := projectContainerLogs(ctx, "gobfd-interop")
	writeTimerArtifact(t, name+"-receipts", "jsonl", []byte(logs))
	if err != nil {
		t.Fatalf("read timer receipts: %v", err)
	}
	receipts, err := parseTimerReceipts(logs)
	if err != nil {
		t.Fatalf("parse timer receipts: %v", err)
	}
	return receipts
}

// Preserve the poll result before detached evidence reads; neither artifact I/O nor rereading may replace it.
func retainTimerPollEvidence(ctx context.Context, name string, generation uint64, lastRaw string, pollErr error) error {
	_, lastWriteErr := persistTimerArtifact(name+"-last-poll", "jsonl", []byte(lastRaw))
	evidenceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	logs, readErr := projectContainerLogs(evidenceCtx, "gobfd-interop")
	_, writeErr := persistTimerArtifact(name+"-receipts", "jsonl", []byte(logs))
	var parseErr error
	if readErr == nil {
		_, parseErr = parseTimerReceipts(logs)
	}
	result := errors.Join(pollErr, lastWriteErr, readErr, writeErr, parseErr)
	summary, marshalErr := json.Marshal(struct {
		Generation uint64 `json:"generation"`
		PollError  string `json:"poll_error"`
		ReadError  string `json:"read_error"`
		Error      string `json:"error"`
	}{
		generation, fmt.Sprint(pollErr), fmt.Sprint(readErr), fmt.Sprint(result),
	})
	var summaryErr error
	if marshalErr == nil {
		_, summaryErr = persistTimerArtifact(name+"-generation", "json", summary)
	}
	return errors.Join(result, marshalErr, summaryErr)
}

func waitTimerQuiet(ctx context.Context, peer string, remoteRX uint64) error {
	boundary, err := lastFrameForFilter(ctx, timerTraffic(peer))
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err = pollUntil(waitCtx, 200*time.Millisecond, func(pollCtx context.Context) (int, bool, error) {
		rows, queryErr := tsharkFields(pollCtx, frameBoundary(timerTraffic(peer), boundary), timerFrameFields(), 0)
		if queryErr != nil || len(rows) == 0 {
			return 0, false, queryErr
		}
		frames, parseErr := parseTimerFrames(rows)
		if parseErr != nil {
			return 0, false, parseErr
		}
		local, remote := 0, 0
		for _, frame := range frames {
			if frame.State != 3 || frame.Poll != 0 || frame.Final != 0 {
				local, remote = 0, 0
				continue
			}
			if frame.Source == peer {
				if frame.RX != remoteRX || frame.TX != 300000 {
					remote = 0
					continue
				}
				remote++
			} else {
				if frame.TX != 300000 || frame.RX != 300000 {
					local = 0
					continue
				}
				local++
			}
		}
		return local + remote, local >= 4 && remote >= 4, nil
	})
	return err
}

func runTimerGeneration(
	ctx context.Context, t *testing.T, baseline, peer, name, tx, rx string, remoteRX uint64, rxZero bool,
) {
	t.Helper()
	phaseCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	before, counters := timerSnapshot(phaseCtx, t, peer, name+"-before")
	if before.LocalState != "Up" || before.RemoteState != "Up" ||
		before.LocalDiscriminator == 0 || before.RemoteDiscriminator == 0 {
		t.Fatalf("timer phase requires established Up identity: %+v", before)
	}
	receipts := readTimerReceipts(phaseCtx, t, name+"-before")
	last := receipts[len(receipts)-1]
	if last.Stale || last.Desired == 0 || last.Desired != last.Applied || last.Pending != 0 || last.Failed != 0 {
		t.Fatalf("timer phase requires converged previous generation: %+v", last)
	}
	start, err := lastFrameForFilter(phaseCtx, timerTraffic(peer))
	if err != nil {
		t.Fatalf("timer phase baseline: %v", err)
	}
	config, err := timerConfig(baseline, peer, tx, rx)
	if err != nil {
		t.Fatalf("timer config: %v", err)
	}
	path := writeTimerArtifact(t, name+"-config", "yml", []byte(config))
	if copyErr := copyTimerConfig(phaseCtx, "gobfd-interop", path, "/etc/gobfd/gobfd.yml"); copyErr != nil {
		t.Fatalf("timer config copy: %v", copyErr)
	}
	output, err := signalTimerReload(phaseCtx)
	writeTimerArtifact(t, name+"-hup", "txt", []byte(fmt.Sprintf("output=%s\nerror=%v\n", output, err)))
	if err != nil {
		t.Fatalf("timer HUP: %v", err)
	}
	var lastRaw string
	var appliedAt time.Time
	_, err = pollUntil(phaseCtx, 100*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
		logs, logErr := projectContainerLogs(pollCtx, "gobfd-interop")
		if logs != "" {
			lastRaw = logs
		}
		if logErr != nil {
			return false, false, logErr
		}
		parsed, parseErr := parseTimerReceipts(logs)
		if parseErr != nil {
			return false, false, parseErr
		}
		var receiptErr error
		appliedAt, receiptErr = timerGenerationApplied(parsed, last.Applied, timerReceiptPoll)
		return !appliedAt.IsZero(), !appliedAt.IsZero(), receiptErr
	})
	if evidenceErr := retainTimerPollEvidence(phaseCtx, name+"-after", last.Applied+1, lastRaw, err); evidenceErr != nil {
		t.Fatalf("timer generation %d pending/applied: %v", last.Applied+1, evidenceErr)
	}
	converged, err := lastFrameForFilter(phaseCtx, timerTraffic(peer))
	if err != nil {
		t.Fatalf("timer convergence boundary: %v", err)
	}
	txDuration, err := time.ParseDuration(tx)
	if err != nil {
		t.Fatalf("timer TX: %v", err)
	}
	rxDuration, err := time.ParseDuration(rx)
	if err != nil {
		t.Fatalf("timer RX: %v", err)
	}
	phase := timerPhase{
		Peer: peer, Start: start, Converged: converged, AppliedAt: appliedAt, Local: before.LocalDiscriminator,
		Remote: before.RemoteDiscriminator, TX: uint64(txDuration / time.Microsecond),
		RX: uint64(rxDuration / time.Microsecond), RemoteRX: remoteRX, RXZero: rxZero,
	}
	fieldNames := timerFrameFields()
	var rows [][]string
	var result timerWireResult
	var wireErr error
	_, err = pollUntil(phaseCtx, 200*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
		end, readErr := lastFrameForFilter(pollCtx, timerTraffic(peer))
		if readErr != nil {
			return false, false, readErr
		}
		phase.End = end
		filter := fmt.Sprintf("%s && frame.number <= %d", frameBoundary(timerTraffic(peer), start), end)
		// Keep the last successful window for the frames artifact when a read fails.
		windowRows, readErr := tsharkFields(pollCtx, filter, fieldNames, 0)
		if readErr != nil {
			return false, false, readErr
		}
		rows = windowRows
		frames, parseErr := parseTimerFrames(rows)
		if parseErr != nil {
			return false, false, parseErr
		}
		result, wireErr = evaluateTimerPhase(frames, phase)
		return wireErr == nil, wireErr == nil, nil
	})
	if err != nil {
		err = errors.Join(err, wireErr)
	}
	var raw strings.Builder
	raw.WriteString(strings.Join(fieldNames, "\t") + "\n")
	for _, row := range rows {
		raw.WriteString(strings.Join(row, "\t") + "\n")
	}
	writeTimerArtifact(t, name+"-frames", "tsv", []byte(raw.String()))
	summary, marshalErr := json.Marshal(struct {
		Generation uint64          `json:"generation"`
		Phase      timerPhase      `json:"phase"`
		Wire       timerWireResult `json:"wire"`
		Error      string          `json:"error"`
	}{last.Applied + 1, phase, result, fmt.Sprint(err)})
	if marshalErr != nil {
		t.Fatalf("timer phase summary: %v", marshalErr)
	}
	writeTimerArtifact(t, name+"-phase", "json", summary)
	if err != nil {
		t.Fatalf("timer phase wire evidence: %v", err)
	}
	if !rxZero {
		after, afterCounters := timerSnapshot(phaseCtx, t, peer, name+"-after")
		if after.LocalState != "Up" || after.RemoteState != "Up" || after.LocalDiscriminator != before.LocalDiscriminator ||
			after.RemoteDiscriminator != before.RemoteDiscriminator || afterCounters != counters {
			t.Fatalf("timer phase identity/counters changed: before=%+v/%v after=%+v/%v", before, counters, after, afterCounters)
		}
		for _, field := range []struct {
			raw  string
			want time.Duration
		}{
			{after.DesiredMinTx, txDuration},
			{after.RequiredMinRx, rxDuration},
			{after.RemoteMinRx, time.Duration(remoteRX) * time.Microsecond},
			{after.NegotiatedTx, time.Duration(max(phase.TX, remoteRX)) * time.Microsecond},
			{after.DetectionTime, time.Duration(result.DetectionMicroseconds) * time.Microsecond},
		} {
			got, parseErr := time.ParseDuration(field.raw)
			if parseErr != nil || got != field.want {
				t.Fatalf("timer status interval %q=%v, want %v: %v", field.raw, got, field.want, parseErr)
			}
		}
	}
}

func setTimerPeerRX(ctx context.Context, t *testing.T, peer, birdBaseline string, milliseconds int) {
	t.Helper()
	var output string
	var err error
	if peer == frrIP {
		output, err = frrVtyshConfig(ctx,
			"configure terminal", "bfd", "peer "+gobfdIP, "receive-interval "+strconv.Itoa(milliseconds))
	} else {
		if strings.Count(birdBaseline, "min rx interval 300 ms;") != 1 {
			t.Fatal("BIRD baseline requires exactly one RX floor")
		}
		floor := fmt.Sprintf("min rx interval %d ms;", milliseconds)
		config := strings.Replace(birdBaseline, "min rx interval 300 ms;", floor, 1)
		path := writeTimerArtifact(t, fmt.Sprintf("bird-rx-%d", milliseconds), "conf", []byte(config))
		if copyErr := copyTimerConfig(ctx, "bird3-interop", path, "/etc/bird/timer-reload.conf"); copyErr != nil {
			t.Fatalf("copy BIRD timer config: %v", copyErr)
		}
		output, err = projectContainerCommand(ctx, "bird3-interop",
			"exec", "birdc", "configure", "check", `"/etc/bird/timer-reload.conf"`)
		writeTimerArtifact(t, "bird-config-check", "txt",
			[]byte(fmt.Sprintf("%s\nerror=%v\n", output, err)))
		if err != nil || !strings.Contains(output, "Configuration OK") {
			t.Fatalf("BIRD config check: error=%v output=%s", err, output)
		}
		output, err = projectContainerCommand(ctx, "bird3-interop",
			"exec", "birdc", "configure", `"/etc/bird/timer-reload.conf"`)
		// BIRD CLI file names are quoted strings; CONF_DONE and CONF_PROGRESS both accept the file.
		if err == nil && !strings.Contains(output, "Reconfigured") &&
			!strings.Contains(output, "Reconfiguration in progress") {
			err = fmt.Errorf("BIRD configure acknowledgement: %w", errTimerEvidence)
		}
	}
	writeTimerArtifact(t, "peer-rx-command", "txt",
		[]byte(fmt.Sprintf("peer=%s rx_ms=%d\n%s\nerror=%v\n", peer, milliseconds, output, err)))
	if err != nil || strings.Contains(output, "%") {
		t.Fatalf("peer RX floor: error=%v output=%s", err, output)
	}
	if err := waitTimerQuiet(ctx, peer, uint64(milliseconds)*1000); err != nil {
		t.Fatalf("peer RX floor convergence: %v", err)
	}
}

func restoreTimerBaseline(ctx context.Context, t *testing.T, baseline []byte, name string) {
	t.Helper()
	receipts := readTimerReceipts(ctx, t, name+"-before")
	generation := receipts[len(receipts)-1].Desired + 1
	path := writeTimerArtifact(t, name, "yml", baseline)
	if err := copyTimerConfig(ctx, "gobfd-interop", path, "/etc/gobfd/gobfd.yml"); err != nil {
		t.Fatalf("restore daemon baseline: %v", err)
	}
	output, err := signalTimerReload(ctx)
	writeTimerArtifact(t, name+"-hup", "txt",
		[]byte(fmt.Sprintf("generation=%d\noutput=%s\nerror=%v\n", generation, output, err)))
	if err != nil {
		t.Fatalf("restore daemon HUP: %v", err)
	}
	var lastRaw string
	_, err = pollUntil(ctx, 100*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
		logs, readErr := projectContainerLogs(pollCtx, "gobfd-interop")
		if logs != "" {
			lastRaw = logs
		}
		if readErr != nil {
			return false, false, readErr
		}
		parsed, parseErr := parseTimerReceipts(logs)
		if parseErr != nil {
			return false, false, parseErr
		}
		for _, receipt := range parsed {
			if receipt.Desired < generation {
				continue
			}
			if receipt.Desired != generation || receipt.Failed != 0 || receipt.Base.Failed != 0 ||
				receipt.Base.Created != 0 || receipt.Base.Released != 0 {
				return false, false, fmt.Errorf("restore generation receipt %+v: %w", receipt, errTimerEvidence)
			}
			if !receipt.Stale && receipt.Desired == receipt.Applied && receipt.Pending == 0 &&
				receipt.Base.Pending == 0 && receipt.Base.TimerPending == 0 {
				return true, true, nil
			}
		}
		return false, false, nil
	})
	if evidenceErr := retainTimerPollEvidence(ctx, name+"-after", generation, lastRaw, err); evidenceErr != nil {
		t.Fatalf("restore generation %d: %v", generation, evidenceErr)
	}
}

func TestTimerReload(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	requireCaptureFields(ctx, t, detectFrameFields())
	baseline, err := os.ReadFile("gobfd/gobfd.yml")
	if err != nil {
		t.Fatalf("read immutable daemon baseline: %v", err)
	}
	bird, err := os.ReadFile("bird3/bird.conf")
	if err != nil {
		t.Fatalf("read immutable BIRD baseline: %v", err)
	}
	// Restore from the saved baseline even when a phase aborts; never edit tracked fixtures.
	t.Cleanup(func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.WithoutCancel(ctx), 35*time.Second)
		defer restoreCancel()
		restoreTimerBaseline(restoreCtx, t, baseline, "cleanup-baseline")
		for _, peer := range []string{frrIP, bird3IP} {
			setTimerPeerRX(restoreCtx, t, peer, string(bird), 300)
		}
	})
	for _, peer := range []struct{ name, address string }{{"FRR", frrIP}, {"BIRD3", bird3IP}} {
		if !t.Run(peer.name, func(t *testing.T) {
			before, counters := timerSnapshot(ctx, t, peer.address, peer.name+"-floor-before")
			setTimerPeerRX(ctx, t, peer.address, string(bird), 100)
			after, afterCounters := timerSnapshot(ctx, t, peer.address, peer.name+"-floor-after")
			if after.LocalState != "Up" || after.RemoteState != "Up" ||
				before.LocalDiscriminator == 0 || before.RemoteDiscriminator == 0 ||
				before.LocalDiscriminator != after.LocalDiscriminator ||
				before.RemoteDiscriminator != after.RemoteDiscriminator || counters != afterCounters {
				t.Fatalf("peer RX floor changed identity/counters: before=%+v/%v after=%+v/%v",
					before, counters, after, afterCounters)
			}
			for _, interval := range []string{"100ms", "500ms", "300ms"} {
				if !t.Run(interval, func(t *testing.T) {
					runTimerGeneration(ctx, t, string(baseline), peer.address, peer.name+"-"+interval,
						interval, interval, 100000, false)
				}) {
					return
				}
			}
			setTimerPeerRX(ctx, t, peer.address, string(bird), 300)
			restored, restoredCounters := timerSnapshot(ctx, t, peer.address, peer.name+"-floor-restored")
			if restored.LocalDiscriminator != before.LocalDiscriminator ||
				restored.RemoteDiscriminator != before.RemoteDiscriminator || restoredCounters != counters {
				t.Fatalf("peer RX restoration changed identity/counters: before=%+v/%v after=%+v/%v",
					before, counters, restored, restoredCounters)
			}
			for _, multiplier := range []uint64{1, 5, 3} {
				runDetectGeneration(ctx, t, string(baseline), peer.address,
					fmt.Sprintf("%s-detect-%d", peer.name, multiplier), multiplier, timerReceiptImmediate)
			}
		}) {
			return
		}
	}
	for _, peer := range []struct{ name, address string }{{"FRR", frrIP}, {"BIRD3", bird3IP}} {
		if !t.Run(peer.name+"-RX0", func(t *testing.T) {
			if err := waitTimerQuiet(ctx, peer.address, 300000); err != nil {
				t.Fatalf("RX0 baseline: %v", err)
			}
			runTimerGeneration(ctx, t, string(baseline), peer.address, peer.name+"-RX0", "300ms", "0s", 300000, true)
			restoreTimerBaseline(ctx, t, baseline, peer.name+"-RX0-restore")
			if err := waitTimerQuiet(ctx, peer.address, 300000); err != nil {
				t.Fatalf("RX0 restore convergence: %v", err)
			}
			readTimerReceipts(ctx, t, peer.name+"-RX0-restored")
		}) {
			return
		}
	}
}
