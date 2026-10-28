package main

import (
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"testing/synctest"

	"connectrpc.com/grpchealth/v2"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestReconciliationRejectsAuthenticationReloadBeforeMutation(t *testing.T) {
	t.Parallel()
	key := config.AuthConfig{Type: "meticulous_keyed_sha1", KeyID: 7, Secret: "reload-auth-test"}
	for _, tc := range []struct {
		name    string
		before  config.AuthConfig
		after   config.AuthConfig
		pending bool
	}{
		{name: "enable", after: key},
		{name: "disable", before: key},
		{
			name: "algorithm", before: key,
			after: config.AuthConfig{Type: "keyed_sha1", KeyID: 7, Secret: "reload-auth-test"},
		},
		{
			name: "key ID", before: key,
			after: config.AuthConfig{Type: key.Type, KeyID: 8, Secret: key.Secret},
		},
		{
			name: "key material", before: key,
			after: config.AuthConfig{Type: key.Type, KeyID: key.KeyID, Secret: "changed-auth-test"},
		},
		{name: "accepted absent runtime", before: key, pending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				logger := slog.New(slog.DiscardHandler)
				cfg := config.DefaultConfig()
				cfg.Sessions = []config.SessionConfig{
					{Peer: "192.0.2.90", Local: "127.0.0.1", Interface: "lo", Auth: tc.before},
					{Peer: "192.0.2.91", Local: "127.0.0.1", Interface: "lo"},
				}
				mgr := bfd.NewManager(logger)
				defer mgr.Close()
				checker := newDaemonHealthChecker()
				c := newReconciliationCoordinator(cfg, logger, checker)
				configuredCalls := 0
				c.onConfiguredPeers = func([]netip.Addr) { configuredCalls++ }
				sf := newNthFailureDeclarativeSenderFactory(0)
				if tc.pending {
					sf.failAt = 1
				}
				level := new(slog.LevelVar)
				if err := c.reconcile(t.Context(), cfg, mgr, sf, level); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				before, sessions, calls := c.Snapshot(), mgr.Sessions(), sf.calls
				closed := len(sf.closed)
				oldKeys := c.retainedCandidate.base[0].config.AuthKeys
				oldConfiguredCalls := configuredCalls
				candidate := cloneStartupContractTestConfig(cfg)
				candidate.Log.Level = "debug"
				candidate.Sessions[0].Auth = tc.after
				candidate.Sessions[0].DetectMult = 5
				candidate.Sessions[1].Peer = "192.0.2.92"
				sf.failAt = 0
				err := c.reconcile(t.Context(), candidate, mgr, sf, level)
				if !errors.Is(err, bfd.ErrAuthenticationReloadUnsupported) {
					t.Fatalf("authentication reload rejection = %v, want ErrAuthenticationReloadUnsupported", err)
				}
				if strings.Contains(err.Error(), key.Secret) || strings.Contains(err.Error(), "changed-auth-test") ||
					strings.Contains(err.Error(), cfg.Sessions[0].Peer) {
					t.Fatal("authentication reload rejection exposed key material")
				}
				synctest.Wait()
				if c.Snapshot() != before || sf.calls != calls || configuredCalls != oldConfiguredCalls ||
					level.Level() != slog.LevelInfo || c.retainedCandidate.base[0].config.AuthKeys != oldKeys ||
					len(sf.closed) != closed {
					t.Fatal("rejected authentication reload changed generation, resources or accepted configuration")
				}
				after := mgr.Sessions()
				if len(after) != len(sessions) {
					t.Fatal("rejected authentication reload changed session membership")
				}
				for _, previous := range sessions {
					session, exists := mgr.LookupByDiscriminator(previous.LocalDiscr)
					if !exists || session.DetectMultiplier() != previous.DetectMultiplier {
						t.Fatal("rejected mixed authentication reload replaced or partially updated a session")
					}
				}
				if !tc.pending {
					assertHealthStatus(t, checker, "", grpchealth.StatusServing)
				}
			})
		})
	}
}

func TestReconciliationAcceptsUnchangedAuthentication(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		logger := slog.New(slog.DiscardHandler)
		cfg := config.DefaultConfig()
		cfg.Sessions = []config.SessionConfig{{
			Peer: "192.0.2.90", Local: "127.0.0.1", Interface: "lo",
			Auth: config.AuthConfig{Type: "meticulous_keyed_sha1", KeyID: 7, Secret: "reload-auth-test"},
		}}
		mgr := bfd.NewManager(logger)
		defer mgr.Close()
		c := newReconciliationCoordinator(cfg, logger, newDaemonHealthChecker())
		sf := newNthFailureDeclarativeSenderFactory(0)
		if err := c.reconcile(t.Context(), cfg, mgr, sf, nil); err != nil {
			t.Fatal(err)
		}
		before := mgr.Sessions()[0]
		session, exists := mgr.LookupByDiscriminator(before.LocalDiscr)
		if !exists {
			t.Fatal("missing authenticated session")
		}
		if err := c.reconcile(t.Context(), cloneStartupContractTestConfig(cfg), mgr, sf, nil); err != nil {
			t.Fatal(err)
		}
		after, exists := mgr.LookupByDiscriminator(before.LocalDiscr)
		if snapshot := c.Snapshot(); !exists || after != session || sf.calls != 1 ||
			snapshot.DesiredGeneration != 2 || snapshot.AppliedGeneration != 2 || snapshot.Stale {
			t.Fatal("unchanged authentication was rejected or replaced the existing session")
		}
	})
}
