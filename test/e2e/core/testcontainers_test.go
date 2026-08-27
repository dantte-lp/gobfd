//go:build e2e_core_testcontainers

package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec" //nolint:depguard // Evidence preserves the Podman CLI JSON contract against the exact configured socket.
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const (
	gobfdAIP          = "172.30.10.10"
	gobfdBIP          = "172.30.10.20"
	maxCoreLogBytes   = 1 << 20
	maxCorePCAPBytes  = 100 << 20
	coreReportRunTime = "20060102T150405Z"
)

type coreTopology struct {
	daemons          map[string]coreDaemon
	endpoint         string
	imageNames       []string
	networkName      string
	root             string
	runID            string
	reportDir        string
	capture          testcontainers.Container
	analyzer         testcontainers.Container
	packetsCollected bool
}

type coreDaemon struct {
	container       testcontainers.Container
	endpoint        string
	metricsEndpoint string
}

type coreSessionView struct {
	PeerAddress         string `json:"peer_address"`
	LocalState          string `json:"local_state"`
	RemoteState         string `json:"remote_state"`
	LocalDiscriminator  uint32 `json:"local_discriminator"`
	RemoteDiscriminator uint32 `json:"remote_discriminator"`
	AuthType            string `json:"auth_type"`
}

func TestCoreBuildContextIncludesGobfdctl(t *testing.T) {
	contextDir := prepareBuildContext(t, repositoryRoot(t))
	if _, err := os.Stat(filepath.Join(contextDir, "cmd/gobfdctl")); err != nil {
		t.Fatalf("bounded build context lacks cmd/gobfdctl: %v", err)
	}
	containerfile, err := os.ReadFile(filepath.Join(contextDir, "Containerfile"))
	if err != nil {
		t.Fatalf("read bounded Containerfile: %v", err)
	}
	if !bytes.Contains(containerfile, []byte("-o /bin/gobfdctl ./cmd/gobfdctl")) {
		t.Fatal("bounded Containerfile does not build /bin/gobfdctl")
	}
}

