package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// fakeScopedOverlayConn blocks receives until Close, as a real socket does,
// and records identity changes.
type fakeScopedOverlayConn struct {
	mu       sync.Mutex
	scopes   [][]bfd.TransportScope
	closed   chan struct{}
	closeOne sync.Once
}

func newFakeScopedOverlayConn(scopes []bfd.TransportScope) *fakeScopedOverlayConn {
	return &fakeScopedOverlayConn{scopes: [][]bfd.TransportScope{slices.Clone(scopes)}, closed: make(chan struct{})}
}

func (*fakeScopedOverlayConn) SendEncapsulated(context.Context, []byte, netip.Addr) error { return nil }

func (c *fakeScopedOverlayConn) SendEncapsulatedFor(_ context.Context, _ []byte, scope bfd.TransportScope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isClosed() || !slices.Contains(c.scopes[len(c.scopes)-1], scope) {
		return netio.ErrOverlayIdentityMismatch
	}
	return nil
}

func (c *fakeScopedOverlayConn) RecvDecapsulated(context.Context) ([]byte, netio.OverlayMeta, error) {
	<-c.closed
	return nil, netio.OverlayMeta{}, netio.ErrOverlayRecvClosed
}

func (c *fakeScopedOverlayConn) Close() error {
	c.closeOne.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeScopedOverlayConn) SetScopes(scopes []bfd.TransportScope) error {
	// Mirror netio: one wire identity may appear once per socket.
	for i := range scopes {
		for j := range i {
			if netio.SameOverlayWireIdentity(scopes[i], scopes[j]) {
				return netio.ErrOverlayIdentityMismatch
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scopes = append(c.scopes, slices.Clone(scopes))
	return nil
}

func (c *fakeScopedOverlayConn) current() []bfd.TransportScope {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scopes[len(c.scopes)-1]
}

func (c *fakeScopedOverlayConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

type fakeOverlayOpener struct {
	mu    sync.Mutex
	conns map[netip.Addr][]*fakeScopedOverlayConn
	fail  map[netip.Addr]error
}

func newFakeOverlayOpener() *fakeOverlayOpener {
	return &fakeOverlayOpener{
		conns: make(map[netip.Addr][]*fakeScopedOverlayConn), fail: make(map[netip.Addr]error),
	}
}

func (f *fakeOverlayOpener) open(
	_ bfd.TransportScopeKind, local netip.Addr, scopes []bfd.TransportScope,
) (scopedOverlayConn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[local]; err != nil {
		return nil, err
	}
	conn := newFakeScopedOverlayConn(scopes)
	f.conns[local] = append(f.conns[local], conn)
	return conn, nil
}

func (f *fakeOverlayOpener) opened(local netip.Addr) []*fakeScopedOverlayConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.conns[local])
}

func newTestOverlayOwner(
	t *testing.T, readiness *daemonReadiness, opener *fakeOverlayOpener,
) (*overlayOwner, *bfd.Manager) {
	t.Helper()
	mgr := bfd.NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	owner := newOverlayOwner(config.DefaultConfig(), newUDPSenderFactory(0), mgr, readiness,
		slog.New(slog.DiscardHandler))
	owner.open = opener.open
	t.Cleanup(owner.close)
	return owner, mgr
}

func testVXLANScope(local string, peer byte) bfd.TransportScope {
	return bfd.TransportScope{
		Kind: bfd.TransportScopeVXLAN, Owner: bfd.VXLANReconciliationOwner().ID, Backend: "userspace-udp",
		OuterLocalAddr: netip.MustParseAddr(local), OuterPeerAddr: netip.AddrFrom4([4]byte{192, 0, 2, peer}),
		AddressFamily: bfd.AddressFamilyIPv4, PeerMAC: [6]byte{0x02, 0, 0, 0, 0, peer},
	}
}

func TestOverlayOwnerWidensInPlaceAndClosesEmptyBackends(t *testing.T) {
	t.Parallel()

	coordinator, checker := newConvergedReadinessCoordinator(t)
	opener := newFakeOverlayOpener()
	owner, _ := newTestOverlayOwner(t, coordinator.readiness, opener)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- owner.run(ctx) }()

	first, second := testVXLANScope("198.51.100.1", 1), testVXLANScope("198.51.100.1", 2)
	other := testVXLANScope("198.51.100.2", 3)
	kind := bfd.TransportScopeVXLAN
	if _, err := acquireAll(owner, kind, []bfd.TransportScope{first}); err != nil {
		t.Fatal(err)
	}
	conns, err := acquireAll(owner, kind, []bfd.TransportScope{first, second, other})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := opener.opened(first.OuterLocalAddr)
	if len(endpoint) != 1 || conns[first] != conns[second] || conns[first] == conns[other] {
		t.Fatalf("endpoint sockets = %d; want one shared socket per local address", len(endpoint))
	}
	if got := endpoint[0].current(); !slices.Equal(got, []bfd.TransportScope{first, second}) {
		t.Fatalf("widened identities = %+v, want first and second", got)
	}

	owner.release(kind, []bfd.TransportScope{second})
	if got := endpoint[0].current(); !slices.Equal(got, []bfd.TransportScope{second}) {
		t.Fatalf("narrowed identities = %+v, want second", got)
	}
	if endpoint[0].isClosed() || !opener.opened(other.OuterLocalAddr)[0].isClosed() {
		t.Fatal("release must keep the claimed endpoint and close the unclaimed one")
	}
	owner.release(kind, nil)
	if !endpoint[0].isClosed() {
		t.Fatal("empty desired set kept a backend")
	}

	assertReadiness(t, checker, readinessAllServing())
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("owner run = %v, want nil after deliberate closes", err)
		}
	case <-time.After(readinessTestWait):
		t.Fatal("owner run did not stop")
	}
}

