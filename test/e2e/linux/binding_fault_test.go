//go:build e2e_linux

package linux_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

type bindingFaultPacket struct {
	Discriminator uint32              `json:"discriminator"`
	Meta          bfd.PacketMeta      `json:"meta"`
	Outcome       bfd.DeliveryOutcome `json:"outcome"`
}

type bindingFaultDemux struct {
	*bfd.Manager

	packets chan bindingFaultPacket
}

func (d *bindingFaultDemux) DemuxWithWire(
	packet bfd.ControlPacket, meta bfd.PacketMeta, wire []byte,
) (bfd.DeliveryOutcome, error) {
	outcome, err := d.Manager.DemuxWithWire(packet, meta, wire)
	select {
	case d.packets <- bindingFaultPacket{packet.MyDiscriminator, meta, outcome}:
	default:
		return outcome, errors.Join(err, errors.New("binding packet observation overflow"))
	}
	return outcome, err
}

func TestRetainedBindingFailuresOnRecreatedVeth(t *testing.T) {
	for _, local := range []string{"10.250.0.1", "fd00:250::1"} {
		for _, ownership := range []string{"config", "shared", "api"} {
			t.Run(local+"/"+ownership, func(t *testing.T) {
				testRetainedBindingFailures(t, netip.MustParseAddr(local), ownership)
			})
		}
	}
}