func TestCoreArtifactDiagnosticsContract(t *testing.T) {
	reportDir := t.TempDir()
	diagnostics := map[string]string{
		"containers.err":   "",
		"pcap-summary.err": "tshark summary warning\n",
		"packets.err":      "tshark packets warning\n",
	}
	for name, contents := range diagnostics {
		if err := writeCoreDiagnostic(reportDir, name, contents); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		path := filepath.Join(reportDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %s, want regular 0600", name, info.Mode())
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != contents {
			t.Fatalf("%s = %q, want exact %q", name, got, contents)
		}
	}
}

func TestCoreDiagnosticsPrecedeEvidenceFailures(t *testing.T) {
	for _, stage := range []string{"stop", "copy", "summary"} {
		t.Run(stage, func(t *testing.T) {
			reportDir := t.TempDir()
			t.Setenv("E2E_CORE_TESTCONTAINERS_ARTIFACT_DIR", reportDir)
			stageErr := errors.New(stage + " failed")
			err := func() error {
				coreReportDirectory(t, repositoryRoot(t))
				return fmt.Errorf("early evidence operation: %w", stageErr)
			}()
			if !errors.Is(err, stageErr) {
				t.Fatalf("early %s error = %v, want errors.Is stage failure", stage, err)
			}
			for _, name := range []string{"containers.err", "pcap-summary.err", "packets.err"} {
				path := filepath.Join(reportDir, name)
				info, statErr := os.Lstat(path)
				if statErr != nil {
					t.Fatalf("early %s failure lacks %s: %v", stage, name, statErr)
				}
				if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 0 {
					t.Fatalf("early %s %s = mode %s size %d, want regular 0600 empty", stage, name, info.Mode(), info.Size())
				}
			}
		})
	}

	reportDir := t.TempDir()
	t.Setenv("E2E_CORE_TESTCONTAINERS_ARTIFACT_DIR", reportDir)
	coreReportDirectory(t, repositoryRoot(t))
	err := writeCoreDiagnostic(reportDir, "containers.err", strings.Repeat("x", maxCoreLogBytes+1))
	if err == nil {
		t.Fatal("oversized containers.err write succeeded, want bounded truncation error")
	}
	info, statErr := os.Lstat(filepath.Join(reportDir, "containers.err"))
	if statErr != nil {
		t.Fatalf("oversized stderr made containers.err absent: %v", statErr)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != maxCoreLogBytes {
		t.Fatalf(
			"oversized containers.err = mode %s size %d, want regular 0600 size %d",
			info.Mode(), info.Size(), maxCoreLogBytes,
		)
	}
}

func TestCoreDaemonTestcontainers(t *testing.T) {
	// The public builder image needs no credentials. Isolate the test from
	// unrelated host credential helpers so a broken private-registry login
	// cannot make the Podman lifecycle nondeterministic.
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	endpoint := containertest.RequirePodman(t)
	t.Setenv("PODMAN_HOST", endpoint)
	var containerIDs []string
	var imageNames []string
	var networkName string

	if !t.Run("authenticated session lifecycle and parity", func(t *testing.T) {
		topology := startCoreTopology(t, endpoint)
		imageNames = append(imageNames, topology.imageNames...)
		networkName = topology.networkName
		for _, name := range []string{"gobfd-a", "gobfd-b"} {
			containerIDs = append(containerIDs, topology.daemons[name].container.GetContainerID())
		}
		containerIDs = append(containerIDs, topology.capture.GetContainerID(), topology.analyzer.GetContainerID())
		t.Cleanup(func() { topology.writeEvidence(t) })

		topology.requireSessionUp(t, "gobfd-a", gobfdBIP)
		topology.requireSessionUp(t, "gobfd-b", gobfdAIP)
		topology.requireCLIParity(t)
		topology.requireMetrics(t)
		topology.requireReloadParity(t)
		topology.requireGracefulStopPackets(t)
	}) {
		t.Error("e2e-core testcontainers lifecycle failed")
	}

	for _, containerID := range containerIDs {
		if containerID != "" {
			containertest.AssertContainerRemoved(t, endpoint, containerID)
		}
	}
	if networkName != "" {
		containertest.AssertNetworkRemoved(t, networkName)
	}
	for _, imageName := range imageNames {
		if imageName != "" {
			containertest.AssertImageRemoved(t, endpoint, imageName)
		}
	}
}

func startCoreTopology(t *testing.T, endpoint string) *coreTopology {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	t.Cleanup(cancel)
	root := repositoryRoot(t)
	runID, reportDir := coreReportDirectory(t, root)
	networkName := fmt.Sprintf("gobfd-e2e-core-%d", time.Now().UnixNano())
	buildID := time.Now().UnixNano()
	imageName := fmt.Sprintf("localhost/gobfd-e2e-core:test-%d", buildID)
	tsharkImageName := fmt.Sprintf("localhost/gobfd-e2e-core-tshark:test-%d", buildID)
	buildContext := prepareBuildContext(t, root)
	builtImage := buildCoreImage(ctx, t, endpoint, buildContext, imageName)
	builtTsharkImage := buildCoreImage(ctx, t, endpoint, prepareTsharkBuildContext(t, root), tsharkImageName)
	//nolint:staticcheck // Explicit provider and IPAM require the v0.44 API.
	_, err := containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name:   networkName,
		Driver: "bridge",
		Labels: map[string]string{"io.gobfd.test": "e2e-core", "io.gobfd.run": runID},
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet:  netip.MustParsePrefix("172.30.10.0/24"),
			Gateway: netip.MustParseAddr("172.30.10.1"),
		}}},
	})
	if err != nil {
		t.Fatalf("create e2e-core network: %v", err)
	}

	topology := &coreTopology{
		daemons: make(map[string]coreDaemon, 2), endpoint: endpoint,
		imageNames: []string{builtImage, builtTsharkImage}, networkName: networkName,
		root: root, runID: runID, reportDir: reportDir,
	}
	topology.writeRuntimeConfig(t, "gobfd-a.yml", coreConfig(gobfdAIP, gobfdBIP))
	topology.writeRuntimeConfig(t, "gobfd-b.yml", coreConfig(gobfdBIP, gobfdAIP))
	daemonA := startCoreDaemon(ctx, t, builtImage, networkName, runID, "gobfd-a", gobfdAIP, gobfdBIP)
	assertDaemonIsolation(ctx, t, endpoint, daemonA.container, networkName, gobfdAIP)
	topology.daemons["gobfd-a"] = daemonA
	inspection, err := daemonA.container.Inspect(ctx)
	if err != nil {
		t.Fatalf("inspect gobfd-a content-addressed image: %v", err)
	}
	if inspection.Image == "" {
		t.Fatal("gobfd-a inspection returned an empty image ID")
	}
	topology.capture = startCoreTshark(
		ctx, t, builtTsharkImage, runID, "gobfd-core-capture", daemonA.container.GetContainerID(), true,
	)
	tsharkImageID := coreContainerImageID(ctx, t, topology.capture, "capture")
	assertCoreTsharkIsolation(ctx, t, topology.capture, tsharkImageID, daemonA.container.GetContainerID())
	topology.analyzer = startCoreTshark(ctx, t, tsharkImageID, runID, "gobfd-core-analyzer", "", false)
	assertCoreTsharkIsolation(ctx, t, topology.analyzer, tsharkImageID, "")
	topology.imageNames = []string{inspection.Image, tsharkImageID}

	daemonB := startCoreDaemon(ctx, t, inspection.Image, networkName, runID, "gobfd-b", gobfdBIP, gobfdAIP)
	assertDaemonIsolation(ctx, t, endpoint, daemonB.container, networkName, gobfdBIP)
	topology.daemons["gobfd-b"] = daemonB
	return topology
}

func startCoreDaemon(
	ctx context.Context,
	t *testing.T,
	imageName string,
	networkName, runID, name, localIP, peerIP string,
) coreDaemon {
	t.Helper()

	request := testcontainers.ContainerRequest{
		Image:        imageName,
		Cmd:          []string{"-config", "/etc/gobfd/gobfd.yml"},
		ExposedPorts: []string{"50051/tcp", "9100/tcp"},
		Labels: map[string]string{
			"io.gobfd.test": "e2e-core", "io.gobfd.run": runID, "io.gobfd.role": name,
		},
		Networks: []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {name},
		},
		Files: []testcontainers.ContainerFile{
			{
				Reader:            strings.NewReader(coreConfig(localIP, peerIP)),
				ContainerFilePath: "/etc/gobfd/gobfd.yml",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForLog("metrics server listening").WithStartupTimeout(30 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			hostConfig.CapDrop = []string{"ALL"}
			hostConfig.CapAdd = []string{"NET_RAW", "NET_ADMIN"}
		},
		EndpointSettingsModifier: func(settings map[string]*network.EndpointSettings) {
			endpoint := settings[networkName]
			if endpoint == nil {
				endpoint = new(network.EndpointSettings)
				settings[networkName] = endpoint
			}
			endpoint.IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(localIP)}
		},
	}
	daemon, err := containertest.Run(ctx, t, request)
	captureContainerLogsOnFailure(ctx, t, daemon, name)
	if err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	endpoint, err := daemon.PortEndpoint(ctx, "50051/tcp", "http")
	if err != nil {
		t.Fatalf("resolve %s gRPC endpoint: %v", name, err)
	}
	metricsEndpoint, err := daemon.PortEndpoint(ctx, "9100/tcp", "http")
	if err != nil {
		t.Fatalf("resolve %s metrics endpoint: %v", name, err)
	}
	return coreDaemon{container: daemon, endpoint: endpoint, metricsEndpoint: metricsEndpoint}
}

