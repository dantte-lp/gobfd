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

type watchStatusSender struct{}

func (watchStatusSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

func watchStatusConfig() bfd.SessionConfig {
	return bfd.SessionConfig{
		PeerAddr: netip.MustParseAddr("192.0.2.1"), LocalAddr: netip.MustParseAddr("192.0.2.2"),
		Interface: "eth0", Type: bfd.SessionTypeSingleHop, Role: bfd.RolePassive,
		DesiredMinTxInterval: 250 * time.Millisecond, RequiredMinRxInterval: 500 * time.Millisecond,
		DetectMultiplier: 3,
	}
}

func TestFullWatchAddsPassiveDownSessionAfterSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var session *bfd.Session
		events := make(chan *bfdv1.WatchSessionEventsResponse, 2)
		done := make(chan error, 1)
		go func() {
			s := &BFDServer{manager: mgr}
			done <- s.watchLoop(ctx, sub, true, func(event *bfdv1.WatchSessionEventsResponse) error {
				events <- event
				if event.GetType() == bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE {
					var createErr error
					session, createErr = mgr.CreateSession(ctx, watchStatusConfig(),
						bfd.NonOwningSenderLeaseFactory(watchStatusSender{}))
					return createErr
				}
				return nil
			})
		}()
		synctest.Wait()
		if sync := <-events; sync.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE {
			t.Fatalf("first event = %v, want SYNC_COMPLETE", sync)
		}
		select {
		case event := <-events:
			if event.GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED ||
				event.GetSession().GetLocalDiscriminator() != session.LocalDiscriminator() ||
				event.GetSession().GetLocalState() != bfdv1.SessionState_SESSION_STATE_DOWN ||
				event.GetSession().GetDetectMultiplier() != 3 {
				t.Fatalf("post-sync registration = %v, want full passive Down addition", event)
			}
		default:
			t.Error("passive Down registration after SYNC_COMPLETE emitted no SESSION_ADDED")
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("watchLoop: %v", err)
		}
	})
}

func TestFullWatchOwnerEventPreservesStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := watchStatusConfig()
		cfg.Role = bfd.RoleActive
		var err error
		cfg.Auth, err = bfd.NewAuthenticator(bfd.AuthTypeKeyedSHA1)
		if err != nil {
			t.Fatal(err)
		}
		cfg.AuthKeys, err = bfd.NewStaticAuthKeyStore(bfd.AuthKey{
			ID: 7, Type: bfd.AuthTypeKeyedSHA1, Secret: []byte("watch-status-test"),
		})
		if err != nil {
			t.Fatal(err)
		}
		session, err := mgr.CreateSession(t.Context(), cfg, bfd.NonOwningSenderLeaseFactory(watchStatusSender{}))
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		synctest.Sleep(time.Second)
		before := session.PacketsSent()
		if before == 0 {
			t.Fatal("active owner did not send its initial packet")
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		sub, err := mgr.SubscribeStateChanges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		go mgr.RunDispatch(ctx)
		if err := session.SetAdminDown(ctx); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		event := stateChangeToProto(<-sub)
		status := event.GetSession()
		if status.GetDesiredMinTxInterval().AsDuration() != cfg.DesiredMinTxInterval ||
			status.GetRequiredMinRxInterval().AsDuration() != cfg.RequiredMinRxInterval ||
			status.GetRemoteMinRxInterval() == nil || status.GetNegotiatedTxInterval() == nil ||
			status.GetDetectionTime() == nil || status.GetDetectMultiplier() != uint32(cfg.DetectMultiplier) ||
			status.GetAuthType() != bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_SHA1 ||
			status.GetCounters().GetPacketsSent() != before || status.GetCounters().GetStateTransitions() != 1 ||
			status.GetLastStateChange() == nil {
			t.Fatalf("owner event lost publication-time status: %v", status)
		}
	})
}

