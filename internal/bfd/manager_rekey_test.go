package bfd_test

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// rekeySeedConfig is a single-hop session without an interface, so its packet
// demultiplexing key equals that of the multihop session with the same
// addresses: a type change must swap the two, not register both.
func rekeySeedConfig() bfd.SessionConfig {
	cfg := defaultManagerConfig()
	cfg.Interface = ""
	return cfg
}

func rekeyDesired(cfg bfd.SessionConfig, factory bfd.SenderLeaseFactory) []bfd.ReconcileConfig {
	return []bfd.ReconcileConfig{{Key: "rekey", SessionConfig: cfg, SenderLeaseFactory: factory}}
}

func failingSenderLeaseFactory(err error) bfd.SenderLeaseFactory {
	return func() (*bfd.SenderLease, error) { return nil, err }
}

func seedRekeySession(t *testing.T, mgr *bfd.Manager) uint32 {
	t.Helper()
	seed := mgr.ReconcileSessionsForOwnerDetailed(context.Background(), bfd.ConfigReconciliationOwner(),
		rekeyDesired(rekeySeedConfig(), bfd.NonOwningSenderLeaseFactory(noopSender{})))
	if seed.Err() != nil || seed.Created != 1 {
		t.Fatalf("seed reconcile = %+v, err %v", seed, seed.Err())
	}
	return mgr.Sessions()[0].LocalDiscr
}

func requireOnlySession(t *testing.T, mgr *bfd.Manager, discr uint32) {
	t.Helper()
	got := mgr.Sessions()
	if len(got) != 1 || got[0].LocalDiscr != discr {
		t.Fatalf("sessions = %+v, want only the original discriminator %d", got, discr)
	}
}

// A re-keyed entry whose new session cannot be created leaves the old session
// in place: claims precede releases, as for Micro-BFD groups.
func TestReconcileRekeyFailedClaimKeepsOldSession(t *testing.T) {
	tests := map[string]func(*bfd.SessionConfig){
		"type change with the same demux key": func(cfg *bfd.SessionConfig) {
			cfg.Type = bfd.SessionTypeMultiHop
		},
		"local address change": func(cfg *bfd.SessionConfig) {
			cfg.LocalAddr = netip.MustParseAddr("192.0.2.3")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				old := seedRekeySession(t, mgr)

				next := rekeySeedConfig()
				mutate(&next)
				bindErr := errors.New("rekey test sender bind failure")
				result := mgr.ReconcileSessionsForOwnerDetailed(context.Background(),
					bfd.ConfigReconciliationOwner(), rekeyDesired(next, failingSenderLeaseFactory(bindErr)))

				if !errors.Is(result.Err(), bindErr) || result.Failed != 1 || result.Released != 0 {
					t.Fatalf("result = failed %d released %d err %v, want the failure without releases",
						result.Failed, result.Released, result.Err())
				}
				requireOnlySession(t, mgr, old)
			})
		})
	}
}

// A same-demux-key replacement whose sender waits for its local address keeps
// the old session until the retry creates the new one: neither side is ever
// absent, and the receipt reports pending instead of a release.
func TestReconcileRekeyPendingClaimKeepsCollidingOldSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		old := seedRekeySession(t, mgr)

		next := rekeySeedConfig()
		next.Type = bfd.SessionTypeMultiHop
		pending := failingSenderLeaseFactory(addressUnavailable(t, "192.0.2.2"))
		result := mgr.ReconcileSessionsForOwnerDetailed(context.Background(),
			bfd.ConfigReconciliationOwner(), rekeyDesired(next, pending))

		if result.Failed != 0 || result.ResourcePending != 1 || result.Released != 0 {
			t.Fatalf("result = failed %d pending %d released %d, want one pending and no release",
				result.Failed, result.ResourcePending, result.Released)
		}
		requireOnlySession(t, mgr, old)

		retry := mgr.ReconcileSessionsForOwnerDetailed(context.Background(),
			bfd.ConfigReconciliationOwner(), rekeyDesired(next, bfd.NonOwningSenderLeaseFactory(noopSender{})))
		if retry.Err() != nil || retry.Created != 1 || retry.Released != 1 {
			t.Fatalf("retry = %+v, err %v, want the swap", retry, retry.Err())
		}
		got := mgr.Sessions()
		if len(got) != 1 || got[0].LocalDiscr == old || got[0].Type != bfd.SessionTypeMultiHop {
			t.Fatalf("sessions after retry = %+v, want one new multihop session", got)
		}
	})
}

// A successful same-demux-key replacement swaps the two sessions in one pass.
func TestReconcileRekeyReplacesCollidingSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		old := seedRekeySession(t, mgr)

		next := rekeySeedConfig()
		next.Type = bfd.SessionTypeMultiHop
		result := mgr.ReconcileSessionsForOwnerDetailed(context.Background(),
			bfd.ConfigReconciliationOwner(), rekeyDesired(next, bfd.NonOwningSenderLeaseFactory(noopSender{})))
		if result.Err() != nil || result.Created != 1 || result.Released != 1 {
			t.Fatalf("result = %+v, err %v, want one created and one released claim", result, result.Err())
		}
		got := mgr.Sessions()
		if len(got) != 1 || got[0].LocalDiscr == old || got[0].Type != bfd.SessionTypeMultiHop {
			t.Fatalf("sessions = %+v, want one new multihop session", got)
		}
	})
}

