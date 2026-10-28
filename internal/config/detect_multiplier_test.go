package config_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
)

func TestLoadRejectsWrappedDetectMultiplier(t *testing.T) {
	t.Parallel()
	for _, field := range []struct{ name, yaml string }{
		{"bfd default", "bfd:\n  default_detect_multiplier: %s\n"},
		{"echo default", "echo:\n  default_detect_multiplier: %s\n"},
		{"vxlan default", "vxlan:\n  default_detect_multiplier: %s\n"},
		{"geneve default", "geneve:\n  default_detect_multiplier: %s\n"},
		{"session", "sessions:\n  - peer: 192.0.2.1\n    detect_mult: %s\n"},
		{"echo peer", "echo:\n  peers:\n    - detect_mult: %s\n"},
		{"vxlan peer", "vxlan:\n  peers:\n    - detect_mult: %s\n"},
		{"geneve peer", "geneve:\n  peers:\n    - detect_mult: %s\n"},
		{"micro-BFD group", "micro_bfd:\n  enabled: true\n  groups:\n    - detect_mult: %s\n"},
	} {
		t.Run(field.name, func(t *testing.T) {
			t.Parallel()
			for _, value := range []string{"4294967299", "-1", "256", "3.5", `"256"`, "true"} {
				_, err := config.Load(writeTemp(t, fmt.Sprintf(field.yaml, value)))
				if !errors.Is(err, config.ErrDetectMultiplierRange) {
					t.Errorf("%s detect multiplier %s: error %v, want ErrDetectMultiplierRange", field.name, value, err)
				}
			}
			valid := []string{"3", "255", `"3"`, `"0xff"`}
			if field.name != "bfd default" {
				valid = append(valid, "0")
			}
			for _, value := range valid {
				if _, err := config.Load(writeTemp(t, fmt.Sprintf(field.yaml, value))); err != nil {
					t.Errorf("rejected %s valid multiplier %s: %v", field.name, value, err)
				}
			}
		})
	}
}

func TestLoadRejectsMultiplierListCoercion(t *testing.T) {
	t.Parallel()
	for _, section := range []string{"echo", "vxlan", "geneve", "micro_bfd"} {
		key := "peers"
		if section == "micro_bfd" {
			key = "groups"
		}
		for _, value := range []string{
			"    detect_mult: 4294967299\n",
			"    - invalid\n    - detect_mult: 4294967299\n",
		} {
			content := fmt.Sprintf("%s:\n  %s:\n%s", section, key, value)
			if _, err := config.Load(writeTemp(t, content)); !errors.Is(err, config.ErrInvalidPeerListShape) {
				t.Errorf("%s.%s shape error = %v, want ErrInvalidPeerListShape", section, key, err)
			}
		}
	}
}

func TestLoadRejectsCaseFoldedMultiplierOverflow(t *testing.T) {
	t.Parallel()
	for _, content := range []string{
		"sessions:\n  - peer: 192.0.2.1\n    DETECT_MULT: 4294967299\n",
		"SESSIONS:\n  - peer: 192.0.2.1\n    detect_mult: 4294967299\n",
		"echo:\n  peers:\n    - DETECT_MULT: 4294967299\n",
		"vxlan:\n  PEERS:\n    - detect_mult: 4294967299\n",
		"GENEVE:\n  peers:\n    - detect_mult: 4294967299\n",
		"micro_bfd:\n  enabled: true\n  GROUPS:\n    - detect_mult: 4294967299\n",
		"bfd:\n  DEFAULT_DETECT_MULTIPLIER: 4294967299\n",
		"unsolicited:\n  SESSION_DEFAULTS:\n    detect_mult: 4294967299\n",
	} {
		if _, err := config.Load(writeTemp(t, content)); err == nil {
			t.Errorf("case-folded input bypassed range validation: %q", content)
		}
	}
	if _, err := config.Load(writeTemp(t,
		"sessions:\n  - peer: 192.0.2.1\n    DETECT_MULT: 3\n")); err != nil {
		t.Fatalf("rejected valid case-folded multiplier: %v", err)
	}
	conflicting := "sessions:\n  - peer: 192.0.2.1\n    detect_mult: 3\n    DETECT_MULT: 4\n"
	if _, err := config.Load(writeTemp(t, conflicting)); !errors.Is(err, config.ErrAmbiguousConfigKey) {
		t.Fatalf("conflicting aliases error = %v, want ErrAmbiguousConfigKey", err)
	}
}
