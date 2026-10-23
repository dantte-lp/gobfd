//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var (
	// ErrClockEpochChanged rejects timing data spanning a realtime discontinuity.
	ErrClockEpochChanged      = errors.New("realtime clock epoch changed")
	errClockEpochUnverifiable = errors.New("realtime clock epoch unverifiable")
	errClockEpochGuardClosed  = errors.New("realtime clock epoch guard closed before verification")
)

const clockEpochMechanism = "linux-timerfd-cancel-on-set"

// ClockEpochObservation covers only the guard's completed, bounded run window.
type ClockEpochObservation struct {
	started, finished time.Time `json:"-"`
	Mechanism         string    `json:"mechanism"`
	StartedAtUTC      time.Time `json:"started_at_utc"`
	FinishedAtUTC     time.Time `json:"finished_at_utc"`
	ElapsedNS         int64     `json:"elapsed_ns"`
	Unchanged         bool      `json:"unchanged"`
	Error             string    `json:"error,omitempty"`
}

// ClockEpochGuard owns one timerfd and no goroutine. Arm it before calibration;
// call Finish after the final sample, or Close on every aborted run.
type ClockEpochGuard struct {
	mu          sync.Mutex
	fd          int
	owned       bool
	started     time.Time
	startedUTC  time.Time
	expiresUTC  time.Time
	closed      bool
	finished    bool
	closeErr    error
	finishErr   error
	observation ClockEpochObservation
}

// NewClockEpochGuard arms a seven-day witness without changing the host clock.
// Expiration, cancellation and unavailable data can never qualify a run.
func NewClockEpochGuard(ctx context.Context) (*ClockEpochGuard, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start realtime epoch guard: %w", err)
	}
	now, err := readEpochRealtime()
	if err != nil {
		return nil, err
	}
	fd, err := unix.TimerfdCreate(unix.CLOCK_REALTIME, unix.TFD_CLOEXEC|unix.TFD_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("create realtime epoch timerfd: %w", err)
	}
	const horizon = 7 * 24 * time.Hour
	guard := &ClockEpochGuard{fd: fd, owned: true, expiresUTC: now.Add(horizon)}
	deadline := unix.ItimerSpec{Value: unix.Timespec{
		Sec: guard.expiresUTC.Unix(), Nsec: int64(guard.expiresUTC.Nanosecond()),
	}}
	if err = unix.TimerfdSettime(fd, unix.TFD_TIMER_ABSTIME|unix.TFD_TIMER_CANCEL_ON_SET, &deadline, nil); err != nil {
		return nil, errors.Join(fmt.Errorf("arm realtime epoch timerfd: %w", err), guard.Close())
	}
	guard.startedUTC, err = readEpochRealtime()
	if err != nil {
		return nil, errors.Join(err, guard.Close())
	}
	guard.started = time.Now()
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("finish realtime epoch guard startup: %w", err), guard.Close())
	}
	return guard, nil
}

func readEpochRealtime() (time.Time, error) {
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_REALTIME, &now); err != nil {
		return time.Time{}, fmt.Errorf("read realtime epoch clock: %w", err)
	}
	return time.Unix(now.Sec, now.Nsec).UTC(), nil
}

func clockEpochReadResult(n int, err error) error {
	if errors.Is(err, unix.ECANCELED) {
		return fmt.Errorf("read realtime epoch timerfd: %w: %w", ErrClockEpochChanged, err)
	}
	if n <= 0 && errors.Is(err, unix.EAGAIN) {
		return nil
	}
	if err != nil && !errors.Is(err, unix.EAGAIN) {
		return fmt.Errorf("read realtime epoch timerfd: %w", err)
	}
	return fmt.Errorf("realtime epoch timerfd returned %d bytes: %w", n, errClockEpochUnverifiable)
}

// Finish checks the witness and closes it. Repeated calls return the same result.
func (g *ClockEpochGuard) Finish(ctx context.Context) (ClockEpochObservation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finished {
		return g.observation, g.finishErr
	}
	finished := time.Now()
	finishedUTC, err := g.checkLocked(ctx)
	if cancelErr := ctx.Err(); cancelErr != nil && !errors.Is(err, cancelErr) {
		err = errors.Join(err, fmt.Errorf("finish realtime epoch guard: %w", cancelErr))
	}
	observation := ClockEpochObservation{
		started: g.started, finished: finished,
		Mechanism: clockEpochMechanism, StartedAtUTC: g.startedUTC, FinishedAtUTC: finishedUTC,
	}
	if !g.started.IsZero() {
		observation.ElapsedNS = int64(time.Since(g.started))
	}
	g.finishErr = errors.Join(err, g.closeLocked())
	observation.Unchanged = g.finishErr == nil
	if g.finishErr != nil {
		observation.Error = g.finishErr.Error()
	}
	g.finished = true
	g.observation = observation
	return observation, g.finishErr
}

func (g *ClockEpochGuard) checkLocked(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, fmt.Errorf("finish realtime epoch guard: %w", err)
	}
	if !g.owned || g.closed {
		return time.Time{}, fmt.Errorf("check realtime epoch guard: %w", errClockEpochGuardClosed)
	}
	finishedUTC, err := readEpochRealtime()
	if err != nil {
		return time.Time{}, err
	}
	var wire [8]byte
	n, readErr := unix.Read(g.fd, wire[:])
	err = clockEpochReadResult(n, readErr)
	if finishedUTC.Before(g.startedUTC) || !finishedUTC.Before(g.expiresUTC) {
		err = errors.Join(err, errClockEpochUnverifiable)
	}
	return finishedUTC, err
}

// Close aborts an unfinished guard; it is safe to call repeatedly.
func (g *ClockEpochGuard) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closeLocked()
}

func (g *ClockEpochGuard) closeLocked() error {
	if g.owned && !g.closed {
		g.closed = true
		if err := unix.Close(g.fd); err != nil {
			g.closeErr = fmt.Errorf("close realtime epoch timerfd: %w", err)
		}
	}
	return g.closeErr
}