// A demux key held by another owner is not a replacement: the claim fails
// and that owner's session stays.
func TestReconcileRekeyDoesNotReplaceSharedSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		old := seedRekeySession(t, mgr)
		if _, err := mgr.CreateSession(context.Background(), rekeySeedConfig(),
			bfd.NonOwningSenderLeaseFactory(noopSender{})); err != nil {
			t.Fatalf("CreateSession sharing the config session: %v", err)
		}

		next := rekeySeedConfig()
		next.Type = bfd.SessionTypeMultiHop
		result := mgr.ReconcileSessionsForOwnerDetailed(context.Background(),
			bfd.ConfigReconciliationOwner(), rekeyDesired(next, bfd.NonOwningSenderLeaseFactory(noopSender{})))
		if !errors.Is(result.Err(), bfd.ErrDuplicateSession) || result.Released != 0 {
			t.Fatalf("result = released %d err %v, want ErrDuplicateSession without release",
				result.Released, result.Err())
		}
		requireOnlySession(t, mgr, old)
	})
}

// Characterization: changes to same-key effective fields other than the
// config-owned Desired Min TX and Required Min RX remain conflicts that leave
// the session and its claims unchanged.
func TestReconcileSameKeyNonTimerChangesRemainConflicts(t *testing.T) {
	key := bfd.AuthKey{ID: 1, Type: bfd.AuthTypeKeyedSHA1, Secret: []byte("rekey-secret")}
	store, err := bfd.NewStaticAuthKeyStore(key)
	if err != nil {
		t.Fatalf("NewStaticAuthKeyStore: %v", err)
	}
	multihop := func() bfd.SessionConfig {
		cfg := rekeySeedConfig()
		cfg.Type = bfd.SessionTypeMultiHop
		return cfg
	}
	tests := map[string]struct {
		seed   func() bfd.SessionConfig
		mutate func(*bfd.SessionConfig)
	}{
		"detect multiplier": {rekeySeedConfig, func(cfg *bfd.SessionConfig) { cfg.DetectMultiplier++ }},
		"padded PDU size":   {rekeySeedConfig, func(cfg *bfd.SessionConfig) { cfg.PaddedPduSize = 128 }},
		"multihop max hops": {multihop, func(cfg *bfd.SessionConfig) { cfg.MultiHopMaxHops = 5 }},
		"authentication": {rekeySeedConfig, func(cfg *bfd.SessionConfig) {
			cfg.Auth, cfg.AuthKeys = bfd.KeyedSHA1Auth{}, store
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				factory := bfd.NonOwningSenderLeaseFactory(noopSender{})
				seed := mgr.ReconcileSessionsForOwnerDetailed(context.Background(),
					bfd.ConfigReconciliationOwner(), rekeyDesired(tc.seed(), factory))
				if seed.Err() != nil {
					t.Fatalf("seed: %v", seed.Err())
				}
				old := mgr.Sessions()[0].LocalDiscr

				next := tc.seed()
				tc.mutate(&next)
				result := mgr.ReconcileSessionsForOwnerDetailed(context.Background(),
					bfd.ConfigReconciliationOwner(), rekeyDesired(next, factory))
				if !errors.Is(result.Err(), bfd.ErrSessionParameterConflict) ||
					result.Created != 0 || result.Released != 0 || result.Updated != 0 {
					t.Fatalf("result = %+v, err %v, want an unapplied conflict", result, result.Err())
				}
				requireOnlySession(t, mgr, old)
			})
		})
	}
}

// A pending replacement keeps only its own old session: the pass still
// releases an unrelated entry removed from the desired set.
func TestReconcileRekeyPendingKeepsOnlyItsOldSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		factory := bfd.NonOwningSenderLeaseFactory(noopSender{})
		removed := rekeySeedConfig()
		removed.PeerAddr = netip.MustParseAddr("198.51.100.1")
		seed := mgr.ReconcileSessionsForOwnerDetailed(context.Background(), bfd.ConfigReconciliationOwner(),
			append(rekeyDesired(rekeySeedConfig(), factory),
				bfd.ReconcileConfig{Key: "removed", SessionConfig: removed, SenderLeaseFactory: factory}))
		if seed.Err() != nil || seed.Created != 2 {
			t.Fatalf("seed reconcile = %+v, err %v", seed, seed.Err())
		}
		var old uint32
		for _, snap := range mgr.Sessions() {
			if snap.PeerAddr == rekeySeedConfig().PeerAddr {
				old = snap.LocalDiscr
			}
		}

		next := rekeySeedConfig()
		next.Type = bfd.SessionTypeMultiHop
		pending := failingSenderLeaseFactory(addressUnavailable(t, "192.0.2.2"))
		result := mgr.ReconcileSessionsForOwnerDetailed(context.Background(),
			bfd.ConfigReconciliationOwner(), rekeyDesired(next, pending))

		if result.Failed != 0 || result.ResourcePending != 1 || result.Released != 1 {
			t.Fatalf("result = failed %d pending %d released %d, want one pending and one release",
				result.Failed, result.ResourcePending, result.Released)
		}
		requireOnlySession(t, mgr, old)
	})
}
