//go:build linux

package main

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

const maxAssessmentReportBytes int64 = 512 << 20

const assessmentInputFileCount = 2

func runAssess(ctx context.Context, args []string, output io.Writer) error {
	var owner bfdload.OwnerWorkloadObservation
	var peers bfdload.PeerObservation
	inputErr := readAssessmentInputs(ctx, args, &owner, &peers)
	report := bfdload.AssessProfile(bfdload.OwnerWorkloadObservation{}, bfdload.PeerObservation{})
	if inputErr == nil {
		report = bfdload.AssessProfile(owner, peers)
	} else {
		// Bound diagnostics independently of user-controlled paths and member names.
		report.Error = fmt.Sprintf("assessment input: %.128s", inputErr.Error())
	}
	if err := ctx.Err(); err != nil {
		report = bfdload.AssessProfile(bfdload.OwnerWorkloadObservation{}, bfdload.PeerObservation{})
		report.Error = fmt.Sprintf("assessment interrupted: %.128s", err.Error())
	}
	var assessmentErr error
	if report.Status != "pass" {
		assessmentErr = fmt.Errorf("assessment %s: %w", report.Status, bfdload.ErrAssessment)
	}
	if err := ctx.Err(); err != nil {
		assessmentErr = errors.Join(assessmentErr, err)
	}
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return fmt.Errorf("encode profile assessment: %w", errors.Join(err, assessmentErr))
	}
	return assessmentErr
}

func readAssessmentInputs(
	ctx context.Context, args []string, owner *bfdload.OwnerWorkloadObservation, peers *bfdload.PeerObservation,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read assessment inputs: %w", err)
	}
	if len(args) != assessmentInputFileCount {
		return fmt.Errorf("assessment requires owner and peer JSON paths: %w", errCommandArguments)
	}
	if err := readAssessmentReport(ctx, args[0], owner); err != nil {
		return fmt.Errorf("read owner report: %w", err)
	}
	if err := readAssessmentReport(ctx, args[1], peers); err != nil {
		return fmt.Errorf("read peer report: %w", err)
	}
	return nil
}

func readAssessmentReport(ctx context.Context, path string, target any) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("open assessment report: %w", err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open report directory: %w", err)
	}
	readErr := readAssessmentInRoot(ctx, root, filepath.Base(path), target)
	if err := errors.Join(readErr, root.Close()); err != nil {
		return fmt.Errorf("read and close report directory: %w", err)
	}
	return nil
}

func readAssessmentInRoot(ctx context.Context, root *os.Root, name string, target any) error {
	// Reject leaf symlinks and avoid blocking on a FIFO before checking its mode.
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open report file: %w", err)
	}
	readErr := readAssessmentFile(ctx, file, target)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return fmt.Errorf("read and close report file: %w", err)
	}
	return nil
}

func readAssessmentFile(ctx context.Context, file *os.File, target any) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat report file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxAssessmentReportBytes {
		return fmt.Errorf("report must be a regular file of at most %d bytes: %w",
			maxAssessmentReportBytes, bfdload.ErrAssessment)
	}
	return decodeAssessmentReport(ctx, file, target, maxAssessmentReportBytes)
}

func decodeAssessmentReport(ctx context.Context, input io.Reader, target any, limit int64) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("decode report: %w", err)
	}
	if limit <= 0 || limit > maxAssessmentReportBytes {
		return fmt.Errorf("invalid report byte limit: %w", bfdload.ErrAssessment)
	}
	bounded := &io.LimitedReader{R: input, N: limit + 1}
	// Keep only v1's numeric time.Duration representation. v2 retains strict
	// UTF-8, duplicate-name and case checks, and consumes one value through EOF.
	decodeErr := jsonv2.UnmarshalRead(bounded, target,
		json.FormatDurationAsNano(true), jsonv2.RejectUnknownMembers(true))
	if bounded.N == 0 {
		return fmt.Errorf("report exceeds %d bytes: %w", limit, errors.Join(bfdload.ErrAssessment, ctx.Err()))
	}
	if err := errors.Join(decodeErr, ctx.Err()); err != nil {
		return fmt.Errorf("decode report JSON: %w", err)
	}
	return nil
}
