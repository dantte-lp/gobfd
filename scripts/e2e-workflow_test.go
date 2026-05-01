package main

import (
	"os"
	"strings"
	"testing"
)

func TestE2EWorkflowPublishesEvidenceGates(t *testing.T) {
	t.Parallel()

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
}
