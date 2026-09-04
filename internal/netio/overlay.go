package netio

// overlay.go: Shared overlay tunnel abstractions for BFD encapsulated
// in VXLAN (RFC 8971) and Geneve (RFC 9521).
//
// Architecture:
//
//	                OverlayConn (interface)
//	               /                      \
//	        VXLANConn                  GeneveConn
//	     (vxlan_conn.go)            (geneve_conn.go)
//
//	OverlaySender adapts OverlayConn -> bfd.PacketSender
//	OverlayReceiver reads from OverlayConn -> Manager.DemuxWithWire
//
// The OverlaySender/OverlayReceiver pattern mirrors the existing
// UDPSender/Receiver pair for standard BFD sessions.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"syscall"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

const overlayReceiverStoppedMsg = "overlay receiver stopped"

// -------------------------------------------------------------------------
// OverlayConn — tunnel connection interface
// -------------------------------------------------------------------------

// OverlayConn abstracts a tunnel connection for encapsulated BFD.
// Implementations handle the tunnel-specific header marshaling/unmarshaling
// (VXLAN or Geneve) and the inner packet assembly (Ethernet + IPv4 + UDP).
//
// Each OverlayConn owns a UDP socket bound to the tunnel port (4789 for
// VXLAN, 6081 for Geneve) and handles both send and receive directions.
type OverlayConn interface {
	// SendEncapsulated wraps a BFD Control payload in the tunnel
	// encapsulation (outer UDP + tunnel header + inner Ethernet/IPv4/UDP)
	// and sends it to the given VTEP/NVE address.
	SendEncapsulated(ctx context.Context, bfdPayload []byte, dstAddr netip.Addr) error

	// RecvDecapsulated reads a tunnel packet from the socket, strips the
	// tunnel header and inner packet headers, and returns the raw BFD
	// Control payload along with overlay metadata (source VTEP/NVE, VNI).
	RecvDecapsulated(ctx context.Context) ([]byte, OverlayMeta, error)

	// Close releases the underlying UDP socket.
	Close() error
}

type scopedOverlayConn interface {
	SendEncapsulatedFor(ctx context.Context, payload []byte, scope bfd.TransportScope) error
}

// OverlayMeta holds metadata extracted from a received tunnel packet.
// Used for session demultiplexing: BFD sessions over tunnels are keyed
// by (VTEP/NVE IP, VNI) rather than by interface name.
type OverlayMeta struct {
	// SrcAddr is the source VTEP (VXLAN) or NVE (Geneve) IP address
	// from the outer UDP packet.
	SrcAddr netip.Addr

	// DstAddr is the destination VTEP/NVE IP address from the outer
	// UDP packet (the local system's tunnel endpoint).
	DstAddr netip.Addr

	// VNI is the tunnel's Virtual Network Identifier (24-bit).
	// For VXLAN: the Management VNI (RFC 8971 Section 3).
	// For Geneve: the VNI from the Geneve header (RFC 9521).
	VNI uint32

	// TTL is the Time-to-Live from the inner IPv4 header.
	TTL uint8

	// TransportScope is the exact configured tunnel and Format A identity
	// matched by the received packet.
	TransportScope bfd.TransportScope
}

type overlayReceiveKey struct {
	kind           bfd.TransportScopeKind
	outerPeerAddr  netip.Addr
	outerLocalAddr netip.Addr
	innerPeerAddr  netip.Addr
	innerLocalAddr netip.Addr
	addressFamily  bfd.AddressFamily
	peerMAC        [6]byte
	localMAC       [6]byte
	vni            uint32
}

func receiveKeyForScope(scope bfd.TransportScope) overlayReceiveKey {
	return overlayReceiveKey{
		kind:           scope.Kind,
		outerPeerAddr:  scope.OuterPeerAddr.Unmap(),
		outerLocalAddr: scope.OuterLocalAddr.Unmap(),
		innerPeerAddr:  scope.InnerPeerAddr.Unmap(),
		innerLocalAddr: scope.InnerLocalAddr.Unmap(),
		addressFamily:  scope.AddressFamily,
		peerMAC:        scope.PeerMAC,
		localMAC:       scope.LocalMAC,
		vni:            scope.VNI,
	}
}

