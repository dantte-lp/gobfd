//go:build e2e_observability_testcontainers

package observability_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec" //nolint:depguard // Contract regression executes the current test binary with an isolated helper mode.
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

func TestObservabilityBuildRequestLimits(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	buildContext := t.TempDir()
	if err := os.WriteFile(filepath.Join(buildContext, "Containerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	topology := &observabilityTopology{
		revision: strings.Repeat("a", 40), buildDate: "2026-10-28T00:00:00Z", runID: "fixture-run",
	}
	request := topology.imageBuildRequest(buildContext, "localhost/gobfd-observability-fixture", "owned")
	options, err := request.BuildOptions()
	if err != nil {
		t.Fatalf("construct actual image-build request: %v", err)
	}
	if closer, ok := options.Context.(io.Closer); ok {
		t.Cleanup(func() {
			if closeErr := closer.Close(); closeErr != nil {
				t.Errorf("close image-build context: %v", closeErr)
			}
		})
	}
	if _, err := io.Copy(io.Discard, options.Context); err != nil {
		t.Fatalf("consume image-build context: %v", err)
	}
	for name, want := range map[string]string{
		"VCS_REF": strings.Repeat("a", 40), "BUILD_DATE": "2026-10-28T00:00:00Z",
	} {
		if got := options.BuildArgs[name]; got == nil || *got != want {
			t.Errorf("image build argument %s = %v, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"io.gobfd.test": "observability", "io.gobfd.run": "fixture-run",
		"io.gobfd.image": "localhost/gobfd-observability-fixture:owned",
	} {
		if got := options.Labels[name]; got != want {
			t.Errorf("image build ownership %s = %q, want %q", name, got, want)
		}
	}
	for _, limit := range []struct {
		name string
		got  int64
		want int64
	}{
		{"cpu-period", options.CPUPeriod, 100000},
		{"cpu-quota", options.CPUQuota, 200000},
		{"memory", options.Memory, 2147483648},
		{"memory-with-swap", options.MemorySwap, 2147483648},
	} {
		t.Run(limit.name, func(t *testing.T) {
			if limit.got != limit.want {
				t.Errorf("build %s = %d, want %d", limit.name, limit.got, limit.want)
			}
		})
	}
}

func observabilityImageFixture() (string, map[string]string) {
	return "sha256:" + strings.Repeat("1", 64), map[string]string{
		"org.opencontainers.image.source":   "https://github.com/dantte-lp/gobfd",
		"org.opencontainers.image.revision": strings.Repeat("a", 40),
		"org.opencontainers.image.created":  "2026-10-28T00:00:00Z",
		"org.opencontainers.image.version":  "fixture", "org.opencontainers.image.licenses": "Apache-2.0",
		"org.opencontainers.image.title": "fixture", "org.opencontainers.image.description": "fixture",
		"org.opencontainers.image.base.name":   "docker.io/library/debian:trixie-slim",
		"org.opencontainers.image.base.digest": "sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f",
		"io.gobfd.test":                        "observability",
		"io.gobfd.run":                         "fixture-run",
		"io.gobfd.image":                       "owned-child",
		"private-value":                        "PRIVATE_FIXTURE",
	}
}

func observabilityAPIClient(t *testing.T, handler http.Handler) *podmanapi.Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "p.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client, err := podmanapi.NewClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestObservabilityBuiltImageProvenance(t *testing.T) {
	id, valid := observabilityImageFixture()
	for _, test := range []struct {
		name, key, value string
	}{
		{"valid", "", ""},
		{"missing annotation", "org.opencontainers.image.version", ""},
		{"wrong source", "org.opencontainers.image.source", "other"},
		{"wrong revision", "org.opencontainers.image.revision", strings.Repeat("b", 40)},
		{"invalid timestamp", "org.opencontainers.image.created", "bad"},
		{"stale timestamp", "org.opencontainers.image.created", "2026-10-27T00:00:00Z"},
		{"wrong base", "org.opencontainers.image.base.name", "other"},
		{"foreign run", "io.gobfd.run", "foreign"},
	} {
		t.Run(test.name, func(t *testing.T) {
			labels := maps.Clone(valid)
			if test.key != "" {
				labels[test.key] = test.value
			}
			client := observabilityAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v5.0.0/images/owned-child/json" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Config": map[string]any{
					"Labels": labels, "Env": []string{"PRIVATE=PRIVATE_FIXTURE"},
				}})
			}))
			topology := &observabilityTopology{
				client: client, reportDir: t.TempDir(), runID: "fixture-run",
				revision: strings.Repeat("a", 40), buildDate: "2026-10-28T00:00:00Z",
			}
			_, err := topology.recordBuiltImage(t.Context(), "owned-child")
			if test.key != "" {
				if err == nil {
					t.Fatal("invalid built image provenance accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(filepath.Join(topology.reportDir, "resources.json"))
			if err != nil {
				t.Fatal(err)
			}
			var retained struct {
				Images map[string]map[string]string `json:"image_provenance"`
			}
			if err := json.Unmarshal(contents, &retained); err != nil {
				t.Fatal(err)
			}
			if len(retained.Images[id]) != 9 || strings.Contains(string(contents), "PRIVATE_FIXTURE") {
				t.Fatal("image provenance missing or retains unapproved metadata")
			}
		})
	}
}

