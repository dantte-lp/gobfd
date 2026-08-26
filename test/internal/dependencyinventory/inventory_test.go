//nolint:lll,modernize // Exact references stay literal; explicit embedded records keep current gopls compatibility.
package dependencyinventory

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRepositoryInventoryMatchesDeclaredDependencies(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	err := Check(context.Background(), Options{
		Root:          root,
		InventoryPath: "docs/supply-chain/dependency-inventory.json",
		SchemaPath:    "docs/supply-chain/dependency-inventory.schema.json",
	})
	if err != nil {
		t.Fatalf("check repository dependency inventory: %v", err)
	}
	inv, err := readInventory(filepath.Join(root, "docs/supply-chain/dependency-inventory.json"))
	if err != nil {
		t.Fatalf("read checked repository inventory: %v", err)
	}
	wantGraphCounts := map[string]int{"runtime": 196, "tools": 387}
	for _, graph := range inv.ModuleGraphs {
		if len(graph.Modules) != wantGraphCounts[graph.ID] {
			t.Fatalf("%s module count = %d, want %d", graph.ID, len(graph.Modules), wantGraphCounts[graph.ID])
		}
		for _, module := range graph.Modules {
			if module.Target != module.Version {
				t.Fatalf("%s %s target = %q, want selected version %q", graph.ID, module.Path, module.Target, module.Version)
			}
			if graph.ID == "runtime" && module.Path == "github.com/ovn-kubernetes/libovsdb" &&
				module.Coordinates.SourceRepository != "https://github.com/ovn-kubernetes/libovsdb" {
				t.Fatalf("libovsdb source repository = %q, want active owner", module.Coordinates.SourceRepository)
			}
			if graph.ID == "runtime" && module.Indirect && module.Assessment.UpstreamCurrent.Status != ReviewNotApplicable {
				t.Fatalf("runtime transitive %s upstream status = %q, want not-applicable", module.Path, module.Assessment.UpstreamCurrent.Status)
			}
		}
	}
	if len(inv.Evidence) == 0 {
		t.Fatal("repository inventory has no structured evidence")
	}
	for _, component := range inv.Components {
		decision := component.Assessment.Decision
		if decision.Status == DecisionDeferred || decision.Status == DecisionStale {
			if decision.Tracking == "" || decision.ReviewBy == "" {
				t.Fatalf("deferred/stale component %s lacks tracking or review date", component.ID)
			}
		}
	}
	assertRepositoryReview(
		t,
		inv,
		"runtime:github.com/ovn-kubernetes/libovsdb",
		"repository_archived",
		ReviewVerified,
		"active",
	)
	assertRepositoryReview(
		t,
		inv,
		"runtime:github.com/ovn-kubernetes/libovsdb",
		"license",
		ReviewVerified,
		"Apache-2.0",
	)
	assertRepositoryReview(
		t,
		inv,
		"runtime:github.com/ovn-kubernetes/libovsdb",
		"artifact_available",
		ReviewVerified,
		"available",
	)
	assertRepositoryReview(t, inv, "github-action:actions/checkout", "license", ReviewVerified, "MIT")
	assertRepositoryReview(t, inv, "github-action:actions/checkout", "channel_current", ReviewUnverified, "immutable commit identity does not prove channel currency")
	assertRepositoryReview(t, inv, "github-action:actions/checkout", "security", ReviewUnverified, "immutable pinning does not prove vulnerability status")
	assertRepositoryComponent(t, inv, "oci-image:docker.io/library/golang:1.27.0-trixie@sha256:ae28539d2ef595b9a2930dd7f031d9592376829dc0eae7cb869559f7d5812c3a", "1.27.0-trixie", "test/interop/thoro/Containerfile")
	for _, component := range inv.Components {
		if component.Kind == KindOCIImage && strings.Contains(strings.ToLower(component.Installed), "alpine") {
			t.Fatalf("forbidden Alpine OCI image remains in inventory: %s", component.Installed)
		}
		if strings.HasSuffix(component.Installed, ".") {
			t.Fatalf("component %s retains trailing punctuation in installed reference %q", component.ID, component.Installed)
		}
	}
}