func receiveKeyForPacket(
	kind bfd.TransportScopeKind,
	outerPeer, outerLocal netip.Addr,
	vni uint32,
	packet innerPacket,
) overlayReceiveKey {
	return overlayReceiveKey{
		kind: kind, outerPeerAddr: outerPeer.Unmap(), outerLocalAddr: outerLocal.Unmap(),
		innerPeerAddr: packet.srcIP, innerLocalAddr: packet.dstIP,
		addressFamily: bfd.AddressFamilyIPv4,
		peerMAC:       packet.srcMAC, localMAC: packet.dstMAC, vni: vni,
	}
}

func validateOverlayScopes(
	kind bfd.TransportScopeKind,
	localAddr netip.Addr,
	scopes []bfd.TransportScope,
) error {
	seen := make(map[overlayReceiveKey]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope.Kind != kind || scope.Owner == "" || scope.Backend == "" ||
			scope.VNI > 0x00ff_ffff || scope.AddressFamily != bfd.AddressFamilyIPv4 ||
			scope.OuterLocalAddr.Unmap() != localAddr.Unmap() ||
			!scope.OuterPeerAddr.Is4() || !scope.OuterLocalAddr.Is4() ||
			!scope.InnerPeerAddr.Is4() || !scope.InnerLocalAddr.Is4() {
			return fmt.Errorf("overlay scope %+v: %w", scope, ErrOverlayIdentityMismatch)
		}
		key := receiveKeyForScope(scope)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate overlay wire identity %+v: %w", key, ErrOverlayIdentityMismatch)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func openScopedOverlayUDP(
	localAddr netip.Addr,
	port uint16,
	kind bfd.TransportScopeKind,
	scopes []bfd.TransportScope,
) (*net.UDPConn, map[overlayReceiveKey]bfd.TransportScope, error) {
	if len(scopes) > 0 {
		if err := validateOverlayScopes(kind, localAddr, scopes); err != nil {
			return nil, nil, err
		}
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: localAddr.AsSlice(), Port: int(port)})
	if err != nil {
		return nil, nil, fmt.Errorf("listen overlay UDP %s:%d: %w", localAddr, port, err)
	}
	if len(scopes) == 0 {
		return conn, nil, nil
	}
	scopeMap := make(map[overlayReceiveKey]bfd.TransportScope, len(scopes))
	for _, scope := range scopes {
		scopeMap[receiveKeyForScope(scope)] = scope
	}
	return conn, scopeMap, nil
}

// -------------------------------------------------------------------------
// OverlayConn Errors
// -------------------------------------------------------------------------

var (
	// ErrOverlayVNIMismatch indicates the received packet's VNI does not
	// match the expected management VNI configured for the tunnel.
	ErrOverlayVNIMismatch = errors.New("overlay: VNI mismatch")

	// ErrOverlayRecvClosed indicates the overlay connection was closed
	// during a receive operation.
	ErrOverlayRecvClosed = errors.New("overlay: connection closed")

	// ErrOverlayInvalidAddr indicates the remote address from the outer
	// UDP packet could not be parsed.
	ErrOverlayInvalidAddr = errors.New("overlay: invalid remote address")

	// ErrOverlayInnerDstMismatch indicates an inner packet addressed to a
	// different local tunnel endpoint.
	ErrOverlayInnerDstMismatch = errors.New("overlay: inner destination address mismatch")

	// ErrOverlayPacketTruncated indicates the receive buffer did not contain
	// the complete UDP datagram.
	ErrOverlayPacketTruncated = errors.New("overlay: truncated UDP datagram")

	// ErrOverlayIdentityMismatch indicates that no exact configured tunnel and
	// Format A identity matched the received packet.
	ErrOverlayIdentityMismatch = errors.New("overlay: tunnel identity mismatch")
)

func readOverlayDatagram(conn *net.UDPConn, buf []byte) (int, *net.UDPAddr, error) {
	n, _, flags, remoteAddr, err := conn.ReadMsgUDP(buf, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("read overlay UDP datagram: %w", err)
	}
	if flags&syscall.MSG_TRUNC != 0 {
		return 0, nil, ErrOverlayPacketTruncated
	}
	return n, remoteAddr, nil
}

