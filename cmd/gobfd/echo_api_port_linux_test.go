//go:build linux

package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/server"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func TestAPISenderDestinationPort(t *testing.T) {
	// Fixed ::1 ports require sequential subtests.
	for _, local := range []string{"127.0.31.1", "::1"} {
		for _, tc := range []struct {
			name string
			kind bfdv1.SessionType
			port uint16
		}{
			{"echo", bfdv1.SessionType_SESSION_TYPE_ECHO, 3785},
			{"single-hop", bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, 3784},
			{"multi-hop", bfdv1.SessionType_SESSION_TYPE_MULTI_HOP, 4784},
		} {
			t.Run(local+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				t.Cleanup(cancel)
				deadline, _ := ctx.Deadline()
				addr := netip.MustParseAddr(local)
				network := "udp4"
				if addr.Is6() {
					network = "udp6"
				}

				type datagram struct {
					port uint16
					data [bfd.MaxPacketSize]byte
					n    int
					from netip.AddrPort
					err  error
				}
				received := make(chan datagram, 3)
				var readers sync.WaitGroup
				t.Cleanup(readers.Wait)
				for _, port := range []uint16{3784, 3785, 4784} {
					listener, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(addr, port)))
					if err != nil {
						t.Fatalf("setup %s loopback listener %s:%d: %v", network, addr, port, err)
					}
					t.Cleanup(func() {
						if err := listener.Close(); err != nil {
							t.Errorf("close UDP listener %s:%d: %v", addr, port, err)
						}
					})
					if err := listener.SetReadDeadline(deadline); err != nil {
						t.Fatalf("set UDP listener %d deadline: %v", port, err)
					}
					readers.Go(func() {
						packet := datagram{port: port}
						packet.n, packet.from, packet.err = listener.ReadFromUDPAddrPort(packet.data[:])
						received <- packet
					})
				}

				logger := slog.New(slog.DiscardHandler)
				sf := newUDPSenderFactory(0)
				t.Cleanup(func() { cleanupCandidateTestSenders(t, sf) })
				mgr := bfd.NewManager(logger)
				t.Cleanup(mgr.Close)
				rpc := connect.NewServer()
				server.Register(rpc, mgr, sf, logger)
				server.RegisterEcho(rpc, mgr, sf, logger)
				mux := http.NewServeMux()
				connecthttp.Mount(mux, rpc)
				httpServer := httptest.NewTestServer(t, mux)
				httpClient := httpServer.Client()
				client := connect.NewClient(connecthttp.NewTransport(httpClient, httpServer.URL))

				var discr uint32
				if tc.kind == bfdv1.SessionType_SESSION_TYPE_ECHO {
					response, err := bfdv1connect.NewEchoServiceClient(client).AddEchoSession(ctx, &bfdv1.AddEchoSessionRequest{
						PeerAddress:      local,
						LocalAddress:     local,
						TxInterval:       durationpb.New(50 * time.Millisecond),
						DetectMultiplier: 3,
					})
					if err != nil {
						t.Fatalf("AddEchoSession: %v", err)
					}
					discr = response.GetSession().GetLocalDiscriminator()
				} else {
					response, err := bfdv1connect.NewBfdServiceClient(client).AddSession(ctx, &bfdv1.AddSessionRequest{
						PeerAddress:           local,
						LocalAddress:          local,
						Type:                  tc.kind,
						DesiredMinTxInterval:  durationpb.New(50 * time.Millisecond),
						RequiredMinRxInterval: durationpb.New(50 * time.Millisecond),
						DetectMultiplier:      3,
					})
					if err != nil {
						t.Fatalf("AddSession: %v", err)
					}
					discr = response.GetSession().GetLocalDiscriminator()
				}
				if discr == 0 {
					t.Fatal("API returned zero local discriminator")
				}

				select {
				case packet := <-received:
					if packet.err != nil {
						t.Fatalf("read UDP destination %d: %v", packet.port, packet.err)
					}
					if packet.from.Addr() != addr || packet.from.Port() < 49152 {
						t.Fatalf("packet source = %s, want %s with port in [49152,65535]", packet.from, addr)
					}
					if packet.n != 24 {
						t.Fatalf("packet length = %d, want 24", packet.n)
					}
					var control bfd.ControlPacket
					if err := bfd.UnmarshalControlPacket(packet.data[:packet.n], &control); err != nil {
						t.Fatalf("decode UDP destination %d packet: %v", packet.port, err)
					}
					if control.Version != 1 || control.State != bfd.StateDown || control.DetectMult != 3 ||
						control.MyDiscriminator != discr || control.YourDiscriminator != 0 {
						t.Fatalf("UDP destination %d packet = %+v, want Down BFD v1 with discriminator %d and detect multiplier 3",
							packet.port, control, discr)
					}
					if packet.port != tc.port {
						t.Fatalf("%s API emitted discriminator %d to UDP destination %d, want %d",
							tc.name, discr, packet.port, tc.port)
					}
				case <-ctx.Done():
					t.Fatalf("%s API emitted no packet to UDP destination %d before deadline: %v", tc.name, tc.port, ctx.Err())
				}
			})
		}
	}
}
