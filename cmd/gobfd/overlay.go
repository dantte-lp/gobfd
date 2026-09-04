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
	vxlan          netio.OverlayConn
	geneve         netio.OverlayConn
	vxlanSessions  map[bfd.TransportScope]netio.OverlayConn
	geneveSessions map[bfd.TransportScope]netio.OverlayConn
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
		sessions, vxlanConns := createVXLANConns(cfg, sf, logger)
		runtime.vxlanSessions = sessions
		for _, vxlanConn := range vxlanConns {
			conns = append(conns, vxlanConn)
			recv := netio.NewOverlayReceiver(vxlanConn, mgr, logger)
			g.Go(func() error { return recv.Run(ctx) })
		}
		logger.Info("VXLAN overlay receivers started (RFC 8971)",
			slog.Int("listeners", len(vxlanConns)),
			slog.Uint64("management_vni", uint64(cfg.VXLAN.ManagementVNI)),
			slog.String("backend", cfg.VXLAN.Backend))
	}

	// Geneve overlay receiver (RFC 9521, port 6081).
	if cfg.Geneve.Enabled && len(cfg.Geneve.Peers) > 0 {
		sessions, geneveConns := createGeneveConns(cfg, sf, logger)
		runtime.geneveSessions = sessions
		for _, geneveConn := range geneveConns {
			conns = append(conns, geneveConn)
			recv := netio.NewOverlayReceiver(geneveConn, mgr, logger)
			g.Go(func() error { return recv.Run(ctx) })
		}
		logger.Info("Geneve overlay receivers started (RFC 9521)",
			slog.Int("listeners", len(geneveConns)),
			slog.Uint64("default_vni", uint64(cfg.Geneve.DefaultVNI)),
			slog.String("backend", cfg.Geneve.Backend))
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

func createVXLANConns(
	cfg *config.Config,
	sf *udpSenderFactory,
	logger *slog.Logger,
) (map[bfd.TransportScope]netio.OverlayConn, []netio.OverlayConn) {
	return createConfiguredOverlayConns(len(cfg.VXLAN.Peers), func(index int) (bfd.TransportScope, error) {
		return vxlanTransportScope(cfg, cfg.VXLAN.Peers[index])
	}, sf, logger, func(
		localAddr netip.Addr, srcPort uint16, scopes []bfd.TransportScope,
	) (netio.OverlayConn, error) {
		return netio.NewVXLANOverlayBackend(netio.VXLANOverlayBackendConfig{
			Backend: netio.OverlayBackendType(cfg.VXLAN.Backend), LocalAddr: localAddr,
			SourcePort: srcPort, Logger: logger, Scopes: scopes,
		})
	})
}

func createGeneveConns(
	cfg *config.Config,
	sf *udpSenderFactory,
	logger *slog.Logger,
) (map[bfd.TransportScope]netio.OverlayConn, []netio.OverlayConn) {
	return createConfiguredOverlayConns(len(cfg.Geneve.Peers), func(index int) (bfd.TransportScope, error) {
		return geneveTransportScope(cfg, cfg.Geneve.Peers[index])
	}, sf, logger, func(
		localAddr netip.Addr, srcPort uint16, scopes []bfd.TransportScope,
	) (netio.OverlayConn, error) {
		return netio.NewGeneveOverlayBackend(netio.GeneveOverlayBackendConfig{
			Backend: netio.OverlayBackendType(cfg.Geneve.Backend), LocalAddr: localAddr,
			SourcePort: srcPort, Logger: logger, Scopes: scopes,
		})
	})
}

func createConfiguredOverlayConns(
	peerCount int,
	scopeAt func(int) (bfd.TransportScope, error),
	sf *udpSenderFactory,
	logger *slog.Logger,
	build overlayConnBuilder,
) (map[bfd.TransportScope]netio.OverlayConn, []netio.OverlayConn) {
	groups := make(map[netip.Addr][]bfd.TransportScope)
	for index := range peerCount {
		scope, err := scopeAt(index)
		if err != nil {
			logger.Error("invalid overlay identity", slog.Int("peer_index", index), slog.String("error", err.Error()))
			continue
		}
		groups[scope.OuterLocalAddr] = append(groups[scope.OuterLocalAddr], scope)
	}
	return createOverlayConnGroups(groups, sf, logger, build)
}

type overlayConnBuilder func(netip.Addr, uint16, []bfd.TransportScope) (netio.OverlayConn, error)

func createOverlayConnGroups(
	groups map[netip.Addr][]bfd.TransportScope,
	sf *udpSenderFactory,
	logger *slog.Logger,
	build overlayConnBuilder,
) (map[bfd.TransportScope]netio.OverlayConn, []netio.OverlayConn) {
	bySession := make(map[bfd.TransportScope]netio.OverlayConn)
	conns := make([]netio.OverlayConn, 0, len(groups))
	for localAddr, scopes := range groups {
		srcPort, err := sf.portAlloc.Allocate()
		if err != nil {
			logger.Error("allocate overlay inner source port", slog.String("error", err.Error()))
			continue
		}
		conn, err := build(localAddr, srcPort, scopes)
		if err != nil {
			sf.portAlloc.Release(srcPort)
			logger.Error("create overlay listener", slog.String("local", localAddr.String()), slog.String("error", err.Error()))
			continue
		}
		conns = append(conns, conn)
		for _, scope := range scopes {
			bySession[scope] = conn
		}
	}
	return bySession, conns
}

func vxlanTransportScope(
	cfg *config.Config,
	peer config.VXLANPeerConfig,
) (bfd.TransportScope, error) {
	outerPeer, err := peer.PeerAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse peer VTEP address: %w", err)
	}
	outerLocal, err := peer.LocalAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse local VTEP address %q: %w", peer.Local, err)
	}
	if !outerLocal.IsValid() {
		return bfd.TransportScope{}, fmt.Errorf(
			"parse local VTEP address %q: %w", peer.Local, config.ErrInvalidVXLANPeer)
	}
	return bfd.TransportScope{
		Kind: bfd.TransportScopeVXLAN, Owner: bfd.VXLANReconciliationOwner().ID,
		Backend: cfg.VXLAN.Backend, VNI: cfg.VXLAN.ManagementVNI,
		OuterPeerAddr: outerPeer.Unmap(), OuterLocalAddr: outerLocal.Unmap(),
		InnerPeerAddr: outerPeer.Unmap(), InnerLocalAddr: outerLocal.Unmap(),
		AddressFamily: bfd.AddressFamilyIPv4,
		PeerMAC:       netio.VXLANFormatAPeerMAC(), LocalMAC: netio.VXLANFormatALocalMAC(),
	}, nil
}

