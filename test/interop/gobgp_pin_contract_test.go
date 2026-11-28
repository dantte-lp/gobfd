package interop_test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// gobgpSourcePattern selects the lines that pin and build the GoBGP source.
var gobgpSourcePattern = regexp.MustCompile(`(?m)^\s*(ADD --checksum=sha256:\S+|` +
	`https://codeload\.github\.com/osrg/gobgp/\S+|` +
	`RUN \["go", "build", .*"\./cmd/gobgp", "\./cmd/gobgpd"\])`)

// golangStagePattern captures the toolchain image of a Containerfile stage.
var golangStagePattern = regexp.MustCompile(`(?m)^FROM (docker\.io/library/golang:\S+) AS (\S+)$`)

func gobgpSourcePins(contents string) []string {
	pins := make([]string, 0, 3)
	for _, match := range gobgpSourcePattern.FindAllStringSubmatch(contents, -1) {
		pins = append(pins, strings.TrimSpace(match[1]))
	}
	return pins
}

func golangStageImage(contents, stage string) string {
	for _, match := range golangStagePattern.FindAllStringSubmatch(contents, -1) {
		if match[2] == stage {
			return match[1]
		}
	}
	return ""
}

// TestContainerlabGoBGPSharesPeerPin keeps the Containerlab GoBFD image on the
// GoBGP v4 source pin and toolchain of the shared interop peer recipe.
func TestContainerlabGoBGPSharesPeerPin(t *testing.T) {
	t.Parallel()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	peer := readContractFile(t, "GoBGP peer recipe", filepath.Join(root, "test/interop/gobgp/Containerfile"))
	clab := readContractFile(t, "Containerlab GoBFD image", filepath.Join(root, "test/interop-clab/Containerfile.gobfd"))
	if strings.Contains(clab, "jauderho/gobgp") {
		t.Fatal("Containerlab GoBFD image retains docker.io/jauderho/gobgp")
	}
	peerPins := gobgpSourcePins(peer)
	if len(peerPins) != 3 {
		t.Fatalf("GoBGP peer recipe pins = %q, want 3", peerPins)
	}
	if clabPins := gobgpSourcePins(clab); !slices.Equal(peerPins, clabPins) {
		t.Fatalf("Containerlab GoBGP pins differ from peer recipe\npeer: %q\nclab: %q", peerPins, clabPins)
	}
	want := golangStageImage(peer, "builder")
	if got := golangStageImage(clab, "gobgp"); want == "" || got != want {
		t.Fatalf("Containerlab gobgp stage toolchain = %q, want peer builder %q", got, want)
	}
}
