package apiclient

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestFullWatchRowReplacementClearsZeroStatus(t *testing.T) {
	t.Parallel()
	old := &bfdv1.BfdSession{
		LocalDiscriminator: 42, ObservationVersion: 10, RemoteKnown: true,
		RemoteState: bfdv1.SessionState_SESSION_STATE_UP, RemoteDiscriminator: 7,
		LocalControlPlaneIndependent: true, RemoteControlPlaneIndependent: true,
		AuthType:             bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_SHA1,
		DesiredMinTxInterval: durationpb.New(time.Second),
		Counters:             &bfdv1.SessionCounters{PacketsReceived: 7, PacketsDropped: 5, AuthFailures: 3},
		LastStateChange:      timestamppb.Now(), LastPacketReceived: timestamppb.Now(),
	}
	v := sessionView{sessions: map[uint32]*bfdv1.BfdSession{42: old}, covered: 1}
	zero := &bfdv1.BfdSession{
		LocalDiscriminator: 42, ObservationVersion: 11,
		LocalState:      bfdv1.SessionState_SESSION_STATE_DOWN,
		LocalDiagnostic: bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NONE,
		AuthType:        bfdv1.AuthenticationType_AUTHENTICATION_TYPE_NONE,
	}
	changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
		Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE, Revision: 2, Session: zero,
	})
	if err != nil || !changed {
		t.Fatalf("zero update: changed=%t err=%v", changed, err)
	}
	got := v.sessions[42]
	if got != zero || got.GetRemoteKnown() || got.GetRemoteDiscriminator() != 0 ||
		got.GetLocalControlPlaneIndependent() || got.GetRemoteControlPlaneIndependent() ||
		got.GetLocalDiagnostic() != bfdv1.DiagnosticCode_DIAGNOSTIC_CODE_NONE ||
		got.GetAuthType() != bfdv1.AuthenticationType_AUTHENTICATION_TYPE_NONE ||
		got.GetCounters().GetPacketsReceived() != 0 || got.GetDesiredMinTxInterval() != nil ||
		got.GetCounters().GetPacketsDropped() != 0 || got.GetCounters().GetAuthFailures() != 0 ||
		got.GetLastStateChange() != nil || got.GetLastPacketReceived() != nil {
		t.Fatalf("row retained old nonzero status: %v", got)
	}
}

func TestSessionViewGapSnapshotAndDeletion(t *testing.T) {
	t.Parallel()
	v := sessionView{sessions: make(map[uint32]*bfdv1.BfdSession), syncing: true}
	up := func(discr uint32) *bfdv1.BfdSession {
		return &bfdv1.BfdSession{
			LocalDiscriminator: discr, PeerAddress: "192.0.2.1", LocalState: bfdv1.SessionState_SESSION_STATE_UP,
		}
	}
	for i, step := range []struct {
		event            bfdv1.WatchSessionEventsResponse_EventType
		rev              uint64
		session          *bfdv1.BfdSession
		changed, syncing bool
		count            int
	}{
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, rev: 7, session: up(1), syncing: true, count: 1},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, rev: 7, session: up(2), syncing: true, count: 2},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, rev: 7, changed: true, count: 2},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE, rev: 7, session: up(3), count: 2},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED, rev: 9, session: up(1), changed: true, count: 1},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP, rev: 15, changed: true, syncing: true},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, rev: 16, session: up(4), syncing: true, count: 1},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, rev: 16, changed: true, count: 1},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP, rev: 20, changed: true, syncing: true},
		{event: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, rev: 20, changed: true},
	} {
		changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
			Type: step.event, Revision: step.rev, Session: step.session,
		})
		if err != nil || changed != step.changed || v.syncing != step.syncing || len(v.sessions) != step.count {
			t.Fatalf("step %d: changed=%v syncing=%v count=%d err=%v", i, changed, v.syncing, len(v.sessions), err)
		}
	}
}