func TestFullWatchNullableLocalAddress(t *testing.T) {
	t.Parallel()
	for _, addr := range []netip.Addr{{}, netip.MustParseAddr("192.0.2.2"), netip.MustParseAddr("2001:db8::2")} {
		t.Run(addr.String(), func(t *testing.T) {
			t.Parallel()
			snap := bfd.SessionSnapshot{LocalAddr: addr}
			want := ""
			if addr.IsValid() {
				want = addr.String()
			}
			if got := snapshotToProto(snap).GetLocalAddress(); got != want {
				t.Fatalf("snapshot local address = %q, want %q", got, want)
			}
			if got := stateChangeToProto(bfd.StateChange{Status: snap}).GetSession().GetLocalAddress(); got != want {
				t.Fatalf("event local address = %q, want %q", got, want)
			}
		})
	}
}

func TestFullWatchControlUsesFrozenZeroStatus(t *testing.T) {
	t.Parallel()
	sc := bfd.StateChange{
		Status:     bfd.SessionSnapshot{LocalDiscr: 42, State: bfd.StateDown, ObservationVersion: 2},
		LocalDiscr: 99, NewState: bfd.StateUp, Diag: bfd.DiagPathDown,
		RemoteKnown: true, RemoteC: true, LocalC: true, ObservationVersion: 1,
	}
	status := stateChangeToProto(sc).GetSession()
	if status.GetLocalDiscriminator() != 42 || status.GetObservationVersion() != 2 ||
		status.GetLocalState() != bfdv1.SessionState_SESSION_STATE_DOWN ||
		status.GetLocalDiagnostic() != bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NONE || status.GetRemoteKnown() ||
		status.GetRemoteControlPlaneIndependent() || status.GetLocalControlPlaneIndependent() ||
		status.GetRemoteDiscriminator() != 0 || status.GetDesiredMinTxInterval().AsDuration() != 0 ||
		status.GetAuthType() != bfdv1.AuthenticationType_AUTHENTICATION_TYPE_NONE ||
		status.GetCounters().GetPacketsReceived() != 0 || status.GetLastStateChange() != nil ||
		status.GetLastPacketReceived() != nil {
		t.Fatalf("Control mapper merged partial fields into frozen zero status: %v", status)
	}
}

func TestFullWatchEchoUsesFrozenStatus(t *testing.T) {
	t.Parallel()
	sc := bfd.StateChange{
		Type: bfd.SessionTypeEcho, LocalDiscr: 42, PeerAddr: netip.MustParseAddr("192.0.2.1"),
		LocalAddr: netip.MustParseAddr("192.0.2.2"), Interface: "eth0", OldState: bfd.StateUp,
		NewState: bfd.StateDown, Diag: bfd.DiagEchoFailed, Timestamp: time.Now(),
		Status: bfd.SessionSnapshot{
			Type: bfd.SessionTypeEcho, LocalDiscr: 42, PeerAddr: netip.MustParseAddr("192.0.2.1"),
			LocalAddr: netip.MustParseAddr("192.0.2.2"), Interface: "eth0", State: bfd.StateDown,
			LocalDiag: bfd.DiagEchoFailed, ObservationVersion: 7, DesiredMinTx: time.Second,
			Counters: bfd.SessionCounters{PacketsReceived: 3},
		},
	}
	status := stateChangeToProto(sc).GetSession()
	if status.GetType() != bfdv1.SessionType_SESSION_TYPE_ECHO || status.GetLocalDiscriminator() != 42 ||
		status.GetPeerAddress() != "192.0.2.1" || status.GetLocalAddress() != "192.0.2.2" ||
		status.GetInterfaceName() != "eth0" || status.GetLocalState() != bfdv1.SessionState_SESSION_STATE_DOWN ||
		status.GetLocalDiagnostic() != bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_ECHO_FAILED ||
		status.GetObservationVersion() != 7 || status.GetCounters().GetPacketsReceived() != 3 ||
		status.GetDesiredMinTxInterval().AsDuration() != time.Second {
		t.Fatalf("Echo frozen status changed: %v", status)
	}
}
