package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func managerRekeyConfig(peer string) SessionConfig {
	cfg := ownershipTestConfig(peer)
	cfg.Interface = ""
	return cfg
}

func managerRekeyDesired(cfg SessionConfig, factory SenderLeaseFactory) ReconcileConfig {
	return ReconcileConfig{SessionConfig: cfg, SenderLeaseFactory: factory}
}

func seedManagerRekey(t *testing.T, mgr *Manager, cfg SessionConfig, factory SenderLeaseFactory) *Session {
	t.Helper()
	result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(),
		[]ReconcileConfig{managerRekeyDesired(cfg, factory)})
	if result.Err() != nil || result.Created != 1 {
		t.Fatalf("seed rekey session: %+v", result)
	}
	s, exists := mgr.LookupByDiscriminator(mgr.Sessions()[0].LocalDiscr)
	if !exists {
		t.Fatal("missing seeded session")
	}
	return s
}

func requireManagerRekeyRetained(t *testing.T, mgr *Manager, old *Session) {
	t.Helper()
	current, exists := mgr.LookupByDiscriminator(old.LocalDiscriminator())
	if !exists || current != old || current.Ref() != old.Ref() || !mgr.discriminators.IsAllocated(old.localDiscr) {
		t.Fatal("rekey lost the original physical session or discriminator")
	}
}

func TestManagerRekeyUnavailableClaimRetainsEveryOldClaim(t *testing.T) {
	t.Parallel()
	for _, pending := range []bool{false, true} {
		for _, collision := range []bool{false, true} {
			t.Run(map[bool]string{false: "fatal", true: "pending"}[pending]+"/"+
				map[bool]string{false: "disjoint", true: "collision"}[collision], func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					mgr := NewManager(slog.New(slog.DiscardHandler))
					defer mgr.Close()
					cfg := managerRekeyConfig("192.0.2.81")
					old := seedManagerRekey(t, mgr, cfg, ownershipSenderLeaseFactory())
					removedCfg := managerRekeyConfig("192.0.2.82")
					removed, err := mgr.CreateSessionAs(t.Context(), removedCfg, ownershipSenderLeaseFactory(),
						ConfigReconciliationOwner())
					if err != nil {
						t.Fatal(err)
					}
					next := cfg
					if collision {
						next.Type = SessionTypeMultiHop
					} else {
						next.LocalAddr = netip.MustParseAddr("192.0.2.253")
					}
					failure := errors.New("injected rekey bind failure")
					if pending {
						failure = NewResourceUnavailableError(ResourceRef{Kind: ResourceKindAddress, ID: "192.0.2.253"})
					}
					calls := 0
					result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(),
						[]ReconcileConfig{managerRekeyDesired(next, func() (*SenderLease, error) {
							calls++
							return nil, failure
						})})
					if result.Released != 0 || result.Created != 0 || calls != 1 || !errors.Is(result.Err(), failure) {
						t.Fatalf("unavailable rekey released committed claims: %+v, factory calls %d", result, calls)
					}
					if (pending && (result.ResourcePending != 1 || result.Failed != 0)) ||
						(!pending && (result.Failed != 1 || result.ResourcePending != 0)) {
						t.Fatalf("incorrect unavailable rekey classification: %+v", result)
					}
					requireManagerRekeyRetained(t, mgr, old)
					requireManagerRekeyRetained(t, mgr, removed)
				})
			})
		}
	}
}

func TestManagerRekeyRefusesForeignOrSharedCollisionBeforeFactory(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"API", "unsolicited", "shared", "same-type scope"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				cfg := managerRekeyConfig("192.0.2.83")
				owner := APISessionOwner("rekey-test")
				if kind == "unsolicited" {
					owner = unsolicitedSessionOwner()
				}
				if kind == "same-type scope" {
					owner = ConfigReconciliationOwner()
				}
				old, err := mgr.CreateSessionAs(t.Context(), cfg, ownershipSenderLeaseFactory(), owner)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "shared" {
					if _, err := mgr.CreateSessionAs(t.Context(), cfg, ownershipSenderLeaseFactory(),
						ConfigReconciliationOwner()); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "same-type scope" {
					cfg.NetworkScope = "rekey-test-blue"
				} else {
					cfg.Type = SessionTypeMultiHop
				}
				calls := 0
				result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(),
					[]ReconcileConfig{managerRekeyDesired(cfg, func() (*SenderLease, error) {
						calls++
						return NewSenderLease(ownershipNoopSender{}, nil), nil
					})})
				if result.Err() == nil || result.Released != 0 || result.Created != 0 || calls != 0 {
					t.Fatalf("unqualified collision reached preparation: %+v, factory calls %d", result, calls)
				}
				requireManagerRekeyRetained(t, mgr, old)
			})
		})
	}
}

