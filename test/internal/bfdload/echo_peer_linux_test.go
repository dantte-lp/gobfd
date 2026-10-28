//go:build linux

package bfdload

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestEchoReturnerPreservesActualDatagramAndRejectsBadInput(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"IPv4", "IPv6"}[index], func(t *testing.T) {
			cfg := echoFixture(t)
			cfg.Profile.Sessions = 1
			cfg.Sessions = cfg.Sessions[index : index+1]
			endpoint := cfg.Sessions[0]
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			receiver, err := netio.NewGenericListener(ctx, endpoint.Local.Addr(), "", endpoint.Local.Port())
			if err != nil {
				t.Fatal(err)
			}
			type received struct {
				wire []byte
				meta netio.PacketMeta
				err  error
			}
			result, done := make(chan received, 1), make(chan struct{})
			go func() {
				defer close(done)
				var wire [bfd.MaxPacketSize]byte
				n, meta, readErr := receiver.ReadPacket(wire[:])
				result <- received{append([]byte(nil), wire[:n]...), meta, readErr}
			}()
			t.Cleanup(func() {
				if closeErr := receiver.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				<-done
			})
			peer, err := bindPeer(ctx, cfg, 0, endpoint, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			group := peerGroup{peers: []*peerRuntime{peer}, cancel: cancel, started: time.Now()}
			group.stopIO = context.AfterFunc(ctx, group.closeIO)
			peer.started = true
			group.workers.Go(func() { peer.read(ctx, cancel) })
			group.workers.Go(func() { peer.run(ctx, cancel, group.started) })
			t.Cleanup(group.stop)
			sender, err := netio.NewUDPSender(endpoint.Local.Addr(), 61000, false, slog.New(slog.DiscardHandler),
				netio.WithDstPort(endpoint.Peer.Port()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := sender.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			packet := bfd.ControlPacket{
				Version: 1, State: bfd.StateUp, DetectMult: 7,
				MyDiscriminator: 42, YourDiscriminator: 77, DesiredMinTxInterval: 123456, RequiredMinRxInterval: 234567,
			}
			wire := make([]byte, bfd.MaxPacketSize)
			n, err := bfd.MarshalControlPacket(&packet, wire)
			if err != nil {
				t.Fatal(err)
			}
			wire = append(wire[:n:n], 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef)
			badTTL, err := newEchoReturnSender(ctx, OwnerEndpoint{Local: endpoint.Peer, Peer: endpoint.Local}, 61001)
			if err != nil {
				t.Fatal(err)
			}
			badErr := badTTL.SendPacket(ctx, wire, endpoint.Peer.Addr())
			t.Cleanup(func() {
				if closeErr := badTTL.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			readCount, rejected := uint64(3), uint64(2)
			if endpoint.Local.Addr().Is4() {
				wrongPeer, bindErr := netio.NewUDPSender(netip.MustParseAddr("127.0.0.3"), 61002,
					false, slog.New(slog.DiscardHandler), netio.WithDstPort(endpoint.Peer.Port()))
				if bindErr != nil {
					t.Fatal(bindErr)
				}
				wrongErr := wrongPeer.SendPacket(ctx, wire, endpoint.Peer.Addr())
				if packetErr := errors.Join(wrongErr, wrongPeer.Close()); packetErr != nil {
					t.Fatal(packetErr)
				}
				readCount, rejected = 4, 3
			}
			if err := errors.Join(badErr, badTTL.Close(), sender.SendPacket(ctx, []byte{0}, endpoint.Peer.Addr()),
				sender.SendPacket(ctx, wire, endpoint.Peer.Addr())); err != nil {
				t.Fatal(err)
			}
			select {
			case returned := <-result:
				if returned.err != nil || !bytes.Equal(returned.wire, wire) || returned.meta.TTL != 254 ||
					returned.meta.SrcAddr != endpoint.Peer.Addr() || returned.meta.DstAddr != endpoint.Local.Addr() {
					t.Fatalf("Echo return was invented, altered or had wrong transport: %+v: %x", returned, wire)
				}
			case <-ctx.Done():
				t.Fatal("independent UDP input did not produce the original Echo return")
			}
			group.stop()
			if peer.observed.Read != readCount || peer.observed.Rejected != rejected || peer.observed.Accepted != 1 ||
				peer.observed.Written != 1 || peer.observed.Scheduled != 1 || peer.readErr != nil || peer.loopErr != nil {
				t.Fatalf("Echo returner bypassed actual-input validation/accounting: %+v read=%v loop=%v",
					peer.observed, peer.readErr, peer.loopErr)
			}
		})
	}
}
