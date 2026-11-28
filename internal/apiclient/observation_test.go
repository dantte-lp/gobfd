package apiclient

import (
	"testing"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestCausalSessionViewVersionFloor(t *testing.T) {
	t.Parallel()
	for _, kind := range []bfdv1.WatchSessionEventsResponse_EventType{
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE,
		bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED,
	} {
		for _, version := range []uint64{400, 500} {
			current := &bfdv1.BfdSession{
				LocalDiscriminator: 1, ObservationVersion: 500,
				RemoteKnown: true, RemoteState: bfdv1.SessionState_SESSION_STATE_ADMIN_DOWN,
			}
			v := sessionView{sessions: map[uint32]*bfdv1.BfdSession{1: current}, covered: 10}
			changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
				Type: kind, Revision: 11,
				Session: &bfdv1.BfdSession{LocalDiscriminator: 1, ObservationVersion: version},
			})
			if err != nil || changed || v.sessions[1] != current || v.covered != 11 {
				t.Fatalf("stale %s v%d replaced/deleted newer row: changed=%t row=%v covered=%d err=%v",
					kind, version, changed, v.sessions[1], v.covered, err)
			}
			newer := &bfdv1.BfdSession{LocalDiscriminator: 1, ObservationVersion: 501}
			changed, err = v.apply(&bfdv1.WatchSessionEventsResponse{
				Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE, Revision: 12, Session: newer,
			})
			if err != nil || !changed || v.sessions[1] != newer {
				t.Fatal("replacement version was not accepted")
			}
		}
	}
	for _, pair := range [][2]uint64{{0, 0}, {0, 500}, {500, 0}} {
		v := sessionView{sessions: map[uint32]*bfdv1.BfdSession{1: {LocalDiscriminator: 1, ObservationVersion: pair[0]}}}
		changed, err := v.apply(&bfdv1.WatchSessionEventsResponse{
			Type:     bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_DELETED,
			Revision: 1, Session: &bfdv1.BfdSession{LocalDiscriminator: 1, ObservationVersion: pair[1]},
		})
		if err != nil || !changed || len(v.sessions) != 0 {
			t.Fatalf("legacy zero version changed behavior: %v", pair)
		}
	}
}

func TestRawLossResnapshotRejectsQueuedOldUp(t *testing.T) {
	t.Parallel()
	for _, kind := range []bfdv1.SessionType{
		bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, bfdv1.SessionType_SESSION_TYPE_ECHO,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()
			up := &bfdv1.BfdSession{
				LocalDiscriminator: 42, Type: kind, ObservationVersion: 10,
				LocalState: bfdv1.SessionState_SESSION_STATE_UP,
			}
			down := &bfdv1.BfdSession{
				LocalDiscriminator: 42, Type: kind, ObservationVersion: 11,
				LocalState: bfdv1.SessionState_SESSION_STATE_DOWN,
			}
			view := sessionView{sessions: map[uint32]*bfdv1.BfdSession{42: up}, covered: 1}
			for _, event := range []*bfdv1.WatchSessionEventsResponse{
				{Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_GAP, Revision: 2},
				{Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SESSION_ADDED, Revision: 2, Session: down},
				{Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_SYNC_COMPLETE, Revision: 2},
			} {
				if _, err := view.apply(event); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := view.apply(&bfdv1.WatchSessionEventsResponse{
				Type: bfdv1.WatchSessionEventsResponse_EVENT_TYPE_STATE_CHANGE, Revision: 3, Session: up,
			})
			if err != nil || changed || view.syncing || view.sessions[42] != down || view.covered != 3 {
				t.Fatalf("old queued Up rolled back the authoritative loss snapshot: changed=%t view=%+v err=%v",
					changed, view, err)
			}
		})
	}
}
