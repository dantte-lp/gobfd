package interop_test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// exabgpPinPattern selects the lines that pin ExaBGP source and its runtime base.
var exabgpPinPattern = regexp.MustCompile(`(?m)^\s*(ADD --checksum=sha256:\S+|` +
	`https://codeload\.github\.com/Exa-Networks/exabgp/\S+|` +
	`FROM docker\.io/library/debian:trixie-slim@sha256:[0-9a-f]{64}|` +
	`org\.opencontainers\.image\.version="[^"]+"|` +
	`io\.gobfd\.upstream\.revision="[^"]+")`)

func exabgpPins(contents string) []string {
	pins := make([]string, 0, 5)
	for _, match := range exabgpPinPattern.FindAllStringSubmatch(contents, -1) {
		pins = append(pins, strings.TrimSpace(match[1]))
	}
	return pins
}

// TestExaBGPAnycastImageSharesPeerPin keeps the anycast integration image on the
// exact ExaBGP source and Debian base of the shared interop peer recipe.
func TestExaBGPAnycastImageSharesPeerPin(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	peer := exabgpPins(readContractFile(t, "ExaBGP peer recipe", filepath.Join(root, "test/interop/exabgp/Containerfile")))
	anycast := exabgpPins(readContractFile(t, "ExaBGP anycast image",
		filepath.Join(root, "deployments/docker/Containerfile.exabgp")))
	if len(peer) != 5 {
		t.Fatalf("ExaBGP peer recipe pins = %q, want 5", peer)
	}
	if !slices.Equal(peer, anycast) {
		t.Fatalf("ExaBGP anycast pins differ from peer recipe\npeer:    %q\nanycast: %q", peer, anycast)
	}
}

// TestExaBGPAnycastComposeUsesSharedCappedPeers verifies that the anycast
// topology builds GoBGP from the shared v4 recipe and caps every service.
func TestExaBGPAnycastComposeUsesSharedCappedPeers(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	contents := readContractFile(t, "anycast Compose",
		filepath.Join(root, "deployments/integrations/exabgp-anycast/compose.yml"))
	for _, legacy := range []string{"docker.io/jauderho/gobgp", "ghcr.io/exa-networks/exabgp", "up --build"} {
		if strings.Contains(contents, legacy) {
			t.Errorf("anycast Compose retains %q", legacy)
		}
	}
	var topology struct {
		Volumes  map[string]any `yaml:"volumes"`
		Services map[string]struct {
			Image        string `yaml:"image"`
			CPUs         any    `yaml:"cpus"`
			MemLimit     string `yaml:"mem_limit"`
			MemswapLimit string `yaml:"memswap_limit"`
			PidsLimit    int    `yaml:"pids_limit"`
			Build        struct {
				Context string `yaml:"context"`
			} `yaml:"build"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(contents), &topology); err != nil {
		t.Fatalf("decode anycast Compose: %v", err)
	}
	// The guarded project cleanup refuses mutable labelled volumes.
	if len(topology.Volumes) != 0 {
		t.Errorf("anycast Compose declares named volumes %v, want container-local storage", topology.Volumes)
	}
	if got := topology.Services["gobgp"].Build.Context; got != "../../../test/interop/gobgp" {
		t.Errorf("gobgp build context = %q, want shared recipe ../../../test/interop/gobgp", got)
	}
	for name, service := range topology.Services {
		if service.Image != "" {
			t.Errorf("service %s uses image %q, want a repository build", name, service.Image)
		}
		if service.CPUs == nil || service.MemLimit != "256m" || service.MemswapLimit != "256m" || service.PidsLimit != 128 {
			t.Errorf("service %s caps = cpus %v mem %q swap %q pids %d, want 1/256m/256m/128",
				name, service.CPUs, service.MemLimit, service.MemswapLimit, service.PidsLimit)
		}
	}
}
