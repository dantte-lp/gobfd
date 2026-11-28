package config_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/bfd"
	"github.com/dantte-lp/gobfd/internal/config"
)

func TestLoadPaddingRawValidation(t *testing.T) {
	t.Parallel()
	for _, field := range []struct{ name, yaml string }{
		{"default", "bfd:\n  default_padded_pdu_size: %s\n"},
		{"session", "sessions:\n  - peer: 192.0.2.1\n    padded_pdu_size: %s\n"},
		{"uppercase", "SESSIONS:\n  - peer: 192.0.2.1\n    PADDED_PDU_SIZE: %s\n"},
		{"uppercase-default", "bfd:\n  DEFAULT_PADDED_PDU_SIZE: %s\n"},
	} {
		for i, scalar := range []string{
			"65664", "65536", "-1", "128.5", "true", "false", ".nan", ".inf",
			`"65664"`, `"128.5"`, `"true"`, "[128]", "{value: 128}",
		} {
			t.Run(fmt.Sprintf("%s/%d", field.name, i), func(t *testing.T) {
				t.Parallel()
				_, err := config.Load(writeTemp(t, fmt.Sprintf(field.yaml, scalar)))
				if !errors.Is(err, bfd.ErrInvalidPaddedPduSize) {
					t.Fatalf("scalar %s: error = %v, want ErrInvalidPaddedPduSize", scalar, err)
				}
				if strings.Contains(err.Error(), scalar) {
					t.Fatalf("raw value leaked into bounded error: %v", err)
				}
			})
		}
	}
}

func TestLoadPaddingPolicy(t *testing.T) {
	t.Parallel()
	for _, global := range []bool{false, true} {
		for _, auth := range []string{
			"", "none", "simple_password", "keyed_md5", "meticulous_keyed_md5", "keyed_sha1", "meticulous_keyed_sha1",
		} {
			for _, size := range []uint16{0, 23, 24, 25, 26, 9000, 9001} {
				t.Run(fmt.Sprintf("global=%t/%s/%d", global, auth, size), func(t *testing.T) {
					t.Parallel()
					contents := "sessions:\n  - peer: 192.0.2.1\n"
					if global {
						contents = fmt.Sprintf("bfd:\n  default_padded_pdu_size: %d\n", size) + contents
					} else {
						contents += fmt.Sprintf("    padded_pdu_size: %d\n", size)
					}
					contents += fmt.Sprintf("    auth:\n      type: %q\n      key_id: 1\n      secret: vector-key\n", auth)
					_, err := config.Load(writeTemp(t, contents))
					minimum := uint16(24)
					if auth != "" && auth != "none" {
						minimum = 26
					}
					invalid := size != 0 && (size < minimum || size > 9000)
					if invalid && !errors.Is(err, bfd.ErrInvalidPaddedPduSize) {
						t.Fatalf("error = %v, want ErrInvalidPaddedPduSize", err)
					}
					if !invalid && err != nil {
						t.Fatalf("Load: %v", err)
					}
				})
			}
		}
	}
}

func TestLoadPaddingCompatibility(t *testing.T) {
	t.Parallel()
	for _, scalar := range []string{"null", "0", `"0"`, "24", `"128"`, `"0x80"`, "128.0", "9000"} {
		contents := fmt.Sprintf("bfd:\n  default_padded_pdu_size: %s\n"+
			"sessions:\n  - peer: 192.0.2.1\n    padded_pdu_size: %s\n", scalar, scalar)
		cfg, err := config.Load(writeTemp(t, contents))
		if err != nil {
			t.Fatalf("scalar %s: %v", scalar, err)
		}
		if cfg.BFD.DefaultPaddedPduSize != cfg.Sessions[0].PaddedPduSize {
			t.Fatalf("default/session disagree: %+v", cfg)
		}
	}
	for _, tc := range []struct {
		global, override int
		invalid          bool
	}{
		{24, 26, false}, {24, 0, true}, {128, 25, true}, {128, 26, false},
	} {
		contents := fmt.Sprintf("bfd:\n  default_padded_pdu_size: %d\n"+
			"sessions:\n  - peer: 192.0.2.1\n    padded_pdu_size: %d\n"+
			"    auth:\n      type: keyed_sha1\n      key_id: 1\n      secret: vector-key\n", tc.global, tc.override)
		_, err := config.Load(writeTemp(t, contents))
		if tc.invalid != errors.Is(err, bfd.ErrInvalidPaddedPduSize) || (!tc.invalid && err != nil) {
			t.Fatalf("default %d override %d: %v", tc.global, tc.override, err)
		}
	}
}

func TestLoadPaddingAmbiguousKeys(t *testing.T) {
	t.Parallel()
	for _, content := range []string{
		"bfd:\n  default_padded_pdu_size: 128\n  DEFAULT_PADDED_PDU_SIZE: 256\n",
		"sessions:\n  - peer: 192.0.2.1\n    padded_pdu_size: 128\n    PADDED_PDU_SIZE: 256\n",
	} {
		if _, err := config.Load(writeTemp(t, content)); !errors.Is(err, config.ErrAmbiguousConfigKey) {
			t.Fatalf("error = %v, want ErrAmbiguousConfigKey", err)
		}
	}
}

func TestValidatePaddingBeforeRuntime(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		global, session uint16
		auth            string
		invalid         bool
	}{
		{0, 0, "keyed_sha1", false},
		{24, 0, "keyed_sha1", true},
		{25, 0, "simple_password", true},
		{24, 26, "keyed_sha1", false},
		{128, 25, "keyed_sha1", true},
		{0, 24, "none", false},
		{9001, 0, "", true},
		{0, 65535, "", true},
	} {
		cfg := config.DefaultConfig()
		cfg.BFD.DefaultPaddedPduSize = tc.global
		cfg.Sessions = []config.SessionConfig{{
			Peer: "192.0.2.1", PaddedPduSize: tc.session,
			Auth: config.AuthConfig{Type: tc.auth, KeyID: 1, Secret: "vector-key"},
		}}
		err := config.Validate(cfg)
		if tc.invalid != errors.Is(err, bfd.ErrInvalidPaddedPduSize) || (!tc.invalid && err != nil) {
			t.Fatalf("global %d session %d auth %s: %v", tc.global, tc.session, tc.auth, err)
		}
	}
}
