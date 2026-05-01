package netio_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestKernelBondLAGBackendWritesSysfsSlaveCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	slavesPath := filepath.Join(root, "bond0", "bonding", "slaves")
	if err := os.MkdirAll(filepath.Dir(slavesPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(slavesPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	backend := netio.NewKernelBondLAGBackend(netio.KernelBondLAGBackendConfig{
		SysfsRoot: root,
	})

	if err := backend.RemoveMember(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	assertFileContent(t, slavesPath, "-eth0\n")

	if err := backend.AddMember(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	assertFileContent(t, slavesPath, "+eth0\n")
}

func TestKernelBondLAGBackendRejectsUnsafeInterfaceNames(t *testing.T) {
	t.Parallel()

	backend := netio.NewKernelBondLAGBackend(netio.KernelBondLAGBackendConfig{
		SysfsRoot: t.TempDir(),
	})

	err := backend.RemoveMember(context.Background(), "../bond0", "eth0")
	if !errors.Is(err, netio.ErrInvalidLAGInterfaceName) {
		t.Fatalf("RemoveMember error = %v, want %v", err, netio.ErrInvalidLAGInterfaceName)
	}

	err = backend.AddMember(context.Background(), "bond0", "eth0/../../x")
	if !errors.Is(err, netio.ErrInvalidLAGInterfaceName) {
		t.Fatalf("AddMember error = %v, want %v", err, netio.ErrInvalidLAGInterfaceName)
	}
}

func TestNewLAGActuatorBackendSelectsKernelBond(t *testing.T) {
	t.Parallel()

	backend, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendKernelBond,
		OwnerPolicy: netio.LAGOwnerPolicyAllowExternal,
	})
	if err != nil {
		t.Fatalf("NewLAGActuatorBackend: %v", err)
	}
	if backend == nil {
		t.Fatal("NewLAGActuatorBackend returned nil backend")
	}
}

func TestNewLAGActuatorBackendRejectsKernelBondWithoutExternalOwnerAllowance(t *testing.T) {
	t.Parallel()

	_, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendKernelBond,
		OwnerPolicy: netio.LAGOwnerPolicyRefuseIfManaged,
	})
	if !errors.Is(err, netio.ErrUnsupportedLAGOwnerPolicy) {
		t.Fatalf("NewLAGActuatorBackend error = %v, want %v",
			err, netio.ErrUnsupportedLAGOwnerPolicy)
	}
}

func TestOVSLAGBackendRunsBondIfaceCommands(t *testing.T) {
	t.Parallel()

	runner := &recordingCommandRunner{}
	backend := netio.NewOVSLAGBackend(netio.OVSLAGBackendConfig{
		Command: "ovs-vsctl-test",
		Runner:  runner,
	})

	if err := backend.RemoveMember(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if err := backend.AddMember(context.Background(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	want := []commandCall{
		{
			name: "ovs-vsctl-test",
			args: []string{"--if-exists", "del-bond-iface", "bond0", "eth0"},
		},
		{
			name: "ovs-vsctl-test",
			args: []string{"--may-exist", "add-bond-iface", "bond0", "eth0"},
		},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("command calls = %#v, want %#v", runner.calls, want)
	}
}

func TestOVSLAGBackendRejectsUnsafeInterfaceNames(t *testing.T) {
	t.Parallel()

	backend := netio.NewOVSLAGBackend(netio.OVSLAGBackendConfig{
		Runner: &recordingCommandRunner{},
	})

	err := backend.RemoveMember(context.Background(), "bond0/../../x", "eth0")
	if !errors.Is(err, netio.ErrInvalidLAGInterfaceName) {
		t.Fatalf("RemoveMember error = %v, want %v", err, netio.ErrInvalidLAGInterfaceName)
	}
}

func TestNewLAGActuatorBackendSelectsOVS(t *testing.T) {
	t.Parallel()

	backend, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendOVS,
		OwnerPolicy: netio.LAGOwnerPolicyAllowExternal,
	})
	if err != nil {
		t.Fatalf("NewLAGActuatorBackend: %v", err)
	}
	if backend == nil {
		t.Fatal("NewLAGActuatorBackend returned nil backend")
	}
}

func TestNewLAGActuatorBackendRejectsUnsupportedBackends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend netio.LAGActuatorBackendType
		policy  netio.LAGOwnerPolicy
	}{
		{
			name:    "auto requires explicit backend",
			backend: netio.LAGActuatorBackendAuto,
			policy:  netio.LAGOwnerPolicyRefuseIfManaged,
		},
		{
			name:    "networkmanager not implemented",
			backend: netio.LAGActuatorBackendNetworkManager,
			policy:  netio.LAGOwnerPolicyNetworkManagerDBus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
				Mode:        netio.LAGActuatorModeEnforce,
				Backend:     tt.backend,
				OwnerPolicy: tt.policy,
			})
			if !errors.Is(err, netio.ErrUnsupportedLAGActuatorBackend) {
				t.Fatalf("NewLAGActuatorBackend error = %v, want %v",
					err, netio.ErrUnsupportedLAGActuatorBackend)
			}
		})
	}
}

type commandCall struct {
	name string
	args []string
}

type recordingCommandRunner struct {
	calls []commandCall
}

func (r *recordingCommandRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, commandCall{
		name: name,
		args: append([]string(nil), args...),
	})
	return nil
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("file content = %q, want %q", string(got), want)
	}
}
