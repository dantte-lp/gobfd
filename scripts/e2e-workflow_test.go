// Package main tests the end-to-end workflow validation helper.
package main

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestE2EWorkflowUsesGoOwnedRuntimeSetupAndDiagnostics(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../.github/workflows/e2e.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run             string `yaml:"run"`
				If              string `yaml:"if"`
				ContinueOnError bool   `yaml:"continue-on-error"` //nolint:tagliatelle // GitHub Actions defines this exact field.
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse E2E workflow: %v", err)
	}
	if len(workflow.Jobs) != 3 {
		t.Fatalf("expected three admitted E2E jobs, got %d", len(workflow.Jobs))
	}
	for _, name := range []string{"pr-safe", "nightly", "vendor"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			job, found := workflow.Jobs[name]
			if !found {
				t.Fatalf("E2E job %s is missing", name)
			}
			var bootstrapCount, diagnosticsCount, cleanupCount int
			bootstrapIndex, startIndex := -1, -1
			for index, step := range job.Steps {
				if strings.ContainsAny(step.Run, "\r\n|;") || strings.Contains(step.Run, "&&") {
					t.Errorf("job retains a repository-owned shell program: %q", step.Run)
				}
				switch step.Run {
				case "go run ./test/cmd/toolbootstrap podman-e2e":
					bootstrapCount++
					bootstrapIndex = index
					if step.ContinueOnError || step.If != "" {
						t.Error("runtime setup can be skipped or its failure ignored")
					}
				case "go run ./test/cmd/toolbootstrap apparmor-denials":
					diagnosticsCount++
					if step.If != "always()" || !step.ContinueOnError {
						t.Error("diagnostics lost always/nonblocking workflow semantics")
					}
				case "make down":
					cleanupCount++
					if step.If != "always()" || !step.ContinueOnError {
						t.Error("cleanup lost always/nonblocking workflow semantics")
					}
				case "make up":
					startIndex = index
				}
			}
			if bootstrapCount != 1 || diagnosticsCount != 1 || cleanupCount != 1 {
				t.Errorf("setup/diagnostics/cleanup counts=%d/%d/%d, want 1/1/1",
					bootstrapCount, diagnosticsCount, cleanupCount)
			}
			if bootstrapIndex < 0 || startIndex <= bootstrapIndex {
				t.Error("runtime setup must precede dev startup")
			}
		})
	}
}

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
		"run: go run ./test/cmd/cictl package-build\n",
		"run: go run ./test/cmd/cictl package-lifecycle\n",
		"make down",
		"actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9",
		"retention-days: 30",
		"if-no-files-found: warn",
	}

	for _, want := range required {
		if !strings.Contains(content, want) {
			t.Fatalf("E2E workflow does not contain %q", want)
		}
	}

	// Ubuntu 24.04 Podman 4.9.3 links Debian imagebuilder 1.2.5, which rejects
	// ADD --checksum in the interop Containerfiles; ubuntu-latest still maps to it.
	if got := strings.Count(content, "runs-on: ubuntu-26.04\n"); got != 3 {
		t.Fatalf("E2E workflow pins ubuntu-26.04 on %d jobs, want all 3", got)
	}
	if strings.Contains(content, "ubuntu-latest") {
		t.Fatal("E2E workflow uses ubuntu-latest, whose Podman rejects ADD --checksum")
	}

	// The package lifecycle runs only in the nightly job, after the routing
	// gates and before the shared evidence upload.
	nightly := content[strings.Index(content, "\n  nightly:"):strings.Index(content, "\n  vendor:")]
	if strings.Count(content, "cictl package-lifecycle") != 1 ||
		!strings.Contains(nightly, "cictl package-build") || !strings.Contains(nightly, "cictl package-lifecycle") ||
		strings.Index(nightly, "cictl package-lifecycle") > strings.Index(nightly, "Upload nightly E2E artifacts") {
		t.Fatal("E2E workflow does not run the package lifecycle once in the nightly job before its upload")
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
