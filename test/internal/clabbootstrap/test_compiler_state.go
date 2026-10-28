package clabbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const testRunSchemaVersion = 1

type testRunReceipt struct {
	SchemaVersion int               `json:"schema_version"`
	RunID         string            `json:"run_id"`
	SourceSHA     string            `json:"source_sha"`
	BinarySHA     string            `json:"binary_sha"`
	ConverterSHA  string            `json:"converter_sha"`
	Containers    map[string]string `json:"containers"`
}

func testRunPath(root string) string {
	return filepath.Join(runtimeDirectory(root), "test-run.json")
}

func testRunDirectory(root, runID string) string {
	return filepath.Join(runtimeDirectory(root), "tests-"+runID)
}

func testRunNames(runID string) []string {
	return []string{"gobfd-clab-compile-" + runID, "gobfd-clab-test-" + runID}
}

func validTestRun(receipt testRunReceipt) bool {
	if receipt.SchemaVersion != testRunSchemaVersion || len(receipt.RunID) > 48 ||
		!networkRunRE.MatchString(receipt.RunID) || !networkIDRE.MatchString(receipt.SourceSHA) ||
		(receipt.BinarySHA != "" && !networkIDRE.MatchString(receipt.BinarySHA)) ||
		(receipt.ConverterSHA != "" && !networkIDRE.MatchString(receipt.ConverterSHA)) ||
		len(receipt.Containers) != 2 {
		return false
	}
	for _, name := range testRunNames(receipt.RunID) {
		id, known := receipt.Containers[name]
		if !known || (id != "" && !networkIDRE.MatchString(id)) {
			return false
		}
	}
	return true
}

func writeTestRun(root string, receipt testRunReceipt) error {
	if !validTestRun(receipt) {
		return fmt.Errorf("validate test-run journal fields: %w", errLifecycleState)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode test-run journal: %w", err)
	}
	return replaceLifecycleReceipt(testRunPath(root), append(data, '\n'))
}

func loadTestRun(root string) (testRunReceipt, error) {
	var receipt testRunReceipt
	if err := loadPrivateReceipt(testRunPath(root), &receipt); err != nil {
		return testRunReceipt{}, err
	}
	if !validTestRun(receipt) {
		return testRunReceipt{}, fmt.Errorf("validate test-run journal fields: %w", errLifecycleState)
	}
	return receipt, nil
}

func recoverTestRun(ctx context.Context, options Options, runner Runner) error {
	if options.DryRun {
		return nil
	}
	receipt, err := loadTestRun(options.ProjectRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return cleanupTestRun(ctx, options, runner, receipt)
}

func cleanupTestRun(ctx context.Context, options Options, runner Runner, receipt testRunReceipt) error {
	if options.DryRun {
		return nil
	}
	if !validTestRun(receipt) {
		return fmt.Errorf("validate test-run cleanup fields: %w", errLifecycleState)
	}
	names := testRunNames(receipt.RunID)
	ids := make(map[string]string, len(names))
	for _, name := range names {
		id, _, err := inspectTestRunContainer(ctx, runner, name, receipt.Containers[name], receipt.RunID)
		if err != nil {
			return err
		}
		ids[name] = id
	}
	changed := false
	for _, name := range names {
		if ids[name] != "" && receipt.Containers[name] == "" {
			receipt.Containers[name] = ids[name]
			changed = true
		}
	}
	if changed {
		if err := writeTestRun(options.ProjectRoot, receipt); err != nil {
			return fmt.Errorf("checkpoint recovered test-run containers: %w", err)
		}
	}
	if err := removeTestRunContainers(ctx, runner, receipt); err != nil {
		return err
	}
	return removeTestRunFiles(options.ProjectRoot, receipt.RunID)
}

func removeTestRunContainers(ctx context.Context, runner Runner, receipt testRunReceipt) error {
	for _, name := range testRunNames(receipt.RunID) {
		wantID := receipt.Containers[name]
		if wantID == "" {
			continue
		}
		id, present, err := inspectTestRunContainer(ctx, runner, name, wantID, receipt.RunID)
		if err != nil {
			return err
		}
		if present {
			if err := runCommand(ctx, runner, Command{
				Executable: executablePodman, Arguments: []string{"rm", "--force", id},
			}); err != nil {
				return err
			}
		}
		if err := verifyTestRunContainerAbsent(ctx, runner, name, wantID); err != nil {
			return err
		}
	}
	for _, name := range testRunNames(receipt.RunID) {
		if err := verifyTestRunContainerAbsent(ctx, runner, name, receipt.Containers[name]); err != nil {
			return err
		}
	}
	return nil
}

func inspectTestRunContainer(
	ctx context.Context, runner Runner, name, wantID, runID string,
) (string, bool, error) {
	present, err := containerExists(ctx, runner, name)
	if err != nil {
		return "", false, err
	}
	if !present {
		if absentErr := verifyTestRunContainerAbsent(ctx, runner, name, wantID); absentErr != nil {
			return "", false, absentErr
		}
		return "", false, nil
	}
	id, err := inspectContainerIdentity(ctx, runner, name, runID, true)
	if err != nil {
		return "", false, err
	}
	if !networkIDRE.MatchString(id) || (wantID != "" && id != wantID) {
		return "", false, fmt.Errorf("test-run container %s changed identity: %w", name, errLifecycleState)
	}
	return id, true, nil
}

func verifyTestRunContainerAbsent(ctx context.Context, runner Runner, name, id string) error {
	for _, reference := range []string{name, id} {
		if reference == "" {
			continue
		}
		present, err := containerExists(ctx, runner, reference)
		if err != nil {
			return err
		}
		if present {
			return fmt.Errorf("test-run container %s remains present: %w", name, errLifecycleState)
		}
	}
	return nil
}

func removeTestRunFiles(projectRoot, runID string) (returnErr error) {
	root, err := os.OpenRoot(runtimeDirectory(projectRoot))
	if err != nil {
		return fmt.Errorf("open test-run runtime root: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open test-run runtime directory: %w", err)
	}
	validationErr := validateReceiptDirectory(directory, runtimeDirectory(projectRoot))
	if closeErr := errors.Join(validationErr, directory.Close()); closeErr != nil {
		return closeErr
	}
	journal := filepath.Base(testRunPath(projectRoot))
	if _, inspectErr := inspectReceiptDestination(root, journal); inspectErr != nil {
		return inspectErr
	}
	contextName := filepath.Base(testRunDirectory(projectRoot, runID))
	info, err := root.Lstat(contextName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect test-run context: %w", err)
	}
	if err == nil && !info.IsDir() {
		return fmt.Errorf("test-run context is not a directory: %w", errLifecycleState)
	}
	if removeErr := root.RemoveAll(contextName); removeErr != nil {
		return fmt.Errorf("remove test-run context: %w", removeErr)
	}
	if _, inspectErr := root.Lstat(contextName); !errors.Is(inspectErr, os.ErrNotExist) {
		return fmt.Errorf("test-run context remains or cannot be inspected: %w",
			errors.Join(inspectErr, errLifecycleState))
	}
	if _, inspectErr := inspectReceiptDestination(root, journal); inspectErr != nil {
		return inspectErr
	}
	if removeErr := root.Remove(journal); removeErr != nil {
		return fmt.Errorf("remove test-run journal: %w", removeErr)
	}
	return nil
}