func assertRepositoryReview(t *testing.T, inv Inventory, id, field string, status ReviewStatus, value string) {
	t.Helper()

	for _, graph := range inv.ModuleGraphs {
		for _, module := range graph.Modules {
			if graph.ID+":"+module.Path == id {
				assertReview(t, id, field, module.Record, status, value)
				return
			}
		}
	}
	for _, component := range inv.Components {
		if component.ID == id {
			assertReview(t, id, field, component.Record, status, value)
			return
		}
	}
	t.Fatalf("dependency %s is missing", id)
}

func assertReview(t *testing.T, id, field string, record Record, status ReviewStatus, value string) {
	t.Helper()

	reviews := map[string]Review{
		"channel_current":     record.Assessment.ChannelCurrent,
		"security":            record.Assessment.Security,
		"license":             record.Assessment.License,
		"repository_archived": record.RepositoryState.RepositoryArchived,
		"artifact_available":  record.RepositoryState.ArtifactAvailable,
	}
	review, ok := reviews[field]
	if !ok {
		t.Fatalf("test has unknown review field %s", field)
	}
	wantEvidence := status == ReviewVerified || status == ReviewStale
	if review.Status != status || review.Value != value || (len(review.EvidenceIDs) > 0) != wantEvidence {
		t.Fatalf("dependency %s %s = %#v, want status %s, value %q, evidence=%t", id, field, review, status, value, wantEvidence)
	}
}

func assertRepositoryComponent(t *testing.T, inv Inventory, id, installedFragment, sourcePath string) {
	t.Helper()

	for _, component := range inv.Components {
		if component.ID != id {
			continue
		}
		if !strings.Contains(component.Installed, installedFragment) {
			t.Fatalf("component %s installed = %q, want fragment %q", id, component.Installed, installedFragment)
		}
		for _, source := range component.SourceLocations {
			if source.Path == sourcePath {
				return
			}
		}
		t.Fatalf("component %s lacks source path %s", id, sourcePath)
	}
	t.Fatalf("component %s is missing", id)
}

func TestValidateRejectsDuplicateDependencyIDs(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components = append(inv.Components, inv.Components[0])

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "duplicate dependency id") {
		t.Fatalf("Validate() error = %v, want duplicate dependency id", err)
	}
}

func TestValidateRejectsVerifiedReviewWithoutMatchingEvidence(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	evidenceID := stableEvidenceID("wrong-subject", "channel_current")
	inv.Evidence = []Evidence{{
		ID: evidenceID, Source: "fixture", Subject: "wrong-subject", Review: "channel_current",
		CommandOrQuery: "bd show fixture", Tool: EvidenceTool{Name: "bd", Version: "v1"},
		ObservedAt: "2026-08-21T00:00:00Z", Result: "selected", Hash: evidenceResultHash("selected"),
	}}
	inv.Components[0].Assessment.ChannelCurrent = Review{
		Status: ReviewVerified, Value: "selected", EvidenceIDs: []string{evidenceID},
	}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "does not match subject and review") {
		t.Fatalf("Validate() error = %v, want mismatched evidence rejection", err)
	}
}

func TestValidateRejectsDuplicateEvidenceID(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	evidenceID := stableEvidenceID("tool:example", "artifact_available")
	evidence := Evidence{
		ID: evidenceID, Source: "fixture", Subject: "tool:example", Review: "artifact_available",
		CommandOrQuery: "go list -m -json all", Tool: EvidenceTool{Name: "go", Version: "v1"},
		ObservedAt: "2026-08-21T00:00:00Z", Result: "available", Hash: evidenceResultHash("available"),
	}
	inv.Evidence = []Evidence{evidence, evidence}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "duplicate evidence id") {
		t.Fatalf("Validate() error = %v, want duplicate evidence rejection", err)
	}
}

