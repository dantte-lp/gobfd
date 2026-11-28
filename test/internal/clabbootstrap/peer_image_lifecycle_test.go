package clabbootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestStageRecordsBothApplicationImages(t *testing.T) {
	root := t.TempDir()
	runner := &ownedImageRunner{}
	receipt, err := stageTestImages(t, DefaultOptions(root), runner)
	if err != nil {
		t.Fatal(err)
	}
	var builds []Command
	for _, command := range runner.commands {
		if command.Executable == executablePodman && len(command.Arguments) > 0 && command.Arguments[0] == "build" {
			builds = append(builds, command)
		}
	}
	if len(builds) != 2 {
		t.Fatalf("application image builds = %d, want GoBFD and FRR", len(builds))
	}
	file := filepath.Join(root, "test", "interop", "frr", "Containerfile")
	if argumentAfter(builds[1].Arguments, "-f") != file ||
		builds[1].Arguments[len(builds[1].Arguments)-1] != filepath.Dir(file) {
		t.Fatalf("FRR build does not consume shared recipe/context: %v", builds[1].Arguments)
	}
	for _, build := range builds {
		if argumentAfter(build.Arguments, "--memory") != "2g" ||
			argumentAfter(build.Arguments, "--memory-swap") != "2g" ||
			argumentAfter(build.Arguments, "--cpu-quota") != "200000" {
			t.Fatalf("application build lacks bounded resources: %v", build.Arguments)
		}
	}
	data, err := os.ReadFile(receiptPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Schema int            `json:"schema_version"`
		Image  lifecycleImage `json:"image"`
		FRR    lifecycleImage `json:"frr"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Schema != 3 || stored.Image.ID == "" || stored.FRR.ID == "" || stored.Image.ID == stored.FRR.ID {
		t.Fatalf("receipt does not retain two distinct application image IDs: %+v", stored)
	}
	if stored.Image != receipt.Image {
		t.Fatal("staged GoBFD identity differs from persisted receipt")
	}
}

func TestFailedSecondApplicationBuildCleansBothOwnedImages(t *testing.T) {
	root := t.TempDir()
	runner := &ownedImageRunner{failFRRBuild: true}
	_, err := stageTestImages(t, DefaultOptions(root), runner)
	if !errors.Is(err, ErrBootstrapFailed) {
		t.Fatalf("second build failure was lost: %v", err)
	}
	if len(runner.images) != 0 {
		t.Fatalf("failed second build left owned image identities: %v", runner.images)
	}
	if _, err := os.Lstat(receiptPath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful rollback retained receipt: %v", err)
	}
}

func TestFRRImageSubstitutionRejectsCleanupBeforeAnyMutation(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions(root)
	runner := &ownedImageRunner{}
	receipt, err := stageTestImages(t, options, runner)
	if err != nil {
		t.Fatal(err)
	}
	runner.foreignReference = receipt.FRR.Reference
	runner.foreignID = "sha256:8fc12c7a7191ca910982f8d869b432c66cb1f5a6a479115f659507571513856a"
	runner.commands = nil
	if err := cleanupTopology(t.Context(), options, runner, receipt); !errors.Is(err, errLifecycleState) {
		t.Fatalf("FRR substitution accepted: %v", err)
	}
	for _, command := range runner.commands {
		if slices.Contains(command.Arguments, "rm") || command.Executable == executableContainerlab {
			t.Fatalf("FRR substitution allowed cleanup mutation: %v", command)
		}
	}
	if len(runner.images) != 2 {
		t.Fatal("FRR substitution removed an application image")
	}
}

func TestSkipBuildRejectsMissingFRRWithoutRebuild(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions(root)
	runner := &ownedImageRunner{}
	receipt, err := stageTestImages(t, options, runner)
	if err != nil {
		t.Fatal(err)
	}
	delete(runner.images, receipt.FRR.Reference)
	runner.commands = nil
	options.SkipBuild = true
	if _, err := stageTestImages(t, options, runner); !errors.Is(err, errLifecycleState) {
		t.Fatalf("missing FRR image accepted: %v", err)
	}
	for _, command := range runner.commands {
		if slices.Contains(command.Arguments, "build") || slices.Contains(command.Arguments, "rm") {
			t.Fatalf("skip-build silently mutated missing FRR: %v", command)
		}
	}
}
