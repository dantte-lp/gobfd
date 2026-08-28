package clabbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testImageID = "sha256:7d8d17484db26d642502e6ba7f4fcb8f18c571c7ba5b456f38675261b154d25b"

type ownedImageRunner struct {
	commands  []Command
	reference string
	runID     string
	imageID   string
	exists    bool
	removed   bool
	foreignID string
	collision bool
}

func (runner *ownedImageRunner) Run(_ context.Context, command Command) (Result, error) {
	runner.commands = append(runner.commands, command)
	if command.Executable != executablePodman || len(command.Arguments) == 0 {
		return Result{}, nil
	}
	switch command.Arguments[0] {
	case "build":
		runner.reference = argumentAfter(command.Arguments, "-t")
		runner.runID = strings.TrimPrefix(argumentAfter(command.Arguments, "--label", 1), runLabel+"=")
		runner.imageID = testImageID
		runner.exists = true
	case "image":
		if len(command.Arguments) < 3 {
			return Result{}, nil
		}
		switch command.Arguments[1] {
		case "exists":
			if runner.collision {
				return Result{}, nil
			}
			if runner.exists && (command.Arguments[2] == runner.reference || command.Arguments[2] == runner.imageID) {
				return Result{}, nil
			}
			return Result{ExitCode: 1}, nil
		case "inspect":
			id := runner.imageID
			if runner.foreignID != "" {
				id = runner.foreignID
			}
			return Result{Stdout: id + "|" + labName + "|" + runner.runID}, nil
		case "rm":
			if command.Arguments[2] != runner.imageID {
				return Result{ExitCode: 2}, nil
			}
			runner.exists = false
			runner.removed = true
		}
	}
	return Result{}, nil
}

func argumentAfter(arguments []string, name string, occurrence ...int) string {
	want := 0
	if len(occurrence) != 0 {
		want = occurrence[0]
	}
	seen := 0
	for index := range len(arguments) - 1 {
		if arguments[index] != name {
			continue
		}
		if seen == want {
			return arguments[index+1]
		}
		seen++
	}
	return ""
}

func TestStageGoBFDImageRecordsRunOwnedIdentity(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions(root)
	runner := &ownedImageRunner{}

	receipt, err := stageGoBFDImage(t.Context(), options, runner)
	if err != nil {
		t.Fatalf("stage GoBFD image: %v", err)
	}
	if receipt.SchemaVersion != 2 || receipt.Image.ID != testImageID {
		t.Fatalf("receipt identity = %#v, want schema 2 and ID %q", receipt, testImageID)
	}
	if !strings.HasPrefix(receipt.Image.Reference, gobfdImageRepo+":") ||
		strings.HasSuffix(receipt.Image.Reference, ":latest") {
		t.Fatalf("owned image reference = %q", receipt.Image.Reference)
	}
	if receipt.Image.Reference != runner.reference || receipt.RunID != runner.runID {
		t.Fatalf(
			"build identity = %q/%q, receipt = %q/%q",
			runner.reference,
			runner.runID,
			receipt.Image.Reference,
			receipt.RunID,
		)
	}
	loaded, err := loadLifecycleReceipt(root)
	if err != nil {
		t.Fatalf("load staged receipt: %v", err)
	}
	if loaded.Image != receipt.Image {
		t.Fatalf("loaded image = %#v, want %#v", loaded.Image, receipt.Image)
	}

	runner.commands = nil
	options.SkipBuild = true
	reused, err := stageGoBFDImage(t.Context(), options, runner)
	if err != nil {
		t.Fatalf("consume staged GoBFD image: %v", err)
	}
	if reused.Image != receipt.Image {
		t.Fatalf("reused image = %#v, want %#v", reused.Image, receipt.Image)
	}
	if err := startGoBFDContainer(t.Context(), options, runner, reused.RunID, reused.Image.ID); err != nil {
		t.Fatalf("start GoBFD container by image ID: %v", err)
	}
	for _, command := range runner.commands {
		if slices.Contains(command.Arguments, "build") {
			t.Fatalf("--skip-build rebuilt staged image: %#v", command)
		}
	}
	runCommand := runner.commands[len(runner.commands)-1]
	if runCommand.Arguments[len(runCommand.Arguments)-2] != testImageID {
		t.Fatalf("container image = %q, want exact ID %q", runCommand.Arguments[len(runCommand.Arguments)-2], testImageID)
	}
}

