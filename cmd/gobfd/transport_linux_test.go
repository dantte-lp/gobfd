//go:build linux

package main

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func loopbackName(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return iface.Name
		}
	}
	t.Skip("no loopback interface in this network namespace")
	return ""
}

func senderBoundDevice(t *testing.T, f *udpSenderFactory, srcPort uint16) string {
	t.Helper()
	f.mu.Lock()
	sender := f.senders[srcPort]
	f.mu.Unlock()
	if sender == nil {
		t.Fatalf("no sender on source port %d", srcPort)
	}
	raw, err := sender.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var device string
	if ctrlErr := raw.Control(func(fd uintptr) {
		var sockErr error
		device, sockErr = unix.GetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE)
		if sockErr != nil {
			t.Error(sockErr)
		}
	}); ctrlErr != nil {
		t.Fatal(ctrlErr)
	}
	return device
}

// RFC 5881 Section 6: the real daemon sender factory binds a single-hop sender
// to the requested interface (API path) and leaves it unbound otherwise.
func TestUDPSenderFactoryBindsSingleHopDevice(t *testing.T) {
	t.Parallel()
	lo := loopbackName(t)
	f := newUDPSenderFactory(0)
	logger := slog.New(slog.DiscardHandler)
	for _, ifName := range []string{lo, ""} {
		_, srcPort, err := f.CreateSender(netip.MustParseAddr("127.0.0.1"), ifName, false, false, logger)
		if err != nil {
			t.Fatalf("CreateSender(%q): %v", ifName, err)
		}
		if got := senderBoundDevice(t, f, srcPort); got != ifName {
			t.Fatalf("sender SO_BINDTODEVICE = %q, want %q", got, ifName)
		}
		if err := f.CloseSender(srcPort); err != nil {
			t.Fatal(err)
		}
	}
}

// The YAML path binds the real sender of a single-hop session with interface.
func TestReconcileBindsSingleHopSenderDevice(t *testing.T) {
	t.Parallel()
	lo := loopbackName(t)
	cfg := config.DefaultConfig()
	cfg.Sessions = []config.SessionConfig{{Peer: "127.0.0.2", Local: "127.0.0.1", Interface: lo}}
	candidates, err := compileBaseSessionCandidates(cfg)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	mgr := bfd.NewManager(logger)
	t.Cleanup(mgr.Close)
	f := newUDPSenderFactory(0)
	if err := applyBaseSessionCandidates(context.Background(), candidates, mgr, f, logger).Err(); err != nil {
		t.Fatalf("applyBaseSessionCandidates: %v", err)
	}
	f.mu.Lock()
	var srcPort uint16
	for port := range f.senders {
		srcPort = port
	}
	f.mu.Unlock()
	if got := senderBoundDevice(t, f, srcPort); got != lo {
		t.Fatalf("session sender SO_BINDTODEVICE = %q, want %q", got, lo)
	}
}