func prepareBuildContext(t *testing.T, root string) string {
	t.Helper()

	contextDir := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{"cmd/gobfd", "cmd/gobfdctl", "internal", "pkg"} {
		subtree, err := fs.Sub(rootFS, sourceDir)
		if err != nil {
			t.Fatalf("open bounded build source %s: %v", sourceDir, err)
		}
		if err := os.CopyFS(filepath.Join(contextDir, sourceDir), subtree); err != nil {
			t.Fatalf("copy bounded build source %s: %v", sourceDir, err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := copyBuildFile(filepath.Join(root, name), filepath.Join(contextDir, name)); err != nil {
			t.Fatalf("copy %s into bounded build context: %v", name, err)
		}
	}
	const containerfile = `FROM docker.io/library/golang:1.27.0-trixie@sha256:` +
		`ae28539d2ef595b9a2930dd7f031d9592376829dc0eae7cb869559f7d5812c3a
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/gobfd ./cmd/gobfd
COPY cmd/gobfdctl ./cmd/gobfdctl
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfd ./cmd/gobfd
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfdctl ./cmd/gobfdctl
ENTRYPOINT ["/bin/gobfd"]
`
	if err := os.WriteFile(filepath.Join(contextDir, "Containerfile"), []byte(containerfile), 0o600); err != nil {
		t.Fatalf("write bounded test Containerfile: %v", err)
	}
	return contextDir
}

func prepareTsharkBuildContext(t *testing.T, root string) string {
	t.Helper()

	contextDir := t.TempDir()
	if err := copyBuildFile(
		filepath.Join(root, "test/interop/tshark/Containerfile"),
		filepath.Join(contextDir, "Containerfile"),
	); err != nil {
		t.Fatalf("copy bounded tshark Containerfile: %v", err)
	}
	return contextDir
}

func copyBuildFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy content: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}
	return nil
}

func buildCoreImage(
	ctx context.Context,
	t *testing.T,
	endpoint, buildContext, imageName string,
) string {
	t.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for image ownership: %v", err)
	}
	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		t.Fatalf("create Podman provider for image build: %v", err)
	}
	dockerProvider, ok := provider.(*testcontainers.DockerProvider)
	if !ok {
		t.Fatalf("Podman provider type = %T, want *testcontainers.DockerProvider", provider)
	}
	repo, tag, found := strings.Cut(imageName, ":")
	if !found {
		t.Fatalf("split e2e-core image name %q", imageName)
	}
	// ContainerRequest, unlike the embedded FromDockerfile, implements
	// ImageBuildInfo because the latter's BuildLogWriter field shadows the method.
	//nolint:modernize // FromDockerfile alone does not implement ImageBuildInfo.
	builtImage, err := dockerProvider.BuildImage(ctx, &testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    buildContext,
			Dockerfile: "Containerfile",
			Repo:       repo,
			Tag:        tag,
			KeepImage:  true,
		},
	})
	closeErr := provider.Close()
	if joinedErr := errors.Join(err, closeErr); joinedErr != nil {
		t.Fatalf("build bounded e2e-core image: %v", joinedErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		if removeErr := client.RemoveImage(cleanupCtx, builtImage); removeErr != nil {
			t.Errorf("remove e2e-core image %s: %v", builtImage, removeErr)
		}
	})
	return builtImage
}

func startCoreTshark(
	ctx context.Context,
	t *testing.T,
	imageID, runID, role, networkContainerID string,
	capture bool,
) testcontainers.Container {
	t.Helper()

	request := testcontainers.ContainerRequest{
		Image:  imageID,
		Labels: map[string]string{"io.gobfd.test": "e2e-core", "io.gobfd.run": runID, "io.gobfd.role": role},
	}
	if capture {
		request.WaitingFor = wait.ForExec([]string{"test", "-f", "/captures/bfd.pcapng"}).WithStartupTimeout(20 * time.Second)
		request.HostConfigModifier = func(hostConfig *container.HostConfig) {
			hostConfig.CapDrop = []string{"ALL"}
			hostConfig.CapAdd = []string{"NET_RAW", "NET_ADMIN"}
			hostConfig.NetworkMode = container.NetworkMode("container:" + networkContainerID)
		}
	} else {
		request.Entrypoint = []string{"sleep"}
		request.Cmd = []string{"infinity"}
		request.WaitingFor = wait.ForExec([]string{"test", "-d", "/captures"}).WithStartupTimeout(20 * time.Second)
		request.HostConfigModifier = func(hostConfig *container.HostConfig) {
			hostConfig.CapDrop = []string{"ALL"}
		}
	}
	tshark, err := containertest.Run(ctx, t, request)
	captureContainerLogsOnFailure(ctx, t, tshark, role)
	if err != nil {
		t.Fatalf("start %s: %v", role, err)
	}
	return tshark
}

func assertCoreTsharkIsolation(
	ctx context.Context,
	t *testing.T,
	tshark testcontainers.Container,
	wantImageID, networkContainerID string,
) {
	t.Helper()

	inspection, err := tshark.Inspect(ctx)
	if err != nil {
		t.Fatalf("inspect core tshark isolation: %v", err)
	}
	if inspection.Image != wantImageID {
		t.Fatalf("tshark image = %q, want exact content ID %q", inspection.Image, wantImageID)
	}
	if inspection.HostConfig == nil {
		t.Fatal("tshark inspection has nil HostConfig")
	}
	if inspection.HostConfig.Privileged {
		t.Fatal("tshark unexpectedly runs privileged")
	}
	if len(inspection.Mounts) != 0 {
		t.Fatalf("tshark mounts = %+v, want no host or volume mounts", inspection.Mounts)
	}
	gotMode := string(inspection.HostConfig.NetworkMode)
	if networkContainerID != "" {
		wantMode := "container:" + networkContainerID
		if gotMode != wantMode {
			t.Fatalf("capture network mode = %q, want exact immutable namespace %q", gotMode, wantMode)
		}
	} else if strings.HasPrefix(gotMode, "container:") {
		t.Fatalf("analyzer unexpectedly shares container network namespace %q", gotMode)
	}
}

