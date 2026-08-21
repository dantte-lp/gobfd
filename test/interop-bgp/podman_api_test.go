//go:build interop_bgp

package interop_bgp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const defaultInteropBGPProjectName = "gobfd-interop-bgp"

var (
	errInvalidInteropProjectName = errors.New("invalid interop Compose project name")
	errForeignProjectContainer   = errors.New("container is not owned by the interop Compose project")
	interopProjectNamePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

type containerClient interface {
	Inspect(ctx context.Context, container string) (json.RawMessage, error)
	Exec(ctx context.Context, container string, command []string) (podmanapi.ExecResult, error)
	Stop(ctx context.Context, container string) error
	Start(ctx context.Context, container string) error
	Pause(ctx context.Context, container string) error
	Unpause(ctx context.Context, container string) error
	Logs(ctx context.Context, container string, tail int) (string, error)
}

type exactContainerRuntime struct {
	client containerClient
}

func newExactContainerRuntime() (exactContainerRuntime, error) {
	client, err := podmanapi.NewClientFromEnvironment()
	if err != nil {
		return exactContainerRuntime{}, fmt.Errorf("create podman client: %w", err)
	}
	return exactContainerRuntime{client: client}, nil
}

func resolveInteropProjectName(raw string) (string, error) {
	projectName := raw
	if projectName == "" {
		projectName = defaultInteropBGPProjectName
	}
	if !interopProjectNamePattern.MatchString(projectName) {
		return "", fmt.Errorf("validate INTEROP_PROJECT_NAME %q: %w", projectName, errInvalidInteropProjectName)
	}
	return projectName, nil
}

func resolveProjectContainerID(
	ctx context.Context,
	client containerClient,
	containerName string,
) (string, error) {
	projectName, err := resolveInteropProjectName(os.Getenv("INTEROP_PROJECT_NAME"))
	if err != nil {
		return "", err
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
			"resolve container %s: id=%q project=%q, want project %q: %w",
			containerName,
			inspected.ID,
			label,
			projectName,
			errForeignProjectContainer,
		)
	}
	return inspected.ID, nil
}

type fakeContainerClient struct {
	inspectProject string
	actions        []string
}

func (f *fakeContainerClient) Inspect(_ context.Context, container string) (json.RawMessage, error) {
	return json.RawMessage(fmt.Sprintf(
		`{"Id":"immutable-container-id","Config":{"Labels":{"com.docker.compose.project":%q}},"Name":%q}`,
		f.inspectProject,
		container,
	)), nil
}

func (f *fakeContainerClient) Exec(_ context.Context, container string, _ []string) (podmanapi.ExecResult, error) {
	f.actions = append(f.actions, "exec:"+container)
	return podmanapi.ExecResult{}, nil
}

func (f *fakeContainerClient) Stop(_ context.Context, container string) error {
	f.actions = append(f.actions, "stop:"+container)
	return nil
}

func (f *fakeContainerClient) Start(_ context.Context, container string) error {
	f.actions = append(f.actions, "start:"+container)
	return nil
}

func (f *fakeContainerClient) Pause(_ context.Context, container string) error {
	f.actions = append(f.actions, "pause:"+container)
	return nil
}

func (f *fakeContainerClient) Unpause(_ context.Context, container string) error {
	f.actions = append(f.actions, "unpause:"+container)
	return nil
}

func (f *fakeContainerClient) Logs(_ context.Context, container string, _ int) (string, error) {
	f.actions = append(f.actions, "logs:"+container)
	return "", nil
}

