//go:build linux

package netio

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func TestListenerReceivesCompletePaddedDatagram(t *testing.T) {
	t.Parallel()
	for _, size := range []int{bfd.MaxPaddedPduSize, bfd.MaxPaddedPduSize + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			conn, err := listenUDP(t.Context(), netip.MustParseAddrPort("127.0.0.1:0"), "", false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := conn.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			if deadlineErr := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
				t.Fatal(deadlineErr)
			}
			sender, err := listenUDP(t.Context(), netip.MustParseAddrPort("127.0.0.1:0"), "", false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := sender.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			payload := bytes.Repeat([]byte{0x5a}, size)
			if _, writeErr := sender.WriteToUDP(payload, conn.LocalAddr().(*net.UDPAddr)); writeErr != nil {
				t.Fatal(writeErr)
			}
			ln := NewListenerFromConn(&LinuxPacketConn{conn: conn}, false)
			packet, err := ln.RecvPacket(context.Background())
			if size > bfd.MaxPaddedPduSize {
				if !errors.Is(err, ErrPacketTruncated) || packet != nil {
					t.Fatalf("oversized UDP packet=%v error=%v, want typed truncation", packet, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer packet.Release()
			if !bytes.Equal(packet.Data, payload) {
				t.Fatalf("received %d bytes, want complete %d-byte datagram", len(packet.Data), size)
			}
		})
	}
}