func TestManagerRekeyQualifiedCollisionBothDirections(t *testing.T) {
	t.Parallel()
	for _, from := range []SessionType{SessionTypeSingleHop, SessionTypeMultiHop} {
		t.Run(from.String(), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				cfg := managerRekeyConfig("192.0.2.94")
				cfg.Type = from
				old := seedManagerRekey(t, mgr, cfg, ownershipSenderLeaseFactory())
				cfg.Type = SessionTypeMultiHop
				if from == SessionTypeMultiHop {
					cfg.Type = SessionTypeSingleHop
				}
				result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(),
					[]ReconcileConfig{managerRekeyDesired(cfg, ownershipSenderLeaseFactory())})
				sessions := mgr.Sessions()
				if result.Err() != nil || result.Created != 1 || result.Released != 1 || len(sessions) != 1 ||
					sessions[0].Ref == old.Ref() || sessions[0].Type != cfg.Type ||
					mgr.discriminators.IsAllocated(old.LocalDiscriminator()) {
					t.Fatalf("qualified type replacement did not replace exactly one identity: %+v", result)
				}
			})
		})
	}
}

func TestManagerRekeyMultiplePreparationsRollbackOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		old := make([]*Session, 0, 2)
		for _, peer := range []string{"192.0.2.84", "192.0.2.85"} {
			cfg := managerRekeyConfig(peer)
			s, err := mgr.CreateSessionAs(t.Context(), cfg, ownershipSenderLeaseFactory(), ConfigReconciliationOwner())
			if err != nil {
				t.Fatal(err)
			}
			old = append(old, s)
		}
		closed, prepared := 0, 0
		retainedDuringPreparation := true
		failure := errors.New("second replacement cannot bind")
		desired := make([]ReconcileConfig, 0, 2)
		for i, peer := range []string{"192.0.2.84", "192.0.2.85"} {
			cfg := managerRekeyConfig(peer)
			cfg.Type = SessionTypeMultiHop
			desired = append(desired, managerRekeyDesired(cfg, func() (*SenderLease, error) {
				prepared++
				for _, s := range old {
					current, exists := mgr.LookupByDiscriminator(s.LocalDiscriminator())
					if !exists || current != s {
						retainedDuringPreparation = false
					}
				}
				lease := NewSenderLease(ownershipNoopSender{}, func() error { closed++; return nil })
				if i == 1 {
					return lease, failure
				}
				return lease, nil
			}))
		}
		result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if !errors.Is(result.Err(), failure) || result.Released != 0 || result.Created != 0 ||
			prepared != 2 || closed != 2 || !retainedDuringPreparation {
			t.Fatalf("replacement rollback = %+v, prepared %d, closed %d", result, prepared, closed)
		}
		for _, s := range old {
			requireManagerRekeyRetained(t, mgr, s)
		}
		mgr.discriminators.mu.Lock()
		allocated := len(mgr.discriminators.allocated)
		mgr.discriminators.mu.Unlock()
		if allocated != 2 {
			t.Fatalf("rollback retained %d discriminators, want two original sessions", allocated)
		}
		mgr.Close()
		if closed != 2 {
			t.Fatal("Manager.Close released discarded preparation more than once")
		}
	})
}

func TestManagerRekeyFatalClaimRollsBackNewDisjointClaim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		old := seedManagerRekey(t, mgr, managerRekeyConfig("192.0.2.91"), ownershipSenderLeaseFactory())
		closed := 0
		failure := errors.New("new rekey endpoint cannot bind")
		desired := []ReconcileConfig{
			managerRekeyDesired(managerRekeyConfig("192.0.2.92"), func() (*SenderLease, error) {
				return NewSenderLease(ownershipNoopSender{}, func() error { closed++; return nil }), nil
			}),
			managerRekeyDesired(managerRekeyConfig("192.0.2.93"), func() (*SenderLease, error) {
				return nil, failure
			}),
		}
		result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if !errors.Is(result.Err(), failure) || result.Created != 0 || result.Released != 0 || closed != 1 ||
			len(mgr.Sessions()) != 1 {
			t.Fatalf("fatal source claim did not roll back only its new claim: %+v, closed %d", result, closed)
		}
		requireManagerRekeyRetained(t, mgr, old)
		mgr.Close()
		if closed != 1 {
			t.Fatal("rolled-back source sender was released twice")
		}
	})
}

