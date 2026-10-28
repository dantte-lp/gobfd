//go:build linux

package bfdload

import (
	"math/rand/v2"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestPeerPeriodicJitterPreservesCompletionDeadline(t *testing.T) {
	t.Parallel()

	for _, multiplier := range []uint8{1, 3} {
		t.Run(time.Duration(multiplier).String(), func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				peer := peerRuntime{state: fixturePeerState(t), jitter: rand.New(rand.NewPCG(17, 1))}
				peer.state.state, peer.state.multiplier = bfd.StateUp, multiplier
				last := time.Now()
				timer := time.NewTimer(time.Hour)
				defer timer.Stop()
				synctest.Sleep(40 * time.Millisecond)
				peer.resetPeriodic(timer, last)
				synctest.Sleep(34 * time.Millisecond)
				select {
				case <-timer.C:
					t.Fatal("periodic send preceded the 75% lower bound")
				default:
				}
				synctest.Sleep(27 * time.Millisecond)
				upper := 100 * time.Millisecond
				if multiplier == 1 {
					upper = 90 * time.Millisecond
				}
				select {
				case fired := <-timer.C:
					if elapsed := fired.Sub(last); elapsed < 75*time.Millisecond || elapsed > upper {
						t.Fatalf("jitter deadline includes rescheduling delay or violates RFC bounds: %s", elapsed)
					}
				default:
					t.Fatal("rescheduling delay postponed a periodic send beyond its RFC jitter bound")
				}
			})
		})
	}
}
