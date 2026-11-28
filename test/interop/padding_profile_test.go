//go:build interop_testcontainers

package interop_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fourPeerProfile uint8

const (
	fourPeerDefault fourPeerProfile = iota
	fourPeerMeticulous
)

func loadFourPeerConfig(root string, profile fourPeerProfile) ([]byte, []byte, error) {
	baseline, err := os.ReadFile(filepath.Join(root, "test/interop/gobfd/gobfd.yml"))
	if err != nil {
		return nil, nil, fmt.Errorf("read daemon fixture: %w", err)
	}
	bird, err := os.ReadFile(filepath.Join(root, "test/interop/bird3/bird.conf"))
	if err != nil {
		return nil, nil, fmt.Errorf("read BIRD fixture: %w", err)
	}
	return selectFourPeerConfig(baseline, bird, profile)
}

func selectFourPeerConfig(baseline, bird []byte, profile fourPeerProfile) ([]byte, []byte, error) {
	if profile != fourPeerDefault && profile != fourPeerMeticulous {
		return nil, nil, fmt.Errorf("unknown four-peer profile %d: %w", profile, errTimerEvidence)
	}
	if profile == fourPeerMeticulous {
		daemonLine, birdLine := "      type: keyed_sha1\n", "    authentication keyed sha1;"
		if strings.Count(string(baseline), daemonLine) != 1 || strings.Count(string(bird), birdLine) != 1 {
			return nil, nil, fmt.Errorf("ambiguous meticulous fixture: %w", errTimerEvidence)
		}
		baseline = []byte(strings.Replace(string(baseline), daemonLine, "      type: meticulous_keyed_sha1\n", 1))
		bird = []byte(strings.Replace(string(bird), birdLine, "    authentication meticulous keyed sha1;", 1))
	}
	return baseline, bird, nil
}

func TestFourPeerMeticulousTopologyTestcontainers(t *testing.T) {
	runFourPeerTopology(t, fourPeerMeticulous)
}

func TestMeticulousRFCCompliance(t *testing.T) { runRFCCompliance(t, 5) }

func TestMeticulousTimerReload(t *testing.T) { runTimerReload(t, fourPeerMeticulous) }

func TestTimerReloadPaddingProfiles(t *testing.T) {
	t.Parallel()
	baseline, err := os.ReadFile("gobfd/gobfd.yml")
	if err != nil {
		t.Fatal(err)
	}
	bird, err := os.ReadFile("bird3/bird.conf")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []fourPeerProfile{fourPeerDefault, fourPeerMeticulous} {
		selected, peer, err := selectFourPeerConfig(baseline, bird, profile)
		if err != nil {
			t.Fatal(err)
		}
		wantDaemon, wantBird := string(baseline), string(bird)
		if profile == fourPeerMeticulous {
			wantDaemon = strings.Replace(wantDaemon, "      type: keyed_sha1\n", "      type: meticulous_keyed_sha1\n", 1)
			wantBird = strings.Replace(wantBird, "    authentication keyed sha1;",
				"    authentication meticulous keyed sha1;", 1)
		}
		if string(selected) != wantDaemon || string(peer) != wantBird {
			t.Fatalf("profile %d changed incorrect fixture bytes", profile)
		}
	}
	if _, _, err := selectFourPeerConfig(baseline, bird, 9); err == nil {
		t.Fatal("accepted unknown profile")
	}
	for _, altered := range [][]byte{
		[]byte(strings.ReplaceAll(string(baseline), "      type: keyed_sha1\n", "")),
		append(append([]byte(nil), baseline...), []byte("      type: keyed_sha1\n")...),
	} {
		if _, _, err := selectFourPeerConfig(altered, bird, fourPeerMeticulous); err == nil {
			t.Fatal("accepted missing/duplicate auth selector")
		}
	}
}
