package bfd

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

type overlayIdentityNoopSender struct{}

func (overlayIdentityNoopSender) SendPacket(context.Context, []byte, netip.Addr) error {
	return nil
}

func TestOverlaySessionsWithSameInnerTupleDifferentVNICoexist(t *testing.T) {
	t.Parallel()

	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)

	base := SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.10"),
		LocalAddr:             netip.MustParseAddr("192.0.2.20"),
		Type:                  SessionTypeGeneve,
		Role:                  RoleActive,
		DesiredMinTxInterval:  time.Second,
		RequiredMinRxInterval: time.Second,
		DetectMultiplier:      3,
		TransportScope: TransportScope{
			Kind:           TransportScopeGeneve,
			Owner:          "geneve-config",
			Backend:        "userspace-udp",
			OuterPeerAddr:  netip.MustParseAddr("198.51.100.10"),
			OuterLocalAddr: netip.MustParseAddr("198.51.100.20"),
			InnerPeerAddr:  netip.MustParseAddr("192.0.2.10"),
			InnerLocalAddr: netip.MustParseAddr("192.0.2.20"),
			AddressFamily:  AddressFamilyIPv4,
			PeerMAC:        [6]byte{0x02, 0, 0, 0, 0, 1},
			LocalMAC:       [6]byte{0x02, 0, 0, 0, 0, 2},
			VNI:            100,
		},
	}
	second := base
	second.TransportScope.VNI = 200

	firstSession, err := mgr.CreateSession(
		context.Background(), base, NonOwningSenderLeaseFactory(overlayIdentityNoopSender{}),
	)
	if err != nil {
		t.Fatalf("CreateSession(first): %v", err)
	}
	secondSession, err := mgr.CreateSession(
		context.Background(), second, NonOwningSenderLeaseFactory(overlayIdentityNoopSender{}),
	)
	if err != nil {
		t.Fatalf("CreateSession(second): %v", err)
	}
	if firstSession.LocalDiscriminator() == secondSession.LocalDiscriminator() {
		t.Fatal("colliding overlay identities reused one wire session")
	}

	pkt := &ControlPacket{
		Version: 1, State: StateDown, DetectMult: 3,
		MyDiscriminator: 100, YourDiscriminator: 0,
		DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
	}
	meta := PacketMeta{
		SrcAddr: second.PeerAddr, DstAddr: second.LocalAddr, TTL: 255,
		TransportScope: second.TransportScope,
	}
	if err := mgr.DemuxWithWire(pkt, meta, nil); err != nil {
		t.Fatalf("DemuxWithWire(second overlay identity): %v", err)
	}
}

func TestOverlayDiscriminatorDemuxValidatesExactTunnelBinding(t *testing.T) {
	t.Parallel()

	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	cfg := SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.10"),
		LocalAddr:             netip.MustParseAddr("192.0.2.20"),
		Type:                  SessionTypeVXLAN,
		Role:                  RoleActive,
		DesiredMinTxInterval:  time.Second,
		RequiredMinRxInterval: time.Second,
		DetectMultiplier:      3,
		TransportScope: TransportScope{
			Kind: TransportScopeVXLAN, VNI: 100,
			OuterPeerAddr:  netip.MustParseAddr("198.51.100.10"),
			OuterLocalAddr: netip.MustParseAddr("198.51.100.20"),
			InnerPeerAddr:  netip.MustParseAddr("192.0.2.10"),
			InnerLocalAddr: netip.MustParseAddr("192.0.2.20"),
			AddressFamily:  AddressFamilyIPv4,
			PeerMAC:        [6]byte{0x02, 0, 0, 0, 0, 1},
			LocalMAC:       [6]byte{0x00, 0x52, 0x02, 0, 0, 0},
		},
	}
	sess, err := mgr.CreateSession(
		context.Background(), cfg, NonOwningSenderLeaseFactory(overlayIdentityNoopSender{}),
	)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	pkt := &ControlPacket{
		Version: 1, State: StateDown, DetectMult: 3,
		MyDiscriminator: 100, YourDiscriminator: sess.LocalDiscriminator(),
		DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
	}
	mismatch := cfg.TransportScope
	mismatch.VNI++
	meta := PacketMeta{SrcAddr: cfg.PeerAddr, DstAddr: cfg.LocalAddr, TTL: 255, TransportScope: mismatch}
	if err := mgr.DemuxWithWire(pkt, meta, nil); !errors.Is(err, ErrDemuxNoMatch) {
		t.Fatalf("DemuxWithWire(mismatched tunnel) error = %v, want ErrDemuxNoMatch", err)
	}

	meta.TransportScope = cfg.TransportScope
	if err := mgr.DemuxWithWire(pkt, meta, nil); err != nil {
		t.Fatalf("DemuxWithWire(exact tunnel): %v", err)
	}
}

