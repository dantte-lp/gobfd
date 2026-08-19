package netio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dantte-lp/gobfd/internal/bfd"
)

// LAGActuatorMode controls whether LAG member actions are disabled, logged, or enforced.
type LAGActuatorMode string

const (
	// LAGActuatorModeDisabled suppresses all LAG member actions.
	LAGActuatorModeDisabled LAGActuatorMode = "disabled"
	// LAGActuatorModeDryRun logs LAG member actions without applying them.
	LAGActuatorModeDryRun LAGActuatorMode = "dry-run"
	// LAGActuatorModeEnforce applies LAG member actions through the configured backend.
	LAGActuatorModeEnforce LAGActuatorMode = "enforce"
)

// LAGActuatorAction identifies an action to apply to a LAG member.
type LAGActuatorAction string

const (
	// LAGActuatorActionNone leaves the LAG member unchanged.
	LAGActuatorActionNone LAGActuatorAction = "none"
	// LAGActuatorActionRemoveMember removes the member from its LAG.
	LAGActuatorActionRemoveMember LAGActuatorAction = "remove-member"
	// LAGActuatorActionAddMember adds the member to its LAG.
	LAGActuatorActionAddMember LAGActuatorAction = "add-member"
)

// LAGActuatorBackendType identifies a backend that manages LAG membership.
type LAGActuatorBackendType string

const (
	// LAGActuatorBackendAuto defers backend selection and is not enforceable by itself.
	LAGActuatorBackendAuto LAGActuatorBackendType = "auto"
	// LAGActuatorBackendKernelBond manages Linux bonding through sysfs.
	LAGActuatorBackendKernelBond LAGActuatorBackendType = "kernel-bond"
	// LAGActuatorBackendOVS manages an Open vSwitch bond through OVSDB.
	LAGActuatorBackendOVS LAGActuatorBackendType = "ovs"
	// LAGActuatorBackendNetworkManager manages a bond through NetworkManager D-Bus.
	LAGActuatorBackendNetworkManager LAGActuatorBackendType = "networkmanager"
)

// LAGOwnerPolicy controls whether the actuator may modify externally managed interfaces.
type LAGOwnerPolicy string

const (
	// LAGOwnerPolicyRefuseIfManaged rejects changes when another manager owns the interface.
	LAGOwnerPolicyRefuseIfManaged LAGOwnerPolicy = "refuse-if-managed"
	// LAGOwnerPolicyAllowExternal permits direct changes to externally managed interfaces.
	LAGOwnerPolicyAllowExternal LAGOwnerPolicy = "allow-external"
	// LAGOwnerPolicyNetworkManagerDBus requires ownership through NetworkManager D-Bus.
	LAGOwnerPolicyNetworkManagerDBus LAGOwnerPolicy = "networkmanager-dbus"
)

var (
	// ErrInvalidLAGActuatorMode indicates an unrecognized actuator mode.
	ErrInvalidLAGActuatorMode = errors.New("invalid LAG actuator mode")
	// ErrInvalidLAGActuatorAction indicates an unrecognized member action.
	ErrInvalidLAGActuatorAction = errors.New("invalid LAG actuator action")
	// ErrInvalidLAGActuatorBackend indicates an unrecognized actuator backend.
	ErrInvalidLAGActuatorBackend = errors.New("invalid LAG actuator backend")
	// ErrInvalidLAGOwnerPolicy indicates an unrecognized interface owner policy.
	ErrInvalidLAGOwnerPolicy = errors.New("invalid LAG owner policy")
	// ErrLAGActuatorBackendNil indicates that enforce mode has no backend.
	ErrLAGActuatorBackendNil = errors.New("LAG actuator backend is required in enforce mode")
)

// LAGActuatorConfig configures the policy gate for RFC 7130 member actions.
type LAGActuatorConfig struct {
	Mode          LAGActuatorMode
	Backend       LAGActuatorBackendType
	OVSDBEndpoint string
	OwnerPolicy   LAGOwnerPolicy
	DownAction    LAGActuatorAction
	UpAction      LAGActuatorAction
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
		if err := a.backend.RemoveMember(ctx, ev.LAGInterface, ev.MemberInterface); err != nil {
			return fmt.Errorf("remove member %q from LAG %q: %w", ev.MemberInterface, ev.LAGInterface, err)
		}
	case LAGActuatorActionAddMember:
		if err := a.backend.AddMember(ctx, ev.LAGInterface, ev.MemberInterface); err != nil {
			return fmt.Errorf("add member %q to LAG %q: %w", ev.MemberInterface, ev.LAGInterface, err)
		}
	default:
		return nil
	}

	return nil
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
