//go:build !linux

package netio

import (
	"errors"
	"log/slog"
	"net/netip"
	"testing"
)

func TestLinuxTransportAPIsRejectUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddr("192.0.2.1")
	logger := slog.New(slog.DiscardHandler)
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "high-level listener",
			run: func() error {
				_, err := NewListener(ListenerConfig{
					Addr:   addr,
					IfName: "eth0",
					Port:   PortSingleHop,
				})
				return err
			},
		},
		{
			name: "single-hop listener",
			run: func() error {
				_, err := NewSingleHopListener(t.Context(), addr, "eth0")
				return err
			},
		},
		{
			name: "multi-hop listener",
			run: func() error {
				_, err := NewMultiHopListener(t.Context(), addr)
				return err
			},
		},
		{
			name: "generic listener",
			run: func() error {
				_, err := NewGenericListener(t.Context(), addr, "eth0", PortMicroBFD)
				return err
			},
		},
		{
			name: "UDP sender",
			run: func() error {
				_, err := NewUDPSender(
					addr,
					sourcePortMin,
					false,
					logger,
					WithDFBit(),
					WithDstPort(PortEcho),
					WithBindDevice("eth0"),
					WithWriteBuffer(4096),
				)
				return err
			},
		},
		{
			name: "source port allocator",
			run: func() error {
				_, err := NewSourcePortAllocator().Allocate()
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if err := test.run(); !errors.Is(err, ErrUnsupportedPlatform) {
				t.Fatalf("error = %v, want %v", err, ErrUnsupportedPlatform)
			}
		})
	}
}
