package netio_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestKernelBondLAGBackendSetsMasterThroughNetlink(t *testing.T) {
	t.Parallel()

	links := newFakeBondLinks()
	backend := netio.NewKernelBondLAGBackend(netio.KernelBondLAGBackendConfig{Links: links})

	if err := backend.RemoveMember(t.Context(), "bond0", "eth0"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if err := backend.AddMember(t.Context(), "bond0", "eth0"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	// An already enslaved member is left alone.
	if err := backend.AddMember(t.Context(), "bond0", "eth0"); err != nil {
		t.Fatalf("repeated AddMember: %v", err)
	}
	want := []string{"eth0 master 0", "eth0 master 10"}
	if !reflect.DeepEqual(links.calls, want) {
		t.Fatalf("SetMaster calls = %q, want %q", links.calls, want)
	}
}

func TestKernelBondLAGBackendRefusesForeignMembership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(*netio.KernelBondLAGBackend) error
		want error
	}{
		{
			name: "add member of another bond",
			call: func(b *netio.KernelBondLAGBackend) error { return b.AddMember(t.Context(), "bond0", "eth9") },
			want: netio.ErrLAGMemberOwnedElsewhere,
		},
		{
			name: "remove member of another bond",
			call: func(b *netio.KernelBondLAGBackend) error { return b.RemoveMember(t.Context(), "bond0", "eth9") },
			want: netio.ErrLAGMemberNotInLAG,
		},
		{
			name: "remove standalone link",
			call: func(b *netio.KernelBondLAGBackend) error { return b.RemoveMember(t.Context(), "bond0", "eth1") },
			want: netio.ErrLAGMemberNotInLAG,
		},
		{
			name: "master is a bridge",
			call: func(b *netio.KernelBondLAGBackend) error { return b.AddMember(t.Context(), "br0", "eth1") },
			want: netio.ErrLAGInterfaceNotBond,
		},
		{
			name: "missing member",
			call: func(b *netio.KernelBondLAGBackend) error { return b.AddMember(t.Context(), "bond0", "eth7") },
			want: errFakeNoLink,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			links := newFakeBondLinks()
			err := tt.call(netio.NewKernelBondLAGBackend(netio.KernelBondLAGBackendConfig{Links: links}))
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if len(links.calls) != 0 {
				t.Fatalf("SetMaster calls = %q, want none", links.calls)
			}
		})
	}
}

func TestKernelBondLAGBackendHonoursCanceledContext(t *testing.T) {
	t.Parallel()

	links := newFakeBondLinks()
	backend := netio.NewKernelBondLAGBackend(netio.KernelBondLAGBackendConfig{Links: links})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := backend.RemoveMember(ctx, "bond0", "eth0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("RemoveMember error = %v, want context.Canceled", err)
	}
	if len(links.calls) != 0 {
		t.Fatalf("SetMaster calls = %q, want none", links.calls)
	}
}

func TestKernelBondLAGBackendRejectsUnsafeInterfaceNames(t *testing.T) {
	t.Parallel()

	backend := netio.NewKernelBondLAGBackend(netio.KernelBondLAGBackendConfig{Links: newFakeBondLinks()})

	err := backend.RemoveMember(t.Context(), "../bond0", "eth0")
	if !errors.Is(err, netio.ErrInvalidLAGInterfaceName) {
		t.Fatalf("RemoveMember error = %v, want %v", err, netio.ErrInvalidLAGInterfaceName)
	}

	err = backend.AddMember(t.Context(), "bond0", "eth0/../../x")
	if !errors.Is(err, netio.ErrInvalidLAGInterfaceName) {
		t.Fatalf("AddMember error = %v, want %v", err, netio.ErrInvalidLAGInterfaceName)
	}
}

var errFakeNoLink = errors.New("fake: no such link")

// fakeBondLinks holds bond0 (10) with eth0, bond1 (11) with eth9, the bridge
// br0 (12) and the standalone link eth1.
type fakeBondLinks struct {
	links map[string]netio.KernelBondLink
	calls []string
}

func newFakeBondLinks() *fakeBondLinks {
	return &fakeBondLinks{links: map[string]netio.KernelBondLink{
		"bond0": {Index: 10, Kind: "bond"},
		"bond1": {Index: 11, Kind: "bond"},
		"br0":   {Index: 12, Kind: "bridge"},
		"eth0":  {Index: 20, Kind: "veth", MasterIndex: 10},
		"eth1":  {Index: 21, Kind: "veth"},
		"eth9":  {Index: 29, Kind: "veth", MasterIndex: 11},
	}}
}

func (f *fakeBondLinks) Link(_ context.Context, name string) (netio.KernelBondLink, error) {
	link, ok := f.links[name]
	if !ok {
		return netio.KernelBondLink{}, errFakeNoLink
	}
	return link, nil
}

func (f *fakeBondLinks) SetMaster(_ context.Context, index, masterIndex int) error {
	for name, link := range f.links {
		if link.Index == index {
			f.calls = append(f.calls, fmt.Sprintf("%s master %d", name, masterIndex))
			link.MasterIndex = masterIndex
			f.links[name] = link
			return nil
		}
	}
	return errFakeNoLink
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
	if _, ok := backend.(*netio.OVSDBLAGBackend); !ok {
		t.Fatalf("NewLAGActuatorBackend returned %T, want *OVSDBLAGBackend", backend)
	}
}

func TestNewLAGActuatorBackendSelectsNetworkManager(t *testing.T) {
	t.Parallel()

	backend, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendNetworkManager,
		OwnerPolicy: netio.LAGOwnerPolicyNetworkManagerDBus,
	})
	if err != nil {
		t.Fatalf("NewLAGActuatorBackend: %v", err)
	}
	if _, ok := backend.(*netio.NetworkManagerLAGBackend); !ok {
		t.Fatalf("NewLAGActuatorBackend returned %T, want *NetworkManagerLAGBackend", backend)
	}
}

func TestNewLAGActuatorBackendRejectsNetworkManagerWithoutDBusOwnerPolicy(t *testing.T) {
	t.Parallel()

	_, err := netio.NewLAGActuatorBackend(netio.LAGActuatorConfig{
		Mode:        netio.LAGActuatorModeEnforce,
		Backend:     netio.LAGActuatorBackendNetworkManager,
		OwnerPolicy: netio.LAGOwnerPolicyAllowExternal,
	})
	if !errors.Is(err, netio.ErrUnsupportedLAGOwnerPolicy) {
		t.Fatalf("NewLAGActuatorBackend error = %v, want %v",
			err, netio.ErrUnsupportedLAGOwnerPolicy)
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
