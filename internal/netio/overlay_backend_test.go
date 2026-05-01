package netio_test

import (
	"errors"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestNewVXLANOverlayBackendDefaultsToUserspaceUDP(t *testing.T) {
	t.Parallel()

	conn, err := netio.NewVXLANOverlayBackend(netio.VXLANOverlayBackendConfig{
		LocalAddr:     netip.MustParseAddr("127.0.0.10"),
		ManagementVNI: 100,
		SourcePort:    49152,
		Logger:        slog.Default(),
	})
	if err != nil {
		t.Skipf("VXLAN userspace backend unavailable: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	})

	if _, ok := conn.(*netio.VXLANConn); !ok {
		t.Fatalf("NewVXLANOverlayBackend returned %T, want *VXLANConn", conn)
	}
}

func TestNewGeneveOverlayBackendDefaultsToUserspaceUDP(t *testing.T) {
	t.Parallel()

	conn, err := netio.NewGeneveOverlayBackend(netio.GeneveOverlayBackendConfig{
		LocalAddr:  netip.MustParseAddr("127.0.0.11"),
		VNI:        200,
		SourcePort: 49153,
		Logger:     slog.Default(),
	})
	if err != nil {
		t.Skipf("Geneve userspace backend unavailable: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
	})

	if _, ok := conn.(*netio.GeneveConn); !ok {
		t.Fatalf("NewGeneveOverlayBackend returned %T, want *GeneveConn", conn)
	}
}

func TestNewOverlayBackendRejectsReservedBackends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "vxlan cilium",
			run: func() error {
				_, err := netio.NewVXLANOverlayBackend(netio.VXLANOverlayBackendConfig{
					Backend:       netio.OverlayBackendCilium,
					LocalAddr:     netip.MustParseAddr("127.0.0.12"),
					ManagementVNI: 100,
					SourcePort:    49154,
					Logger:        slog.Default(),
				})
				return err
			},
		},
		{
			name: "geneve ovs",
			run: func() error {
				_, err := netio.NewGeneveOverlayBackend(netio.GeneveOverlayBackendConfig{
					Backend:    netio.OverlayBackendOVS,
					LocalAddr:  netip.MustParseAddr("127.0.0.13"),
					VNI:        200,
					SourcePort: 49155,
					Logger:     slog.Default(),
				})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.run(); !errors.Is(err, netio.ErrUnsupportedOverlayBackend) {
				t.Fatalf("error = %v, want %v", err, netio.ErrUnsupportedOverlayBackend)
			}
		})
	}
}

func TestNewOverlayBackendRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	_, err := netio.NewVXLANOverlayBackend(netio.VXLANOverlayBackendConfig{
		Backend:       "bad",
		LocalAddr:     netip.MustParseAddr("127.0.0.14"),
		ManagementVNI: 100,
		SourcePort:    49156,
		Logger:        slog.Default(),
	})
	if !errors.Is(err, netio.ErrInvalidOverlayBackend) {
		t.Fatalf("invalid backend error = %v, want %v", err, netio.ErrInvalidOverlayBackend)
	}

	_, err = netio.NewGeneveOverlayBackend(netio.GeneveOverlayBackendConfig{
		Backend:    netio.OverlayBackendUserspaceUDP,
		VNI:        200,
		SourcePort: 49157,
		Logger:     slog.Default(),
	})
	if !errors.Is(err, netio.ErrInvalidOverlayBackendInput) {
		t.Fatalf("invalid input error = %v, want %v", err, netio.ErrInvalidOverlayBackendInput)
	}
}
