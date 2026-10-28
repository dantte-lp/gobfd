package clabbootstrap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

type externalActivationRunner struct {
	changedNetworkRunner

	labels   map[string]map[string]string
	accepted error
	conflict error
}

func (runner *externalActivationRunner) Run(ctx context.Context, command Command) (Result, error) {
	if command.Executable == executableContainerlab {
		// Containerlab's lab-uniqueness check also applies to ext-container nodes.
		for _, labels := range runner.labels {
			if labels["containerlab"] == "gobfd-vendors" {
				return Result{}, runner.conflict
			}
		}
		return Result{}, runner.accepted
	}
	if command.Executable == executablePodman && len(command.Arguments) > 0 {
		switch command.Arguments[0] {
		case "run":
			labels := make(map[string]string)
			for index, argument := range command.Arguments[:len(command.Arguments)-1] {
				if argument == "--label" {
					key, value, _ := strings.Cut(command.Arguments[index+1], "=")
					labels[key] = value
				}
			}
			name := argumentAfter(command.Arguments, "--name")
			runner.labels[name] = labels
		case "inspect":
			name := command.Arguments[len(command.Arguments)-1]
			labels := runner.labels[name]
			return Result{Stdout: fmt.Sprintf("%s-id|%s|%s|%s", name,
				labels[containerlabLabel], labels[ownerLabel], labels[runLabel])}, nil
		}
	}
	return runner.changedNetworkRunner.Run(ctx, command)
}

func TestExternalApplicationsDoNotClaimDeployedLab(t *testing.T) {
	t.Parallel()
	for _, foreignLab := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign-lab-%t", foreignLab), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := ensureRuntimeDirectory(root); err != nil {
				t.Fatal(err)
			}
			receipt := newImageReceipt(testNetworkRun)
			receipt.Image.ID = testImageID
			receipt.FRR.ID = testFRRImageID
			receipt.Topology = runtimeTopologyPath(root)
			receipt.Network.Name = networkName(receipt.RunID)
			receipt.ExpectedContainers = []string{gobfdContainer, frrContainer}
			if err := createLifecycleReceipt(root, receipt); err != nil {
				t.Fatal(err)
			}
			accepted := errors.New("deployment passed lab uniqueness check")
			conflict := errors.New("lab has already been deployed")
			runner := &externalActivationRunner{
				network: newNetworkScript(t,
					networkExistsStep(receipt.Network.Name, false),
					networkExistsStep(receipt.Network.Name, false),
				),
				labels: make(map[string]map[string]string), accepted: accepted, conflict: conflict,
			}
			want := accepted
			if foreignLab {
				runner.labels["foreign-container"] = map[string]string{"containerlab": "gobfd-vendors"}
				want = conflict
			}
			err := activateTopology(t.Context(), DefaultOptions(root), runner, nil, &receipt)
			if !errors.Is(err, want) {
				t.Fatalf("deployment boundary: %v, want %v", err, want)
			}
			for _, name := range []string{gobfdContainer, frrContainer} {
				if receipt.Containers[name] != name+"-id" {
					t.Fatalf("application %s was not checkpointed", name)
				}
				labels := runner.labels[name]
				if labels[ownerLabel] != labName || labels[runLabel] != testNetworkRun ||
					labels["clab-topo-file"] != receipt.Topology ||
					labels["clab-node-lab-dir"] != filepath.Join(filepath.Dir(receipt.Topology), "clab-gobfd-vendors", name) {
					t.Fatalf("application %s lost ownership or cleanup discovery: %v", name, labels)
				}
			}
		})
	}
}
