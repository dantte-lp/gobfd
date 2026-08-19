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

	testcontainers "github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

const rootfulPodmanSocket = "/run/podman/podman.sock"

// RequirePodman configures testcontainers for the local Podman API socket.
// Missing Podman skips optional local tests and fails required CI/test targets.
func RequirePodman(tb testing.TB) string {
	tb.Helper()

	endpoint, found := resolvePodmanEndpoint(os.Getenv, isSocket)
	if !found {
		if envTrue(os.Getenv("GOBFD_REQUIRE_PODMAN")) {
			tb.Fatal("Podman socket is required but was not found; start podman.socket or set DOCKER_HOST, PODMAN_HOST, or CONTAINER_HOST to an existing unix:// socket")
		}
		tb.Skip("Podman socket not found; start podman.socket or set DOCKER_HOST, PODMAN_HOST, or CONTAINER_HOST to an existing unix:// socket")
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