func coreContainerImageID(
	ctx context.Context,
	t *testing.T,
	testContainer testcontainers.Container,
	description string,
) string {
	t.Helper()
	inspection, err := testContainer.Inspect(ctx)
	if err != nil {
		t.Fatalf("inspect %s content-addressed image: %v", description, err)
	}
	imageID := strings.TrimPrefix(inspection.Image, "sha256:")
	if len(imageID) != 64 {
		t.Fatalf("%s image ID = %q, want 64 lowercase hexadecimal characters", description, inspection.Image)
	}
	for _, character := range imageID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			t.Fatalf("%s image ID = %q, want 64 lowercase hexadecimal characters", description, inspection.Image)
		}
	}
	return inspection.Image
}

func coreReportDirectory(t *testing.T, root string) (string, string) {
	t.Helper()

	runID := time.Now().UTC().Format(coreReportRunTime)
	reportDir := strings.TrimSpace(os.Getenv("E2E_CORE_TESTCONTAINERS_ARTIFACT_DIR"))
	if reportDir == "" {
		reportDir = filepath.Join(root, "reports/e2e/core", runID)
	} else {
		if !filepath.IsAbs(reportDir) {
			t.Fatalf("e2e-core artifact directory %q must be absolute", reportDir)
		}
		runID = filepath.Base(filepath.Clean(reportDir))
	}
	for _, path := range []string{reportDir, filepath.Join(reportDir, "runtime"), filepath.Join(reportDir, "captures")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("create e2e-core artifact directory %s: %v", path, err)
		}
	}
	if err := initializeCoreDiagnostics(reportDir); err != nil {
		t.Fatalf("initialize e2e-core artifact diagnostics: %v", err)
	}
	return runID, reportDir
}

func (topology *coreTopology) writeRuntimeConfig(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(topology.reportDir, "runtime", name), []byte(contents), 0o600); err != nil {
		t.Fatalf("write runtime config %s: %v", name, err)
	}
}

func (topology *coreTopology) requireCLIParity(t *testing.T) {
	t.Helper()
	daemon := topology.daemons["gobfd-a"]

	listOutput := topology.execGobfdctl(t, daemon, "session", "list")
	var sessions []coreSessionView
	decodeOneCoreJSON(t, listOutput, &sessions)
	if len(sessions) != 1 || sessions[0].PeerAddress != gobfdBIP || sessions[0].LocalState != "Up" ||
		sessions[0].RemoteState != "Up" || sessions[0].AuthType != "SimplePassword" ||
		sessions[0].LocalDiscriminator == 0 || sessions[0].RemoteDiscriminator == 0 {
		t.Fatalf("gobfdctl session list = %+v, want one authenticated Up session to %s", sessions, gobfdBIP)
	}

	showOutput := topology.execGobfdctl(t, daemon, "session", "show", gobfdBIP)
	var shown coreSessionView
	decodeOneCoreJSON(t, showOutput, &shown)
	if shown.PeerAddress != gobfdBIP || shown.LocalState != "Up" || shown.RemoteState != "Up" {
		t.Fatalf("gobfdctl session show = %+v, want peer %s Up", shown, gobfdBIP)
	}

	monitorCtx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	exitCode, output, err := containertest.Exec(monitorCtx, daemon.container, []string{
		"timeout", "3s", "/bin/gobfdctl", "--addr", "127.0.0.1:50051", "--format", "json",
		"monitor", "--current",
	})
	if err != nil {
		t.Fatalf("gobfdctl monitor --current: %v", err)
	}
	if exitCode != 124 {
		t.Fatalf("gobfdctl monitor exit = %d, output=%q, want timeout exit 124", exitCode, output)
	}
	if !strings.Contains(output, gobfdBIP) || !strings.Contains(output, "Up") {
		t.Fatalf("gobfdctl monitor output = %q, want current Up event for %s", output, gobfdBIP)
	}
}

func (topology *coreTopology) execGobfdctl(t *testing.T, daemon coreDaemon, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := make([]string, 0, 5+len(args))
	command = append(command, "/bin/gobfdctl", "--addr", "127.0.0.1:50051", "--format", "json")
	command = append(command, args...)
	exitCode, output, err := containertest.Exec(ctx, daemon.container, command)
	if err != nil || exitCode != 0 {
		t.Fatalf("gobfdctl %s: exit=%d output=%q error=%v", strings.Join(args, " "), exitCode, output, err)
	}
	return output
}

func decodeOneCoreJSON(t *testing.T, input string, target any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(input))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode gobfdctl JSON: %v: %s", err, input)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			t.Fatalf("decode gobfdctl JSON: multiple documents: %s", input)
		}
		t.Fatalf("finish gobfdctl JSON: %v: %s", err, input)
	}
}

func (topology *coreTopology) requireMetrics(t *testing.T) {
	t.Helper()
	for _, name := range []string{"gobfd-a", "gobfd-b"} {
		daemon := topology.daemons[name]
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, daemon.metricsEndpoint+"/metrics", nil)
		if err != nil {
			cancel()
			t.Fatalf("build %s metrics request: %v", name, err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			cancel()
			t.Fatalf("GET %s metrics: %v", name, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxCoreLogBytes+1))
		closeErr := response.Body.Close()
		cancel()
		if joinedErr := errors.Join(readErr, closeErr); joinedErr != nil {
			t.Fatalf("read %s metrics: %v", name, joinedErr)
		}
		if len(body) > maxCoreLogBytes {
			t.Fatalf("%s metrics exceed %d bytes", name, maxCoreLogBytes)
		}
		if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("gobfd_bfd_sessions")) {
			t.Fatalf("%s metrics status=%d body=%q", name, response.StatusCode, body)
		}
	}
}

