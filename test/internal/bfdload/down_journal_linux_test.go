//go:build linux

package bfdload

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdmetrics "github.com/dantte-lp/gobfd/internal/metrics"
)

func TestDownJournalCutoffAccountsRealManagerTransitions(t *testing.T) {
	for _, mode := range []string{"buffered", "delayed_dispatch", "stopped_dispatch", "source_after_cutoff"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				owner, session, changes := journalManager(ctx, t)
				dispatched := make(chan struct{})
				defer func() {
					cancel()
					<-dispatched
					owner.manager.Close()
				}()
				switch mode {
				case "buffered", "source_after_cutoff":
					go func() { defer close(dispatched); owner.manager.RunDispatch(ctx) }()
				case "stopped_dispatch":
					close(dispatched)
				}
				started := time.Now()
				journalTransition(t, session)
				synctest.Sleep(2 * time.Millisecond)
				if mode == "delayed_dispatch" {
					go func() {
						defer close(dispatched)
						synctest.Sleep(10 * time.Millisecond)
						owner.manager.RunDispatch(ctx)
					}()
				}
				if mode == "source_after_cutoff" {
					journalTransition(t, session)
				}
				report := OwnerWorkloadObservation{Config: PeerConfig{Profile: Profile{Duration: time.Millisecond}}}
				err := report.observe(ctx, owner, started)
				if err != nil {
					t.Fatal(err)
				}
				journal := startDownJournal(ctx, changes, nil)
				if mode == "delayed_dispatch" {
					synctest.Sleep(20 * time.Millisecond)
				}
				owner.manager.Close()
				if drainErr := report.joinDownJournal(ctx, started, journal); drainErr != nil {
					t.Fatal(drainErr)
				}
				families, err := owner.registry.Gather()
				if err != nil {
					t.Fatal(err)
				}
				report.Final = OwnerObservation{Stopped: true, Metrics: families}
				err = report.finalizeDownJournal()
				if mode == "stopped_dispatch" {
					if err == nil || report.DownJournal.Complete {
						t.Fatalf("missing pre-cutoff Manager transition silently accepted: journal=%+v err=%v", report.DownEvents, err)
					}
					return
				}
				if err != nil || len(report.DownEvents) != 1 {
					t.Fatalf("pre-cutoff Manager transition lost: journal=%+v err=%v", report.DownEvents, err)
				}
				//nolint:musttag // The workload intentionally retains core Observation field names.
				wire, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				var receipt struct {
					Journal *struct {
						Source   uint64    `json:"source_transitions"`
						Observed uint64    `json:"observed_before_cutoff"`
						Settled  bool      `json:"settled"`
						Cutoff   time.Time `json:"cutoff_at_utc"`
					} `json:"down_journal"`
				}
				if err := json.Unmarshal(wire, &receipt); err != nil {
					t.Fatal(err)
				}
				wantSource := uint64(1)
				if mode == "source_after_cutoff" {
					wantSource = 2
				}
				if receipt.Journal == nil || !receipt.Journal.Settled || receipt.Journal.Source != wantSource ||
					receipt.Journal.Observed != 1 || !receipt.Journal.Cutoff.Equal(started.Add(time.Millisecond)) {
					t.Fatalf("no independently settled cutoff receipt: %s", wire)
				}
			})
		})
	}
}

func journalManager(ctx context.Context, t *testing.T) (*Owner, *bfd.Session, <-chan bfd.StateChange) {
	t.Helper()
	registry := prometheus.NewRegistry()
	collector := bfdmetrics.NewCollector(registry)
	manager := bfd.NewManager(slog.New(slog.DiscardHandler), bfd.WithManagerMetrics(collector))
	changes, err := manager.SubscribeStateChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.CreateSession(ctx, bfd.SessionConfig{
		LocalAddr: netip.MustParseAddr("192.0.2.1"), PeerAddr: netip.MustParseAddr("192.0.2.2"),
		Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
		DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
	}, func() (*bfd.SenderLease, error) { return bfd.NewSenderLease(journalSender{}, nil), nil })
	if err != nil {
		manager.Close()
		t.Fatal(err)
	}
	return &Owner{manager: manager, registry: registry, done: make(chan struct{})}, session, changes
}

