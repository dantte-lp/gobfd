//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	sessionChurnPeriod    = 5 * time.Second
	sessionChurnOffset    = 250 * time.Millisecond
	sessionChurnGap       = 200 * time.Millisecond
	sessionChurnMinimum   = 6 * time.Second
	sessionChurnBudget    = 5 * time.Second
	maxSessionChurnEvents = int(referenceDuration / sessionChurnPeriod)
)

// SessionChurnEvent retains actual generation boundaries for failure correlation.
// Cardinality pointers are nil until the corresponding snapshot was collected.
type SessionChurnEvent struct {
	Slot                int                 `json:"slot"`
	Endpoint            OwnerEndpoint       `json:"endpoint"`
	ElapsedNS           int64               `json:"elapsed_ns"`
	OldGeneration       uint64              `json:"old_generation"`
	NewGeneration       uint64              `json:"new_generation"`
	OldDiscriminator    uint32              `json:"old_discriminator"`
	NewDiscriminator    uint32              `json:"new_discriminator"`
	OldSourcePort       uint16              `json:"old_source_port"`
	NewSourcePort       uint16              `json:"new_source_port"`
	ReleaseStartedAtUTC time.Time           `json:"release_started_at_utc"`
	ReleasedAtUTC       time.Time           `json:"released_at_utc"`
	CreatedAtUTC        time.Time           `json:"created_at_utc"`
	RecoveredAtUTC      time.Time           `json:"recovered_at_utc"`
	Before              *OwnerRegistrations `json:"before"`
	Absent              *OwnerRegistrations `json:"absent"`
	Restored            *OwnerRegistrations `json:"restored"`
	SkippedNotUp        bool                `json:"skipped_not_up"`
	Error               string              `json:"error,omitempty"`
}

// SessionChurnObservation measures real final-claim release and recovery.
// It cycles the fixed manifest; it does not introduce new peer label identities.
type SessionChurnObservation struct {
	Enabled           bool                `json:"enabled"`
	Joined            bool                `json:"joined"`
	ScheduledAtUTC    time.Time           `json:"scheduled_at_utc"`
	PeriodNS          int64               `json:"period_ns"`
	GapNS             int64               `json:"gap_ns"`
	RecoveryTimeoutNS int64               `json:"recovery_timeout_ns"`
	Created           uint64              `json:"created"`
	Released          uint64              `json:"released"`
	Recovered         uint64              `json:"recovered"`
	SkippedNotUp      uint64              `json:"skipped_not_up"`
	SkippedCycles     uint64              `json:"skipped_cycles"`
	SkippedTail       uint64              `json:"skipped_tail_cycles"`
	Events            []SessionChurnEvent `json:"events"`
	EventsOmitted     uint64              `json:"events_omitted"`
	Completed         bool                `json:"completed"`
}

type sessionChurnResult struct {
	observation SessionChurnObservation
	err         error
}

type sessionChurn struct {
	observation SessionChurnObservation
	result      chan sessionChurnResult
}

func startSessionChurn(
	ctx context.Context, owner *Owner, profile Profile, at time.Time, failed context.CancelFunc,
) (*sessionChurn, error) {
	churn := &sessionChurn{observation: SessionChurnObservation{
		Enabled: profile.SessionChurn, ScheduledAtUTC: at.UTC(),
		PeriodNS: int64(sessionChurnPeriod), GapNS: int64(sessionChurnGap),
		RecoveryTimeoutNS: int64(sessionChurnBudget), Completed: true,
	}}
	if !profile.SessionChurn {
		return churn, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start session churn: %w", err)
	}
	if owner == nil || profile.Duration < sessionChurnMinimum {
		return nil, fmt.Errorf("session churn needs an owner and at least six seconds: %w", ErrProfile)
	}
	churn.result = make(chan sessionChurnResult, 1)
	go func() {
		observation := churn.observation
		err := observation.run(ctx, owner, profile, at)
		observation.Completed = err == nil
		if err != nil {
			failed()
		}
		churn.result <- sessionChurnResult{observation, err}
	}()
	return churn, nil
}

func (c *sessionChurn) join() (SessionChurnObservation, error) {
	if c.result == nil {
		return c.observation, nil
	}
	result := <-c.result
	result.observation.Joined = true
	return result.observation, result.err
}

