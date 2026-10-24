//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

const (
	ownerSamplePeriod = time.Second
	rssWindowDuration = 6 * time.Hour
	maxDownEvents     = 8 * sourcePortCapacity
)

// RSSWindow is a bounded-memory mean of observed process RSS, not an integral.
type RSSWindow struct {
	Samples   uint64  `json:"samples"`
	MeanBytes float64 `json:"mean_bytes"`
}

// ResourceWindow retains sampled peaks; short-lived unsampled peaks remain unknown.
type ResourceWindow struct {
	Samples        uint64    `json:"samples"`
	PeakRSSBytes   uint64    `json:"peak_rss_bytes"`
	PeakThreads    uint64    `json:"peak_threads"`
	PeakFDs        uint64    `json:"peak_fds"`
	PeakGoroutines int       `json:"peak_goroutines"`
	FirstRSS       RSSWindow `json:"first_rss"`
	LastRSS        RSSWindow `json:"last_rss"`
}

func (w *RSSWindow) add(rss uint64) {
	w.Samples++
	w.MeanBytes += (float64(rss) - w.MeanBytes) / float64(w.Samples)
}

func (w *ResourceWindow) observe(environment Environment, elapsed, duration time.Duration) {
	w.Samples++
	w.PeakRSSBytes = max(w.PeakRSSBytes, environment.Process.RSSBytes)
	w.PeakThreads = max(w.PeakThreads, environment.Process.Threads)
	w.PeakFDs = max(w.PeakFDs, environment.Process.FDs)
	w.PeakGoroutines = max(w.PeakGoroutines, environment.Runtime.Goroutines)
	if elapsed < rssWindowDuration {
		w.FirstRSS.add(environment.Process.RSSBytes)
	}
	if elapsed >= duration-rssWindowDuration {
		w.LastRSS.add(environment.Process.RSSBytes)
	}
}

// OwnerDownEvent preserves transition time for subsequent correlation with peer omissions.
// It does not classify a Down as intentional without the independent peer report.
type OwnerDownEvent struct {
	AtUTC              time.Time `json:"at_utc"`
	ElapsedNS          int64     `json:"elapsed_ns"`
	LocalDiscriminator uint32    `json:"local_discriminator"`
	Diagnostic         string    `json:"diagnostic"`
}

// OwnerWorkloadObservation covers the production core, not all daemon surfaces.
// Completed means the requested observation duration ended; it does not prove
// that every session stayed Up or that latency and clock acceptance passed.
type OwnerWorkloadObservation struct {
	Scope              string                 `json:"scope"`
	Config             PeerConfig             `json:"config"`
	Admission          *ResourceAdmission     `json:"admission,omitempty"`
	StartedAtUTC       time.Time              `json:"started_at_utc"`
	ElapsedNS          int64                  `json:"elapsed_ns"`
	SamplePeriodNS     int64                  `json:"sample_period_ns"`
	Completed          bool                   `json:"completed"`
	ReferenceQualified bool                   `json:"reference_qualified"`
	Initial            OwnerObservation       `json:"initial"`
	Final              OwnerObservation       `json:"final"`
	Resources          ResourceWindow         `json:"resources"`
	SchedulerWindow    Histogram              `json:"scheduler_window"`
	GCPauseWindow      Histogram              `json:"gc_pause_window"`
	DownEvents         []OwnerDownEvent       `json:"down_events"`
	DownEventsOmitted  uint64                 `json:"down_events_omitted"`
	NotificationGaps   uint64                 `json:"notification_gaps"`
	Error              string                 `json:"error,omitempty"`
	Phase              *Phase                 `json:"phase,omitempty"`
	MeasurementFinal   OwnerObservation       `json:"measurement_final"`
	CPUPressure        CPUPressureObservation `json:"cpu_pressure"`
	GCPressure         GCPressureObservation  `json:"gc_pressure"`
}

