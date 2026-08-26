package interopcheck

import (
	"strings"
	"testing"
)

func TestPeerStateAndDetectionGap(t *testing.T) {
	t.Parallel()

	t.Run("GoBGP numeric session state", func(t *testing.T) {
		t.Parallel()
		input := []byte(`[{"state":{"neighbor_address":"172.21.0.20","session_state":6}}]`)
		got, err := GoBGPNeighborState(input, "172.21.0.20")
		if err != nil {
			t.Fatalf("GoBGPNeighborState: %v", err)
		}
		if got != "established" {
			t.Fatalf("GoBGPNeighborState = %q, want established", got)
		}
	})

	t.Run("FRR warning wrapped array", func(t *testing.T) {
		t.Parallel()
		input := []byte("% warning\n[{\"peer\":\"172.20.0.10\",\"status\":\"up\"}]\n% suffix\n")
		got, err := FRRBFDPeerStatus(input, "172.20.0.10")
		if err != nil {
			t.Fatalf("FRRBFDPeerStatus: %v", err)
		}
		if got != "up" {
			t.Fatalf("FRRBFDPeerStatus = %q, want up", got)
		}
	})

	t.Run("last packet before Down", func(t *testing.T) {
		t.Parallel()
		got, err := DetectionGap(strings.NewReader("10.000\n11.250\n12.500\n"), 12, 3)
		if err != nil {
			t.Fatalf("DetectionGap: %v", err)
		}
		if got.Status != StatusPass || got.Gap != 0.75 {
			t.Fatalf("DetectionGap = %+v, want pass gap 0.75", got)
		}
	})
}

func TestPeerStateAndDetectionGapRejectInvalidInput(t *testing.T) {
	t.Parallel()

	if _, err := GoBGPNeighborState([]byte(`[] {}`), "172.21.0.20"); err == nil {
		t.Fatal("GoBGPNeighborState accepted multiple JSON documents")
	}
	if _, err := FRRBFDPeerStatus([]byte(`[]`), "172.20.0.10"); err == nil {
		t.Fatal("FRRBFDPeerStatus accepted a missing peer")
	}
	if _, err := DetectionGap(strings.NewReader("not-an-epoch\n"), 12, 3); err == nil {
		t.Fatal("DetectionGap accepted a malformed epoch")
	}
}
