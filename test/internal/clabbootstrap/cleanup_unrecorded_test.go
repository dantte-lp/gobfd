package clabbootstrap

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestCleanupRejectsUnrecordedLabContainersBeforeMutation(t *testing.T) {
	for _, name := range []string{gobfdContainer, "clab-gobfd-vendors-frr", "clab-gobfd-vendors-arista"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			options := DefaultOptions(root)
			runner := &ownedImageRunner{unrecorded: name}
			receipt, err := stageTestImages(t, options, runner)
			if err != nil {
				t.Fatal(err)
			}
			receipt.Topology = runtimeTopologyPath(root)
			receipt.Network.Name = networkName(receipt.RunID)
			receipt.ExpectedContainers = []string{gobfdContainer, frrContainer}
			if err := writeLifecycleReceipt(root, receipt); err != nil {
				t.Fatal(err)
			}
			runner.commands = nil
			cleanupErr := cleanupTopology(t.Context(), options, runner, receipt)
			if !errors.Is(cleanupErr, errLifecycleState) {
				t.Fatalf("cleanup with unrecorded %s: error=%v, want lifecycle rejection", name, cleanupErr)
			}
			for _, command := range runner.commands {
				if command.Executable == executableContainerlab || slices.Contains(command.Arguments, "rm") {
					t.Fatalf("unrecorded %s allowed cleanup mutation: %v", name, command)
				}
			}
			if _, err := os.Stat(receiptPath(root)); err != nil {
				t.Fatalf("cleanup rejection lost receipt: %v", err)
			}
		})
	}
}

func TestStagedImageCleanupDoesNotSelectForeignLab(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions(root)
	runner := &ownedImageRunner{unrecorded: "clab-gobfd-vendors-frr"}
	receipt, err := stageTestImages(t, options, runner)
	if err != nil {
		t.Fatal(err)
	}
	runner.commands = nil
	if err := cleanupTopology(t.Context(), options, runner, receipt); err != nil {
		t.Fatal(err)
	}
	if !runner.removed {
		t.Fatal("staged image was not removed")
	}
	for _, command := range runner.commands {
		if command.Executable == executableContainerlab {
			t.Fatalf("image-only cleanup selected foreign lab: %v", command)
		}
	}
}