func (topology *coreTopology) requireReloadParity(t *testing.T) {
	t.Helper()
	daemon := topology.daemons["gobfd-a"]
	updatedConfig := coreConfigWithLogLevel(gobfdAIP, gobfdBIP, "info")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	if err := daemon.container.CopyToContainer(ctx, []byte(updatedConfig), "/etc/gobfd/gobfd.yml", 0o644); err != nil {
		cancel()
		t.Fatalf("copy mutable gobfd-a config: %v", err)
	}
	exitCode, output, err := containertest.Exec(ctx, daemon.container, []string{"/bin/sh", "-c", "kill -HUP 1"})
	cancel()
	if err != nil || exitCode != 0 {
		t.Fatalf("kill -HUP 1: exit=%d output=%q error=%v", exitCode, output, err)
	}
	topology.writeRuntimeConfig(t, "gobfd-a.yml", updatedConfig)
	topology.requireSessionUp(t, "gobfd-a", gobfdBIP)
	topology.requireLogContains(t, daemon.container, "configuration reloaded")
}

func (topology *coreTopology) requireLogContains(t *testing.T, daemon testcontainers.Container, want string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		logs, err := readCoreContainerLogs(ctx, daemon)
		cancel()
		if err == nil {
			last = logs
			if strings.Contains(logs, want) {
				return
			}
		} else {
			last = err.Error()
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("container logs lack %q; last=%s", want, last)
		case <-t.Context().Done():
			t.Fatalf("wait for container log %q: %v", want, t.Context().Err())
		}
	}
}

func (topology *coreTopology) requireGracefulStopPackets(t *testing.T) {
	t.Helper()
	daemon := topology.daemons["gobfd-a"]
	stopCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	stopTimeout := 5 * time.Second
	if err := daemon.container.Stop(stopCtx, &stopTimeout); err != nil {
		t.Fatalf("gracefully stop gobfd-a: %v", err)
	}
	inspection, err := daemon.container.Inspect(stopCtx)
	if err != nil {
		t.Fatalf("inspect stopped gobfd-a: %v", err)
	}
	if inspection.State == nil || inspection.State.Running || inspection.State.ExitCode != 0 {
		t.Fatalf("gobfd-a stopped state = %+v, want non-running exit zero", inspection.State)
	}
	if err := topology.collectPacketArtifacts(stopCtx); err != nil {
		t.Fatalf("collect core packet artifacts: %v", err)
	}
	for _, check := range []struct {
		filter, description string
	}{
		{"bfd && ip.src == " + gobfdAIP + " && ip.dst == " + gobfdBIP + " && bfd.sta == 0x03", "Up"},
		{
			"bfd && ip.src == " + gobfdAIP + " && ip.dst == " + gobfdBIP +
				" && bfd.sta == 0x00 && bfd.diag == 0x07",
			"AdminDown",
		},
	} {
		count, countErr := topology.tsharkPacketCount(stopCtx, check.filter)
		if countErr != nil {
			t.Fatalf("count %s packets: %v", check.description, countErr)
		}
		if count == 0 {
			t.Fatalf("capture has no %s packets for filter %q", check.description, check.filter)
		}
	}
}

func (topology *coreTopology) collectPacketArtifacts(ctx context.Context) error {
	if topology.packetsCollected {
		return nil
	}
	if err := stopCoreContainer(ctx, topology.capture, 3*time.Second); err != nil {
		return fmt.Errorf("stop tshark capture: %w", err)
	}
	reader, err := topology.capture.CopyFileFromContainer(ctx, "/captures/bfd.pcapng")
	if err != nil {
		return fmt.Errorf("copy packet capture from tshark: %w", err)
	}
	pcap, readErr := io.ReadAll(io.LimitReader(reader, maxCorePCAPBytes+1))
	closeErr := reader.Close()
	if joinedErr := errors.Join(readErr, closeErr); joinedErr != nil {
		return fmt.Errorf("read packet capture: %w", joinedErr)
	}
	if len(pcap) == 0 {
		return errors.New("packet capture is empty")
	}
	if len(pcap) > maxCorePCAPBytes {
		return fmt.Errorf("packet capture exceeds %d bytes", maxCorePCAPBytes)
	}
	for _, path := range []string{
		filepath.Join(topology.reportDir, "packets.pcapng"),
		filepath.Join(topology.reportDir, "captures", "bfd.pcapng"),
	} {
		if writeErr := os.WriteFile(path, pcap, 0o600); writeErr != nil {
			return fmt.Errorf("write packet capture %s: %w", path, writeErr)
		}
	}
	if copyErr := topology.analyzer.CopyToContainer(ctx, pcap, "/captures/bfd.pcapng", 0o600); copyErr != nil {
		return fmt.Errorf("copy packet capture to analyzer: %w", copyErr)
	}

	summary, err := topology.execTshark(ctx, "pcap-summary.err", []string{
		"-r", "/captures/bfd.pcapng", "-Y", "bfd", "-T", "fields",
		"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst", "-e", "bfd.sta", "-e", "bfd.diag",
		"-e", "bfd.my_discriminator", "-e", "bfd.your_discriminator",
		"-E", "header=y", "-E", "separator=\t",
	})
	if err != nil {
		return fmt.Errorf("decode pcap summary: %w", err)
	}
	if summaryErr := validateCorePacketSummary(summary); summaryErr != nil {
		return summaryErr
	}
	if writeErr := os.WriteFile(
		filepath.Join(topology.reportDir, "pcap-summary.tsv"), []byte(summary), 0o600,
	); writeErr != nil {
		return fmt.Errorf("write pcap summary: %w", writeErr)
	}

	packets, err := topology.execTshark(ctx, "packets.err", []string{
		"-r", "/captures/bfd.pcapng", "-Y", "bfd", "-T", "fields",
		"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst",
		"-e", "udp.srcport", "-e", "udp.dstport", "-e", "bfd.sta", "-e", "bfd.diag",
		"-e", "bfd.message_length", "-e", "bfd.flags.a", "-e", "bfd.auth.type", "-e", "bfd.auth.key",
		"-e", "bfd.my_discriminator", "-e", "bfd.your_discriminator",
		"-E", "header=y", "-E", "separator=,",
	})
	if err != nil {
		return fmt.Errorf("decode packet CSV: %w", err)
	}
	if err := os.WriteFile(filepath.Join(topology.reportDir, "packets.csv"), []byte(packets), 0o600); err != nil {
		return fmt.Errorf("write packet CSV: %w", err)
	}
	topology.packetsCollected = true
	return nil
}

