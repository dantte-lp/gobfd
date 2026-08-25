//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"os/exec" //nolint:depguard // The outer topology runs the current test binary with a fixed test filter.
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const (
	fourPeerHoloImage = "ghcr.io/holo-routing/holo-bundle@sha256:" +
		"5c1f61475b1623b3eab611921f8319fb0a10492ced3f7da05e656418abb5ca4a"
	fourPeerFRRImage = "quay.io/frrouting/frr:10.7.0@sha256:" +
		"65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68"
	fourPeerProjectLabel = "com.docker.compose.project"
)

type fourPeerResources struct {
	containerIDs []string
	imageNames   []string
	networkName  string
	tshark       testcontainers.Container
}

func TestFourPeerTopologyTestcontainers(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	resources := new(fourPeerResources)

	if !t.Run("test-owned topology", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
		t.Cleanup(cancel)
		projectName := fmt.Sprintf("gobfd-interop-tc-%d", time.Now().UnixNano())
		root := fourPeerRepositoryRoot(t)

		assertFourPeerNamesAvailable(ctx, t, endpoint)
		registerFourPeerScapyImageCleanup(ctx, t, endpoint, resources)
		startFourPeerTopology(ctx, t, endpoint, root, projectName, resources)
		runFourPeerAssertions(ctx, t, projectName)
		captureFourPeerPCAP(ctx, t, resources.tshark)
	}) {
		t.Error("four-peer testcontainers topology failed")
	}

	for _, containerID := range resources.containerIDs {
		containertest.AssertContainerRemoved(t, endpoint, containerID)
	}
	if resources.networkName != "" {
		containertest.AssertNetworkRemoved(t, resources.networkName)
	}
	for _, imageName := range resources.imageNames {
		containertest.AssertImageRemoved(t, endpoint, imageName)
	}
}

func registerFourPeerScapyImageCleanup(
	ctx context.Context,
	t *testing.T,
	endpoint string,
	resources *fourPeerResources,
) {
	t.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for Scapy image ownership: %v", err)
	}
	exists, err := client.ImageExists(ctx, scapyImage)
	if err != nil {
		t.Fatalf("inspect Scapy image before test: %v", err)
	}
	if exists {
		t.Fatalf("Scapy image %s already exists; refusing ambiguous ownership", scapyImage)
	}
	resources.imageNames = append(resources.imageNames, scapyImage)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		exists, err := client.ImageExists(cleanupCtx, scapyImage)
		if err != nil {
			t.Errorf("inspect test-owned Scapy image during cleanup: %v", err)
			return
		}
		if exists {
			if err := client.RemoveImage(cleanupCtx, scapyImage); err != nil {
				t.Errorf("remove test-owned Scapy image: %v", err)
			}
		}
	})
}

