package server_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// M-07: a peer address is not a session identity. Two sessions to one peer
// make a lookup by peer address fail with FAILED_PRECONDITION (gRPC
// doc/statuscodes.md: the system state precludes the operation) and name
// the discriminators to use instead.
func TestGetSessionByPeerAddressAmbiguous(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)
	discrs := make([]uint32, 0, 2)
	for _, iface := range []string{"eth0", "eth1"} {
		req := validAddRequest()
		req.InterfaceName = iface
		resp, err := client.AddSession(context.Background(), req)
		if err != nil {
			t.Fatalf("AddSession(%s): %v", iface, err)
		}
		discrs = append(discrs, resp.GetSession().GetLocalDiscriminator())
	}

	_, err := client.GetSession(context.Background(), &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_PeerAddress{PeerAddress: testPeerAddr},
	})
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("GetSession(peer) error = %v, want FailedPrecondition", err)
	}
	for _, d := range discrs {
		if !strings.Contains(connectErr.Message(), strconv.FormatUint(uint64(d), 10)) {
			t.Errorf("error %q does not name discriminator %d", connectErr.Message(), d)
		}
	}
}

// H-29: Get and List build the session from one snapshot, so Get carries
// the same fields as List.
func TestGetSessionMatchesListFields(t *testing.T) {
	t.Parallel()

	client := setupTestServer(t)
	add, err := client.AddSession(context.Background(), validAddRequest())
	if err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	get, err := client.GetSession(context.Background(), &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_LocalDiscriminator{
			LocalDiscriminator: add.GetSession().GetLocalDiscriminator(),
		},
	})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	list, err := client.ListSessions(context.Background(), &bfdv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	g, l := get.GetSession(), list.GetSessions()[0]
	if got, want := add.GetSession().GetRemoteMinRxInterval().AsDuration(),
		l.GetRemoteMinRxInterval().AsDuration(); got != want {
		t.Errorf("AddSession remote_min_rx_interval = %v, want snapshot value %v", got, want)
	}
	if g.GetRemoteMinRxInterval() == nil || l.GetRemoteMinRxInterval() == nil {
		t.Fatalf("remote_min_rx_interval unset: get %v, list %v", g.GetRemoteMinRxInterval(), l.GetRemoteMinRxInterval())
	}
	if g.GetAuthType() != l.GetAuthType() || g.GetType() != l.GetType() ||
		g.GetInterfaceName() != l.GetInterfaceName() {
		t.Fatalf("Get and List differ:\nget  %v\nlist %v", g, l)
	}
}

// M-08: the stream carries revisions, closes each snapshot with
// SYNC_COMPLETE, and answers a gap with GAP and a fresh snapshot even
// without include_current.
func TestWatchLoopResyncsAfterGap(t *testing.T) {
	t.Parallel()

	for _, includeCurrent := range []bool{true, false} {
		t.Run(strconv.FormatBool(includeCurrent), func(t *testing.T) {
			t.Parallel()

			mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
			t.Cleanup(mgr.Close)
			cfg := bfd.SessionConfig{
				PeerAddr:              netip.MustParseAddr(testPeerAddr),
				LocalAddr:             netip.MustParseAddr(testLocalAddr),
				Interface:             "eth0",
				Type:                  bfd.SessionTypeSingleHop,
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  time.Second,
				RequiredMinRxInterval: time.Second,
				DetectMultiplier:      3,
			}
			if _, err := mgr.CreateSession(context.Background(), cfg,
				bfd.NonOwningSenderLeaseFactory(noopPacketSender{})); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}

			ch := make(chan bfd.StateChange, 4)
			// Revision 0 is covered by every snapshot of this manager.
			ch <- bfd.StateChange{
				LocalDiscr: 7, PeerAddr: cfg.PeerAddr, OldState: bfd.StateAdminDown, NewState: bfd.StateDown,
			}
			ch <- bfd.StateChange{
				LocalDiscr: 7, PeerAddr: cfg.PeerAddr, OldState: bfd.StateDown, NewState: bfd.StateInit, Revision: 1,
			}
			ch <- bfd.StateChange{Gap: true, Revision: 2}
			ch <- bfd.StateChange{
				LocalDiscr: 7, PeerAddr: cfg.PeerAddr, OldState: bfd.StateInit, NewState: bfd.StateUp, Revision: 3,
			}
			close(ch)

			var got []string
			err := server.WatchLoop(context.Background(), mgr, ch, includeCurrent,
				func(r *bfdv1.WatchSessionEventsResponse) error {
					got = append(got, strings.TrimPrefix(r.GetType().String(), "EVENT_TYPE_")+
						"@"+strconv.FormatUint(r.GetRevision(), 10))
					return nil
				})
			if err != nil {
				t.Fatalf("WatchLoop: %v", err)
			}

			want := []string{"STATE_CHANGE@1", "GAP@2", "SESSION_ADDED@0", "SYNC_COMPLETE@0", "STATE_CHANGE@3"}
			if includeCurrent {
				want = append([]string{"SESSION_ADDED@0", "SYNC_COMPLETE@0"}, want...)
			}
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Fatalf("stream = %v\nwant     %v", got, want)
			}
		})
	}
}

type noopPacketSender struct{}

func (noopPacketSender) SendPacket(context.Context, []byte, netip.Addr) error { return nil }

func TestWatchLoopPublishesSessionDeletion(t *testing.T) {
	t.Parallel()
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	session, err := mgr.CreateSession(t.Context(), bfd.SessionConfig{
		PeerAddr: netip.MustParseAddr(testPeerAddr), LocalAddr: netip.MustParseAddr(testLocalAddr),
		Interface: "eth0", Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
		DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
	}, bfd.NonOwningSenderLeaseFactory(noopPacketSender{}))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sub, err := mgr.SubscribeStateChanges(ctx)
	if err != nil {
		t.Fatalf("SubscribeStateChanges: %v", err)
	}
	var got []*bfdv1.WatchSessionEventsResponse
	err = server.WatchLoop(ctx, mgr, sub, true, func(event *bfdv1.WatchSessionEventsResponse) error {
		got = append(got, event)
		if event.GetType() == bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE {
			if deleteErr := mgr.DestroySession(ctx, session.LocalDiscriminator()); deleteErr != nil {
				t.Fatalf("DestroySession: %v", deleteErr)
			}
		} else if len(got) == 3 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WatchLoop: %v, want cancellation", err)
	}
	if len(got) != 3 || got[2].GetType() != bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED ||
		got[2].GetRevision() <= got[1].GetRevision() ||
		got[2].GetSession().GetLocalDiscriminator() != session.LocalDiscriminator() ||
		got[2].GetSession().GetPeerAddress() != testPeerAddr ||
		got[2].GetSession().GetLocalAddress() != testLocalAddr ||
		got[2].GetSession().GetInterfaceName() != "eth0" {
		t.Fatalf("Watch events = %v, want snapshot, sync, revisioned deletion with session identity", got)
	}
}