func geneveTransportScope(
	cfg *config.Config,
	peer config.GenevePeerConfig,
) (bfd.TransportScope, error) {
	outerPeer, err := peer.PeerAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse peer NVE address: %w", err)
	}
	outerLocal, err := peer.LocalAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse local NVE address %q: %w", peer.Local, err)
	}
	if !outerLocal.IsValid() {
		return bfd.TransportScope{}, fmt.Errorf(
			"parse local NVE address %q: %w", peer.Local, config.ErrInvalidGenevePeer)
	}
	innerPeer, err := peer.InnerPeerAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse peer VAP address: %w", err)
	}
	innerLocal, err := peer.InnerLocalAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse local VAP address: %w", err)
	}
	peerMAC, err := peer.PeerMACAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse peer VAP MAC: %w", err)
	}
	localMAC, err := peer.LocalMACAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse local VAP MAC: %w", err)
	}
	return bfd.TransportScope{
		Kind: bfd.TransportScopeGeneve, Owner: bfd.GeneveReconciliationOwner().ID,
		Backend: cfg.Geneve.Backend, VNI: effectiveGeneveVNI(peer, cfg.Geneve.DefaultVNI),
		OuterPeerAddr: outerPeer.Unmap(), OuterLocalAddr: outerLocal.Unmap(),
		InnerPeerAddr: innerPeer, InnerLocalAddr: innerLocal,
		AddressFamily: bfd.AddressFamilyIPv4, PeerMAC: peerMAC, LocalMAC: localMAC,
	}, nil
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
	scope      bfd.TransportScope
	conn       netio.OverlayConn
	scopeErr   error
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

	vxlanEntries := buildVXLANPeerEntries(cfg, rt)
	geneveEntries := buildGenevePeerEntries(cfg, rt)

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

func buildVXLANPeerEntries(cfg *config.Config, rt *overlayRuntime) []overlayPeerEntry {
	if !cfg.VXLAN.Enabled {
		return nil
	}
	entries := make([]overlayPeerEntry, 0, len(cfg.VXLAN.Peers))
	for _, peer := range cfg.VXLAN.Peers {
		scope, err := vxlanTransportScope(cfg, peer)
		conn := rt.vxlanSessions[scope]
		if conn == nil {
			conn = rt.vxlan
		}
		entries = append(entries, overlayPeerEntry{
			key: peer.VXLANSessionKey(), peerName: peer.Peer,
			peerStr: scope.InnerPeerAddr.String(), localStr: scope.InnerLocalAddr.String(),
			peerTx: peer.DesiredMinTx, peerRx: peer.RequiredMinRx,
			peerDetect: peer.DetectMult, scope: scope, conn: conn, scopeErr: err,
		})
	}
	return entries
}

