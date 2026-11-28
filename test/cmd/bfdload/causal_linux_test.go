//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

func TestWorkloadTXOnlyBlackholeFlagIsExplicitAndFixtureOnly(t *testing.T) {
	t.Parallel()
	for _, option := range []string{"--tx-only-blackhole", "--tx-only-blackhole=true", "--tx-only-blackhole=false"} {
		cfg, err := parseWorkloadConfig([]string{
			"fixture", "--sessions=1", option, "--session=127.0.0.1:3784,127.31.0.2:3784",
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(cfg.Profile)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			TXOnly *bool `json:"tx_only_blackhole"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.TXOnly == nil || *decoded.TXOnly != (option != "--tx-only-blackhole=false") {
			t.Fatalf("explicit fault direction was not recorded: %s", data)
		}
	}
	for _, options := range [][]string{
		{"--tx-only-blackhole=invalid"}, {"--tx-only-blackhole", "--tx-only-blackhole=false"},
	} {
		args := append([]string{"fixture", "--sessions=1"}, options...)
		args = append(args, "--session=127.0.0.1:3784,127.31.0.2:3784")
		if _, err := parseWorkloadConfig(args); !errors.Is(err, bfdload.ErrProfile) {
			t.Fatalf("invalid fault direction accepted before binding: options=%v error=%v", options, err)
		}
	}
}
