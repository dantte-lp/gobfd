//go:build linux

package netio

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"testing"
)

func TestUDPSenderRejectsInvalidSourcePortBeforeOptions(t *testing.T) {
	t.Parallel()
	for _, mode := range []struct {
		name     string
		multiHop bool
		dstPort  uint16
	}{
		{"single", false, 3784},
		{"multi", true, 4784},
		{"echo", false, 3785},
		{"micro", false, 6784},
	} {
		for _, port := range []uint16{0, 1, 3784, 3785, 4784, 6784, 49151} {
			t.Run(fmt.Sprintf("%s/%d", mode.name, port), func(t *testing.T) {
				t.Parallel()
				called := false
				sender, err := NewUDPSender(netip.MustParseAddr("127.0.0.26"), port, mode.multiHop,
					slog.New(slog.DiscardHandler), WithDstPort(mode.dstPort), func(*UDPSender) { called = true })
				if sender != nil {
					if closeErr := sender.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				if !errors.Is(err, ErrSourcePortInvalid) || sender != nil || called {
					t.Fatalf("invalid source port %d: sender=%v error=%v optionsCalled=%v", port, sender, err, called)
				}
			})
		}
	}
}

func TestUDPSenderSourcePortBoundaries(t *testing.T) {
	t.Parallel()
	for _, port := range []uint16{49152, 65535} {
		t.Run(strconv.FormatUint(uint64(port), 10), func(t *testing.T) {
			t.Parallel()
			sender, err := NewUDPSender(netip.MustParseAddr("127.0.0.26"), port, false, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := sender.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			if got := sender.conn.LocalAddr().(*net.UDPAddr).AddrPort().Port(); got != port {
				t.Fatalf("bound source port = %d, want %d", got, port)
			}
		})
	}
}
