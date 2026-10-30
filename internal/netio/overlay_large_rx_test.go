package netio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestOverlayLargeFrameDecapsulationIsPortable(t *testing.T) {
	t.Parallel()

	for _, kind := range []bfd.TransportScopeKind{bfd.TransportScopeVXLAN, bfd.TransportScopeGeneve} {
		for _, test := range []struct {
			name     string
			size     int
			truncate bool
			want     error
		}{
			{name: "maximum-frame", size: bfd.MaxPaddedPduSize},
			{name: "payload-beyond-limit", size: bfd.MaxPaddedPduSize + 1, want: ErrOverlayPayloadTooLarge},
			{name: "shortened-inner-packet", size: bfd.MaxPaddedPduSize, truncate: true, want: ErrInnerBadIPLength},
		} {
			name := "vxlan"
			if kind == bfd.TransportScopeGeneve {
				name = "geneve"
			}
			t.Run(name+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				scope := bfd.TransportScope{
					Kind: kind, VNI: 100,
					OuterLocalAddr: netip.MustParseAddr("192.0.2.2"),
					OuterPeerAddr:  netip.MustParseAddr("192.0.2.1"),
					InnerLocalAddr: netip.MustParseAddr("10.0.0.2"),
					InnerPeerAddr:  netip.MustParseAddr("10.0.0.1"),
				}
				payload := bytes.Repeat([]byte{0x5a}, test.size)
				frame := largeOverlayFrame(t, scope, payload, true)
				if test.truncate {
					frame = frame[:len(frame)-1]
				}
				got, err := decapLargeOverlayFrame(scope, frame)
				if test.want != nil {
					// Low-level decoders may retain parsed fields when rejecting a packet.
					if !errors.Is(err, test.want) {
						t.Fatalf("decapsulation payload=%d error=%v, want %v", len(got), err, test.want)
					}
					return
				}
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("decapsulation payload=%d error=%v, want complete %d bytes", len(got), err, len(payload))
				}
				wantSize := 9090
				if kind == bfd.TransportScopeGeneve {
					wantSize = 9342
				}
				if len(frame) != wantSize {
					t.Fatalf("maximum frame=%d, want %d", len(frame), wantSize)
				}
			})
		}
	}
}

func decapLargeOverlayFrame(scope bfd.TransportScope, frame []byte) ([]byte, error) {
	if scope.Kind == bfd.TransportScopeVXLAN {
		conn := &VXLANConn{managementVNI: scope.VNI, localAddr: scope.InnerLocalAddr}
		payload, _, err := conn.decapVXLANDatagram(frame, &net.UDPAddr{IP: scope.OuterPeerAddr.AsSlice()})
		return payload, err
	}
	conn := &GeneveConn{vni: scope.VNI}
	packet, _, err := conn.decapGeneveInnerPacket(frame)
	return packet.payload, err
}

func largeOverlayFrame(t *testing.T, scope bfd.TransportScope, payload []byte, maximal bool) []byte {
	t.Helper()
	inner, err := BuildInnerPacket(payload, scope.InnerPeerAddr, scope.InnerLocalAddr, 49152)
	if err != nil {
		t.Fatal(err)
	}
	copy(inner[:6], scope.LocalMAC[:])
	if scope.Kind == bfd.TransportScopeVXLAN {
		copy(inner[:6], innerDstMAC[:])
	}
	copy(inner[6:12], scope.PeerMAC[:])
	if maximal {
		const optionsSize = 40
		ipEnd := InnerEthSize + InnerIPv4Size
		extended := make([]byte, len(inner)+optionsSize)
		copy(extended, inner[:ipEnd])
		copy(extended[ipEnd+optionsSize:], inner[ipEnd:])
		inner = extended
		ip := inner[InnerEthSize : ipEnd+optionsSize]
		ip[0] = 0x4f
		binary.BigEndian.PutUint16(ip[2:4], uint16(len(inner)-InnerEthSize))
		ip[10], ip[11] = 0, 0
		binary.BigEndian.PutUint16(ip[10:12], ipv4HeaderChecksum(ip))
	}
	headerLen := VXLANHeaderSize
	if scope.Kind == bfd.TransportScopeGeneve && maximal {
		headerLen = GeneveHeaderMinSize + 63*4
	}
	frame := make([]byte, headerLen+len(inner))
	copy(frame[headerLen:], inner)
	if scope.Kind == bfd.TransportScopeVXLAN {
		if _, err := MarshalVXLANHeader(frame, scope.VNI); err != nil {
			t.Fatal(err)
		}
	} else {
		header := GeneveHeader{VNI: scope.VNI, OBit: true, ProtocolType: GeneveProtocolEthernet}
		if maximal {
			header.OptLen = 63
		}
		if _, err := MarshalGeneveHeader(frame, header); err != nil {
			t.Fatal(err)
		}
	}
	return frame
}
