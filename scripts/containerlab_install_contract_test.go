package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerlabBootstrapOwnedOrchestrationUsesGo(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, path := range []string{
		filepath.Join(repositoryRoot, "test", "internal", "clabbootstrap", "orchestration.go"),
		filepath.Join(repositoryRoot, "test", "internal", "vendorprofiles", "manifest.go"),
		filepath.Join(repositoryRoot, "test", "cmd", "clabbootstrap", "main.go"),
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Errorf("required bootstrap split path %s: %v", path, statErr)
			continue
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			t.Errorf("bootstrap split path %s is not a nonempty regular file", path)
		}
	}
	legacy := filepath.Join(repositoryRoot, "test", "interop-clab", "bootstrap.py")
	if _, statErr := os.Lstat(legacy); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("legacy Python bootstrap still exists: %v", statErr)
	}

	pythonFiles, globErr := filepath.Glob(filepath.Join(repositoryRoot, "test", "interop-clab", "*.py"))
	if globErr != nil {
		t.Fatalf("scan Python bootstrap files: %v", globErr)
	}
	if len(pythonFiles) != 0 {
		t.Errorf("vendor Python files remain: %v", pythonFiles)
	}

	makefile := readContractFile(t, "../Makefile")
	// TestMakeClabTargetsUseIsolatedColdController executes the actual Make targets;
	// keep this contract focused on source retention and the removed Python entrypoint.
	if strings.Contains(makefile, "test/interop-clab/bootstrap.py") {
		t.Error("Makefile still references the legacy Python bootstrap")
	}
}
