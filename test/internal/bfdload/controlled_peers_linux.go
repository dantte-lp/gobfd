//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (g *peerGroup) runControlled(ctx context.Context, cfg PeerConfig) (bool, error) {
	control, err := OpenControl(cfg.ControlDirectory)
	if err != nil {
		return false, err
	}
	completed, runErr := g.observeControlled(ctx, cfg, control)
	return completed, errors.Join(runErr, control.Close())
}

func (g *peerGroup) observeControlled(ctx context.Context, cfg PeerConfig, control *Control) (bool, error) {
	readyCtx, cancel := context.WithTimeout(ctx, controlReadyTimeout)
	defer cancel()
	if err := control.wait(readyCtx, func() (bool, error) {
		for _, peer := range g.peers {
			if !peer.up.Load() {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		return false, err
	}
	if publishErr := control.publish("peers-ready", "ready\n"); publishErr != nil {
		return false, publishErr
	}
	phase, err := control.awaitStart(readyCtx)
	if err != nil {
		return false, err
	}
	g.phase.Store(phase)
	stopCtx, stopCancel := context.WithDeadline(ctx, phase.goStart.Add(cfg.Profile.Duration+controlDrainTimeout))
	defer stopCancel()
	waitErr := control.wait(stopCtx, func() (bool, error) { return control.token(stopCtx, "stop-peers", "stop\n") })
	if waitErr != nil {
		return false, waitErr
	}
	if time.Now().Before(phase.goStart.Add(cfg.Profile.Duration)) {
		return false, fmt.Errorf("peers stopped before measurement end: %w", ErrControl)
	}
	return true, nil
}