func TestManagerRekeyPendingPreparationRetainsAllOldIdentities(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		old := make([]*Session, 0, 2)
		desired := make([]ReconcileConfig, 0, 2)
		closed := 0
		pending := NewResourceUnavailableError(ResourceRef{Kind: ResourceKindAddress, ID: "192.0.2.254"})
		for i, peer := range []string{"192.0.2.95", "192.0.2.96"} {
			cfg := managerRekeyConfig(peer)
			s, err := mgr.CreateSessionAs(t.Context(), cfg, ownershipSenderLeaseFactory(), ConfigReconciliationOwner())
			if err != nil {
				t.Fatal(err)
			}
			old = append(old, s)
			cfg.Type = SessionTypeMultiHop
			desired = append(desired, managerRekeyDesired(cfg, func() (*SenderLease, error) {
				if i == 1 {
					return nil, pending
				}
				return NewSenderLease(ownershipNoopSender{}, func() error { closed++; return nil }), nil
			}))
		}
		result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if result.Failed != 0 || result.ResourcePending != 1 || result.Created != 0 || result.Released != 0 || closed != 1 {
			t.Fatalf("partial preparation retired a committed identity: %+v, discarded leases %d", result, closed)
		}
		for _, s := range old {
			requireManagerRekeyRetained(t, mgr, s)
		}
		mgr.discriminators.mu.Lock()
		allocated := len(mgr.discriminators.allocated)
		mgr.discriminators.mu.Unlock()
		if allocated != 2 {
			t.Fatalf("pending preparation leaked discriminator: allocated %d", allocated)
		}
	})
}

func TestManagerRekeyAdmissionRejectsUnqualifiedCollisionBeforeAcceptance(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := managerRekeyConfig("192.0.2.97")
		old, err := mgr.CreateSession(t.Context(), cfg, ownershipSenderLeaseFactory())
		if err != nil {
			t.Fatal(err)
		}
		cfg.Type = SessionTypeMultiHop
		accepted := false
		err = mgr.AdmitSessionReload(nil, []ReconcileConfig{managerRekeyDesired(cfg, ownershipSenderLeaseFactory())},
			func() { accepted = true })
		if !errors.Is(err, ErrDuplicateSession) || accepted {
			t.Fatalf("unqualified replacement reached candidate acceptance: %v, accepted %t", err, accepted)
		}
		requireManagerRekeyRetained(t, mgr, old)
	})
}

func TestManagerRekeyCanceledPreparationRetainsOld(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"collision", "disjoint"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManager(slog.New(slog.DiscardHandler))
				defer mgr.Close()
				cfg := managerRekeyConfig("192.0.2.98")
				old := seedManagerRekey(t, mgr, cfg, ownershipSenderLeaseFactory())
				oldRef := old.Ref()
				if kind == "collision" {
					cfg.Type = SessionTypeMultiHop
				} else {
					cfg.LocalAddr = netip.MustParseAddr("192.0.2.253")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				closed, prepared := 0, 0
				result := mgr.ReconcileSessionsForOwnerDetailed(ctx, ConfigReconciliationOwner(),
					[]ReconcileConfig{managerRekeyDesired(cfg, func() (*SenderLease, error) {
						prepared++
						cancel()
						return NewSenderLease(ownershipNoopSender{}, func() error { closed++; return nil }), nil
					})})
				if !errors.Is(result.Err(), context.Canceled) || result.Created != 0 || result.Released != 0 ||
					prepared != 1 || closed != 1 {
					t.Fatalf("canceled preparation committed a replacement: %+v, prepared %d, closed %d", result, prepared, closed)
				}
				requireManagerRekeyRetained(t, mgr, old)
				mgr.mu.RLock()
				entry := mgr.sessions[old.LocalDiscriminator()]
				_, owned := entry.owners[ConfigReconciliationOwner()]
				owners := len(entry.owners)
				mgr.mu.RUnlock()
				if old.Ref() != oldRef || !owned || owners != 1 || len(mgr.Sessions()) != 1 || old.State() != StateDown {
					t.Fatal("canceled preparation changed the original identity, ownership or FSM")
				}
				mgr.discriminators.mu.Lock()
				allocated := len(mgr.discriminators.allocated)
				mgr.discriminators.mu.Unlock()
				if allocated != 1 {
					t.Fatalf("canceled preparation leaked discriminator: allocated %d", allocated)
				}
				mgr.Close()
				if closed != 1 {
					t.Fatal("canceled preparation lease released more than once")
				}
			})
		})
	}
}

