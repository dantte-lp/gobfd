//go:build e2e_observability_testcontainers

package observability_test

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"os/exec" //nolint:depguard // Contract regression executes the current test binary with an isolated helper mode.
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	testcontainers "github.com/testcontainers/testcontainers-go"
)

type inspectOnlyContainer struct {
	testcontainers.Container

	inspection *container.InspectResponse
}

func (fake inspectOnlyContainer) Inspect(context.Context) (*container.InspectResponse, error) {
	return fake.inspection, nil
}

func TestObservabilityReportDirectoriesAreExclusive(t *testing.T) {
	t.Setenv("E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_DIR", "")
	t.Setenv("E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER", "")
	root := t.TempDir()

	const workers = 8
	directories := make(chan string, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			_, directory := observabilityReportDirectory(t, root)
			directories <- directory
		})
	}
	group.Wait()
	close(directories)

	seen := make(map[string]struct{}, workers)
	for directory := range directories {
		if _, exists := seen[directory]; exists {
			t.Fatalf("observability report directory %s was reused concurrently", directory)
		}
		seen[directory] = struct{}{}
	}
}

func TestObservabilityReportDirectoryRejectsUnownedExistingDirectory(t *testing.T) {
	switch helperMode := os.Getenv("GOBFD_OBSERVABILITY_REPORT_HELPER"); helperMode {
	case "validate-owner":
		t.Setenv(
			"E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_DIR",
			os.Getenv("GOBFD_OBSERVABILITY_REPORT_FIXTURE"),
		)
		t.Setenv("E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER", "")
		observabilityReportDirectory(t, t.TempDir())
		return
	case "unrelated-failure":
		t.Fatal("unrelated helper failure")
	case "":
	default:
		t.Fatalf("unsupported observability report helper mode %q", helperMode)
	}

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "foreign.txt"), []byte("preserve\n"), 0o600); err != nil {
		t.Fatalf("write foreign report fixture: %v", err)
	}
	runHelper := func(mode string) ([]byte, error) {
		t.Helper()
		command := exec.CommandContext(t.Context(), os.Args[0],
			"-test.run=^TestObservabilityReportDirectoryRejectsUnownedExistingDirectory$")
		command.Env = append(os.Environ(),
			"GOBFD_OBSERVABILITY_REPORT_HELPER="+mode,
			"GOBFD_OBSERVABILITY_REPORT_FIXTURE="+directory,
		)
		return command.CombinedOutput()
	}
	validationOutput, validationErr := runHelper("validate-owner")
	wantDiagnostics := []string{
		"validate owned observability artifact directory",
		`artifact owner "" does not match directory`,
	}
	if !commandExitedAsExpected(validationErr, validationOutput, wantDiagnostics...) {
		t.Fatalf(
			"owner validation result = %v, output = %q; want exit 1 and diagnostics %q",
			validationErr, validationOutput, wantDiagnostics,
		)
	}
	unrelatedOutput, unrelatedErr := runHelper("unrelated-failure")
	if commandExitedAsExpected(unrelatedErr, unrelatedOutput, wantDiagnostics...) {
		t.Fatalf("unrelated child failure was accepted: %q", unrelatedOutput)
	}
}

func TestPrometheusAnonymousVolumeContract(t *testing.T) {
	validMount := container.MountPoint{
		Type: mount.TypeVolume, Name: "prometheus-anonymous-volume",
		Destination: "/prometheus", RW: true,
	}
	for _, test := range []struct {
		name        string
		role        string
		mounts      []container.MountPoint
		volumeNames []string
		wantErr     bool
	}{
		{
			name: "prometheus anonymous data volume", role: "prometheus",
			mounts: []container.MountPoint{validMount}, volumeNames: []string{validMount.Name},
		},
		{
			name: "missing ownership ledger", role: "prometheus",
			mounts: []container.MountPoint{validMount}, wantErr: true,
		},
		{
			name: "mismatched ownership ledger", role: "prometheus",
			mounts: []container.MountPoint{validMount}, volumeNames: []string{"foreign-volume"}, wantErr: true,
		},
		{name: "non-prometheus volume", role: "grafana", mounts: []container.MountPoint{validMount}, wantErr: true},
		{name: "bind", role: "prometheus", mounts: []container.MountPoint{{
			Type: mount.TypeBind, Source: "/host", Destination: "/prometheus", RW: true,
		}}, wantErr: true},
		{name: "read-only", role: "prometheus", mounts: []container.MountPoint{{
			Type: mount.TypeVolume, Name: "readonly", Destination: "/prometheus", RW: false,
		}}, wantErr: true},
		{name: "wrong destination", role: "prometheus", mounts: []container.MountPoint{{
			Type: mount.TypeVolume, Name: "wrong-destination", Destination: "/other", RW: true,
		}}, wantErr: true},
		{name: "empty name", role: "prometheus", mounts: []container.MountPoint{{
			Type: mount.TypeVolume, Destination: "/prometheus", RW: true,
		}}, wantErr: true},
		{name: "multiple", role: "prometheus", mounts: []container.MountPoint{validMount, validMount}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			topology := &observabilityTopology{
				contract:    observabilityContract{prometheusIP: "172.25.0.30"},
				networkName: "observability-test",
				volumeNames: append([]string(nil), test.volumeNames...),
				containers: []namedContainer{{
					name: test.role,
					container: inspectOnlyContainer{inspection: &container.InspectResponse{
						Mounts: test.mounts,
						NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
							"observability-test": {IPAddress: netip.MustParseAddr("172.25.0.30")},
						}},
					}},
				}},
			}
			err := topology.assertRuntimeContract(t.Context())
			if test.wantErr && err == nil {
				t.Fatal("unsupported observability mount was accepted")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("official Prometheus anonymous volume was rejected: %v", err)
			}
		})
	}

	topologySource := readContractFile(t, filepath.Join(repositoryRoot(t), "test/e2e/observability/topology_test.go"))
	for _, required := range []string{
		"PreStarts: []testcontainers.ContainerHook",
		"recordOwnedVolume",
		"containertest.AssertVolumeRemoved",
		"VolumeNames",
	} {
		if !strings.Contains(topologySource, required) {
			t.Errorf("observability topology lacks Prometheus volume ownership contract %q", required)
		}
	}
}