func validateInnerDestination(got, want netip.Addr) error {
	if got != want.Unmap() {
		return fmt.Errorf("inner destination=%s local=%s: %w", got, want, ErrOverlayInnerDstMismatch)
	}
	return nil
}

// -------------------------------------------------------------------------
// OverlaySender — adapts OverlayConn to bfd.PacketSender
// -------------------------------------------------------------------------

// OverlaySender adapts an OverlayConn into a bfd.PacketSender, allowing
// BFD sessions to send tunnel-encapsulated packets through the standard
// session.sendControl() path without awareness of the tunnel layer.
//
// The BFD Session calls SendPacket(ctx, bfdPayload, peerAddr), and the
// OverlaySender wraps the payload in the appropriate tunnel encapsulation.
type OverlaySender struct {
	conn     OverlayConn
	scope    bfd.TransportScope
	isScoped bool
}

// NewOverlaySender creates a PacketSender that wraps BFD payloads in
// tunnel encapsulation before sending.
func NewOverlaySender(conn OverlayConn, scope ...bfd.TransportScope) *OverlaySender {
	sender := &OverlaySender{conn: conn}
	if len(scope) > 0 {
		sender.scope = scope[0]
		sender.isScoped = true
	}
	return sender
}

// SendPacket implements bfd.PacketSender by delegating to the OverlayConn's
// SendEncapsulated method. The addr parameter is the remote VTEP/NVE IP.
func (s *OverlaySender) SendPacket(
	ctx context.Context,
	buf []byte,
	addr netip.Addr,
) error {
	if s.isScoped {
		scopedConn, ok := s.conn.(scopedOverlayConn)
		if !ok {
			return fmt.Errorf("overlay send for exact scope: %w", ErrOverlayIdentityMismatch)
		}
		if err := scopedConn.SendEncapsulatedFor(ctx, buf, s.scope); err != nil {
			return fmt.Errorf("overlay send to %s: %w", s.scope.OuterPeerAddr, err)
		}
		return nil
	}
	if err := s.conn.SendEncapsulated(ctx, buf, addr); err != nil {
		return fmt.Errorf("overlay send to %s: %w", addr, err)
	}
	return nil
}

// -------------------------------------------------------------------------
// OverlayReceiver — reads tunnel packets, delivers inner BFD to Manager
// -------------------------------------------------------------------------

// OverlayReceiver reads tunnel-encapsulated packets from an OverlayConn,
// strips the tunnel and inner headers, unmarshals the BFD Control packet,
// and delivers it to the Manager via Demuxer.
//
// This is the tunnel equivalent of the standard netio.Receiver, adapted
// for overlay encapsulation. The receive loop pattern follows the existing
// EchoReceiver design.
type OverlayReceiver struct {
	conn   OverlayConn
	mgr    Demuxer
	logger *slog.Logger
}

// NewOverlayReceiver creates a receiver that strips tunnel encapsulation
// and delivers inner BFD Control packets to the manager.
func NewOverlayReceiver(
	conn OverlayConn,
	mgr Demuxer,
	logger *slog.Logger,
) *OverlayReceiver {
	return &OverlayReceiver{
		conn:   conn,
		mgr:    mgr,
		logger: logger.With(slog.String("component", "netio.overlay_receiver")),
	}
}

// Run reads from the overlay connection in a loop until ctx is cancelled.
// Each received packet is decapsulated, unmarshaled, and routed to the
// manager. Errors from individual packets are logged but do not stop the
// receiver. Only context cancellation terminates the loop.
//
// The receive flow for each packet:
//  1. conn.RecvDecapsulated(ctx) -> raw BFD payload + OverlayMeta
//  2. bfd.UnmarshalControlPacket(payload) -> ControlPacket
//  3. Build bfd.PacketMeta from OverlayMeta
//  4. mgr.DemuxWithWire(pkt, meta, wire) -> route to session
func (r *OverlayReceiver) Run(ctx context.Context) error {
	r.logger.Info("overlay receiver started")

	for {
		if ctx.Err() != nil {
			r.logger.Info(overlayReceiverStoppedMsg)
			return nil //nolint:nilerr // Context cancellation is expected; return nil to signal clean shutdown.
		}

		if err := r.recvOne(ctx); err != nil {
			if ctx.Err() != nil {
				r.logger.Info(overlayReceiverStoppedMsg)
				return nil //nolint:nilerr // Context cancellation during recv is expected at shutdown.
			}
			if errors.Is(err, ErrOverlayRecvClosed) {
				r.logger.Info(overlayReceiverStoppedMsg)
				return nil
			}
			if isExpectedOverlayDrop(err) {
				r.logger.Debug("overlay packet dropped",
					slog.String("error", err.Error()),
				)
				continue
			}
			r.logger.Warn("overlay recv error",
				slog.String("error", err.Error()),
			)
		}
	}
}