func startFourPeerTopology(
	ctx context.Context,
	t *testing.T,
	endpoint, root, projectName string,
	resources *fourPeerResources,
) {
	t.Helper()

	buildID := time.Now().UnixNano()
	gobfdImage := buildFourPeerImage(
		ctx, t, endpoint, prepareFourPeerGoContext(t, root),
		fmt.Sprintf("localhost/gobfd-interop-test:%d", buildID),
	)
	birdImage := buildFourPeerImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/bird3"),
		fmt.Sprintf("localhost/bird3-interop-test:%d", buildID),
	)
	thoroImage := buildFourPeerImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/thoro"),
		fmt.Sprintf("localhost/thoro-interop-test:%d", buildID),
	)
	tsharkImage := buildFourPeerImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/tshark"),
		fmt.Sprintf("localhost/tshark-interop-test:%d", buildID),
	)
	resources.imageNames = append(resources.imageNames, gobfdImage, birdImage, thoroImage, tsharkImage)

	networkName := interopNetworkName(projectName)
	resources.networkName = networkName
	//nolint:staticcheck // ProviderPodman plus static IPAM requires this v0.44 API.
	_, err := containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name:   networkName,
		Driver: "bridge",
		Labels: map[string]string{"io.gobfd.test": "four-peer-testcontainers"},
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet:  netip.MustParsePrefix("172.20.0.0/24"),
			Gateway: netip.MustParseAddr("172.20.0.1"),
		}}},
	})
	if err != nil {
		t.Fatalf("create four-peer Podman network: %v", err)
	}

	labels := map[string]string{
		fourPeerProjectLabel: projectName,
		"io.gobfd.test":      "four-peer-testcontainers",
	}
	holo := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    fourPeerHoloImage,
		Name:     "holo-interop",
		Labels:   labels,
		Networks: []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"holo"},
		},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop/holo/holod.toml"),
			ContainerFilePath: "/etc/holod.toml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForHealthCheck().WithStartupTimeout(30 * time.Second),
		ConfigModifier: func(config *container.Config) {
			config.Healthcheck = &container.HealthConfig{
				Test:        []string{"CMD-SHELL", "netstat -ltn | grep -q ':50051 '"},
				Interval:    time.Second,
				Timeout:     time.Second,
				StartPeriod: 2 * time.Second,
				Retries:     15,
			}
		},
		HostConfigModifier:       addFourPeerCapabilities,
		EndpointSettingsModifier: staticFourPeerIP(networkName, holoIP),
	})
	resources.containerIDs = append(resources.containerIDs, holo.GetContainerID())

	loader := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:      fourPeerHoloImage,
		Name:       "holo-config-interop",
		Labels:     labels,
		Networks:   []string{networkName},
		Entrypoint: []string{"holo-cli"},
		Cmd: []string{
			"--address", "http://holo:50051", "--file", "/etc/holo.startup",
		},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop/holo/holo.startup"),
			ContainerFilePath: "/etc/holo.startup",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForExit().WithExitTimeout(20 * time.Second),
	})
	resources.containerIDs = append(resources.containerIDs, loader.GetContainerID())
	verifyFourPeerHoloConfiguration(ctx, t, holo, loader)

	gobfd := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    gobfdImage,
		Name:     "gobfd-interop",
		Labels:   labels,
		Networks: []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"gobfd"},
		},
		Cmd: []string{"-config", "/etc/gobfd/gobfd.yml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop/gobfd/gobfd.yml"),
			ContainerFilePath: "/etc/gobfd/gobfd.yml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForExec([]string{
			"/bin/gobfdctl", "--addr", "127.0.0.1:50051",
			"session", "list", "--format", "json",
		}).WithStartupTimeout(30 * time.Second),
		ConfigModifier: func(config *container.Config) {
			config.User = "0:0"
		},
		HostConfigModifier:       addFourPeerCapabilities,
		EndpointSettingsModifier: staticFourPeerIP(networkName, gobfdIP),
	})
	resources.containerIDs = append(resources.containerIDs, gobfd.GetContainerID())

	tshark := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:      tsharkImage,
		Name:       "tshark-interop",
		Labels:     labels,
		WaitingFor: wait.ForExec([]string{"test", "-f", "/captures/bfd.pcapng"}).WithStartupTimeout(20 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addFourPeerCapabilities(hostConfig)
			hostConfig.NetworkMode = container.NetworkMode("container:" + gobfd.GetContainerID())
		},
	})
	resources.tshark = tshark
	resources.containerIDs = append(resources.containerIDs, tshark.GetContainerID())

	frr := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    fourPeerFRRImage,
		Name:     "frr-interop",
		Labels:   labels,
		Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(root, "test/interop/frr/daemons"),
				ContainerFilePath: "/etc/frr/daemons",
				FileMode:          0o644,
			},
			{
				HostFilePath:      filepath.Join(root, "test/interop/frr/frr.conf"),
				ContainerFilePath: "/etc/frr/frr.conf",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForExec([]string{"vtysh", "-c", "show version"}).WithStartupTimeout(30 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addFourPeerCapabilities(hostConfig)
			hostConfig.CapAdd = append(hostConfig.CapAdd, "SYS_ADMIN")
		},
		EndpointSettingsModifier: staticFourPeerIP(networkName, frrIP),
	})
	resources.containerIDs = append(resources.containerIDs, frr.GetContainerID())

	bird := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    birdImage,
		Name:     "bird3-interop",
		Labels:   labels,
		Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop/bird3/bird.conf"),
			ContainerFilePath: "/etc/bird/bird.conf",
			FileMode:          0o644,
		}},
		WaitingFor:               wait.ForExec([]string{"birdc", "show", "status"}).WithStartupTimeout(30 * time.Second),
		HostConfigModifier:       addFourPeerCapabilities,
		EndpointSettingsModifier: staticFourPeerIP(networkName, bird3IP),
	})
	resources.containerIDs = append(resources.containerIDs, bird.GetContainerID())

	thoro := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    thoroImage,
		Name:     "thoro-interop",
		Labels:   labels,
		Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop/thoro/config.yaml"),
			ContainerFilePath: "/etc/bfdd/config.yaml",
			FileMode:          0o644,
		}},
		HostConfigModifier:       addFourPeerCapabilities,
		EndpointSettingsModifier: staticFourPeerIP(networkName, thoroIP),
	})
	resources.containerIDs = append(resources.containerIDs, thoro.GetContainerID())
}