func TestValidateRejectsMissingCanonicalCoordinates(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components[0].Coordinates = Coordinates{}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "canonical coordinates") {
		t.Fatalf("Validate() error = %v, want missing canonical coordinates", err)
	}
}

func TestValidateRejectsMissingRepositoryStateDimension(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components[0].RepositoryState.ReleaseLineEOL.Status = ""

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "release_line_eol.status") {
		t.Fatalf("Validate() error = %v, want missing release line EOL", err)
	}
}

func TestValidateRejectsAdoptedUnverifiedLicenseWithoutException(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components[0].Assessment.License = Review{
		Status: ReviewUnverified, Value: "license corpus is not yet reproducible",
	}
	inv.Components[0].Assessment.Decision.Status = DecisionAdopted
	inv.Components[0].Assessment.Decision.LicenseException = nil

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unverified license requires exception") {
		t.Fatalf("Validate() error = %v, want missing license exception", err)
	}
}

func TestValidateRejectsEvidenceHashMismatch(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	evidenceID := stableEvidenceID("tool:example", "artifact_available")
	inv.Evidence = []Evidence{{
		ID: evidenceID, Source: "fixture", Subject: "tool:example", Review: "artifact_available",
		CommandOrQuery: "fixture query", Tool: EvidenceTool{Name: "fixture", Version: "v1"},
		ObservedAt: "2026-08-21T00:00:00Z", Result: "available", Hash: "sha256:" + strings.Repeat("0", 64),
	}}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "evidence result hash mismatch") {
		t.Fatalf("Validate() error = %v, want evidence hash mismatch", err)
	}
}

func TestValidateRejectsSelfAttestedVerifiedExternalReview(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	result := "active"
	evidenceID := stableEvidenceID("tool:example", "repository_archived")
	evidence := Evidence{
		ID: evidenceID, Source: "component.txt", Subject: "tool:example", Review: "repository_archived",
		CommandOrQuery: "dependency inventory batch audit", Tool: EvidenceTool{Name: "dependencyinventory", Version: "schema-v2"},
		ObservedAt: "2026-08-21T00:00:00Z", Result: result, Hash: evidenceResultHash(result),
	}
	inv.Evidence = []Evidence{evidence}
	inv.Components[0].RepositoryState.RepositoryArchived = Review{
		Status: ReviewVerified, Value: result, EvidenceIDs: []string{evidence.ID},
	}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "self-attested evidence") {
		t.Fatalf("Validate() error = %v, want self-attested evidence rejection", err)
	}
}

func TestValidateRejectsWrongCommandSemanticsForVerifiedReview(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	result := "no reachable vulnerabilities"
	evidenceID := stableEvidenceID("tool:example", "security")
	evidence := Evidence{
		ID: evidenceID, Source: "go.mod", Subject: "tool:example", Review: "security",
		CommandOrQuery: "go list -m -json all", Tool: EvidenceTool{Name: "go", Version: "go1.27.0"},
		ObservedAt: "2026-08-21T00:00:00Z", Result: result, Hash: evidenceResultHash(result),
	}
	inv.Evidence = []Evidence{evidence}
	inv.Components[0].Assessment.Security = Review{
		Status: ReviewVerified, Value: result, EvidenceIDs: []string{evidence.ID},
	}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "cannot prove security") {
		t.Fatalf("Validate() error = %v, want dimension-specific evidence rejection", err)
	}
}

func TestValidateRejectsMissingRequiredReviewFields(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components[0].Assessment.License.Status = ""

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "license.status") {
		t.Fatalf("Validate() error = %v, want missing license.status", err)
	}
}