func TestOverlayOwnerBackendLossFailStops(t *testing.T) {
	t.Parallel()

	coordinator, _ := newConvergedReadinessCoordinator(t)
	opener := newFakeOverlayOpener()
	owner, _ := newTestOverlayOwner(t, coordinator.readiness, opener)
	scope := testVXLANScope("198.51.100.1", 1)
	if _, err := acquireAll(owner, bfd.TransportScopeVXLAN, []bfd.TransportScope{scope}); err != nil {
		t.Fatal(err)
	}
	if err := opener.opened(scope.OuterLocalAddr)[0].Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-func() chan error {
		result := make(chan error, 1)
		go func() { result <- owner.run(t.Context()) }()
		return result
	}():
		if !errors.Is(err, errRequiredWorkerStopped) || !errors.Is(err, netio.ErrOverlayRecvClosed) {
			t.Fatalf("backend loss = %v, want %v and %v", err, errRequiredWorkerStopped, netio.ErrOverlayRecvClosed)
		}
	case <-time.After(readinessTestWait):
		t.Fatal("backend loss did not fail the owner")
	}
}

// A backend whose local address is not assigned yet leaves its source
// pending, unapplied, for the address-event retry; another error fails it.
func TestOverlayBackendOpenFailureClassification(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                    string
		err                     error
		wantPending, wantFailed int
	}{
		{"unassigned address", fmt.Errorf("listen overlay UDP: %w", syscall.EADDRNOTAVAIL), 1, 0},
		{"address in use", fmt.Errorf("listen overlay UDP: %w", syscall.EADDRINUSE), 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coordinator, _ := newConvergedReadinessCoordinator(t)
			opener := newFakeOverlayOpener()
			scope := testVXLANScope("198.51.100.1", 1)
			opener.fail[scope.OuterLocalAddr] = tc.err
			owner, _ := newTestOverlayOwner(t, coordinator.readiness, opener)
			coordinator.overlays = owner
			var candidate compiledControlSessionCandidate
			candidate.overlays[0] = compiledOverlayCandidate{
				owner:   bfd.VXLANReconciliationOwner(),
				desired: []bfd.ReconcileConfig{{Key: "vxlan", SessionConfig: bfd.SessionConfig{TransportScope: scope}}},
			}
			appliedEntries := -1
			result := coordinator.withListeners(func(
				_ context.Context, _ reconciliationSource, applied compiledControlSessionCandidate,
			) sourceApplyResult {
				appliedEntries = len(applied.overlays[0].desired)
				return sourceApplyResult{}
			})(t.Context(), sourceVXLAN, candidate)
			if appliedEntries != -1 || result.Pending != tc.wantPending || result.Failed != tc.wantFailed {
				t.Fatalf("applied entries=%d result=%+v, want no source apply, pending=%d failed=%d",
					appliedEntries, result, tc.wantPending, tc.wantFailed)
			}
		})
	}
}