func startFourPeerContainer(
	ctx context.Context,
	t *testing.T,
	request testcontainers.ContainerRequest,
) testcontainers.Container {
	t.Helper()

	testContainer, err := containertest.Run(ctx, t, request)
	if testContainer != nil {
		captureFourPeerLogsOnFailure(ctx, t, testContainer, request.Name)
	}
	if err != nil {
		t.Fatalf("start %s: %v", request.Name, err)
	}
	return testContainer
}

func addFourPeerCapabilities(hostConfig *container.HostConfig) {
	hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN")
}

func staticFourPeerIP(networkName, address string) func(map[string]*network.EndpointSettings) {
	return func(settings map[string]*network.EndpointSettings) {
		endpoint := settings[networkName]
		if endpoint == nil {
			endpoint = new(network.EndpointSettings)
			settings[networkName] = endpoint
		}
		endpoint.IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(address)}
	}
}

func verifyFourPeerHoloConfiguration(
	ctx context.Context,
	t *testing.T,
	holo, loader testcontainers.Container,
) {
	t.Helper()

	inspection, err := loader.Inspect(ctx)
	if err != nil {
		t.Fatalf("inspect Holo configuration loader: %v", err)
	}
	if inspection.State == nil || inspection.State.ExitCode != 0 {
		t.Fatalf("Holo configuration loader exit state = %+v, want exact zero", inspection.State)
	}
	logs, err := loader.Logs(ctx)
	if err != nil {
		t.Fatalf("read Holo configuration loader logs: %v", err)
	}
	loaderOutput, readErr := io.ReadAll(logs)
	closeErr := logs.Close()
	if joinedErr := errors.Join(readErr, closeErr); joinedErr != nil {
		t.Fatalf("consume Holo configuration loader logs: %v", joinedErr)
	}
	if strings.TrimSpace(string(loaderOutput)) != "" {
		t.Fatalf("Holo configuration loader produced unexpected output: %q", loaderOutput)
	}

	exitCode, output, err := containertest.Exec(ctx, holo, []string{"holo-cli", "--version"})
	if err != nil || exitCode != 0 {
		t.Fatalf("inspect Holo CLI version: exit=%d output=%q error=%v", exitCode, output, err)
	}
	if got, want := strings.TrimSpace(output), "Holo command-line interface 0.5.0"; got != want {
		t.Fatalf("Holo CLI version = %q, want %q", got, want)
	}

	exitCode, output, err = containertest.Exec(ctx, holo, []string{
		"holo-cli", "--no-colors", "--no-pager",
		"--address", "http://127.0.0.1:50051",
		"--command", "show running format json",
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("inspect Holo running configuration: exit=%d output=%q error=%v", exitCode, output, err)
	}
	if err := validateHoloRunningConfig(strings.NewReader(output)); err != nil {
		t.Fatalf("validate Holo running configuration: %v", err)
	}
}

func validateHoloRunningConfig(input io.Reader) error {
	decoder := json.NewDecoder(input)
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode Holo running configuration: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode Holo running configuration: multiple JSON documents")
		}
		return fmt.Errorf("finish Holo running configuration: %w", err)
	}

	interfaces := nestedObjects(document, "ietf-interfaces:interfaces", "interface")
	interfaceCount := 0
	for _, configuredInterface := range interfaces {
		if configuredInterface["name"] == "eth0" &&
			configuredInterface["type"] == "iana-if-type:ethernetCsmacd" {
			if _, found := configuredInterface["ietf-ip:ipv4"].(map[string]any); found {
				interfaceCount++
			}
		}
	}

	protocols := nestedObjects(
		document,
		"ietf-routing:routing",
		"control-plane-protocols",
		"control-plane-protocol",
	)
	protocolCount := 0
	sessionCount := 0
	for _, protocol := range protocols {
		if protocol["type"] != "ietf-bfd-types:bfdv1" || protocol["name"] != "main" {
			continue
		}
		protocolCount++
		for _, session := range nestedObjects(
			protocol,
			"ietf-bfd:bfd",
			"ietf-bfd-ip-sh:ip-sh",
			"sessions",
			"session",
		) {
			if session["interface"] == "eth0" &&
				session["dest-addr"] == gobfdIP &&
				session["source-addr"] == holoIP &&
				session["local-multiplier"] == float64(3) &&
				session["desired-min-tx-interval"] == float64(300000) &&
				session["required-min-rx-interval"] == float64(300000) {
				sessionCount++
			}
		}
	}
	if interfaceCount != 1 || protocolCount != 1 || sessionCount != 1 {
		return fmt.Errorf(
			"required interface/protocol/session counts = %d/%d/%d, want 1/1/1",
			interfaceCount,
			protocolCount,
			sessionCount,
		)
	}
	return nil
}

