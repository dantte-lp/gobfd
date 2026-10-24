//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	controlPollPeriod   = 10 * time.Millisecond
	controlStartLead    = 250 * time.Millisecond
	controlReadyTimeout = 30 * time.Second
	controlDrainTimeout = 5 * time.Second
)

func (c *Control) wait(ctx context.Context, ready func() (bool, error)) error {
	ticker := time.NewTicker(controlPollPeriod)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for workload phase: %w", err)
		}
		if done, err := ready(); err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for workload phase: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *Control) token(ctx context.Context, name, value string) (bool, error) {
	data, found, err := c.read(ctx, name)
	if err != nil || !found {
		return false, err
	}
	if string(data) != value {
		return false, fmt.Errorf("unexpected control token %s: %w", name, ErrControl)
	}
	return true, nil
}

func waitListenerReady(ctx context.Context, directory string, peer bool) (resultErr error) {
	control, err := OpenControl(directory)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, control.Close()) }()
	readyCtx, cancel := context.WithTimeout(ctx, controlReadyTimeout)
	defer cancel()
	name := "peers-listening"
	if peer {
		if publishErr := control.publish(name, "ready\n"); publishErr != nil {
			return publishErr
		}
		name = "owner-listening"
	}
	return control.wait(readyCtx, func() (bool, error) { return control.token(readyCtx, name, "ready\n") })
}

func (c *Control) awaitStart(ctx context.Context) (*phaseStart, error) {
	var phase *phaseStart
	err := c.wait(ctx, func() (bool, error) {
		var found bool
		var err error
		phase, found, err = c.readStart(ctx)
		return found, err
	})
	return phase, err
}

func (p *phaseStart) observation() *Phase {
	return &Phase{NativeStartNS: p.NativeStartNS, TimeNamespaceID: p.TimeNamespaceID, BracketNS: p.BracketNS}
}

// Begin waits for both roles to reach Up and publishes a future common epoch.
func (c *Control) Begin(ctx context.Context) (*Phase, error) {
	readyCtx, cancel := context.WithTimeout(ctx, controlReadyTimeout)
	defer cancel()
	if err := c.wait(readyCtx, func() (bool, error) {
		for _, name := range []string{"owner-ready", "peers-ready"} {
			if ready, tokenErr := c.token(readyCtx, name, "ready\n"); tokenErr != nil || !ready {
				return false, tokenErr
			}
		}
		return true, nil
	}); err != nil {
		return nil, err
	}
	namespace, err := timeNamespaceID()
	if err != nil {
		return nil, err
	}
	now, err := monotonicNowNS()
	if err != nil {
		return nil, err
	}
	value := fmt.Sprintf("%d %d\n", namespace, now+int64(controlStartLead))
	if publishErr := c.publish("start", value); publishErr != nil {
		return nil, publishErr
	}
	phase, _, err := c.readStart(readyCtx)
	if err != nil {
		return nil, err
	}
	return phase.observation(), nil
}

// WaitMeasured leaves both traffic processes alive until the controller stops them.
func (c *Control) WaitMeasured(ctx context.Context) error {
	return c.wait(ctx, func() (bool, error) { return c.token(ctx, "owner-measured", "done\n") })
}

// StopPeers requests a joined final peer report before the owner is stopped.
func (c *Control) StopPeers() error { return c.publish("stop-peers", "stop\n") }

// StopOwner supplies actual successful writes after the peer process has joined.
func (c *Control) StopOwner(written uint64) error {
	return c.publish("stop-owner", strconv.FormatUint(written, 10)+"\n")
}

func (c *Control) expectedWrites(ctx context.Context) (uint64, error) {
	var count uint64
	err := c.wait(ctx, func() (bool, error) {
		data, found, readErr := c.read(ctx, "stop-owner")
		if readErr != nil || !found {
			return false, readErr
		}
		parsed, parseErr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if parseErr != nil {
			return false, fmt.Errorf("parse final peer writes: %w: %w", ErrControl, parseErr)
		}
		count = parsed
		return true, nil
	})
	return count, err
}

func awaitPhaseTime(ctx context.Context, at time.Time) error {
	timer := time.NewTimer(max(0, time.Until(at)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for common workload epoch: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
