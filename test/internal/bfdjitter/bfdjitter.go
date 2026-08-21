// Package bfdjitter validates BFD transmit jitter from tshark TSV output.
package bfdjitter

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	fieldCount       = 4
	minimumUpPackets = 10
	minimumSamples   = 5
	minimumDelta     = 0.150
	maximumDelta     = 0.400
	upState          = 3
)

var (
	errMalformedRow       = errors.New("malformed jitter TSV row")
	errInvalidEpoch       = errors.New("invalid jitter epoch")
	errNonIncreasingEpoch = errors.New("jitter epoch is not increasing")
	errInvalidState       = errors.New("invalid BFD state")
	errInvalidFlag        = errors.New("invalid BFD flag")
	errInvalidFlags       = errors.New("invalid BFD flag combination")
)

// Status describes the jitter assessment outcome.
type Status string

const (
	// StatusPass indicates that every eligible periodic sample is within bounds.
	StatusPass Status = "pass"
	// StatusFail indicates that an eligible periodic sample is outside bounds.
	StatusFail Status = "fail"
	// StatusSkip indicates that the stream has insufficient eligible samples.
	StatusSkip Status = "skip"
)

// Report describes continuous-Up jitter samples and their assessment.
type Report struct {
	Status    Status
	Reason    string
	UpPackets int
	Samples   int
	MinDelta  float64
	MaxDelta  float64
}

type analyzer struct {
	report                 Report
	previousEpoch          float64
	previousRegularUpEpoch float64
	havePreviousEpoch      bool
	havePreviousRegularUp  bool
}

type packet struct {
	epoch float64
	state uint64
	final bool
}

// Evaluate parses one frame.time_epoch,bfd.sta,bfd.flags.p,bfd.flags.f TSV stream and assesses jitter.
func Evaluate(input io.Reader) (Report, error) {
	var analysis analyzer

	scanner := bufio.NewScanner(input)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		parsed, err := parseRow(scanner.Text(), lineNumber)
		if err != nil {
			return Report{}, err
		}
		if err := analysis.add(parsed, lineNumber); err != nil {
			return Report{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return Report{}, fmt.Errorf("scan jitter TSV: %w", err)
	}
	return analysis.finish(), nil
}

func parseRow(line string, lineNumber int) (packet, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != fieldCount {
		return packet{}, fmt.Errorf(
			"parse jitter TSV row %d: got %d fields, want %d: %w",
			lineNumber, len(fields), fieldCount, errMalformedRow,
		)
	}
	epoch, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
	if err != nil {
		return packet{}, fmt.Errorf("parse jitter epoch at row %d: %q: %w", lineNumber, fields[0], err)
	}
	if math.IsNaN(epoch) || math.IsInf(epoch, 0) || epoch < 0 {
		return packet{}, fmt.Errorf("parse jitter epoch at row %d: %q: %w", lineNumber, fields[0], errInvalidEpoch)
	}
	state, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 0, 8)
	if err != nil {
		return packet{}, fmt.Errorf("parse BFD state at row %d: %q: %w", lineNumber, fields[1], err)
	}
	if state > upState {
		return packet{}, fmt.Errorf("parse BFD state at row %d: %q: %w", lineNumber, fields[1], errInvalidState)
	}
	poll, err := parseFlag(fields[2], "Poll", lineNumber)
	if err != nil {
		return packet{}, err
	}
	final, err := parseFlag(fields[3], "Final", lineNumber)
	if err != nil {
		return packet{}, err
	}
	if poll && final {
		return packet{}, fmt.Errorf("parse BFD flags at row %d: Poll and Final are both set: %w", lineNumber, errInvalidFlags)
	}
	return packet{epoch: epoch, state: state, final: final}, nil
}

func parseFlag(raw, name string, lineNumber int) (bool, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 0, 8)
	if err != nil {
		return false, fmt.Errorf("parse BFD %s flag at row %d: %q: %w", name, lineNumber, raw, err)
	}
	if value > 1 {
		return false, fmt.Errorf("parse BFD %s flag at row %d: %q: %w", name, lineNumber, raw, errInvalidFlag)
	}
	return value == 1, nil
}

func (analysis *analyzer) add(parsed packet, lineNumber int) error {
	if analysis.havePreviousEpoch && parsed.epoch <= analysis.previousEpoch {
		return fmt.Errorf(
			"parse jitter epoch at row %d: %.9f is not after %.9f: %w",
			lineNumber, parsed.epoch, analysis.previousEpoch, errNonIncreasingEpoch,
		)
	}
	analysis.previousEpoch = parsed.epoch
	analysis.havePreviousEpoch = true
	if parsed.state != upState {
		analysis.havePreviousRegularUp = false
		return nil
	}
	analysis.report.UpPackets++
	if parsed.final {
		return nil
	}
	if !analysis.havePreviousRegularUp {
		analysis.previousRegularUpEpoch = parsed.epoch
		analysis.havePreviousRegularUp = true
		return nil
	}
	delta := parsed.epoch - analysis.previousRegularUpEpoch
	analysis.previousRegularUpEpoch = parsed.epoch
	analysis.addSample(delta)
	return nil
}

func (analysis *analyzer) addSample(delta float64) {
	if analysis.report.Samples == 0 || delta < analysis.report.MinDelta {
		analysis.report.MinDelta = delta
	}
	if delta > analysis.report.MaxDelta {
		analysis.report.MaxDelta = delta
	}
	analysis.report.Samples++
}

func (analysis *analyzer) finish() Report {
	switch {
	case analysis.report.UpPackets < minimumUpPackets:
		analysis.report.Status = StatusSkip
		analysis.report.Reason = "insufficient-up-packets"
	case analysis.report.Samples < minimumSamples:
		analysis.report.Status = StatusSkip
		analysis.report.Reason = "insufficient-continuous-up-samples"
	case analysis.report.MinDelta < minimumDelta || analysis.report.MaxDelta > maximumDelta:
		analysis.report.Status = StatusFail
		analysis.report.Reason = "outside-bounds"
	default:
		analysis.report.Status = StatusPass
		analysis.report.Reason = "within-bounds"
	}
	return analysis.report
}
