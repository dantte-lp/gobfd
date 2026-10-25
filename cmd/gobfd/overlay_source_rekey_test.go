package main

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"slices"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func overlayLifecycleConfig(kind bfd.TransportScopeKind, peers ...config.VXLANPeerConfig) *config.Config {
	if kind == bfd.TransportScopeVXLAN {
		return overlayReloadConfig(peers...)
	}
	cfg := config.DefaultConfig()
	cfg.Geneve.Enabled = len(peers) != 0
	cfg.Geneve.DefaultVNI = 100
	cfg.Geneve.DefaultDesiredMinTx, cfg.Geneve.DefaultRequiredMinRx = time.Second, time.Second
	cfg.Geneve.DefaultDetectMultiplier = 3
	for _, peer := range peers {
		cfg.Geneve.Peers = append(cfg.Geneve.Peers, config.GenevePeerConfig{
			Peer: peer.Peer, Local: peer.Local, PeerMAC: peer.PeerMAC, LocalMAC: peer.LocalMAC,
			InnerPeer: "192.0.2.100", InnerLocal: "192.0.2.200",
		})
	}
	return cfg
}

func TestOverlaySourceReplacementKeepsOldOnAcquireFailure(t *testing.T) {
	t.Parallel()
	for _, kind := range []bfd.TransportScopeKind{bfd.TransportScopeVXLAN, bfd.TransportScopeGeneve} {
		name := "VXLAN"
		if kind == bfd.TransportScopeGeneve {
			name = "Geneve"
		}
		for _, unavailable := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/hard failure", true: "/pending"}[unavailable], func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					peer := config.VXLANPeerConfig{
						Peer: "192.0.2.1", Local: "198.51.100.1",
						PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:0a",
					}
					cfg := overlayLifecycleConfig(kind, peer)
					c := newReconciliationCoordinator(cfg, slog.New(slog.DiscardHandler), newDaemonHealthChecker())
					opener := newFakeOverlayOpener()
					owner, mgr := newTestOverlayOwner(t, c.readiness, opener)
					defer owner.close()
					defer mgr.Close()
					c.overlays = owner
					sf := newNthFailureDeclarativeSenderFactory(0)
					if err := c.reconcile(t.Context(), cfg, mgr, sf, nil); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					original := mgr.Sessions()
					if len(original) != 1 || c.Snapshot().Stale {
						t.Fatal("initial overlay session did not converge")
					}
					old := opener.opened(netip.MustParseAddr(peer.Local))[0]
					scopes := slices.Clone(old.current())
					peer.Local = "198.51.100.2"
					failure := error(syscall.EADDRINUSE)
					if unavailable {
						failure = syscall.EADDRNOTAVAIL
					}
					opener.fail[netip.MustParseAddr(peer.Local)] = failure
					if err := c.reconcile(t.Context(), overlayLifecycleConfig(kind, peer), mgr, sf, nil); err != nil {
						t.Fatal(err)
					}
					snapshot := c.Snapshot()
					source := sourceVXLAN
					if kind == bfd.TransportScopeGeneve {
						source = sourceGeneve
					}
					receipt := snapshot.LastReceipt.Sources[source]
					if !snapshot.Stale || snapshot.AppliedGeneration != 1 || receipt.Released != 0 || receipt.Created != 0 {
						t.Errorf("failed acquisition changed source claims: %+v", receipt)
					}
					if unavailable && (receipt.Pending != 1 || receipt.Failed != 0) ||
						!unavailable && (receipt.Pending != 0 || receipt.Failed != 1) {
						t.Errorf("acquisition failure classification changed: %+v", receipt)
					}
					if _, exists := mgr.LookupByDiscriminator(original[0].LocalDiscr); !exists || len(mgr.Sessions()) != 1 {
						t.Error("new backend failure removed the original session")
					}
					if old.isClosed() || !slices.Equal(old.current(), scopes) {
						t.Error("new backend failure closed or changed the original receive backend")
					}
					if err := sendOverlayFor(t, old, scopes[0]); err != nil {
						t.Errorf("original scoped sender was invalidated: %v", err)
					}
					if t.Failed() {
						return
					}
					delete(opener.fail, netip.MustParseAddr(peer.Local))
					if err := c.reconcile(t.Context(), overlayLifecycleConfig(kind, peer), mgr, sf, nil); err != nil {
						t.Fatal(err)
					}
					if c.Snapshot().Stale || len(mgr.Sessions()) != 1 || !old.isClosed() {
						t.Fatal("explicit retry failed to replace and retire the old overlay source")
					}
					if _, exists := mgr.LookupByDiscriminator(original[0].LocalDiscr); exists {
						t.Fatal("retry retained a retired discriminator")
					}
				})
			})
		}
	}
}

