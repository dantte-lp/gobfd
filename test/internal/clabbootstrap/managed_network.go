package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	networkOwnershipFields = 4
	networkLabelOption     = "--label"
	networkFormatOption    = "--format"
	networkExistsCommand   = "exists"
)

const inspectNetworkOwnershipFormat = `{{.ID}}|{{.Name}}|` +
	`{{ index .Labels "io.gobfd.interop-clab.owner" }}|` +
	`{{ index .Labels "io.gobfd.interop-clab.run" }}`

var (
	networkIDRE  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	networkRunRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

type lifecycleNetwork struct {
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

func networkName(runID string) string { return "gobfd-clab-" + runID }

// stageManagementNetwork requires the caller to checkpoint the intended name first.
func stageManagementNetwork(ctx context.Context, runner Runner, runID string) (lifecycleNetwork, error) {
	network := lifecycleNetwork{Name: networkName(runID)}
	if err := validateNetworkReference(network, runID); err != nil {
		return network, err
	}
	present, err := networkReferenceExists(ctx, runner, network.Name)
	if err != nil {
		return network, err
	}
	if present {
		return network, fmt.Errorf("management network %s already exists: %w", network.Name, errLifecycleState)
	}
	createErr := runNetworkCommand(ctx, runner, []string{
		"network", "create", networkLabelOption, ownerLabel + "=" + labName,
		networkLabelOption, runLabel + "=" + runID, network.Name,
	})
	// A failed command can still create the network; preserve a provable ID for recovery.
	id, inspectErr := inspectOwnedNetwork(ctx, runner, network.Name, runID)
	if inspectErr == nil {
		network.ID = id
	}
	return network, errors.Join(createErr, inspectErr)
}

func validateOwnedNetwork(
	ctx context.Context, runner Runner, network lifecycleNetwork, runID string, requireEmpty bool,
) (bool, error) {
	_, present, err := checkedOwnedNetwork(ctx, runner, network, runID, requireEmpty)
	return present, err
}

func removeOwnedNetwork(ctx context.Context, runner Runner, network lifecycleNetwork, runID string) error {
	id, present, err := checkedOwnedNetwork(ctx, runner, network, runID, true)
	if err != nil || !present {
		return err
	}
	if err := runNetworkCommand(ctx, runner, []string{"network", "rm", id}); err != nil {
		return err
	}
	for _, reference := range []string{network.Name, id} {
		exists, err := networkReferenceExists(ctx, runner, reference)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("management network %s remains after cleanup: %w", reference, errLifecycleState)
		}
	}
	return nil
}

func checkedOwnedNetwork(
	ctx context.Context, runner Runner, network lifecycleNetwork, runID string, requireEmpty bool,
) (string, bool, error) {
	if err := validateNetworkReference(network, runID); err != nil {
		return "", false, err
	}
	present, err := networkReferenceExists(ctx, runner, network.Name)
	if err != nil {
		return "", false, err
	}
	if !present {
		return "", false, verifyMissingNetworkID(ctx, runner, network.ID)
	}
	id, err := inspectOwnedNetwork(ctx, runner, network.Name, runID)
	if err != nil {
		return "", false, err
	}
	if network.ID != "" && id != network.ID {
		return "", false, fmt.Errorf("management network changed identity: %w", errLifecycleState)
	}
	if requireEmpty {
		result, err := runner.Run(ctx, Command{
			Executable: executablePodman,
			Arguments: []string{
				"ps", "--all", "--quiet", "--no-trunc", "--filter", "network=" + id,
			},
		})
		if err != nil {
			return "", false, fmt.Errorf("query management network attachments: %w", err)
		}
		if result.ExitCode != 0 {
			return "", false, fmt.Errorf(
				"query management network attachments: exit %d: %w", result.ExitCode, ErrBootstrapFailed,
			)
		}
		if strings.TrimSpace(result.Stdout) != "" {
			return "", false, fmt.Errorf("management network has attached containers: %w", errLifecycleState)
		}
	}
	return id, true, nil
}

func validateNetworkReference(network lifecycleNetwork, runID string) error {
	if !networkRunRE.MatchString(runID) || network.Name != networkName(runID) ||
		(network.ID != "" && !networkIDRE.MatchString(network.ID)) {
		return fmt.Errorf("invalid management network reference: %w", errLifecycleState)
	}
	return nil
}

func inspectOwnedNetwork(ctx context.Context, runner Runner, name, runID string) (string, error) {
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments:  []string{"network", "inspect", networkFormatOption, inspectNetworkOwnershipFormat, name},
	})
	if err != nil {
		return "", fmt.Errorf("inspect management network: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("inspect management network: exit %d: %w", result.ExitCode, ErrBootstrapFailed)
	}
	fields := strings.Split(strings.TrimSpace(result.Stdout), "|")
	if len(fields) != networkOwnershipFields || !networkIDRE.MatchString(fields[0]) || fields[1] != name ||
		fields[2] != labName || fields[3] != runID {
		return "", fmt.Errorf("management network ownership mismatch: %w", errLifecycleState)
	}
	return fields[0], nil
}

func networkReferenceExists(ctx context.Context, runner Runner, reference string) (bool, error) {
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments:  []string{"network", networkExistsCommand, reference},
	})
	if err != nil {
		return false, fmt.Errorf("query management network existence: %w", err)
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("query management network existence: exit %d: %w", result.ExitCode, ErrBootstrapFailed)
	}
}

func runNetworkCommand(ctx context.Context, runner Runner, arguments []string) error {
	result, err := runner.Run(ctx, Command{Executable: executablePodman, Arguments: arguments})
	if err != nil {
		return fmt.Errorf("run management network command: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("run management network command: exit %d: %w", result.ExitCode, ErrBootstrapFailed)
	}
	return nil
}

func verifyMissingNetworkID(ctx context.Context, runner Runner, id string) error {
	if id == "" {
		return nil
	}
	present, err := networkReferenceExists(ctx, runner, id)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("management network name lost its recorded ID: %w", errLifecycleState)
	}
	return nil
}
