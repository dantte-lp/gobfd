package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"
)

type timerSample struct {
	kind  TimerKind
	event TimerEvent
	delay time.Duration
}

type timerTestMetrics struct {
	noopMetrics

	samples []timerSample
	effect  chan struct{}
}

func (m *timerTestMetrics) ObserveTimerEvent(kind TimerKind, event TimerEvent, delay time.Duration) {
	m.samples = append(m.samples, timerSample{kind, event, delay})
	if event == TimerEffect && m.effect != nil {
		close(m.effect)
	}
}

type timerTestSender func(context.Context, []byte, netip.Addr) error

func (f timerTestSender) SendPacket(ctx context.Context, packet []byte, addr netip.Addr) error {
	return f(ctx, packet, addr)
}

func newTimerTestControl(t *testing.T, metrics *timerTestMetrics, sender PacketSender) *Session {
	t.Helper()
	s, err := NewSession(SessionConfig{
		PeerAddr: netip.MustParseAddr("192.0.2.1"), LocalAddr: netip.MustParseAddr("192.0.2.2"), IfIndex: 1,
		Type: SessionTypeSingleHop, Role: RoleActive, DesiredMinTxInterval: 100 * time.Millisecond,
		RequiredMinRxInterval: 100 * time.Millisecond, DetectMultiplier: 3,
	}, 42, sender, nil, slog.New(slog.DiscardHandler), WithMetrics(metrics))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func startTimerTestOwner(t *testing.T, run func(context.Context)) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx) }()
	return func() { cancel(); <-done }
}

func requireTimerSamples(t *testing.T, metrics *timerTestMetrics, want ...timerSample) {
	t.Helper()
	if len(metrics.samples) != len(want) {
		t.Fatalf("timer observations=%v, want%v", metrics.samples, want)
	}
	for i := range want {
		if metrics.samples[i] != want[i] {
			t.Fatalf("timer observation%d=%v, want%v", i, metrics.samples[i], want[i])
		}
	}
}

func TestControlTimerDispatchAndSendEffect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		metrics := &timerTestMetrics{}
		s := newTimerTestControl(t, metrics, timerTestSender(func(context.Context, []byte, netip.Addr) error {
			time.Sleep(15 * time.Millisecond)
			return nil
		}))
		tx, detect := time.NewTimer(10*time.Millisecond), time.NewTimer(time.Hour)
		defer tx.Stop()
		defer detect.Stop()
		synctest.Sleep(20 * time.Millisecond)
		stop := startTimerTestOwner(t, func(ctx context.Context) { s.runLoop(ctx, tx, detect) })
		defer stop()
		synctest.Sleep(15 * time.Millisecond)
		requireTimerSamples(t, metrics,
			timerSample{TimerControlTX, TimerDispatched, 10 * time.Millisecond},
			timerSample{TimerControlTX, TimerEffect, 25 * time.Millisecond})
	})
}

func TestControlTimerNoEffect(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "suppressed", true: "send_failure"}[fail], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				metrics := &timerTestMetrics{}
				s := newTimerTestControl(t, metrics, timerTestSender(func(context.Context, []byte, netip.Addr) error {
					return errors.New("test send failure")
				}))
				if !fail {
					s.role = RolePassive
				}
				tx, detect := time.NewTimer(10*time.Millisecond), time.NewTimer(time.Hour)
				defer tx.Stop()
				defer detect.Stop()
				stop := startTimerTestOwner(t, func(ctx context.Context) { s.runLoop(ctx, tx, detect) })
				defer stop()
				synctest.Sleep(10 * time.Millisecond)
				requireTimerSamples(t, metrics,
					timerSample{TimerControlTX, TimerDispatched, 0}, timerSample{TimerControlTX, TimerNoEffect, 0})
			})
		})
	}
}

func TestControlDetectionEffectPrecedesAnnouncement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		metrics := &timerTestMetrics{}
		s := newTimerTestControl(t, metrics, timerTestSender(func(context.Context, []byte, netip.Addr) error {
			time.Sleep(20 * time.Millisecond)
			return nil
		}))
		s.cachedState = StateUp
		s.commitObservation(StateUp, DiagNone, CauseUnknown)
		tx, detect := time.NewTimer(time.Hour), time.NewTimer(10*time.Millisecond)
		defer tx.Stop()
		defer detect.Stop()
		stop := startTimerTestOwner(t, func(ctx context.Context) { s.runLoop(ctx, tx, detect) })
		defer stop()
		synctest.Sleep(10 * time.Millisecond)
		if s.State() != StateDown {
			t.Fatal("detection state not committed before blocked announcement")
		}
		requireTimerSamples(t, metrics,
			timerSample{TimerControlDetection, TimerDispatched, 0}, timerSample{TimerControlDetection, TimerEffect, 0})
		synctest.Sleep(20 * time.Millisecond)
	})
}