func TestBaseDiscriminatorDemuxRejectsOverlayTransport(t *testing.T) {
	t.Parallel()

	mgr := NewManager(slog.New(slog.DiscardHandler))
	t.Cleanup(mgr.Close)
	cfg := SessionConfig{
		PeerAddr: netip.MustParseAddr("192.0.2.10"), LocalAddr: netip.MustParseAddr("192.0.2.20"),
		Type: SessionTypeSingleHop, Role: RoleActive,
		DesiredMinTxInterval: time.Second, RequiredMinRxInterval: time.Second, DetectMultiplier: 3,
	}
	sess, err := mgr.CreateSession(
		context.Background(), cfg, NonOwningSenderLeaseFactory(overlayIdentityNoopSender{}),
	)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	pkt := &ControlPacket{
		Version: 1, State: StateDown, DetectMult: 3,
		MyDiscriminator: 100, YourDiscriminator: sess.LocalDiscriminator(),
		DesiredMinTxInterval: 1_000_000, RequiredMinRxInterval: 1_000_000,
	}
	meta := PacketMeta{
		SrcAddr: cfg.PeerAddr, DstAddr: cfg.LocalAddr, TTL: 255,
		TransportScope: TransportScope{Kind: TransportScopeVXLAN, VNI: 100},
	}
	if err := mgr.DemuxWithWire(pkt, meta, nil); !errors.Is(err, ErrDemuxNoMatch) {
		t.Fatalf("DemuxWithWire(overlay transport) error = %v, want ErrDemuxNoMatch", err)
	}
}

func TestSessionKeyCanonicalizesOverlayMappedIPv4(t *testing.T) {
	t.Parallel()

	cfg := SessionConfig{
		PeerAddr:  netip.MustParseAddr("192.0.2.1"),
		LocalAddr: netip.MustParseAddr("192.0.2.2"),
		Type:      SessionTypeGeneve,
		TransportScope: TransportScope{
			Kind:           TransportScopeGeneve,
			OuterPeerAddr:  netip.MustParseAddr("::ffff:198.51.100.1"),
			OuterLocalAddr: netip.MustParseAddr("::ffff:198.51.100.2"),
			InnerPeerAddr:  netip.MustParseAddr("::ffff:192.0.2.1"),
			InnerLocalAddr: netip.MustParseAddr("::ffff:192.0.2.2"),
		},
	}
	mapped, err := sessionKeyFromConfig(cfg)
	if err != nil {
		t.Fatalf("sessionKeyFromConfig(mapped): %v", err)
	}
	cfg.TransportScope.OuterPeerAddr = netip.MustParseAddr("198.51.100.1")
	cfg.TransportScope.OuterLocalAddr = netip.MustParseAddr("198.51.100.2")
	cfg.TransportScope.InnerPeerAddr = netip.MustParseAddr("192.0.2.1")
	cfg.TransportScope.InnerLocalAddr = netip.MustParseAddr("192.0.2.2")
	unmapped, err := sessionKeyFromConfig(cfg)
	if err != nil {
		t.Fatalf("sessionKeyFromConfig(unmapped): %v", err)
	}
	if mapped != unmapped {
		t.Fatalf("mapped overlay key = %+v, want %+v", mapped, unmapped)
	}
}