func commandExitedAsExpected(err error, output []byte, diagnostics ...string) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	for _, diagnostic := range diagnostics {
		if !strings.Contains(string(output), diagnostic) {
			return false
		}
	}
	return true
}

func TestWriteEvidenceLeavesFinalResultPending(t *testing.T) {
	reportDir := t.TempDir()
	topology := &observabilityTopology{
		contract:  newObservabilityContract(repositoryRoot(t)),
		runID:     "pending-summary",
		reportDir: reportDir,
	}
	if err := initializeDiagnostics(reportDir); err != nil {
		t.Fatalf("initialize diagnostics: %v", err)
	}
	if err := topology.writeEvidence(context.Background()); err != nil {
		t.Fatalf("write pre-cleanup evidence: %v", err)
	}
	summary, err := os.ReadFile(filepath.Join(reportDir, "summary.md"))
	if err != nil {
		t.Fatalf("read pending summary: %v", err)
	}
	if !strings.Contains(string(summary), "| Exit code | pending") {
		t.Fatalf("pre-cleanup summary lacks explicit pending result:\n%s", summary)
	}
	if strings.Contains(string(summary), "| Exit code | 0 |") {
		t.Fatalf("pre-cleanup summary claims success:\n%s", summary)
	}
}

func TestResourceSnapshotUsesMode0600UnderRestrictiveUmask(t *testing.T) {
	reportDir := t.TempDir()
	topology := &observabilityTopology{
		contract:  newObservabilityContract(repositoryRoot(t)),
		reportDir: reportDir,
	}
	previousUmask := syscall.Umask(0o077)
	err := topology.writeResourceSnapshot()
	syscall.Umask(previousUmask)
	if err != nil {
		t.Fatalf("write resource snapshot: %v", err)
	}
	info, err := os.Lstat(filepath.Join(reportDir, "resources.json"))
	if err != nil {
		t.Fatalf("lstat resource snapshot: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("resource snapshot mode/type = %v, want regular 0600", info.Mode())
	}
}

func TestResourceSnapshotPreRenameFailurePreservesLedger(t *testing.T) {
	reportDir := t.TempDir()
	ledger := filepath.Join(reportDir, "resources.json")
	original := []byte("{\"container_ids\":[\"preserve\"]}\n")
	if err := os.WriteFile(ledger, original, 0o600); err != nil {
		t.Fatalf("write original resource ledger: %v", err)
	}
	topology := &observabilityTopology{
		contract:                     newObservabilityContract(repositoryRoot(t)),
		reportDir:                    reportDir,
		containerIDs:                 []string{"replacement"},
		resourceSnapshotBeforeRename: func() error { return errors.New("injected pre-rename failure") },
	}
	snapshotErr := topology.writeResourceSnapshot()
	if snapshotErr == nil || !strings.Contains(snapshotErr.Error(), "injected pre-rename failure") {
		t.Fatalf("resource snapshot error = %v, want injected pre-rename failure", snapshotErr)
	}
	contents, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read preserved resource ledger: %v", err)
	}
	if string(contents) != string(original) {
		t.Fatalf("resource ledger changed on pre-rename failure: %q", contents)
	}
	matches, err := filepath.Glob(filepath.Join(reportDir, ".resources.json.tmp-*"))
	if err != nil {
		t.Fatalf("glob resource temporaries: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("resource snapshot left temporary files: %v", matches)
	}
}

func TestResourceSnapshotDoesNotFollowExistingSymlink(t *testing.T) {
	reportDir := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.json")
	original := []byte("preserve external ledger\n")
	if err := os.WriteFile(external, original, 0o600); err != nil {
		t.Fatalf("write external fixture: %v", err)
	}
	ledger := filepath.Join(reportDir, "resources.json")
	if err := os.Symlink(external, ledger); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	topology := &observabilityTopology{
		contract:  newObservabilityContract(repositoryRoot(t)),
		reportDir: reportDir,
	}
	if err := topology.writeResourceSnapshot(); err != nil {
		t.Fatalf("atomically replace symlink ledger: %v", err)
	}
	externalContents, err := os.ReadFile(external)
	if err != nil {
		t.Fatalf("read external fixture: %v", err)
	}
	if string(externalContents) != string(original) {
		t.Fatalf("resource snapshot followed symlink and changed external file: %q", externalContents)
	}
	info, err := os.Lstat(ledger)
	if err != nil {
		t.Fatalf("lstat published ledger: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("published resource ledger mode = %v, want regular file", info.Mode())
	}
}
