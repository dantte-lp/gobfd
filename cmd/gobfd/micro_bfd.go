package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
	"github.com/dantte-lp/gobfd/internal/netio"
)

var errDuplicateMicroBFDGroupKey = errors.New("duplicate Micro-BFD LAG interface")

// -------------------------------------------------------------------------
// Micro-BFD — RFC 7130 LAG member link sessions
// -------------------------------------------------------------------------

// microListenerConfigs returns one port 6784 listener per unique (local
// address, member link) pair across all Micro-BFD groups. RFC 7130 Section
// 2.1: each member link has its own BFD session on port 6784.
func microListenerConfigs(cfg *config.Config) []netio.ListenerConfig {
	seen := make(map[listenerKey]struct{})
	var configs []netio.ListenerConfig
	for _, group := range cfg.MicroBFD.Groups {
		localAddr, err := netip.ParseAddr(group.LocalAddr)
		if err != nil || !localAddr.IsValid() {
			continue // Candidate compilation rejects an invalid local address.
		}
		for _, member := range group.MemberLinks {
			lnCfg := netio.ListenerConfig{
				ReadBufferSize: cfg.Socket.ReadBufferSize,
				Addr:           localAddr,
				IfName:         member,
				Port:           netio.PortMicroBFD,
				MultiHop:       false, // Micro-BFD uses single-hop TTL=255 (RFC 7130 Section 2).
			}
			if _, exists := seen[listenerKeyOf(lnCfg)]; exists {
				continue
			}
			seen[listenerKeyOf(lnCfg)] = struct{}{}
			configs = append(configs, lnCfg)
		}
	}
	return configs
}

// reconcileMicroBFDGroups reconciles micro-BFD groups and their per-member
// BFD sessions with the current configuration as one Manager transaction.
//
// Each group commits together with one SessionTypeMicroBFD session per
// member link, bound to the member via SO_BINDTODEVICE on port 6784
// (RFC 7130 Section 2.1). A failed member session rolls the pass back;
// groups absent from the configuration are destroyed with their members.
func reconcileMicroBFDGroups(
	ctx context.Context,
	cfg *config.Config,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	logger *slog.Logger,
) {
	groups, err := compileMicroBFDCandidates(cfg)
	if err != nil {
		logger.Error("invalid Micro-BFD candidate, keeping current groups and sessions",
			slog.String("error", err.Error()))
		return
	}
	result := applyMicroBFDCandidates(ctx, groups, mgr, sf, logger)
	if err := result.Err(); err != nil {
		logger.Error("micro-BFD reconciliation had errors",
			slog.String("error", err.Error()),
			slog.Int("created", result.Created),
			slog.Int("released", result.Released),
			slog.Int("pending", result.Pending),
			slog.Int("failed", result.Failed),
			slog.Any("error_codes", reconcileErrorCodes(result)),
		)
		return
	}
	logger.Info("micro-BFD reconciliation complete",
		slog.Int("groups_created", result.Created),
		slog.Int("groups_released", result.Released),
	)
}

// applyMicroBFDCandidates attaches member senders to the compiled groups and
// applies them in one Manager transaction.
func applyMicroBFDCandidates(
	ctx context.Context,
	groups []bfd.MicroBFDReconcileConfig,
	mgr *bfd.Manager,
	sf declarativeSenderFactory,
	logger *slog.Logger,
) bfd.ReconcileResult {
	desired := slices.Clone(groups)
	for i := range desired {
		localAddr := desired[i].Config.LocalAddr
		desired[i].SenderLeaseFactory = func(member string) bfd.SenderLeaseFactory {
			return declarativeSenderLeaseFactoryFor(
				sf, localAddr, false, logger,
				netio.WithDstPort(netio.PortMicroBFD),
				netio.WithBindDevice(member),
			)
		}
	}
	return mgr.ReconcileMicroBFDGroupsDetailed(ctx, desired)
}

// compileMicroBFDCandidates converts every configured group, applies the
// bfd.default_* member timers and validates the complete desired set.
func compileMicroBFDCandidates(cfg *config.Config) ([]bfd.MicroBFDReconcileConfig, error) {
	if err := validateUniqueMicroBFDGroupKeys(cfg.MicroBFD.Groups); err != nil {
		return nil, err
	}
	groups := make([]bfd.MicroBFDReconcileConfig, 0, len(cfg.MicroBFD.Groups))
	for _, group := range cfg.MicroBFD.Groups {
		microCfg, err := configMicroBFDToBFD(microBFDGroupWithDefaults(group, cfg.BFD))
		if err != nil {
			return nil, fmt.Errorf("Micro-BFD group %q: %w", group.LAGInterface, err)
		}
		groups = append(groups, bfd.MicroBFDReconcileConfig{Key: group.LAGInterface, Config: microCfg})
	}
	if err := bfd.ValidateMicroBFDReconcileConfigs(groups); err != nil {
		return nil, fmt.Errorf("validate complete Micro-BFD candidate: %w", err)
	}
	return groups, nil
}

// microBFDGroupWithDefaults fills unset member session timers from bfd.default_*.
func microBFDGroupWithDefaults(group config.MicroBFDGroupConfig, defaults config.BFDConfig) config.MicroBFDGroupConfig {
	if group.DetectMult == 0 {
		group.DetectMult = defaults.DefaultDetectMultiplier
	}
	if group.DesiredMinTx == 0 {
		group.DesiredMinTx = defaults.DefaultDesiredMinTx
	}
	if group.RequiredMinRx == 0 {
		group.RequiredMinRx = defaults.DefaultRequiredMinRx
	}
	return group
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
		MemberLinks:           slices.Clone(gc.MemberLinks),
		PeerAddr:              peerAddr,
		LocalAddr:             localAddr,
		DesiredMinTxInterval:  gc.DesiredMinTx,
		RequiredMinRxInterval: gc.RequiredMinRx,
		DetectMultiplier:      uint8(gc.DetectMult), // Range validated above: gc.DetectMult <= 255.
		MinActiveLinks:        gc.MinActiveLinks,
	}, nil
}
