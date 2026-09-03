package main

import (
	"os"
	"strings"
	"testing"
)

func TestGoToolsUseIsolatedModule(t *testing.T) {
	t.Parallel()

	rootModule := readContractFile(t, "../go.mod")
	if strings.Contains(rootModule, "\ntool (") {
		t.Fatal("root go.mod declares developer tools; tool dependencies must not affect the runtime MVS graph")
	}

	toolsModule := readContractFile(t, "../tools/go.mod")
	for _, required := range []string{
		"module github.com/dantte-lp/gobfd/tools",
		"go 1.27",
		"toolchain go1.27.0",
		"github.com/golangci/golangci-lint/v2/cmd/golangci-lint",
		"golang.org/x/perf/cmd/benchstat",
		"gotest.tools/gotestsum",
		"connectrpc.com/connect/cmd/protoc-gen-connect-go",
		"google.golang.org/protobuf/cmd/protoc-gen-go",
	} {
		if !strings.Contains(toolsModule, required) {
			t.Errorf("tools/go.mod is missing %q", required)
		}
	}

	for _, path := range []string{
		"../Makefile",
		"../.github/workflows/ci.yml",
		"../.github/workflows/release.yml",
	} {
		content := readContractFile(t, path)
		if strings.Contains(content, "go -C tools tool") {
			t.Errorf("%s changes the tool process working directory away from the repository root", path)
		}
		for _, tool := range []string{"golangci-lint", "benchstat", "gotestsum"} {
			if strings.Contains(content, "go tool "+tool) {
				t.Errorf("%s invokes %s through the runtime module", path, tool)
			}
		}
	}

	makefile := readContractFile(t, "../Makefile")
	if !strings.Contains(makefile, "go tool -modfile=tools/go.mod golangci-lint") {
		t.Error("Makefile does not run golangci-lint from the repository root through tools/go.mod")
	}

	workflow := readContractFile(t, "../.github/workflows/ci.yml")
	for _, forbidden := range []string{
		"go install google.golang.org/protobuf/cmd/protoc-gen-go",
		"go install connectrpc.com/connect/cmd/protoc-gen-connect-go",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("CI workflow installs generator outside the isolated tools module: %q", forbidden)
		}
	}
	if !strings.Contains(workflow, "go run ./test/cmd/cictl proto-verify") {
		t.Error("CI workflow does not delegate protobuf verification to cictl")
	}
	protoHelper := readContractFile(t, "../test/internal/cirunner/proto.go")
	requireContractStrings(t, "protobuf CI helper", protoHelper, []string{
		"\"build\", \"-modfile=tools/go.mod\", \"-o\", filepath.Join(binDir, \"protoc-gen-go\")",
		"\"google.golang.org/protobuf/cmd/protoc-gen-go\"",
		"\"build\", \"-modfile=tools/go.mod\", \"-o\", filepath.Join(binDir, \"protoc-gen-connect-go\")",
		"\"connectrpc.com/connect/cmd/protoc-gen-connect-go\"",
	})
}

func TestDependencyInventoryHasReproducibleMakeTargets(t *testing.T) {
	t.Parallel()

	makefile := readContractFile(t, "../Makefile")
	for _, required := range []string{
		"dependency-inventory dependency-inventory-check",
		"go run -tags dependencyinventory_generate ./test/cmd/dependencyinventory " +
			"--root . --output docs/supply-chain/dependency-inventory.json",
		"go test -race -count=1 ./test/internal/dependencyinventory",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile is missing dependency inventory contract %q", required)
		}
	}
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(content)
}
