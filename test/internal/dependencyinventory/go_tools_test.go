package dependencyinventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverGoToolManifestUsesModulePinWithoutWriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const declaration = `module example.invalid/gopls

go 1.27.0

tool golang.org/x/tools/gopls

require golang.org/x/tools/gopls v0.24.0
`
	writeFixture(t, root, "tools/gopls/go.mod", declaration)
	components, err := discoverDeclaredComponents(context.Background(), root)
	if err != nil {
		t.Fatalf("discover Go tool: %v", err)
	}
	if len(components) != 1 || components[0].ID != "tool:gopls" || components[0].Installed != "v0.24.0" ||
		len(components[0].Sources) != 2 {
		t.Fatalf("Go tool declarations = %#v", components)
	}
	data, err := os.ReadFile(filepath.Join(root, "tools/gopls/go.mod"))
	if err != nil {
		t.Fatalf("read unchanged manifest: %v", err)
	}
	if string(data) != declaration {
		t.Fatal("Go tool discovery rewrote its manifest")
	}
}

func TestDiscoverGoToolDeclarationsRejectsMissingOrConflictingPin(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		data  string
		prior string
	}{
		{name: "missing", data: `{"Tool":[{"Path":"golang.org/x/tools/gopls"}]}`},
		{name: "conflicting", prior: "v0.23.0", data: `{
  "Tool":[{"Path":"golang.org/x/tools/gopls"}],
  "Require":[{"Path":"golang.org/x/tools/gopls","Version":"v0.24.0"}]
}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			components := make(map[string]declaredComponent)
			if test.prior != "" {
				components["tool:gopls"] = declaredComponent{ID: "tool:gopls", Installed: test.prior}
			}
			if err := discoverGoToolDeclarations(components, "tools/gopls/go.mod", []byte(test.data)); err == nil {
				t.Fatal("incomplete or conflicting tool pin accepted")
			}
		})
	}
}

func TestDiscoverGoToolDeclarationsMergesScannerSources(t *testing.T) {
	t.Parallel()

	components := make(map[string]declaredComponent)
	addTool(components, "Containerfile", "GOVULNCHECK_VERSION", "v1.9.0", "ARG GOVULNCHECK_VERSION=v1.9.0")
	data := []byte(`{
  "Tool":[
    {"Path":"golang.org/x/vuln/cmd/govulncheck"},
    {"Path":"github.com/google/osv-scanner/v2/cmd/osv-scanner"}
  ],
  "Require":[
    {"Path":"golang.org/x/vuln","Version":"v1.9.0"},
    {"Path":"github.com/google/osv-scanner/v2","Version":"v2.7.0"}
  ]
}`)
	if err := discoverGoToolDeclarations(components, "tools/go.mod", data); err != nil {
		t.Fatalf("discover scanners: %v", err)
	}
	if len(components) != 2 || len(components["tool:govulncheck"].Sources) != 3 ||
		components["tool:osv_scanner"].Installed != "v2.7.0" {
		t.Fatalf("merged tool declarations = %#v", components)
	}
}

func TestValidateRequiresIsolatedGoplsGraph(t *testing.T) {
	t.Parallel()

	inv := validInventory()
	inv.ModuleGraphs = inv.ModuleGraphs[:2]
	if err := Validate(inv, ""); err == nil || !strings.Contains(err.Error(), `required module graph "gopls" is missing`) {
		t.Fatalf("Validate() = %v, want missing isolated gopls graph", err)
	}
}

func TestGoplsGraphLicenseUsesExactModuleIdentity(t *testing.T) {
	t.Parallel()

	record := Record{Installed: "v0.62.0"}
	const subject = "gopls:golang.org/x/net"
	identity, ok := recordModuleIdentity(subject, &record)
	if !ok || identity.Path != "golang.org/x/net" || identity.Version != record.Installed {
		t.Fatalf("gopls module identity = %#v, %t", identity, ok)
	}
	source, command, tool := evidenceContext(
		subject, reviewLicense, "tools/gopls/go.mod", "unused", EvidenceTool{}, &record,
	)
	if source != depsDevVersionURL(identity.Path, identity.Version) || command != "GET "+source ||
		tool.Name != depsDevToolName {
		t.Fatalf("gopls license source = %q, command = %q, tool = %#v", source, command, tool)
	}
}

func TestDiscoverSyftToolFromIsolatedManifest(t *testing.T) {
	t.Parallel()
	components := map[string]declaredComponent{}
	data := []byte(`{"Tool":[{"Path":"github.com/anchore/syft/cmd/syft"}],"Require":[{"Path":"github.com/an` +
		`chore/syft","Version":"v1.55.0"}]}`)
	if err := discoverGoToolDeclarations(components, "tools/syft/go.mod", data); err != nil {
		t.Fatal(err)
	}
	component, ok := components["tool:syft"]
	if !ok || component.Installed != "v1.55.0" || !isGoToolManifest("tools/syft/go.mod") {
		t.Fatalf("isolated Syft declaration = %#v, %t", component, ok)
	}
}

func TestValidateRequiresIsolatedSyftGraph(t *testing.T) {
	t.Parallel()
	inv := validInventory()
	inv.ModuleGraphs = inv.ModuleGraphs[:3]
	if err := Validate(inv, ""); err == nil || !strings.Contains(err.Error(), `required module graph "syft" is missing`) {
		t.Fatalf("Validate() = %v, want missing isolated Syft graph", err)
	}
}

func TestDiscoverPublisherToolFromQualifiedConsumerManifest(t *testing.T) {
	t.Parallel()
	components := map[string]declaredComponent{}
	data := []byte(`{"Tool":[{"Path":"github.com/goreleaser/goreleaser/v2"}],"Require":[
		{"Path":"github.com/goreleaser/goreleaser/v2","Version":"v2.19.0"}]}`)
	const path = "tools/goreleaser/qualified.mod"
	if err := discoverGoToolDeclarations(components, path, data); err != nil {
		t.Fatal(err)
	}
	component, ok := components["tool:goreleaser"]
	if !ok || component.Installed != "v2.19.0" || !isGoToolManifest(path) || !shouldScanDeclaredSource(path) {
		t.Fatalf("qualified publisher declaration = %#v, %t", component, ok)
	}
}
