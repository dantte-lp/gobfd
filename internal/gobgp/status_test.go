package gobgp_test

import (
	"log/slog"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/gobgp"
)

func TestDampenerSnapshotDecaysWithoutChangingRoutingState(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0)
	d := gobgp.NewDampener(gobgp.DampeningConfig{
		Enabled: true, SuppressThreshold: 2, ReuseThreshold: 0.5,
		HalfLife: 15 * time.Second, MaxSuppressTime: time.Minute,
	}, slog.New(slog.DiscardHandler), gobgp.WithClock(func() time.Time { return now }))
	d.ShouldSuppress("192.0.2.1")
	d.ShouldSuppress("192.0.2.1")
	now = now.Add(15 * time.Second)
	observed := d.Snapshot("192.0.2.1")
	if math.Abs(observed.Penalty-1) > 0.001 || !observed.Suppressed ||
		observed.SuppressionDeadline != time.Unix(160, 0) {
		t.Fatalf("snapshot after half-life = %+v, want penalty 1, suppressed until 160", observed)
	}
	now = now.Add(30 * time.Second)
	observed = d.Snapshot("192.0.2.1")
	if observed.Penalty != 0 || observed.Suppressed || !observed.SuppressionDeadline.IsZero() {
		t.Fatalf("snapshot below reuse threshold = %+v, want unsuppressed zero", observed)
	}
	// Observing expired recovery suppression must not reset the live penalty:
	// a new Down still uses the existing accumulated history.
	if !d.ShouldSuppress("192.0.2.1") {
		t.Fatal("observing the snapshot changed the subsequent Down decision")
	}
	now = time.Unix(160, 0)
	if observed = d.Snapshot("192.0.2.1"); observed.Suppressed || observed.Penalty != 0 {
		t.Fatalf("snapshot at maximum bound = %+v, want cleared suppression", observed)
	}
	if observed = d.Snapshot("192.0.2.99"); observed.Penalty != 0 || observed.Suppressed {
		t.Fatalf("unknown peer = %+v, want idle", observed)
	}
}

func TestHandlerPeerStatusHeldRecoveryAndRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		h := newTestHandler(t, mock, gobgp.DampeningConfig{
			Enabled: true, SuppressThreshold: 1, ReuseThreshold: 0.5,
			HalfLife: time.Minute, MaxSuppressTime: 10 * time.Second,
		})
		events := runHandler(t, h)
		started := time.Now()
		events <- peerTransition(bfd.StateUp, bfd.StateDown)
		events <- peerTransition(bfd.StateDown, bfd.StateUp)
		synctest.Wait()
		observed := h.PeerStatus("10.0.0.1")
		if observed.Action != "up" || observed.NextAttempt != started.Add(time.Second) ||
			!observed.Suppressed || observed.SuppressionDeadline != started.Add(10*time.Second) {
			t.Fatalf("held recovery = %+v, want Up recheck in 1s and suppression bound in 10s", observed)
		}
		synctest.Sleep(11 * time.Second)
		observed = h.PeerStatus("10.0.0.1")
		if observed.Action != "" || !observed.NextAttempt.IsZero() || observed.Suppressed || observed.Penalty != 0 {
			t.Fatalf("released recovery = %+v, want idle and unsuppressed", observed)
		}
		assertMethods(t, mock, methodDisablePeer, methodEnablePeer)
	})
}

func TestHandlerPeerStatusPendingLifecycle(t *testing.T) {
	for _, finish := range []string{"retry", "admin-down", "delete", "stop"} {
		t.Run(finish, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mock := newMockClient()
				mock.setError(status.Error(codes.Unavailable, "connection refused"))
				h := newTestHandler(t, mock, gobgp.DampeningConfig{})
				events := runHandler(t, h)
				events <- peerTransition(bfd.StateUp, bfd.StateDown)
				synctest.Wait()
				observed := h.PeerStatus("10.0.0.1")
				if observed.Action != "down" || observed.NextAttempt != time.Now().Add(time.Second) {
					t.Fatalf("pending retry = %+v, want Down retry in 1s", observed)
				}
				switch finish {
				case "retry":
					mock.setError(nil)
					synctest.Sleep(2 * time.Second)
				case "admin-down":
					events <- peerTransition(bfd.StateDown, bfd.StateAdminDown)
				case "delete":
					deleted := peerTransition(bfd.StateDown, bfd.StateDown)
					deleted.Deleted = true
					events <- deleted
				case "stop":
					close(events)
				}
				synctest.Wait()
				if observed = h.PeerStatus("10.0.0.1"); observed.Action != "" || !observed.NextAttempt.IsZero() {
					t.Fatalf("status after %s = %+v, want no pending action", finish, observed)
				}
			})
		})
	}
}

func TestHandlerPeerStatusConcurrentObservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mock := newMockClient()
		h := newTestHandler(t, mock, gobgp.DefaultDampeningConfig())
		events := runHandler(t, h)
		var readers sync.WaitGroup
		readers.Go(func() {
			for range 100 {
				_ = h.PeerStatus("10.0.0.1")
				_ = h.PeerStatus("192.0.2.99")
			}
		})
		for range 100 {
			events <- peerTransition(bfd.StateUp, bfd.StateDown)
			events <- peerTransition(bfd.StateDown, bfd.StateUp)
		}
		readers.Wait()
		synctest.Wait()
	})
}
