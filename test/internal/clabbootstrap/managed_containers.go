package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

func managedContainerArguments(options Options, runID, name string) []string {
	return []string{
		"run", "--detach", "--name", name,
		labelOption, ownerLabel + "=" + labName,
		labelOption, runLabel + "=" + runID,
		labelOption, containerlabLabel + "=" + labName,
		labelOption, "clab-topo-file=" + runtimeTopologyPath(options.ProjectRoot),
		labelOption, "clab-node-lab-dir=" + filepath.Join(containerlabDirectory(options.ProjectRoot), name),
		"--network", "none", "--cpus", "1", "--memory", "256m", "--memory-swap", "256m", "--pids-limit", "128",
		"--cap-add", "NET_RAW", "--cap-add", "NET_ADMIN",
	}
}

func startFRRContainer(ctx context.Context, options Options, runner Runner, receipt lifecycleReceipt) error {
	config := filepath.Join(options.ProjectRoot, "test", "interop-clab", "frr")
	arguments := append(managedContainerArguments(options, receipt.RunID, frrContainer),
		"--cap-add", "SYS_ADMIN",
		"--volume", filepath.Join(config, "daemons")+":/etc/frr/daemons:ro",
		"--volume", filepath.Join(config, "frr.conf")+":/etc/frr/frr.conf:ro",
		receipt.FRR.ID, "mgmtd", "zebra", "bgpd", "bfdd", "staticd")
	return runCommand(ctx, runner, Command{Executable: executablePodman, Arguments: arguments, DryRun: options.DryRun})
}

func checkpointManagedContainer(
	ctx context.Context, options Options, runner Runner, receipt *lifecycleReceipt, name string,
) error {
	id, err := inspectContainerIdentity(ctx, runner, name, receipt.RunID, true)
	if err != nil {
		return err
	}
	if previous, recorded := receipt.Containers[name]; recorded && previous != id {
		return fmt.Errorf("container %s changed since checkpoint: %w", name, errLifecycleState)
	}
	receipt.Containers[name] = id
	if err := writeLifecycleReceipt(options.ProjectRoot, *receipt); err != nil {
		return fmt.Errorf("checkpoint application container %s: %w", name, err)
	}
	return nil
}

func containerlabDirectory(projectRoot string) string {
	return filepath.Join(filepath.Dir(runtimeTopologyPath(projectRoot)), "clab-"+labName)
}

func prepareDeploymentNetwork(ctx context.Context, options Options, runner Runner, receipt *lifecycleReceipt) error {
	if len(receipt.ExpectedContainers) == managedApplicationCount {
		present, err := validateOwnedNetwork(ctx, runner, receipt.Network, receipt.RunID, false)
		if err != nil {
			return err
		}
		if present {
			return fmt.Errorf("external-only topology requires an absent management network: %w", errLifecycleState)
		}
		return nil
	}
	network, stageErr := stageManagementNetwork(ctx, runner, receipt.RunID)
	receipt.Network = network
	checkpointErr := writeLifecycleReceipt(options.ProjectRoot, *receipt)
	if err := errors.Join(stageErr, checkpointErr); err != nil {
		return err
	}
	return validateDeployedNetwork(ctx, runner, *receipt)
}

func validateDeployedNetwork(ctx context.Context, runner Runner, receipt lifecycleReceipt) error {
	if len(receipt.ExpectedContainers) > managedApplicationCount && receipt.Network.ID == "" {
		return fmt.Errorf("mixed topology network was never checkpointed: %w", errLifecycleState)
	}
	present, err := validateOwnedNetwork(ctx, runner, receipt.Network, receipt.RunID, false)
	if err != nil {
		return err
	}
	if present != (receipt.Network.ID != "") {
		return fmt.Errorf("management network differs from deployed receipt: %w", errLifecycleState)
	}
	return nil
}

func startManagedApplications(ctx context.Context, options Options, runner Runner, receipt *lifecycleReceipt) error {
	if err := startGoBFDContainer(ctx, options, runner, receipt.RunID, receipt.Image.ID); err != nil {
		return err
	}
	if err := checkpointManagedContainer(ctx, options, runner, receipt, gobfdContainer); err != nil {
		return err
	}
	if err := startFRRContainer(ctx, options, runner, *receipt); err != nil {
		return err
	}
	return checkpointManagedContainer(ctx, options, runner, receipt, frrContainer)
}

func recoverPartialTopology(ctx context.Context, options Options, runner Runner, receipt *lifecycleReceipt) error {
	if receipt.SchemaVersion != ownedReceiptV3 || receipt.Topology == "" {
		return nil
	}
	for _, name := range receipt.ExpectedContainers {
		if _, recorded := receipt.Containers[name]; recorded {
			continue
		}
		present, err := containerExists(ctx, runner, name)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		id, err := inspectContainerIdentity(ctx, runner, name, receipt.RunID, true)
		if err != nil {
			return err
		}
		receipt.Containers[name] = id
		if err := writeLifecycleReceipt(options.ProjectRoot, *receipt); err != nil {
			return err
		}
	}
	return nil
}
