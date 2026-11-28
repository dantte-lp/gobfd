package config_test

import (
	"errors"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
)

func TestMicroPreviewRequiresExplicitOptIn(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"disabled", "dry-run", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			cfg.MicroBFD.Actuator.Mode = mode
			if mode == "disabled" {
				cfg.MicroBFD.Groups = []config.MicroBFDGroupConfig{{
					LAGInterface: "bond0", MemberLinks: []string{"eth0"},
					PeerAddr: "192.0.2.2", LocalAddr: "192.0.2.1", MinActiveLinks: 1,
				}}
			}
			if err := config.Validate(cfg); !errors.Is(err, config.ErrMicroBFDPreviewDisabled) {
				t.Fatalf("validation error = %v, want ErrMicroBFDPreviewDisabled", err)
			}
			cfg.MicroBFD.Enabled = true
			if err := config.Validate(cfg); err != nil {
				t.Fatalf("explicit opt-in rejected: %v", err)
			}
		})
	}
}

func TestMicroPreviewYAMLRequiresOptIn(t *testing.T) {
	t.Parallel()

	for _, enabled := range []string{"", "  enabled: false\n", "  enabled: true\n"} {
		t.Run(enabled, func(t *testing.T) {
			t.Parallel()
			_, err := config.Load(writeTemp(t, "micro_bfd:\n"+enabled+"  groups:\n    - lag_interface: bond0\n"))
			if enabled == "  enabled: true\n" {
				if err != nil {
					t.Fatalf("explicit YAML opt-in rejected: %v", err)
				}
			} else if !errors.Is(err, config.ErrMicroBFDPreviewDisabled) {
				t.Fatalf("YAML validation error = %v, want ErrMicroBFDPreviewDisabled", err)
			}
		})
	}
}
