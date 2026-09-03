package e2erunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec" //nolint:depguard // The subprocess isolates the process-global umask regression.
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const (
	fakeGoEnabled  = "GOBFD_E2ERUNNER_FAKE_GO"
	fakeGoCapture  = "GOBFD_E2ERUNNER_FAKE_GO_CAPTURE"
	fakeGoExit     = "GOBFD_E2ERUNNER_FAKE_GO_EXIT"
	fakeGoSilent   = "GOBFD_E2ERUNNER_FAKE_GO_SILENT"
	fakeGoOutput   = "fake go test output\n"
	secureFilePath = "GOBFD_E2ERUNNER_SECURE_FILE_PATH"
)

type fakeGoInvocation struct {
	Args        []string          `json:"args"`
	Environment map[string]string `json:"environment"`
}

func TestMain(m *testing.M) {
	if path := os.Getenv(secureFilePath); path != "" {
		runSecureFileHelper(path)
		return
	}
	if os.Getenv(fakeGoEnabled) == "1" {
		runFakeGo()
		return
	}
	os.Exit(m.Run())
}

func TestSecureFileEnforcesModeUnderRestrictiveUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	command := exec.CommandContext(t.Context(), os.Args[0])
	command.Env = append(os.Environ(), secureFilePath+"="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run secure-file subprocess: %v: %s", err, output)
	}
	requireMode(t, path, 0o600)
}

func TestReportTargetsRunFixedGoTestsWithSecureArtifacts(t *testing.T) {
	fakeGo := installFakeGo(t)
	capturePath := filepath.Join(t.TempDir(), "invocation.json")
	t.Setenv("PATH", filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeGoEnabled, "1")
	t.Setenv(fakeGoCapture, capturePath)
	for _, name := range reportArtifactEnvironmentNames() {
		t.Setenv(name, "stale-parent-value")
	}

	tests := []struct {
		target          string
		reportPath      string
		artifactEnv     string
		ownerEnv        string
		wantArgs        []string
		wantOwnerMarker bool
	}{
		{
			target: "core", reportPath: "core", artifactEnv: "E2E_CORE_TESTCONTAINERS_ARTIFACT_DIR",
			wantArgs: []string{"test", "-tags", "e2e_core_testcontainers", "./test/e2e/core", "-race", "-count=1", "-json", "-timeout", "10m", "-run", "^TestCoreDaemonTestcontainers$"},
		},
		{
			target: "bgp-fast-failover", reportPath: "bgp-fast-failover",
			artifactEnv: "E2E_BGP_FAILOVER_TESTCONTAINERS_ARTIFACT_DIR",
			wantArgs:    []string{"test", "-tags", "e2e_bgp_failover_testcontainers", "./test/e2e/bgp-failover", "-race", "-count=1", "-json", "-timeout", "10m", "-run", "^TestBGPFastFailoverTestcontainers$"},
		},
		{
			target: "haproxy-health", reportPath: "haproxy-health",
			artifactEnv: "E2E_HAPROXY_TESTCONTAINERS_ARTIFACT_DIR",
			wantArgs:    []string{"test", "-trimpath", "-tags", "e2e_haproxy_testcontainers", "./test/e2e/haproxy-health", "-race", "-count=1", "-json", "-timeout", "10m"},
		},
		{
			target: "observability", reportPath: "observability",
			artifactEnv:     "E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_DIR",
			ownerEnv:        "E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER",
			wantArgs:        []string{"test", "-trimpath", "-tags", "e2e_observability_testcontainers", "./test/e2e/observability", "-race", "-count=1", "-json", "-timeout", "15m"},
			wantOwnerMarker: true,
		},
	}

	for _, test := range tests {
		t.Run(test.target, func(t *testing.T) {
			root := t.TempDir()
			var stdout bytes.Buffer
			if err := Run(context.Background(), root, []string{test.target}, &stdout, &bytes.Buffer{}); err != nil {
				t.Fatalf("Run(%s): %v", test.target, err)
			}
			if stdout.String() != fakeGoOutput {
				t.Fatalf("stdout = %q, want %q", stdout.String(), fakeGoOutput)
			}

			reportDir := singleReportDirectory(t, filepath.Join(root, "reports", "e2e", test.reportPath))
			requireMode(t, reportDir, 0o700)
			for _, name := range []string{goTestJSONName, goTestLogName} {
				path := filepath.Join(reportDir, name)
				requireMode(t, path, 0o600)
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				if string(content) != fakeGoOutput {
					t.Errorf("%s = %q, want stdout %q", name, content, fakeGoOutput)
				}
			}

			invocation := readFakeGoInvocation(t, capturePath)
			if !slices.Equal(invocation.Args, test.wantArgs) {
				t.Errorf("go argv = %q, want %q", invocation.Args, test.wantArgs)
			}
			wantEnvironment := map[string]string{
				"GOBFD_REQUIRE_PODMAN": "1",
				test.artifactEnv:       reportDir,
			}
			if test.ownerEnv != "" {
				wantEnvironment[test.ownerEnv] = filepath.Base(reportDir)
			}
			if !maps.Equal(invocation.Environment, wantEnvironment) {
				t.Errorf("artifact environment = %v, want %v", invocation.Environment, wantEnvironment)
			}
			if test.wantOwnerMarker {
				marker := filepath.Join(reportDir, ".gobfd-observability-owner")
				requireMode(t, marker, 0o600)
				content, err := os.ReadFile(marker)
				if err != nil {
					t.Fatalf("read observability owner marker: %v", err)
				}
				if string(content) != filepath.Base(reportDir)+"\n" {
					t.Errorf("observability owner marker = %q", content)
				}
			}
		})
	}
}

