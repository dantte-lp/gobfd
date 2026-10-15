package gobgp

import (
	"net/netip"
	"slices"
	"testing"

	gobgpapi "github.com/osrg/gobgp/v4/api"
	"google.golang.org/grpc"
)

// peerListService streams a fixed GoBGP v4 neighbor list.
type peerListService struct {
	gobgpapi.UnimplementedGoBgpServiceServer

	peers []*gobgpapi.Peer
}

func (s peerListService) ListPeer(
	_ *gobgpapi.ListPeerRequest, stream grpc.ServerStreamingServer[gobgpapi.ListPeerResponse],
) error {
	for _, peer := range s.peers {
		if err := stream.Send(&gobgpapi.ListPeerResponse{Peer: peer}); err != nil {
			return err
		}
	}
	return nil
}

func gobgpPeer(address string, nativeBFD bool) *gobgpapi.Peer {
	return &gobgpapi.Peer{
		Conf: &gobgpapi.PeerConf{NeighborAddress: address},
		Bfd:  &gobgpapi.BfdPeerConfig{Enabled: nativeBFD},
	}
}

func TestNativeBFDPeersReturnsOnlyEnabledNeighbors(t *testing.T) {
	t.Parallel()

	addr := startPreflightServer(t, func(s *grpc.Server) {
		gobgpapi.RegisterGoBgpServiceServer(s, peerListService{peers: []*gobgpapi.Peer{
			gobgpPeer("192.0.2.1", true),
			gobgpPeer("192.0.2.2", false),
			{Conf: &gobgpapi.PeerConf{NeighborAddress: "2001:db8::1"}},
			gobgpPeer("2001:db8::2", true),
		}})
	})
	got, err := newPreflightClient(t, addr).NativeBFDPeers(t.Context())
	if err != nil {
		t.Fatalf("NativeBFDPeers() error = %v", err)
	}
	want := []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("2001:db8::2")}
	if !slices.Equal(got, want) {
		t.Fatalf("NativeBFDPeers() = %v, want %v", got, want)
	}
}

func TestNativeBFDPeersRejectsInvalidNeighborAddress(t *testing.T) {
	t.Parallel()

	addr := startPreflightServer(t, func(s *grpc.Server) {
		gobgpapi.RegisterGoBgpServiceServer(s, peerListService{peers: []*gobgpapi.Peer{gobgpPeer("not-an-ip", true)}})
	})
	if _, err := newPreflightClient(t, addr).NativeBFDPeers(t.Context()); err == nil {
		t.Fatal("NativeBFDPeers() error = nil, want invalid neighbor address error")
	}
}

// .8.2.4.1: GoBFD reads a neighbor's administrative state before disabling
// it, so it can leave operator-disabled peers alone.
func TestPeerAdminDown(t *testing.T) {
	t.Parallel()

	down := gobgpPeer("192.0.2.1", false)
	down.State = &gobgpapi.PeerState{AdminState: gobgpapi.PeerState_ADMIN_STATE_DOWN}
	up := gobgpPeer("192.0.2.2", false)
	up.State = &gobgpapi.PeerState{AdminState: gobgpapi.PeerState_ADMIN_STATE_UP}
	addr := startPreflightServer(t, func(s *grpc.Server) {
		gobgpapi.RegisterGoBgpServiceServer(s, peerListService{peers: []*gobgpapi.Peer{down, up}})
	})
	client := newPreflightClient(t, addr)
	for _, tc := range []struct {
		peer    string
		want    bool
		wantErr bool
	}{
		{"192.0.2.1", true, false},
		{"192.0.2.2", false, false},
		{"192.0.2.3", false, true},
	} {
		got, err := client.PeerAdminDown(t.Context(), tc.peer)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("PeerAdminDown(%s) = %t, %v; want %t, error %t", tc.peer, got, err, tc.want, tc.wantErr)
		}
	}
}
