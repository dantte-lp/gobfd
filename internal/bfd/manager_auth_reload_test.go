package bfd

import (
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func authenticationReloadConfig(t *testing.T, current AuthKey, additional ...AuthKey) SessionConfig {
	t.Helper()
	keys, err := NewStaticAuthKeyStore(current, additional...)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ownershipTestConfig("192.0.2.90")
	cfg.Auth, cfg.AuthKeys = MeticulousKeyedSHA1Auth{}, keys
	if current.Type == AuthTypeKeyedSHA1 {
		cfg.Auth = KeyedSHA1Auth{}
	}
	return cfg
}

func authenticationReloadDesired(cfg SessionConfig) []ReconcileConfig {
	return []ReconcileConfig{{Key: "private-reload-key", SessionConfig: cfg}}
}

func TestManagerAuthenticationReloadRetainedDesired(t *testing.T) {
	t.Parallel()
	key := AuthKey{ID: 7, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("test-material-a")}
	other := AuthKey{ID: 8, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("test-material-b")}
	algorithm := key
	algorithm.Type = AuthTypeKeyedSHA1
	material := key
	material.Secret = []byte("test-material-c")
	id := key
	id.ID = 9
	plain := ownershipTestConfig("192.0.2.90")
	authenticated := authenticationReloadConfig(t, key)
	fresh := authenticationReloadConfig(t, key)
	timing := fresh
	timing.DesiredMinTxInterval, timing.RequiredMinRxInterval, timing.DetectMultiplier =
		2*time.Second, 2*time.Second, 5
	mapped := authenticationReloadConfig(t, material)
	mapped.PeerAddr, mapped.LocalAddr = netip.MustParseAddr("::ffff:192.0.2.90"),
		netip.MustParseAddr("::ffff:192.0.2.254")
	newIdentity := authenticationReloadConfig(t, material)
	newIdentity.PeerAddr = netip.MustParseAddr("192.0.2.91")
	for _, tc := range []struct {
		name              string
		previous, desired []ReconcileConfig
		reject            bool
	}{
		{
			name: "unchanged fresh store", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(fresh),
		},
		{
			name: "supported timing", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(timing),
		},
		{
			name: "enable", previous: authenticationReloadDesired(plain),
			desired: authenticationReloadDesired(authenticated), reject: true,
		},
		{
			name: "disable", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(plain), reject: true,
		},
		{
			name: "algorithm", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(authenticationReloadConfig(t, algorithm)), reject: true,
		},
		{
			name: "key ID", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(authenticationReloadConfig(t, id)), reject: true,
		},
		{
			name: "key material", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(authenticationReloadConfig(t, material)), reject: true,
		},
		{
			name: "receive set", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(authenticationReloadConfig(t, key, other)), reject: true,
		},
		{
			name: "selected transmit key", previous: authenticationReloadDesired(authenticationReloadConfig(t, key, other)),
			desired: authenticationReloadDesired(authenticationReloadConfig(t, other, key)), reject: true,
		},
		{
			name: "canonical identity", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(mapped), reject: true,
		},
		{
			name: "new identity", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(newIdentity),
		},
		{name: "removed identity", previous: authenticationReloadDesired(authenticated)},
		{
			name: "invalid previous", previous: authenticationReloadDesired(SessionConfig{}),
			desired: authenticationReloadDesired(fresh), reject: true,
		},
		{
			name: "invalid desired", previous: authenticationReloadDesired(authenticated),
			desired: authenticationReloadDesired(SessionConfig{}), reject: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := NewManager(slog.New(slog.DiscardHandler))
			t.Cleanup(mgr.Close)
			accepted := 0
			err := mgr.AdmitAuthenticationReload(tc.previous, tc.desired, func() { accepted++ })
			if tc.reject {
				if !errors.Is(err, ErrAuthenticationReloadUnsupported) || accepted != 0 {
					t.Fatalf("unsupported reload: error %v, acceptance calls %d", err, accepted)
				}
				for _, private := range []string{
					"private-reload-key", "192.0.2.90", "192.0.2.254", "test-material-",
				} {
					if strings.Contains(err.Error(), private) {
						t.Fatal("authentication rejection disclosed private configuration")
					}
				}
			} else if err != nil || accepted != 1 {
				t.Fatalf("supported reload: error %v, acceptance calls %d", err, accepted)
			}
			if len(mgr.Sessions()) != 0 {
				t.Fatal("authentication admission created a runtime session")
			}
		})
	}
}

