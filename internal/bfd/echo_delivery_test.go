package bfd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func newEchoOwnerDeliveryStep(tb testing.TB, handler slog.Handler) func() (StateChange, StateChange, uint64) {
	tb.Helper()
	mgr := newEchoLeaseManager()
	tb.Cleanup(mgr.Close)
	ch := make(chan StateChange, 1)
	es, err := NewEchoSession(echoLeaseConfig("192.0.2.61"), 42, echoLeaseSender{}, ch,
		slog.New(handler), withEchoDelivery(mgr))
	if err != nil {
		tb.Fatal(err)
	}
	detect := time.NewTimer(time.Hour)
	tb.Cleanup(func() { detect.Stop() })
	ctx := tb.Context()
	return func() (StateChange, StateChange, uint64) {
		es.RecvEcho()
		<-es.recvCh
		es.handleEchoRecv(ctx, detect)
		up := <-ch
		es.handleDetectTimeout(ctx, detect)
		return up, <-ch, mgr.delivery[DeliveryEchoOwner][DeliveryCommitted].Load()
	}
}

func BenchmarkEchoOwnerDelivery(b *testing.B) {
	for _, tc := range []struct {
		name    string
		handler slog.Handler
	}{
		{name: "disabled", handler: slog.DiscardHandler},
		//nolint:sloglint // Allocation regression requires actual JSON encoding, not a disabled logger.
		{name: "enabled_JSON", handler: slog.NewJSONHandler(io.Discard, nil)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			step := newEchoOwnerDeliveryStep(b, tc.handler)
			// Measure without -race, which may discard the JSON handler's pooled buffers.
			if allocs := testing.AllocsPerRun(100, func() { step() }); allocs != 0 {
				b.Fatalf("Echo admission/owner/publication = %g allocs/op, want 0", allocs)
			}
			b.ReportAllocs()
			for b.Loop() {
				step()
			}
		})
	}
}

func TestEchoOwnerDelivery(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			var logs bytes.Buffer
			handler := slog.DiscardHandler
			if enabled {
				handler = slog.NewJSONHandler(&logs, nil)
			}
			step := newEchoOwnerDeliveryStep(t, handler)
			for i := range 100 {
				up, down, committed := step()
				if up.OldState != StateDown || up.NewState != StateUp ||
					down.OldState != StateUp || down.NewState != StateDown || down.Diag != DiagEchoFailed ||
					up.echoSource == nil || up.echoSource != down.echoSource || committed != uint64(i+1) ||
					up.echoSource.EchosReceived() != uint64(i+1) || up.echoSource.StateTransitions() != uint64(2*(i+1)) {
					t.Fatalf("Echo owner/publication = %+v, %+v, committed=%d", up, down, committed)
				}
			}
			want := 0
			if enabled {
				want = 200
			}
			if got := bytes.Count(logs.Bytes(), []byte{'\n'}); got != want {
				t.Errorf("serialized JSON records=%d, want %d", got, want)
			}
		})
	}
}

func TestEchoDemuxRejectsFullQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sender := &lifecycleBlockingSender{entered: make(chan struct{}), release: make(chan struct{})}
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		defer close(sender.release)
		cfg := echoLeaseConfig("192.0.2.60")
		cfg.TxInterval = time.Second
		discr, err := mgr.CreateEchoSession(t.Context(), cfg, sender)
		if err != nil {
			t.Fatal(err)
		}
		<-sender.entered // Hold the owner at its send boundary while filling RX.
		mgr.mu.RLock()
		es := mgr.echoSessions[discr].session
		mgr.mu.RUnlock()
		received := es.EchosReceived()
		for range cap(es.recvCh) {
			es.RecvEcho()
		}
		if len(es.recvCh) != cap(es.recvCh) || es.EchosReceived() != received {
			t.Fatal("echo receive queue was not filled while the owner was blocked")
		}
		if _, err := mgr.DemuxEcho(discr); !errors.Is(err, ErrDeliveryQueueFull) {
			t.Errorf("full echo receive queue reported %v, want ErrDeliveryQueueFull", err)
		}
		if len(es.recvCh) != cap(es.recvCh) || es.EchosReceived() != received {
			t.Fatal("refused echo changed the queued or owner-committed receive count")
		}
	})
}

func TestEchoCanceledFirstRunAbandonsPreRunQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		es, err := NewEchoSession(echoLeaseConfig("192.0.2.62"), 42, echoLeaseSender{}, nil,
			mgr.logger, withEchoDelivery(mgr))
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if es.RecvEcho() != DeliveryQueued {
				t.Fatal("pre-Run echo admission was refused")
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		es.Run(ctx)
		es.Run(t.Context())
		if len(es.recvCh) != 0 || es.EchosReceived() != 0 || es.RecvEcho() != DeliveryClosed ||
			deliveryCount(t, mgr, DeliveryEchoOwner, DeliveryAbandoned) != 2 ||
			deliveryCount(t, mgr, DeliveryEchoOwner, DeliveryCommitted) != 0 {
			t.Fatal("canceled first owner did not abandon once and permanently close admission")
		}
	})
}

func TestEchoConcurrentRunAndAdmissionStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		es, err := NewEchoSession(echoLeaseConfig("192.0.2.63"), 42, echoLeaseSender{}, nil,
			mgr.logger, withEchoDelivery(mgr))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() { es.Run(ctx); close(done) }()
		synctest.Wait()
		es.Run(t.Context())
		if es.RecvEcho() != DeliveryQueued {
			t.Fatal("losing Run closed the winning owner's admission")
		}
		var producers sync.WaitGroup
		producers.Go(func() {
			for range 100 {
				es.RecvEcho()
			}
		})
		cancel()
		producers.Wait()
		<-done
		if es.RecvEcho() != DeliveryClosed || len(es.recvCh) != 0 {
			t.Fatal("stopped echo owner retained or admitted RX")
		}
		queued := deliveryCount(t, mgr, DeliveryEchoAdmission, DeliveryQueued)
		committed := deliveryCount(t, mgr, DeliveryEchoOwner, DeliveryCommitted)
		abandoned := deliveryCount(t, mgr, DeliveryEchoOwner, DeliveryAbandoned)
		if queued != committed+abandoned || es.EchosReceived() != committed ||
			queued+deliveryCount(t, mgr, DeliveryEchoAdmission, DeliveryQueueFull)+
				deliveryCount(t, mgr, DeliveryEchoAdmission, DeliveryClosed) != 102 {
			t.Fatalf("Echo admission/stop lost or duplicated delivery: queued=%d committed=%d abandoned=%d",
				queued, committed, abandoned)
		}
	})
}

func TestEchoDemuxRejectsStoppedOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := newEchoLeaseManager()
		defer mgr.Close()
		discr, err := mgr.CreateEchoSession(t.Context(), echoLeaseConfig("192.0.2.67"), echoLeaseSender{})
		if err != nil {
			t.Fatal(err)
		}
		mgr.mu.RLock()
		entry := mgr.echoSessions[discr]
		mgr.mu.RUnlock()
		entry.cancel()
		<-entry.done
		outcome, err := mgr.DemuxEcho(discr)
		if outcome != DeliveryClosed || !errors.Is(err, ErrDeliveryClosed) ||
			deliveryCount(t, mgr, DeliveryEchoAdmission, DeliveryClosed) != 1 {
			t.Fatalf("stopped Echo owner delivery=%s err=%v", outcome, err)
		}
	})
}
