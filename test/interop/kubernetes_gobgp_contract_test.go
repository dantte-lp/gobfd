package interop_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestKubernetesDaemonSetUsesLocalGoBGPv4 keeps the Kubernetes example on the
// GoBGP v4 image that integrationctl builds from the shared recipe and imports.
func TestKubernetesDaemonSetUsesLocalGoBGPv4(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	contents := readContractFile(t, "Kubernetes DaemonSet",
		filepath.Join(root, "deployments/integrations/kubernetes/manifests/daemonset.yml"))
	if strings.Contains(contents, "jauderho/gobgp") {
		t.Fatal("Kubernetes DaemonSet retains docker.io/jauderho/gobgp")
	}
	var daemonSet struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name            string   `yaml:"name"`
						Image           string   `yaml:"image"`
						ImagePullPolicy string   `yaml:"imagePullPolicy"` //nolint:tagliatelle // Kubernetes API field.
						Command         []string `yaml:"command"`
					} `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(contents), &daemonSet); err != nil {
		t.Fatalf("decode Kubernetes DaemonSet: %v", err)
	}
	found := false
	for _, container := range daemonSet.Spec.Template.Spec.Containers {
		if container.Name != "gobgp" {
			continue
		}
		found = true
		// The shared recipe sets CMD, not ENTRYPOINT, so Kubernetes args need an explicit command.
		if container.Image != "gobgp:local" || container.ImagePullPolicy != "Never" ||
			!slices.Equal(container.Command, []string{"gobgpd"}) {
			t.Fatalf("gobgp container = image %q pull %q command %q, want gobgp:local/Never/[gobgpd]",
				container.Image, container.ImagePullPolicy, container.Command)
		}
	}
	if !found {
		t.Fatal("Kubernetes DaemonSet has no gobgp container")
	}
	runner := readContractFile(t, "integration runner", filepath.Join(root, "test/internal/integrationrunner/runner.go"))
	assertContainsAll(t, "Kubernetes image build", runner, []string{
		`"gobgp:local"`, `filepath.Join(r.root, "test", "interop", "gobgp", "Containerfile")`,
	})
}
