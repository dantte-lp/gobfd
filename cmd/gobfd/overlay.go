package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// Overlay Tunnel Wiring — VXLAN (RFC 8971) + Geneve (RFC 9521)
// -------------------------------------------------------------------------

type overlayRuntime struct {
	vxlan  netio.OverlayConn
	geneve netio.OverlayConn
}

// startOverlayReceivers creates overlay tunnel connections and starts
// OverlayReceiver goroutines in the errgroup. Returns a cleanup function
// that closes all overlay connections when called.
//
// For each enabled tunnel type (VXLAN/Geneve), one UDP socket is bound
// (port 4789 / 6081) and a dedicated receiver goroutine reads packets,
// strips encapsulation, and delivers inner BFD payloads to the Manager.
func startOverlayReceivers(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) (*overlayRuntime, func()) {
	runtime := &overlayRuntime{}
	var conns []netio.OverlayConn

	// VXLAN overlay receiver (RFC 8971, port 4789).
	if cfg.VXLAN.Enabled && len(cfg.VXLAN.Peers) > 0 {
		vxlanConn, err := createVXLANConn(cfg, sf, logger)
		if err != nil {
			logger.Error("failed to create VXLAN connection, skipping overlay",
				slog.String("error", err.Error()),
			)
		} else {
			runtime.vxlan = vxlanConn
			conns = append(conns, vxlanConn)
			recv := netio.NewOverlayReceiver(vxlanConn, mgr, logger)
			g.Go(func() error { return recv.Run(ctx) })
			logger.Info("VXLAN overlay receiver started (RFC 8971)",
				slog.Uint64("management_vni", uint64(cfg.VXLAN.ManagementVNI)),
				slog.String("backend", cfg.VXLAN.Backend),
			)
		}
	}

	// Geneve overlay receiver (RFC 9521, port 6081).
	if cfg.Geneve.Enabled && len(cfg.Geneve.Peers) > 0 {
		geneveConn, err := createGeneveConn(cfg, sf, logger)
		if err != nil {
			logger.Error("failed to create Geneve connection, skipping overlay",
				slog.String("error", err.Error()),
			)
		} else {
			runtime.geneve = geneveConn
			conns = append(conns, geneveConn)
			recv := netio.NewOverlayReceiver(geneveConn, mgr, logger)
			g.Go(func() error { return recv.Run(ctx) })
			logger.Info("Geneve overlay receiver started (RFC 9521)",
				slog.Uint64("default_vni", uint64(cfg.Geneve.DefaultVNI)),
				slog.String("backend", cfg.Geneve.Backend),
			)
		}
	}

	return runtime, func() {
		for _, c := range conns {
			if err := c.Close(); err != nil {
				logger.Warn("failed to close overlay connection",
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

// createVXLANConn creates a VXLANConn bound to the first VXLAN peer's local address.
// All VXLAN peers share a single UDP socket on port 4789.
func createVXLANConn(
	cfg *config.Config,
	sf *udpSenderFactory,
	logger *slog.Logger,
) (netio.OverlayConn, error) {
	// Use the first peer's local address for binding the socket.
	localAddr, err := cfg.VXLAN.Peers[0].LocalAddr()
	if err != nil || !localAddr.IsValid() {
		return nil, fmt.Errorf("vxlan: first peer has invalid local address: %w", err)
	}

	// Allocate an ephemeral source port for the inner UDP header.
	srcPort, err := sf.portAlloc.Allocate()
	if err != nil {
		return nil, fmt.Errorf("vxlan: allocate inner src port: %w", err)
	}

	conn, err := netio.NewVXLANOverlayBackend(netio.VXLANOverlayBackendConfig{
		Backend:       netio.OverlayBackendType(cfg.VXLAN.Backend),
		LocalAddr:     localAddr,
		ManagementVNI: cfg.VXLAN.ManagementVNI,
		SourcePort:    srcPort,
		Logger:        logger,
	})
	if err != nil {
		sf.portAlloc.Release(srcPort)
		return nil, fmt.Errorf("vxlan: create conn: %w", err)
	}

	return conn, nil
}

// createGeneveConn creates a GeneveConn bound to the first Geneve peer's local address.
// All Geneve peers share a single UDP socket on port 6081.
func createGeneveConn(
	cfg *config.Config,
	sf *udpSenderFactory,
	logger *slog.Logger,
) (netio.OverlayConn, error) {
	localAddr, err := cfg.Geneve.Peers[0].LocalAddr()
	if err != nil || !localAddr.IsValid() {
		return nil, fmt.Errorf("geneve: first peer has invalid local address: %w", err)
	}

	// Resolve VNI: first peer's VNI, falling back to default.
	vni := cfg.Geneve.Peers[0].VNI
	if vni == 0 {
		vni = cfg.Geneve.DefaultVNI
	}

	srcPort, err := sf.portAlloc.Allocate()
	if err != nil {
		return nil, fmt.Errorf("geneve: allocate inner src port: %w", err)
	}

	conn, err := netio.NewGeneveOverlayBackend(netio.GeneveOverlayBackendConfig{
		Backend:    netio.OverlayBackendType(cfg.Geneve.Backend),
		LocalAddr:  localAddr,
		VNI:        vni,
		SourcePort: srcPort,
		Logger:     logger,
	})
	if err != nil {
		sf.portAlloc.Release(srcPort)
		return nil, fmt.Errorf("geneve: create conn: %w", err)
	}

	return conn, nil
}

// overlayPeerEntry holds the raw per-peer data before conversion to
// bfd.SessionConfig. Used by the common reconcileOverlayTunnel helper.
type overlayPeerEntry struct {
	key        string
	peerName   string
	peerStr    string
	localStr   string
	peerTx     time.Duration
	peerRx     time.Duration
	peerDetect uint32
}

// overlayTimerDefaults holds the default timer values shared by VXLAN and Geneve
// overlay config. Extracted to deduplicate configVXLANToBFD/configGeneveToBFD.
type overlayTimerDefaults struct {
	desiredMinTx  time.Duration
	requiredMinRx time.Duration
	detectMult    uint32
}

// reconcileOverlayTunnels reconciles both VXLAN (RFC 8971) and Geneve (RFC 9521)
// BFD sessions from the configuration. Each enabled tunnel type is processed
// through the shared reconcileOverlayTunnel path.
func reconcileOverlayTunnels(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	overlayRuntime *overlayRuntime,
	logger *slog.Logger,
) {
	for _, tp := range buildOverlayTunnelParams(cfg, overlayRuntime) {
		reconcileOverlayTunnel(ctx, mgr, logger, tp)
	}
}

// buildOverlayTunnelParams builds one source-scoped candidate for each overlay
// type, including an empty candidate when that source is disabled.
func buildOverlayTunnelParams(
	cfg *config.Config,
	rt *overlayRuntime,
) []overlayTunnelParams {
	if rt == nil {
		rt = &overlayRuntime{}
	}

	vxlanEntries := make([]overlayPeerEntry, 0, len(cfg.VXLAN.Peers))
	if cfg.VXLAN.Enabled {
		for _, peer := range cfg.VXLAN.Peers {
			vxlanEntries = append(vxlanEntries, overlayPeerEntry{
				key: peer.VXLANSessionKey(), peerName: peer.Peer,
				peerStr: peer.Peer, localStr: peer.Local,
				peerTx: peer.DesiredMinTx, peerRx: peer.RequiredMinRx,
				peerDetect: peer.DetectMult,
			})
		}
	}

	geneveEntries := make([]overlayPeerEntry, 0, len(cfg.Geneve.Peers))
	if cfg.Geneve.Enabled {
		for _, peer := range cfg.Geneve.Peers {
			geneveEntries = append(geneveEntries, overlayPeerEntry{
				key: peer.GeneveSessionKey(), peerName: peer.Peer,
				peerStr: peer.Peer, localStr: peer.Local,
				peerTx: peer.DesiredMinTx, peerRx: peer.RequiredMinRx,
				peerDetect: peer.DetectMult,
			})
		}
	}

	return []overlayTunnelParams{
		{
			rfc: "RFC 8971", sessType: bfd.SessionTypeVXLAN,
			owner: bfd.VXLANReconciliationOwner(),
			defaults: overlayTimerDefaults{
				desiredMinTx:  cfg.VXLAN.DefaultDesiredMinTx,
				requiredMinRx: cfg.VXLAN.DefaultRequiredMinRx,
				detectMult:    cfg.VXLAN.DefaultDetectMultiplier,
			},
			conn: rt.vxlan, entries: vxlanEntries,
		},
		{
			rfc: "RFC 9521", sessType: bfd.SessionTypeGeneve,
			owner: bfd.GeneveReconciliationOwner(),
			defaults: overlayTimerDefaults{
				desiredMinTx:  cfg.Geneve.DefaultDesiredMinTx,
				requiredMinRx: cfg.Geneve.DefaultRequiredMinRx,
				detectMult:    cfg.Geneve.DefaultDetectMultiplier,
			},
			conn: rt.geneve, entries: geneveEntries,
		},
	}
}

// overlayTunnelParams holds the parameters for reconcileOverlayTunnel,
// capturing the differences between VXLAN and Geneve reconciliation.
type overlayTunnelParams struct {
	rfc      string
	sessType bfd.SessionType
	owner    bfd.SessionOwner
	defaults overlayTimerDefaults
	conn     netio.OverlayConn
	entries  []overlayPeerEntry
}

// reconcileOverlayTunnel is the shared implementation for overlay BFD session
// reconciliation. It reuses the running tunnel backend, converts peer entries
// to session configs, and calls mgr.ReconcileSessions.
func reconcileOverlayTunnel(
	ctx context.Context,
	mgr *bfd.Manager,
	logger *slog.Logger,
	params overlayTunnelParams,
) {
	if len(params.entries) == 0 {
		reconcileOverlaySessions(ctx, mgr, params.owner, nil, params.rfc, logger)
		return
	}
	if params.conn == nil {
		logger.Error("overlay backend is not running, skipping reconciliation",
			slog.String("rfc", params.rfc))
		return
	}

	desired, err := compileOverlaySessionCandidates(params)
	if err != nil {
		logger.Error("invalid overlay candidate, keeping current sessions",
			slog.String("rfc", params.rfc), slog.String("error", err.Error()))
		return
	}
	sender := netio.NewOverlaySender(params.conn)
	for i := range desired {
		desired[i].Sender = sender
	}
	reconcileOverlaySessions(ctx, mgr, params.owner, desired, params.rfc, logger)
}

func compileOverlaySessionCandidates(params overlayTunnelParams) ([]bfd.ReconcileConfig, error) {
	desired := make([]bfd.ReconcileConfig, 0, len(params.entries))
	for _, e := range params.entries {
		sessCfg, cfgErr := buildOverlaySessionConfig(
			e.peerStr, e.localStr, e.peerTx, e.peerRx, e.peerDetect,
			params.defaults, params.sessType)
		if cfgErr != nil {
			return nil, fmt.Errorf("%s peer %q: %w", params.rfc, e.peerName, cfgErr)
		}
		desired = append(desired, bfd.ReconcileConfig{
			Key: e.key, SessionConfig: sessCfg,
		})
	}
	if err := bfd.ValidateReconcileConfigs(desired); err != nil {
		return nil, fmt.Errorf("validate complete %s session set: %w", params.rfc, err)
	}
	return desired, nil
}

// reconcileOverlaySessions performs the common reconciliation loop for overlay
// BFD sessions (VXLAN or Geneve), calling mgr.ReconcileSessions with the
// pre-built desired set.
func reconcileOverlaySessions(
	ctx context.Context,
	mgr *bfd.Manager,
	owner bfd.SessionOwner,
	desired []bfd.ReconcileConfig,
	rfc string,
	logger *slog.Logger,
) {
	created, destroyed, err := mgr.ReconcileSessionsForOwner(ctx, owner, desired)
	if err != nil {
		logger.Error("overlay session reconciliation had errors",
			slog.String("rfc", rfc), slog.String("error", err.Error()))
	}

	logger.Info("overlay session reconciliation complete",
		slog.String("rfc", rfc),
		slog.Int("created", created), slog.Int("destroyed", destroyed))
}

// buildOverlaySessionConfig converts per-peer overlay fields (address strings,
// timer overrides) into a bfd.SessionConfig, applying defaults from
// overlayTimerDefaults. Shared by VXLAN and Geneve config paths.
func buildOverlaySessionConfig(
	peerStr, localStr string,
	peerTx, peerRx time.Duration,
	peerDetect uint32,
	defaults overlayTimerDefaults,
	sessType bfd.SessionType,
) (bfd.SessionConfig, error) {
	peerAddr, err := netip.ParseAddr(peerStr)
	if err != nil {
		return bfd.SessionConfig{}, fmt.Errorf("parse peer address %q: %w", peerStr, err)
	}

	var localAddr netip.Addr
	if localStr != "" {
		localAddr, err = netip.ParseAddr(localStr)
		if err != nil {
			return bfd.SessionConfig{}, fmt.Errorf("parse local address %q: %w", localStr, err)
		}
	}

	desiredMinTx := peerTx
	if desiredMinTx == 0 {
		desiredMinTx = defaults.desiredMinTx
	}
	requiredMinRx := peerRx
	if requiredMinRx == 0 {
		requiredMinRx = defaults.requiredMinRx
	}
	detectMult := peerDetect
	if detectMult == 0 {
		detectMult = defaults.detectMult
	}
	if detectMult > maxBFDWireUint8 {
		return bfd.SessionConfig{}, fmt.Errorf("detect_mult %d: %w", detectMult, errDetectMultOverflow)
	}

	return bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Type:                  sessType,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(detectMult),
	}, nil
}

// newLoggerWithLevel creates a structured logger using a shared LevelVar
// for dynamic log level changes via SIGHUP reload.
