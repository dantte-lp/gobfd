//go:build interop_clab

package interop_clab_test

import (
	"slices"
	"testing"
)

func TestVendorPeersExcludeRetainedUnavailableNOS(t *testing.T) {
	t.Parallel()

	peers, err := vendorPeers()
	if err != nil {
		t.Fatalf("load executable vendor peers: %v", err)
	}
	names := make([]string, len(peers))
	for index, peer := range peers {
		names[index] = peer.name
	}
	if want := []string{"frr", "frr-v6"}; !slices.Equal(names, want) {
		t.Fatalf("executable vendor peers = %v, want %v under retained-unavailable NOS policy", names, want)
	}
	for _, peer := range peers {
		if peer.baseName != "frr" || peer.container != "clab-gobfd-vendors-frr" {
			t.Fatalf("allowed FRR peer selects another runtime target: %+v", peer)
		}
	}
}
