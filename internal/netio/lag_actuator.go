package netio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

type LAGActuatorMode string

const (
	LAGActuatorModeDisabled LAGActuatorMode = "disabled"
	LAGActuatorModeDryRun   LAGActuatorMode = "dry-run"
	LAGActuatorModeEnforce  LAGActuatorMode = "enforce"
)

type LAGActuatorAction string

const (
	LAGActuatorActionNone         LAGActuatorAction = "none"
	LAGActuatorActionRemoveMember LAGActuatorAction = "remove-member"
	LAGActuatorActionAddMember    LAGActuatorAction = "add-member"
)

type LAGActuatorBackendType string

const (
	LAGActuatorBackendAuto           LAGActuatorBackendType = "auto"
	LAGActuatorBackendKernelBond     LAGActuatorBackendType = "kernel-bond"
	LAGActuatorBackendOVS            LAGActuatorBackendType = "ovs"
	LAGActuatorBackendNetworkManager LAGActuatorBackendType = "networkmanager"
)

type LAGOwnerPolicy string

const (
	LAGOwnerPolicyRefuseIfManaged    LAGOwnerPolicy = "refuse-if-managed"
	LAGOwnerPolicyAllowExternal      LAGOwnerPolicy = "allow-external"
	LAGOwnerPolicyNetworkManagerDBus LAGOwnerPolicy = "networkmanager-dbus"
)

var (
	ErrInvalidLAGActuatorMode    = errors.New("invalid LAG actuator mode")
	ErrInvalidLAGActuatorAction  = errors.New("invalid LAG actuator action")
	ErrInvalidLAGActuatorBackend = errors.New("invalid LAG actuator backend")
	ErrInvalidLAGOwnerPolicy     = errors.New("invalid LAG owner policy")
	ErrLAGActuatorBackendNil     = errors.New("LAG actuator backend is required in enforce mode")
)

// LAGActuatorConfig configures the policy gate for RFC 7130 member actions.
type LAGActuatorConfig struct {
	Mode        LAGActuatorMode
	Backend     LAGActuatorBackendType
	OwnerPolicy LAGOwnerPolicy
	DownAction  LAGActuatorAction
	UpAction    LAGActuatorAction
}

// LAGActuatorBackend applies selected member actions to a Linux LAG backend.
type LAGActuatorBackend interface {
	RemoveMember(ctx context.Context, lagInterface, memberInterface string) error
	AddMember(ctx context.Context, lagInterface, memberInterface string) error
}

// LAGActuator maps Micro-BFD member transitions to guarded LAG actions.
type LAGActuator struct {
	cfg     LAGActuatorConfig
	backend LAGActuatorBackend
	logger  *slog.Logger
}

