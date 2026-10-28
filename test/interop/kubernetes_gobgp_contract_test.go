package interop_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type kubernetesAPIProbe struct {
	Exec *struct {
		Command []string `yaml:"command"`
	} `yaml:"exec"`
	TCPSocket      any `yaml:"tcpSocket"`      //nolint:tagliatelle // Kubernetes API field.
	TimeoutSeconds int `yaml:"timeoutSeconds"` //nolint:tagliatelle // Kubernetes API field.
}

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
						Name            string             `yaml:"name"`
						Image           string             `yaml:"image"`
						ImagePullPolicy string             `yaml:"imagePullPolicy"` //nolint:tagliatelle // Kubernetes API field.
						Command         []string           `yaml:"command"`
						Args            []string           `yaml:"args"`
						ReadinessProbe  kubernetesAPIProbe `yaml:"readinessProbe"` //nolint:tagliatelle // Kubernetes API field.
						LivenessProbe   kubernetesAPIProbe `yaml:"livenessProbe"`  //nolint:tagliatelle // Kubernetes API field.
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
		var apiHosts []string
		for _, arg := range container.Args {
			if strings.HasPrefix(arg, "--api-hosts") {
				apiHosts = append(apiHosts, arg)
			}
		}
		if !slices.Equal(apiHosts, []string{"--api-hosts=127.0.0.1:50051"}) {
			t.Errorf("GoBGP API listener arguments = %q, want one explicit loopback listener", apiHosts)
		}
		for name, probe := range map[string]kubernetesAPIProbe{
			"readiness": container.ReadinessProbe,
			"liveness":  container.LivenessProbe,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				want := []string{"/usr/local/bin/gobgp", "--host", "127.0.0.1", "--port", "50051", "global"}
				if probe.Exec == nil || !slices.Equal(probe.Exec.Command, want) || probe.TCPSocket != nil {
					t.Fatalf("GoBGP probe = %+v, want an exec API query on loopback", probe)
				}
				const timeoutSeconds = 3
				if probe.TimeoutSeconds != timeoutSeconds {
					t.Errorf("GoBGP probe timeout = %d, want %d seconds", probe.TimeoutSeconds, timeoutSeconds)
				}
			})
		}
	}
	if !found {
		t.Fatal("Kubernetes DaemonSet has no gobgp container")
	}
	var configMap struct {
		Data map[string]string `yaml:"data"`
	}
	config := readContractFile(t, "Kubernetes daemon ConfigMap",
		filepath.Join(root, "deployments/integrations/kubernetes/manifests/configmap-gobfd.yml"))
	if err := yaml.Unmarshal([]byte(config), &configMap); err != nil {
		t.Fatalf("decode daemon ConfigMap: %v", err)
	}
	var daemonConfig struct {
		GoBGP struct {
			Addr string `yaml:"addr"`
		} `yaml:"gobgp"`
	}
	if err := yaml.Unmarshal([]byte(configMap.Data["gobfd.yml"]), &daemonConfig); err != nil {
		t.Fatalf("decode daemon config: %v", err)
	}
	if daemonConfig.GoBGP.Addr != "127.0.0.1:50051" {
		t.Errorf("GoBFD GoBGP client address = %q, want the loopback API listener", daemonConfig.GoBGP.Addr)
	}
	runner := readContractFile(t, "integration runner", filepath.Join(root, "test/internal/integrationrunner/runner.go"))
	assertContainsAll(t, "Kubernetes image build", runner, []string{
		`"gobgp:local"`, `filepath.Join(r.root, "test", "interop", "gobgp", "Containerfile")`,
	})
}
