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
	fieldCount       = 2
	minimumUpPackets = 10
	minimumSamples   = 5
	// RFC 5880 section 6.8.7 exempts a Final response from the periodic timer.
	minimumPeriodicDelta = 0.100
	minimumDelta         = 0.150
	maximumDelta         = 0.400
	upState              = 3
)

var (
	errMalformedRow       = errors.New("malformed jitter TSV row")
	errInvalidEpoch       = errors.New("invalid jitter epoch")
	errNonIncreasingEpoch = errors.New("jitter epoch is not increasing")
	errInvalidState       = errors.New("invalid BFD state")
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
	report            Report
	previousEpoch     float64
	previousUpEpoch   float64
	havePreviousEpoch bool
	havePreviousUp    bool
}

// Evaluate parses one frame.time_epoch,bfd.sta TSV stream and assesses jitter.
func Evaluate(input io.Reader) (Report, error) {
	var analysis analyzer

	scanner := bufio.NewScanner(input)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		epoch, state, err := parseRow(scanner.Text(), lineNumber)
		if err != nil {
			return Report{}, err
		}
		if err := analysis.add(epoch, state, lineNumber); err != nil {
			return Report{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return Report{}, fmt.Errorf("scan jitter TSV: %w", err)
	}
	return analysis.finish(), nil
}

func parseRow(line string, lineNumber int) (float64, uint64, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != fieldCount {
		return 0, 0, fmt.Errorf(
			"parse jitter TSV row %d: got %d fields, want %d: %w",
			lineNumber, len(fields), fieldCount, errMalformedRow,
		)
	}
	epoch, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse jitter epoch at row %d: %q: %w", lineNumber, fields[0], err)
	}
	if math.IsNaN(epoch) || math.IsInf(epoch, 0) || epoch < 0 {
		return 0, 0, fmt.Errorf("parse jitter epoch at row %d: %q: %w", lineNumber, fields[0], errInvalidEpoch)
	}
	state, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 0, 8)
	if err != nil {
		return 0, 0, fmt.Errorf("parse BFD state at row %d: %q: %w", lineNumber, fields[1], err)
	}
	if state > upState {
		return 0, 0, fmt.Errorf("parse BFD state at row %d: %q: %w", lineNumber, fields[1], errInvalidState)
	}
	return epoch, state, nil
}

func (analysis *analyzer) add(epoch float64, state uint64, lineNumber int) error {
	if analysis.havePreviousEpoch && epoch <= analysis.previousEpoch {
		return fmt.Errorf(
			"parse jitter epoch at row %d: %.9f is not after %.9f: %w",
			lineNumber, epoch, analysis.previousEpoch, errNonIncreasingEpoch,
		)
	}
	analysis.previousEpoch = epoch
	analysis.havePreviousEpoch = true
	if state != upState {
		analysis.havePreviousUp = false
		return nil
	}
	analysis.report.UpPackets++
	if !analysis.havePreviousUp {
		analysis.previousUpEpoch = epoch
		analysis.havePreviousUp = true
		return nil
	}
	delta := epoch - analysis.previousUpEpoch
	analysis.previousUpEpoch = epoch
	if delta < minimumPeriodicDelta {
		return nil
	}
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