func overlayReloadConfig(peers ...config.VXLANPeerConfig) *config.Config {
	cfg := config.DefaultConfig()
	cfg.VXLAN.Enabled = len(peers) != 0
	cfg.VXLAN.DefaultDesiredMinTx = time.Second
	cfg.VXLAN.DefaultRequiredMinRx = time.Second
	cfg.VXLAN.DefaultDetectMultiplier = 3
	cfg.VXLAN.Peers = peers
	return cfg
}

// SIGHUP adds and removes overlay identities and endpoints without restart:
// a new identity on an open endpoint reuses its socket, and removing the
// last identity of an endpoint closes it.
func TestReloadOverlayIdentitiesConvergeWithoutRestart(t *testing.T) {
	t.Parallel()

	first := config.VXLANPeerConfig{
		Peer: "192.0.2.1", Local: "198.51.100.1", PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:0a",
	}
	second := config.VXLANPeerConfig{
		Peer: "192.0.2.2", Local: "198.51.100.1", PeerMAC: "02:00:00:00:00:02", LocalMAC: "02:00:00:00:00:0a",
	}
	moved := config.VXLANPeerConfig{
		Peer: "192.0.2.3", Local: "198.51.100.2", PeerMAC: "02:00:00:00:00:03", LocalMAC: "02:00:00:00:00:0a",
	}
	startup := overlayReloadConfig(first)
	coordinator := newReconciliationCoordinator(startup, slog.New(slog.DiscardHandler), newDaemonHealthChecker())
	opener := newFakeOverlayOpener()
	owner, mgr := newTestOverlayOwner(t, coordinator.readiness, opener)
	coordinator.overlays = owner
	sf := newNthFailureDeclarativeSenderFactory(0)
	reconcile := func(cfg *config.Config, wantSessions int) {
		t.Helper()
		if err := coordinator.reconcile(t.Context(), cfg, mgr, sf, nil); err != nil {
			t.Fatal(err)
		}
		if snapshot := coordinator.Snapshot(); snapshot.Stale {
			t.Fatalf("snapshot = %+v, want converged", snapshot)
		}
		if got := len(mgr.Sessions()); got != wantSessions {
			t.Fatalf("sessions = %d, want %d", got, wantSessions)
		}
	}
	endpoint := netip.MustParseAddr("198.51.100.1")

	reconcile(startup, 1)
	reconcile(overlayReloadConfig(first, second), 2)
	if got := len(opener.opened(endpoint)); got != 1 {
		t.Fatalf("endpoint sockets = %d, want 1 (identity added in place)", got)
	}
	reconcile(overlayReloadConfig(second, moved), 2)
	if conns := opener.opened(endpoint); len(conns) != 1 || conns[0].isClosed() || len(conns[0].current()) != 1 {
		t.Fatal("endpoint must keep one socket narrowed to the remaining identity")
	}
	if got := len(opener.opened(netip.MustParseAddr("198.51.100.2"))); got != 1 {
		t.Fatalf("new endpoint sockets = %d, want 1", got)
	}
	reconcile(overlayReloadConfig(), 0)
	for _, local := range []string{"198.51.100.1", "198.51.100.2"} {
		if conns := opener.opened(netip.MustParseAddr(local)); !conns[len(conns)-1].isClosed() {
			t.Fatalf("backend %s survived the empty overlay source", local)
		}
	}
}