func nestedObjects(root map[string]any, path ...string) []map[string]any {
	var current any = root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[key]
		if !ok {
			return nil
		}
	}
	values, ok := current.([]any)
	if !ok {
		return nil
	}
	objects := make([]map[string]any, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if ok {
			objects = append(objects, object)
		}
	}
	return objects
}

func runFourPeerAssertions(ctx context.Context, t *testing.T, projectName string) {
	t.Helper()

	functionalTests := []string{
		"TestFRRHandshake",
		"TestBIRD3Handshake",
		"TestHoloHandshake",
		"TestThoroHandshake",
		"TestRFCCompliance",
		"TestHoloFailureRecoveryLifecycle",
		"TestFRRDetectionTimeout",
		"TestScapyFuzzing",
		"TestGracefulShutdown",
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current interop test binary: %v", err)
	}
	filter := "^(" + strings.Join(functionalTests, "|") + ")$"
	cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.count=1", "-test.run="+filter)
	cmd.Env = append(os.Environ(), "INTEROP_PROJECT_NAME="+projectName)
	output, err := cmd.CombinedOutput()
	t.Logf("four-peer assertion subprocess:\n%s", output)
	if err != nil {
		t.Fatalf("run four-peer Go assertions: %v", err)
	}
}

func captureFourPeerPCAP(ctx context.Context, t *testing.T, tshark testcontainers.Container) {
	t.Helper()

	input, err := tshark.CopyFileFromContainer(ctx, "/captures/bfd.pcapng")
	if err != nil {
		t.Fatalf("copy four-peer packet capture: %v", err)
	}
	defer input.Close()
	artifactDirectory := strings.TrimSpace(os.Getenv("INTEROP_TESTCONTAINERS_ARTIFACT_DIR"))
	if artifactDirectory == "" {
		artifactDirectory = t.TempDir()
	} else {
		if !filepath.IsAbs(artifactDirectory) {
			t.Fatalf("four-peer artifact directory %q must be absolute", artifactDirectory)
		}
		if mkdirErr := os.MkdirAll(artifactDirectory, 0o700); mkdirErr != nil {
			t.Fatalf("create four-peer artifact directory: %v", mkdirErr)
		}
	}
	outputPath := filepath.Join(
		artifactDirectory,
		fmt.Sprintf("bfd-%d.pcapng", time.Now().UnixNano()),
	)
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create four-peer packet capture artifact: %v", err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if joinedErr := errors.Join(copyErr, closeErr); joinedErr != nil {
		t.Fatalf("persist four-peer packet capture artifact: %v", joinedErr)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("inspect four-peer packet capture artifact: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("four-peer packet capture artifact is empty")
	}
	t.Logf("four-peer packet capture: %s (%d bytes)", outputPath, info.Size())
}

func captureFourPeerLogsOnFailure(
	ctx context.Context,
	t *testing.T,
	testContainer testcontainers.Container,
	name string,
) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		logs, err := testContainer.Logs(logCtx)
		if err != nil {
			t.Logf("read %s logs after failure: %v", name, err)
			return
		}
		defer logs.Close()
		output, err := io.ReadAll(io.LimitReader(logs, 1<<20))
		if err != nil {
			t.Logf("read bounded %s logs after failure: %v", name, err)
			return
		}
		t.Logf("%s logs:\n%s", name, output)
	})
}