func TestObservabilityImageCleanupIdentity(t *testing.T) {
	id, valid := observabilityImageFixture()
	for _, test := range []struct {
		name       string
		bound      bool
		foreign    bool
		wantError  bool
		wantDelete bool
	}{
		{"known identity despite substituted tag", true, true, false, true},
		{"recover own failed build", false, false, false, true},
		{"preserve foreign failed-build tag", false, true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			built, removed := false, false
			var deleted []string
			client := observabilityAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodDelete {
					deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/v5.0.0/images/"))
					removed = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if !built || removed {
					http.NotFound(w, r)
					return
				}
				labels := maps.Clone(valid)
				responseID := id
				if test.foreign && strings.HasSuffix(r.URL.Path, "/owned-child/json") {
					labels["io.gobfd.run"] = "foreign"
					responseID = "sha256:" + strings.Repeat("2", 64)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Id": responseID, "Config": map[string]any{"Labels": labels},
				})
			}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var callbacks []func()
			var reported []error
			ownedID := ""
			if err := containertest.RegisterImageCleanup(ctx, "owned-child", "fixture-run", "observability", &ownedID, client,
				func(fn func()) { callbacks = append(callbacks, fn) },
				func(err error) { reported = append(reported, err) }); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			built = true
			mu.Unlock()
			if test.bound {
				ownedID = id
			}
			cancel()
			callbacks[0]()
			if test.wantDelete {
				if ownedID != id {
					t.Errorf("recovered image identity = %q, want %s", ownedID, id)
				}
				callbacks[0]()
			}
			mu.Lock()
			defer mu.Unlock()
			if (len(reported) != 0) != test.wantError {
				t.Errorf("cleanup errors = %v, want error %t", reported, test.wantError)
			}
			if test.wantDelete {
				if len(deleted) != 1 || deleted[0] != id {
					t.Errorf("cleanup references = %v, want exact owned %s", deleted, id)
				}
			} else if len(deleted) != 0 {
				t.Errorf("foreign image cleanup attempted: %v", deleted)
			}
		})
	}
}

type inspectOnlyContainer struct {
	testcontainers.Container

	inspection *container.InspectResponse
}

