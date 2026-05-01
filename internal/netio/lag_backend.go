package netio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	defaultKernelBondSysfsRoot = "/sys/class/net"
	defaultOVSVSCTLCommand     = "ovs-vsctl"
	maxLinuxInterfaceNameLen   = 15
)

var (
	ErrUnsupportedLAGActuatorBackend = errors.New("unsupported LAG actuator backend")
	ErrUnsupportedLAGOwnerPolicy     = errors.New("unsupported LAG owner policy")
	ErrInvalidLAGInterfaceName       = errors.New("invalid LAG interface name")
	ErrLAGActuatorBackendNotRequired = errors.New("LAG actuator backend is only required in enforce mode")
)

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
		return NewOVSLAGBackend(OVSLAGBackendConfig{}), nil
	case LAGActuatorBackendAuto,
		LAGActuatorBackendNetworkManager:
		return nil, fmt.Errorf("%s: %w",
			normalized.Backend, ErrUnsupportedLAGActuatorBackend)
	default:
		return nil, fmt.Errorf("%s: %w",
			normalized.Backend, ErrInvalidLAGActuatorBackend)
	}
}

// KernelBondLAGBackendConfig configures Linux bonding sysfs writes.
type KernelBondLAGBackendConfig struct {
	// SysfsRoot points at the network class root. Empty means /sys/class/net.
	SysfsRoot string
}

// KernelBondLAGBackend applies member changes through Linux bonding sysfs.
type KernelBondLAGBackend struct {
	sysfsRoot string
}

// NewKernelBondLAGBackend creates a Linux bonding sysfs backend.
func NewKernelBondLAGBackend(cfg KernelBondLAGBackendConfig) *KernelBondLAGBackend {
	root := cfg.SysfsRoot
	if root == "" {
		root = defaultKernelBondSysfsRoot
	}
	return &KernelBondLAGBackend{sysfsRoot: root}
}

// RemoveMember removes memberInterface from lagInterface through bonding/slaves.
func (b *KernelBondLAGBackend) RemoveMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	return b.writeSlaveCommand(ctx, lagInterface, memberInterface, "-")
}

// AddMember adds memberInterface to lagInterface through bonding/slaves.
func (b *KernelBondLAGBackend) AddMember(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
) error {
	return b.writeSlaveCommand(ctx, lagInterface, memberInterface, "+")
}

func (b *KernelBondLAGBackend) writeSlaveCommand(
	ctx context.Context,
	lagInterface string,
	memberInterface string,
	prefix string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateLAGInterfaceName(lagInterface); err != nil {
		return fmt.Errorf("lag interface %q: %w", lagInterface, err)
	}
	if err := validateLAGInterfaceName(memberInterface); err != nil {
		return fmt.Errorf("member interface %q: %w", memberInterface, err)
	}

	path := filepath.Join(b.sysfsRoot, lagInterface, "bonding", "slaves")
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := file.WriteString(prefix + memberInterface + "\n"); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("write %s: %w",
				path, errors.Join(err, fmt.Errorf("close: %w", closeErr)))
		}
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
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
		return err
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