// recvOne performs a single receive-unmarshal-demux cycle for a tunnel packet.
func (r *OverlayReceiver) recvOne(ctx context.Context) error {
	// Step 1: Read and decapsulate tunnel packet.
	bfdPayload, ometa, err := r.conn.RecvDecapsulated(ctx)
	if err != nil {
		return fmt.Errorf("overlay recv: %w", err)
	}

	// Step 2: Unmarshal BFD Control packet.
	// RFC 5880 Section 6.8.6 steps 1-7: version, length, detect mult,
	// multipoint, my discriminator, your discriminator validation.
	var pkt bfd.ControlPacket
	if err := bfd.UnmarshalControlPacket(bfdPayload, &pkt); err != nil {
		r.logger.Debug("invalid BFD packet in overlay",
			slog.String("src", ometa.SrcAddr.String()),
			slog.Uint64("vni", uint64(ometa.VNI)),
			slog.String("error", err.Error()),
		)
		return nil // Drop invalid packets silently per RFC 5880 Section 6.8.6.
	}

	// Step 3: Build bfd.PacketMeta from overlay metadata.
	srcAddr := ometa.TransportScope.InnerPeerAddr
	dstAddr := ometa.TransportScope.InnerLocalAddr
	if !srcAddr.IsValid() || !dstAddr.IsValid() {
		srcAddr = ometa.SrcAddr
		dstAddr = ometa.DstAddr
	}
	meta := bfd.PacketMeta{
		SrcAddr:        srcAddr,
		DstAddr:        dstAddr,
		TTL:            ometa.TTL,
		TransportScope: ometa.TransportScope,
	}

	// Step 4: Copy raw wire bytes only when auth verification needs stable
	// backing storage for authentication slices.
	var wire []byte
	if pkt.Auth != nil {
		wire = make([]byte, len(bfdPayload))
		copy(wire, bfdPayload)
		if err := bfd.UnmarshalControlPacket(wire, &pkt); err != nil {
			return fmt.Errorf("overlay reparse auth packet: %w", err)
		}
	}

	// Step 5: Route to session via manager.
	if err := r.mgr.DemuxWithWire(&pkt, meta, wire); err != nil {
		r.logger.Debug("overlay demux failed",
			slog.String("src", ometa.SrcAddr.String()),
			slog.Uint64("vni", uint64(ometa.VNI)),
			slog.String("error", err.Error()),
		)
	}

	return nil
}

func isExpectedOverlayDrop(err error) bool {
	expected := []error{
		ErrOverlayVNIMismatch,
		ErrOverlayIdentityMismatch,
		ErrOverlayInvalidAddr,
		ErrInnerPacketTooShort,
		ErrInnerBadEtherType,
		ErrInnerBadDestinationMAC,
		ErrInnerBadIPVersion,
		ErrInnerBadIHL,
		ErrInnerBadIPLength,
		ErrInnerFragmented,
		ErrInnerBadIPChecksum,
		ErrInnerBadProtocol,
		ErrInnerBadTTL,
		ErrInnerBadUDPSourcePort,
		ErrInnerBadUDPDestinationPort,
		ErrInnerBadUDPLength,
		ErrInnerBadUDPChecksum,
		ErrOverlayInnerDstMismatch,
		ErrOverlayPacketTruncated,
		ErrGeneveVAPIdentityUnavailable,
		ErrVXLANHeaderTooShort,
		ErrVXLANInvalidFlags,
		ErrGeneveHeaderTooShort,
		ErrGeneveInvalidVersion,
		ErrGeneveOBitNotSet,
		ErrGeneveCBitSet,
		ErrGeneveUnexpectedProto,
	}
	for _, target := range expected {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
