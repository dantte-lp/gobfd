//go:build interop_rfc || interop_rfc_testcontainers

package interop_rfc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const defaultInteropRFCProjectName = "gobfd-interop-rfc"

var interopRFCProjectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type rfcContainerClient interface {
	Inspect(ctx context.Context, container string) (json.RawMessage, error)
	Exec(ctx context.Context, container string, command []string) (podmanapi.ExecResult, error)
	Pause(ctx context.Context, container string) error
	Unpause(ctx context.Context, container string) error
	Logs(ctx context.Context, container string, tail int) (string, error)
}

func newRFCContainerClient() (rfcContainerClient, error) {
	client, err := podmanapi.NewClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("create podman client: %w", err)
	}
	return client, nil
}

func resolveRFCProjectContainerID(
	ctx context.Context,
	client rfcContainerClient,
	containerName string,
) (string, error) {
	projectName := os.Getenv("INTEROP_PROJECT_NAME")
	if projectName == "" {
		projectName = defaultInteropRFCProjectName
	}
	if !interopRFCProjectNamePattern.MatchString(projectName) {
		return "", fmt.Errorf("validate INTEROP_PROJECT_NAME %q", projectName)
	}
	raw, err := client.Inspect(ctx, containerName)
	if err != nil {
		return "", fmt.Errorf("inspect container %s ownership: %w", containerName, err)
	}
	var inspected struct {
		ID     string `json:"Id"` //nolint:tagliatelle // Docker-compatible API field.
		Config struct {
			Labels map[string]string `json:"Labels"` //nolint:tagliatelle // Docker-compatible API field.
		} `json:"Config"` //nolint:tagliatelle // Docker-compatible API field.
	}
	if err := json.Unmarshal(raw, &inspected); err != nil {
		return "", fmt.Errorf("decode container %s ownership: %w", containerName, err)
	}
	label := inspected.Config.Labels["com.docker.compose.project"]
	if inspected.ID == "" || label != projectName {
		return "", fmt.Errorf(
			"resolve container %s: id=%q project=%q, want project %q",
			containerName,
			inspected.ID,
			label,
			projectName,
		)
	}
	return inspected.ID, nil
}

func containerExec(ctx context.Context, container string, command ...string) (string, error) {
	client, err := newRFCContainerClient()
	if err != nil {
		return "", err
	}
	containerID, err := resolveRFCProjectContainerID(ctx, client, container)
	if err != nil {
		return "", err
	}
	result, err := client.Exec(ctx, containerID, command)
	output := result.Stdout + result.Stderr
	if err != nil {
		return output, fmt.Errorf("exec exact container %s: %w", container, err)
	}
	return output, nil
}

func containerPause(ctx context.Context, container string) error {
	client, err := newRFCContainerClient()
	if err != nil {
		return err
	}
	containerID, err := resolveRFCProjectContainerID(ctx, client, container)
	if err != nil {
		return err
	}
	if err := client.Pause(ctx, containerID); err != nil {
		return fmt.Errorf("pause exact container %s: %w", container, err)
	}
	return nil
}

func containerUnpause(ctx context.Context, container string) error {
	client, err := newRFCContainerClient()
	if err != nil {
		return err
	}
	containerID, err := resolveRFCProjectContainerID(ctx, client, container)
	if err != nil {
		return err
	}
	if err := client.Unpause(ctx, containerID); err != nil {
		return fmt.Errorf("unpause exact container %s: %w", container, err)
	}
	return nil
}

func containerLogs(ctx context.Context, container string, tail int) (string, error) {
	client, err := newRFCContainerClient()
	if err != nil {
		return "", err
	}
	containerID, err := resolveRFCProjectContainerID(ctx, client, container)
	if err != nil {
		return "", err
	}
	logs, err := client.Logs(ctx, containerID, tail)
	if err != nil {
		return "", fmt.Errorf("read exact container %s logs: %w", container, err)
	}
	return logs, nil
}
