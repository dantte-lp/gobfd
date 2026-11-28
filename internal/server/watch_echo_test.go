package server

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func watchEchoConfig() bfd.EchoSessionConfig {
	return bfd.EchoSessionConfig{
		PeerAddr: netip.MustParseAddr("192.0.2.61"), LocalAddr: netip.MustParseAddr("192.0.2.62"),
		Interface: "eth0", TxInterval: time.Second, DetectMultiplier: 3,
	}
}

func TestEchoWatchInitialDownSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		control, err := mgr.CreateSession(t.Context(), watchStatusConfig(),
			bfd.NonOwningSenderLeaseFactory(watchStatusSender{}))
		if err != nil {
			t.Fatal(err)
		}
		cfg := watchEchoConfig()
		discr, err := mgr.CreateEchoSession(t.Context(), cfg, watchStatusSender{})
		if err != nil {
			t.Fatal(err)
		}
		var events []*bfdv1.WatchSessionEventsResponse
		s := &BFDServer{manager: mgr}
		revision, err := s.sendSnapshot(func(event *bfdv1.WatchSessionEventsResponse) error {
			events = append(events, event)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 3 {
			t.Fatalf("initial mixed snapshot = %v, want Control and Echo rows followed by SYNC_COMPLETE", events)
		}
		var echo, base *bfdv1.BfdSession
		for _, event := range events[:2] {
			if event.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED ||
				event.GetRevision() != revision {
				t.Fatalf("snapshot row = %v, want SESSION_ADDED at revision %d", event, revision)
			}
			if event.GetSession().GetLocalDiscriminator() == discr {
				echo = event.GetSession()
			} else if event.GetSession().GetLocalDiscriminator() == control.LocalDiscriminator() {
				base = event.GetSession()
			}
		}
		if echo == nil || base == nil || echo.GetType() != bfdv1.SessionType_SESSION_TYPE_ECHO ||
			echo.GetLocalState() != bfdv1.SessionState_SESSION_STATE_DOWN ||
			echo.GetLocalDiagnostic() != bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NONE ||
			echo.GetPeerAddress() != cfg.PeerAddr.String() || echo.GetLocalAddress() != cfg.LocalAddr.String() ||
			echo.GetInterfaceName() != cfg.Interface || echo.GetDesiredMinTxInterval().AsDuration() != cfg.TxInterval ||
			echo.GetDetectMultiplier() != uint32(cfg.DetectMultiplier) ||
			echo.GetDetectionTime().AsDuration() != 3*cfg.TxInterval || echo.GetCounters() == nil ||
			echo.GetObservationVersion() <= base.GetObservationVersion() || base.GetObservationVersion() == 0 ||
			echo.GetRemoteKnown() || echo.GetRemoteDiscriminator() != 0 ||
			events[2].GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE ||
			events[2].GetRevision() != revision {
			t.Fatalf("initial Echo status or mixed boundary is incomplete: %v", events)
		}
	})
}

func TestEchoWatchAddsDownAfterSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cfg := watchEchoConfig()
		var discr uint32
		events := make(chan *bfdv1.WatchSessionEventsResponse, 2)
		done := make(chan error, 1)
		go func() {
			s := &BFDServer{manager: mgr}
			done <- s.watchLoop(ctx, sub, true, func(event *bfdv1.WatchSessionEventsResponse) error {
				events <- event
				if event.GetType() == bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE {
					var createErr error
					discr, createErr = mgr.CreateEchoSession(ctx, cfg, watchStatusSender{})
					return createErr
				}
				return nil
			})
		}()
		synctest.Wait()
		var revision uint64
		select {
		case event := <-events:
			if event.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE {
				t.Fatalf("first event = %v, want SYNC_COMPLETE", event)
			}
			revision = event.GetRevision()
		default:
			t.Fatal("Watch emitted no initial SYNC_COMPLETE")
		}
		select {
		case event := <-events:
			status := event.GetSession()
			if event.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED ||
				event.GetRevision() <= revision || status.GetLocalDiscriminator() != discr ||
				status.GetType() != bfdv1.SessionType_SESSION_TYPE_ECHO ||
				status.GetLocalState() != bfdv1.SessionState_SESSION_STATE_DOWN ||
				status.GetObservationVersion() == 0 || status.GetDesiredMinTxInterval().AsDuration() != cfg.TxInterval ||
				status.GetDetectMultiplier() != uint32(cfg.DetectMultiplier) || status.GetCounters() == nil {
				t.Errorf("post-sync Echo registration = %v, want full revisioned Down addition", event)
			}
		default:
			t.Error("Echo Down registration after SYNC_COMPLETE emitted no SESSION_ADDED without RunDispatch")
		}
		cancel()
		if watchErr := <-done; !errors.Is(watchErr, context.Canceled) {
			t.Fatalf("watchLoop: %v, want cancellation", watchErr)
		}
	})
}

