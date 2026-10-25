package clabbootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRuntimeTopologyUsesOwnedExternalFRRWithoutManagementNetwork(t *testing.T) {
	root := bootstrapTestRoot(t)
	if err := ensureRuntimeDirectory(root); err != nil {
		t.Fatal(err)
	}
	source := "name: gobfd-vendors\ntopology:\n  nodes:\n" +
		"    clab-gobfd-vendors-gobfd:\n      kind: ext-container\n" +
		"    frr:\n      kind: ext-container\n" +
		"  links:\n    - endpoints: [clab-gobfd-vendors-gobfd:eth6, frr:eth1]\n"
	template := filepath.Join(root, "test", "interop-clab", "gobfd-vendors.clab.yml")
	if err := os.WriteFile(template, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions(root)
	profiles := []vendorProfile{{name: "frr", image: testFRRImageID}}
	path, err := writeRuntimeTopology(options, profiles, newImageReceipt("topology-test"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Mgmt struct {
			Network string `yaml:"network"`
			Skip    bool   `yaml:"skip-when-unused"` //nolint:tagliatelle // Containerlab defines this public key.
		} `yaml:"mgmt"`
		Topology struct {
			Nodes map[string]map[string]any `yaml:"nodes"`
			Links []struct {
				Endpoints []string `yaml:"endpoints"`
			} `yaml:"links"`
		} `yaml:"topology"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if _, present := document.Topology.Nodes["frr"]; present {
		t.Fatal("generated ext-container still uses the logical FRR node name")
	}
	for _, name := range []string{gobfdContainer, frrContainer} {
		node := document.Topology.Nodes[name]
		if node["kind"] != "ext-container" || node["image"] != nil || node["network-mode"] != "none" {
			t.Fatalf("managed node %s does not preserve owned immutable startup: %v", name, node)
		}
	}
	if len(document.Topology.Links) != 1 || document.Topology.Links[0].Endpoints[1] != "clab-gobfd-vendors-frr:eth1" {
		t.Fatalf("FRR link endpoint is not rewritten: %v", document.Topology.Links)
	}
	if !document.Mgmt.Skip || document.Mgmt.Network == "" || document.Mgmt.Network == "clab" {
		t.Fatalf("external topology can select a shared management network: %+v", document.Mgmt)
	}
}