func validateCorePacketSummary(summary string) error {
	const header = "frame.time_relative\tip.src\tip.dst\tbfd.sta\tbfd.diag\t" +
		"bfd.my_discriminator\tbfd.your_discriminator"
	lines := strings.Split(strings.TrimSpace(summary), "\n")
	if len(lines) < 2 || lines[0] != header {
		return fmt.Errorf("pcap summary lacks exact header or packet row: %q", summary)
	}
	for index, line := range lines[1:] {
		if strings.Count(line, "\t") != 6 {
			return fmt.Errorf("pcap summary row %d is not seven-field TSV: %q", index+2, line)
		}
	}
	return nil
}

func stopCoreContainer(ctx context.Context, testContainer testcontainers.Container, timeout time.Duration) error {
	state, err := testContainer.State(ctx)
	if err != nil {
		return fmt.Errorf("inspect container state: %w", err)
	}
	if !state.Running {
		return nil
	}
	if err := testContainer.Stop(ctx, &timeout); err != nil {
		return fmt.Errorf("stop container with %s grace: %w", timeout, err)
	}
	return nil
}

func (topology *coreTopology) execTshark(
	ctx context.Context,
	diagnosticName string,
	args []string,
) (string, error) {
	if diagnosticName != "" {
		if err := writeCoreDiagnostic(topology.reportDir, diagnosticName, ""); err != nil {
			return "", err
		}
	}
	command := append([]string{"tshark"}, args...)
	client, err := podmanapi.NewClient(strings.TrimPrefix(topology.endpoint, "unix://"))
	if err != nil {
		return "", fmt.Errorf("create exact tshark exec client: %w", err)
	}
	result, err := client.Exec(ctx, topology.analyzer.GetContainerID(), command)
	if err != nil {
		execErr := fmt.Errorf(
			"exec tshark in %s: %w: stdout=%q stderr=%q",
			topology.analyzer.GetContainerID(), err, result.Stdout, result.Stderr,
		)
		if diagnosticName == "" {
			return "", execErr
		}
		diagnosticErr := writeCoreDiagnostic(topology.reportDir, diagnosticName, result.Stderr)
		return "", errors.Join(execErr, diagnosticErr)
	}
	return result.Stdout, nil
}