func testRetainedBindingFailures(t *testing.T, local netip.Addr, ownership string) {
	t.Helper()
	assertNetworkNoneIsolation(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	nl := newRouteSocket(t)
	defer nl.close(t)
	defer func() {
		nl.deleteLinkByName(rebindLinkA)
		assertNetworkNoneIsolation(t)
	}()
	first := createRebindVeth(t, nl)
	peerAddr := local.Next()
	prefix := uint8(30)
	if local.Is6() {
		prefix = 64
	}
	assign := func(index int, address netip.Addr) {
		t.Helper()
		if err := nl.addIPAddress(index, address, prefix); err != nil {
			t.Fatal(err)
		}
	}
	assignPeer := func() int {
		t.Helper()
		iface, err := net.InterfaceByName(rebindLinkB)
		if err != nil {
			t.Fatal(err)
		}
		assign(iface.Index, peerAddr)
		return iface.Index
	}
	assign(first, local)
	assignPeer()
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger, bfd.WithSessionInterfaceResolver(netio.ResolveSessionIfIndex))
	defer mgr.Close()
	listener, err := netio.NewListener(netio.ListenerConfig{Addr: local, IfName: rebindLinkA, Port: netio.PortSingleHop})
	if err != nil {
		t.Fatal(err)
	}
	tap := &bindingFaultDemux{Manager: mgr, packets: make(chan bindingFaultPacket, 16)}
	receiverCtx, stop := context.WithCancel(ctx)
	receiverDone := make(chan error, 1)
	go func() { receiverDone <- netio.NewReceiver(tap, logger).Run(receiverCtx, listener) }()
	closeReceiver := sync.OnceFunc(func() {
		stop()
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		if receiveErr := <-receiverDone; receiveErr != nil {
			t.Error(receiveErr)
		}
	})
	defer closeReceiver()
	var sender *netio.UDPSender
	var acquired, released atomic.Uint32
	factory := func() (*bfd.SenderLease, error) {
		created, createErr := netio.NewUDPSender(local, 49152, false, logger, netio.WithBindDevice(rebindLinkA))
		if createErr != nil {
			return nil, createErr
		}
		sender = created
		acquired.Add(1)
		return bfd.NewSenderLease(created, func() error { released.Add(1); return created.Close() }), nil
	}
	cfg := bfd.SessionConfig{
		LocalAddr: local, PeerAddr: peerAddr, Interface: rebindLinkA, Type: bfd.SessionTypeSingleHop, Role: bfd.RoleActive,
		DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
	}
	if ownership != "api" {
		result := mgr.ReconcileSessionsForOwnerDetailed(ctx, bfd.ConfigReconciliationOwner(),
			[]bfd.ReconcileConfig{{SessionConfig: cfg, SenderLeaseFactory: factory}})
		if result.Err() != nil {
			t.Fatal(result.Err())
		}
	}
	if ownership != "config" {
		if _, createErr := mgr.CreateSessionAs(ctx, cfg, factory, bfd.APISessionOwner("binding-fault")); createErr != nil {
			t.Fatal(createErr)
		}
	}
	initial := mgr.Sessions()[0]
	session, exists := mgr.LookupByDiscriminator(initial.LocalDiscr)
	if !exists || sender == nil {
		t.Fatal("initial physical transport missing")
	}
	beforeSockets, _, _, err := observeDaemonResources(os.Getpid())
	if err != nil || len(beforeSockets) != 2 {
		t.Fatalf("initial UDP socket inventory=%v error=%v", beforeSockets, err)
	}
	var peer *netio.UDPSender
	var peerReceiver *net.UDPConn
	closePeer := func() {
		t.Helper()
		if peer != nil {
			if closeErr := peer.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			peer = nil
		}
		if peerReceiver != nil {
			if closeErr := peerReceiver.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			peerReceiver = nil
		}
	}
	defer closePeer()
	openPeer := func() {
		t.Helper()
		peerReceiver, err = net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(peerAddr, netio.PortSingleHop)))
		if err != nil {
			t.Fatal(err)
		}
		peer, err = netio.NewUDPSender(peerAddr, 49153, false, logger, netio.WithBindDevice(rebindLinkA))
		if err != nil {
			t.Fatal(err)
		}
	}
	openPeer()
	send := func(marker uint32) {
		t.Helper()
		packet := bfd.ControlPacket{
			Version: bfd.Version, State: bfd.StateInit, DetectMult: 3, MyDiscriminator: marker,
			YourDiscriminator: initial.LocalDiscr, DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
		}
		var wire [bfd.MaxPacketSize]byte
		n, marshalErr := bfd.MarshalControlPacket(&packet, wire[:])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if sendErr := peer.SendPacket(ctx, wire[:n], local); sendErr != nil {
			t.Fatal(sendErr)
		}
	}
	send(177)
	requireBindingFaultPacket(ctx, t, tap, 177, local, first, bfd.DeliveryQueued)
	awaitBindingFaultOwner(ctx, t, session, 177, 0)
	requireBindingFaultTX(t, peerReceiver, local, initial.LocalDiscr, 177)
	closePeer()
	nl.deleteLinkByName(rebindLinkA)
	next := createRebindVeth(t, nl)
	if next == first {
		t.Fatal("recreated interface retained old index")
	}
	wrongIndex := assignPeer()
	resolved, resolveErr := netio.ResolveSessionIfIndex(local, rebindLinkA)
	if resolveErr != nil || resolved != next {
		t.Fatalf("new interface resolution=%d error=%v", resolved, resolveErr)
	}
	var listenerChecks atomic.Uint32
	beforeCommit := func(index int) func() error {
		return func() error { listenerChecks.Add(1); return listener.RebindDeviceTo(index) }
	}
	pending, refreshErr := mgr.RefreshInterfaceBindings(ctx, nil, beforeCommit(next))
	resource, unavailable := bfd.UnavailableResource(refreshErr)
	if pending != 1 || !unavailable || resource.Kind != bfd.ResourceKindAddress ||
		listenerChecks.Load() != 0 || !mgr.InterfaceBindingPending() {
		t.Fatalf("missing address did not fail sender before listener proof: pending=%d error=%v checks=%d",
			pending, refreshErr, listenerChecks.Load())
	}
	senderFailure := refreshErr.Error()
	assign(next, local)
	pending, refreshErr = mgr.RefreshInterfaceBindings(ctx, nil, beforeCommit(wrongIndex))
	if pending != 1 || !errors.Is(refreshErr, unix.ENODEV) ||
		listenerChecks.Load() != 1 || !mgr.InterfaceBindingPending() {
		t.Fatalf("wrong listener proof published binding: pending=%d error=%v checks=%d",
			pending, refreshErr, listenerChecks.Load())
	}
	listenerFailure := refreshErr.Error()
	openPeer()
	priorRX, priorTX := session.PacketsReceived(), session.PacketsSent()
	send(178)
	rejected := requireBindingFaultPacket(ctx, t, tap, 178, local, next, bfd.DeliveryScopeMismatch)
	window := time.NewTimer(1100 * time.Millisecond)
	defer window.Stop()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-window.C:
	}
	if session.PacketsReceived() != priorRX || session.PacketsSent() != priorTX || !mgr.InterfaceBindingPending() {
		t.Fatal("pending binding admitted RX or emitted periodic TX")
	}
	pending, refreshErr = mgr.RefreshInterfaceBindings(ctx, nil, beforeCommit(next))
	if pending != 0 || refreshErr != nil || mgr.InterfaceBindingPending() {
		t.Fatalf("correct retry failed: pending=%d error=%v", pending, refreshErr)
	}
	current := mgr.Sessions()
	if len(current) != 1 || current[0].Ref != initial.Ref || acquired.Load() != 1 || released.Load() != 0 {
		t.Fatal("failure or retry replaced physical ownership")
	}
	send(179)
	requireBindingFaultPacket(ctx, t, tap, 179, local, next, bfd.DeliveryQueued)
	awaitBindingFaultOwner(ctx, t, session, 179, priorRX)
	requireBindingFaultTX(t, peerReceiver, local, initial.LocalDiscr, 179)
	afterSockets, _, _, observeErr := observeDaemonResources(os.Getpid())
	if observeErr != nil || len(afterSockets) != 4 || !slices.Contains(afterSockets, beforeSockets[0]) ||
		!slices.Contains(afterSockets, beforeSockets[1]) || sender.SrcPort() != 49152 {
		t.Fatalf("retained transport changed socket identity: before=%v after=%v error=%v",
			beforeSockets, afterSockets, observeErr)
	}
	if ownership != "api" {
		result := mgr.ReconcileSessionsForOwnerDetailed(ctx, bfd.ConfigReconciliationOwner(), nil)
		if result.Err() != nil || result.Released != 1 {
			t.Fatalf("configuration claim was lost during recovery: %+v", result)
		}
	}
	if ownership == "shared" {
		retained := mgr.Sessions()
		if len(retained) != 1 || retained[0].Ref != initial.Ref || released.Load() != 0 {
			t.Fatal("configuration withdrawal lost surviving API ownership")
		}
	}
	if ownership != "config" {
		destroyErr := mgr.DestroySessionAs(ctx, initial.LocalDiscr, bfd.APISessionOwner("binding-fault"))
		if destroyErr != nil {
			t.Fatal(destroyErr)
		}
	}
	if len(mgr.Sessions()) != 0 || released.Load() != 1 {
		t.Fatal("last owner withdrawal leaked physical transport")
	}
	closePeer()
	mgr.Close()
	mgr.Close()
	closeReceiver()
	remaining, _, _, observeErr := observeDaemonResources(os.Getpid())
	if observeErr != nil || len(remaining) != 0 || released.Load() != 1 {
		t.Fatalf("transport cleanup: sockets=%v release=%d error=%v", remaining, released.Load(), observeErr)
	}
	family := "ipv4"
	if local.Is6() {
		family = "ipv6"
	}
	writeJSONArtifact(t, "binding-fault-"+family+"-"+ownership+".json", struct {
		First, Next                    int
		LocalDiscriminator             uint32
		RefRetained                    bool
		SenderFailure, ListenerFailure string
		Rejected                       bindingFaultPacket
		RetainedSockets                []string
		SourcePort                     uint16
	}{
		First: first, Next: next, LocalDiscriminator: initial.LocalDiscr,
		RefRetained: current[0].Ref == initial.Ref, SenderFailure: senderFailure,
		ListenerFailure: listenerFailure, Rejected: rejected,
		RetainedSockets: beforeSockets, SourcePort: sender.SrcPort(),
	})
}

