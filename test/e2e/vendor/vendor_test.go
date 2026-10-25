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

	"go.yaml.in/yaml/v3"

	"github.com/dantte-lp/gobfd/test/internal/vendorprofiles"
)

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
		if p.ID == "frr" {
			if p.ExecutionPolicy != vendorprofiles.OwnedReceiptRequired || !m.AllowsNode(p.TopologyNode) {
				t.Fatalf("FRR profile must require the owned receipt: %+v", p)
			}
		} else if p.ExecutionPolicy != vendorprofiles.RetainedUnavailable || m.AllowsNode(p.TopologyNode) {
			t.Fatalf("profile %s must retain unavailable qualification: %+v", p.ID, p)
		}
		if p.Vendor == "" || p.Platform == "" || p.TopologyNode == "" || p.Scenario == "" {
			t.Fatalf("profile %s has incomplete metadata: %+v", p.ID, p)
		}
		if p.Build != nil {
			if p.ID != "frr" || p.Build.Context != "test/interop/frr" ||
				p.Build.Dockerfile != "Containerfile" || len(p.Images) != 0 {
				t.Fatalf("profile %s has an unsupported local build: %+v", p.ID, p)
			}
			contextPath := repoPath(t, p.Build.Context)
			info, err := os.Stat(contextPath)
			if err != nil || !info.IsDir() {
				t.Fatalf("profile %s build context is not a directory: %v", p.ID, err)
			}
			info, err = os.Stat(filepath.Join(contextPath, p.Build.Dockerfile))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("profile %s build recipe is not a regular file: %v", p.ID, err)
			}
		} else if len(p.Images) == 0 || p.ID == "frr" {
			t.Fatalf("profile %s requires image references or the shared FRR build recipe", p.ID)
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
	data, err := os.ReadFile(repoPath(t, "test/interop-clab/gobfd-vendors.clab.yml"))
	if err != nil {
		t.Fatalf("read containerlab topology: %v", err)
	}
	var topology struct {
		Topology struct {
			Nodes map[string]map[string]any `yaml:"nodes"`
			Links []struct {
				Endpoints []string `yaml:"endpoints"`
			} `yaml:"links"`
		} `yaml:"topology"`
	}
	if err := yaml.Unmarshal(data, &topology); err != nil {
		t.Fatalf("decode containerlab topology: %v", err)
	}
	if len(topology.Topology.Nodes) != 2 {
		t.Fatalf("active topology node count = %d, want 2 owned applications", len(topology.Topology.Nodes))
	}
	for _, name := range []string{"clab-gobfd-vendors-gobfd", "frr"} {
		node := topology.Topology.Nodes[name]
		if node["kind"] != "ext-container" {
			t.Fatalf("application node %s must be external: %v", name, node)
		}
		if _, present := node["image"]; present {
			t.Fatalf("application node %s bypasses the owned image receipt: %v", name, node)
		}
	}
	for _, p := range m.Profiles {
		_, active := topology.Topology.Nodes[p.TopologyNode]
		if active != m.AllowsNode(p.TopologyNode) {
			t.Fatalf("profile %s active topology presence = %t, violates %s", p.ID, active, p.ExecutionPolicy)
		}
	}
	if len(topology.Topology.Links) != 1 ||
		!slices.Equal(topology.Topology.Links[0].Endpoints, []string{"clab-gobfd-vendors-gobfd:eth6", "frr:eth1"}) {
		t.Fatalf("active topology must contain only the owned FRR link: %v", topology.Topology.Links)
	}
	writeJSONArtifact(t, "skip-summary.json", map[string]any{
		"topology":                      "test/interop-clab/gobfd-vendors.clab.yml",
		"runner":                        "test/internal/clabbootstrap/lifecycle.go",
		"primary_profiles":              []string{"arista-ceos", "nokia-srlinux", "sonic-vs", "vyos"},
		"baseline_profiles":             []string{"frr"},
		"deferred_profiles":             []string{"cisco-xrd"},
		"retained_unavailable_profiles": []string{"arista-ceos", "nokia-srlinux", "cisco-xrd", "sonic-vs", "vyos"},
		"qualification_tracking":        "gobfd-qj0.8.2.8.5.2.7.4",
		"public_ci_default":             m.Runtime.PublicCIDefault,
		"cisco_xrd_profile":             "deferred; native qualification retained unavailable",
		"primary_skip_policy":           "historical profile class does not authorize execution",
		"retained_unavailable_behavior": "cached images do not authorize preparation or execution",
	})
}

func readManifest(t *testing.T) vendorprofiles.Manifest {
	t.Helper()
	m, err := vendorprofiles.Load(repoPath(t, ""))
	if err != nil {
		t.Fatalf("load vendor profiles: %v", err)
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
