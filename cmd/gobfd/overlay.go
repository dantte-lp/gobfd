package main

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// Overlay Tunnel Wiring — VXLAN (RFC 8971) + Geneve (RFC 9521)
// -------------------------------------------------------------------------

// leasedOverlayConn owns the listener's inner source port. Session senders
// borrow this connection and never release its lease.
type leasedOverlayConn struct {
	netio.OverlayConn

	portAlloc *netio.SourcePortAllocator
	port      uint16
	closeOnce sync.Once
	closeErr  error
}

func (c *leasedOverlayConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.OverlayConn.Close()
		c.portAlloc.Release(c.port)
	})
	return c.closeErr
}

func (c *leasedOverlayConn) SendEncapsulatedFor(
	ctx context.Context, payload []byte, scope bfd.TransportScope,
) error {
	conn, ok := c.OverlayConn.(interface {
		SendEncapsulatedFor(ctx context.Context, payload []byte, scope bfd.TransportScope) error
	})
	if !ok {
		return fmt.Errorf("overlay scoped send: %w", netio.ErrOverlayIdentityMismatch)
	}
	if err := conn.SendEncapsulatedFor(ctx, payload, scope); err != nil {
		return fmt.Errorf("overlay scoped send: %w", err)
	}
	return nil
}

// SetScopes replaces the identities of the leased scoped backend in place.
func (c *leasedOverlayConn) SetScopes(scopes []bfd.TransportScope) error {
	conn, ok := c.OverlayConn.(interface {
		SetScopes(scopes []bfd.TransportScope) error
	})
	if !ok {
		return fmt.Errorf("overlay set scopes: %w", netio.ErrOverlayIdentityMismatch)
	}
	if err := conn.SetScopes(scopes); err != nil {
		return fmt.Errorf("overlay set scopes: %w", err)
	}
	return nil
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
	peerMAC, err := peer.PeerMACAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse peer VTEP MAC: %w", err)
	}
	localMAC, err := peer.LocalMACAddr()
	if err != nil {
		return bfd.TransportScope{}, fmt.Errorf("parse local VTEP MAC: %w", err)
	}
	return bfd.TransportScope{
		Kind: bfd.TransportScopeVXLAN, Owner: bfd.VXLANReconciliationOwner().ID,
		Backend: cfg.VXLAN.Backend, VNI: cfg.VXLAN.ManagementVNI,
		OuterPeerAddr: outerPeer.Unmap(), OuterLocalAddr: outerLocal.Unmap(),
		InnerPeerAddr: outerPeer.Unmap(), InnerLocalAddr: outerLocal.Unmap(),
		AddressFamily: bfd.AddressFamilyIPv4,
		PeerMAC:       peerMAC, LocalMAC: localMAC,
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
// bfd.SessionConfig.
type overlayPeerEntry struct {
	key        string
	peerName   string
	peerStr    string
	localStr   string
	peerTx     time.Duration
	peerRx     time.Duration
	peerDetect uint32
	scope      bfd.TransportScope
	scopeErr   error
}

// overlayTimerDefaults holds the default timer values shared by VXLAN and Geneve
// overlay config. Extracted to deduplicate configVXLANToBFD/configGeneveToBFD.
type overlayTimerDefaults struct {
	desiredMinTx  time.Duration
	requiredMinRx time.Duration
	detectMult    uint32
}

// buildOverlayTunnelParams builds one source-scoped candidate for each overlay
// type, including an empty candidate when that source is disabled.
func buildOverlayTunnelParams(cfg *config.Config) []overlayTunnelParams {
	return []overlayTunnelParams{
		{
			rfc: "RFC 8971", sessType: bfd.SessionTypeVXLAN,
			owner: bfd.VXLANReconciliationOwner(),
			defaults: overlayTimerDefaults{
				desiredMinTx:  cfg.VXLAN.DefaultDesiredMinTx,
				requiredMinRx: cfg.VXLAN.DefaultRequiredMinRx,
				detectMult:    cfg.VXLAN.DefaultDetectMultiplier,
			},
			entries: buildVXLANPeerEntries(cfg),
		},
		{
			rfc: "RFC 9521", sessType: bfd.SessionTypeGeneve,
			owner: bfd.GeneveReconciliationOwner(),
			defaults: overlayTimerDefaults{
				desiredMinTx:  cfg.Geneve.DefaultDesiredMinTx,
				requiredMinRx: cfg.Geneve.DefaultRequiredMinRx,
				detectMult:    cfg.Geneve.DefaultDetectMultiplier,
			},
			entries: buildGenevePeerEntries(cfg),
		},
	}
}

func buildVXLANPeerEntries(cfg *config.Config) []overlayPeerEntry {
	if !cfg.VXLAN.Enabled {
		return nil
	}
	entries := make([]overlayPeerEntry, 0, len(cfg.VXLAN.Peers))
	for _, peer := range cfg.VXLAN.Peers {
		scope, err := vxlanTransportScope(cfg, peer)
		entries = append(entries, overlayPeerEntry{
			key: peer.VXLANSessionKey(), peerName: peer.Peer,
			peerStr: scope.InnerPeerAddr.String(), localStr: scope.InnerLocalAddr.String(),
			peerTx: peer.DesiredMinTx, peerRx: peer.RequiredMinRx,
			peerDetect: peer.DetectMult, scope: scope, scopeErr: err,
		})
	}
	return entries
}

func buildGenevePeerEntries(cfg *config.Config) []overlayPeerEntry {
	if !cfg.Geneve.Enabled {
		return nil
	}
	entries := make([]overlayPeerEntry, 0, len(cfg.Geneve.Peers))
	for _, peer := range cfg.Geneve.Peers {
		scope, err := geneveTransportScope(cfg, peer)
		entries = append(entries, overlayPeerEntry{
			key: peer.GeneveSessionKey(cfg.Geneve.DefaultVNI), peerName: peer.Peer,
			peerStr: scope.InnerPeerAddr.String(), localStr: scope.InnerLocalAddr.String(),
			peerTx: peer.DesiredMinTx, peerRx: peer.RequiredMinRx,
			peerDetect: peer.DetectMult, scope: scope, scopeErr: err,
		})
	}
	return entries
}

// overlayTunnelParams captures the differences between VXLAN and Geneve
// candidate compilation.
type overlayTunnelParams struct {
	rfc      string
	sessType bfd.SessionType
	owner    bfd.SessionOwner
	defaults overlayTimerDefaults
	entries  []overlayPeerEntry
}

// compileOverlaySessionCandidates validates the complete desired overlay set.
// Backends are claimed at apply time by the overlay owner.
func compileOverlaySessionCandidates(params overlayTunnelParams) ([]bfd.ReconcileConfig, error) {
	desired := make([]bfd.ReconcileConfig, 0, len(params.entries))
	for _, e := range params.entries {
		if e.scopeErr != nil {
			return nil, fmt.Errorf("%s peer %q identity: %w", params.rfc, e.peerName, e.scopeErr)
		}
		sessCfg, cfgErr := buildOverlaySessionConfig(
			e.peerStr, e.localStr, e.peerTx, e.peerRx, e.peerDetect,
			params.defaults, params.sessType, e.scope)
		if cfgErr != nil {
			return nil, fmt.Errorf("%s peer %q: %w", params.rfc, e.peerName, cfgErr)
		}
		desired = append(desired, bfd.ReconcileConfig{Key: e.key, SessionConfig: sessCfg})
	}
	if err := bfd.ValidateReconcileConfigs(desired); err != nil {
		return nil, fmt.Errorf("validate complete %s session set: %w", params.rfc, err)
	}
	return desired, nil
}

func applyOverlayDesiredSessions(
	ctx context.Context,
	mgr *bfd.Manager,
	owner bfd.SessionOwner,
	desired []bfd.ReconcileConfig,
) bfd.ReconcileResult {
	return mgr.ReconcileSessionsForOwnerDetailed(ctx, owner, desired)
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
