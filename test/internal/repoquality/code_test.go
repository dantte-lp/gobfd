package repoquality

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCodeInventoryIncludesVariantsClosuresAndRenamedDuplicates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := []string{"a.go", "other_linux.go", "other_test.go", "internal/testutil/support.go"}
	fixture := `package fixture
func Sum(a int) int { n := a + 1; return n }
func Other(x int) int { y := x + 1; return y }
func Closure() func() int { return func() int { return 42 } }
`
	for _, name := range files {
		data := fixture
		if name == "other_linux.go" {
			data = "//go:build linux\n\npackage fixture\nfunc Linux() {}\n"
		}
		if name == "internal/testutil/support.go" {
			data = "package testutil\nfunc Support() {}\n"
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := InspectCode(t.Context(), root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 4 || len(report.Functions) != 10 || len(report.Clones) == 0 {
		t.Fatalf("incomplete inventory: files=%d functions=%d clones=%d",
			len(report.Files), len(report.Functions), len(report.Clones))
	}
	for _, fn := range report.Functions {
		if fn.Path == "a.go" && fn.Category != "production" {
			t.Fatalf("runtime source misclassified: %+v", fn)
		}
		if fn.Path == "internal/testutil/support.go" && fn.Category != "tooling" {
			t.Fatalf("test support counted as runtime source: %+v", fn)
		}
	}
	first, err := json.Marshal(report)
	var sumID, otherID string
	for _, fn := range report.Functions {
		if fn.Path == "a.go" && fn.Name == "Sum" {
			sumID = fn.ID
		}
		if fn.Path == "a.go" && fn.Name == "Other" {
			otherID = fn.ID
		}
	}
	matchedRename := false
	for _, clone := range report.Clones {
		if clone.Kind == "local-renaming" && slices.Contains(clone.Functions, sumID) &&
			slices.Contains(clone.Functions, otherID) {
			matchedRename = true
		}
	}
	if !matchedRename {
		t.Fatal("renamed local bindings were not identified independently of exact clones")
	}
	if err != nil {
		t.Fatal(err)
	}
	again, err := InspectCode(t.Context(), root, []string{files[3], files[2], files[0], files[1]})
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(again)
	if err != nil || string(first) != string(second) {
		t.Fatalf("inventory order is not deterministic: %v", err)
	}
}

func TestCodeInventoryRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	for _, files := range [][]string{nil, {"../a.go"}, {"a.go", "a.go"}, {"missing.go"}} {
		if _, err := InspectCode(t.Context(), t.TempDir(), files); err == nil {
			t.Fatalf("invalid inventory accepted: %q", files)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\nfunc {"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectCode(t.Context(), root, []string{"broken.go"}); err == nil {
		t.Fatal("invalid Go source accepted")
	}
}

func TestCodebaseQuality(t *testing.T) {
	root := filepath.Clean("../../..")
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.HasPrefix(string(data), "module github.com/dantte-lp/gobfd\n") {
		t.Fatalf("wrong repository quality test root: %v", err)
	}
	files, err := DiscoverGoSources(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	report, err := InspectCode(t.Context(), root, files)
	if err != nil || len(report.Functions) == 0 || len(report.Files) != len(files) {
		t.Fatalf("quality analysis received incomplete input: %v", err)
	}
	identities := make(map[string]bool)
	for _, fn := range report.Functions {
		if identities[fn.ID] || fn.Category == "" || fn.Idempotency == "" || fn.Runtime == "" {
			t.Fatalf("missing classification or duplicate function identity: %+v", fn)
		}
		identities[fn.ID] = true
	}
	t.Logf("quality vector: files=%d functions=%d imports=%d clones=%d high_complexity_non_test=%d production=%d",
		len(report.Files), len(report.Functions), len(report.Imports), len(report.Clones),
		report.HighComplexity, report.ProductionHighComplexity)
}

type failingCodeWriter struct{ failure error }

func TestCodeMetricsRespectClosureAndSelectorBoundaries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const source = `package fixture
func Outer() {
 for range 2 {
  if true {
   go func() { for range 3 { if true {} } }()
  }
 }
}
func First(a Thing) int { n := a.Left; return n }
func Second(b Thing) int { m := b.Right; return m }
`
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := InspectCode(t.Context(), root, []string{"a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Functions[0].Complexity != 3 || report.Functions[0].LoopDepth != 1 ||
		report.Functions[0].Goroutines != 1 || report.Functions[1].Complexity != 3 ||
		report.Functions[2].RenamedBody == report.Functions[3].RenamedBody {
		t.Fatalf("nested body metrics or selector semantics were collapsed: %+v", report.Functions)
	}
}

func TestCodeCoordinatesUsePhysicalLines(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const source = "package fixture\n//line virtual.go:900\nfunc Run() {}\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := InspectCode(t.Context(), root, []string{"a.go"})
	if err != nil || report.Functions[0].Line != 3 {
		t.Fatalf("physical source coordinate was replaced by a line directive: %+v: %v", report.Functions, err)
	}
}

func TestCodeDiscoveryRetainsVendorProtocolTests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{"vendor/dependency.go", "test/e2e/vendor/protocol_test.go"} {
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("package fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := DiscoverGoSources(t.Context(), root)
	if err != nil || !slices.Equal(files, []string{"test/e2e/vendor/protocol_test.go"}) {
		t.Fatalf("domain-specific vendor tests were excluded: %q: %v", files, err)
	}
}

func (writer failingCodeWriter) Write([]byte) (int, error) { return 0, writer.failure }

func TestCodeReportRejectsOutputFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("output failed")
	if err := WriteCodeReport(failingCodeWriter{failure: failure}, CodeReport{}); !errors.Is(err, failure) {
		t.Fatalf("output error lost: %v", err)
	}
}

func TestCodeGraphPreservesPackageBoundaries(t *testing.T) {
	t.Parallel()
	report := CodeReport{
		Files: []CodeFile{
			{Path: "internal/a/a.go", Package: "internal/a:a"},
			{Path: "internal/b/b.go", Package: "internal/b:b"},
		},
		Imports: []CodeImport{{From: "internal/a:a", To: "github.com/dantte-lp/gobfd/internal/b"}},
	}
	graph := CodeDependencyGraph(report)
	if !strings.Contains(graph, "n0 --> n1") || !strings.Contains(graph, "internal/a:a") {
		t.Fatalf("missing first-party dependency: %s", graph)
	}
}