// RunOwner uses the peer manifest to run the actual production core and collector.
// SourcePortBase describes the independent peers; owner senders keep their allocator.
func RunOwner(ctx context.Context, cfg PeerConfig, logger *slog.Logger) (OwnerWorkloadObservation, error) {
	result := OwnerWorkloadObservation{
		Scope: "production-core-owner-workload", Config: cfg,
		SamplePeriodNS: int64(ownerSamplePeriod),
	}
	if err := cfg.Validate(); err != nil {
		result.Error = err.Error()
		return result, err
	}
	if cfg.Profile.Kind != fixtureProfile {
		admission, err := AdmitReferenceResources(ctx, len(cfg.Sessions))
		result.Admission = &admission
		if err != nil {
			result.Error = err.Error()
			return result, err
		}
	}
	if cfg.ControlDirectory != "" {
		if err := waitListenerReady(ctx, cfg.ControlDirectory, false); err != nil {
			result.Error = err.Error()
			return result, err
		}
	}
	guard, err := NewClockEpochGuard(ctx)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	owner, err := StartOwner(ctx, OwnerConfig{
		Sessions: cfg.Sessions,
		Interval: cfg.Profile.Interval, DetectMultiplier: cfg.Profile.DetectMultiplier,
	}, logger)
	if err != nil {
		err = errors.Join(err, guard.Close())
		result.Error = err.Error()
		return result, err
	}
	started, err := result.runWindow(ctx, owner)
	return result.finish(ctx, owner, guard, started, err)
}

func (r *OwnerWorkloadObservation) observe(ctx context.Context, owner *Owner, started time.Time) error {
	changes, err := owner.manager.SubscribeStateChanges(ctx)
	if err != nil {
		return fmt.Errorf("subscribe owner workload transitions: %w", err)
	}
	timer := time.NewTimer(max(0, time.Until(started.Add(r.Config.Profile.Duration))))
	defer timer.Stop()
	ticker := time.NewTicker(ownerSamplePeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("owner workload interrupted: %w", ctx.Err())
		case <-owner.Done():
			return fmt.Errorf("owner stopped before observation completed: %w", errors.Join(errOwnerConfig, ctx.Err()))
		case <-timer.C:
			r.Completed = true
			return nil
		case <-ticker.C:
			environment, err := CaptureEnvironment(ctx)
			if err != nil {
				return err
			}
			r.Resources.observe(environment, time.Since(started), r.Config.Profile.Duration)
		case change, open := <-changes:
			if !open {
				return fmt.Errorf("owner workload subscription closed: %w", errors.Join(errOwnerConfig, ctx.Err()))
			}
			r.observeChange(change, started)
		}
	}
}

func (r *OwnerWorkloadObservation) observeChange(change bfd.StateChange, started time.Time) {
	if change.Gap {
		r.NotificationGaps++
		return
	}
	if change.Added || change.Deleted || change.OldState != bfd.StateUp || change.NewState != bfd.StateDown {
		return
	}
	if len(r.DownEvents) == maxDownEvents {
		r.DownEventsOmitted++
		return
	}
	r.DownEvents = append(r.DownEvents, OwnerDownEvent{
		AtUTC: change.Timestamp.UTC(), ElapsedNS: int64(change.Timestamp.Sub(started)),
		LocalDiscriminator: change.LocalDiscr, Diagnostic: change.Diag.String(),
	})
}

func (r *OwnerWorkloadObservation) finish(
	ctx context.Context, owner *Owner, guard *ClockEpochGuard, started time.Time, runErr error,
) (OwnerWorkloadObservation, error) {
	runErr = errors.Join(runErr, owner.Close())
	observeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var snapshotErr error
	r.Final, snapshotErr = owner.Snapshot(observeCtx)
	epoch, epochErr := guard.Finish(observeCtx)
	r.Final = r.Final.WithClockEpoch(epoch)
	runErr = errors.Join(runErr, snapshotErr, epochErr, guard.Close())
	if snapshotErr == nil && len(r.Initial.Sessions) != 0 {
		measuredFinal := r.Final
		if r.Phase != nil && len(r.MeasurementFinal.Sessions) != 0 {
			measuredFinal = r.MeasurementFinal
		}
		var schedulerErr, gcErr error
		r.SchedulerWindow, schedulerErr = histogramDelta(r.Initial.Environment.Runtime.SchedulerLatency,
			measuredFinal.Environment.Runtime.SchedulerLatency)
		r.GCPauseWindow, gcErr = histogramDelta(r.Initial.Environment.Runtime.GCPauses,
			measuredFinal.Environment.Runtime.GCPauses)
		runErr = errors.Join(runErr, schedulerErr, gcErr)
	}
	r.ElapsedNS = int64(time.Since(started))
	if runErr != nil {
		r.Completed, r.Error = false, runErr.Error()
	}
	return *r, runErr
}
