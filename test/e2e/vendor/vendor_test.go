//go:build e2e_vendor

// Package vendor_test validates the S10.6 optional vendor profile contract.
package vendor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const profileManifestPath = "test/e2e/vendor/profiles.json"

type manifest struct {
	Runtime  runtimeContract `json:"runtime"`
	Profiles []profile       `json:"profiles"`
}

type runtimeContract struct {
	ContainerlabRuntime string `json:"containerlab_runtime"`
	PodmanRequired      bool   `json:"podman_required"`
	PublicCIDefault     string `json:"public_ci_default"`
}

type profile struct {
	ID           string   `json:"id"`
	Vendor       string   `json:"vendor"`
	Platform     string   `json:"platform"`
	ProfileClass string   `json:"profile_class"`
	TopologyNode string   `json:"topology_node"`
	ConfigPaths  []string `json:"config_paths"`
	Images       []string `json:"images"`
	SkipPolicy   string   `json:"skip_policy"`
	Scenario     string   `json:"scenario"`
	Standards    []string `json:"standards"`
	Evidence     []string `json:"evidence"`
}

func TestVendorProfilesContract(t *testing.T) {
	m := readManifest(t)

	if m.Runtime.ContainerlabRuntime != "podman" {
		t.Fatalf("containerlab runtime = %q, want podman", m.Runtime.ContainerlabRuntime)
	}
	if !m.Runtime.PodmanRequired {
		t.Fatal("podman runtime must be explicit for S10.6")
	}
	if m.Runtime.PublicCIDefault != "skip-topology" {
		t.Fatalf("public CI default = %q, want skip-topology", m.Runtime.PublicCIDefault)
	}

	required := map[string]bool{
		"arista-ceos":   false,
		"nokia-srlinux": false,
		"cisco-xrd":     false,
		"sonic-vs":      false,
		"vyos":          false,
		"frr":           false,
	}
	allowedSkipPolicy := map[string]bool{
		"licensed-vendor-image": true,
		"manual-only-image":     true,
		"missing-image":         true,
	}

	for _, p := range m.Profiles {
		if _, ok := required[p.ID]; !ok {
			t.Fatalf("unexpected profile id %q", p.ID)
		}
		required[p.ID] = true
		if p.Vendor == "" || p.Platform == "" || p.TopologyNode == "" || p.Scenario == "" {
			t.Fatalf("profile %s has incomplete metadata: %+v", p.ID, p)
		}
		if len(p.Images) == 0 {
			t.Fatalf("profile %s has no image references", p.ID)
		}
		if !allowedSkipPolicy[p.SkipPolicy] {
			t.Fatalf("profile %s skip policy = %q", p.ID, p.SkipPolicy)
		}
		if !slices.Contains(p.Standards, "RFC 5880") || !slices.Contains(p.Standards, "RFC 5881") {
			t.Fatalf("profile %s must anchor base single-hop BFD standards, got %v", p.ID, p.Standards)
		}
		if strings.Contains(p.Scenario, "bgp") && !slices.Contains(p.Standards, "RFC 5882") {
			t.Fatalf("profile %s scenario %q must include RFC 5882", p.ID, p.Scenario)
		}
		if len(p.Evidence) == 0 {
			t.Fatalf("profile %s has no evidence commands", p.ID)
		}
		var configText strings.Builder
		for _, path := range p.ConfigPaths {
			data, err := os.ReadFile(repoPath(t, path))
			if err != nil {
				t.Fatalf("profile %s config path %s: %v", p.ID, path, err)
			}
			configText.Write(data)
			configText.WriteByte('\n')
		}
		if slices.Contains(p.Standards, "RFC 8971") && !strings.Contains(configText.String(), "bfd vtep evpn") {
			t.Fatalf("profile %s claims RFC 8971 but no config contains bfd vtep evpn", p.ID)
		}
		if p.ID == "arista-ceos" && slices.Contains(p.Evidence, "show bfd peers protocol VXLAN") {
			t.Fatalf("profile %s evidence claims VXLAN BFD while current config is single-hop BGP BFD", p.ID)
		}
	}

	for id, seen := range required {
		if !seen {
			t.Fatalf("required profile %s is missing", id)
		}
	}
	writeJSONArtifact(t, "vendor-profiles.json", m)
}