func TestContainerRuntimeUsesExactProjectOwnedID(t *testing.T) {
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop-bgp")

	foreign := &fakeContainerClient{inspectProject: "foreign-project"}
	foreignRuntime := exactContainerRuntime{client: foreign}
	t.Setenv("INTEROP_PROJECT_NAME", "invalid.project")
	_, err := foreignRuntime.exec(t.Context(), "frr-bgp-interop", "true")
	if !errors.Is(err, errInvalidInteropProjectName) {
		t.Fatalf("invalid project error = %v, want errInvalidInteropProjectName", err)
	}
	t.Setenv("INTEROP_PROJECT_NAME", "gobfd-interop-bgp")
	foreignOperations := []struct {
		name string
		run  func() error
	}{
		{name: "exec", run: func() error {
			_, err := foreignRuntime.exec(t.Context(), "frr-bgp-interop", "true")
			return err
		}},
		{name: "stop", run: func() error { return foreignRuntime.stop(t.Context(), "frr-bgp-interop") }},
		{name: "start", run: func() error { return foreignRuntime.start(t.Context(), "frr-bgp-interop") }},
		{name: "pause", run: func() error { return foreignRuntime.pause(t.Context(), "frr-bgp-interop") }},
		{name: "unpause", run: func() error { return foreignRuntime.unpause(t.Context(), "frr-bgp-interop") }},
		{name: "logs", run: func() error {
			_, err := foreignRuntime.logs(t.Context(), "frr-bgp-interop", 10)
			return err
		}},
	}
	for _, operation := range foreignOperations {
		if err := operation.run(); !errors.Is(err, errForeignProjectContainer) {
			t.Fatalf("foreign container %s error = %v, want errForeignProjectContainer", operation.name, err)
		}
	}
	if len(foreign.actions) != 0 {
		t.Fatalf("foreign container reached runtime mutation: %v", foreign.actions)
	}

	owned := &fakeContainerClient{inspectProject: "gobfd-interop-bgp"}
	ownedRuntime := exactContainerRuntime{client: owned}
	if _, err := ownedRuntime.exec(t.Context(), "gobgp-interop", "true"); err != nil {
		t.Fatalf("exec exact owned container: %v", err)
	}
	for _, action := range []func(context.Context, string) error{
		ownedRuntime.stop,
		ownedRuntime.start,
		ownedRuntime.pause,
		ownedRuntime.unpause,
	} {
		if err := action(t.Context(), "frr-bgp-interop"); err != nil {
			t.Fatalf("mutate exact owned container: %v", err)
		}
	}
	if _, err := ownedRuntime.logs(t.Context(), "gobfd-bgp-interop", 10); err != nil {
		t.Fatalf("logs exact owned container: %v", err)
	}
	for _, action := range owned.actions {
		if !strings.HasSuffix(action, ":immutable-container-id") {
			t.Fatalf("runtime operation used mutable name: %q", action)
		}
	}
}

func (runtime exactContainerRuntime) exec(ctx context.Context, container string, command ...string) (string, error) {
	containerID, err := resolveProjectContainerID(ctx, runtime.client, container)
	if err != nil {
		return "", err
	}
	result, err := runtime.client.Exec(ctx, containerID, command)
	output := result.Stdout + result.Stderr
	if err != nil {
		return output, fmt.Errorf("exec exact container %s: %w", container, err)
	}
	return output, nil
}

func (runtime exactContainerRuntime) stop(ctx context.Context, container string) error {
	containerID, err := resolveProjectContainerID(ctx, runtime.client, container)
	if err != nil {
		return err
	}
	if err := runtime.client.Stop(ctx, containerID); err != nil {
		return fmt.Errorf("stop exact container %s: %w", container, err)
	}
	return nil
}

func (runtime exactContainerRuntime) start(ctx context.Context, container string) error {
	containerID, err := resolveProjectContainerID(ctx, runtime.client, container)
	if err != nil {
		return err
	}
	if err := runtime.client.Start(ctx, containerID); err != nil {
		return fmt.Errorf("start exact container %s: %w", container, err)
	}
	return nil
}

func (runtime exactContainerRuntime) pause(ctx context.Context, container string) error {
	containerID, err := resolveProjectContainerID(ctx, runtime.client, container)
	if err != nil {
		return err
	}
	if err := runtime.client.Pause(ctx, containerID); err != nil {
		return fmt.Errorf("pause exact container %s: %w", container, err)
	}
	return nil
}

func (runtime exactContainerRuntime) unpause(ctx context.Context, container string) error {
	containerID, err := resolveProjectContainerID(ctx, runtime.client, container)
	if err != nil {
		return err
	}
	if err := runtime.client.Unpause(ctx, containerID); err != nil {
		return fmt.Errorf("unpause exact container %s: %w", container, err)
	}
	return nil
}

func (runtime exactContainerRuntime) logs(ctx context.Context, container string, tail int) (string, error) {
	containerID, err := resolveProjectContainerID(ctx, runtime.client, container)
	if err != nil {
		return "", err
	}
	logs, err := runtime.client.Logs(ctx, containerID, tail)
	if err != nil {
		return "", fmt.Errorf("read exact container %s logs: %w", container, err)
	}
	return logs, nil
}

func containerExec(ctx context.Context, container string, command ...string) (string, error) {
	runtime, err := newExactContainerRuntime()
	if err != nil {
		return "", err
	}
	return runtime.exec(ctx, container, command...)
}

func containerStop(ctx context.Context, container string) error {
	runtime, err := newExactContainerRuntime()
	if err != nil {
		return err
	}
	return runtime.stop(ctx, container)
}

func containerStart(ctx context.Context, container string) error {
	runtime, err := newExactContainerRuntime()
	if err != nil {
		return err
	}
	return runtime.start(ctx, container)
}

func containerPause(ctx context.Context, container string) error {
	runtime, err := newExactContainerRuntime()
	if err != nil {
		return err
	}
	return runtime.pause(ctx, container)
}

func containerUnpause(ctx context.Context, container string) error {
	runtime, err := newExactContainerRuntime()
	if err != nil {
		return err
	}
	return runtime.unpause(ctx, container)
}

func containerLogs(ctx context.Context, container string, tail int) (string, error) {
	runtime, err := newExactContainerRuntime()
	if err != nil {
		return "", err
	}
	return runtime.logs(ctx, container, tail)
}
