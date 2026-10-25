package clabbootstrap

import (
	"bytes"
	"debug/buildinfo"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func compilerSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyVendorPolicyFixture(t, root)
	for path, content := range map[string]string{
		"go.mod":                            "module github.com/dantte-lp/gobfd\n\ngo 1.27\n\ntoolchain go1.27.3\n",
		"go.sum":                            "",
		"test/interop-clab/clab_test.go":    "package clab_test\n",
		"test/internal/podmanapi/client.go": "package podmanapi\n",
		"test/internal/vendorprofiles/manifest.go": "package vendorprofiles\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPrepareTestRunRejectsInvalidCopiedVendorManifest(t *testing.T) {
	t.Parallel()
	if _, err := prepareTestRun(compilerSourceFixture(t)); err != nil {
		t.Fatalf("prepare valid source fixture: %v", err)
	}
	root := compilerSourceFixture(t)
	path := filepath.Join(root, "test", "e2e", "vendor", "profiles.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := []byte(`"execution_policy": "retained-unavailable"`)
	if !bytes.Contains(data, before) {
		t.Fatal("copied manifest does not contain the retained-unavailable policy fixture")
	}
	data = bytes.Replace(data, before, []byte(`"execution_policy": "approved"`), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestRun(root); err == nil {
		t.Fatal("prepareTestRun accepted an invalid copied vendor execution policy before compiler creation")
	}
}

func TestVerifyCompiledArtifactRejectsWrongArchitecture(t *testing.T) {
	t.Parallel()
	const wantPath = "github.com/dantte-lp/gobfd/test/internal/clabbootstrap.test"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, verifyErr := verifyCompiledArtifact(executable, wantPath, true); verifyErr != nil {
		t.Fatalf("current pinned native race test executable is not a valid control: %v", verifyErr)
	}
	wrongArch := "arm64"
	switch runtime.GOARCH {
	case "amd64":
	case "arm64":
		wrongArch = "amd64"
	default:
		t.Fatalf("architecture fixture requires amd64 or arm64, got %s", runtime.GOARCH)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	before := []byte("build\tGOARCH=" + runtime.GOARCH + "\n")
	after := []byte("build\tGOARCH=" + wrongArch + "\n")
	if len(before) != len(after) || !bytes.Contains(data, before) {
		t.Fatal("current executable lacks the same-length literal GOARCH BuildInfo fixture")
	}
	path := filepath.Join(t.TempDir(), "wrong-architecture.test")
	if writeErr := os.WriteFile(path, bytes.ReplaceAll(data, before, after), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatalf("read cloned executable BuildInfo: %v", err)
	}
	found := false
	for _, setting := range info.Settings {
		if setting.Key == "GOARCH" {
			found = setting.Value == wrongArch
		}
	}
	if !found {
		t.Fatal("cloned executable did not retain the literal wrong-architecture BuildInfo mutation")
	}
	if _, err := verifyCompiledArtifact(path, wantPath, true); !errors.Is(err, errLifecycleState) {
		t.Fatalf("wrong-architecture artifact error = %v, want errLifecycleState", err)
	}
}

func TestVerifyTestSourceSnapshotRejectsChangedFrozenInputs(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{
		"source-bytes", "policy", "extra-input", "receipt-digest", "writable-input", "missing-input",
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root := compilerSourceFixture(t)
			receipt, err := prepareTestRun(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyTestSourceSnapshot(root, receipt); err != nil {
				t.Fatalf("unchanged frozen source control: %v", err)
			}
			source := filepath.Join(testRunDirectory(root, receipt.RunID), "src")
			input := filepath.Join(source, "test", "interop-clab", "clab_test.go")
			switch kind {
			case "source-bytes":
				replaceFrozenInput(t, input, []byte("package clab_test\n// changed compiler input\n"))
			case "policy":
				path := filepath.Join(source, "test", "e2e", "vendor", "profiles.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				replaceFrozenInput(t, path, bytes.Replace(data,
					[]byte(`"execution_policy": "retained-unavailable"`),
					[]byte(`"execution_policy": "approved"`), 1))
			case "extra-input":
				if err := os.WriteFile(filepath.Join(source, "go.work"), []byte("go 1.27\n"), 0o400); err != nil {
					t.Fatal(err)
				}
			case "receipt-digest":
				receipt.SourceSHA = strings.Repeat("0", 64)
			case "writable-input":
				if err := os.Chmod(input, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing-input":
				if err := os.Remove(input); err != nil {
					t.Fatal(err)
				}
			}
			if err := verifyTestSourceSnapshot(root, receipt); !errors.Is(err, errLifecycleState) {
				t.Fatalf("changed frozen source %s error = %v, want errLifecycleState", kind, err)
			}
		})
	}
}

func TestVerifyCompiledArtifactRejectsCGOBinary(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(build.Default.GOROOT, "bin", "go")
	if _, err := verifyCompiledArtifact(binary, "cmd/go", false); err != nil {
		t.Fatalf("pinned Go binary is not a valid non-CGO control: %v", err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	before := []byte("build\tCGO_ENABLED=0\n")
	after := []byte("build\tCGO_ENABLED=1\n")
	if !bytes.Contains(data, before) {
		t.Fatal("pinned Go binary lacks the literal CGO_ENABLED=0 BuildInfo fixture")
	}
	path := filepath.Join(t.TempDir(), "cgo-go")
	if err := os.WriteFile(path, bytes.ReplaceAll(data, before, after), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCompiledArtifact(path, "cmd/go", false); !errors.Is(err, errLifecycleState) {
		t.Fatalf("CGO-enabled binary error = %v, want errLifecycleState", err)
	}
}

func TestVerifyTestSourceSnapshotDoesNotSubstituteLaterHostSources(t *testing.T) {
	t.Parallel()
	root := compilerSourceFixture(t)
	receipt, err := prepareTestRun(root)
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"test/interop-clab/clab_test.go": "package clab_test\n// later host edit\n",
		"test/e2e/vendor/profiles.json":  "{}",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyTestSourceSnapshot(root, receipt); err != nil {
		t.Fatalf("later host edits changed the independently frozen compiler snapshot: %v", err)
	}
}

func replaceFrozenInput(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
}
