package dependencyinventory

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBuildUsesOnlyPublicSelectionTracking(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.invalid/runtime\n\ngo 1.27.0\n")
	writeFixture(t, root, "main.go", "package main\n\nfunc main() {}\n")
	writeFixture(t, root, "go.sum", "")
	writeFixture(t, root, "tools/go.mod", "module example.invalid/tools\n\ngo 1.27.0\n")
	writeFixture(t, root, "tools/go.sum", "")
	writeFixture(t, root, "tools/gopls/go.mod", "module example.invalid/gopls\n\ngo 1.27.0\n")
	writeFixture(t, root, "tools/gopls/go.sum", "")
	writeFixture(t, root, "tools/syft/go.mod", "module example.invalid/syft\n\ngo 1.27.0\n")
	writeFixture(t, root, "tools/syft/go.sum", "")
	writeFixture(t, root, "tools/goreleaser/qualified.sum", "")
	// Source preparation is covered by its owning package. This fixture checks
	// tracking on generated records without downloading an entire publisher tree.
	inv, err := buildInventory(context.Background(), root,
		func(ctx context.Context, root string, graph ModuleGraph) ([]moduleIdentity, error) {
			if graph.ID == goreleaserGraphID {
				return []moduleIdentity{{Path: "example.invalid/publisher-dependency", Version: "v1.0.0"}}, nil
			}
			return selectedModuleGraph(ctx, root, graph)
		})
	if err != nil {
		t.Fatalf("build public inventory: %v", err)
	}
	if len(inv.ModuleGraphs) != 5 || len(inv.Components) == 0 {
		t.Fatalf("native generator fixture did not exercise graphs and records: %#v", inv)
	}
	for subject, record := range inventoryRecords(&inv) {
		if record.Assessment.Decision.Tracking != "https://github.com/dantte-lp/gobfd/pull/105" {
			t.Fatalf("%s tracking = %q, want current public release review", subject, record.Assessment.Decision.Tracking)
		}
	}
	encoded, err := json.Marshal(inv)
	if err != nil {
		t.Fatalf("encode public inventory: %v", err)
	}
	for _, private := range []string{"gobfd-qj", "bead:", "bd show "} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("native generator reintroduced private tracking marker %q", private)
		}
	}
	templates := []string{"runtime-direct-audited", "tools-selected-graph", "gobgp-v4-risk", "removed-direct-require"}
	for _, template := range templates {
		if tracking := assessmentTemplate(template).Decision.Tracking; tracking != devTracking {
			t.Fatalf("template %s tracking = %q, want public release review", template, tracking)
		}
	}
}

func TestPublicTrackingNormalizationPreservesPublicReferencesAndEvidence(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	decision := &inv.Components[0].Assessment.Decision
	decision.Tracking = "gobfd-qj0.8.2.8.7"
	decision.LicenseException = &LicenseException{Tracking: "bead:gobfd-qj0.8.1.1"}
	const publicURL = "https://github.com/dantte-lp/gobfd/issues/88"
	decision.ReviewException = &ReviewException{Tracking: publicURL}
	evidence := Evidence{
		ID: "historical-observation", Source: "https://example.invalid/immutable-measurement",
		ObservedAt: "2026-10-28T19:52:52Z", Result: "historical measured value", Hash: "unchanged-original-hash",
	}
	inv.Evidence = []Evidence{evidence}
	normalizePublicTracking(&inv)
	if decision.Tracking != devTracking || decision.LicenseException.Tracking != devTracking ||
		decision.ReviewException.Tracking != publicURL {
		t.Fatalf("tracking normalization = %#v", decision)
	}
	if !reflect.DeepEqual(inv.Evidence, []Evidence{evidence}) {
		t.Fatal("public tracking normalization rewrote historical observation data")
	}
	before, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	normalizePublicTracking(&inv)
	after, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("public tracking normalization is not idempotent")
	}
}
