//go:build interop_bgp || interop_bgp_testcontainers

package interop_bgp_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
)

func TestGoBGPRouteObservationRejectsCanceledQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p")
	var config net.ListenConfig
	listener, listenErr := config.Listen(t.Context(), "unix", path)
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	t.Setenv("PODMAN_HOST", "unix://"+path)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	exists, err := gobgpRouteExists(ctx, "10.20.0.0/24")
	if !errors.Is(err, context.Canceled) || exists {
		t.Fatalf("failed RIB query became route absence: exists=%t error=%v", exists, err)
	}
}
