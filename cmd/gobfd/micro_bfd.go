package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

// -------------------------------------------------------------------------
// Micro-BFD — RFC 7130 LAG member link sessions
// -------------------------------------------------------------------------

// createMicroBFDListeners creates listeners on port 6784 for each unique
// (localAddr, memberLink) pair across all micro-BFD groups.
// RFC 7130 Section 2.1: each member link has its own BFD session on port 6784.
func createMicroBFDListeners(cfg *config.Config, logger *slog.Logger) ([]*netio.Listener, error) {
	type microKey struct {
		addr   netip.Addr
		ifName string
	}

	seen := make(map[microKey]struct{})
	var listeners []*netio.Listener

	for _, group := range cfg.MicroBFD.Groups {
		localAddr, err := netip.ParseAddr(group.LocalAddr)
		if err != nil || !localAddr.IsValid() {
			logger.Warn("skipping micro-BFD group with invalid local address",
				slog.String("lag", group.LAGInterface),
				slog.String("local_addr", group.LocalAddr),
			)
			continue
		}

		for _, member := range group.MemberLinks {
			key := microKey{addr: localAddr, ifName: member}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			lnCfg := netio.ListenerConfig{
				Addr:     localAddr,
				IfName:   member,
				Port:     netio.PortMicroBFD,
				MultiHop: false, // Micro-BFD uses single-hop TTL=255 (RFC 7130 Section 2).
			}

			ln, err := netio.NewListener(lnCfg)
			if err != nil {
				closeListeners(listeners, logger)
				return nil, fmt.Errorf("create micro-BFD listener on %s%%%s: %w",
					localAddr, member, err)
			}

			logger.Info("micro-BFD listener started",
				slog.String("addr", localAddr.String()),
				slog.String("member", member),
				slog.Uint64("port", uint64(netio.PortMicroBFD)),
			)

			listeners = append(listeners, ln)
		}
	}

	return listeners, nil
}

// reconcileMicroBFDGroups creates and destroys micro-BFD groups and their
// per-member BFD sessions based on the current configuration.
//
// For each group in the config:
//  1. Create the MicroBFDGroup in the Manager (aggregate state tracker)
//  2. For each member link: create a BFD session with SessionTypeMicroBFD,
//     bound to the member interface via SO_BINDTODEVICE, on port 6784
//
// On SIGHUP reload, groups not in the new config are destroyed along
// with their member sessions.
func reconcileMicroBFDGroups(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) {
	if len(cfg.MicroBFD.Groups) == 0 {
		logger.Debug("no micro-BFD groups in config, skipping reconciliation")
		return
	}

	reconcileMicroBFDGroupState(cfg, mgr, logger)
	reconcileMicroBFDMemberSessions(ctx, cfg, mgr, sf, logger)
}

// reconcileMicroBFDGroupState performs Step 1 of micro-BFD reconciliation:
// create/destroy MicroBFDGroup objects in the Manager based on config.
func reconcileMicroBFDGroupState(
	cfg *config.Config,
	mgr *bfd.Manager,
	logger *slog.Logger,
) {
	desired := make([]bfd.MicroBFDReconcileConfig, 0, len(cfg.MicroBFD.Groups))
	for _, group := range cfg.MicroBFD.Groups {
		microCfg, err := configMicroBFDToBFD(group)
		if err != nil {
			logger.Error("invalid micro-BFD group config, skipping",
				slog.String("lag", group.LAGInterface),
				slog.String("error", err.Error()),
			)
			continue
		}
		desired = append(desired, bfd.MicroBFDReconcileConfig{
			Key:    group.LAGInterface,
			Config: microCfg,
		})
	}

	created, destroyed, err := mgr.ReconcileMicroBFDGroups(desired)
	if err != nil {
		logger.Error("micro-BFD group reconciliation had errors",
			slog.String("error", err.Error()),
		)
	}

	logger.Info("micro-BFD group reconciliation complete",
		slog.Int("groups_created", created),
		slog.Int("groups_destroyed", destroyed),
	)
}

// reconcileMicroBFDMemberSessions performs Step 2 of micro-BFD reconciliation:
// create/destroy per-member-link BFD sessions with SO_BINDTODEVICE on port 6784.
func reconcileMicroBFDMemberSessions(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf *udpSenderFactory,
	logger *slog.Logger,
) {
	desiredSessions := make([]bfd.ReconcileConfig, 0, len(cfg.MicroBFD.Groups)*2)

	for _, group := range cfg.MicroBFD.Groups {
		//nolint:contextcheck // Socket creation is a quick local operation.
		sessions := buildMemberSessions(cfg, group, sf, logger)
		desiredSessions = append(desiredSessions, sessions...)
	}

	if len(desiredSessions) == 0 {
		return
	}

	sessCreated, sessDestroyed, sessErr := mgr.ReconcileSessions(ctx, desiredSessions)
	if sessErr != nil {
		logger.Error("micro-BFD session reconciliation had errors",
			slog.String("error", sessErr.Error()),
		)
	}
	logger.Info("micro-BFD session reconciliation complete",
		slog.Int("sessions_created", sessCreated),
		slog.Int("sessions_destroyed", sessDestroyed),
	)
}

