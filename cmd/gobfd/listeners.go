package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"

	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// BFD Listeners — receive incoming BFD Control packets
// -------------------------------------------------------------------------

// startListenersAndReceivers creates all BFD listeners and starts receiver
// goroutines in the errgroup. Returns a cleanup function that closes all
// listeners when called.
func startListenersAndReceivers(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	mgr *bfd.Manager,
	logger *slog.Logger,
) (func(), error) {
	//nolint:contextcheck // Listener socket creation uses context.Background() internally.
	lnResult, err := createListeners(cfg, logger)
	if err != nil {
		return nil, err
	}

	cleanup := func() {
		closeListeners(lnResult.control, logger)
		closeListeners(lnResult.echo, logger)
		closeListeners(lnResult.microBFD, logger)
	}

	if len(lnResult.control) > 0 {
		recv := netio.NewReceiver(mgr, logger)
		g.Go(func() error { return recv.Run(ctx, lnResult.control...) })
	}

	// Start echo receiver for RFC 9747 echo packets (port 3785).
	if len(lnResult.echo) > 0 {
		echoRecv := netio.NewEchoReceiver(mgr, logger)
		g.Go(func() error { return echoRecv.Run(ctx, lnResult.echo...) })
	}

	// Start micro-BFD receiver for RFC 7130 packets (port 6784).
	// Micro-BFD uses the same BFD Control packet format as single-hop,
	// so we reuse the standard Receiver with the micro-BFD listeners.
	if len(lnResult.microBFD) > 0 {
		microRecv := netio.NewReceiver(mgr, logger)
		g.Go(func() error { return microRecv.Run(ctx, lnResult.microBFD...) })
	}

	return cleanup, nil
}

// listenersResult holds the listeners created by createListeners, separated
// by type (control, echo, micro-BFD) for independent receiver goroutines.
type listenersResult struct {
	control  []*netio.Listener
	echo     []*netio.Listener
	microBFD []*netio.Listener
}

// createListeners inspects the declared sessions and echo peers and creates
// the necessary BFD packet listeners. For each unique (localAddr, type) pair
// a single listener is created on the appropriate port (3784 for single-hop,
// 4784 for multi-hop, 3785 for echo). Returns the listeners and any error.
func createListeners(cfg *config.Config, logger *slog.Logger) (listenersResult, error) {
	var result listenersResult

	controlListeners, err := createControlListeners(cfg, logger)
	if err != nil {
		return listenersResult{}, err
	}
	result.control = controlListeners

	// Create echo listeners (port 3785) if echo is enabled with peers.
	if cfg.Echo.Enabled && len(cfg.Echo.Peers) > 0 {
		echoListeners, err := createEchoListeners(cfg, logger)
		if err != nil {
			closeListeners(result.control, logger)
			return listenersResult{}, fmt.Errorf("create echo listeners: %w", err)
		}
		result.echo = echoListeners
	}

	// Create micro-BFD listeners (port 6784) for each member link (RFC 7130).
	if len(cfg.MicroBFD.Groups) > 0 {
		microListeners, err := createMicroBFDListeners(cfg, logger)
		if err != nil {
			closeListeners(result.control, logger)
			closeListeners(result.echo, logger)
			return listenersResult{}, fmt.Errorf("create micro-BFD listeners: %w", err)
		}
		result.microBFD = microListeners
	}

	return result, nil
}

// createControlListeners creates BFD Control packet listeners on port 3784
// (single-hop) and 4784 (multi-hop) for each unique (localAddr, type) pair.
func createControlListeners(cfg *config.Config, logger *slog.Logger) ([]*netio.Listener, error) {
	type listenerKey struct {
		addr     netip.Addr
		multiHop bool
	}

	seen := make(map[listenerKey]struct{})
	var listeners []*netio.Listener

	for _, sc := range cfg.Sessions {
		localAddr, err := sc.LocalAddr()
		if err != nil || !localAddr.IsValid() {
			continue
		}

		multiHop := sc.Type == "multi_hop"
		key := listenerKey{addr: localAddr, multiHop: multiHop}

		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		lnCfg := netio.ListenerConfig{
			Addr:     localAddr,
			IfName:   sc.Interface,
			MultiHop: multiHop,
		}
		if multiHop {
			lnCfg.Port = netio.PortMultiHop
		} else {
			lnCfg.Port = netio.PortSingleHop
		}

		ln, err := netio.NewListener(lnCfg)
		if err != nil {
			closeListeners(listeners, logger)
			return nil, fmt.Errorf("create listener on %s (multihop=%v): %w", localAddr, multiHop, err)
		}

		logger.Info("BFD listener started",
			slog.String("addr", localAddr.String()),
			slog.Bool("multi_hop", multiHop),
			slog.String("interface", sc.Interface),
		)

		listeners = append(listeners, ln)
	}

	return listeners, nil
}

// createEchoListeners creates listeners on port 3785 for echo packet
// reception. One listener per unique local address.
func createEchoListeners(cfg *config.Config, logger *slog.Logger) ([]*netio.Listener, error) {
	type echoKey struct {
		addr netip.Addr
	}

	seen := make(map[echoKey]struct{})
	var listeners []*netio.Listener

	for _, ep := range cfg.Echo.Peers {
		localAddr, err := ep.LocalAddr()
		if err != nil || !localAddr.IsValid() {
			continue
		}

		key := echoKey{addr: localAddr}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		lnCfg := netio.ListenerConfig{
			Addr:        localAddr,
			IfName:      ep.Interface,
			Port:        netio.PortEcho,
			MultiHop:    false,
			ExpectedTTL: netio.TTLEchoLoopback,
		}

		ln, err := netio.NewListener(lnCfg)
		if err != nil {
			closeListeners(listeners, logger)
			return nil, fmt.Errorf("create echo listener on %s: %w", localAddr, err)
		}

		logger.Info("BFD echo listener started",
			slog.String("addr", localAddr.String()),
			slog.String("interface", ep.Interface),
		)

		listeners = append(listeners, ln)
	}

	return listeners, nil
}

// closeListeners closes all provided listeners, logging any errors.
func closeListeners(listeners []*netio.Listener, logger *slog.Logger) {
	for _, ln := range listeners {
		if err := ln.Close(); err != nil {
			logger.Warn("failed to close BFD listener",
				slog.String("error", err.Error()),
			)
		}
	}
}
