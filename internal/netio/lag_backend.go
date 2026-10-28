package netio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	defaultOVSVSCTLCommand     = "ovs-vsctl"
	maxLinuxInterfaceNameLen   = 15
)

var (
	// ErrUnsupportedLAGActuatorBackend indicates a recognized backend that cannot be selected.
	ErrUnsupportedLAGActuatorBackend = errors.New("unsupported LAG actuator backend")
	// ErrUnsupportedLAGOwnerPolicy indicates a backend and owner-policy mismatch.
	ErrUnsupportedLAGOwnerPolicy = errors.New("unsupported LAG owner policy")
	// ErrInvalidLAGInterfaceName indicates an unsafe or malformed Linux interface name.
	ErrInvalidLAGInterfaceName = errors.New("invalid LAG interface name")
	// ErrLAGInterfaceNotBond indicates a kernel-bond LAG interface that is not a Linux bond.
	ErrLAGInterfaceNotBond = errors.New("LAG interface is not a Linux bond")
	// ErrLAGMemberNotInLAG indicates a member to release that is not enslaved to the LAG.
	ErrLAGMemberNotInLAG = errors.New("member link is not in the LAG")
	// ErrLAGMemberOwnedElsewhere indicates a member to add that already has another master.
	ErrLAGMemberOwnedElsewhere = errors.New("member link belongs to another master")
	// ErrLAGActuatorBackendNotRequired indicates that no backend is needed outside enforce mode.
	ErrLAGActuatorBackendNotRequired = errors.New("LAG actuator backend is only required in enforce mode")
)

// BondLAGClient applies high-level bond member changes to a specific backend.
type BondLAGClient interface {
	RemoveBondInterface(ctx context.Context, bond string, iface string) error
	AddBondInterface(ctx context.Context, bond string, iface string) error
}

// NewLAGActuatorBackend creates the Linux backend selected by an actuator config.
func NewLAGActuatorBackend(cfg LAGActuatorConfig) (LAGActuatorBackend, error) {
	normalized, err := normalizeLAGActuatorConfig(cfg)
	if err != nil {
		return nil, err
	}
	if normalized.Mode != LAGActuatorModeEnforce {
		return nil, ErrLAGActuatorBackendNotRequired
	}

	switch normalized.Backend {
	case LAGActuatorBackendKernelBond:
		if normalized.OwnerPolicy != LAGOwnerPolicyAllowExternal {
			return nil, fmt.Errorf("%s with %s: %w",
				normalized.Backend, normalized.OwnerPolicy, ErrUnsupportedLAGOwnerPolicy)
		}
		return NewKernelBondLAGBackend(KernelBondLAGBackendConfig{}), nil
	case LAGActuatorBackendOVS:
		if normalized.OwnerPolicy != LAGOwnerPolicyAllowExternal {
			return nil, fmt.Errorf("%s with %s: %w",
				normalized.Backend, normalized.OwnerPolicy, ErrUnsupportedLAGOwnerPolicy)
		}
		return NewOVSDBLAGBackend(OVSDBLAGBackendConfig{
			Endpoint: normalized.OVSDBEndpoint,
		}), nil
	case LAGActuatorBackendNetworkManager:
		if normalized.OwnerPolicy != LAGOwnerPolicyNetworkManagerDBus {
			return nil, fmt.Errorf("%s with %s: %w",
				normalized.Backend, normalized.OwnerPolicy, ErrUnsupportedLAGOwnerPolicy)
		}
		return NewNetworkManagerLAGBackend(NetworkManagerLAGBackendConfig{}), nil
	case LAGActuatorBackendAuto:
		return nil, fmt.Errorf("%s: %w",
			normalized.Backend, ErrUnsupportedLAGActuatorBackend)
	default:
		return nil, fmt.Errorf("%s: %w",
			normalized.Backend, ErrInvalidLAGActuatorBackend)
	}
}

// KernelBondLink is the rtnetlink view of one link: its ifindex, its
// IFLA_INFO_KIND and the ifindex of its master (IFLA_MASTER, 0 for none).
type KernelBondLink struct {
	Index       int
	Kind        string
	MasterIndex int
}

// KernelBondLinks reads links and sets their master through rtnetlink.
type KernelBondLinks interface {
	Link(ctx context.Context, name string) (KernelBondLink, error)
	SetMaster(ctx context.Context, index, masterIndex int) error
}

// KernelBondLAGBackendConfig configures the Linux bonding backend.
type KernelBondLAGBackendConfig struct {
	// Links is the rtnetlink client. Nil uses the current network namespace.
	Links KernelBondLinks
}

// KernelBondLAGBackend applies member changes to a Linux bond through
// rtnetlink IFLA_MASTER, the operation behind "ip link set dev <member>
// master <bond>" and "nomaster". It needs CAP_NET_ADMIN and no sysfs write.
type KernelBondLAGBackend struct {
	links KernelBondLinks
}

// NewKernelBondLAGBackend creates a Linux bonding rtnetlink backend.
func NewKernelBondLAGBackend(cfg KernelBondLAGBackendConfig) *KernelBondLAGBackend {
	links := cfg.Links
	if links == nil {
		links = rtnetlinkBondLinks{}
	}
	return &KernelBondLAGBackend{links: links}
}