func buildGenevePeerEntries(cfg *config.Config, rt *overlayRuntime) []overlayPeerEntry {
	if !cfg.Geneve.Enabled {
		return nil
	}
	entries := make([]overlayPeerEntry, 0, len(cfg.Geneve.Peers))
	for _, peer := range cfg.Geneve.Peers {
		scope, err := geneveTransportScope(cfg, peer)
		conn := rt.geneveSessions[scope]
		if conn == nil {
			conn = rt.geneve
		}
		entries = append(entries, overlayPeerEntry{
			key: peer.GeneveSessionKey(cfg.Geneve.DefaultVNI), peerName: peer.Peer,
			peerStr: scope.InnerPeerAddr.String(), localStr: scope.InnerLocalAddr.String(),
			peerTx: peer.DesiredMinTx, peerRx: peer.RequiredMinRx,
			peerDetect: peer.DetectMult, scope: scope, conn: conn, scopeErr: err,
		})
	}
	return entries
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
	desired, conns, err := compileOverlaySessionCandidates(params)
	if err != nil {
		logger.Error("invalid overlay candidate, keeping current sessions",
			slog.String("rfc", params.rfc), slog.String("error", err.Error()))
		return
	}
	source := sourceVXLAN
	if params.sessType == bfd.SessionTypeGeneve {
		source = sourceGeneve
	}
	result := applyCompiledOverlay(ctx, mgr, source, compiledOverlayCandidate{
		owner: params.owner, conns: conns, desired: desired,
	})
	logOverlaySourceApplyResult(logger, params.rfc, result)
}

func compileOverlaySessionCandidates(
	params overlayTunnelParams,
) ([]bfd.ReconcileConfig, []netio.OverlayConn, error) {
	desired := make([]bfd.ReconcileConfig, 0, len(params.entries))
	conns := make([]netio.OverlayConn, 0, len(params.entries))
	for _, e := range params.entries {
		if e.scopeErr != nil {
			return nil, nil, fmt.Errorf("%s peer %q identity: %w", params.rfc, e.peerName, e.scopeErr)
		}
		sessCfg, cfgErr := buildOverlaySessionConfig(
			e.peerStr, e.localStr, e.peerTx, e.peerRx, e.peerDetect,
			params.defaults, params.sessType, e.scope)
		if cfgErr != nil {
			return nil, nil, fmt.Errorf("%s peer %q: %w", params.rfc, e.peerName, cfgErr)
		}
		desired = append(desired, bfd.ReconcileConfig{
			Key: e.key, SessionConfig: sessCfg,
		})
		conn := e.conn
		if conn == nil {
			conn = params.conn
		}
		conns = append(conns, conn)
	}
	if err := bfd.ValidateReconcileConfigs(desired); err != nil {
		return nil, nil, fmt.Errorf("validate complete %s session set: %w", params.rfc, err)
	}
	return desired, conns, nil
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
	result := applyOverlayDesiredSessions(ctx, mgr, owner, desired)
	if err := result.Err(); err != nil {
		logger.Error("overlay session reconciliation had errors",
			slog.String("rfc", rfc),
			slog.String("error", err.Error()),
			slog.Int("created", result.Created),
			slog.Int("released", result.Released),
			slog.Int("pending", result.Pending),
			slog.Int("failed", result.Failed),
			slog.Any("error_codes", reconcileErrorCodes(result)),
		)
		return
	}

	logger.Info("overlay session reconciliation complete",
		slog.String("rfc", rfc),
		slog.Int("created", result.Created), slog.Int("released", result.Released))
}

func applyOverlayDesiredSessions(
	ctx context.Context,
	mgr *bfd.Manager,
	owner bfd.SessionOwner,
	desired []bfd.ReconcileConfig,
) bfd.ReconcileResult {
	return mgr.ReconcileSessionsForOwnerDetailed(ctx, owner, desired)
}

func logOverlaySourceApplyResult(
	logger *slog.Logger,
	rfc string,
	result sourceApplyResult,
) {
	if result.Err != nil {
		logger.Error("overlay session reconciliation had errors",
			slog.String("rfc", rfc),
			slog.String("error", result.Err.Error()),
			slog.Int("created", result.Created),
			slog.Int("released", result.Released),
			slog.Int("pending", result.Pending),
			slog.Int("failed", result.Failed),
			slog.Any("error_codes", sourceApplyErrorCodes(result)),
		)
		return
	}
	logger.Info("overlay session reconciliation complete",
		slog.String("rfc", rfc),
		slog.Int("created", result.Created),
		slog.Int("released", result.Released),
	)
}

func sourceApplyErrorCodes(result sourceApplyResult) []string {
	codes := make([]string, 0, result.Failed)
	for code := bfd.ReconcileErrorLifecycle; code <= bfd.ReconcileErrorCleanup; code++ {
		for range result.Errors.Count(code) {
			codes = append(codes, code.String())
		}
	}
	return codes
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
	transportScopes ...bfd.TransportScope,
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

	var transportScope bfd.TransportScope
	if len(transportScopes) > 0 {
		transportScope = transportScopes[0]
	}
	return bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Type:                  sessType,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      uint8(detectMult),
		TransportScope:        transportScope,
	}, nil
}

// newLoggerWithLevel creates a structured logger using a shared LevelVar
// for dynamic log level changes via SIGHUP reload.
