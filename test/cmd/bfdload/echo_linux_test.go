//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/bfdload"
)

func TestEchoWorkloadArgumentSelectsWireContract(t *testing.T) {
	t.Parallel()
	cfg, err := parseWorkloadConfig([]string{
		"fixture", "--echo", "--sessions=1", "--session=127.0.0.1:3785,127.31.0.2:3785",
	})
	if err != nil {
		t.Fatalf("Echo selection did not admit its wire manifest: %v", err)
	}
	encoded, err := json.Marshal(cfg.Profile)
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Echo bool `json:"echo"`
	}
	if err := json.Unmarshal(encoded, &recorded); err != nil {
		t.Fatal(err)
	}
	if !recorded.Echo {
		t.Fatal("Echo selection was not recorded in the workload report")
	}
	for _, args := range [][]string{
		{"fixture", "--echo", "--echo=false", "--sessions=1", "--session=127.0.0.1:3785,127.31.0.2:3785"},
		{"fixture", "--echo=invalid", "--sessions=1", "--session=127.0.0.1:3785,127.31.0.2:3785"},
		{"fixture", "--echo=false", "--sessions=1", "--session=127.0.0.1:3785,127.31.0.2:3785"},
		{"fixture", "--echo", "--sessions=1", "--session=127.0.0.1:3784,127.31.0.2:3785"},
	} {
		if _, err := parseWorkloadConfig(args); !errors.Is(err, bfdload.ErrProfile) {
			t.Fatalf("invalid mode/port combination %v accepted: %v", args, err)
		}
	}
}
