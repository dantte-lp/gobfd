package main

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"sync"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/netio"
)

type lifecycleOverlayConn struct {
	stubOverlayConn

	closes   int
	closeErr error
	scope    bfd.TransportScope
}

func (c *lifecycleOverlayConn) Close() error {
	c.closes++
	return c.closeErr
}

//nolint:unparam // The scoped overlay interface requires an error result; this stub records successful dispatch.
func (c *lifecycleOverlayConn) SendEncapsulatedFor(
	_ context.Context, _ []byte, scope bfd.TransportScope,
) error {
	c.scope = scope
	return nil
}

func TestOverlayBackendOwnsExactPortLease(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("source port allocator requires Linux")
	}
	for _, scenario := range []string{"success", "construction failure", "close failure"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			alloc, available := exhaustedOverlayAllocator(t)
			alloc.Release(available)
			backend := &lifecycleOverlayConn{}
			if scenario == "close failure" {
				backend.closeErr = errors.New("injected close failure")
			}
			scope := bfd.TransportScope{
				Kind: bfd.TransportScopeGeneve, VNI: 100,
				OuterLocalAddr: netip.MustParseAddr("127.0.0.92"),
			}
			leased, err := leaseOverlayBackend(alloc, func(port uint16) (netio.OverlayConn, error) {
				if port != available {
					t.Fatalf("lease port = %d, want %d", port, available)
				}
				if scenario == "construction failure" {
					return nil, errors.New("injected construction failure")
				}
				return backend, nil
			})
			var conns []netio.OverlayConn
			if scenario == "construction failure" {
				if err == nil || leased != nil {
					t.Fatal("failed constructor published a connection")
				}
			} else {
				conns = append(conns, leased)
				assertOverlayPortExhausted(t, alloc)
				assertOverlayLeaseDispatch(t, leased, scope, backend)
				var group sync.WaitGroup
				for range 4 {
					group.Go(func() {
						if closeErr := conns[0].Close(); !errors.Is(closeErr, backend.closeErr) {
							t.Errorf("Close = %v, want %v", closeErr, backend.closeErr)
						}
					})
				}
				group.Wait()
				if backend.closes != 1 {
					t.Fatalf("backend closed %d times", backend.closes)
				}
			}
			got, err := alloc.Allocate()
			if err != nil || got != available {
				t.Fatalf("released lease = %d, %v; want %d", got, err, available)
			}
			for _, conn := range conns {
				if err := conn.Close(); !errors.Is(err, backend.closeErr) {
					t.Error(err)
				}
			}
			assertOverlayPortExhausted(t, alloc)
		})
	}
}

func exhaustedOverlayAllocator(t *testing.T) (*netio.SourcePortAllocator, uint16) {
	t.Helper()
	alloc := netio.NewSourcePortAllocator()
	var last uint16
	for range 16384 {
		port, err := alloc.Allocate()
		if err != nil {
			t.Fatal(err)
		}
		last = port
	}
	assertOverlayPortExhausted(t, alloc)
	return alloc, last
}

func assertOverlayPortExhausted(t *testing.T, alloc *netio.SourcePortAllocator) {
	t.Helper()
	if _, err := alloc.Allocate(); !errors.Is(err, netio.ErrPortExhausted) {
		t.Fatalf("allocator should remain exhausted: %v", err)
	}
}

func assertOverlayLeaseDispatch(
	t *testing.T, conn netio.OverlayConn, scope bfd.TransportScope, backend *lifecycleOverlayConn,
) {
	t.Helper()
	sender := netio.NewOverlaySender(conn, scope)
	if err := sender.SendPacket(t.Context(), []byte{1}, netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	if backend.scope != scope {
		t.Fatalf("scope lost through lease wrapper: %+v", backend.scope)
	}
}
