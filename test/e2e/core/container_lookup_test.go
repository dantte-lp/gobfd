//go:build e2e_core

package core_test

import (
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

func TestContainerIDFromSummaries(t *testing.T) {
	containers := []podmanapi.ContainerSummary{
		{
			ID:    "ignored",
			Names: []string{"gobfd-e2e-core_gobfd-a_1"},
		},
		{
			ID: "label-id",
			Labels: map[string]string{
				"io.podman.compose.project": "gobfd-e2e-core",
				"io.podman.compose.service": "gobfd-a",
			},
		},
	}

	id := containerIDFromSummaries(containers, "gobfd-e2e-core", "gobfd-a")
	if id != "label-id" {
		t.Fatalf("containerIDFromSummaries = %q, want label-id", id)
	}
}

func TestContainerIDFromSummariesFallsBackToComposeName(t *testing.T) {
	containers := []podmanapi.ContainerSummary{
		{
			ID:    "name-id",
			Names: []string{"/gobfd-e2e-core_gobfd-a_1"},
		},
	}

	id := containerIDFromSummaries(containers, "gobfd-e2e-core", "gobfd-a")
	if id != "name-id" {
		t.Fatalf("containerIDFromSummaries = %q, want name-id", id)
	}
}

func TestSummarizeContainersIncludesVisibleAPIState(t *testing.T) {
	summary := summarizeContainers([]podmanapi.ContainerSummary{
		{
			ID:    "1234567890abcdef",
			Names: []string{"gobfd-e2e-core_gobfd-a_1"},
			Labels: map[string]string{
				"io.podman.compose.project": "gobfd-e2e-core",
				"io.podman.compose.service": "gobfd-a",
			},
		},
	})

	for _, want := range []string{"1234567890ab", "gobfd-e2e-core_gobfd-a_1", "gobfd-e2e-core/gobfd-a"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary = %q, want substring %q", summary, want)
		}
	}
}