func TestEchoTimerObservations(t *testing.T) {
	t.Parallel()
	for _, detection := range []bool{false, true} {
		t.Run(map[bool]string{false: "transmit", true: "detection"}[detection], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				metrics := &timerTestMetrics{}
				es, err := NewEchoSession(EchoSessionConfig{
					PeerAddr: netip.MustParseAddr("192.0.2.1"), TxInterval: 100 * time.Millisecond, DetectMultiplier: 3,
				}, 42, timerTestSender(func(context.Context, []byte, netip.Addr) error {
					time.Sleep(15 * time.Millisecond)
					return nil
				}), nil, slog.New(slog.DiscardHandler), WithEchoMetrics(metrics))
				if err != nil {
					t.Fatal(err)
				}
				tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
				defer tx.Stop()
				defer detect.Stop()
				kind, effect := TimerEchoTX, 15*time.Millisecond
				if detection {
					kind, effect = TimerEchoDetection, 0
					es.transitionTo(t.Context(), StateUp, DiagNone)
					detect.Reset(10 * time.Millisecond)
				} else {
					tx.Reset(10 * time.Millisecond)
				}
				stop := startTimerTestOwner(t, func(ctx context.Context) { es.echoRunLoop(ctx, tx, detect) })
				defer stop()
				synctest.Sleep(25 * time.Millisecond)
				requireTimerSamples(t, metrics, timerSample{kind, TimerDispatched, 0}, timerSample{kind, TimerEffect, effect})
			})
		})
	}
}

func TestTXTimerEffectPrecedesPollBookkeeping(t *testing.T) {
	t.Parallel()
	metrics := &timerTestMetrics{effect: make(chan struct{})}
	locked, done := make(chan struct{}), make(chan struct{})
	s := newTimerTestControl(t, metrics, nil)
	s.pollActive = true
	s.sender = timerTestSender(func(context.Context, []byte, netip.Addr) error {
		s.mu.Lock()
		close(locked)
		return nil
	})
	s.timerEvent.begin(metrics, TimerControlTX, time.Now())
	go func() { defer close(done); s.sendControl(t.Context()) }()
	<-locked
	observed := false
	select {
	case <-metrics.effect:
		observed = true
	case <-time.After(time.Second):
	}
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sender did not finish after releasing bookkeeping lock")
	}
	if !observed {
		t.Fatal("successful transport effect waited for later Poll bookkeeping")
	}
}

func TestTimerObservationInvalidAndScope(t *testing.T) {
	t.Parallel()
	for _, due := range []time.Time{{}, time.Now().Add(time.Hour)} {
		metrics := &timerTestMetrics{}
		var observation timerObservation
		observation.effect(metrics, TimerControlTX, time.Now())
		observation.begin(metrics, TimerControlTX, due)
		observation.effect(metrics, TimerEchoTX, time.Now())
		observation.effect(metrics, TimerControlTX, time.Now())
		observation.effect(metrics, TimerControlTX, time.Now())
		observation.finish(metrics)
		observation.effect(metrics, TimerControlTX, time.Now())
		requireTimerSamples(t, metrics,
			timerSample{TimerControlTX, TimerInvalidDispatch, 0}, timerSample{TimerControlTX, TimerInvalidEffect, 0})
	}
}

func TestTimerPanicDoesNotComplete(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		metrics := &timerTestMetrics{}
		s := newTimerTestControl(t, metrics, timerTestSender(func(context.Context, []byte, netip.Addr) error {
			panic("test sender panic")
		}))
		tx, detect := time.NewTimer(10*time.Millisecond), time.NewTimer(time.Hour)
		defer tx.Stop()
		defer detect.Stop()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan any, 1)
		go func() {
			defer func() { done <- recover() }()
			s.runLoop(ctx, tx, detect)
		}()
		synctest.Sleep(10 * time.Millisecond)
		select {
		case value := <-done:
			if value == nil {
				t.Fatal("sender panic did not propagate")
			}
		default:
			t.Fatal("owner did not exit on sender panic")
		}
		requireTimerSamples(t, metrics, timerSample{TimerControlTX, TimerDispatched, 0})
	})
}

func BenchmarkTimerObservation(b *testing.B) {
	metrics := noopMetrics{}
	b.ReportAllocs()
	for b.Loop() {
		var observation timerObservation
		observation.begin(metrics, TimerControlTX, time.Now())
		observation.effect(metrics, TimerControlTX, time.Now())
		observation.finish(metrics)
	}
}

func TestControlDetectionNoEffect(t *testing.T) {
	t.Parallel()
	for _, state := range []State{StateDown, StateAdminDown} {
		t.Run(state.String(), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				metrics := &timerTestMetrics{}
				s := newTimerTestControl(t, metrics, timerTestSender(func(context.Context, []byte, netip.Addr) error {
					return nil
				}))
				s.cachedState = state
				s.commitObservation(state, DiagNone, CauseUnknown)
				tx, detect := time.NewTimer(time.Hour), time.NewTimer(10*time.Millisecond)
				defer tx.Stop()
				defer detect.Stop()
				stop := startTimerTestOwner(t, func(ctx context.Context) { s.runLoop(ctx, tx, detect) })
				defer stop()
				synctest.Sleep(10 * time.Millisecond)
				requireTimerSamples(t, metrics, timerSample{TimerControlDetection, TimerDispatched, 0},
					timerSample{TimerControlDetection, TimerNoEffect, 0})
				if s.State() != state {
					t.Fatal("idle/admin detection changed state")
				}
			})
		})
	}
}

func TestImmediateActionsHaveNoTimerObservation(t *testing.T) {
	t.Parallel()
	metrics := &timerTestMetrics{}
	s := newTimerTestControl(t, metrics, timerTestSender(func(context.Context, []byte, netip.Addr) error { return nil }))
	s.sendControl(t.Context())
	s.cachedState = StateUp
	s.commitObservation(StateUp, DiagNone, CauseUnknown)
	tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	defer tx.Stop()
	defer detect.Stop()
	s.handleDetectTimer(t.Context(), tx, detect)
	requireTimerSamples(t, metrics)
}
