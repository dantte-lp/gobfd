//go:build linux

package bfdload

import (
	"context"
	"errors"
	"time"
)

func startOwnerControl(ctx context.Context, owner *Owner, directory string) (*Control, *phaseStart, error) {
	if directory == "" {
		return nil, nil, nil
	}
	control, err := OpenControl(directory)
	if err != nil {
		return nil, nil, err
	}
	if publishErr := control.publish("owner-listening", "ready\n"); publishErr != nil {
		return nil, nil, errors.Join(publishErr, control.Close())
	}
	phase, err := control.ownerStart(ctx, owner)
	if err != nil {
		return nil, nil, errors.Join(err, control.Close())
	}
	return control, phase, nil
}

func (r *OwnerWorkloadObservation) runWindow(ctx context.Context, owner *Owner) (time.Time, error) {
	started := time.Now()
	control, phase, err := startOwnerControl(ctx, owner, r.Config.ControlDirectory)
	if err != nil {
		return started, err
	}
	if phase != nil {
		started = phase.goStart
		r.Phase = phase.observation()
	}
	r.StartedAtUTC = started.UTC()
	err = r.observeWithCPUPressure(ctx, owner, started)
	if control == nil {
		return started, err
	}
	if err == nil {
		err = r.completeControlled(ctx, owner, control)
	}
	return started, errors.Join(err, control.Close())
}

func (r *OwnerWorkloadObservation) observeWithCPUPressure(ctx context.Context, owner *Owner, started time.Time) error {
	windowCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	pressure, err := startCPUPressure(windowCtx, r.Config.Profile, started, cancel)
	if err != nil {
		return err
	}
	err = awaitPhaseTime(windowCtx, started)
	if err == nil {
		r.Initial, err = owner.Snapshot(windowCtx)
	}
	if err == nil {
		r.Resources.observe(r.Initial.Environment, 0, r.Config.Profile.Duration)
		err = r.observe(windowCtx, owner, started)
	}
	if err != nil {
		cancel()
	}
	var pressureErr error
	r.CPUPressure, pressureErr = pressure.join()
	return errors.Join(err, pressureErr)
}

func (r *OwnerWorkloadObservation) completeControlled(ctx context.Context, owner *Owner, control *Control) error {
	var err error
	r.MeasurementFinal, err = owner.Snapshot(ctx)
	if err != nil {
		return err
	}
	if publishErr := control.publish("owner-measured", "done\n"); publishErr != nil {
		return publishErr
	}
	return control.drainOwner(ctx, owner)
}