func TestManagerRekeyPendingRetainsDisjointClaimWithoutOldReleases(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		old := seedManagerRekey(t, mgr, managerRekeyConfig("192.0.2.86"), ownershipSenderLeaseFactory())
		next := managerRekeyConfig("192.0.2.86")
		next.Type = SessionTypeMultiHop
		pending := NewResourceUnavailableError(ResourceRef{Kind: ResourceKindAddress, ID: "192.0.2.254"})
		desired := []ReconcileConfig{
			managerRekeyDesired(managerRekeyConfig("192.0.2.87"), ownershipSenderLeaseFactory()),
			managerRekeyDesired(next, func() (*SenderLease, error) { return nil, pending }),
		}
		result := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if result.Failed != 0 || result.ResourcePending != 1 || result.Released != 0 || len(mgr.Sessions()) != 2 {
			t.Fatalf("pending replacement dropped old or valid disjoint claim: %+v", result)
		}
		requireManagerRekeyRetained(t, mgr, old)
		desired[1].SenderLeaseFactory = ownershipSenderLeaseFactory()
		retry := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if retry.Err() != nil || retry.Created != 1 || retry.Released != 1 || len(mgr.Sessions()) != 2 {
			t.Fatalf("resource-ready retry did not replace only the old identity: %+v", retry)
		}
	})
}

func TestManagerRekeySharedCreationRejectsAlreadyCanceledCaller(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := managerRekeyConfig("192.0.2.99")
		old, err := mgr.CreateSessionAs(t.Context(), cfg, ownershipSenderLeaseFactory(), APISessionOwner("original"))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := 0
		_, err = mgr.CreateSessionAs(ctx, cfg, func() (*SenderLease, error) {
			calls++
			return NewSenderLease(ownershipNoopSender{}, nil), nil
		}, APISessionOwner("canceled"))
		if !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("canceled caller acquired an existing session: %v, factory calls %d", err, calls)
		}
		requireManagerRekeyRetained(t, mgr, old)
		mgr.mu.RLock()
		owners := len(mgr.sessions[old.LocalDiscriminator()].owners)
		mgr.mu.RUnlock()
		if owners != 1 {
			t.Fatalf("canceled caller changed shared ownership: owners %d", owners)
		}
	})
}

type managerRekeyWireSender struct{ admin chan ControlPacket }

func (s managerRekeyWireSender) SendPacket(_ context.Context, wire []byte, _ netip.Addr) error {
	var packet ControlPacket
	if err := UnmarshalControlPacket(wire, &packet); err != nil {
		return err
	}
	if packet.State == StateAdminDown {
		s.admin <- packet
	}
	return nil
}