func TestOverlaySourceIncompleteAcquireKeepsWholePreviousSource(t *testing.T) {
	t.Parallel()
	for _, kind := range []bfd.TransportScopeKind{bfd.TransportScopeVXLAN, bfd.TransportScopeGeneve} {
		name := "VXLAN"
		if kind == bfd.TransportScopeGeneve {
			name = "Geneve"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				first := config.VXLANPeerConfig{
					Peer: "192.0.2.1", Local: "198.51.100.2",
					PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:0a",
				}
				removed := first
				removed.Peer, removed.PeerMAC = "192.0.2.2", "02:00:00:00:00:02"
				cfg := overlayLifecycleConfig(kind, first, removed)
				logger := slog.New(slog.DiscardHandler)
				c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
				opener := newFakeOverlayOpener()
				owner, mgr := newTestOverlayOwner(t, c.readiness, opener)
				defer owner.close()
				defer mgr.Close()
				c.overlays = owner
				sf := newNthFailureDeclarativeSenderFactory(0)
				if err := c.reconcile(t.Context(), cfg, mgr, sf, nil); err != nil {
					t.Fatal(err)
				}
				before := mgr.Sessions()
				if len(before) != 2 || c.Snapshot().Stale {
					t.Fatal("initial source did not converge")
				}
				old := opener.opened(netip.MustParseAddr(first.Local))[0]
				scopes := slices.Clone(old.current())
				available, missing := first, removed
				available.Local, missing.Local = "198.51.100.1", "198.51.100.3"
				opener.fail[netip.MustParseAddr(missing.Local)] = syscall.EADDRNOTAVAIL
				if err := c.reconcile(t.Context(), overlayLifecycleConfig(kind, available, missing), mgr, sf, nil); err != nil {
					t.Fatal(err)
				}
				source := sourceVXLAN
				if kind == bfd.TransportScopeGeneve {
					source = sourceGeneve
				}
				snapshot := c.Snapshot()
				receipt := snapshot.LastReceipt.Sources[source]
				if receipt.Pending != 1 || receipt.Failed != 0 || receipt.Released != 0 || receipt.Created != 0 {
					t.Errorf("incomplete acquire applied part of the desired source: %+v", receipt)
				}
				if len(mgr.Sessions()) != 2 || old.isClosed() || !slices.Equal(old.current(), scopes) {
					t.Error("incomplete acquire discarded the previous complete source")
				}
				for _, previous := range before {
					if _, exists := mgr.LookupByDiscriminator(previous.LocalDiscr); !exists {
						t.Error("incomplete acquire removed a previous physical session")
					}
				}
				if t.Failed() {
					return
				}
				delete(opener.fail, netip.MustParseAddr(missing.Local))
				c.retryPendingSources(t.Context(), snapshot.DesiredGeneration, allReconciliationSources,
					c.withListeners(func(
						ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate,
					) sourceApplyResult {
						return applyCompiledSource(ctx, source, candidate, mgr, sf, logger, c.listeners)
					}))
				receipt = c.Snapshot().LastReceipt.Sources[source]
				if c.Snapshot().Stale || receipt.Created != 2 || receipt.Released != 2 || !old.isClosed() {
					t.Fatalf("retry did not converge and retire the old source: %+v", receipt)
				}
				if len(opener.opened(netip.MustParseAddr(available.Local))) != 1 {
					t.Fatal("retry reopened an already tracked disjoint backend")
				}
			})
		})
	}
}

func TestOverlaySameWireReplacementRejectsBeforeAnyMutation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		peer := config.VXLANPeerConfig{
			Peer: "192.0.2.1", Local: "198.51.100.2",
			PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:0a",
		}
		cfg := overlayReloadConfig(peer)
		logger := slog.New(slog.DiscardHandler)
		c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
		opener := newFakeOverlayOpener()
		owner, mgr := newTestOverlayOwner(t, c.readiness, opener)
		defer owner.close()
		defer mgr.Close()
		c.overlays = owner
		sf := newNthFailureDeclarativeSenderFactory(0)
		if err := c.reconcile(t.Context(), cfg, mgr, sf, nil); err != nil {
			t.Fatal(err)
		}
		original := mgr.Sessions()[0]
		old := opener.opened(netip.MustParseAddr(peer.Local))[0]
		scopes := slices.Clone(old.current())
		changed, disjoint := peer, peer
		changed.LocalMAC, disjoint.Local = "02:00:00:00:00:0b", "198.51.100.1"
		candidate, err := compileControlSessionCandidate(overlayReloadConfig(disjoint, changed))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		applied := false
		result := c.claimOverlaySource(ctx, sourceVXLAN, candidate, func(
			ctx context.Context, source reconciliationSource, next compiledControlSessionCandidate,
		) sourceApplyResult {
			applied = true
			cancel()
			return applyCompiledSource(ctx, source, next, mgr, sf, logger, c.listeners)
		})
		if !errors.Is(result.Err, bfd.ErrSessionParameterConflict) || applied {
			t.Errorf("unqualified same-wire change reached apply: %+v, applied=%t", result, applied)
		}
		if len(opener.opened(netip.MustParseAddr(disjoint.Local))) != 0 || !slices.Equal(old.current(), scopes) {
			t.Error("same-wire rejection occurred after backend creation or scope mutation")
		}
		if _, exists := mgr.LookupByDiscriminator(original.LocalDiscr); !exists || old.isClosed() {
			t.Error("same-wire change removed the original live source")
		}
		if err := sendOverlayFor(t, old, scopes[0]); err != nil {
			t.Errorf("same-wire preparation invalidated the original scoped sender: %v", err)
		}
	})
}

