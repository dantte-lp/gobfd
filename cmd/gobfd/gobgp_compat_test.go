package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	gobgpapi "github.com/osrg/gobgp/v4/api"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/gobgp"
)

var errGoBGPUnavailable = errors.New("connection refused")

// sequenceChecker returns results in order and repeats the last one.
func sequenceChecker(results ...error) (func(context.Context) error, *atomic.Int32) {
	calls := new(atomic.Int32)
	return func(context.Context) error {
		n := int(calls.Add(1))
		return results[min(n, len(results))-1]
	}, calls
}

func TestWatchGoBGPCompatibilityReportsLateIncompatibleDaemon(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		check, calls := sequenceChecker(
			errGoBGPUnavailable,
			errGoBGPUnavailable,
			fmt.Errorf("probe: %w", gobgp.ErrIncompatibleServer),
		)
		done := make(chan struct{})
		go func() {
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			watchGoBGPCompatibility(t.Context(), check, "127.0.0.1:50051", time.Second, logger)
			close(done)
		}()

		// Probes run at 1s, 3s and 7s of virtual time: backoff 1s, 2s, 4s.
		for _, step := range []struct {
			sleep time.Duration
			calls int
		}{{time.Second, 1}, {2 * time.Second, 2}, {3 * time.Second, 2}, {time.Second, 3}} {
			time.Sleep(step.sleep)
			synctest.Wait()
			if int(calls.Load()) != step.calls {
				t.Fatalf("probe calls = %d, want %d", calls.Load(), step.calls)
			}
		}
		select {
		case <-done:
		default:
			t.Fatal("watch did not stop after a conclusive incompatible result")
		}
		if !strings.Contains(logs.String(), "gobgp endpoint requires GoBGP v4") {
			t.Fatalf("missing incompatibility error log: %s", logs.String())
		}
	})
}

func TestWatchGoBGPCompatibilityStopsAfterSuccess(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		check, calls := sequenceChecker(errGoBGPUnavailable, nil)
		watchGoBGPCompatibility(t.Context(), check, "127.0.0.1:50051", time.Second, slog.New(slog.NewTextHandler(&logs, nil)))
		if calls.Load() != 2 {
			t.Fatalf("probe calls = %d, want 2", calls.Load())
		}
		if !strings.Contains(logs.String(), "gobgp compatibility preflight passed") {
			t.Fatalf("missing success log: %s", logs.String())
		}
	})
}

func TestWatchGoBGPCompatibilityStopsOnCancel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		check, _ := sequenceChecker(errGoBGPUnavailable)
		done := make(chan struct{})
		go func() {
			watchGoBGPCompatibility(ctx, check, "127.0.0.1:50051", time.Second, slog.New(slog.DiscardHandler))
			close(done)
		}()
		time.Sleep(time.Minute)
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("watch did not stop after cancellation")
		}
	})
}

// nativeBFDGoBGP answers GetBgp and lists one neighbor with native BFD enabled.
type nativeBFDGoBGP struct {
	gobgpapi.UnimplementedGoBgpServiceServer
}

func (nativeBFDGoBGP) GetBgp(context.Context, *gobgpapi.GetBgpRequest) (*gobgpapi.GetBgpResponse, error) {
	return &gobgpapi.GetBgpResponse{Global: &gobgpapi.Global{Asn: 65001}}, nil
}

func (nativeBFDGoBGP) ListPeer(
	_ *gobgpapi.ListPeerRequest, stream grpc.ServerStreamingServer[gobgpapi.ListPeerResponse],
) error {
	return stream.Send(&gobgpapi.ListPeerResponse{Peer: &gobgpapi.Peer{
		Conf: &gobgpapi.PeerConf{NeighborAddress: "192.0.2.1"},
		Bfd:  &gobgpapi.BfdPeerConfig{Enabled: true},
	}})
}

func startNativeBFDGoBGP(t *testing.T) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	gobgpapi.RegisterGoBgpServiceServer(server, nativeBFDGoBGP{})
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if serveErr := <-served; serveErr != nil {
			t.Errorf("serve: %v", serveErr)
		}
	})
	return listener.Addr().String()
}

func TestStartGoBGPHandlerRejectsNativeBFDConflict(t *testing.T) {
	t.Parallel()

	_, err := startGoBGPHandler(
		t.Context(),
		new(errgroup.Group),
		config.GoBGPConfig{Enabled: true, Addr: startNativeBFDGoBGP(t), ActionTimeout: 5 * time.Second},
		[]netip.Addr{netip.MustParseAddr("192.0.2.1")},
		nil,
		nil,
		slog.New(slog.DiscardHandler),
	)
	if !errors.Is(err, errNativeBFDConflict) {
		t.Fatalf("startGoBGPHandler() = %v, want errNativeBFDConflict", err)
	}
}

func TestGoBGPPreflightIgnoresUnmanagedNativeBFDPeers(t *testing.T) {
	t.Parallel()

	client, err := gobgp.NewGRPCClient(gobgp.GRPCClientConfig{Addr: startNativeBFDGoBGP(t)}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	})
	preflight := goBGPPreflight(client, []netip.Addr{netip.MustParseAddr("192.0.2.9")})
	if err := preflight(t.Context()); err != nil {
		t.Fatalf("preflight() = %v, want nil for an unmanaged native BFD peer", err)
	}
}

func TestWatchGoBGPCompatibilityReportsNativeBFDConflict(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		check, _ := sequenceChecker(errGoBGPUnavailable, fmt.Errorf("probe: %w", errNativeBFDConflict))
		watchGoBGPCompatibility(t.Context(), check, "127.0.0.1:50051", time.Second, slog.New(slog.NewTextHandler(&logs, nil)))
		if !strings.Contains(logs.String(), "gobgp native BFD conflicts with GoBFD sessions") {
			t.Fatalf("missing native BFD conflict log: %s", logs.String())
		}
	})
}

func TestGoBGPManagedPeersUsesSessionPeers(t *testing.T) {
	t.Parallel()

	got := goBGPManagedPeers([]config.SessionConfig{
		{Peer: "192.0.2.1"}, {Peer: "::ffff:192.0.2.2"}, {Peer: "2001:db8::1"},
	})
	want := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2"), netip.MustParseAddr("2001:db8::1"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("goBGPManagedPeers() = %v, want %v", got, want)
	}
}
