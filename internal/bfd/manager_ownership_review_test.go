package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type ownershipNoopSender struct{}

func (ownershipNoopSender) SendPacket(context.Context, []byte, netip.Addr) error {
	return nil
}

type unregisterBarrierMetrics struct {
	noopMetrics

	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (m *unregisterBarrierMetrics) UnregisterSession(netip.Addr, netip.Addr, string) {
	if !m.armed.Load() {
		return
	}
	close(m.entered)
	<-m.release
}

type ownershipOperationResult struct {
	created   int
	destroyed int
	err       error
}

func TestReconcileOwnershipOperationIsAtomicWithConcurrentCreate(t *testing.T) {
	metrics := &unregisterBarrierMetrics{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	mgr := NewManager(slog.New(slog.DiscardHandler), WithManagerMetrics(metrics))
	defer mgr.Close()

	stale := ownershipTestConfig("192.0.2.1")
	if _, _, err := mgr.ReconcileSessions(context.Background(), []ReconcileConfig{
		{Key: "stale", SessionConfig: stale, Sender: ownershipNoopSender{}},
	}); err != nil {
		t.Fatalf("seed ReconcileSessions: %v", err)
	}

	desired := ownershipTestConfig("198.51.100.1")
	conflict := desired
	conflict.DetectMultiplier++
	metrics.armed.Store(true)

	reconcileResult := make(chan ownershipOperationResult, 1)
	go func() {
		created, destroyed, err := mgr.ReconcileSessions(context.Background(), []ReconcileConfig{
			{Key: "desired", SessionConfig: desired, Sender: ownershipNoopSender{}},
		})
		reconcileResult <- ownershipOperationResult{created: created, destroyed: destroyed, err: err}
	}()
	<-metrics.entered
	if mgr.ownershipMu.TryLock() {
		mgr.ownershipMu.Unlock()
		close(metrics.release)
		<-reconcileResult
		t.Fatal("reconciliation released ownership lock before completing")
	}

	createStarted := make(chan struct{})
	createResult := make(chan error, 1)
	go func() {
		close(createStarted)
		_, err := mgr.CreateSession(context.Background(), conflict, ownershipNoopSender{})
		createResult <- err
	}()
	<-createStarted
	close(metrics.release)

	reconciled := <-reconcileResult
	if reconciled.err != nil {
		t.Fatalf("ReconcileSessions: %v", reconciled.err)
	}
	if reconciled.created != 1 || reconciled.destroyed != 1 {
		t.Errorf("reconcile = (%d created, %d destroyed), want (1, 1)",
			reconciled.created, reconciled.destroyed)
	}
	if err := <-createResult; !errors.Is(err, ErrSessionParameterConflict) {
		t.Fatalf("concurrent CreateSession error = %v, want ErrSessionParameterConflict", err)
	}

	sessions := mgr.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("wire sessions = %d, want 1", len(sessions))
	}
	if sessions[0].PeerAddr != desired.PeerAddr ||
		sessions[0].DetectMultiplier != desired.DetectMultiplier {
		t.Errorf("final session = %+v, want desired config", sessions[0])
	}
}

func TestUnsolicitedClaimCleanupPreservesMatchingConfigAndReleasesQuota(t *testing.T) {
	policy := &UnsolicitedPolicy{
		Enabled:     true,
		MaxSessions: 1,
		Interfaces: map[string]UnsolicitedInterfaceConfig{
			"eth0": {
				Enabled: true,
				AllowedPrefixes: []netip.Prefix{
					netip.MustParsePrefix("192.0.2.0/24"),
				},
			},
		},
		SessionDefaults: UnsolicitedSessionDefaults{
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
		},
	}
	mgr := NewManager(
		slog.New(slog.DiscardHandler),
		WithUnsolicitedPolicy(policy),
		WithUnsolicitedSender(ownershipNoopSender{}),
	)
	defer mgr.Close()

	firstMeta := PacketMeta{
		SrcAddr: netip.MustParseAddr("192.0.2.1"),
		DstAddr: netip.MustParseAddr("192.0.2.254"),
		IfName:  "eth0",
		TTL:     255,
	}
	if err := mgr.tryCreateUnsolicited(ownershipDownPacket(101), firstMeta, nil); err != nil {
		t.Fatalf("tryCreateUnsolicited(first): %v", err)
	}
	snapshots := mgr.Sessions()
	if len(snapshots) != 1 {
		t.Fatalf("wire sessions after unsolicited create = %d, want 1", len(snapshots))
	}
	discr := snapshots[0].LocalDiscr

	config := ownershipTestConfig(firstMeta.SrcAddr.String())
	config.LocalAddr = firstMeta.DstAddr
	config.Role = RolePassive
	if _, _, err := mgr.ReconcileSessions(context.Background(), []ReconcileConfig{
		{Key: "matching-config", SessionConfig: config, Sender: ownershipNoopSender{}},
	}); err != nil {
		t.Fatalf("ReconcileSessions matching config: %v", err)
	}
	// Satisfy cleanup's Down-state precondition without a wall-clock wait; the
	// state field is atomic and the ownership behavior is the subject here.
	mgr.mu.RLock()
	mgr.sessions[discr].session.state.Store(uint32(StateDown))
	mgr.mu.RUnlock()

	mgr.cleanupUnsolicitedSession(context.Background(), discr)
	if _, ok := mgr.LookupByDiscriminator(discr); !ok {
		t.Fatal("unsolicited cleanup removed config-owned wire session")
	}
	if got := mgr.unsolicited.sessionCount.Load(); got != 0 {
		t.Errorf("unsolicited quota after claim cleanup = %d, want 0", got)
	}
	mgr.mu.RLock()
	entry := mgr.sessions[discr]
	markedUnsolicited := entry != nil && entry.unsolicited
	mgr.mu.RUnlock()
	if markedUnsolicited {
		t.Error("wire session remains marked unsolicited after unsolicited claim cleanup")
	}

	secondMeta := firstMeta
	secondMeta.SrcAddr = netip.MustParseAddr("192.0.2.2")
	if err := mgr.tryCreateUnsolicited(ownershipDownPacket(102), secondMeta, nil); err != nil {
		t.Fatalf("tryCreateUnsolicited(second) after quota release: %v", err)
	}
}

type rotatingAuthKeyStore struct {
	mu      sync.RWMutex
	current AuthKey
}

func (s *rotatingAuthKeyStore) LookupKey(id uint8) (AuthKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current.ID != id {
		return AuthKey{}, ErrAuthKeyNotFound
	}
	return s.current, nil
}

func (s *rotatingAuthKeyStore) CurrentKey() AuthKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *rotatingAuthKeyStore) rotate() {
	s.mu.Lock()
	s.current.Secret[0] ^= 0xff
	s.mu.Unlock()
}

