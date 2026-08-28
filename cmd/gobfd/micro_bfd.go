package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

var errDuplicateMicroBFDGroupKey = errors.New("duplicate Micro-BFD LAG interface")

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
	sf declarativeSenderFactory,
	logger *slog.Logger,
) {
	groups, members, err := compileMicroBFDCandidates(cfg)
	if err != nil {
		logger.Error("invalid Micro-BFD candidate, keeping current groups and sessions",
			slog.String("error", err.Error()))
		return
	}
	reconcileMicroBFDGroupState(groups, mgr, logger)
	reconcileMicroBFDMemberSessions(ctx, mgr, sf, members, logger)
}

// reconcileMicroBFDGroupState performs Step 1 of micro-BFD reconciliation:
// create/destroy MicroBFDGroup objects in the Manager based on config.
func reconcileMicroBFDGroupState(
	desired []bfd.MicroBFDReconcileConfig,
	mgr *bfd.Manager,
	logger *slog.Logger,
) {
	created, destroyed, err := mgr.ReconcileMicroBFDGroups(desired)
	if err != nil {
		logger.Error("micro-BFD group reconciliation had errors",
			slog.String("error", err.Error()),
		)
		return
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
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	candidates []microBFDMemberCandidate,
	logger *slog.Logger,
) {
	desiredSessions := make([]bfd.ReconcileConfig, 0, len(candidates))
	for _, candidate := range candidates {
		rc := candidate.reconcile
		rc.SenderLeaseFactory = declarativeSenderLeaseFactoryFor(
			sf,
			candidate.config.LocalAddr,
			false,
			logger,
			netio.WithDstPort(netio.PortMicroBFD),
			netio.WithBindDevice(candidate.member),
		)
		desiredSessions = append(desiredSessions, rc)
	}

	sessCreated, sessDestroyed, sessErr := mgr.ReconcileSessionsForOwner(
		ctx,
		bfd.MicroBFDReconciliationOwner(),
		desiredSessions,
	)
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

type microBFDMemberCandidate struct {
	lagInterface string
	member       string
	config       bfd.SessionConfig
	reconcile    bfd.ReconcileConfig
}

func compileMicroBFDCandidates(
	cfg *config.Config,
) ([]bfd.MicroBFDReconcileConfig, []microBFDMemberCandidate, error) {
	if err := validateUniqueMicroBFDGroupKeys(cfg.MicroBFD.Groups); err != nil {
		return nil, nil, err
	}
	return compileUniqueMicroBFDCandidates(cfg)
}

func validateUniqueMicroBFDGroupKeys(groups []config.MicroBFDGroupConfig) error {
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if _, exists := seen[group.LAGInterface]; exists {
			return fmt.Errorf("Micro-BFD group %q: %w",
				group.LAGInterface, errDuplicateMicroBFDGroupKey)
		}
		seen[group.LAGInterface] = struct{}{}
	}
	return nil
}

func compileUniqueMicroBFDCandidates(
	cfg *config.Config,
) ([]bfd.MicroBFDReconcileConfig, []microBFDMemberCandidate, error) {
	groups := make([]bfd.MicroBFDReconcileConfig, 0, len(cfg.MicroBFD.Groups))
	members := make([]microBFDMemberCandidate, 0, len(cfg.MicroBFD.Groups)*2)
	desiredSessions := make([]bfd.ReconcileConfig, 0, len(cfg.MicroBFD.Groups)*2)
	for _, group := range cfg.MicroBFD.Groups {
		microCfg, err := configMicroBFDToBFD(group)
		if err != nil {
			return nil, nil, fmt.Errorf("Micro-BFD group %q: %w", group.LAGInterface, err)
		}
		if err := bfd.ValidateMicroBFDConfig(microCfg); err != nil {
			return nil, nil, fmt.Errorf("validate Micro-BFD group %q: %w", group.LAGInterface, err)
		}
		groups = append(groups, bfd.MicroBFDReconcileConfig{Key: group.LAGInterface, Config: microCfg})

		detectMult := group.DetectMult
		if detectMult == 0 {
			detectMult = cfg.BFD.DefaultDetectMultiplier
		}
		if detectMult > maxBFDWireUint8 {
			return nil, nil, fmt.Errorf("Micro-BFD group %q detect_mult %d: %w",
				group.LAGInterface, detectMult, errDetectMultOverflow)
		}
		desiredMinTx := group.DesiredMinTx
		if desiredMinTx == 0 {
			desiredMinTx = cfg.BFD.DefaultDesiredMinTx
		}
		requiredMinRx := group.RequiredMinRx
		if requiredMinRx == 0 {
			requiredMinRx = cfg.BFD.DefaultRequiredMinRx
		}
		for _, member := range group.MemberLinks {
			sessCfg := bfd.SessionConfig{
				PeerAddr:              microCfg.PeerAddr,
				LocalAddr:             microCfg.LocalAddr,
				Interface:             member,
				Type:                  bfd.SessionTypeMicroBFD,
				Role:                  bfd.RoleActive,
				DesiredMinTxInterval:  desiredMinTx,
				RequiredMinRxInterval: requiredMinRx,
				DetectMultiplier:      uint8(detectMult),
			}
			rc := bfd.ReconcileConfig{
				Key:           microCfg.PeerAddr.String() + "|" + microCfg.LocalAddr.String() + "|" + member,
				SessionConfig: sessCfg,
			}
			members = append(members, microBFDMemberCandidate{
				lagInterface: group.LAGInterface,
				member:       member,
				config:       sessCfg,
				reconcile:    rc,
			})
			desiredSessions = append(desiredSessions, rc)
		}
	}
	if err := bfd.ValidateReconcileConfigs(desiredSessions); err != nil {
		return nil, nil, fmt.Errorf("validate complete Micro-BFD member set: %w", err)
	}
	return groups, members, nil
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

	if gc.DetectMult > maxBFDWireUint8 {
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