func TestManagerAuthenticationReloadChecksRuntimeIndependently(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "API owner", true: "shared owners"}[shared], func(t *testing.T) {
			t.Parallel()
			mgr := NewManager(slog.New(slog.DiscardHandler))
			t.Cleanup(mgr.Close)
			key := AuthKey{ID: 7, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("test-material-a")}
			live := authenticationReloadConfig(t, key)
			session, err := mgr.CreateSession(t.Context(), live, ownershipSenderLeaseFactory())
			if err != nil {
				t.Fatal(err)
			}
			if shared {
				if _, err = mgr.CreateSessionAs(t.Context(), live, ownershipSenderLeaseFactory(),
					ConfigReconciliationOwner()); err != nil {
					t.Fatal(err)
				}
			}
			key.Secret = []byte("test-material-b")
			desired := authenticationReloadDesired(authenticationReloadConfig(t, key))
			for _, previous := range [][]ReconcileConfig{nil, desired} {
				accepted := false
				err = mgr.AdmitAuthenticationReload(previous, desired, func() { accepted = true })
				if !errors.Is(err, ErrAuthenticationReloadUnsupported) || accepted {
					t.Fatalf("live authentication mismatch: error %v, accepted %t", err, accepted)
				}
			}
			accepted := false
			unchanged := authenticationReloadDesired(authenticationReloadConfig(t,
				AuthKey{ID: 7, Type: AuthTypeMeticulousKeyedSHA1, Secret: []byte("test-material-a")}))
			if err = mgr.AdmitAuthenticationReload(nil, unchanged, func() { accepted = true }); err != nil || !accepted {
				t.Fatalf("unchanged live authentication: error %v, accepted %t", err, accepted)
			}
			current := mgr.Sessions()
			if len(current) != 1 || current[0].Ref != session.Ref() {
				t.Fatal("authentication admission replaced a runtime session")
			}
		})
	}
}

func TestManagerAuthenticationReloadAcceptanceLockScope(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	accepted := false
	err := mgr.AdmitAuthenticationReload(nil, nil, func() {
		accepted = true
		if mgr.ownershipMu.TryLock() {
			mgr.ownershipMu.Unlock()
			t.Error("acceptance did not serialize ownership changes")
		}
		if !mgr.mu.TryLock() {
			t.Error("acceptance held the registry lock")
		} else {
			mgr.mu.Unlock()
		}
		if !mgr.mu.TryRLock() {
			t.Error("acceptance prevented registry readers")
		} else {
			mgr.mu.RUnlock()
		}
	})
	if err != nil || !accepted {
		t.Fatalf("authentication acceptance: error %v, accepted %t", err, accepted)
	}
	if !mgr.ownershipMu.TryLock() {
		t.Fatal("authentication admission retained the ownership lock")
	}
	mgr.ownershipMu.Unlock()
}

func TestManagerAuthenticationReloadUnlocksAfterAcceptancePanic(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	want := errors.New("acceptance panic")
	func() {
		defer func() {
			got, ok := recover().(error)
			if !ok || !errors.Is(got, want) {
				t.Errorf("acceptance panic = %v, want the original panic", got)
			}
		}()
		_ = mgr.AdmitAuthenticationReload(nil, nil, func() { panic(want) })
	}()
	if !mgr.ownershipMu.TryLock() {
		t.Fatal("panicking acceptance retained the ownership lock")
	}
	mgr.ownershipMu.Unlock()
}

func TestManagerAuthenticationReloadRejectsClosedManager(t *testing.T) {
	t.Parallel()
	mgr := NewManager(slog.New(slog.DiscardHandler))
	mgr.Close()
	accepted := false
	err := mgr.AdmitAuthenticationReload(nil, nil, func() { accepted = true })
	if !errors.Is(err, ErrManagerClosed) || accepted {
		t.Fatalf("closed authentication admission: error %v, accepted %t", err, accepted)
	}
}