// buildMemberSessions builds ReconcileConfig entries for all member links
// in a single micro-BFD group. Each member gets its own BFD session with
// SessionTypeMicroBFD, SO_BINDTODEVICE, and destination port 6784.
func buildMemberSessions(
	cfg *config.Config,
	group config.MicroBFDGroupConfig,
	sf *udpSenderFactory,
	logger *slog.Logger,
) []bfd.ReconcileConfig {
	peerAddr, err := netip.ParseAddr(group.PeerAddr)
	if err != nil {
		return nil
	}
	localAddr, err := netip.ParseAddr(group.LocalAddr)
	if err != nil {
		return nil
	}

	detectMult := group.DetectMult
	if detectMult > 255 {
		logger.Error("micro-BFD detect_mult exceeds uint8 range, skipping",
			slog.String("lag", group.LAGInterface),
			slog.Uint64("detect_mult", uint64(detectMult)),
		)
		return nil
	}

	desiredMinTx := group.DesiredMinTx
	if desiredMinTx == 0 {
		desiredMinTx = cfg.BFD.DefaultDesiredMinTx
	}
	requiredMinRx := group.RequiredMinRx
	if requiredMinRx == 0 {
		requiredMinRx = cfg.BFD.DefaultRequiredMinRx
	}
	if detectMult == 0 {
		detectMult = cfg.BFD.DefaultDetectMultiplier
	}

	var sessions []bfd.ReconcileConfig
	for _, member := range group.MemberLinks {
		rc := buildMemberReconcile(
			peerAddr, localAddr, member, group.LAGInterface,
			desiredMinTx, requiredMinRx, uint8(detectMult), // Range validated: detectMult <= 255.
			sf, logger,
		)
		if rc != nil {
			sessions = append(sessions, *rc)
		}
	}
	return sessions
}

// buildMemberReconcile creates a single ReconcileConfig for one member link.
func buildMemberReconcile(
	peerAddr, localAddr netip.Addr,
	member, lagIface string,
	desiredMinTx, requiredMinRx time.Duration,
	detectMult uint8,
	sf *udpSenderFactory,
	logger *slog.Logger,
) *bfd.ReconcileConfig {
	sessCfg := bfd.SessionConfig{
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		Interface:             member,
		Type:                  bfd.SessionTypeMicroBFD,
		Role:                  bfd.RoleActive,
		DesiredMinTxInterval:  desiredMinTx,
		RequiredMinRxInterval: requiredMinRx,
		DetectMultiplier:      detectMult,
	}

	// Create sender with SO_BINDTODEVICE per member link and
	// destination port 6784 (RFC 7130 Section 2.1).
	sender, sErr := sf.createSenderForSession(
		localAddr, false, logger,
		netio.WithDstPort(netio.PortMicroBFD),
		netio.WithBindDevice(member),
	)
	if sErr != nil {
		logger.Error("failed to create sender for micro-BFD member, skipping",
			slog.String("lag", lagIface),
			slog.String("member", member),
			slog.String("error", sErr.Error()),
		)
		return nil
	}

	key := peerAddr.String() + "|" + localAddr.String() + "|" + member
	return &bfd.ReconcileConfig{
		Key:           key,
		SessionConfig: sessCfg,
		Sender:        sender,
	}
}

// configMicroBFDToBFD converts a config.MicroBFDGroupConfig to a bfd.MicroBFDConfig.
func configMicroBFDToBFD(gc config.MicroBFDGroupConfig) (bfd.MicroBFDConfig, error) {
	peerAddr, err := netip.ParseAddr(gc.PeerAddr)
	if err != nil {
		return bfd.MicroBFDConfig{}, fmt.Errorf("parse micro-BFD peer address %q: %w", gc.PeerAddr, err)
	}

	localAddr, err := netip.ParseAddr(gc.LocalAddr)
	if err != nil {
		return bfd.MicroBFDConfig{}, fmt.Errorf("parse micro-BFD local address %q: %w", gc.LocalAddr, err)
	}

	if gc.DetectMult > 255 {
		return bfd.MicroBFDConfig{}, fmt.Errorf("micro-BFD detect_mult %d: %w", gc.DetectMult, errDetectMultOverflow)
	}

	return bfd.MicroBFDConfig{
		LAGInterface:          gc.LAGInterface,
		MemberLinks:           gc.MemberLinks,
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		DesiredMinTxInterval:  gc.DesiredMinTx,
		RequiredMinRxInterval: gc.RequiredMinRx,
		DetectMultiplier:      uint8(gc.DetectMult), // Range validated above: gc.DetectMult <= 255.
		MinActiveLinks:        gc.MinActiveLinks,
	}, nil
}
