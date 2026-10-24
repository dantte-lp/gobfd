//go:build linux

package bfdload

import (
	"math/bits"
	"time"
)

const pressurePeriod = 100 * time.Millisecond

func burnPressureChunk(checksum *uint64) {
	for range 1024 {
		*checksum = bits.RotateLeft64(*checksum+0x9e3779b97f4a7c15, 13)
	}
}

func wholePressureWindows(duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	return uint64(duration) / uint64(pressurePeriod)
}