func TestEchoWatchMixedReuseRejectsStaleStateAndDeletion(t *testing.T) {
	t.Parallel()
	for _, kinds := range [][2]bfdv1.SessionType{
		{bfdv1.SessionType_SESSION_TYPE_ECHO, bfdv1.SessionType_SESSION_TYPE_ECHO},
		{bfdv1.SessionType_SESSION_TYPE_ECHO, bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP},
		{bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, bfdv1.SessionType_SESSION_TYPE_ECHO},
	} {
		t.Run(kinds[0].String()+"-to-"+kinds[1].String(), func(t *testing.T) {
			t.Parallel()
			old := &bfdv1.BfdSession{LocalDiscriminator: 42, Type: kinds[0], ObservationVersion: 10}
			deleted := &bfdv1.BfdSession{LocalDiscriminator: 42, Type: kinds[0], ObservationVersion: 11}
			current := &bfdv1.BfdSession{
				LocalDiscriminator: 42, Type: kinds[1], ObservationVersion: 12,
				LocalState: bfdv1.SessionState_SESSION_STATE_DOWN,
			}
			v := sessionView{sessions: make(map[uint32]*bfdv1.BfdSession)}
			for i, event := range []struct {
				kind bfdv1.WatchSessionEventsResponse_EventType
				row  *bfdv1.BfdSession
			}{
				{bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, old},
				{bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED, deleted},
				{bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, current},
				{bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE, old},
				{bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED, deleted},
			} {
				changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
					Type: event.kind, Session: event.row, Revision: uint64(i + 1),
				})
				if err != nil || changed != (i < 3) || i >= 2 && v.sessions[42] != current {
					t.Fatalf("mixed reuse step %d: changed=%t row=%v err=%v", i, changed, v.sessions[42], err)
				}
			}
			companionType := bfdv1.SessionType_SESSION_TYPE_ECHO
			if current.GetType() == companionType {
				companionType = bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP
			}
			companion := &bfdv1.BfdSession{LocalDiscriminator: 43, Type: companionType, ObservationVersion: 13}
			for _, revision := range []uint64{6, 1} {
				if revision == 6 {
					if changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
						Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP, Revision: revision,
					}); err != nil || !changed || !v.syncing || len(v.sessions) != 0 {
						t.Fatalf("mixed GAP retained a usable view: changed=%t err=%v", changed, err)
					}
				} else {
					v = sessionView{sessions: make(map[uint32]*bfdv1.BfdSession), syncing: true}
				}
				for _, row := range []*bfdv1.BfdSession{current, companion} {
					if changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
						Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, Revision: revision, Session: row,
					}); err != nil || changed || !v.syncing {
						t.Fatalf("mixed snapshot became usable before sync: changed=%t err=%v", changed, err)
					}
				}
				if changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
					Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, Revision: revision,
				}); err != nil || !changed || v.syncing || len(v.sessions) != 2 || v.sessions[42] != current {
					t.Fatalf("mixed GAP/reconnect snapshot did not commit: changed=%t err=%v", changed, err)
				}
				if changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
					Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED, Revision: revision + 1, Session: deleted,
				}); err != nil || changed || v.sessions[42] != current {
					t.Fatalf("stale deletion erased mixed resnapshot: changed=%t err=%v", changed, err)
				}
			}
		})
	}
}

type unavailableWatch struct {
	bfdv1connect.BfdServiceClient

	calls atomic.Int32
}

func (c *unavailableWatch) WatchSessionEvents(
	context.Context, *bfdv1.WatchSessionEventsRequest,
) (bfdv1connect.BfdServiceWatchSessionEventsClientStream, error) {
	c.calls.Add(1)
	return bfdv1connect.BfdServiceWatchSessionEventsClientStream{}, syscall.ECONNREFUSED
}

func TestWatchSessionsBoundedBackoffAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		client := &unavailableWatch{}
		done := make(chan error, 1)
		go func() {
			done <- WatchSessions(ctx, client, func(sessions []*bfdv1.BfdSession) error {
				if sessions != nil {
					t.Error("failed stream published a usable view")
				}
				return nil
			}, slog.New(slog.DiscardHandler))
		}()
		synctest.Wait()
		if client.calls.Load() != 1 {
			t.Fatalf("initial attempts %d", client.calls.Load())
		}
		waits := []time.Duration{
			time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
		}
		for i, wait := range waits {
			synctest.Sleep(wait)
			if client.calls.Load() != int32(i+2) {
				t.Fatalf("after %v: attempts=%d, want %d", wait, client.calls.Load(), i+2)
			}
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	})
}

func TestWatchSessionsConsumerErrorIsTerminal(t *testing.T) {
	t.Parallel()
	client := &unavailableWatch{}
	err := WatchSessions(t.Context(), client,
		func([]*bfdv1.BfdSession) error { return syscall.EPIPE }, slog.New(slog.DiscardHandler))
	if !errors.Is(err, syscall.EPIPE) || client.calls.Load() != 0 {
		t.Fatalf("consumer failure = %v, stream attempts %d", err, client.calls.Load())
	}
}