func TestManagerRekeyRejectsRetiringExactKeyBeforeFactory(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := managerRekeyConfig("192.0.2.87")
		wire := make(chan ControlPacket, 16)
		var releases atomic.Int32
		old := seedManagerRekey(t, mgr, cfg, func() (*SenderLease, error) {
			return NewSenderLease(managerRekeyWireSender{wire}, func() error { releases.Add(1); return nil }), nil
		})
		next := cfg
		next.Type = SessionTypeMultiHop
		previous := []ReconcileConfig{managerRekeyDesired(next, ownershipSenderLeaseFactory())}
		done := make(chan ReconcileResult, 1)
		go func() {
			done <- mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), previous)
		}()
		synctest.Wait()
		if len(wire) == 0 || len(done) != 0 || releases.Load() != 0 ||
			!mgr.discriminators.IsAllocated(old.LocalDiscriminator()) {
			t.Fatal("first rekey did not retain old identity for AdminDown drain")
		}
		sessions := mgr.Sessions()
		if len(sessions) != 1 || sessions[0].Ref == old.Ref() || sessions[0].Type != SessionTypeMultiHop {
			t.Fatal("qualified first rekey did not publish the new type")
		}
		current, exists := mgr.LookupByDiscriminator(sessions[0].LocalDiscr)
		if !exists {
			t.Fatal("first rekey lost its current identity")
		}
		calls := 0
		desired := []ReconcileConfig{managerRekeyDesired(cfg, func() (*SenderLease, error) {
			calls++
			return NewSenderLease(ownershipNoopSender{}, nil), nil
		})}
		accepted := false
		admissionErr := mgr.AdmitSessionReload(previous, desired, func() { accepted = true })
		if !errors.Is(admissionErr, ErrDuplicateSession) || accepted {
			t.Errorf("reverse rekey admission accepted retiring key: err=%v accepted=%t", admissionErr, accepted)
		}
		rejected := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		if !errors.Is(rejected.Err(), ErrDuplicateSession) || rejected.Created != 0 || rejected.Released != 0 || calls != 0 {
			t.Fatalf("reverse rekey reached retiring exact key: %+v, factory calls %d", rejected, calls)
		}
		requireManagerRekeyRetained(t, mgr, current)
		if len(done) != 0 || releases.Load() != 0 || !mgr.discriminators.IsAllocated(old.LocalDiscriminator()) {
			t.Fatal("rejected reverse rekey completed the old drain")
		}
		finished := <-done
		if finished.Err() != nil || finished.Created != 1 || finished.Released != 1 || releases.Load() != 1 ||
			mgr.discriminators.IsAllocated(old.LocalDiscriminator()) {
			t.Fatalf("first rekey cleanup: %+v, releases %d", finished, releases.Load())
		}
		requireManagerRekeyRetained(t, mgr, current)
		retry := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), desired)
		sessions = mgr.Sessions()
		if retry.Err() != nil || retry.Created != 1 || retry.Released != 1 || calls != 1 || len(sessions) != 1 ||
			sessions[0].Type != SessionTypeSingleHop || sessions[0].Ref == old.Ref() {
			t.Fatalf("reverse rekey after cleanup: %+v, factory calls %d", retry, calls)
		}
		mgr.Close()
		if releases.Load() != 1 {
			t.Errorf("Close released old rekey sender %d times", releases.Load())
		}
	})
}

func TestManagerRekeyDrainsOldWireOutsideLocks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		wire, released := make(chan ControlPacket, 16), make(chan struct{}, 1)
		cfg := managerRekeyConfig("192.0.2.88")
		old := seedManagerRekey(t, mgr, cfg, func() (*SenderLease, error) {
			return NewSenderLease(managerRekeyWireSender{wire}, func() error { released <- struct{}{}; return nil }), nil
		})
		cfg.Type = SessionTypeMultiHop
		done := make(chan ReconcileResult, 1)
		go func() {
			done <- mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(),
				[]ReconcileConfig{managerRekeyDesired(cfg, ownershipSenderLeaseFactory())})
		}()
		synctest.Wait()
		if len(wire) == 0 || len(released) != 0 || len(done) != 0 {
			t.Fatal("rekey did not retain old sender for actual AdminDown notification")
		}
		packet := <-wire
		if packet.MyDiscriminator != old.LocalDiscriminator() || packet.Diag != DiagAdminDown || packet.Poll {
			t.Fatalf("retired wire does not identify the old AdminDown session: %+v", packet)
		}
		if !mgr.discriminators.IsAllocated(old.LocalDiscriminator()) {
			t.Fatal("old discriminator released before drain")
		}
		if !mgr.mu.TryLock() {
			t.Fatal("drain holds Manager.mu")
		}
		mgr.mu.Unlock()
		if !mgr.ownershipMu.TryLock() {
			t.Fatal("drain holds ownershipMu")
		}
		mgr.ownershipMu.Unlock()
		if !mgr.lifecycleMu.TryLock() {
			t.Fatal("drain holds lifecycleMu")
		}
		mgr.lifecycleMu.Unlock()
		sessions := mgr.Sessions()
		if len(sessions) != 1 || sessions[0].Ref == old.Ref() || sessions[0].Type != SessionTypeMultiHop {
			t.Fatal("new physical identity was not registered before draining the old one")
		}
		synctest.Sleep(3*time.Second - time.Nanosecond)
		if len(done) != 0 || len(released) != 0 {
			t.Fatal("retiring sender released before its notification window")
		}
		synctest.Sleep(time.Nanosecond)
		result := <-done
		if result.Err() != nil || result.Created != 1 || result.Released != 1 || len(released) != 1 ||
			mgr.discriminators.IsAllocated(old.LocalDiscriminator()) {
			t.Fatalf("rekey drain did not complete and release exactly once: %+v", result)
		}
	})
}

