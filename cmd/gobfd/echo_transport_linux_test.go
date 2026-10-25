//go:build linux

package main

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
	"github.com/dantte-lp/gobfd/internal/testutil"
)

// Run serially: the wire probes own UDP 3785 in their network namespace.
func TestReconcileEchoSenderTransport(t *testing.T) {
	testutil.LockLoopbackBFD(t)
	lo := loopbackName(t)
	for _, test := range []struct {
		name, network, local, peer, device string
		invalid                            bool
	}{
		{name: "IPv4 bound", network: "udp4", local: "127.0.31.1", peer: "127.0.31.254", device: lo},
		{name: "IPv6 bound", network: "udp6", local: "::1", peer: "::1", device: lo},
		{name: "IPv4 route lookup", network: "udp4", local: "127.0.31.1", peer: "127.0.31.254"},
		{name: "IPv6 route lookup", network: "udp6", local: "::1", peer: "::1"},
		{
			name: "IPv4 invalid device", network: "udp4", local: "127.0.31.1", peer: "127.0.31.254",
			device: "gobfd-missing", invalid: true,
		},
		{
			name: "IPv6 invalid device", network: "udp6", local: "::1", peer: "::1",
			device: "gobfd-missing", invalid: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger := slog.New(slog.DiscardHandler)
			local, peer := netip.MustParseAddr(test.local), netip.MustParseAddr(test.peer)
			factory := newUDPSenderFactory(8192)
			t.Cleanup(func() { cleanupCandidateTestSenders(t, factory) })
			mgr := bfd.NewManager(logger)
			t.Cleanup(mgr.Close)
			allocator, port := exhaustedOverlayAllocator(t)
			allocator.Release(port)
			factory.portAlloc = allocator
			cfg := config.DefaultConfig()
			cfg.Echo.Enabled = true
			cfg.Echo.Peers = []config.EchoPeerConfig{{
				Peer: test.peer, Local: test.local, Interface: test.device, TxInterval: time.Hour, DetectMult: 3,
			}}
			candidates, err := compileEchoSessionCandidates(cfg)
			if err != nil {
				t.Fatal(err)
			}
			result := applyEchoSessionCandidates(t.Context(), candidates, mgr, factory, logger, nil)
			if test.invalid {
				if !errors.Is(result.Err(), unix.ENODEV) || result.Created != 0 || len(mgr.EchoSessions()) != 0 {
					t.Fatalf("invalid device accepted or wrong failure: %+v", result)
				}
			} else {
				if result.Err() != nil || result.Created != 1 {
					t.Fatalf("create Echo sender: %+v", result)
				}
				if got := senderBoundDevice(t, factory, port); got != test.device {
					t.Errorf("Echo SO_BINDTODEVICE = %q, want %q", got, test.device)
				}
				_, wantWrite := referenceSocketBuffers(t, 8192)
				if got := socketOptionAt(t, local, port, unix.SOL_SOCKET, unix.SO_SNDBUF); got != wantWrite {
					t.Errorf("Echo SO_SNDBUF = %d, want %d", got, wantWrite)
				}
				factory.mu.Lock()
				sender := factory.senders[port]
				factory.mu.Unlock()
				listener, err := net.ListenUDP(test.network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(peer, 3785)))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if closeErr := listener.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				})
				payload := []byte("echo-transport-probe")
				if sendErr := sender.SendPacket(t.Context(), payload, peer); sendErr != nil {
					t.Fatal(sendErr)
				}
				if deadlineErr := listener.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
					t.Fatal(deadlineErr)
				}
				var received [128]byte
				n, source, err := listener.ReadFromUDPAddrPort(received[:])
				if err != nil || !bytes.Equal(received[:n], payload) || source != netip.AddrPortFrom(local, port) {
					t.Fatalf("Echo UDP/3785 probe: source=%s bytes=%q err=%v", source, received[:n], err)
				}
				unchanged := applyEchoSessionCandidates(t.Context(), candidates, mgr, factory, logger, nil)
				if unchanged.Err() != nil || unchanged.Created != 0 || unchanged.Released != 0 {
					t.Fatalf("unchanged Echo desired set reopened sender: %+v", unchanged)
				}
				removed := applyEchoSessionCandidates(t.Context(), nil, mgr, factory, logger, nil)
				if removed.Err() != nil || removed.Released != 1 || len(mgr.EchoSessions()) != 0 {
					t.Fatalf("remove Echo sender: %+v", removed)
				}
				if err := sender.SendPacket(t.Context(), payload, peer); !errors.Is(err, netio.ErrSocketClosed) {
					t.Fatalf("retired Echo sender remained open: %v", err)
				}
			}
			factory.mu.Lock()
			remaining := len(factory.senders)
			factory.mu.Unlock()
			if got, err := allocator.Allocate(); err != nil || got != port || remaining != 0 {
				t.Fatalf("Echo source-port lease leaked: port=%d want=%d senders=%d err=%v", got, port, remaining, err)
			}
		})
	}
}