// The production backend chain widens and narrows a real socket in place:
// openOverlayBackend, the leased conn and the netio SetScopes.
func TestOverlayOwnerRealBackendsWidenInPlace(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	coordinator, _ := newConvergedReadinessCoordinator(t)
	owner := newOverlayOwner(config.DefaultConfig(), newUDPSenderFactory(0), mgr, coordinator.readiness, logger)
	t.Cleanup(owner.close)
	for index, kind := range []bfd.TransportScopeKind{bfd.TransportScopeVXLAN, bfd.TransportScopeGeneve} {
		local := netip.AddrFrom4([4]byte{127, 0, 57, byte(index + 1)})
		first := bfd.TransportScope{
			Kind: kind, Owner: "overlay-test", Backend: "userspace-udp", VNI: 100,
			OuterPeerAddr: netip.AddrFrom4([4]byte{127, 0, 57, 100}), OuterLocalAddr: local,
			InnerPeerAddr: netip.MustParseAddr("192.0.2.1"), InnerLocalAddr: netip.MustParseAddr("192.0.2.2"),
			AddressFamily: bfd.AddressFamilyIPv4,
			PeerMAC:       [6]byte{0x02, 0, 0, 0, 0, 1}, LocalMAC: [6]byte{0x02, 0, 0, 0, 0, 2},
		}
		second := first
		second.OuterPeerAddr = netip.AddrFrom4([4]byte{127, 0, 57, 101})
		second.PeerMAC[5] = 3
		opened, err := acquireAll(owner, kind, []bfd.TransportScope{first})
		if err != nil {
			t.Fatalf("kind %d: open real backend: %v", kind, err)
		}
		widened, err := acquireAll(owner, kind, []bfd.TransportScope{first, second})
		if err != nil {
			t.Fatalf("kind %d: widen real backend: %v", kind, err)
		}
		if widened[first] != opened[first] || widened[second] != opened[first] {
			t.Fatalf("kind %d: widening replaced the socket", kind)
		}
		if err := sendOverlayFor(t, widened[second], second); err != nil {
			t.Fatalf("kind %d: send for widened identity: %v", kind, err)
		}
		owner.release(kind, []bfd.TransportScope{second})
		if err := sendOverlayFor(t, widened[first], first); err == nil {
			t.Fatalf("kind %d: narrowed socket still sends for the removed identity", kind)
		}
		owner.release(kind, nil)
		if err := sendOverlayFor(t, widened[second], second); err == nil {
			t.Fatalf("kind %d: closed backend still sends", kind)
		}
	}
}

func sendOverlayFor(t *testing.T, conn netio.OverlayConn, scope bfd.TransportScope) error {
	t.Helper()
	return netio.NewOverlaySender(conn, scope).SendPacket(t.Context(), make([]byte, 24), scope.InnerPeerAddr)
}

// Unequal scopes sharing a wire identity cannot coexist during session drain.
func TestOverlayOwnerRejectsSameWireReplacement(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	coordinator, _ := newConvergedReadinessCoordinator(t)
	owner := newOverlayOwner(config.DefaultConfig(), newUDPSenderFactory(0), mgr, coordinator.readiness, logger)
	t.Cleanup(owner.close)
	old := bfd.TransportScope{
		Kind: bfd.TransportScopeVXLAN, Owner: "overlay-test", Backend: "userspace-udp", VNI: 100,
		OuterPeerAddr:  netip.AddrFrom4([4]byte{127, 0, 58, 100}),
		OuterLocalAddr: netip.AddrFrom4([4]byte{127, 0, 58, 1}),
		InnerPeerAddr:  netip.MustParseAddr("192.0.2.1"), InnerLocalAddr: netip.MustParseAddr("192.0.2.2"),
		AddressFamily: bfd.AddressFamilyIPv4,
		PeerMAC:       [6]byte{0x02, 0, 0, 0, 0, 1}, LocalMAC: [6]byte{0x02, 0, 0, 0, 0, 2},
	}
	changed := old
	changed.LocalMAC[5] = 9
	opened, err := acquireAll(owner, bfd.TransportScopeVXLAN, []bfd.TransportScope{old})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := owner.senderLeaseFactory(opened[old], old)()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := lease.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	_, err = acquireAll(owner, bfd.TransportScopeVXLAN, []bfd.TransportScope{changed})
	if !errors.Is(err, bfd.ErrSessionParameterConflict) {
		t.Fatalf("claim with only a new local MAC = %v, want parameter conflict", err)
	}
	if err := sendOverlayFor(t, opened[old], old); err != nil {
		t.Fatalf("send for the retained identity: %v", err)
	}
	if err := sendOverlayFor(t, opened[old], changed); !errors.Is(err, netio.ErrOverlayIdentityMismatch) {
		t.Fatalf("send for rejected identity = %v, want identity mismatch", err)
	}
}

