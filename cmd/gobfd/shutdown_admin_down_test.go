package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type shutdownBlockingSender struct {
	entered chan struct{}
	release chan struct{}
}

func (s shutdownBlockingSender) SendPacket(context.Context, []byte, netip.Addr) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return nil
}

func TestGracefulShutdownAdminDownCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logger := slog.New(slog.DiscardHandler)
		mgr := bfd.NewManager(logger)
		ctx, cancel := context.WithCancel(t.Context())
		sess, err := mgr.CreateSession(ctx, shutdownSessionConfig(), candidateTestSenderLeaseFactory())
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		started := time.Now()
		if err := gracefulShutdown(ctx, mgr, logger, nil); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed != 3*time.Second {
			t.Errorf("shutdown duration = %s, want negotiated 3s window", elapsed)
		}
		if sess.State() != bfd.StateAdminDown || sess.LocalDiag() != bfd.DiagAdminDown {
			t.Error("cancelled parent prevented AdminDown commit")
		}
	})
}

func TestGracefulShutdownAdminDownDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logger := slog.New(slog.DiscardHandler)
		mgr := bfd.NewManager(logger)
		sender := shutdownBlockingSender{entered: make(chan struct{}, 1), release: make(chan struct{})}
		ctx, cancel := context.WithCancel(t.Context())
		if _, err := mgr.CreateSession(ctx, shutdownSessionConfig(), bfd.NonOwningSenderLeaseFactory(sender)); err != nil {
			t.Fatal(err)
		}
		<-sender.entered
		cancel()
		result := make(chan error, 1)
		server := &http.Server{}
		stopped := make(chan struct{})
		server.RegisterOnShutdown(func() { close(stopped) })
		go func() { result <- gracefulShutdown(ctx, mgr, logger, nil, server) }()
		synctest.Sleep(shutdownTimeout + time.Second)
		close(sender.release)
		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("blocked owner drain error = %v, want deadline", err)
		}
		<-stopped
	})
}

func shutdownSessionConfig() bfd.SessionConfig {
	return bfd.SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.180"),
		LocalAddr:             netip.MustParseAddr("192.0.2.181"),
		Type:                  bfd.SessionTypeSingleHop,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  time.Second,
		RequiredMinRxInterval: time.Second,
		DetectMultiplier:      3,
	}
}

// A passive session whose peer never supplied a discriminator cannot send
// AdminDown (RFC 5880 Section 6.8.7); an orderly stop warns and succeeds.
func TestGracefulShutdownUnknownPeerDrainWarns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		mgr := bfd.NewManager(logger)
		ctx, cancel := context.WithCancel(t.Context())
		cfg := shutdownSessionConfig()
		cfg.Role = bfd.RolePassive
		if _, err := mgr.CreateSession(ctx, cfg, candidateTestSenderLeaseFactory()); err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := gracefulShutdown(ctx, mgr, logger, nil); err != nil {
			t.Fatalf("unknown peer drain error = %v, want warning only", err)
		}
		if !strings.Contains(logs.String(), "level=WARN") ||
			!strings.Contains(logs.String(), "passive peer discriminator unknown") {
			t.Errorf("logs lack the unknown-peer drain warning:\n%s", logs.String())
		}
	})
}

// A local send failure stays an error even when another drain only lacks a peer.
func TestGracefulShutdownKeepsLocalDrainErrorBesidePeerWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		mgr := bfd.NewManager(logger)
		sender := shutdownBlockingSender{entered: make(chan struct{}, 1), release: make(chan struct{})}
		ctx, cancel := context.WithCancel(t.Context())
		if _, err := mgr.CreateSession(ctx, shutdownSessionConfig(), bfd.NonOwningSenderLeaseFactory(sender)); err != nil {
			t.Fatal(err)
		}
		passive := shutdownSessionConfig()
		passive.Role = bfd.RolePassive
		passive.PeerAddr = netip.MustParseAddr("192.0.2.182")
		if _, err := mgr.CreateSession(ctx, passive, candidateTestSenderLeaseFactory()); err != nil {
			t.Fatal(err)
		}
		<-sender.entered
		cancel()
		result := make(chan error, 1)
		go func() { result <- gracefulShutdown(ctx, mgr, logger, nil) }()
		synctest.Sleep(shutdownTimeout + time.Second)
		close(sender.release)
		err := <-result
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("local drain error = %v, want deadline", err)
		}
		if strings.Contains(err.Error(), "passive peer discriminator unknown") {
			t.Errorf("peer-attributed drain stayed in the error: %v", err)
		}
		if !strings.Contains(logs.String(), "level=WARN") {
			t.Errorf("logs lack the unknown-peer drain warning:\n%s", logs.String())
		}
	})
}