func TestEchoWatchOwnerEventPreservesFrozenStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := watchEchoConfig()
		discr, err := mgr.CreateEchoSession(t.Context(), cfg, watchStatusSender{})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		synctest.Sleep(time.Second)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		go mgr.RunDispatch(ctx)
		if outcome, recvErr := mgr.DemuxEcho(discr); recvErr != nil || outcome != bfd.DeliveryQueued {
			t.Fatalf("DemuxEcho = %v, %v, want queued", outcome, recvErr)
		}
		synctest.Wait()
		var up bfd.StateChange
		select {
		case up = <-sub:
		default:
			t.Fatal("actual Echo return emitted no owner state change")
		}
		before := mgr.EchoSessions()[0]
		if before.State != bfd.StateUp || before.EchosSent == 0 || before.EchosReceived != 1 {
			t.Fatalf("Echo owner did not commit receive after TX: %+v", before)
		}
		synctest.Sleep(3 * time.Second)
		var down bfd.StateChange
		select {
		case down = <-sub:
		default:
			t.Fatal("actual Echo detection timeout emitted no owner state change")
		}
		status := stateChangeToProto(up).GetSession()
		if status.GetType() != bfdv1.SessionType_SESSION_TYPE_ECHO ||
			status.GetLocalDiscriminator() != discr || status.GetPeerAddress() != cfg.PeerAddr.String() ||
			status.GetLocalAddress() != cfg.LocalAddr.String() || status.GetInterfaceName() != cfg.Interface ||
			status.GetLocalState() != bfdv1.SessionState_SESSION_STATE_UP ||
			status.GetLocalDiagnostic() != bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NONE ||
			status.GetDesiredMinTxInterval().AsDuration() != cfg.TxInterval ||
			status.GetDetectMultiplier() != uint32(cfg.DetectMultiplier) ||
			status.GetDetectionTime().AsDuration() != before.DetectionTime ||
			status.GetCounters().GetPacketsSent() != before.EchosSent ||
			status.GetCounters().GetPacketsReceived() != before.EchosReceived ||
			status.GetCounters().GetStateTransitions() != before.StateTransitions ||
			!status.GetLastStateChange().AsTime().Equal(before.LastStateChange) ||
			!status.GetLastPacketReceived().AsTime().Equal(before.LastEchoReceived) ||
			status.GetObservationVersion() == 0 || down.ObservationVersion <= up.ObservationVersion ||
			down.NewState != bfd.StateDown || down.Diag != bfd.DiagEchoFailed ||
			down.Cause != bfd.CauseDetectionTimeout || status.GetRemoteKnown() {
			t.Fatalf("delayed owner event lost frozen Echo status: up=%v down=%+v want=%+v", status, down, before)
		}
	})
}

func TestEchoWatchGapAndReconnectUseMixedSnapshots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		if _, err := mgr.CreateSession(t.Context(), watchStatusConfig(),
			bfd.NonOwningSenderLeaseFactory(watchStatusSender{})); err != nil {
			t.Fatal(err)
		}
		cfg := watchEchoConfig()
		cfg.LocalAddr = netip.Addr{}
		discr, err := mgr.CreateEchoSession(t.Context(), cfg, watchStatusSender{})
		if err != nil {
			t.Fatal(err)
		}
		if _, recvErr := mgr.DemuxEcho(discr); recvErr != nil {
			t.Fatal(recvErr)
		}
		synctest.Wait()
		for _, reconnect := range []bool{false, true} {
			ch := make(chan bfd.StateChange, 1)
			if !reconnect {
				ch <- bfd.StateChange{Gap: true, Revision: mgr.Revision() + 1}
			}
			close(ch)
			var events []*bfdv1.WatchSessionEventsResponse
			s := &BFDServer{manager: mgr, logger: slog.New(slog.DiscardHandler)}
			if watchErr := s.watchLoop(t.Context(), ch, reconnect, func(event *bfdv1.WatchSessionEventsResponse) error {
				events = append(events, event)
				return nil
			}); watchErr != nil {
				t.Fatal(watchErr)
			}
			if !reconnect {
				if len(events) == 0 || events[0].GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP {
					t.Fatalf("missing GAP before resnapshot: %v", events)
				}
				events = events[1:]
			}
			if len(events) != 3 || events[2].GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE {
				t.Fatalf("mixed GAP/reconnect snapshot = %v", events)
			}
			var echo *bfdv1.BfdSession
			for _, event := range events[:2] {
				if event.GetRevision() != events[2].GetRevision() || event.GetSession().GetObservationVersion() == 0 {
					t.Fatalf("mixed snapshot row has a different boundary or unversioned owner: %v", event)
				}
				if event.GetSession().GetType() == bfdv1.SessionType_SESSION_TYPE_ECHO {
					echo = event.GetSession()
				}
			}
			if echo == nil || echo.GetLocalAddress() != "" || echo.GetLocalState() != bfdv1.SessionState_SESSION_STATE_UP ||
				echo.GetCounters().GetPacketsReceived() != 1 || echo.GetLastPacketReceived() == nil ||
				len(mgr.Sessions()) != 1 || len(mgr.EchoSessions()) != 1 {
				t.Fatalf("mixed resnapshot lost nullable Echo status or changed typed listings: %v", events)
			}
		}
	})
}
