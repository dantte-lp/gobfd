package clabbootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testRunStateContainer struct {
	id    string
	owner string
	runID string
}

type testRunStateRunner struct {
	containers map[string]testRunStateContainer
	commands   []Command
	failRemove bool
}

func (runner *testRunStateRunner) Run(_ context.Context, command Command) (Result, error) {
	runner.commands = append(runner.commands, command)
	if command.Executable != executablePodman || len(command.Arguments) == 0 {
		return Result{}, fmt.Errorf("unexpected test-run command: %v", command)
	}
	reference := command.Arguments[len(command.Arguments)-1]
	switch command.Arguments[0] {
	case "container":
		for name, container := range runner.containers {
			if reference == name || reference == container.id {
				return Result{}, nil
			}
		}
		return Result{ExitCode: 1}, nil
	case "inspect":
		container, present := runner.containers[reference]
		if !present {
			return Result{ExitCode: 1}, nil
		}
		return Result{Stdout: fmt.Sprintf("%s|%s|%s|%s",
			container.id, labName, container.owner, container.runID)}, nil
	case "rm":
		if runner.failRemove {
			return Result{ExitCode: 42}, nil
		}
		for name, container := range runner.containers {
			if reference == container.id {
				delete(runner.containers, name)
				return Result{}, nil
			}
		}
		return Result{ExitCode: 1}, nil
	default:
		return Result{}, fmt.Errorf("unexpected test-run Podman operation: %v", command.Arguments)
	}
}

func testRunStateFixture(t *testing.T) (Options, testRunReceipt, *testRunStateRunner) {
	t.Helper()
	options := DefaultOptions(t.TempDir())
	if err := ensureRuntimeDirectory(options.ProjectRoot); err != nil {
		t.Fatal(err)
	}
	receipt := testRunReceipt{
		SchemaVersion: 1, RunID: "state-run", SourceSHA: strings.Repeat("a", 64),
		Containers: map[string]string{
			"gobfd-clab-compile-state-run": strings.Repeat("1", 64),
			"gobfd-clab-test-state-run":    strings.Repeat("2", 64),
		},
	}
	runner := &testRunStateRunner{containers: make(map[string]testRunStateContainer)}
	for name, id := range receipt.Containers {
		runner.containers[name] = testRunStateContainer{id: id, owner: labName, runID: receipt.RunID}
	}
	directory := testRunDirectory(options.ProjectRoot, receipt.RunID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "source-marker"), []byte("owned source"), 0o600); err != nil {
		t.Fatal(err)
	}
	return options, receipt, runner
}

func TestTestRunCleanupRejectsForeignAndChangedIDsBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"foreign", "changed-id"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			options, receipt, runner := testRunStateFixture(t)
			name := "gobfd-clab-test-state-run"
			container := runner.containers[name]
			if kind == "foreign" {
				container.owner = "foreign"
			} else {
				container.id = strings.Repeat("3", 64)
			}
			runner.containers[name] = container
			if err := createPrivateReceipt(testRunPath(options.ProjectRoot), receipt); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(testRunPath(options.ProjectRoot))
			if err != nil {
				t.Fatal(err)
			}
			cleanupErr := cleanupTestRun(t.Context(), options, runner, receipt)
			if !errors.Is(cleanupErr, errLifecycleState) {
				t.Fatalf("untrusted test-run identity was accepted: %v", cleanupErr)
			}
			for _, command := range runner.commands {
				if command.Arguments[0] == "rm" {
					t.Fatalf("identity rejection allowed removal: %v", command)
				}
			}
			after, err := os.ReadFile(testRunPath(options.ProjectRoot))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("identity rejection changed the journal: %v", err)
			}
			marker := filepath.Join(testRunDirectory(options.ProjectRoot, receipt.RunID), "source-marker")
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("identity rejection lost source context: %v", err)
			}
		})
	}
}

func TestRecoverTestRunCheckpointsPartialCreationAndRetries(t *testing.T) {
	t.Parallel()
	options, receipt, runner := testRunStateFixture(t)
	compileName := "gobfd-clab-compile-state-run"
	delete(runner.containers, "gobfd-clab-test-state-run")
	for name := range receipt.Containers {
		receipt.Containers[name] = ""
	}
	if err := createPrivateReceipt(testRunPath(options.ProjectRoot), receipt); err != nil {
		t.Fatal(err)
	}
	runner.failRemove = true
	if err := recoverTestRun(t.Context(), options, runner); !errors.Is(err, ErrBootstrapFailed) {
		t.Fatalf("failed exact-ID removal was lost: %v", err)
	}
	stored, err := loadTestRun(options.ProjectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Containers[compileName] != strings.Repeat("1", 64) {
		t.Fatal("partial creation was not checkpointed before attempted removal")
	}
	marker := filepath.Join(testRunDirectory(options.ProjectRoot, receipt.RunID), "source-marker")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("failed removal lost source context: %v", err)
	}
	runner.failRemove = false
	if err := recoverTestRun(t.Context(), options, runner); err != nil {
		t.Fatalf("recovery retry failed: %v", err)
	}
	if len(runner.containers) != 0 {
		t.Fatal("recovery retry retained its container")
	}
	paths := []string{testRunPath(options.ProjectRoot), testRunDirectory(options.ProjectRoot, receipt.RunID)}
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("successful recovery retained %s: %v", path, err)
		}
	}
	if err := recoverTestRun(t.Context(), options, runner); err != nil {
		t.Fatalf("absent journal is not an idempotent recovery: %v", err)
	}
}

func TestTestRunDryRunPreservesJournalAndContext(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"recover", "cleanup"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			options, receipt, runner := testRunStateFixture(t)
			options.DryRun = true
			path := testRunPath(options.ProjectRoot)
			if err := createPrivateReceipt(path, receipt); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "recover" {
				err = recoverTestRun(t.Context(), options, runner)
			} else {
				err = cleanupTestRun(t.Context(), options, runner, receipt)
			}
			if err != nil {
				t.Fatalf("dry-run %s failed: %v", operation, err)
			}
			if len(runner.commands) != 0 || len(runner.containers) != 2 {
				t.Fatalf("dry-run %s touched engine resources: commands=%d containers=%d",
					operation, len(runner.commands), len(runner.containers))
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("dry-run %s changed the journal: %v", operation, err)
			}
			marker := filepath.Join(testRunDirectory(options.ProjectRoot, receipt.RunID), "source-marker")
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("dry-run %s lost source context: %v", operation, err)
			}
		})
	}
}

func TestLoadTestRunRejectsUnsafeJournalFields(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"unknown-name", "invalid-run", "uppercase-id", "missing-source"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			options, receipt, _ := testRunStateFixture(t)
			switch kind {
			case "unknown-name":
				delete(receipt.Containers, "gobfd-clab-test-state-run")
				receipt.Containers["foreign-container"] = strings.Repeat("2", 64)
			case "invalid-run":
				receipt.RunID = "../foreign"
			case "uppercase-id":
				receipt.Containers["gobfd-clab-test-state-run"] = strings.Repeat("A", 64)
			case "missing-source":
				receipt.SourceSHA = ""
			}
			if err := createPrivateReceipt(testRunPath(options.ProjectRoot), receipt); err != nil {
				t.Fatal(err)
			}
			if _, err := loadTestRun(options.ProjectRoot); !errors.Is(err, errLifecycleState) {
				t.Fatalf("unsafe journal %s was accepted: %v", kind, err)
			}
		})
	}
}