func TestValidateRejectsMissingPerRecordAssessment(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components[0].Assessment = Assessment{}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "channel_current.status") {
		t.Fatalf("Validate() error = %v, want missing per-record assessment", err)
	}
}

func TestValidateRejectsDeferredDecisionWithoutReviewDate(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components[0].Assessment.Decision.ReviewBy = ""

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "missing or invalid review_by") {
		t.Fatalf("Validate() error = %v, want missing review date", err)
	}
}

func TestValidateRejectsUnexpectedModuleGraphManifest(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.ModuleGraphs[1].Manifest = "go.mod"

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unexpected manifest or sum") {
		t.Fatalf("Validate() error = %v, want unexpected manifest or sum", err)
	}
}

func TestDecodeOneJSONFileRejectsDuplicateFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "duplicate.json", `{"schema_version":1,"schema_version":1}`)

	var target map[string]any
	err := decodeOneJSONFile(filepath.Join(root, "duplicate.json"), &target)
	if err == nil || !strings.Contains(err.Error(), `duplicate JSON field "schema_version"`) {
		t.Fatalf("decodeOneJSONFile() error = %v, want duplicate field", err)
	}
}

func TestValidateRejectsStaleSourceLocation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "component.txt", "actual-version\n")
	inv := validInventory()
	inv.Components[0].SourceLocations = []SourceLocation{{Path: "component.txt", Match: "missing-version"}}

	err := Validate(inv, root)
	if err == nil || !strings.Contains(err.Error(), "stale source location") {
		t.Fatalf("Validate() error = %v, want stale source location", err)
	}
}

func TestValidateRejectsTagOnlyReferenceClaimedImmutable(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components[0].Kind = KindOCIImage
	inv.Components[0].Installed = "example.invalid/router:latest"
	inv.Components[0].ImmutablePin = ImmutablePin{Status: PinVerified, Kind: "oci-digest", Value: "example.invalid/router:latest"}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "tag-only OCI reference") {
		t.Fatalf("Validate() error = %v, want tag-only OCI reference", err)
	}
}

func TestValidateRejectsTagOnlyActionClaimedImmutable(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.Components[0].Kind = KindGitHubAction
	inv.Components[0].Installed = "example/action@v1"
	inv.Components[0].ImmutablePin = ImmutablePin{Status: PinVerified, Kind: "git-commit", Value: "example/action@v1"}

	err := Validate(inv, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "tag-only GitHub Action reference") {
		t.Fatalf("Validate() error = %v, want tag-only GitHub Action reference", err)
	}
}

func TestValidateAllowsHonestDeferredMutableReference(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	root := t.TempDir()
	writeFixture(t, root, "component.txt", "v1.0.0\n")
	inv.Components[0].Kind = KindOCIImage
	inv.Components[0].Installed = "example.invalid/router:latest"
	inv.Components[0].ImmutablePin = ImmutablePin{Status: PinMutable, Kind: "tag", Value: "example.invalid/router:latest"}
	if err := Validate(inv, root); err != nil {
		t.Fatalf("Validate() error = %v, want deferred mutable reference accepted", err)
	}
}

func TestCompareModuleInventoryRejectsIncompleteSelectedGraph(t *testing.T) {
	t.Parallel()

	want := []moduleIdentity{{Path: "example.invalid/one", Version: "v1.0.0"}, {Path: "example.invalid/two", Version: "v2.0.0", Indirect: true}}
	got := []moduleIdentity{{Path: "example.invalid/one", Version: "v1.0.0"}}
	err := compareModuleInventory("runtime", want, got)
	if err == nil || !strings.Contains(err.Error(), "module graph mismatch") || !strings.Contains(err.Error(), "example.invalid/two@v2.0.0") {
		t.Fatalf("Check() error = %v, want missing module graph entry", err)
	}
}

