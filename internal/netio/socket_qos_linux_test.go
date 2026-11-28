//go:build linux

package netio

import (
	"errors"
	"log/slog"
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSocketQoSRejectsInvalidDirectOptions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		qos  SocketQoS
	}{
		{"negative DSCP", SocketQoS{IPv4DSCP: -1}},
		{"DSCP overflow", SocketQoS{IPv4DSCP: 64}},
		{"negative Traffic Class", SocketQoS{IPv6TrafficClass: -1}},
		{"Traffic Class overflow", SocketQoS{IPv6TrafficClass: 256}},
		{"negative priority", SocketQoS{Priority: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sender, err := NewUDPSender(netip.MustParseAddr("127.0.47.1"), 49152, false,
				slog.New(slog.DiscardHandler), WithBindDevice("gobfd-missing"), WithSocketQoS(test.qos))
			if sender != nil {
				if closeErr := sender.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			if sender != nil || !errors.Is(err, ErrInvalidSocketQoS) {
				t.Fatalf("invalid QoS reached socket creation: sender=%v error=%v", sender, err)
			}
		})
	}
}

func TestSocketQoSZeroMarkNeedsNoPermission(t *testing.T) {
	t.Parallel()
	err := setSenderQoS(-1, SocketQoS{MarkRequired: true}, slog.New(slog.DiscardHandler),
		func(_, _, option, _ int) error {
			if option == unix.SO_MARK {
				return unix.EPERM
			}
			return nil
		})
	if err != nil {
		t.Fatalf("disabled mark attempted a privileged operation: %v", err)
	}
}

func TestSocketQoSNeutralProbePreservesPriority(t *testing.T) {
	t.Parallel()
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_UDP)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := unix.Close(fd); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if optionErr := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_PRIORITY, 6); optionErr != nil {
		t.Fatal(optionErr)
	}
	if optionErr := setSenderSockOpts(fd, false, false, "", nil, nil); optionErr != nil {
		t.Fatal(optionErr)
	}
	priority, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_PRIORITY)
	if err != nil || priority != 6 {
		t.Fatalf("neutral probe changed queue policy: priority=%d error=%v", priority, err)
	}
}