func (topology *coreTopology) tsharkPacketCount(ctx context.Context, filter string) (int, error) {
	output, err := topology.execTshark(ctx, "", []string{
		"-r", "/captures/bfd.pcapng", "-Y", filter, "-T", "fields", "-e", "frame.number",
	})
	if err != nil {
		return 0, err
	}
	count := 0
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

func (topology *coreTopology) writeEvidence(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var evidenceErrors []error
	if !topology.packetsCollected {
		if err := topology.collectPacketArtifacts(ctx); err != nil {
			evidenceErrors = append(evidenceErrors, err)
		}
	}
	if err := topology.writeContainerLogs(ctx); err != nil {
		evidenceErrors = append(evidenceErrors, err)
	}
	if err := topology.writeContainerSnapshot(ctx); err != nil {
		evidenceErrors = append(evidenceErrors, err)
	}
	if err := topology.writeEnvironment(); err != nil {
		evidenceErrors = append(evidenceErrors, err)
	}
	status := 0
	if t.Failed() || len(evidenceErrors) != 0 {
		status = 1
	}
	if err := topology.writeSummary(status); err != nil {
		evidenceErrors = append(evidenceErrors, err)
	}
	for _, err := range evidenceErrors {
		t.Errorf("write e2e-core evidence before cleanup: %v", err)
	}
	t.Logf("e2e-core testcontainers artifacts: %s", topology.reportDir)
}

func (topology *coreTopology) writeContainerLogs(ctx context.Context) error {
	var output strings.Builder
	for _, name := range []string{"gobfd-a", "gobfd-b"} {
		logs, err := readCoreContainerLogs(ctx, topology.daemons[name].container)
		if err != nil {
			return fmt.Errorf("read %s evidence logs: %w", name, err)
		}
		fmt.Fprintf(&output, "===== %s =====\n%s\n", name, logs)
	}
	for _, item := range []struct {
		name      string
		container testcontainers.Container
	}{
		{"gobfd-core-capture", topology.capture},
		{"gobfd-core-analyzer", topology.analyzer},
	} {
		logs, err := readCoreContainerLogs(ctx, item.container)
		if err != nil {
			return fmt.Errorf("read %s evidence logs: %w", item.name, err)
		}
		fmt.Fprintf(&output, "===== %s =====\n%s\n", item.name, logs)
	}
	if err := os.WriteFile(
		filepath.Join(topology.reportDir, "containers.log"), []byte(output.String()), 0o600,
	); err != nil {
		return fmt.Errorf("write container logs: %w", err)
	}
	return nil
}

func readCoreContainerLogs(ctx context.Context, testContainer testcontainers.Container) (string, error) {
	logs, err := testContainer.Logs(ctx)
	if err != nil {
		return "", fmt.Errorf("open logs: %w", err)
	}
	output, readErr := io.ReadAll(io.LimitReader(logs, maxCoreLogBytes+1))
	closeErr := logs.Close()
	if joinedErr := errors.Join(readErr, closeErr); joinedErr != nil {
		return "", fmt.Errorf("consume logs: %w", joinedErr)
	}
	if len(output) > maxCoreLogBytes {
		return "", fmt.Errorf("logs exceed %d bytes", maxCoreLogBytes)
	}
	return string(output), nil
}

func writeCoreDiagnostic(reportDir, name, contents string) error {
	truncated := len(contents) > maxCoreLogBytes
	if truncated {
		contents = contents[:maxCoreLogBytes]
	}
	path := filepath.Join(reportDir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create diagnostic %s: %w", name, err)
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod diagnostic %s: %w", name, errors.Join(err, file.Close()))
	}
	_, writeErr := io.WriteString(file, contents)
	closeErr := file.Close()
	if joinedErr := errors.Join(writeErr, closeErr); joinedErr != nil {
		return fmt.Errorf("write diagnostic %s: %w", name, joinedErr)
	}
	if truncated {
		return fmt.Errorf("diagnostic %s truncated to %d bytes", name, maxCoreLogBytes)
	}
	return nil
}

func initializeCoreDiagnostics(reportDir string) error {
	var diagnosticsErr error
	for _, name := range []string{"containers.err", "pcap-summary.err", "packets.err"} {
		if err := writeCoreDiagnostic(reportDir, name, ""); err != nil {
			diagnosticsErr = errors.Join(diagnosticsErr, err)
		}
	}
	if diagnosticsErr != nil {
		return fmt.Errorf("initialize artifact diagnostics: %w", diagnosticsErr)
	}
	return nil
}

func (topology *coreTopology) writeContainerSnapshot(ctx context.Context) error {
	command := exec.CommandContext(ctx, "podman", "ps", "-a",
		"--filter", "label=io.gobfd.run="+topology.runID, "--format", "json")
	command.Env = podmanChildEnvironment(topology.endpoint)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	diagnostic := ""
	if err != nil {
		diagnostic = stderr.String()
	}
	if writeErr := writeCoreDiagnostic(topology.reportDir, "containers.err", diagnostic); writeErr != nil {
		return errors.Join(fmt.Errorf("write container snapshot diagnostics: %w", writeErr), err)
	}
	if err != nil {
		return fmt.Errorf("snapshot exact test containers: %w: %s", err, stderr.String())
	}
	if err := os.WriteFile(filepath.Join(topology.reportDir, "containers.json"), output, 0o600); err != nil {
		return fmt.Errorf("write container snapshot: %w", err)
	}
	return nil
}

func podmanChildEnvironment(endpoint string) []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "DOCKER_HOST=") || strings.HasPrefix(entry, "PODMAN_HOST=") ||
			strings.HasPrefix(entry, "CONTAINER_HOST=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "DOCKER_HOST="+endpoint, "PODMAN_HOST="+endpoint, "CONTAINER_HOST="+endpoint)
}

func (topology *coreTopology) writeEnvironment() error {
	type environment struct {
		Target         string `json:"target"`
		RunID          string `json:"run_id"`
		ComposeProject string `json:"compose_project"`
		ComposeFile    string `json:"compose_file"`
		DevProject     string `json:"dev_project"`
		PodmanRuntime  string `json:"podman_runtime"`
		AGRPCPort      int    `json:"a_grpc_port"`
		AMetricsPort   int    `json:"a_metrics_port"`
		BGRPCPort      int    `json:"b_grpc_port"`
		BMetricsPort   int    `json:"b_metrics_port"`
	}
	devProject := strings.TrimSpace(os.Getenv("COMPOSE_PROJECT_NAME"))
	if devProject == "" {
		devProject = filepath.Base(topology.root)
	}
	aGRPCPort, err := mappedEndpointPort(topology.daemons["gobfd-a"].endpoint)
	if err != nil {
		return err
	}
	aMetricsPort, err := mappedEndpointPort(topology.daemons["gobfd-a"].metricsEndpoint)
	if err != nil {
		return err
	}
	bGRPCPort, err := mappedEndpointPort(topology.daemons["gobfd-b"].endpoint)
	if err != nil {
		return err
	}
	bMetricsPort, err := mappedEndpointPort(topology.daemons["gobfd-b"].metricsEndpoint)
	if err != nil {
		return err
	}
	document := environment{
		Target: "e2e-core", RunID: topology.runID,
		ComposeProject: "testcontainers-owned", ComposeFile: "", DevProject: devProject,
		PodmanRuntime: "podman-testcontainers",
		AGRPCPort:     aGRPCPort,
		AMetricsPort:  aMetricsPort,
		BGRPCPort:     bGRPCPort,
		BMetricsPort:  bMetricsPort,
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode environment evidence: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(filepath.Join(topology.reportDir, "environment.json"), contents, 0o600); err != nil {
		return fmt.Errorf("write environment evidence: %w", err)
	}
	return nil
}

func mappedEndpointPort(endpoint string) (int, error) {
	hostPort := strings.TrimPrefix(endpoint, "http://")
	_, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return 0, fmt.Errorf("parse mapped endpoint %q: %w", endpoint, err)
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("parse mapped endpoint port %q: %w", port, err)
	}
	return value, nil
}

func (topology *coreTopology) writeSummary(status int) error {
	contents := fmt.Sprintf(`# e2e-core Summary

| Field | Value |
|---|---|
| Target | %s |
| Run ID | %s |
| Compose project | %s |
| Exit code | %d |
| Go test JSON | %s |
| Packet capture | %s |
| Packet CSV | %s |
| Container logs | %s |
`, "`make e2e-core`", "`"+topology.runID+"`", "`testcontainers-owned`", status,
		"`go-test.json`", "`packets.pcapng`", "`packets.csv`", "`containers.log`")
	if err := os.WriteFile(filepath.Join(topology.reportDir, "summary.md"), []byte(contents), 0o600); err != nil {
		return fmt.Errorf("write summary evidence: %w", err)
	}
	return nil
}

