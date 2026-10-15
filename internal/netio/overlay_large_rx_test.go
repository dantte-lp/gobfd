package netio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestOverlayReceivesMaximumPaddedPayload(t *testing.T) {
	for _, kind := range []bfd.TransportScopeKind{bfd.TransportScopeVXLAN, bfd.TransportScopeGeneve} {
		for _, test := range []struct {
			name    string
			size    int
			maximal bool
			reject  bool
		}{
			{"maximum-frame", bfd.MaxPaddedPduSize, true, false},
			{"truncated-frame", bfd.MaxPaddedPduSize + 1, true, true},
			{"payload-beyond-limit", bfd.MaxPaddedPduSize + 1, false, true},
		} {
			name := "vxlan"
			if kind == bfd.TransportScopeGeneve {
				name = "geneve"
			}
			t.Run(name+"/"+test.name, func(t *testing.T) {
				local := netip.MustParseAddr("127.0.39.1")
				peer := netip.MustParseAddr("127.0.0.1")
				scope := bfd.TransportScope{
					Kind: kind, Owner: "test", Backend: string(OverlayBackendUserspaceUDP), VNI: 100,
					OuterLocalAddr: local, OuterPeerAddr: peer,
					InnerLocalAddr: netip.MustParseAddr("10.0.0.2"), InnerPeerAddr: netip.MustParseAddr("10.0.0.1"),
					AddressFamily: bfd.AddressFamilyIPv4, LocalMAC: [6]byte{2, 0, 0, 0, 0, 2}, PeerMAC: [6]byte{2, 0, 0, 0, 0, 1},
				}
				var conn OverlayConn
				var err error
				port := VXLANPort
				if kind == bfd.TransportScopeVXLAN {
					conn, err = NewVXLANConnForScopes(local, 49152, []bfd.TransportScope{scope}, slog.New(slog.DiscardHandler))
				} else {
					port = GenevePort
					conn, err = NewGeneveConnForScopes(local, 49152, []bfd.TransportScope{scope}, slog.New(slog.DiscardHandler))
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if closeErr := conn.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				})
				payload := bytes.Repeat([]byte{0x5a}, test.size)
				frame := largeOverlayFrame(t, scope, payload, test.maximal)
				if !test.reject {
					want := 9090
					if kind == bfd.TransportScopeGeneve {
						want = 9342
					}
					if len(frame) != want {
						t.Fatalf("fixture frame=%d, want %d", len(frame), want)
					}
				}
				sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: peer.AsSlice()})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if closeErr := sender.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				})
				if _, writeErr := sender.WriteToUDP(
					frame, net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, port)),
				); writeErr != nil {
					t.Fatal(writeErr)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				received, meta, err := conn.RecvDecapsulated(ctx)
				if test.reject {
					want := ErrOverlayPayloadTooLarge
					if test.maximal {
						want = ErrOverlayPacketTruncated
					}
					if !errors.Is(err, want) || received != nil {
						t.Fatalf("oversized overlay payload=%d error=%v, want %v", len(received), err, want)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(received, payload) || meta.TransportScope != scope {
					t.Fatalf("complete payload/identity lost: length=%d meta=%+v", len(received), meta)
				}
			})
		}
	}
}

func TestOverlaySendsMaximumPaddedPayload(t *testing.T) {
	for _, geneve := range []bool{false, true} {
		name, port := "vxlan", VXLANPort
		if geneve {
			name, port = "geneve", GenevePort
		}
		t.Run(name, func(t *testing.T) {
			local, peer := netip.MustParseAddr("127.0.39.2"), netip.MustParseAddr("127.0.39.3")
			remote, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(peer, port)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := remote.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			if deadlineErr := remote.SetReadDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
				t.Fatal(deadlineErr)
			}
			var conn OverlayConn
			if geneve {
				conn, err = NewGeneveConn(local, 100, 49152, slog.New(slog.DiscardHandler))
			} else {
				conn, err = NewVXLANConn(local, 100, 49152, slog.New(slog.DiscardHandler))
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := conn.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			payload := bytes.Repeat([]byte{0x5a}, bfd.MaxPaddedPduSize)
			if sendErr := conn.SendEncapsulated(t.Context(), payload, peer); sendErr != nil {
				t.Fatal(sendErr)
			}
			frame := make([]byte, bfd.MaxPaddedPduSize+1000)
			n, _, err := remote.ReadFromUDP(frame)
			if err != nil {
				t.Fatal(err)
			}
			if n != bfd.MaxPaddedPduSize+VXLANHeaderSize+InnerOverheadIPv4 {
				t.Fatalf("sent frame=%d, want 9050", n)
			}
			if err := conn.SendEncapsulated(t.Context(), append(payload, 0), peer); !errors.Is(err, ErrOverlayPayloadTooLarge) {
				t.Fatalf("payload beyond 9000 error=%v, want typed limit", err)
			}
		})
	}
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