// RemoveMember releases memberInterface from lagInterface (IFLA_MASTER 0).
// A member that is not enslaved to lagInterface is refused.
func (b *KernelBondLAGBackend) RemoveMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	lag, member, err := b.resolve(ctx, lagInterface, memberInterface)
	if err != nil {
		return err
	}
	if member.MasterIndex != lag.Index {
		return fmt.Errorf("release %q from bond %q: %w", memberInterface, lagInterface, ErrLAGMemberNotInLAG)
	}
	if err := b.links.SetMaster(ctx, member.Index, 0); err != nil {
		return fmt.Errorf("release %q from bond %q: %w", memberInterface, lagInterface, err)
	}
	return nil
}

// AddMember enslaves memberInterface to lagInterface (IFLA_MASTER bond
// ifindex). A member already in lagInterface is left alone; a member of
// another master is refused, because rtnetlink would move it.
func (b *KernelBondLAGBackend) AddMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	lag, member, err := b.resolve(ctx, lagInterface, memberInterface)
	if err != nil {
		return err
	}
	switch member.MasterIndex {
	case lag.Index:
		return nil
	case 0:
	default:
		return fmt.Errorf("enslave %q to bond %q: %w", memberInterface, lagInterface, ErrLAGMemberOwnedElsewhere)
	}
	if err := b.links.SetMaster(ctx, member.Index, lag.Index); err != nil {
		return fmt.Errorf("enslave %q to bond %q: %w", memberInterface, lagInterface, err)
	}
	return nil
}

func (b *KernelBondLAGBackend) resolve(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) (KernelBondLink, KernelBondLink, error) {
	if err := ctx.Err(); err != nil {
		return KernelBondLink{}, KernelBondLink{}, fmt.Errorf("change Linux bond membership canceled: %w", err)
	}
	if err := validateLAGInterfaceName(lagInterface); err != nil {
		return KernelBondLink{}, KernelBondLink{}, fmt.Errorf("lag interface %q: %w", lagInterface, err)
	}
	if err := validateLAGInterfaceName(memberInterface); err != nil {
		return KernelBondLink{}, KernelBondLink{}, fmt.Errorf("member interface %q: %w", memberInterface, err)
	}
	lag, err := b.links.Link(ctx, lagInterface)
	if err != nil {
		return KernelBondLink{}, KernelBondLink{}, fmt.Errorf("lag interface %q: %w", lagInterface, err)
	}
	if lag.Kind != "bond" {
		return KernelBondLink{}, KernelBondLink{}, fmt.Errorf("lag interface %q kind %q: %w",
			lagInterface, lag.Kind, ErrLAGInterfaceNotBond)
	}
	member, err := b.links.Link(ctx, memberInterface)
	if err != nil {
		return KernelBondLink{}, KernelBondLink{}, fmt.Errorf("member interface %q: %w", memberInterface, err)
	}
	return lag, member, nil
}

// CommandRunner runs an external command.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	// #nosec G204 -- the command is a fixed backend binary and all interface
	// arguments are validated before this runner is called.
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s",
			name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// OVSLAGBackendConfig configures an ovs-vsctl-backed LAG actuator.
type OVSLAGBackendConfig struct {
	// Command is the ovs-vsctl binary path. Empty means ovs-vsctl from PATH.
	Command string

	// Runner executes ovs-vsctl. Empty uses os/exec.
	Runner CommandRunner
}

// OVSLAGBackend applies member changes to an existing OVS bond port.
type OVSLAGBackend struct {
	command string
	runner  CommandRunner
}

// NewOVSLAGBackend creates an ovs-vsctl backend for OVS bonded ports.
func NewOVSLAGBackend(cfg OVSLAGBackendConfig) *OVSLAGBackend {
	command := cfg.Command
	if command == "" {
		command = defaultOVSVSCTLCommand
	}
	runner := cfg.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	return &OVSLAGBackend{
		command: command,
		runner:  runner,
	}
}

// RemoveMember removes memberInterface from an OVS bond port.
func (b *OVSLAGBackend) RemoveMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	return b.runBondIfaceCommand(ctx, "--if-exists", "del-bond-iface", lagInterface, memberInterface)
}

// AddMember adds memberInterface to an existing OVS bond port.
func (b *OVSLAGBackend) AddMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	return b.runBondIfaceCommand(ctx, "--may-exist", "add-bond-iface", lagInterface, memberInterface)
}

func (b *OVSLAGBackend) runBondIfaceCommand(
	ctx context.Context,
	existenceFlag string,
	command string,
	lagInterface string,
	memberInterface string,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("run OVS bond member command canceled: %w", err)
	}
	if err := validateLAGInterfaceName(lagInterface); err != nil {
		return fmt.Errorf("lag interface %q: %w", lagInterface, err)
	}
	if err := validateLAGInterfaceName(memberInterface); err != nil {
		return fmt.Errorf("member interface %q: %w", memberInterface, err)
	}

	if err := b.runner.Run(ctx, b.command,
		existenceFlag, command, lagInterface, memberInterface); err != nil {
		return fmt.Errorf("ovs lag backend: %w", err)
	}
	return nil
}

func validateLAGInterfaceName(name string) error {
	if name == "" ||
		name == "." ||
		name == ".." ||
		len(name) > maxLinuxInterfaceNameLen ||
		strings.ContainsAny(name, "/\x00") {
		return ErrInvalidLAGInterfaceName
	}
	return nil
}
