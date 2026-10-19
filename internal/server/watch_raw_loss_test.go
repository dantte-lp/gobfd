package server

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestWatchRawNotificationLossResyncsFinalOwnerDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logger := slog.New(slog.DiscardHandler)
		mgr := bfd.NewManager(logger)
		defer mgr.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		session, err := mgr.CreateSession(ctx, watchStatusConfig(),
			bfd.NonOwningSenderLeaseFactory(watchStatusSender{}))
		if err != nil {
			t.Fatal(err)
		}
		pkt := bfd.ControlPacket{
			Version: 1, State: bfd.StateInit, DetectMult: 3, Length: bfd.MinPacketSizeNoAuth,
			MyDiscriminator: 99, YourDiscriminator: session.LocalDiscriminator(),
			DesiredMinTxInterval: 100000000, RequiredMinRxInterval: 1000000,
		}
		if session.RecvPacket(&pkt) != bfd.DeliveryQueued {
			t.Fatal("initial Control receive was refused")
		}
		synctest.Wait()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		events := make(chan *bfdv1.WatchSessionEventsResponse, 16)
		done := make(chan error, 1)
		go func() {
			s := &BFDServer{manager: mgr, logger: logger}
			done <- s.watchLoop(ctx, sub, true, func(event *bfdv1.WatchSessionEventsResponse) error {
				select {
				case events <- event:
					return nil
				default:
					return errors.New("Watch fixture event buffer full")
				}
			})
		}()
		synctest.Wait()
		if len(events) != 2 {
			t.Fatalf("initial Watch events = %d, want Up snapshot and SYNC_COMPLETE", len(events))
		}
		initial, sync := <-events, <-events
		view := initial.GetSession()
		if initial.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED ||
			view.GetLocalDiscriminator() != session.LocalDiscriminator() ||
			view.GetLocalState() != bfdv1.SessionState_SESSION_STATE_UP || view.GetObservationVersion() == 0 ||
			sync.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE {
			t.Fatalf("initial Watch did not commit owner Up: %v, %v", initial, sync)
		}

		// Keep dispatch unstarted after the committed Up snapshot; stale noise cannot fill the subscriber.
		cfg := watchStatusConfig()
		cfg.PeerAddr = netip.MustParseAddr("192.0.2.82")
		noise, err := mgr.CreateSession(ctx, cfg, bfd.NonOwningSenderLeaseFactory(watchStatusSender{}))
		if err != nil {
			t.Fatal(err)
		}
		pkt.State, pkt.MyDiscriminator, pkt.YourDiscriminator = bfd.StateAdminDown, 100, noise.LocalDiscriminator()
		// Keep the target's detection deadline beyond the noise owner's bounded AdminDown drain.
		pkt.DesiredMinTxInterval = 1000000
		// One target Up plus 62 noise observations leave one raw slot for lawful AdminDown on deletion.
		for i := range 62 {
			pkt.ControlPlaneIndependent = i%2 == 0
			if noise.RecvPacket(&pkt) != bfd.DeliveryQueued {
				t.Fatal("noise receive was refused")
			}
			synctest.Wait()
		}
		if destroyErr := mgr.DestroySession(ctx, noise.LocalDiscriminator()); destroyErr != nil {
			t.Fatal(destroyErr)
		}
		synctest.Wait()
		if len(sub) != 0 || rawLossDeliveryCount(t, mgr, bfd.DeliveryControlNotification, bfd.DeliveryQueued) != 64 ||
			rawLossDeliveryCount(t, mgr, bfd.DeliveryControlNotification, bfd.DeliveryQueueFull) != 0 {
			t.Fatal("fixture did not fill only the raw queue without any lost owner notification")
		}
		if session.SetPathDown() != bfd.DeliveryQueued {
			t.Fatal("final PathDown command was refused")
		}
		synctest.Wait()
		latest := mgr.Sessions()
		if len(latest) != 1 || latest[0].State != bfd.StateDown || latest[0].LocalDiag != bfd.DiagPathDown ||
			latest[0].ObservationVersion <= view.GetObservationVersion() || latest[0].Counters.StateTransitions != 2 ||
			rawLossDeliveryCount(t, mgr, bfd.DeliveryControlNotification, bfd.DeliveryQueueFull) != 1 || len(sub) != 0 {
			t.Fatalf("final committed owner Down was not the sole raw loss: %+v", latest)
		}
		go mgr.RunDispatch(ctx)
		synctest.Wait()
		var gapRevision uint64
		var recovered, synced bool
		for len(events) > 0 {
			event := <-events
			switch event.GetType() {
			case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP:
				gapRevision = event.GetRevision()
			case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED:
				if event.GetSession().GetLocalDiscriminator() == session.LocalDiscriminator() && gapRevision != 0 {
					recovered = event.GetSession().GetLocalState() == bfdv1.SessionState_SESSION_STATE_DOWN &&
						event.GetSession().GetObservationVersion() == latest[0].ObservationVersion
				}
			case bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE:
				synced = recovered && event.GetRevision() >= gapRevision
			default:
			}
			status := event.GetSession()
			// The consumer's observation floor rejects the old Up still queued before the loss.
			if status.GetLocalDiscriminator() == session.LocalDiscriminator() &&
				status.GetObservationVersion() > view.GetObservationVersion() {
				view = status
			}
		}
		if gapRevision <= sync.GetRevision() || !recovered || !synced ||
			view.GetLocalState() != bfdv1.SessionState_SESSION_STATE_DOWN ||
			view.GetLocalDiagnostic() != bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_PATH_DOWN ||
			rawLossDeliveryCount(t, mgr, bfd.DeliveryWatchRecipient, bfd.DeliveryGapped) != 0 {
			t.Errorf("Watch stayed stale without another owner edge/reconnect: gap=%d recovered=%t synced=%t view=%v",
				gapRevision, recovered, synced, view)
		}
		cancel()
		synctest.Wait()
		select {
		case watchErr := <-done:
			if !errors.Is(watchErr, context.Canceled) {
				t.Errorf("watchLoop returned %v, want cancellation", watchErr)
			}
		default:
			t.Error("watchLoop did not stop after cancellation")
		}
	})
}

func rawLossDeliveryCount(
	t *testing.T, mgr *bfd.Manager, source bfd.DeliverySource, outcome bfd.DeliveryOutcome,
) uint64 {
	t.Helper()
	for _, row := range mgr.DeliveryCounters() {
		if row.Source == source && row.Outcome == outcome {
			return row.Count
		}
	}
	t.Fatalf("missing delivery pair %s/%s", source, outcome)
	return 0
}
