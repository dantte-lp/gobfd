//go:build linux

package bfd

import (
	"fmt"
	"math"
	"time"

	"golang.org/x/sys/unix"
)

func receiveClockResolutions() (time.Duration, time.Duration, error) {
	realtime, err := receiveClockResolution(unix.CLOCK_REALTIME)
	if err != nil {
		return 0, 0, err
	}
	monotonic, err := receiveClockResolution(unix.CLOCK_MONOTONIC)
	if err != nil {
		return 0, 0, err
	}
	return realtime, monotonic, nil
}

func receiveClockResolution(clockID int32) (time.Duration, error) {
	var resolution unix.Timespec
	if err := unix.ClockGetres(clockID, &resolution); err != nil {
		return 0, fmt.Errorf("read clock %d resolution: %w", clockID, err)
	}
	if resolution.Sec < 0 || resolution.Nsec < 0 || resolution.Nsec >= int64(time.Second) ||
		resolution.Sec > (math.MaxInt64-resolution.Nsec)/int64(time.Second) {
		return 0, fmt.Errorf("clock %d resolution overflows: %w", clockID, errReceiveClockResolution)
	}
	value := time.Duration(resolution.Sec)*time.Second + time.Duration(resolution.Nsec)
	if value <= 0 {
		return 0, fmt.Errorf("clock %d resolution is not positive: %w", clockID, errReceiveClockResolution)
	}
	return value, nil
}