func TestStageGoBFDImageRejectsReferenceCollisionBeforeBuild(t *testing.T) {
	root := t.TempDir()
	runner := &ownedImageRunner{collision: true}
	_, err := stageGoBFDImage(t.Context(), DefaultOptions(root), runner)
	if !errors.Is(err, errLifecycleState) {
		t.Fatalf("stage collision error = %v, want lifecycle state error", err)
	}
	for _, command := range runner.commands {
		if slices.Contains(command.Arguments, "build") || slices.Contains(command.Arguments, "rm") {
			t.Fatalf("collision caused mutation: %#v", command)
		}
	}
	if _, err := os.Lstat(receiptPath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("collision left receipt: %v", err)
	}
}

func TestCleanupTopologyRemovesOnlyRecordedOwnedImage(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions(root)
	runner := &ownedImageRunner{}
	receipt, err := stageGoBFDImage(t.Context(), options, runner)
	if err != nil {
		t.Fatalf("stage GoBFD image: %v", err)
	}

	if err := cleanupTopology(t.Context(), options, runner, receipt); err != nil {
		t.Fatalf("cleanup staged GoBFD image: %v", err)
	}
	if !runner.removed {
		t.Fatal("owned image was not removed")
	}
	if _, err := os.Lstat(receiptPath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt remains after cleanup: %v", err)
	}
	if !slices.ContainsFunc(runner.commands, func(command Command) bool {
		return command.Executable == executablePodman &&
			slices.Equal(command.Arguments, []string{"image", "rm", testImageID})
	}) {
		t.Fatal("cleanup did not remove the exact recorded image ID without force")
	}
}

func TestCleanupTopologyRejectsChangedImageIdentityBeforeMutation(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions(root)
	runner := &ownedImageRunner{}
	receipt, err := stageGoBFDImage(t.Context(), options, runner)
	if err != nil {
		t.Fatalf("stage GoBFD image: %v", err)
	}
	runner.foreignID = "sha256:8fc12c7a7191ca910982f8d869b432c66cb1f5a6a479115f659507571513856a"
	before := len(runner.commands)

	err = cleanupTopology(t.Context(), options, runner, receipt)
	if !errors.Is(err, errLifecycleState) {
		t.Fatalf("cleanup error = %v, want lifecycle state error", err)
	}
	for _, command := range runner.commands[before:] {
		if slices.Contains(command.Arguments, "rm") || command.Executable == executableContainerlab {
			t.Fatalf("cleanup mutated resources after identity mismatch: %#v", command)
		}
	}
	if _, err := os.Stat(receiptPath(root)); err != nil {
		t.Fatalf("receipt was not retained after mismatch: %v", err)
	}
}

func TestLifecycleLockSerializesRepositoryWorktreesBeforeRunnerMutation(t *testing.T) {
	runtimeBase := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeBase)
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()

	lock, err := acquireLifecycleLock(firstRoot)
	if err != nil {
		t.Fatalf("acquire first worktree lifecycle lock: %v", err)
	}
	t.Cleanup(func() {
		if releaseErr := releaseLifecycleLock(lock); releaseErr != nil {
			t.Errorf("release first worktree lifecycle lock: %v", releaseErr)
		}
	})
	runner := &recordingRunner{}
	options := DefaultOptions(secondRoot)
	options.Down = true

	err = Run(t.Context(), options, runner)
	if err == nil {
		t.Fatal("second worktree acquired the host-global lifecycle lock")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("losing worktree issued commands before lock failure: %#v", runner.commands)
	}
	if _, err := os.Lstat(runtimeDirectory(secondRoot)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("losing worktree mutated its runtime directory: %v", err)
	}
}

func TestLoadLifecycleReceiptAcceptsLegacyContainerOnlySchema(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := runtimeDirectory(root)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create runtime directory: %v", err)
	}
	topology := filepath.Join(root, "legacy.clab.yml")
	receipt := lifecycleReceipt{
		SchemaVersion: 1,
		RunID:         "legacy-run",
		Topology:      topology,
		Containers:    map[string]string{gobfdContainer: "legacy-container-id"},
	}
	if err := createLifecycleReceipt(root, receipt); err != nil {
		t.Fatalf("create legacy receipt: %v", err)
	}

	loaded, err := loadLifecycleReceipt(root)
	if err != nil {
		t.Fatalf("load legacy receipt: %v", err)
	}
	if loaded.SchemaVersion != 1 || loaded.Image != (lifecycleImage{}) {
		t.Fatalf("legacy receipt = %#v", loaded)
	}
}
