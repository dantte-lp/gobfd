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

	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const smokeImage = "docker.io/library/debian:trixie-slim@sha256:d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132"

func TestPodmanSmoke(t *testing.T) {
	endpoint := RequirePodman(t)

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
		assertNetworkLabel(ctx, t, networkName, "io.gobfd.test", "podman-smoke")

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

	AssertContainerRemoved(t, endpoint, containerID)
	AssertNetworkRemoved(t, networkName)
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

func assertNetworkLabel(ctx context.Context, t *testing.T, networkName, key, want string) {
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