func TestReportTargetPreservesGoTestExitCode(t *testing.T) {
	fakeGo := installFakeGo(t)
	t.Setenv("PATH", filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeGoEnabled, "1")
	t.Setenv(fakeGoCapture, filepath.Join(t.TempDir(), "invocation.json"))
	t.Setenv(fakeGoExit, "23")
	t.Setenv(fakeGoSilent, "1")

	err := Run(context.Background(), t.TempDir(), []string{"core"}, &bytes.Buffer{}, &bytes.Buffer{})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run(core) error = %v, want ExitError", err)
	}
	if exitErr.Code != 23 {
		t.Errorf("ExitError.Code = %d, want 23", exitErr.Code)
	}
}

func TestReportTargetRejectsSilentSuccess(t *testing.T) {
	fakeGo := installFakeGo(t)
	t.Setenv("PATH", filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeGoEnabled, "1")
	t.Setenv(fakeGoCapture, filepath.Join(t.TempDir(), "invocation.json"))
	t.Setenv(fakeGoSilent, "1")

	err := Run(context.Background(), t.TempDir(), []string{"core"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run(core) accepted successful go test with empty report artifacts")
	}
	for _, name := range []string{goTestJSONName, goTestLogName} {
		if !strings.Contains(err.Error(), name+" is empty") {
			t.Errorf("Run(core) error = %q, want empty %s context", err, name)
		}
	}
}

func installFakeGo(t *testing.T) string {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	path := filepath.Join(t.TempDir(), "go")
	if err := os.Symlink(executable, path); err != nil {
		t.Fatalf("install fake go: %v", err)
	}
	return path
}

func runFakeGo() {
	invocation := fakeGoInvocation{Args: os.Args[1:], Environment: map[string]string{}}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok && (name == "GOBFD_REQUIRE_PODMAN" || strings.Contains(name, "_TESTCONTAINERS_ARTIFACT_")) {
			invocation.Environment[name] = value
		}
	}
	data, err := json.Marshal(invocation)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	if writeErr := os.WriteFile(os.Getenv(fakeGoCapture), data, 0o600); writeErr != nil {
		fmt.Fprintln(os.Stderr, writeErr)
		os.Exit(125)
	}
	if os.Getenv(fakeGoSilent) != "1" {
		fmt.Print(fakeGoOutput)
	}
	code, err := strconv.Atoi(os.Getenv(fakeGoExit))
	if err == nil {
		os.Exit(code)
	}
	if os.Getenv(fakeGoExit) != "" {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	os.Exit(0)
}

func runSecureFileHelper(path string) {
	syscall.Umask(0o777)
	file, err := secureFile(path)
	if err == nil {
		err = file.Close()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	os.Exit(0)
}

func reportArtifactEnvironmentNames() []string {
	return []string{
		"GOBFD_REQUIRE_PODMAN",
		"E2E_CORE_TESTCONTAINERS_ARTIFACT_DIR",
		"E2E_BGP_FAILOVER_TESTCONTAINERS_ARTIFACT_DIR",
		"E2E_HAPROXY_TESTCONTAINERS_ARTIFACT_DIR",
		"E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_DIR",
		"E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER",
	}
}

func singleReportDirectory(t *testing.T, parent string) string {
	t.Helper()

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read report parent %s: %v", parent, err)
	}
	if len(entries) != 1 || !entries[0].IsDir() || !strings.HasPrefix(entries[0].Name(), "run.") {
		t.Fatalf("report parent entries = %v, want one run.* directory", entries)
	}
	return filepath.Join(parent, entries[0].Name())
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
}

func readFakeGoInvocation(t *testing.T, path string) fakeGoInvocation {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake go invocation: %v", err)
	}
	var invocation fakeGoInvocation
	if err := json.Unmarshal(data, &invocation); err != nil {
		t.Fatalf("decode fake go invocation: %v", err)
	}
	return invocation
}