func requireBindingFaultPacket(
	ctx context.Context, t *testing.T, tap *bindingFaultDemux, marker uint32,
	local netip.Addr, index int, want bfd.DeliveryOutcome,
) bindingFaultPacket {
	t.Helper()
	for {
		select {
		case observed := <-tap.packets:
			if observed.Discriminator != marker {
				continue
			}
			if observed.Meta.SrcAddr != local.Next() || observed.Meta.DstAddr != local || observed.Meta.TTL != 255 ||
				observed.Meta.IfIndex != index || observed.Meta.MultiHop || observed.Outcome != want {
				t.Fatalf("packet transport proof mismatch: %+v wantindex=%d outcome=%s", observed, index, want)
			}
			return observed
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func awaitBindingFaultOwner(ctx context.Context, t *testing.T, session *bfd.Session, marker uint32, prior uint64) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for session.PacketsReceived() <= prior || session.RemoteDiscriminator() != marker || session.State() != bfd.StateUp {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func requireBindingFaultTX(t *testing.T, conn *net.UDPConn, local netip.Addr, discriminator, marker uint32) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var wire [bfd.MaxPacketSize]byte
	for {
		n, remote, err := conn.ReadFromUDPAddrPort(wire[:])
		if err != nil {
			t.Fatal(err)
		}
		var packet bfd.ControlPacket
		if err := bfd.UnmarshalControlPacket(wire[:n], &packet); err != nil {
			t.Fatal(err)
		}
		if packet.MyDiscriminator != discriminator || remote.Addr().Unmap() != local.Unmap() || remote.Port() != 49152 {
			t.Fatalf("unexpected retained TX: %+v from=%s", packet, remote)
		}
		if packet.YourDiscriminator == marker && packet.State == bfd.StateUp {
			return
		}
	}
}
