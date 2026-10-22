package netio

import (
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

func scopedTestGeneveScopes(local, peer netip.Addr) (bfd.TransportScope, bfd.TransportScope) {
	first := bfd.TransportScope{
		Kind: bfd.TransportScopeGeneve, Owner: "geneve-config", Backend: "userspace-udp", VNI: 100,
		OuterPeerAddr: peer, OuterLocalAddr: local,
		InnerPeerAddr: netip.MustParseAddr("192.0.2.1"), InnerLocalAddr: netip.MustParseAddr("192.0.2.2"),
		AddressFamily: bfd.AddressFamilyIPv4,
		PeerMAC:       [6]byte{0x02, 0, 0, 0, 0, 1}, LocalMAC: [6]byte{0x02, 0, 0, 0, 0, 2},
	}
	second := first
	second.VNI = 200
	second.InnerPeerAddr = netip.MustParseAddr("192.0.2.3")
	second.InnerLocalAddr = netip.MustParseAddr("192.0.2.4")
	return first, second
}

// SetScopes widens and narrows the identities of a running scoped socket
// without replacing it.
func TestGeneveConnSetScopesWidensAndNarrowsInPlace(t *testing.T) {
	local, peer := netip.MustParseAddr("127.0.55.1"), netip.MustParseAddr("127.0.55.2")
	first, second := scopedTestGeneveScopes(local, peer)
	conn, err := NewGeneveConnForScopes(local, 49152, []bfd.TransportScope{first}, slog.New(slog.DiscardHandler))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES) {
			t.Skipf("Geneve loopback socket unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if deadlineErr := conn.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
		t.Fatal(deadlineErr)
	}
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: peer.AsSlice()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sender.Close() })
	expect := func(scope bfd.TransportScope, accepted bool) {
		t.Helper()
		sendScopedGenevePacket(t, sender, local, scope)
		_, meta, recvErr := conn.RecvDecapsulated(t.Context())
		switch {
		case accepted && (recvErr != nil || meta.TransportScope != scope):
			t.Fatalf("scope VNI %d: meta=%+v err=%v, want accepted", scope.VNI, meta, recvErr)
		case !accepted && !errors.Is(recvErr, ErrOverlayIdentityMismatch):
			t.Fatalf("scope VNI %d: err=%v, want ErrOverlayIdentityMismatch", scope.VNI, recvErr)
		}
	}

	expect(second, false)
	if err := conn.SetScopes([]bfd.TransportScope{first, second}); err != nil {
		t.Fatal(err)
	}
	expect(second, true)
	expect(first, true)
	if err := conn.SetScopes([]bfd.TransportScope{second}); err != nil {
		t.Fatal(err)
	}
	expect(first, false)
	if err := conn.SendEncapsulatedFor(t.Context(), make([]byte, 24), first); !errors.Is(err, ErrOverlayIdentityMismatch) {
		t.Fatalf("send for removed scope = %v, want ErrOverlayIdentityMismatch", err)
	}
	if err := conn.SendEncapsulatedFor(t.Context(), make([]byte, 24), second); err != nil {
		t.Fatalf("send for kept scope: %v", err)
	}
}

func TestOverlayConnSetScopesRejectsInvalidSets(t *testing.T) {
	local, peer := netip.MustParseAddr("127.0.55.3"), netip.MustParseAddr("127.0.55.4")
	first, _ := scopedTestGeneveScopes(local, peer)
	conn, err := NewGeneveConnForScopes(local, 49152, []bfd.TransportScope{first}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Skipf("Geneve loopback socket unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	foreign := first
	foreign.OuterLocalAddr = netip.MustParseAddr("127.0.55.9")
	for name, scopes := range map[string][]bfd.TransportScope{
		"empty": nil, "foreign endpoint": {foreign}, "duplicate": {first, first},
	} {
		if setErr := conn.SetScopes(scopes); !errors.Is(setErr, ErrOverlayIdentityMismatch) {
			t.Errorf("%s: SetScopes = %v, want ErrOverlayIdentityMismatch", name, setErr)
		}
	}
	unscoped, err := NewVXLANConn(netip.MustParseAddr("127.0.55.5"), 7, 49153, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Skipf("VXLAN loopback socket unavailable: %v", err)
	}
	t.Cleanup(func() { _ = unscoped.Close() })
	if err := unscoped.SetScopes([]bfd.TransportScope{first}); !errors.Is(err, ErrOverlayIdentityMismatch) {
		t.Fatalf("unscoped SetScopes = %v, want ErrOverlayIdentityMismatch", err)
	}
}

// Run with -race: identity lookups on the send and receive paths read the
// set with one atomic load while SetScopes replaces it.
func TestOverlayConnSetScopesRacesSendAndDecap(t *testing.T) {
	local, peer := netip.MustParseAddr("127.0.55.6"), netip.MustParseAddr("127.0.55.7")
	first, second := scopedTestGeneveScopes(local, peer)
	conn, err := NewGeneveConnForScopes(local, 49152, []bfd.TransportScope{first}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Skipf("Geneve loopback socket unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 500 {
			scopes := []bfd.TransportScope{first}
			if i%2 == 0 {
				scopes = append(scopes, second)
			}
			if err := conn.SetScopes(scopes); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Go(func() {
		for range 500 {
			_ = conn.SendEncapsulatedFor(t.Context(), make([]byte, 24), second)
		}
	})
	wg.Wait()
}

// VXLAN receive identity ignores the local MAC (the inner destination MAC is
// fixed), so two scopes that differ only there share one wire identity.
func TestSameOverlayWireIdentity(t *testing.T) {
	t.Parallel()

	local, peer := netip.MustParseAddr("198.51.100.1"), netip.MustParseAddr("198.51.100.2")
	geneve, other := scopedTestGeneveScopes(local, peer)
	vxlan := geneve
	vxlan.Kind = bfd.TransportScopeVXLAN
	vxlanMAC := vxlan
	vxlanMAC.LocalMAC[5]++
	geneveMAC := geneve
	geneveMAC.LocalMAC[5]++
	for _, tc := range []struct {
		a, b bfd.TransportScope
		want bool
	}{
		{vxlan, vxlanMAC, true},
		{geneve, geneveMAC, false},
		{geneve, other, false},
	} {
		if got := SameOverlayWireIdentity(tc.a, tc.b); got != tc.want {
			t.Errorf("SameOverlayWireIdentity(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