func assertDaemonIsolation(
	ctx context.Context,
	t *testing.T,
	podmanEndpoint string,
	daemon testcontainers.Container,
	networkName, wantIP string,
) {
	t.Helper()

	inspection, err := daemon.Inspect(ctx)
	if err != nil {
		t.Fatalf("inspect daemon isolation contract: %v", err)
	}
	if inspection.HostConfig == nil {
		t.Fatal("daemon inspection has nil HostConfig")
	}
	if inspection.HostConfig.Privileged {
		t.Fatal("daemon unexpectedly runs privileged")
	}
	if len(inspection.Mounts) != 0 {
		t.Fatalf("daemon mounts = %+v, want no host or volume mounts", inspection.Mounts)
	}
	wantCaps := []string{"NET_ADMIN", "NET_RAW"}
	gotCaps := normalizeCapabilities(inspection.HostConfig.CapAdd)
	if !slices.Equal(gotCaps, wantCaps) {
		t.Fatalf("daemon CapAdd = %v, want exactly %v", gotCaps, wantCaps)
	}
	assertEffectiveCapabilities(ctx, t, podmanEndpoint, daemon.GetContainerID())
	if inspection.NetworkSettings == nil {
		t.Fatal("daemon inspection has nil NetworkSettings")
	}
	endpoint := inspection.NetworkSettings.Networks[networkName]
	if endpoint == nil {
		t.Fatalf("daemon is not attached to network %s", networkName)
	}
	if got := endpoint.IPAddress.String(); got != wantIP {
		t.Fatalf("daemon IP on %s = %s, want %s", networkName, got, wantIP)
	}
}

func assertEffectiveCapabilities(ctx context.Context, t *testing.T, endpoint, containerID string) {
	t.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for effective capability inspection: %v", err)
	}
	effectiveCaps, err := client.EffectiveCapabilities(ctx, containerID)
	if err != nil {
		t.Fatalf("inspect daemon effective capabilities: %v", err)
	}
	got := normalizeCapabilities(effectiveCaps)
	want := []string{"NET_ADMIN", "NET_RAW"}
	if !slices.Equal(got, want) {
		t.Fatalf("daemon effective capabilities = %v, want exactly %v", got, want)
	}
}

func normalizeCapabilities(capabilities []string) []string {
	normalized := make([]string, len(capabilities))
	for i, capability := range capabilities {
		normalized[i] = strings.TrimPrefix(capability, "CAP_")
	}
	slices.Sort(normalized)
	return normalized
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get test working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		} else if !os.IsNotExist(statErr) {
			t.Fatalf("inspect repository root candidate %s: %v", dir, statErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("find repository root from %s: go.mod not found", dir)
		}
		dir = parent
	}
}

func (topology *coreTopology) requireSessionUp(t *testing.T, name, peerIP string) {
	t.Helper()

	daemon, ok := topology.daemons[name]
	if !ok {
		t.Fatalf("topology has no daemon %s", name)
	}
	client := bfdv1connect.NewBfdServiceClient(http.DefaultClient, daemon.endpoint)
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var last string

	for {
		requestCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		response, err := client.ListSessions(requestCtx, &bfdv1.ListSessionsRequest{})
		cancel()
		if err != nil {
			last = err.Error()
		} else {
			last = response.String()
			for _, session := range response.GetSessions() {
				if session.GetPeerAddress() == peerIP &&
					session.GetLocalState() == bfdv1.SessionState_SESSION_STATE_UP &&
					session.GetRemoteState() == bfdv1.SessionState_SESSION_STATE_UP &&
					session.GetAuthType() == bfdv1.AuthenticationType_AUTHENTICATION_TYPE_SIMPLE_PASSWORD &&
					session.GetLocalDiscriminator() != 0 && session.GetRemoteDiscriminator() != 0 {
					return
				}
			}
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("%s session with peer %s did not reach authenticated Up; last=%s", name, peerIP, last)
		case <-t.Context().Done():
			t.Fatalf("wait for %s session with peer %s: %v", name, peerIP, t.Context().Err())
		}
	}
}

func captureContainerLogsOnFailure(
	parent context.Context,
	t *testing.T,
	daemon testcontainers.Container,
	name string,
) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		if daemon == nil {
			t.Logf("%s did not return a container for diagnostics", name)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		logs, err := daemon.Logs(ctx)
		if err != nil {
			t.Logf("read %s diagnostics: %v", name, err)
			return
		}
		defer logs.Close()
		const maxLogBytes = 1 << 20
		buffer := new(strings.Builder)
		if _, copyErr := io.CopyN(buffer, logs, maxLogBytes); copyErr != nil && !errors.Is(copyErr, io.EOF) {
			t.Logf("read %s diagnostics: %v", name, copyErr)
			return
		}
		t.Logf("%s logs:\n%s", name, buffer.String())
	})
}

func coreConfig(localIP, peerIP string) string {
	return coreConfigWithLogLevel(localIP, peerIP, "debug")
}

func coreConfigWithLogLevel(localIP, peerIP, logLevel string) string {
	return fmt.Sprintf(`grpc:
  addr: ":50051"
metrics:
  addr: ":9100"
  path: "/metrics"
log:
  level: "%s"
  format: "text"
bfd:
  default_desired_min_tx: "300ms"
  default_required_min_rx: "300ms"
  default_detect_multiplier: 3
sessions:
  - peer: "%s"
    local: "%s"
    type: single_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
    auth:
      type: simple_password
      key_id: 7
      secret: "s10-core-auth"
`, logLevel, peerIP, localIP)
}
