package netio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultKernelBondSysfsRoot = "/sys/class/net"
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
	case LAGActuatorBackendAuto,
		LAGActuatorBackendOVS,
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