func TestSelectedModuleGraphIncludesTransitiveBuildList(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", `module example.invalid/root

go 1.27

require example.invalid/one v1.0.0

replace example.invalid/one => ./one
replace example.invalid/two => ./two
`)
	writeFixture(t, root, "one/go.mod", `module example.invalid/one

go 1.27

require example.invalid/two v1.2.0
`)
	writeFixture(t, root, "two/go.mod", "module example.invalid/two\n\ngo 1.27\n")

	modules, err := selectedModuleGraph(context.Background(), root, ModuleGraph{ID: "runtime", Manifest: "go.mod", Sum: "go.sum"})
	if err != nil {
		t.Fatalf("selectedModuleGraph() error = %v", err)
	}
	want := []moduleIdentity{
		{Path: "example.invalid/one", Version: "v1.0.0"},
		{Path: "example.invalid/two", Version: "v1.2.0", Indirect: true},
	}
	if err := compareModuleInventory("runtime", want, modules); err != nil {
		t.Fatalf("selectedModuleGraph() omitted transitive build-list module: %v", err)
	}
}

func TestDiscoverDeclaredComponentsIncludesFixedShellToolVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "install.sh", "readonly CONTAINERLAB_VERSION=\"0.78.2\"\n")

	components, err := discoverDeclaredComponents(context.Background(), root)
	if err != nil {
		t.Fatalf("discoverDeclaredComponents() error = %v", err)
	}
	want := declaredComponent{
		ID: "tool:containerlab", Kind: KindTool, Installed: "0.78.2",
		Sources: []SourceLocation{{Path: "install.sh", Match: `readonly CONTAINERLAB_VERSION="0.78.2"`}},
	}
	if len(components) != 1 || components[0].ID != want.ID || components[0].Kind != want.Kind ||
		components[0].Installed != want.Installed || !slices.Equal(components[0].Sources, want.Sources) {
		t.Fatalf("discoverDeclaredComponents() = %#v, want %#v", components, want)
	}
}

func TestDiscoverDeclaredComponentsIgnoresProsePunctuationAndTemplates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "CHANGELOG.md", "Uses docker.io/example/router:latest.\n")
	writeFixture(t, root, "bootstrap.py", `template = "docker.io/example/router:{tag}"`)
	writeFixture(t, root, "compose.yml", "# image: docker.io/example/router:latest.\n")
	writeFixture(t, root, "run.sh", "# pull docker.io/example/router:latest.\n")
	writeFixture(t, root, "profiles.json", `{"image":"docker.io/example/router:1.2.3"}`)

	components, err := discoverDeclaredComponents(context.Background(), root)
	if err != nil {
		t.Fatalf("discoverDeclaredComponents() error = %v", err)
	}
	if len(components) != 1 || components[0].Installed != "docker.io/example/router:1.2.3" {
		t.Fatalf("discoverDeclaredComponents() = %#v, want only exact JSON image", components)
	}
}

func TestCheckRejectsUndeclaredWorkflowAction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.invalid/runtime\n\ngo 1.27\n\nrequire example.invalid/one v1.0.0\n")
	writeFixture(t, root, "main.go", "package runtime\n")
	writeFixture(t, root, "go.sum", "")
	writeFixture(t, root, "tools/go.mod", "module example.invalid/tools\n\ngo 1.27\n")
	writeFixture(t, root, "tools/go.sum", "")
	writeFixture(t, root, ".github/workflows/ci.yml", "steps:\n  - uses: example/action@0123456789abcdef0123456789abcdef01234567 # v1.0.0\n")
	writeFixture(t, root, "schema.json", minimalSchema)
	writeFixture(t, root, "inventory.json", minimalInventoryJSON)

	err := Check(context.Background(), Options{Root: root, InventoryPath: "inventory.json", SchemaPath: "schema.json"})
	if err == nil || !strings.Contains(err.Error(), "declared component missing from inventory") || !strings.Contains(err.Error(), "example/action") {
		t.Fatalf("Check() error = %v, want missing workflow action", err)
	}
}

func validInventory() Inventory {
	unverified := func() Review {
		return Review{Status: ReviewUnverified, Value: "not yet audited", EvidenceIDs: []string{}}
	}
	return Inventory{
		Schema:         SchemaID,
		SchemaVersion:  currentSchemaVersion,
		AuditedAt:      "2026-08-21T00:00:00Z",
		GoPackageCount: 1,
		ModuleGraphs: []ModuleGraph{
			{ID: "runtime", Manifest: "go.mod", Sum: "go.sum"},
			{ID: "tools", Manifest: "tools/go.mod", Sum: "tools/go.sum"},
		},
		Components: []Component{{
			ID:   "tool:example",
			Kind: KindTool,
			Record: Record{
				Installed:       "v1.0.0",
				DeliveryChannel: "https://example.invalid/releases",
				SourceLocations: []SourceLocation{{Path: "component.txt", Match: "v1.0.0"}},
				Coordinates:     Coordinates{PURL: "pkg:generic/example@v1.0.0"},
				Assessment: Assessment{
					ChannelCurrent: unverified(), UpstreamCurrent: unverified(),
					ReleaseImpact: unverified(), Security: unverified(),
					License:  unverified(),
					Decision: Decision{Status: DecisionDeferred, Reason: "evidence review remains open", Owner: "maintainers", Tracking: "gobfd-qj0.8.1.1", ReviewBy: "2026-09-30"},
				},
				RepositoryState: RepositoryState{
					RepositoryArchived: unverified(), ArtifactAvailable: unverified(),
					ReleaseLineEOL: unverified(),
				},
				ImmutablePin: ImmutablePin{Status: PinVerified, Kind: "version", Value: "v1.0.0"},
			},
		}},
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func writeFixture(t *testing.T, root, relative, content string) {
	t.Helper()

	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", relative, err)
	}
}

const minimalSchema = `{"$id":"` + SchemaID + `","type":"object"}`

const minimalInventoryJSON = `{
  "$schema": "` + SchemaID + `",
  "schema_version": 2,
  "audited_at": "2026-08-21T00:00:00Z",
  "go_package_count": 1,
  "evidence": [],
  "module_graphs": [
    {
      "id": "runtime",
      "manifest": "go.mod",
      "sum": "go.sum",
      "modules": [
        {
          "path": "example.invalid/one",
          "version": "v1.0.0",
          "indirect": false,
          "source_locations": [{"path":"go.mod","match":"example.invalid/one v1.0.0"}],
          "coordinates": {"purl":"pkg:golang/example.invalid/one@v1.0.0"},
          "installed": "v1.0.0",
          "delivery_channel": "https://proxy.golang.org",
          "assessment": {
            "channel_current":{"status":"unverified","value":"not yet audited","evidence_ids":[]},
            "upstream_current":{"status":"unverified","value":"not yet audited","evidence_ids":[]},
            "release_impact":{"status":"unverified","value":"not yet audited","evidence_ids":[]},
            "security":{"status":"unverified","value":"not yet audited","evidence_ids":[]},
            "license":{"status":"unverified","value":"not yet audited","evidence_ids":[]},
            "decision":{"status":"deferred","reason":"audit open","owner":"maintainers","tracking":"gobfd-qj0.8.1.1","review_by":"2026-09-30"}
          },
          "repository_state": {
            "repository_archived":{"status":"unverified","value":"not yet audited","evidence_ids":[]},
            "artifact_available":{"status":"unverified","value":"not yet audited","evidence_ids":[]},
            "release_line_eol":{"status":"unverified","value":"not yet audited","evidence_ids":[]}
          },
          "immutable_pin": {"status":"verified","kind":"go-module-version","value":"example.invalid/one@v1.0.0"}
        }
      ]
    },
    {"id":"tools","manifest":"tools/go.mod","sum":"tools/go.sum","modules":[]}
  ],
  "components": []
}`
