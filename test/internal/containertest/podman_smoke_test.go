//go:build testcontainers

package containertest

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const smokeImage = "docker.io/library/alpine@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b"

func TestPodmanSmoke(t *testing.T) {
	endpoint := RequirePodman(t)

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create cleanup verification client: %v", err)
	}

	t.Run("network creation failure", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := NewNetwork(ctx, t, testcontainers.NetworkRequest{ //nolint:staticcheck // provider-aware v0.44.0 API
			Name:   fmt.Sprintf("gobfd-smoke-invalid-%d", time.Now().UnixNano()),
			Driver: "gobfd-invalid-network-driver",
		})
		if err == nil {
			t.Fatal("create network with invalid driver succeeded; want an error")
		}
	})

	var containerID, networkName string
	if !t.Run("lifecycle", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		networkName = fmt.Sprintf("gobfd-smoke-%d", time.Now().UnixNano())
		_, err := NewNetwork(ctx, t, testcontainers.NetworkRequest{ //nolint:staticcheck // provider-aware v0.44.0 API
			Name:   networkName,
			Driver: "bridge",
			Labels: map[string]string{"io.gobfd.test": "podman-smoke"},
		})
		if err != nil {
			t.Fatalf("create smoke network: %v", err)
		}

		container, err := Run(ctx, t, testcontainers.ContainerRequest{
			Image:          smokeImage,
			Cmd:            []string{"sh", "-c", "printf 'podman-ready\\n'; exec sleep 30"},
			Labels:         map[string]string{"io.gobfd.test": "podman-smoke"},
			Networks:       []string{networkName},
			NetworkAliases: map[string][]string{networkName: {"smoke"}},
			WaitingFor:     wait.ForLog("podman-ready").WithStartupTimeout(30 * time.Second),
		})
		if err != nil {
			t.Fatalf("run smoke container: %v", err)
		}
		containerID = container.GetContainerID()
		if containerID == "" {
			t.Fatal("testcontainers returned an empty container ID")
		}
		inspection, err := container.Inspect(ctx)
		if err != nil {
			t.Fatalf("inspect labeled smoke container: %v", err)
		}
		if got := inspection.Config.Labels["io.gobfd.test"]; got != "podman-smoke" {
			t.Fatalf("container label io.gobfd.test = %q, want podman-smoke", got)
		}
		networks, err := container.Networks(ctx)
		if err != nil {
			t.Fatalf("inspect smoke container networks: %v", err)
		}
		if !contains(networks, networkName) {
			t.Fatalf("container networks = %v, want %s", networks, networkName)
		}
		assertNetworkLabel(t, ctx, networkName, "io.gobfd.test", "podman-smoke")

		logs, err := container.Logs(ctx)
		if err != nil {
			t.Fatalf("read smoke container logs: %v", err)
		}
		defer logs.Close()
		logBytes, err := io.ReadAll(logs)
		if err != nil {
			t.Fatalf("consume smoke container logs: %v", err)
		}
		if !strings.Contains(string(logBytes), "podman-ready") {
			t.Fatalf("logs = %q, want readiness marker", logBytes)
		}

		exitCode, output, err := Exec(ctx, container, []string{"sh", "-c", "printf exec-ok"})
		if err != nil {
			t.Fatalf("exec in smoke container: %v", err)
		}
		if exitCode != 0 || output != "exec-ok" {
			t.Fatalf("exec = code %d output %q, want code 0 output exec-ok", exitCode, output)
		}
	}) {
		t.Fatal("smoke lifecycle subtest failed")
	}

	waitContainerRemoved(t, client, containerID)
	assertNetworkRemoved(t, networkName)
}

func waitContainerRemoved(t *testing.T, client *podmanapi.Client, containerID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		containers, err := client.Containers(ctx)
		if err != nil {
			t.Fatalf("list containers while verifying cleanup: %v", err)
		}
		found := false
		for _, container := range containers {
			if container.ID == containerID {
				found = true
				break
			}
		}
		if !found {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("container %s still exists after test cleanup: %v", containerID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertNetworkRemoved(t *testing.T, networkName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		t.Fatalf("create Podman provider for network cleanup verification: %v", err)
	}
	defer func() {
		if closeErr := provider.Close(); closeErr != nil {
			t.Errorf("close Podman provider after network cleanup verification: %v", closeErr)
		}
	}()

	// NetworkRequest is the only v0.44.0 lookup API.
	//nolint:staticcheck // upstream replacement does not yet expose inspection
	_, err = provider.GetNetwork(ctx, testcontainers.NetworkRequest{Name: networkName})
	if err == nil {
		t.Fatalf("network %s still exists after test cleanup", networkName)
	}
	if !errdefs.IsNotFound(err) {
		t.Fatalf("inspect removed network %s: %v", networkName, err)
	}
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

func assertNetworkLabel(t *testing.T, ctx context.Context, networkName, key, want string) {
	t.Helper()

	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		t.Fatalf("create Podman provider for network label verification: %v", err)
	}
	defer func() {
		if closeErr := provider.Close(); closeErr != nil {
			t.Errorf("close Podman provider after network label verification: %v", closeErr)
		}
	}()

	//nolint:staticcheck // upstream replacement does not yet expose inspection
	inspection, err := provider.GetNetwork(ctx, testcontainers.NetworkRequest{Name: networkName})
	if err != nil {
		t.Fatalf("inspect labeled network %s: %v", networkName, err)
	}
	if got := inspection.Labels[key]; got != want {
		t.Fatalf("network label %s = %q, want %q", key, got, want)
	}
}