func TestReloadOverlayLocalMACChangeRetainsPreviousSource(t *testing.T) {
	t.Parallel()

	peer := config.VXLANPeerConfig{
		Peer: "192.0.2.1", Local: "198.51.100.1", PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:0a",
	}
	startup := overlayReloadConfig(peer)
	coordinator := newReconciliationCoordinator(startup, slog.New(slog.DiscardHandler), newDaemonHealthChecker())
	opener := newFakeOverlayOpener()
	owner, mgr := newTestOverlayOwner(t, coordinator.readiness, opener)
	coordinator.overlays = owner
	sf := newNthFailureDeclarativeSenderFactory(0)
	if err := coordinator.reconcile(t.Context(), startup, mgr, sf, nil); err != nil {
		t.Fatal(err)
	}
	original := mgr.Sessions()[0]
	backend := opener.opened(netip.MustParseAddr(peer.Local))[0]
	scopes := slices.Clone(backend.current())
	peer.LocalMAC = "02:00:00:00:00:0b"
	if err := coordinator.reconcile(t.Context(), overlayReloadConfig(peer), mgr, sf, nil); err != nil {
		t.Fatal(err)
	}
	snapshot := coordinator.Snapshot()
	receipt := snapshot.LastReceipt.Sources[sourceVXLAN]
	if !snapshot.Stale || snapshot.AppliedGeneration != 1 || receipt.Failed != 1 ||
		receipt.Errors[bfd.ReconcileErrorConflict] != 1 || receipt.Created != 0 || receipt.Released != 0 {
		t.Fatalf("same-wire rejection receipt = %+v, snapshot = %+v", receipt, snapshot)
	}
	if _, exists := mgr.LookupByDiscriminator(original.LocalDiscr); !exists ||
		backend.isClosed() || !slices.Equal(backend.current(), scopes) {
		t.Fatal("same-wire rejection changed the previous session or backend")
	}
	if err := sendOverlayFor(t, backend, scopes[0]); err != nil {
		t.Fatalf("retained scoped send = %v", err)
	}
}