func TestObservabilityContainerRequestLimits(t *testing.T) {
	for _, test := range []struct {
		role   string
		memory int64
	}{
		{"gobfd", 268435456},
		{"frr", 268435456},
		{"prometheus", 268435456},
		{"grafana", 536870912},
		{"tshark-capture", 268435456},
		{"tshark-analyzer", 268435456},
	} {
		t.Run(test.role, func(t *testing.T) {
			modified := false
			request := observabilityContainerRequest(test.role, testcontainers.ContainerRequest{
				HostConfigModifier: func(config *container.HostConfig) {
					modified = true
					config.CapAdd = append(config.CapAdd, "NET_RAW")
					config.NetworkMode = "container:fixture-owner"
					config.Memory = 64 << 20
				},
			})
			config := new(container.HostConfig)
			request.HostConfigModifier(config)
			if !modified || config.NetworkMode != "container:fixture-owner" ||
				len(config.CapAdd) != 1 || config.CapAdd[0] != "NET_RAW" {
				t.Fatalf("runtime limits discarded the role modifier: %+v", config)
			}
			if config.CPUPeriod != 100000 || config.CPUQuota != 100000 ||
				config.Memory != test.memory || config.MemorySwap != test.memory ||
				config.PidsLimit == nil || *config.PidsLimit != 128 {
				t.Errorf("runtime resources = %+v, want 1 CPU/%d bytes/no extra swap/128 PIDs", config.Resources, test.memory)
			}
		})
	}
	t.Run("without role modifier", func(t *testing.T) {
		request := observabilityContainerRequest("gobfd", testcontainers.ContainerRequest{})
		if request.HostConfigModifier == nil {
			t.Fatal("runtime request has no resource modifier")
		}
		config := new(container.HostConfig)
		request.HostConfigModifier(config)
		if config.Memory != 268435456 || config.MemorySwap != 268435456 ||
			config.CPUPeriod != 100000 || config.CPUQuota != 100000 ||
			config.PidsLimit == nil || *config.PidsLimit != 128 {
			t.Errorf("unmodified-role resources = %+v", config.Resources)
		}
	})
}

func (fake inspectOnlyContainer) Inspect(context.Context) (*container.InspectResponse, error) {
	return fake.inspection, nil
}

func TestObservabilityRejectsUnboundedRuntime(t *testing.T) {
	for _, test := range []struct {
		name      string
		alter     func(*container.HostConfig)
		missing   bool
		wantError bool
	}{
		{name: "bounded"},
		{name: "missing config", missing: true, wantError: true},
		{name: "unlimited CPU", alter: func(c *container.HostConfig) { c.CPUQuota = 0 }, wantError: true},
		{name: "wrong CPU", alter: func(c *container.HostConfig) { c.CPUQuota = 200000 }, wantError: true},
		{name: "wrong period", alter: func(c *container.HostConfig) { c.CPUPeriod = 0 }, wantError: true},
		{name: "unlimited memory", alter: func(c *container.HostConfig) {
			c.Memory, c.MemorySwap = 0, 0
		}, wantError: true},
		{name: "wrong memory", alter: func(c *container.HostConfig) {
			c.Memory, c.MemorySwap = 512<<20, 512<<20
		}, wantError: true},
		{name: "additional swap", alter: func(c *container.HostConfig) { c.MemorySwap = 512 << 20 }, wantError: true},
		{name: "unlimited PIDs", alter: func(c *container.HostConfig) { c.PidsLimit = nil }, wantError: true},
		{name: "wrong PIDs", alter: func(c *container.HostConfig) { c.PidsLimit = new(int64(256)) }, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := &container.HostConfig{
				CPUPeriod: 100000, CPUQuota: 100000, Memory: 268435456, MemorySwap: 268435456,
				PidsLimit: new(int64(128)),
			}
			if test.alter != nil {
				test.alter(config)
			}
			if test.missing {
				config = nil
			}
			topology := &observabilityTopology{containers: []namedContainer{{
				name: "tshark-analyzer",
				container: inspectOnlyContainer{inspection: &container.InspectResponse{
					HostConfig: config,
				}},
			}}}
			if err := topology.assertRuntimeContract(t.Context()); (err != nil) != test.wantError {
				t.Errorf("runtime validation error = %v, want error %t", err, test.wantError)
			}
		})
	}
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
			memory := int64(268435456)
			if test.role == "grafana" {
				memory = 536870912
			}
			topology := &observabilityTopology{
				contract:    observabilityContract{prometheusIP: "172.25.0.30"},
				networkName: "observability-test",
				volumeNames: append([]string(nil), test.volumeNames...),
				containers: []namedContainer{{
					name: test.role,
					container: inspectOnlyContainer{inspection: &container.InspectResponse{
						HostConfig: &container.HostConfig{
							CPUPeriod: 100000, CPUQuota: 100000, Memory: memory, MemorySwap: memory,
							PidsLimit: new(int64(128)),
						},
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