// NewLAGActuator creates a policy-gated RFC 7130 LAG actuator.
func NewLAGActuator(
	cfg LAGActuatorConfig,
	backend LAGActuatorBackend,
	logger *slog.Logger,
) (*LAGActuator, error) {
	normalized, err := normalizeLAGActuatorConfig(cfg)
	if err != nil {
		return nil, err
	}
	if normalized.Mode == LAGActuatorModeEnforce && backend == nil {
		return nil, ErrLAGActuatorBackendNil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &LAGActuator{
		cfg:     normalized,
		backend: backend,
		logger:  logger.With(slog.String("component", "lag-actuator")),
	}, nil
}

// HandleMicroBFDMemberEvent implements bfd.MicroBFDActuator.
func (a *LAGActuator) HandleMicroBFDMemberEvent(
	ctx context.Context,
	ev bfd.MicroBFDMemberEvent,
) error {
	decision := a.decision(ev)
	if decision == LAGActuatorActionNone {
		return nil
	}

	a.logger.Info("micro-BFD LAG actuator decision",
		slog.String("mode", string(a.cfg.Mode)),
		slog.String("backend", string(a.cfg.Backend)),
		slog.String("owner_policy", string(a.cfg.OwnerPolicy)),
		slog.String("action", string(decision)),
		slog.String("lag", ev.LAGInterface),
		slog.String("member", ev.MemberInterface),
		slog.String("old_state", ev.OldState.String()),
		slog.String("new_state", ev.NewState.String()),
	)

	if a.cfg.Mode != LAGActuatorModeEnforce {
		return nil
	}

	switch decision {
	case LAGActuatorActionRemoveMember:
		return a.backend.RemoveMember(ctx, ev.LAGInterface, ev.MemberInterface)
	case LAGActuatorActionAddMember:
		return a.backend.AddMember(ctx, ev.LAGInterface, ev.MemberInterface)
	default:
		return nil
	}
}

func (a *LAGActuator) decision(ev bfd.MicroBFDMemberEvent) LAGActuatorAction {
	if a.cfg.Mode == LAGActuatorModeDisabled {
		return LAGActuatorActionNone
	}
	if ev.NewState == bfd.StateUp {
		return a.cfg.UpAction
	}
	if ev.OldState == bfd.StateUp && ev.NewState != bfd.StateUp {
		return a.cfg.DownAction
	}
	return LAGActuatorActionNone
}

func normalizeLAGActuatorConfig(cfg LAGActuatorConfig) (LAGActuatorConfig, error) {
	if cfg.Mode == "" {
		cfg.Mode = LAGActuatorModeDisabled
	}
	if cfg.Backend == "" {
		cfg.Backend = LAGActuatorBackendAuto
	}
	if cfg.OwnerPolicy == "" {
		cfg.OwnerPolicy = LAGOwnerPolicyRefuseIfManaged
	}
	if cfg.DownAction == "" {
		cfg.DownAction = LAGActuatorActionRemoveMember
	}
	if cfg.UpAction == "" {
		cfg.UpAction = LAGActuatorActionAddMember
	}
	if err := validateLAGActuatorMode(cfg.Mode); err != nil {
		return LAGActuatorConfig{}, err
	}
	if err := validateLAGActuatorBackend(cfg.Backend); err != nil {
		return LAGActuatorConfig{}, err
	}
	if err := validateLAGOwnerPolicy(cfg.OwnerPolicy); err != nil {
		return LAGActuatorConfig{}, err
	}
	if err := validateLAGActuatorAction(cfg.DownAction); err != nil {
		return LAGActuatorConfig{}, fmt.Errorf("down_action: %w", err)
	}
	if err := validateLAGActuatorAction(cfg.UpAction); err != nil {
		return LAGActuatorConfig{}, fmt.Errorf("up_action: %w", err)
	}
	return cfg, nil
}

func validateLAGActuatorMode(mode LAGActuatorMode) error {
	switch mode {
	case LAGActuatorModeDisabled, LAGActuatorModeDryRun, LAGActuatorModeEnforce:
		return nil
	default:
		return fmt.Errorf("%q: %w", mode, ErrInvalidLAGActuatorMode)
	}
}

func validateLAGActuatorBackend(backend LAGActuatorBackendType) error {
	switch backend {
	case LAGActuatorBackendAuto,
		LAGActuatorBackendKernelBond,
		LAGActuatorBackendOVS,
		LAGActuatorBackendNetworkManager:
		return nil
	default:
		return fmt.Errorf("%q: %w", backend, ErrInvalidLAGActuatorBackend)
	}
}

func validateLAGOwnerPolicy(policy LAGOwnerPolicy) error {
	switch policy {
	case LAGOwnerPolicyRefuseIfManaged,
		LAGOwnerPolicyAllowExternal,
		LAGOwnerPolicyNetworkManagerDBus:
		return nil
	default:
		return fmt.Errorf("%q: %w", policy, ErrInvalidLAGOwnerPolicy)
	}
}

func validateLAGActuatorAction(action LAGActuatorAction) error {
	switch action {
	case LAGActuatorActionNone, LAGActuatorActionRemoveMember, LAGActuatorActionAddMember:
		return nil
	default:
		return fmt.Errorf("%q: %w", action, ErrInvalidLAGActuatorAction)
	}
}