// One unassigned endpoint postpones the source apply and retains all old claims.
func TestReloadOverlayKeepsPreviousSourceWhileEndpointPending(t *testing.T) {
	t.Parallel()

	available := config.VXLANPeerConfig{
		Peer: "192.0.2.1", Local: "198.51.100.1", PeerMAC: "02:00:00:00:00:01", LocalMAC: "02:00:00:00:00:0a",
	}
	removed := config.VXLANPeerConfig{
		Peer: "192.0.2.3", Local: "198.51.100.1", PeerMAC: "02:00:00:00:00:03", LocalMAC: "02:00:00:00:00:0a",
	}
	unassigned := config.VXLANPeerConfig{
		Peer: "192.0.2.2", Local: "198.51.100.2", PeerMAC: "02:00:00:00:00:02", LocalMAC: "02:00:00:00:00:0a",
	}
	startup := overlayReloadConfig(available, removed)
	coordinator := newReconciliationCoordinator(startup, slog.New(slog.DiscardHandler), newDaemonHealthChecker())
	opener := newFakeOverlayOpener()
	opener.fail[netip.MustParseAddr(unassigned.Local)] = fmt.Errorf("listen overlay UDP: %w", syscall.EADDRNOTAVAIL)
	owner, mgr := newTestOverlayOwner(t, coordinator.readiness, opener)
	coordinator.overlays = owner
	sf := newNthFailureDeclarativeSenderFactory(0)
	if err := coordinator.reconcile(t.Context(), startup, mgr, sf, nil); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.reconcile(t.Context(), overlayReloadConfig(available, unassigned), mgr, sf, nil); err != nil {
		t.Fatal(err)
	}
	snapshot := coordinator.Snapshot()
	vxlan := snapshot.LastReceipt.Sources[sourceVXLAN]
	if vxlan.Pending != 1 || vxlan.Failed != 0 || vxlan.Released != 0 || !snapshot.Stale {
		t.Fatalf("VXLAN receipt = %+v, want only the unassigned identity pending", vxlan)
	}
	sessions := mgr.Sessions()
	if len(sessions) != 2 || !slices.ContainsFunc(sessions, func(session bfd.SessionSnapshot) bool {
		return session.PeerAddr == netip.MustParseAddr(removed.Peer)
	}) {
		t.Fatalf("sessions = %+v, want both previous peers retained", sessions)
	}
	endpoint := opener.opened(netip.MustParseAddr(available.Local))
	if len(endpoint) != 1 || endpoint[0].isClosed() || len(endpoint[0].current()) != 2 {
		t.Fatal("available endpoint must keep the previous identity set")
	}

	// Retries for the pending identity leave the available backend alone.
	for range 3 {
		coordinator.retryPendingSources(t.Context(), snapshot.DesiredGeneration, allReconciliationSources,
			coordinator.withListeners(func(
				ctx context.Context, source reconciliationSource, candidate compiledControlSessionCandidate,
			) sourceApplyResult {
				return applyCompiledSource(ctx, source, candidate, mgr, sf, slog.New(slog.DiscardHandler), coordinator.listeners)
			}))
	}
	if endpoint := opener.opened(netip.MustParseAddr(available.Local)); len(endpoint) != 1 || endpoint[0].isClosed() {
		t.Fatal("retries reopened or closed the available backend")
	}
}

// acquireAll claims scopes and reports any endpoint failure as an error.
func acquireAll(
	owner *overlayOwner, kind bfd.TransportScopeKind, scopes []bfd.TransportScope,
) (map[bfd.TransportScope]netio.OverlayConn, error) {
	claim, err := owner.acquire(kind, scopes)
	if err != nil {
		return nil, err
	}
	failures := make([]error, 0, len(claim.failures))
	for _, failure := range claim.failures {
		failures = append(failures, failure)
	}
	return claim.conns, errors.Join(failures...)
}

func TestOverlayScopeLeasesRetainBackendUntilLastClose(t *testing.T) {
	t.Parallel()
	coordinator, _ := newConvergedReadinessCoordinator(t)
	opener := newFakeOverlayOpener()
	owner, _ := newTestOverlayOwner(t, coordinator.readiness, opener)
	scope := testVXLANScope("198.51.100.1", 1)
	conns, err := acquireAll(owner, scope.Kind, []bfd.TransportScope{scope})
	if err != nil {
		t.Fatal(err)
	}
	factory := owner.senderLeaseFactory(conns[scope], scope)
	first, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	backend := opener.opened(scope.OuterLocalAddr)[0]
	for range 2 {
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
	}
	owner.release(scope.Kind, nil)
	if backend.isClosed() || !slices.Equal(backend.current(), []bfd.TransportScope{scope}) {
		t.Fatal("duplicate close released a scope with another active lease")
	}
	if err := second.Sender().SendPacket(t.Context(), make([]byte, 24), scope.InnerPeerAddr); err != nil {
		t.Fatalf("retained scoped sender: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	owner.release(scope.Kind, nil)
	if !backend.isClosed() {
		t.Fatal("backend survived pruning after its last lease closed")
	}
	if _, err := factory(); !errors.Is(err, netio.ErrOverlayIdentityMismatch) {
		t.Fatalf("obsolete factory = %v, want identity mismatch", err)
	}
	owner.close()
	if _, err := factory(); !errors.Is(err, errOverlayOwnerClosed) {
		t.Fatalf("closed owner factory = %v, want owner closed", err)
	}
}
