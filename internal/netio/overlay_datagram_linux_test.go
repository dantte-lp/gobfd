//go:build linux

package netio_test

import (
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

// This kernel truncation fixture sends a jumbo datagram above portable UDP limits.
func TestOverlayRecvRejectsTruncatedDatagram(t *testing.T) {
	local := netip.MustParseAddr("127.0.0.1")
	payload := makePayload(9000 - netio.VXLANHeaderSize - netio.InnerOverheadIPv4)
	tests := []struct {
		name    string
		port    uint16
		newConn func() (netio.OverlayConn, error)
		packet  func() ([]byte, error)
	}{
		{
			name: "vxlan",
			port: netio.VXLANPort,
			newConn: func() (netio.OverlayConn, error) {
				return netio.NewVXLANConn(local, 100, 49152, slog.New(slog.DiscardHandler))
			},
			packet: func() ([]byte, error) {
				return netio.BuildVXLANPacket(payload, 100, local, local, 49152)
			},
		},
		{
			name: "geneve",
			port: netio.GenevePort,
			newConn: func() (netio.OverlayConn, error) {
				return netio.NewGeneveConn(local, 100, 49152, slog.New(slog.DiscardHandler))
			},
			packet: func() ([]byte, error) {
				return netio.BuildGenevePacket(payload, 100, local, local, 49152)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := tt.newConn()
			if err != nil {
				t.Skipf("%s loopback socket unavailable: %v", tt.name, err)
			}
			defer conn.Close()

			packet, err := tt.packet()
			if err != nil {
				t.Fatalf("build packet: %v", err)
			}
			// Exceed every supported VXLAN/Geneve framing allowance.
			packet = append(packet, make([]byte, 1024)...)
			sender, err := net.ListenUDP("udp4", nil)
			if err != nil {
				t.Fatalf("open sender: %v", err)
			}
			defer sender.Close()
			if _, writeErr := sender.WriteToUDP(
				packet,
				net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, tt.port)),
			); writeErr != nil {
				t.Fatalf("send packet: %v", writeErr)
			}

			got, _, err := conn.RecvDecapsulated(t.Context())
			if !errors.Is(err, netio.ErrOverlayPacketTruncated) || got != nil {
				t.Fatalf("RecvDecapsulated payload length=%d error=%v, want ErrOverlayPacketTruncated", len(got), err)
			}
		})
	}
}
