//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
	"github.com/dantte-lp/gobfd/internal/testutil"
)

type replacementReceiveFactory struct {
	*udpSenderFactory

	calls      atomic.Uint32
	failSecond bool
	drained    chan bfd.ControlPacket
	drainGate  <-chan struct{}
}

func (f *replacementReceiveFactory) createSenderForSession(
	local netip.Addr, multiHop bool, logger *slog.Logger, opts ...netio.SenderOption,
) (bfd.PacketSender, uint16, error) {
	if f.calls.Add(1) == 2 && f.failSecond {
		return nil, 0, errInjectedSenderCreation
	}
	sender, port, err := f.udpSenderFactory.createSenderForSession(local, multiHop, logger, opts...)
	if err != nil {
		return nil, 0, err
	}
	return replacementReceiveSender{sender, f.drained, f.drainGate}, port, nil
}

type replacementReceiveSender struct {
	bfd.PacketSender

	drained   chan<- bfd.ControlPacket
	drainGate <-chan struct{}
}

func (s replacementReceiveSender) SendPacket(ctx context.Context, wire []byte, peer netip.Addr) error {
	if err := s.PacketSender.SendPacket(ctx, wire, peer); err != nil {
		return err
	}
	var packet bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(wire, &packet); err != nil {
		return err
	}
	if packet.State == bfd.StateAdminDown {
		select {
		case s.drained <- packet:
		default:
			return errors.New("AdminDown observation overflow")
		}
		select {
		case <-s.drainGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func TestBaseReplacementReceiveLifetime(t *testing.T) {
	testutil.LockLoopbackBFD(t)
	// Real ::1 BFD ports require sequential subtests.
	for _, local := range []string{"127.0.47.1", "::1"} {
		for _, from := range []string{"single_hop", "multi_hop"} {
			for _, fail := range []bool{false, true} {
				name := local + "/" + from + "/success"
				if fail {
					name = local + "/" + from + "/prepare-failure-retry"
				}
				t.Run(name, func(t *testing.T) { testBaseReplacementReceiveLifetime(t, local, from, fail) })
			}
		}
	}
}

func testBaseReplacementReceiveLifetime(t *testing.T, local, from string, fail bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	logger := slog.New(slog.DiscardHandler)
	coordinator, _ := newConvergedReadinessCoordinator(t) // fail-silent-ok: the second result is an unused health checker.
	mgr := bfd.NewManager(logger)
	owner := newListenerOwner(mgr, coordinator.readiness, 0, logger)
	t.Cleanup(owner.close)
	t.Cleanup(mgr.Close)
	drainGate := make(chan struct{})
	releaseDrain := sync.OnceFunc(func() { close(drainGate) })
	defer releaseDrain()
	factory := &replacementReceiveFactory{
		udpSenderFactory: newUDPSenderFactory(config.SocketConfig{}),
		failSecond:       fail, drained: make(chan bfd.ControlPacket, 16), drainGate: drainGate,
	}
	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{{Peer: local, Local: local, Type: from}}
	oldCandidates, err := compileBaseSessionCandidates(cfg)
	if err != nil {
		t.Fatal(err)
	}
	oldListener := sessionListenerConfigs(cfg)[0]
	oldKey := listenerKeyOf(oldListener)
	if acquireErr := owner.acquire(sourceClaim(sourceBase), []netio.ListenerConfig{oldListener}); acquireErr != nil {
		t.Fatal(acquireErr)
	}
	if result := applyBaseSessionCandidates(ctx, oldCandidates, mgr, factory, logger, owner); result.Err() != nil {
		t.Fatal(result.Err())
	}
	old := mgr.Sessions()[0]
	owner.mu.Lock()
	oldSocket := owner.listeners[oldKey].ln
	owner.mu.Unlock()
	owner.release(sourceClaim(sourceBase), nil)
	requireReplacementReceiveClaim(t, owner, oldKey, oldSocket)

	cfg.Sessions[0].Type = "multi_hop"
	if from == "multi_hop" {
		cfg.Sessions[0].Type = "single_hop"
	}
	nextCandidates, err := compileBaseSessionCandidates(cfg)
	if err != nil {
		t.Fatal(err)
	}
	nextListener := sessionListenerConfigs(cfg)[0]
	nextKey := listenerKeyOf(nextListener)
	if acquireErr := owner.acquire(sourceClaim(sourceBase), []netio.ListenerConfig{nextListener}); acquireErr != nil {
		t.Fatal(acquireErr)
	}
	if fail {
		result := applyBaseSessionCandidates(ctx, nextCandidates, mgr, factory, logger, owner)
		if !errors.Is(result.Err(), errInjectedSenderCreation) || result.Created != 0 || result.Released != 0 {
			t.Fatalf("failed preparation changed the committed identity: %+v", result)
		}
		current := mgr.Sessions()
		if len(current) != 1 || current[0].Ref != old.Ref {
			t.Fatal("failed preparation replaced the predecessor")
		}
		owner.release(sourceClaim(sourceBase), nil)
		requireReplacementReceiveClaim(t, owner, oldKey, oldSocket)
		requireReplacementPort(t, nextListener, false)
		if acquireErr := owner.acquire(sourceClaim(sourceBase), []netio.ListenerConfig{nextListener}); acquireErr != nil {
			t.Fatal(acquireErr)
		}
	}
	done := make(chan bfd.ReconcileResult, 1)
	go func() { done <- applyBaseSessionCandidates(ctx, nextCandidates, mgr, factory, logger, owner) }()
	select {
	case packet := <-factory.drained:
		if packet.MyDiscriminator != old.LocalDiscr || packet.Diag != bfd.DiagAdminDown || packet.Poll {
			t.Fatalf("incorrect predecessor drain: %+v", packet)
		}
	case result := <-done:
		t.Fatalf("replacement finished before a predecessor AdminDown send: %+v", result)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	current := mgr.Sessions()
	if len(current) != 1 || current[0].Ref == old.Ref || current[0].Type != nextCandidates[0].config.Type {
		t.Fatal("replacement was not published before predecessor drain")
	}
	owner.release(sourceClaim(sourceBase), nil)
	requireReplacementReceiveClaim(t, owner, oldKey, oldSocket)
	requireReplacementReceiveClaim(t, owner, nextKey, nil)
	requireReplacementPort(t, oldListener, true)
	requireReplacementPort(t, nextListener, true)
	releaseDrain()
	select {
	case result := <-done:
		if result.Err() != nil || result.Created != 1 || result.Released != 1 {
			t.Fatalf("replacement or retirement failed: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	requireReplacementPort(t, oldListener, false)
	requireReplacementReceiveClaim(t, owner, nextKey, nil)
	requireReplacementRX(ctx, t, mgr, factory.udpSenderFactory, current[0], logger)
	mgr.Close()
	mgr.Close()
	requireReplacementPort(t, nextListener, false)
	factory.mu.Lock()
	remaining := len(factory.senders)
	factory.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("sender sockets after repeated Close: %d", remaining)
	}
}

func requireReplacementReceiveClaim(t *testing.T, owner *listenerOwner, key listenerKey, socket *netio.Listener) {
	t.Helper()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	listener := owner.listeners[key]
	if listener == nil || listener.apiClaims != 1 || listener.claims != 0 || (socket != nil && listener.ln != socket) {
		t.Fatalf("receive lease missing, duplicated or replaced: key=%+v listener=%+v", key, listener)
	}
}

func requireReplacementPort(t *testing.T, cfg netio.ListenerConfig, occupied bool) {
	t.Helper()
	network := "udp4"
	if cfg.Addr.Is6() {
		network = "udp6"
	}
	probe, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(cfg.Addr, cfg.Port)))
	if probe != nil {
		if closeErr := probe.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if (occupied && !errors.Is(err, syscall.EADDRINUSE)) || (!occupied && err != nil) {
		t.Fatalf("UDP port %s:%d occupied=%t: %v", cfg.Addr, cfg.Port, occupied, err)
	}
}

func requireReplacementRX(
	ctx context.Context, t *testing.T, mgr *bfd.Manager, factory *udpSenderFactory,
	snapshot bfd.SessionSnapshot, logger *slog.Logger,
) {
	t.Helper()
	//nolint:contextcheck // The production factory owns socket construction; packet I/O accepts the probe context.
	peer, port, err := factory.CreateSender(snapshot.LocalAddr, "", snapshot.Type, false, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := factory.CloseSender(port); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	session, exists := mgr.LookupByDiscriminator(snapshot.LocalDiscr)
	if !exists {
		t.Fatal("new session disappeared before UDP RX probe")
	}
	prior := session.PacketsReceived()
	remoteDiscriminator := uint32(177)
	if remoteDiscriminator == snapshot.LocalDiscr {
		remoteDiscriminator++
	}
	packet := bfd.ControlPacket{
		Version: bfd.Version, State: bfd.StateInit, DetectMult: 3,
		MyDiscriminator: remoteDiscriminator, YourDiscriminator: snapshot.LocalDiscr,
		DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
	}
	var wire [bfd.MaxPacketSize]byte
	n, err := bfd.MarshalControlPacket(&packet, wire[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.SendPacket(ctx, wire[:n], snapshot.LocalAddr); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		sessions := mgr.Sessions()
		if len(sessions) == 1 && sessions[0].Ref == snapshot.Ref && sessions[0].RemoteDiscr == remoteDiscriminator &&
			sessions[0].State == bfd.StateUp && session.PacketsReceived() > prior {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("new-type UDP RX did not commit: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
