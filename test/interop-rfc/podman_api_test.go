//go:build interop_rfc

package interop_rfc_test

import (
	"context"
	"fmt"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

func containerExec(ctx context.Context, container string, command ...string) (string, error) {
	client, err := podmanapi.NewClientFromEnvironment()
	if err != nil {
		return "", err
	}
	result, err := client.Exec(ctx, container, command)
	return result.Stdout + result.Stderr, err
}

func containerPause(ctx context.Context, container string) error {
	client, err := podmanapi.NewClientFromEnvironment()
	if err != nil {
		return fmt.Errorf("create podman client: %w", err)
	}
	return client.Pause(ctx, container)
}

func containerUnpause(ctx context.Context, container string) error {
	client, err := podmanapi.NewClientFromEnvironment()
	if err != nil {
		return fmt.Errorf("create podman client: %w", err)
	}
	return client.Unpause(ctx, container)
}

func containerLogs(ctx context.Context, container string, tail int) (string, error) {
	client, err := podmanapi.NewClientFromEnvironment()
	if err != nil {
		return "", fmt.Errorf("create podman client: %w", err)
	}
	return client.Logs(ctx, container, tail)
}
