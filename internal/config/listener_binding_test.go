package config_test

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
)

func TestValidateListenerInterfaceBindings(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		echo       bool
		disabled   bool
		locals     [2]string
		types      [2]string
		interfaces [2]string
		conflict   bool
	}{
		{
			name: "control IPv4 conflict", locals: [2]string{"192.0.2.1", "192.0.2.1"},
			interfaces: [2]string{"eth0", "eth1"}, conflict: true,
		},
		{
			name:   "control canonical IPv6 and default type alias",
			locals: [2]string{"2001:db8::1", "2001:0db8:0000:0:0:0:0:1"}, types: [2]string{"", "single_hop"},
			interfaces: [2]string{"eth0", "eth1"}, conflict: true,
		},
		{
			name: "control empty interface conflicts with named", locals: [2]string{"192.0.2.1", "192.0.2.1"},
			interfaces: [2]string{"", "eth0"}, conflict: true,
		},
		{
			name: "control same interface", locals: [2]string{"192.0.2.1", "192.0.2.1"},
			interfaces: [2]string{"eth0", "eth0"},
		},
		{
			name: "control distinct types", locals: [2]string{"192.0.2.1", "192.0.2.1"},
			types: [2]string{"single_hop", "multi_hop"}, interfaces: [2]string{"eth0", ""},
		},
		{
			name: "control distinct locals", locals: [2]string{"192.0.2.1", "192.0.2.2"},
			interfaces: [2]string{"eth0", "eth1"},
		},
		{
			name: "control IPv4 differs from mapped", locals: [2]string{"192.0.2.1", "::ffff:192.0.2.1"},
			interfaces: [2]string{"eth0", "eth1"},
		},
		{
			name: "control canonical mapped addresses", locals: [2]string{"::ffff:192.0.2.1", "::ffff:c000:201"},
			interfaces: [2]string{"eth0", "eth1"}, conflict: true,
		},
		{name: "control no local listener", interfaces: [2]string{"eth0", "eth1"}},
		{
			name: "echo IPv4 conflict", echo: true, locals: [2]string{"192.0.2.1", "192.0.2.1"},
			interfaces: [2]string{"eth0", "eth1"}, conflict: true,
		},
		{
			name: "echo canonical IPv6", echo: true, locals: [2]string{"2001:db8::1", "2001:0db8:0000:0:0:0:0:1"},
			interfaces: [2]string{"eth0", "eth1"}, conflict: true,
		},
		{
			name: "echo empty interface conflicts with named", echo: true, locals: [2]string{"192.0.2.1", "192.0.2.1"},
			interfaces: [2]string{"", "eth0"}, conflict: true,
		},
		{
			name: "echo same interface", echo: true, locals: [2]string{"192.0.2.1", "192.0.2.1"},
			interfaces: [2]string{"eth0", "eth0"},
		},
		{
			name: "echo distinct locals", echo: true, locals: [2]string{"192.0.2.1", "192.0.2.2"},
			interfaces: [2]string{"eth0", "eth1"},
		},
		{
			name: "echo IPv4 differs from mapped", echo: true, locals: [2]string{"192.0.2.1", "::ffff:192.0.2.1"},
			interfaces: [2]string{"eth0", "eth1"},
		},
		{
			name: "echo disabled", echo: true, disabled: true, locals: [2]string{"192.0.2.1", "192.0.2.1"},
			interfaces: [2]string{"eth0", "eth1"},
		},
		{name: "echo no local listener", echo: true, interfaces: [2]string{"eth0", "eth1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, reverse := range []bool{false, true} {
				cfg := config.DefaultConfig()
				for i, peer := range [...]string{"198.51.100.1", "198.51.100.2"} {
					if tt.echo {
						cfg.Echo.Enabled = !tt.disabled
						cfg.Echo.Peers = append(cfg.Echo.Peers, config.EchoPeerConfig{
							Peer: peer, Local: tt.locals[i], Interface: tt.interfaces[i],
						})
					} else {
						cfg.Sessions = append(cfg.Sessions, config.SessionConfig{
							Peer: peer, Local: tt.locals[i], Type: tt.types[i], Interface: tt.interfaces[i],
						})
					}
				}
				if reverse {
					slices.Reverse(cfg.Sessions)
					slices.Reverse(cfg.Echo.Peers)
				}
				err := config.Validate(cfg)
				if (err != nil) != tt.conflict {
					t.Errorf("Validate(reverse=%t) = %v, want conflict=%t", reverse, err, tt.conflict)
				}
				if tt.conflict {
					var conflict *config.ListenerBindingConflictError
					wantField := config.ListenerBindingControlInterface
					if tt.echo {
						wantField = config.ListenerBindingEchoInterface
					}
					if !errors.Is(err, config.ErrAmbiguousListenerInterface) ||
						!errors.As(err, &conflict) || conflict.FieldID() != wantField {
						t.Errorf("Validate(reverse=%t) = %v, want typed %s conflict", reverse, err, wantField)
					}
				}
			}
		})
	}
}

func TestLoadListenerBindingConflictReportsOnlyBoundedField(t *testing.T) {
	t.Parallel()
	for _, echo := range []bool{false, true} {
		t.Run(strconv.FormatBool(echo), func(t *testing.T) {
			t.Parallel()
			section, enabled, wantField := "sessions", "", config.ListenerBindingControlInterface
			if echo {
				section, enabled, wantField = "echo", "  enabled: true\n  peers:\n", config.ListenerBindingEchoInterface
			}
			marker := "private-interface-" + strings.Repeat("x", 4096)
			yaml := fmt.Sprintf("%s:\n%s    - peer: 198.51.100.1\n      local: 2001:db8::1\n"+
				"      interface: %s\n    - peer: 198.51.100.2\n      local: 2001:0db8::1\n      interface: eth1\n",
				section, enabled, marker)
			_, err := config.Load(writeTemp(t, yaml))
			var conflict *config.ListenerBindingConflictError
			if !errors.As(err, &conflict) || !errors.Is(err, config.ErrAmbiguousListenerInterface) ||
				conflict.FieldID() != wantField {
				t.Fatalf("Load error = %v, want typed %s conflict", err, wantField)
			}
			wantMessage := config.ErrAmbiguousListenerInterface.Error() + ": " + wantField.String()
			if conflict.Error() != wantMessage || strings.Contains(err.Error(), marker) ||
				strings.Contains(err.Error(), "198.51.100") || strings.Contains(err.Error(), "2001:") {
				t.Fatalf("conflict error contains configuration values or unexpected identifiers: %s", conflict)
			}
		})
	}
}
