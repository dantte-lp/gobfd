//go:build linux

package bfdload

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestClockEpochReadResults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		n    int
		err  error
		want error
	}{
		{"no_cancellation_observed", -1, unix.EAGAIN, nil},
		{"no_cancellation_observed_zero", 0, unix.EAGAIN, nil},
		{"changed", -1, unix.ECANCELED, ErrClockEpochChanged},
		{"expired", 8, nil, errClockEpochUnverifiable},
		{"short", 7, nil, errClockEpochUnverifiable},
		{"empty", 0, nil, errClockEpochUnverifiable},
		{"contradictory", 8, unix.EAGAIN, errClockEpochUnverifiable},
		{"interrupted", -1, unix.EINTR, unix.EINTR},
		{"closed", -1, unix.EBADF, unix.EBADF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := clockEpochReadResult(tc.n, tc.err); !errors.Is(err, tc.want) {
				t.Fatalf("read(%d,%v)=%v, want %v", tc.n, tc.err, err, tc.want)
			}
		})
	}
}

func TestClockEpochActualFinishAndCleanup(t *testing.T) {
	guard, err := NewClockEpochGuard(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := guard.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	fd := guard.fd
	observed, err := guard.Finish(t.Context())
	if err != nil || !observed.NoCancellationObserved || observed.ElapsedNS < 0 || observed.Mechanism == "" {
		t.Fatalf("actual epoch witness unavailable: %+v err=%v", observed, err)
	}
	again, err := guard.Finish(t.Context())
	if err != nil || again != observed {
		t.Fatalf("second Finish lost its immutable result: %+v err=%v", again, err)
	}
	var wire [8]byte
	if _, err := unix.Read(fd, wire[:]); !errors.Is(err, unix.EBADF) {
		t.Fatalf("epoch FD remained open after Finish: %v", err)
	}
}

func TestClockEpochCancellationAndAbort(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if guard, err := NewClockEpochGuard(ctx); guard != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor returned guard=%v err=%v", guard, err)
	}
	for _, abort := range []bool{false, true} {
		guard, err := NewClockEpochGuard(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		fd := guard.fd
		want := context.Canceled
		if abort {
			want = errClockEpochGuardClosed
			if closeErr := guard.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			ctx = t.Context()
		}
		observed, finishErr := guard.Finish(ctx)
		if !errors.Is(finishErr, want) || observed.NoCancellationObserved || observed.Error == "" {
			t.Fatalf("aborted witness qualified: %+v err=%v, want %v", observed, finishErr, want)
		}
		if closeErr := guard.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		var wire [8]byte
		if _, readErr := unix.Read(fd, wire[:]); !errors.Is(readErr, unix.EBADF) {
			t.Fatalf("epoch FD leaked on failure: %v", readErr)
		}
	}
}

func TestClockEpochUnownedDescriptorIsPreserved(t *testing.T) {
	for _, finish := range []bool{false, true} {
		fds := make([]int, 2)
		if err := unix.Pipe2(fds, unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, fd := range fds {
				if err := unix.Close(fd); err != nil {
					t.Error(err)
				}
			}
		})
		guard := &ClockEpochGuard{fd: fds[0]}
		if finish {
			observation, err := guard.Finish(t.Context())
			if !errors.Is(err, errClockEpochGuardClosed) || observation.NoCancellationObserved {
				t.Errorf("unowned guard was usable: %+v err=%v", observation, err)
			}
		} else if err := guard.Close(); err != nil {
			t.Fatal(err)
		}
		var wire [8]byte
		if _, err := unix.Read(fds[0], wire[:]); !errors.Is(err, unix.EAGAIN) {
			t.Errorf("guard touched an unowned descriptor: %v", err)
		}
	}
}

func TestClockEpochActualExpiryIsRejected(t *testing.T) {
	guard, err := NewClockEpochGuard(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := guard.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	deadline := unix.ItimerSpec{Value: unix.Timespec{Sec: 1}}
	if err = unix.TimerfdSettime(guard.fd, unix.TFD_TIMER_ABSTIME|unix.TFD_TIMER_CANCEL_ON_SET,
		&deadline, nil); err != nil {
		t.Fatal(err)
	}
	// #nosec G115 -- Linux file descriptors fit the signed int32 pollfd field.
	fds := []unix.PollFd{{Fd: int32(guard.fd), Events: unix.POLLIN}}
	if n, pollErr := unix.Poll(fds, 1000); pollErr != nil || n != 1 {
		t.Fatalf("owned timer did not expire: events=%d err=%v", n, pollErr)
	}
	observed, err := guard.Finish(t.Context())
	if !errors.Is(err, errClockEpochUnverifiable) || observed.NoCancellationObserved {
		t.Fatalf("expired guard qualified: %+v err=%v", observed, err)
	}
}
