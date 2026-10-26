//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

var errDownJournalIncomplete = errors.New("down journal is incomplete")

// DownJournalObservation settles notifications against the actual FSM counter.
// Complete requires a closed subscription and all source notifications after join.
type DownJournalObservation struct {
	CutoffAtUTC            time.Time `json:"cutoff_at_utc"`
	SourceSampledAtUTC     time.Time `json:"source_sampled_at_utc"`
	SourceTransitions      uint64    `json:"source_transitions"`
	FinalSourceTransitions *uint64   `json:"final_source_transitions"`
	ObservedBeforeCutoff   uint64    `json:"observed_before_cutoff"`
	ObservedAfterCutoff    uint64    `json:"observed_after_cutoff"`
	Closed                 bool      `json:"closed"`
	Settled                bool      `json:"settled"`
	Complete               bool      `json:"complete"`
	Error                  string    `json:"error,omitempty"`
}

type downTransition struct {
	at      time.Time
	discr   uint32
	diag    bfd.Diag
	witness OwnerDownWitness
	timing  bfd.TransitionTiming
}

type downJournalResult struct {
	events        []downTransition
	gaps, omitted uint64
	closed        bool
	err           error
}

type downJournal struct {
	cancel context.CancelFunc
	result chan downJournalResult
}

func startDownJournal(ctx context.Context, changes <-chan bfd.StateChange) *downJournal {
	runCtx, cancel := context.WithCancel(ctx)
	journal := &downJournal{cancel: cancel, result: make(chan downJournalResult, 1)}
	go func() { journal.result <- collectDownJournal(runCtx, changes) }()
	return journal
}

func collectDownJournal(ctx context.Context, changes <-chan bfd.StateChange) downJournalResult {
	var result downJournalResult
	for {
		if err := ctx.Err(); err != nil {
			result.err = err
			return result
		}
		select {
		case <-ctx.Done():
			result.err = ctx.Err()
			return result
		case change, open := <-changes:
			if !open {
				result.closed = true
				return result
			}
			if change.Gap {
				result.gaps++
			} else if !change.Added && !change.Deleted && change.OldState == bfd.StateUp && change.NewState == bfd.StateDown {
				if len(result.events) == maxDownEvents {
					result.omitted++
				} else {
					result.events = append(result.events, captureDownTransition(change))
				}
			}
		}
	}
}

func (j *downJournal) join(ctx context.Context) (downJournalResult, error) {
	defer j.cancel()
	select {
	case result := <-j.result:
		return result, result.err
	case <-ctx.Done():
		j.cancel()
		result := <-j.result
		return result, errors.Join(result.err, ctx.Err())
	}
}

func (r *OwnerWorkloadObservation) captureDownJournal(owner *Owner, started time.Time) error {
	r.DownJournal.CutoffAtUTC = started.Add(r.Config.Profile.Duration).UTC()
	families, err := owner.registry.Gather()
	if err != nil {
		return r.failDownJournal(err)
	}
	r.DownJournal.SourceTransitions, err = sourceDownTransitions(families)
	if err != nil {
		return r.failDownJournal(err)
	}
	r.DownJournal.SourceSampledAtUTC = time.Now().UTC()
	return nil
}

func (r *OwnerWorkloadObservation) joinDownJournal(
	ctx context.Context, started time.Time, journal *downJournal,
) error {
	result, err := journal.join(ctx)
	r.DownJournal.Closed = result.closed
	r.NotificationGaps += result.gaps
	r.DownEventsOmitted += result.omitted
	r.DownWitnesses = make([]OwnerDownWitness, len(result.events))
	for index, event := range result.events {
		r.observeChange(bfd.StateChange{
			OldState: bfd.StateUp, NewState: bfd.StateDown,
			Timestamp: event.at, LocalDiscr: event.discr, Diag: event.diag,
		}, started)
		witness := event.witness
		witness.ElapsedNS = int64(event.at.Sub(started))
		witness.Timing = projectDownTiming(event.timing, witness.ObservationVersion, started)
		r.DownWitnesses[index] = witness
	}
	if err != nil {
		return r.failDownJournal(err)
	}
	return nil
}

func (r *OwnerWorkloadObservation) finalizeDownJournal() error {
	count, err := sourceDownTransitions(r.Final.Metrics)
	if err != nil {
		return r.failDownJournal(err)
	}
	r.DownJournal.FinalSourceTransitions = &count
	before, after := r.DownJournal.ObservedBeforeCutoff, r.DownJournal.ObservedAfterCutoff
	// shortcut: unobserved startup flaps fail completeness; subscribe before startup if those must be accepted.
	if !r.Final.Stopped || !r.DownJournal.Closed || r.DownJournal.SourceSampledAtUTC.IsZero() ||
		r.NotificationGaps != 0 || r.DownEventsOmitted != 0 || r.DownJournal.Error != "" ||
		before > r.DownJournal.SourceTransitions || r.DownJournal.SourceTransitions > count || after != count-before {
		return r.failDownJournal(errOwnerConfig)
	}
	r.DownJournal.Settled, r.DownJournal.Complete = true, true
	return nil
}

func (r *OwnerWorkloadObservation) failDownJournal(cause error) error {
	err := fmt.Errorf("settle cutoff source=%d, observed before=%d after=%d, gaps=%d, omitted=%d: %w",
		r.DownJournal.SourceTransitions, r.DownJournal.ObservedBeforeCutoff, r.DownJournal.ObservedAfterCutoff,
		r.NotificationGaps, r.DownEventsOmitted,
		errors.Join(errDownJournalIncomplete, cause))
	r.DownJournal.Complete, r.DownJournal.Error = false, err.Error()
	return err
}

func sourceDownTransitions(families []*dto.MetricFamily) (uint64, error) {
	var count uint64
	found := false
	for _, family := range families {
		if family.GetName() != "gobfd_bfd_state_transitions_total" {
			continue
		}
		if family.GetType() != dto.MetricType_COUNTER {
			return 0, fmt.Errorf("invalid source transition family: %w", errOwnerConfig)
		}
		for _, metric := range family.GetMetric() {
			value, matched, err := downTransitionValue(metric)
			if err != nil {
				return 0, err
			}
			if !matched {
				continue
			}
			found = true
			if value > math.MaxUint64-count {
				return 0, fmt.Errorf("source transition counter overflow: %w", errOwnerConfig)
			}
			count += value
		}
	}
	if !found {
		return 0, fmt.Errorf("missing source Up-to-Down counter: %w", errOwnerConfig)
	}
	return count, nil
}

func downTransitionValue(metric *dto.Metric) (uint64, bool, error) {
	labels := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	if len(labels) != 4 || len(metric.GetLabel()) != 4 || labels["peer_addr"] == "" || labels["local_addr"] == "" {
		return 0, false, fmt.Errorf("invalid source transition labels: %w", errOwnerConfig)
	}
	if labels["from_state"] != "Up" || labels["to_state"] != "Down" {
		return 0, false, nil
	}
	value := metric.GetCounter().GetValue()
	const largestExactCounter = uint64(1) << 53
	if metric.GetCounter() == nil || value < 0 || value >= float64(largestExactCounter) || value != math.Trunc(value) {
		return 0, false, fmt.Errorf("invalid source transition counter: %w", errOwnerConfig)
	}
	return uint64(value), true, nil
}
