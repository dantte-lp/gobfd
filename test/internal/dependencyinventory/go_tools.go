package dependencyinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec" //nolint:depguard // Inventory invokes the fixed read-only Go manifest parser.
	"strings"

	"github.com/dantte-lp/gobfd/internal/toolprofile"
)

var errGoToolDeclaration = errors.New("invalid Go tool declaration")

const (
	runtimeGraphID    = "runtime"
	goplsToolID       = "tool:gopls"
	govulncheckToolID = "tool:govulncheck"
	osvScannerToolID  = "tool:osv_scanner"
	syftToolID        = "tool:syft"
	goreleaserGraphID = "goreleaser"
)

func moduleGraphDefinitions() []ModuleGraph {
	return []ModuleGraph{
		{ID: runtimeGraphID, Manifest: "go.mod", Sum: "go.sum"},
		{ID: "tools", Manifest: "tools/go.mod", Sum: "tools/go.sum"},
		{ID: "gopls", Manifest: "tools/gopls/go.mod", Sum: "tools/gopls/go.sum"},
		{ID: "syft", Manifest: toolprofile.SyftManifest, Sum: "tools/syft/go.sum"},
		{ID: goreleaserGraphID, Manifest: toolprofile.GoReleaserQualifiedMod, Sum: toolprofile.GoReleaserQualifiedSum},
	}
}

func isGoToolManifest(path string) bool {
	for _, graph := range moduleGraphDefinitions() {
		if graph.ID != runtimeGraphID && graph.Manifest == path {
			return true
		}
	}
	return false
}

func moduleGraphFiles(id string) (ModuleGraph, bool) {
	for _, graph := range moduleGraphDefinitions() {
		if graph.ID == id {
			return graph, true
		}
	}
	return ModuleGraph{}, false
}

// The owning Go parser prints JSON without modifying the selected manifest.
func discoverGoToolManifest(
	ctx context.Context, root string, components map[string]declaredComponent, path string,
) error {
	if !isGoToolManifest(path) {
		return fmt.Errorf("unsupported Go tool manifest %q: %w", path, errGoToolDeclaration)
	}
	// #nosec G204 -- Fixed read-only Go arguments; path must match the first-party manifest allowlist above.
	command := exec.CommandContext(ctx, "go", "mod", "edit", "-json", path)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("read Go tool declarations in %s: %w", path, err)
	}
	return discoverGoToolDeclarations(components, path, output)
}

func discoverGoToolDeclarations(components map[string]declaredComponent, path string, data []byte) error {
	//nolint:tagliatelle // These exported field names belong to go mod edit -json.
	var manifest struct {
		Tool []struct {
			Path string `json:"Path"`
		} `json:"Tool"`
		Require []struct {
			Path    string `json:"Path"`
			Version string `json:"Version"`
		} `json:"Require"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode Go tool declarations in %s: %w", path, err)
	}
	for _, tool := range manifest.Tool {
		var id string
		switch tool.Path {
		case toolprofile.SyftCommand:
			id = syftToolID
		case toolprofile.GoReleaserModule:
			id = "tool:goreleaser"
		case "golang.org/x/tools/gopls":
			id = goplsToolID
		case "golang.org/x/vuln/cmd/govulncheck":
			id = govulncheckToolID
		case "github.com/google/osv-scanner/v2/cmd/osv-scanner":
			id = osvScannerToolID
		default:
			continue
		}
		identity, _ := declaredToolModule(id, "")
		version := ""
		for _, requirement := range manifest.Require {
			if requirement.Path == identity.Path {
				version = requirement.Version
				break
			}
		}
		if version == "" {
			return fmt.Errorf("tool %s in %s has no pinned module requirement: %w", tool.Path, path, errGoToolDeclaration)
		}
		if previous, exists := components[id]; exists &&
			strings.TrimPrefix(previous.Installed, "v") != strings.TrimPrefix(version, "v") {
			return fmt.Errorf("conflicting Go tool %s declarations: %s and %s: %w",
				id, previous.Installed, version, errGoToolDeclaration)
		}
		addDeclared(components, declaredComponent{
			ID: id, Kind: KindTool, Installed: version,
			Sources: []SourceLocation{{Path: path, Match: tool.Path}, {Path: path, Match: identity.Path + " " + version}},
		})
	}
	return nil
}