func TestManagerRejectsUnknownRotatingAuthStoreFailClosed(t *testing.T) {
	store := &rotatingAuthKeyStore{
		current: AuthKey{
			ID:     1,
			Type:   AuthTypeSimplePassword,
			Secret: []byte("secret"),
		},
	}
	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.rotate()
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				store.rotate()
				runtime.Gosched()
			}
		}
	}()
	<-started
	defer func() {
		close(stop)
		<-done
	}()

	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	cfg := ownershipTestConfig("192.0.2.10")
	cfg.Auth = SimplePasswordAuth{}
	cfg.AuthKeys = store

	if _, err := mgr.CreateSession(context.Background(), cfg, ownershipNoopSender{}); !errors.Is(
		err, ErrAuthKeyStoreIdentityUnavailable,
	) {
		t.Fatalf("CreateSession error = %v, want ErrAuthKeyStoreIdentityUnavailable", err)
	}
}

func TestStaticAuthStoresWithEquivalentKeysShareSession(t *testing.T) {
	current := AuthKey{ID: 1, Type: AuthTypeSimplePassword, Secret: []byte("current")}
	receiveA := AuthKey{ID: 2, Type: AuthTypeSimplePassword, Secret: []byte("receive-a")}
	receiveB := AuthKey{ID: 3, Type: AuthTypeSimplePassword, Secret: []byte("receive-b")}
	storeA, err := NewStaticAuthKeyStore(current, receiveA, receiveB)
	if err != nil {
		t.Fatalf("NewStaticAuthKeyStore(A): %v", err)
	}
	storeB, err := NewStaticAuthKeyStore(current, receiveB, receiveA)
	if err != nil {
		t.Fatalf("NewStaticAuthKeyStore(B): %v", err)
	}

	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	cfgA := ownershipTestConfig("192.0.2.20")
	cfgA.Auth = SimplePasswordAuth{}
	cfgA.AuthKeys = storeA
	sess, err := mgr.CreateSession(context.Background(), cfgA, ownershipNoopSender{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cfgB := cfgA
	cfgB.AuthKeys = storeB
	created, destroyed, err := mgr.ReconcileSessions(context.Background(), []ReconcileConfig{
		{Key: "equivalent-auth", SessionConfig: cfgB, Sender: ownershipNoopSender{}},
	})
	if err != nil {
		t.Fatalf("ReconcileSessions: %v", err)
	}
	if created != 0 || destroyed != 0 {
		t.Errorf("reconcile = (%d created, %d destroyed), want shared (0, 0)", created, destroyed)
	}
	if got := mgr.Sessions(); len(got) != 1 || got[0].LocalDiscr != sess.LocalDiscriminator() {
		t.Errorf("equivalent auth stores did not share wire session: %+v", got)
	}
}

func TestStaticAuthKeyStoreAccessorsPreserveImmutableSemantics(t *testing.T) {
	current := AuthKey{ID: 1, Type: AuthTypeSimplePassword, Secret: []byte("current")}
	receive := AuthKey{ID: 2, Type: AuthTypeSimplePassword, Secret: []byte("receive")}
	store, err := NewStaticAuthKeyStore(current, receive)
	if err != nil {
		t.Fatalf("NewStaticAuthKeyStore: %v", err)
	}
	originalFingerprint := store.effectiveAuthKeyStoreFingerprint()

	current.Secret[0] = 'X'
	receive.Secret[0] = 'X'
	returnedCurrent := store.CurrentKey()
	returnedCurrent.Secret[0] = 'Y'
	returnedReceive, err := store.LookupKey(receive.ID)
	if err != nil {
		t.Fatalf("LookupKey(%d): %v", receive.ID, err)
	}
	returnedReceive.Secret[0] = 'Y'

	gotCurrent := store.CurrentKey()
	if got := string(gotCurrent.Secret); got != "current" {
		t.Errorf("CurrentKey secret after caller mutations = %q, want %q", got, "current")
	}
	gotReceive, err := store.LookupKey(receive.ID)
	if err != nil {
		t.Fatalf("LookupKey(%d) after caller mutations: %v", receive.ID, err)
	}
	if got := string(gotReceive.Secret); got != "receive" {
		t.Errorf("LookupKey secret after caller mutations = %q, want %q", got, "receive")
	}
	if got := store.effectiveAuthKeyStoreFingerprint(); got != originalFingerprint {
		t.Errorf("fingerprint after caller mutations = %x, want %x", got, originalFingerprint)
	}
}

func TestManagerMissingAuthKeyStorePreservesLegacyError(t *testing.T) {
	mgr := NewManager(slog.New(slog.DiscardHandler))
	defer mgr.Close()
	cfg := ownershipTestConfig("192.0.2.30")
	cfg.Auth = SimplePasswordAuth{}

	if _, err := mgr.CreateSession(context.Background(), cfg, ownershipNoopSender{}); !errors.Is(
		err, ErrMissingAuthKeyStore,
	) {
		t.Fatalf("CreateSession error = %v, want ErrMissingAuthKeyStore", err)
	}
}

func ownershipTestConfig(peer string) SessionConfig {
	return SessionConfig{
		PeerAddr:              netip.MustParseAddr(peer),
		LocalAddr:             netip.MustParseAddr("192.0.2.254"),
		Interface:             "eth0",
		Type:                  SessionTypeSingleHop,
		Role:                  RoleActive,
		DesiredMinTxInterval:  time.Second,
		RequiredMinRxInterval: time.Second,
		DetectMultiplier:      3,
	}
}

func ownershipDownPacket(remoteDiscr uint32) *ControlPacket {
	return &ControlPacket{
		Version:               Version,
		State:                 StateDown,
		DetectMult:            3,
		MyDiscriminator:       remoteDiscr,
		DesiredMinTxInterval:  1_000_000,
		RequiredMinRxInterval: 1_000_000,
	}
}