func TestPrimaryVendorSetAndCiscoDeferred(t *testing.T) {
	m := readManifest(t)

	wantClass := map[string]string{
		"arista-ceos":   "primary",
		"nokia-srlinux": "primary",
		"sonic-vs":      "primary",
		"vyos":          "primary",
		"frr":           "baseline",
		"cisco-xrd":     "deferred",
	}
	for _, p := range m.Profiles {
		want, ok := wantClass[p.ID]
		if !ok {
			t.Fatalf("unexpected profile %s", p.ID)
		}
		if p.ProfileClass != want {
			t.Fatalf("profile %s class = %q, want %q", p.ID, p.ProfileClass, want)
		}
		if p.ID == "arista-ceos" && !slices.Contains(p.Images, "localhost/ceos:4.36.0.1F") {
			t.Fatalf("profile %s images = %v, want local cEOS 4.36.0.1F candidate", p.ID, p.Images)
		}
		delete(wantClass, p.ID)
	}
	if len(wantClass) > 0 {
		t.Fatalf("missing vendor profile classes: %v", wantClass)
	}
}

func TestContainerlabTopologyMatchesManifest(t *testing.T) {
	m := readManifest(t)
	topology, err := os.ReadFile(repoPath(t, "test/interop-clab/gobfd-vendors.clab.yml"))
	if err != nil {
		t.Fatalf("read containerlab topology: %v", err)
	}
	runner, err := os.ReadFile(repoPath(t, "test/interop-clab/run.sh"))
	if err != nil {
		t.Fatalf("read containerlab runner: %v", err)
	}
	for _, p := range m.Profiles {
		if !strings.Contains(string(topology), p.TopologyNode+":") {
			t.Fatalf("topology does not define node %q for profile %s", p.TopologyNode, p.ID)
		}
		for _, image := range p.Images {
			if strings.Contains(string(topology), image) || strings.Contains(string(runner), image) {
				continue
			}
			if p.ID == "cisco-xrd" && strings.Contains(string(runner), "ios-xr/xrd-control-plane") {
				continue
			}
			t.Fatalf("profile %s image %q is not referenced by topology or runner", p.ID, image)
		}
	}
	writeJSONArtifact(t, "skip-summary.json", map[string]any{
		"topology":            "test/interop-clab/gobfd-vendors.clab.yml",
		"runner":              "test/interop-clab/run.sh",
		"primary_profiles":    []string{"arista-ceos", "nokia-srlinux", "sonic-vs", "vyos"},
		"baseline_profiles":   []string{"frr"},
		"deferred_profiles":   []string{"cisco-xrd"},
		"missing_images":      "documented as skips",
		"public_ci_default":   m.Runtime.PublicCIDefault,
		"cisco_xrd_profile":   "deferred until an operator-provided XRd image is available",
		"primary_skip_policy": "primary profile images are explicit evidence, not public CI requirements",
	})
}

func readManifest(t *testing.T) manifest {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, profileManifestPath))
	if err != nil {
		t.Fatalf("read %s: %v", profileManifestPath, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode %s: %v", profileManifestPath, err)
	}
	return m
}

func repoPath(t *testing.T, path string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return filepath.Join(wd, path)
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("repository root not found from %s", wd)
		}
		wd = parent
	}
}

func writeJSONArtifact(t *testing.T, name string, value any) {
	t.Helper()
	reportDir := os.Getenv("E2E_VENDOR_REPORT_DIR")
	if reportDir == "" {
		return
	}
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("create report dir: %v", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, name), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
