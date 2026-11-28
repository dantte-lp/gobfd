//go:build interop_testcontainers

package interop_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dantte-lp/gobfd/internal/config"
)

type craftedProfile uint8

const (
	craftedSingleHop craftedProfile = iota
	craftedMultihopTTL
)

func selectCraftedConfig(baseline []byte, profile craftedProfile) ([]byte, error) {
	if profile != craftedSingleHop && profile != craftedMultihopTTL {
		return nil, fmt.Errorf("crafted profile %d: %w", profile, errTimerEvidence)
	}
	if profile == craftedMultihopTTL {
		text := string(baseline)
		if strings.Contains(text, "    auth:\n") {
			return nil, fmt.Errorf("unexpected baseline auth: %w", errTimerEvidence)
		}
		for _, change := range [][2]string{
			{"    type: single_hop\n", "    type: multi_hop\n    multihop_max_hops: 1\n"},
			{"    required_min_rx: \"300ms\"\n", "    required_min_rx: \"3s\"\n"},
			{"    padded_pdu_size: 128\n", "    padded_pdu_size: 128\n    auth:\n" +
				"      type: meticulous_keyed_sha1\n      key_id: 7\n      secret: gobfd-interop\n"},
		} {
			if strings.Count(text, change[0]) != 1 {
				return nil, fmt.Errorf("ambiguous crafted fixture: %w", errTimerEvidence)
			}
			text = strings.Replace(text, change[0], change[1], 1)
		}
		text = strings.Replace(text, "# One Active single-hop session", "# One Active authenticated multihop session", 1)
		text = strings.Replace(text, "3 * max(300ms, 300ms) = 900ms", "3 * max(3s, 300ms) = 9s", 1)
		return []byte(text), nil
	}
	return baseline, nil
}

func TestTimerReloadTTLStartupProfile(t *testing.T) {
	t.Parallel()
	baseline, err := os.ReadFile("gobfd/crafted-peer.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []craftedProfile{craftedSingleHop, craftedMultihopTTL} {
		selected, selectErr := selectCraftedConfig(baseline, profile)
		if selectErr != nil {
			t.Fatal(selectErr)
		}
		if profile == craftedSingleHop && string(selected) != string(baseline) {
			t.Fatal("single-hop baseline changed")
		}
		path := filepath.Join(t.TempDir(), "startup.yml")
		if writeErr := os.WriteFile(path, selected, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		cfg, loadErr := config.Load(path)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if len(cfg.Sessions) != 1 {
			t.Fatal("unexpected peer count")
		}
		peer := cfg.Sessions[0]
		if peer.Peer != fuzzIP || peer.Local != gobfdIP || peer.DesiredMinTx != 300*time.Millisecond ||
			peer.DetectMult != 3 || peer.PaddedPduSize != 128 {
			t.Fatalf("startup changed unrelated fields: %+v", peer)
		}
		if profile == craftedMultihopTTL {
			if peer.Type != "multi_hop" || !peer.MultihopMaxHopsSet || peer.MultihopMaxHops != 1 ||
				peer.RequiredMinRx != 3*time.Second || peer.Auth.Type != "meticulous_keyed_sha1" ||
				peer.Auth.KeyID != 7 || peer.Auth.Secret != "gobfd-interop" {
				t.Fatalf("invalid multihop startup: %+v", peer)
			}
		}
	}
	if _, err := selectCraftedConfig(baseline, 9); err == nil {
		t.Fatal("unknown profile accepted")
	}
}