func (o *SessionChurnObservation) run(ctx context.Context, owner *Owner, profile Profile, at time.Time) error {
	end := at.Add(profile.Duration)
	index := 0
	for next := at.Add(sessionChurnOffset); next.Before(end); next = next.Add(sessionChurnPeriod) {
		if err := awaitPhaseTime(ctx, next); err != nil {
			return err
		}
		if missed := int64(min(time.Since(next)/sessionChurnPeriod, end.Sub(next)/sessionChurnPeriod)); missed > 0 {
			o.SkippedCycles += uint64(missed)
			next = next.Add(time.Duration(missed) * sessionChurnPeriod)
		}
		if !next.Before(end) {
			break
		}
		// AdminDown alone needs three seconds at multiplier 3; never truncate it to fit the tail.
		if time.Until(end) < sessionChurnBudget {
			o.SkippedTail++
			break
		}
		cycleCtx, cancel := context.WithDeadline(ctx, end)
		event, err := runSessionChurnCycle(cycleCtx, owner, profile, index, at)
		cancel()
		o.record(event, err)
		if err != nil {
			return err
		}
		index = (index + 1) % profile.Sessions
	}
	if waitErr := awaitPhaseTime(ctx, end); waitErr != nil {
		return waitErr
	}
	if o.Recovered == 0 {
		return fmt.Errorf("session churn observed no completed recovery: %w", errOwnerConfig)
	}
	return nil
}

func (o *SessionChurnObservation) record(event SessionChurnEvent, err error) {
	if !event.ReleasedAtUTC.IsZero() {
		o.Released++
	}
	if !event.CreatedAtUTC.IsZero() {
		o.Created++
	}
	if !event.RecoveredAtUTC.IsZero() {
		o.Recovered++
	}
	if event.SkippedNotUp {
		o.SkippedNotUp++
	}
	if err != nil {
		event.Error = err.Error()
	}
	if len(o.Events) == maxSessionChurnEvents {
		o.EventsOmitted++
	} else {
		o.Events = append(o.Events, event)
	}
}

func runSessionChurnCycle(
	ctx context.Context, owner *Owner, profile Profile, index int, at time.Time,
) (SessionChurnEvent, error) {
	event := SessionChurnEvent{Slot: index, ElapsedNS: int64(time.Since(at))}
	before, err := owner.registrationSnapshot()
	if err != nil {
		return event, err
	}
	event.Before = &before
	event.ReleaseStartedAtUTC = time.Now().UTC()
	previous, released, releaseErr := owner.releaseForChurn(ctx, index)
	event.Endpoint, event.OldGeneration, event.OldSourcePort = previous.endpoint, previous.generation, previous.sourcePort
	if previous.session != nil {
		event.OldDiscriminator = previous.session.LocalDiscriminator()
	}
	if errors.Is(releaseErr, errChurnNotUp) {
		event.SkippedNotUp = true
		return event, nil
	}
	if !released {
		return event, releaseErr
	}
	event.ReleasedAtUTC = time.Now().UTC()
	absent, snapshotErr := owner.registrationSnapshot()
	if snapshotErr == nil {
		event.Absent = &absent
	}
	if joined := errors.Join(releaseErr, snapshotErr); joined != nil {
		return event, joined
	}
	if waitErr := awaitPhaseTime(ctx, time.Now().Add(sessionChurnGap)); waitErr != nil {
		return event, waitErr
	}
	current, err := owner.createForChurn(ctx, index, profile)
	if err != nil {
		return event, err
	}
	event.CreatedAtUTC, event.NewGeneration = time.Now().UTC(), current.generation
	event.NewDiscriminator, event.NewSourcePort = current.session.LocalDiscriminator(), current.sourcePort
	recoveryCtx, cancel := context.WithTimeout(ctx, sessionChurnBudget)
	defer cancel()
	if recoveryErr := awaitChurnRecovery(recoveryCtx, owner, index, current.generation); recoveryErr != nil {
		return event, recoveryErr
	}
	event.RecoveredAtUTC = time.Now().UTC()
	restored, err := owner.registrationSnapshot()
	if err == nil {
		event.Restored = &restored
	}
	return event, err
}

func awaitChurnRecovery(ctx context.Context, owner *Owner, index int, generation uint64) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := owner.churnRecovered(ctx, index, generation)
		if err != nil || ready {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for session churn recovery: %w", ctx.Err())
		case <-owner.Done():
			return fmt.Errorf("owner stopped during session churn: %w", errOwnerConfig)
		case <-ticker.C:
		}
	}
}