func TestOverlaySourceSupersedesUnusedPreparations(t *testing.T) {
	t.Parallel()
	for _, kind := range []bfd.TransportScopeKind{bfd.TransportScopeVXLAN, bfd.TransportScopeGeneve} {
		name := "VXLAN"
		if kind == bfd.TransportScopeGeneve {
			name = "Geneve"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				peer := config.VXLANPeerConfig{
					Peer: "192.0.2.1", Local: "198.51.100.1",
					PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:0a",
				}
				cfg := overlayLifecycleConfig(kind, peer)
				logger := slog.New(slog.DiscardHandler)
				c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
				opener := newFakeOverlayOpener()
				owner, mgr := newTestOverlayOwner(t, c.readiness, opener)
				defer owner.close()
				defer mgr.Close()
				c.overlays = owner
				sf := newNthFailureDeclarativeSenderFactory(0)
				apply := func(peers ...config.VXLANPeerConfig) {
					t.Helper()
					if err := c.reconcile(t.Context(), overlayLifecycleConfig(kind, peers...), mgr, sf, nil); err != nil {
						t.Fatal(err)
					}
				}
				apply(peer)
				original := mgr.Sessions()[0]
				old := opener.opened(netip.MustParseAddr(peer.Local))[0]
				oldScopes := slices.Clone(old.current())
				prepared, missing := peer, peer
				prepared.Local, missing.Local = "198.51.100.2", "198.51.100.3"
				opener.fail[netip.MustParseAddr(missing.Local)] = syscall.EADDRNOTAVAIL
				apply(prepared, missing)
				obsolete := opener.opened(netip.MustParseAddr(prepared.Local))[0]
				prepared.Local = "198.51.100.4"
				apply(prepared, missing)
				if !obsolete.isClosed() {
					t.Error("superseding generation retained an unused backend")
				}
				source := sourceVXLAN
				if kind == bfd.TransportScopeGeneve {
					source = sourceGeneve
				}
				snapshot := c.Snapshot()
				c.retryPendingSources(t.Context(), snapshot.DesiredGeneration, allReconciliationSources,
					c.withListeners(func(ctx context.Context, source reconciliationSource, next compiledControlSessionCandidate,
					) sourceApplyResult {
						return applyCompiledSource(ctx, source, next, mgr, sf, logger, c.listeners)
					}))
				if len(opener.opened(netip.MustParseAddr(prepared.Local))) != 1 {
					t.Error("same-generation retry reopened its prepared backend")
				}
				unusedScopes := opener.opened(netip.MustParseAddr(prepared.Local))[0].current()
				prepared.LocalMAC = "02:00:00:00:00:0b"
				apply(prepared, missing)
				receipt := c.Snapshot().LastReceipt.Sources[source]
				if receipt.Pending != 1 || receipt.Failed != 0 || receipt.Created != 0 || receipt.Released != 0 {
					t.Errorf("unused same-wire preparation blocked supersession: %+v", receipt)
				}
				conns := opener.opened(netip.MustParseAddr(prepared.Local))
				if slices.Contains(conns[len(conns)-1].current(), unusedScopes[0]) {
					t.Error("superseding generation retained an unused exact scope")
				}
				if old.isClosed() || !slices.Equal(old.current(), oldScopes) {
					t.Error("pruning preparations changed the active backend")
				}
				if _, exists := mgr.LookupByDiscriminator(original.LocalDiscr); !exists || len(mgr.Sessions()) != 1 {
					t.Error("pruning preparations removed the active session")
				}
				if err := sendOverlayFor(t, old, oldScopes[0]); err != nil {
					t.Errorf("active sender after supersession: %v", err)
				}
			})
		})
	}
}