func TestManagerRekeyIncompleteDrainKeepsNewIdentity(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := managerRekeyConfig("192.0.2.89")
		cfg.Role = RolePassive
		releases := 0
		old := seedManagerRekey(t, mgr, cfg, func() (*SenderLease, error) {
			return NewSenderLease(ownershipNoopSender{}, func() error { releases++; return nil }), nil
		})
		cfg.Type = SessionTypeMultiHop
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		result := mgr.ReconcileSessionsForOwnerDetailed(ctx, ConfigReconciliationOwner(),
			[]ReconcileConfig{managerRekeyDesired(cfg, ownershipSenderLeaseFactory())})
		if _, ok := errors.AsType[*DrainIncompleteError](result.Err()); !ok ||
			!errors.Is(result.Err(), context.DeadlineExceeded) || result.Created != 1 || result.Released != 1 {
			t.Fatalf("incomplete retirement lost typed cleanup failure or committed replacement: %+v", result)
		}
		sessions := mgr.Sessions()
		if len(sessions) != 1 || sessions[0].Ref == old.Ref() || releases != 1 ||
			mgr.discriminators.IsAllocated(old.LocalDiscriminator()) {
			t.Fatal("incomplete drain removed replacement or retained old resources")
		}
	})
}

func TestManagerRekeyOldTimerReceiptCannotConfirmReplacement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManager(slog.New(slog.DiscardHandler))
		defer mgr.Close()
		cfg := managerRekeyConfig("192.0.2.90")
		old := seedManagerRekey(t, mgr, cfg, ownershipSenderLeaseFactory())
		peer := ownershipDownPacket(700)
		peer.YourDiscriminator = old.LocalDiscriminator()
		for _, state := range []State{StateDown, StateInit} {
			peer.State = state
			if old.RecvPacket(peer) != DeliveryQueued {
				t.Fatal("fixture could not establish original session")
			}
			synctest.Wait()
		}
		cfg.DesiredMinTxInterval = 2 * time.Second
		update := managerRekeyDesired(cfg, ownershipSenderLeaseFactory())
		update.DesiredGeneration = 2
		pending := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(), []ReconcileConfig{update})
		synctest.Wait()
		if pending.Pending != 1 || len(pending.TimerUpdates) != 1 || old.State() != StateUp {
			t.Fatalf("fixture did not retain pending timer intent: %+v", pending)
		}
		cfg.Type = SessionTypeMultiHop
		replacement := mgr.ReconcileSessionsForOwnerDetailed(t.Context(), ConfigReconciliationOwner(),
			[]ReconcileConfig{managerRekeyDesired(cfg, ownershipSenderLeaseFactory())})
		if replacement.Err() != nil || replacement.Created != 1 || replacement.Released != 1 {
			t.Fatalf("replace pending session: %+v", replacement)
		}
		current, exists := mgr.LookupByDiscriminator(mgr.Sessions()[0].LocalDiscr)
		if !exists || current.Ref() == old.Ref() {
			t.Fatal("replacement reused the original physical identity")
		}
		before := current.timerUpdateSnapshot()
		peer.State, peer.Final = StateUp, true
		outcome, err := mgr.Demux(peer, PacketMeta{SrcAddr: cfg.PeerAddr, DstAddr: cfg.LocalAddr, MultiHop: true, TTL: 255})
		if !errors.Is(err, ErrDemuxNoMatch) || outcome != DeliveryUnknownSession {
			t.Fatalf("late old Final reached a replacement: %s, %v", outcome, err)
		}
		observed := mgr.ObserveTimerUpdates(pending.TimerUpdates)
		if observed.Failed != 1 || observed.Updated != 0 || observed.Pending != 0 || current.timerUpdateSnapshot() != before {
			t.Fatalf("retired timer receipt confirmed new identity: %+v", observed)
		}
	})
}
