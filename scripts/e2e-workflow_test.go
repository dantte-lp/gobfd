// Package main tests the end-to-end workflow validation helper.
package main

import (
	"os"
	"strings"
	"testing"
)

func TestE2EWorkflowPublishesEvidenceGates(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"../test/e2e/core/run.sh",
		"../test/e2e/core/compose.yml",
		"../test/e2e/core/core_test.go",
		"../test/e2e/core/container_lookup_test.go",
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("legacy core E2E file still exists: %s", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat legacy core E2E file %s: %v", path, err)
		}
	}

	workflow, err := os.ReadFile("../.github/workflows/e2e.yml")
	if err != nil {
		t.Fatalf("read E2E workflow: %v", err)
	}

	content := string(workflow)
	required := []string{
		"name: E2E Evidence",
		"pull_request:",
		"schedule:",
		"workflow_dispatch:",
		"permissions:",
		"contents: read",
		"concurrency:",
		"make e2e-core",
		"make e2e-overlay",
		"make e2e-routing",
		"make e2e-rfc",
		"make e2e-linux",
		"make e2e-vendor",
		"make down",
		"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		"retention-days: 30",
		"if-no-files-found: warn",
	}

	for _, want := range required {
		if !strings.Contains(content, want) {
			t.Fatalf("E2E workflow does not contain %q", want)
		}
	}

	if got := strings.Count(content, "run: make e2e-core\n"); got != 1 {
		t.Fatalf("E2E workflow invokes make e2e-core %d times, want exactly once", got)
	}
	if strings.Contains(content, "make e2e-core-testcontainers") {
		t.Fatal("E2E workflow invokes duplicate core testcontainers migration gate")
	}

	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	if !strings.Contains(string(makefile), "e2e-core: e2e-core-testcontainers\n") {
		t.Fatal("make e2e-core does not delegate to the Go testcontainers runner")
	}

	makeContent := string(makefile)
	for _, name := range []string{"E2E_CORE_COMPOSE", "E2E_CORE_PROJECT", "E2E_CORE_DC"} {
		if strings.Contains(makeContent, name) {
			t.Errorf("Makefile retains legacy core E2E variable %s", name)
		}
	}
	for _, target := range []string{"e2e-core-test", "e2e-core-up", "e2e-core-down", "e2e-core-logs"} {
		if strings.Contains(makeContent, "\n"+target+":") {
			t.Errorf("Makefile retains legacy target %s", target)
		}
		for field := range strings.FieldsSeq(makeContent) {
			if field == target {
				t.Errorf("Makefile retains legacy .PHONY name %s", target)
			}
		}
	}
	if !strings.Contains(makeContent, "\ne2e-core-testcontainers:") {
		t.Fatal("Makefile does not retain e2e-core-testcontainers")
	}

	recipeStart := strings.Index(makeContent, "e2e-core-testcontainers:")
	if recipeStart < 0 {
		t.Fatal("cannot locate e2e-core-testcontainers recipe")
	}
	recipeEnd := strings.Index(makeContent[recipeStart:], "\ne2e-routing:")
	if recipeEnd < 0 {
		t.Fatal("cannot locate e2e-routing after e2e-core-testcontainers recipe")
	}
	recipe := makeContent[recipeStart : recipeStart+recipeEnd]
	for _, assignment := range []string{"DOCKER_HOST=", "PODMAN_HOST=", "CONTAINER_HOST="} {
		if strings.Contains(recipe, assignment) {
			t.Fatalf("make e2e-core overrides caller endpoint with %s: %s", assignment, recipe)
		}
	}
}
