// Package containertest provides shared Podman-backed testcontainers fixtures.
package containertest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const rootfulPodmanSocket = "/run/podman/podman.sock"

// RequirePodman configures testcontainers for the local Podman API socket.
// Missing Podman skips optional local tests and fails required CI/test targets.
func RequirePodman(tb testing.TB) string {
	tb.Helper()

	endpoint, found := resolvePodmanEndpoint(os.Getenv, isSocket)
	if !found {
		if envTrue(os.Getenv("GOBFD_REQUIRE_PODMAN")) {
			tb.Fatal(
				"Podman socket is required but was not found; start podman.socket or set " +
					"DOCKER_HOST, PODMAN_HOST, or CONTAINER_HOST to an existing unix:// socket",
			)
		}
		tb.Skip(
			"Podman socket not found; start podman.socket or set DOCKER_HOST, PODMAN_HOST, " +
				"or CONTAINER_HOST to an existing unix:// socket",
		)
		return ""
	}
	if os.Getenv("DOCKER_HOST") != endpoint {
		tb.Setenv("DOCKER_HOST", endpoint)
	}
	// Ryuk cannot access a label-confined Podman socket on SELinux hosts.
	// Every fixture registers synchronous test cleanup, including failure paths.
	if os.Getenv("TESTCONTAINERS_RYUK_DISABLED") == "" {
		tb.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
	return endpoint
}

// Run starts a Podman-backed test container and immediately registers cleanup.
func Run(
	ctx context.Context,
	tb testing.TB,
	request testcontainers.ContainerRequest,
) (testcontainers.Container, error) {
	tb.Helper()

	container, err := testcontainers.GenericContainer(ctx, podmanRequest(request))
	testcontainers.CleanupContainer(tb, container)
	if err != nil {
		return container, fmt.Errorf("start Podman test container: %w", err)
	}
	return container, nil
}

// NewNetwork creates an explicit Podman user network and immediately
// registers cleanup. NetworkRequest remains the only v0.44.0 API that accepts
// custom IPAM together with an explicit provider.
//
//nolint:staticcheck // testcontainers has not replaced the provider-aware API
func NewNetwork(
	ctx context.Context,
	tb testing.TB,
	request testcontainers.NetworkRequest,
) (testcontainers.Network, error) {
	tb.Helper()

	network, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: request,
		ProviderType:   testcontainers.ProviderPodman,
	})
	if err != nil {
		return nil, fmt.Errorf("create Podman test network: %w", err)
	}
	testcontainers.CleanupNetwork(tb, network)
	return network, nil
}

// Exec runs a command and removes Docker-compatible stream framing from the
// combined stdout and stderr returned by the Podman API.
func Exec(
	ctx context.Context,
	container testcontainers.Container,
	command []string,
) (int, string, error) {
	exitCode, output, err := container.Exec(ctx, command, tcexec.Multiplexed())
	if err != nil {
		return exitCode, "", fmt.Errorf("exec test container command: %w", err)
	}
	outputBytes, err := io.ReadAll(output)
	if err != nil {
		return exitCode, "", fmt.Errorf("read test container exec output: %w", err)
	}
	return exitCode, string(outputBytes), nil
}

// AssertContainerRemoved proves that a container is absent from the exact
// Podman endpoint. API errors fail the assertion instead of masquerading as
// successful cleanup.
func AssertContainerRemoved(tb testing.TB, endpoint, containerID string) {
	tb.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		tb.Fatalf("create cleanup verification client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		containers, err := client.Containers(ctx)
		if err != nil {
			tb.Fatalf("list containers while verifying cleanup: %v", err)
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
			tb.Fatalf("container %s still exists after test cleanup: %v", containerID, ctx.Err())
		case <-ticker.C:
		}
	}
}

// AssertNetworkRemoved proves that a network is absent from the configured
// Podman endpoint and fails closed on errors other than not-found.
func AssertNetworkRemoved(tb testing.TB, networkName string) {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		tb.Fatalf("create Podman provider for network cleanup verification: %v", err)
	}
	defer func() {
		if closeErr := provider.Close(); closeErr != nil {
			tb.Errorf("close Podman provider after network cleanup verification: %v", closeErr)
		}
	}()

	//nolint:staticcheck // upstream replacement does not yet expose inspection
	_, err = provider.GetNetwork(ctx, testcontainers.NetworkRequest{Name: networkName})
	if err == nil {
		tb.Fatalf("network %s still exists after test cleanup", networkName)
	}
	if !errdefs.IsNotFound(err) {
		tb.Fatalf("inspect removed network %s: %v", networkName, err)
	}
}

// AssertImageRemoved proves that a test-owned image is absent from the exact
// Podman endpoint and fails closed on API errors.
func AssertImageRemoved(tb testing.TB, endpoint, imageName string) {
	tb.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		tb.Fatalf("create image cleanup verification client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		exists, err := client.ImageExists(ctx, imageName)
		if err != nil {
			tb.Fatalf("inspect image while verifying cleanup: %v", err)
		}
		if !exists {
			return
		}
		select {
		case <-ctx.Done():
			tb.Fatalf("image %s still exists after test cleanup: %v", imageName, ctx.Err())
		case <-ticker.C:
		}
	}
}

// AssertVolumeRemoved proves that an exact test-owned volume is absent from
// the selected Podman endpoint and fails closed on API errors.
func AssertVolumeRemoved(tb testing.TB, endpoint, volumeName string) {
	tb.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		tb.Fatalf("create volume cleanup verification client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		exists, err := client.VolumeExists(ctx, volumeName)
		if err != nil {
			tb.Fatalf("inspect volume while verifying cleanup: %v", err)
		}
		if !exists {
			return
		}
		select {
		case <-ctx.Done():
			tb.Fatalf("volume %s still exists after test cleanup: %v", volumeName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func resolvePodmanEndpoint(getenv func(string) string, socketExists func(string) bool) (string, bool) {
	for _, key := range []string{"DOCKER_HOST", "PODMAN_HOST", "CONTAINER_HOST"} {
		if endpoint := validUnixEndpoint(getenv(key), socketExists); endpoint != "" {
			return endpoint, true
		}
	}

	if socketExists(rootfulPodmanSocket) {
		return "unix://" + rootfulPodmanSocket, true
	}
	if runtimeDir := strings.TrimSpace(getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
		path := filepath.Join(runtimeDir, "podman", "podman.sock")
		if socketExists(path) {
			return "unix://" + path, true
		}
	}
	return "", false
}

func validUnixEndpoint(endpoint string, socketExists func(string) bool) string {
	endpoint = strings.TrimSpace(endpoint)
	path, ok := strings.CutPrefix(endpoint, "unix://")
	if !ok || path == "" || !socketExists(path) {
		return ""
	}
	return "unix://" + path
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func envTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func podmanRequest(request testcontainers.ContainerRequest) testcontainers.GenericContainerRequest {
	return testcontainers.GenericContainerRequest{
		ContainerRequest: request,
		Started:          true,
		ProviderType:     testcontainers.ProviderPodman,
	}
}