func assertFourPeerNamesAvailable(ctx context.Context, t *testing.T, endpoint string) {
	t.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for fixed-name preflight: %v", err)
	}
	containers, err := client.Containers(ctx)
	if err != nil {
		t.Fatalf("list containers for fixed-name preflight: %v", err)
	}
	reserved := map[string]struct{}{
		"gobfd-interop": {}, "frr-interop": {}, "bird3-interop": {},
		"tshark-interop": {}, "holo-interop": {}, "holo-config-interop": {},
		"thoro-interop": {}, "scapy-interop": {},
	}
	for _, existing := range containers {
		for _, name := range existing.Names {
			name = strings.TrimPrefix(name, "/")
			if _, found := reserved[name]; found {
				t.Fatalf("fixed interop container name %s is already in use by %s", name, existing.ID)
			}
		}
	}
}

func buildFourPeerImage(
	ctx context.Context,
	t *testing.T,
	endpoint, contextPath, imageName string,
) string {
	t.Helper()

	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		t.Fatalf("create Podman provider for %s: %v", imageName, err)
	}
	dockerProvider, ok := provider.(*testcontainers.DockerProvider)
	if !ok {
		t.Fatalf("Podman provider type = %T, want *testcontainers.DockerProvider", provider)
	}
	repository, tag, found := strings.Cut(imageName, ":")
	if !found {
		t.Fatalf("split test image name %q", imageName)
	}
	//nolint:modernize // FromDockerfile alone does not implement ImageBuildInfo in v0.44.
	builtImage, buildErr := dockerProvider.BuildImage(ctx, &testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    contextPath,
			Dockerfile: "Containerfile",
			Repo:       repository,
			Tag:        tag,
			KeepImage:  true,
		},
	})
	closeErr := provider.Close()
	if joinedErr := errors.Join(buildErr, closeErr); joinedErr != nil {
		t.Fatalf("build test-owned image %s: %v", imageName, joinedErr)
	}

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for image cleanup: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		if err := client.RemoveImage(cleanupCtx, builtImage); err != nil {
			t.Errorf("remove test-owned image %s: %v", builtImage, err)
		}
	})
	return builtImage
}

func prepareFourPeerGoContext(t *testing.T, root string) string {
	t.Helper()

	contextPath := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{"cmd/gobfd", "cmd/gobfdctl", "internal", "pkg"} {
		subtree, err := fs.Sub(rootFS, sourceDir)
		if err != nil {
			t.Fatalf("open bounded build source %s: %v", sourceDir, err)
		}
		if err := os.CopyFS(filepath.Join(contextPath, sourceDir), subtree); err != nil {
			t.Fatalf("copy bounded build source %s: %v", sourceDir, err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s for bounded build context: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(contextPath, name), contents, 0o600); err != nil {
			t.Fatalf("write %s into bounded build context: %v", name, err)
		}
	}
	const containerfile = "FROM docker.io/library/golang:1.27.0-trixie@sha256:" +
		"ae28539d2ef595b9a2930dd7f031d9592376829dc0eae7cb869559f7d5812c3a AS builder\n" +
		"WORKDIR /src\n" +
		"COPY go.mod go.sum ./\n" +
		"RUN go mod download && go mod verify\n" +
		"COPY cmd/gobfd ./cmd/gobfd\n" +
		"COPY cmd/gobfdctl ./cmd/gobfdctl\n" +
		"COPY internal ./internal\n" +
		"COPY pkg ./pkg\n" +
		"RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfd ./cmd/gobfd\n" +
		"RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfdctl ./cmd/gobfdctl\n" +
		"FROM docker.io/library/debian:trixie-slim@sha256:" +
		"d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132\n" +
		"COPY --from=builder /bin/gobfd /bin/gobfdctl /bin/\n" +
		"ENTRYPOINT [\"/bin/gobfd\"]\n"
	if err := os.WriteFile(filepath.Join(contextPath, "Containerfile"), []byte(containerfile), 0o600); err != nil {
		t.Fatalf("write bounded interop Containerfile: %v", err)
	}
	return contextPath
}

func fourPeerRepositoryRoot(t *testing.T) string {
	t.Helper()

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve interop test working directory: %v", err)
	}
	root, err := filepath.Abs(filepath.Join(workingDirectory, "../.."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read repository module: %v", err)
	}
	if !strings.HasPrefix(string(module), "module github.com/dantte-lp/gobfd\n") {
		t.Fatalf("resolved repository root %s has unexpected module", root)
	}
	return root
}
