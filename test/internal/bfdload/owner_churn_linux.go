//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

var errChurnNotUp = errors.New("owner churn slot is not Up")

func (o *Owner) releaseForChurn(ctx context.Context, index int) (ownerSession, bool, error) {
	o.inventory.Lock()
	defer o.inventory.Unlock()
	if err := o.churnAdmission(ctx, index); err != nil {
		return ownerSession{}, false, err
	}
	entry := o.sessions[index]
	if entry.session == nil || entry.session.State() != bfd.StateUp {
		return entry, false, errChurnNotUp
	}
	discriminator := entry.session.LocalDiscriminator()
	if !o.sessionRegistered(entry) {
		return entry, false, fmt.Errorf("churn slot is not the registered session: %w", errOwnerConfig)
	}
	var err error
	if o.echo {
		err = o.manager.DestroyEchoSessionAs(ctx, discriminator, bfd.APISessionOwner("bfdload"))
	} else {
		err = o.manager.DestroySession(ctx, discriminator)
	}
	if o.sessionRegistered(entry) {
		return entry, false, errors.Join(err, fmt.Errorf("churn session remains registered: %w", errOwnerConfig))
	}
	// DestroySession has joined the retired session before its counters are frozen.
	o.retired++
	o.retiredSent += entry.session.PacketsSent()
	o.retiredRX += entry.session.PacketsReceived()
	o.sessions[index].session, o.sessions[index].lease, o.sessions[index].sourcePort = nil, nil, 0
	return entry, true, errors.Join(err, entry.lease.Close())
}

func (o *Owner) createForChurn(ctx context.Context, index int, profile Profile) (ownerSession, error) {
	o.inventory.Lock()
	defer o.inventory.Unlock()
	if err := o.churnAdmission(ctx, index); err != nil {
		return ownerSession{}, err
	}
	previous := o.sessions[index]
	if previous.session != nil {
		return ownerSession{}, fmt.Errorf("churn slot still owns a session: %w", errOwnerConfig)
	}
	entry, err := o.createSession(ctx, OwnerConfig{
		Echo:     o.echo,
		Interval: profile.Interval, DetectMultiplier: profile.DetectMultiplier,
	}, previous.endpoint)
	if err != nil {
		return ownerSession{}, err
	}
	entry.generation = previous.generation + 1
	o.sessions[index] = entry
	return entry, nil
}

func (o *Owner) churnAdmission(ctx context.Context, index int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("owner churn interrupted: %w", err)
	}
	if o.closing || index < 0 || index >= len(o.sessions) {
		return fmt.Errorf("owner churn is closed or has an invalid slot: %w", errOwnerConfig)
	}
	return nil
}

func (o *Owner) churnRecovered(ctx context.Context, index int, generation uint64) (bool, error) {
	o.inventory.RLock()
	defer o.inventory.RUnlock()
	if err := o.churnAdmission(ctx, index); err != nil {
		return false, err
	}
	entry := o.sessions[index]
	if entry.session == nil || entry.generation != generation {
		return false, fmt.Errorf("owner churn generation disappeared: %w", errOwnerConfig)
	}
	return entry.session.State() == bfd.StateUp, nil
}

func (o *Owner) registrationSnapshot() (OwnerRegistrations, error) {
	o.inventory.RLock()
	defer o.inventory.RUnlock()
	families, err := o.registry.Gather()
	if err != nil {
		return OwnerRegistrations{}, fmt.Errorf("gather owner registration metrics: %w", err)
	}
	statuses, _ := o.manager.WatchSessionsWithRevision()
	return observeOwnerRegistrations(families, len(statuses))
}
