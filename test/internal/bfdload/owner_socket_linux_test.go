//go:build linux

package bfdload

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/testutil"
)

// Missing either owner socket option must differ from an independently configured UDP socket.
func TestOwnerSocketBufferPolicy(t *testing.T) {
	testutil.LockLoopbackBFD(t)
	for _, echo := range []bool{false, true} {
		t.Run(map[bool]string{false: "Control", true: "Echo"}[echo], func(t *testing.T) {
			cfg := echoFixture(t)
			cfg.Profile.Echo, cfg.Profile.Duration = echo, 10*time.Second
			if !echo {
				for index := range cfg.Sessions {
					endpoint := &cfg.Sessions[index]
					endpoint.Local = netip.AddrPortFrom(endpoint.Local.Addr(), uint16(38401+index))
				}
			}
			owner := socketBufferOwnerFixture(t, cfg)
			socket := config.DefaultConfig().Socket
			wantRead, wantWrite := testutil.ReferenceSocketBuffers(t, socket.ReadBufferSize, socket.WriteBufferSize)
			checkOwnerSocketBuffers(t, owner, wantRead, wantWrite)
			for index := range cfg.Sessions {
				readyCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				if err := awaitChurnRecovery(readyCtx, owner, index, 1); err != nil {
					cancel()
					t.Fatal(err)
				}
				cancel()
				if _, released, err := owner.releaseForChurn(t.Context(), index); err != nil || !released {
					t.Fatalf("release actual socket for churn: released=%t: %v", released, err)
				}
				if _, err := owner.createForChurn(t.Context(), index, cfg.Profile); err != nil {
					t.Fatal(err)
				}
			}
			checkOwnerSocketBuffers(t, owner, wantRead, wantWrite)
		})
	}
}

func socketBufferOwnerFixture(t *testing.T, cfg PeerConfig) *Owner {
	t.Helper()
	owner, err := StartOwner(t.Context(), OwnerConfig{
		Echo: cfg.Profile.Echo, Sessions: cfg.Sessions,
		Interval: cfg.Profile.Interval, DetectMultiplier: cfg.Profile.DetectMultiplier,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := owner.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	peers := make(chan error, 1)
	go func() {
		_, runErr := RunPeers(ctx, cfg, slog.New(slog.DiscardHandler))
		peers <- runErr
	}()
	t.Cleanup(func() {
		cancel()
		if peerErr := <-peers; peerErr != nil && !errors.Is(peerErr, context.Canceled) {
			t.Error(peerErr)
		}
	})
	return owner
}

func checkOwnerSocketBuffers(t *testing.T, owner *Owner, wantRead, wantWrite int) {
	t.Helper()
	observation, err := owner.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range observation.Sessions {
		local := session.Endpoint.Local
		if got := testutil.SocketOptionAt(t, local.Addr(), local.Port(), unix.SOL_SOCKET, unix.SO_RCVBUF); got != wantRead {
			t.Errorf("owner listener %s SO_RCVBUF=%d, want %d", local, got, wantRead)
		}
		got := testutil.SocketOptionAt(t, local.Addr(), session.SourcePort, unix.SOL_SOCKET, unix.SO_SNDBUF)
		if got != wantWrite {
			t.Errorf("owner sender %s:%d SO_SNDBUF=%d, want %d", local.Addr(), session.SourcePort, got, wantWrite)
		}
	}
}
