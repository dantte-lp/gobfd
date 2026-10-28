//go:build !linux

package bfd

import "time"

func receiveClockResolutions() (time.Duration, time.Duration, error) {
	return 0, 0, errReceiveClockResolution
}
