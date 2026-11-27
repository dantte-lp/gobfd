package netio_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/netio"
)

func TestValidateInterfaceName(t *testing.T) {
	t.Parallel()

	const maxValidLen = 15
	tests := []struct {
		name    string
		ifName  string
		wantErr bool
	}{
		{name: "standard ethernet", ifName: "eth0", wantErr: false},
		{name: "vlan subinterface", ifName: "eth0.100", wantErr: false},
		{name: "hyphen and underscore", ifName: "br-lan_0", wantErr: false},
		{name: "max 15 byte name", ifName: strings.Repeat("a", maxValidLen), wantErr: false},
		{name: "single char", ifName: "a", wantErr: false},
		{name: "leading dot valid", ifName: ".eth0", wantErr: false},
		{name: "empty", ifName: "", wantErr: true},
		{name: "dot", ifName: ".", wantErr: true},
		{name: "dotdot", ifName: "..", wantErr: true},
		{name: "16 byte IFNAMSIZ", ifName: strings.Repeat("a", maxValidLen+1), wantErr: true},
		{name: "slash", ifName: "eth/0", wantErr: true},
		{name: "colon alias", ifName: "eth0:0", wantErr: true},
		{name: "nul byte", ifName: "eth\x000", wantErr: true},
		{name: "space", ifName: "eth 0", wantErr: true},
		{name: "tab", ifName: "eth\t0", wantErr: true},
		{name: "newline", ifName: "eth\n0", wantErr: true},
		{name: "vertical tab", ifName: "eth\v0", wantErr: true},
		{name: "form feed", ifName: "eth\f0", wantErr: true},
		{name: "carriage return", ifName: "eth\r0", wantErr: true},
		{name: "latin1 nbsp 0xa0 byte", ifName: "eth\xa00", wantErr: true},
		{name: "utf8 nbsp u00a0", ifName: "eth\u00a00", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := netio.ValidateInterfaceName(tt.ifName)
			if tt.wantErr {
				if !errors.Is(err, netio.ErrInvalidInterfaceName) {
					t.Fatalf("ValidateInterfaceName(%q) = %v, want ErrInvalidInterfaceName", tt.ifName, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateInterfaceName(%q) unexpected error: %v", tt.ifName, err)
			}
		})
	}
}

func TestLAGBackendsUseUnifiedValidateInterfaceName(t *testing.T) {
	t.Parallel()

	backends := []struct {
		name    string
		backend netio.LAGActuatorBackend
	}{
		{
			name:    "kernel-bond",
			backend: netio.NewKernelBondLAGBackend(netio.KernelBondLAGBackendConfig{SysfsRoot: t.TempDir()}),
		},
		{name: "ovs", backend: netio.NewOVSLAGBackend(netio.OVSLAGBackendConfig{})},
		{name: "ovsdb", backend: netio.NewOVSDBLAGBackend(netio.OVSDBLAGBackendConfig{})},
		{name: "networkmanager", backend: netio.NewNetworkManagerLAGBackend(netio.NetworkManagerLAGBackendConfig{})},
	}
	invalidNames := []string{"", ".", "..", "eth0:1", "eth 0", "eth\t0", "eth\n0", strings.Repeat("x", 16)}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()
			for _, bad := range invalidNames {
				for _, err := range []error{
					b.backend.RemoveMember(context.Background(), bad, "eth0"),
					b.backend.AddMember(context.Background(), "bond0", bad),
				} {
					if !errors.Is(err, netio.ErrInvalidInterfaceName) ||
						!errors.Is(err, netio.ErrInvalidLAGInterfaceName) {
						t.Fatalf("%s invalid name %q err = %v, want ErrInvalidInterfaceName & ErrInvalidLAGInterfaceName",
							b.name, bad, err)
					}
				}
			}
		})
	}
}