func TestDownJournalFinalSourceRequiresEveryLateNotification(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owner, session, changes := journalManager(ctx, t)
			defer owner.manager.Close()
			go owner.manager.RunDispatch(ctx)
			started := time.Now()
			journalTransition(t, session)
			synctest.Sleep(2 * time.Millisecond)
			report := OwnerWorkloadObservation{Config: PeerConfig{Profile: Profile{Duration: time.Millisecond}}}
			if err := report.observe(ctx, owner, started); err != nil {
				t.Fatal(err)
			}
			// Later transitions cannot replace a missing pre-cutoff notification.
			journalTransition(t, session)
			owner.manager.Close()
			families, err := owner.registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			report.Final = OwnerObservation{Stopped: true, Metrics: families}
			if !delivered {
				filtered := make(chan bfd.StateChange, cap(changes))
				for change := range changes {
					if change.NewState != bfd.StateDown || change.OldState != bfd.StateUp ||
						change.Timestamp.Before(started.Add(time.Millisecond)) {
						filtered <- change
					}
				}
				close(filtered)
				changes = filtered
			}
			if drainErr := report.joinDownJournal(ctx, started, startDownJournal(ctx, changes, nil)); drainErr != nil {
				t.Fatal(drainErr)
			}
			err = report.finalizeDownJournal()
			if delivered {
				if err != nil || !report.DownJournal.Complete || len(report.DownEvents) != 1 {
					t.Fatalf("fully delivered late transition invalidated the measured journal: %+v err=%v", report.DownJournal, err)
				}
			} else if !errors.Is(err, errDownJournalIncomplete) || report.DownJournal.Complete {
				t.Fatalf("missing late transition escaped the joined final check: %+v err=%v", report.DownJournal, err)
			}
		})
	}
}

func TestDownJournalRejectsLostOrCanceledDelivery(t *testing.T) {
	for _, mode := range []string{"gap", "overflow", "canceled", "unclosed"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				owner, session, changes := journalManager(ctx, t)
				defer owner.manager.Close()
				go owner.manager.RunDispatch(ctx)
				started := time.Now()
				journalTransition(t, session)
				if mode == "gap" {
					for range 40 {
						journalTransition(t, session)
					}
				}
				synctest.Sleep(2 * time.Millisecond)
				report := OwnerWorkloadObservation{Config: PeerConfig{Profile: Profile{Duration: time.Millisecond}}}
				if mode == "overflow" {
					report.DownEvents = make([]OwnerDownEvent, maxDownEvents)
				}
				if mode == "canceled" {
					cancel()
				}
				if err := report.captureDownJournal(owner, started); err != nil {
					t.Fatal(err)
				}
				owner.manager.Close()
				if mode == "unclosed" {
					changes = nil
				}
				joinCtx, stopJoin := context.WithTimeout(ctx, time.Second)
				defer stopJoin()
				drainErr := report.joinDownJournal(joinCtx, started, startDownJournal(ctx, changes, nil))
				families, err := owner.registry.Gather()
				if err != nil {
					t.Fatal(err)
				}
				report.Final = OwnerObservation{Stopped: true, Metrics: families}
				if err := errors.Join(drainErr, report.finalizeDownJournal()); !errors.Is(err, errDownJournalIncomplete) ||
					report.DownJournal.Settled || report.DownJournal.Complete {
					t.Fatalf("lost/closed journal was accepted: %+v err=%v", report.DownJournal, err)
				}
			})
		})
	}
}

func TestSourceDownTransitionsRejectsInvalidCounters(t *testing.T) {
	t.Parallel()
	if _, err := sourceDownTransitions(nil); !errors.Is(err, errOwnerConfig) {
		t.Fatal("missing source counter was accepted")
	}
	name, kind := "gobfd_bfd_state_transitions_total", dto.MetricType_COUNTER
	labels := make([]*dto.LabelPair, 0, 4)
	for key, value := range map[string]string{
		"peer_addr": "192.0.2.2", "local_addr": "192.0.2.1", "from_state": "Up", "to_state": "Down",
	} {
		labels = append(labels, &dto.LabelPair{Name: &key, Value: &value})
	}
	for _, value := range []float64{-1, 0.5, math.NaN(), math.Inf(1), float64(uint64(1) << 53)} {
		families := []*dto.MetricFamily{{Name: &name, Type: &kind, Metric: []*dto.Metric{{
			Label: labels, Counter: &dto.Counter{Value: &value},
		}}}}
		if _, err := sourceDownTransitions(families); !errors.Is(err, errOwnerConfig) {
			t.Fatalf("invalid source value %v accepted: %v", value, err)
		}
	}
}

func journalTransition(t *testing.T, session *bfd.Session) {
	t.Helper()
	packet := bfd.ControlPacket{
		Version: 1, State: bfd.StateInit, DetectMult: 3,
		MyDiscriminator: 100, YourDiscriminator: session.LocalDiscriminator(),
		DesiredMinTxInterval: 1000000, RequiredMinRxInterval: 1000000,
	}
	if session.RecvPacket(&packet) != bfd.DeliveryQueued {
		t.Fatal("real session refused Init")
	}
	synctest.Wait()
	if session.State() != bfd.StateUp || session.SetPathDown() != bfd.DeliveryQueued {
		t.Fatal("real session did not accept Up-to-Down")
	}
	synctest.Wait()
	if session.State() != bfd.StateDown {
		t.Fatal("real session did not commit Down")
	}
}

type journalSender struct{}

func (journalSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }
