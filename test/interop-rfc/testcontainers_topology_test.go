//go:build interop_rfc_testcontainers

package interop_rfc_test

import (
	"context"
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
	rfcTestGoBGPImage = "docker.io/jauderho/gobgp:v3.37.0@sha256:" +
		"3bb7304d299c42383c738f5bde2464793e2def9c1ff7fa3f25707a5bb10aee37"
	rfcTestFRRImage = "quay.io/frrouting/frr:10.7.0@sha256:" +
		"65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68"
	rfcTestProjectLabel = "com.docker.compose.project"
)

type rfcTestResources struct {
	containerIDs []string
	containers   []testcontainers.Container
	imageNames   []string
	networkName  string
	tshark       testcontainers.Container
}

func TestRFCInteropTopologyTestcontainers(t *testing.T) {
	runRFCInteropTestcontainers(t)
}

func runRFCInteropTestcontainers(t *testing.T) {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	t.Setenv("PODMAN_HOST", endpoint)
	resources := new(rfcTestResources)

	if !t.Run("test-owned topology", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
		t.Cleanup(cancel)
		projectName := fmt.Sprintf("gobfd-interop-rfc-tc-%d", time.Now().UnixNano())
		root := rfcTestRepositoryRoot(t)

		assertRFCTestNamesAvailable(ctx, t, endpoint)
		startRFCTestTopology(ctx, t, endpoint, root, projectName, resources)
		verifyRFCTestVersions(ctx, t, resources)
		runRFCTestAssertions(ctx, t, projectName)
		captureRFCTestPCAP(ctx, t, resources.tshark)
	}) {
		t.Error("RFC interop testcontainers topology failed")
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

func startRFCTestTopology(
	ctx context.Context,
	t *testing.T,
	endpoint, root, projectName string,
	resources *rfcTestResources,
) {
	t.Helper()
	buildID := time.Now().UnixNano()
	gobfdImage := buildRFCTestImage(
		ctx, t, endpoint, prepareRFCTestGoContext(t, root),
		fmt.Sprintf("localhost/gobfd-rfc-interop-test:%d", buildID),
	)
	tsharkImage := buildRFCTestImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/tshark"),
		fmt.Sprintf("localhost/tshark-rfc-interop-test:%d", buildID),
	)
	echoImage := buildRFCTestImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop-rfc/echo-reflector"),
		fmt.Sprintf("localhost/echo-reflector-rfc-interop-test:%d", buildID),
	)
	resources.imageNames = append(resources.imageNames, gobfdImage, tsharkImage, echoImage)

	networkName := projectName + "-rfcnet"
	resources.networkName = networkName
	//nolint:staticcheck // ProviderPodman plus static IPAM requires this v0.44 API.
	_, err := containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name: networkName, Driver: "bridge", Labels: map[string]string{"io.gobfd.test": "rfc-testcontainers"},
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet:  netip.MustParsePrefix("172.22.0.0/24"),
			Gateway: netip.MustParseAddr("172.22.0.1"),
		}}},
	})
	if err != nil {
		t.Fatalf("create RFC Podman network: %v", err)
	}
	labels := map[string]string{rfcTestProjectLabel: projectName, "io.gobfd.test": "rfc-testcontainers"}

	echo := startRFCTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image: echoImage, Name: echoReflectorContainer, Labels: labels, Networks: []string{networkName},
		WaitingFor:               wait.ForLog("echo reflector listening address=:3785").WithStartupTimeout(30 * time.Second),
		EndpointSettingsModifier: staticRFCTestIP(networkName, echoReflectorIP),
	})
	addRFCTestContainer(resources, echo)

	gobfd := startRFCTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image: gobfdImage, Name: gobfdRFCContainer, Labels: labels, Networks: []string{networkName},
		Cmd: []string{"-config", "/etc/gobfd/gobfd.yml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop-rfc/gobfd-rfc/gobfd.yml"),
			ContainerFilePath: "/etc/gobfd/gobfd.yml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForExec([]string{
			"/bin/gobfdctl", "--addr", "127.0.0.1:50052", "session", "list", "--format", "json",
		}).WithStartupTimeout(30 * time.Second),
		ConfigModifier:     func(config *container.Config) { config.User = "0:0" },
		HostConfigModifier: addRFCTestCapabilities, EndpointSettingsModifier: staticRFCTestIP(networkName, gobfdRFCIP),
	})
	addRFCTestContainer(resources, gobfd)

	tshark := startRFCTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image: tsharkImage, Name: tsharkRFCContainer, Labels: labels,
		WaitingFor: wait.ForExec([]string{"test", "-f", "/captures/bfd.pcapng"}).WithStartupTimeout(20 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addRFCTestCapabilities(hostConfig)
			hostConfig.NetworkMode = container.NetworkMode("container:" + gobfd.GetContainerID())
		},
	})
	resources.tshark = tshark
	addRFCTestContainer(resources, tshark)

	for _, peer := range []struct{ name, directory, address string }{
		{name: frrRFCContainer, directory: "frr", address: frrRFCIP},
		{name: frrUnsolicitedContainer, directory: "frr-unsolicited", address: frrUnsolicitedIP},
	} {
		request := rfcFRRRequest(root, networkName, labels, peer.name, peer.directory, peer.address)
		addRFCTestContainer(resources, startRFCTestContainer(ctx, t, request))
	}

	gobfd9384 := startRFCTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image: gobfdImage, Name: gobfdRFC9384Container, Labels: labels, Networks: []string{networkName},
		Cmd: []string{"-config", "/etc/gobfd/gobfd.yml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop-rfc/gobfd-rfc9384/gobfd.yml"),
			ContainerFilePath: "/etc/gobfd/gobfd.yml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForExec([]string{
			"/bin/gobfdctl", "--addr", "127.0.0.1:50052", "session", "list", "--format", "json",
		}).WithStartupTimeout(30 * time.Second),
		ConfigModifier:     func(config *container.Config) { config.User = "0:0" },
		HostConfigModifier: addRFCTestCapabilities, EndpointSettingsModifier: staticRFCTestIP(networkName, gobfdRFC9384IP),
	})
	addRFCTestContainer(resources, gobfd9384)

	gobgp := startRFCTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image: rfcTestGoBGPImage, Name: gobgpRFCContainer, Labels: labels,
		Cmd: []string{"gobgpd", "-f", "/etc/gobgp/gobgp.toml", "-l", "info"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop-rfc/gobgp/gobgp.toml"),
			ContainerFilePath: "/etc/gobgp/gobgp.toml",
			FileMode:          0o644,
		}},
		WaitingFor:     wait.ForExec([]string{"gobgp", "neighbor", "-j"}).WithStartupTimeout(30 * time.Second),
		ConfigModifier: func(config *container.Config) { config.User = "0:0" },
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			hostConfig.NetworkMode = container.NetworkMode("container:" + gobfd9384.GetContainerID())
			hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_BIND_SERVICE")
		},
	})
	addRFCTestContainer(resources, gobgp)
	frrBGPRequest := rfcFRRRequest(root, networkName, labels, frrRFCBGPContainer, "frr-bgp", frrRFCBGPIP)
	addRFCTestContainer(resources, startRFCTestContainer(ctx, t, frrBGPRequest))
}

func rfcFRRRequest(
	root, networkName string,
	labels map[string]string,
	name, configDirectory, address string,
) testcontainers.ContainerRequest {
	return testcontainers.ContainerRequest{
		Image: rfcTestFRRImage, Name: name, Labels: labels, Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(root, "test/interop-rfc", configDirectory, "daemons"),
				ContainerFilePath: "/etc/frr/daemons",
				FileMode:          0o644,
			},
			{
				HostFilePath:      filepath.Join(root, "test/interop-rfc", configDirectory, "frr.conf"),
				ContainerFilePath: "/etc/frr/frr.conf",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForExec([]string{"vtysh", "-c", "show version"}).WithStartupTimeout(30 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addRFCTestCapabilities(hostConfig)
			hostConfig.CapAdd = append(hostConfig.CapAdd, "SYS_ADMIN")
		},
		EndpointSettingsModifier: staticRFCTestIP(networkName, address),
	}
}

func addRFCTestContainer(resources *rfcTestResources, testContainer testcontainers.Container) {
	resources.containerIDs = append(resources.containerIDs, testContainer.GetContainerID())
	resources.containers = append(resources.containers, testContainer)
}

func startRFCTestContainer(
	ctx context.Context,
	t *testing.T,
	request testcontainers.ContainerRequest,
) testcontainers.Container {
	t.Helper()
	testContainer, err := containertest.Run(ctx, t, request)
	if testContainer != nil {
		captureRFCTestLogsOnFailure(ctx, t, testContainer, request.Name)
	}
	if err != nil {
		t.Fatalf("start %s: %v", request.Name, err)
	}
	return testContainer
}

func addRFCTestCapabilities(hostConfig *container.HostConfig) {
	hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN")
}

func staticRFCTestIP(networkName, address string) func(map[string]*network.EndpointSettings) {
	return func(settings map[string]*network.EndpointSettings) {
		endpoint := settings[networkName]
		if endpoint == nil {
			endpoint = new(network.EndpointSettings)
			settings[networkName] = endpoint
		}
		endpoint.IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(address)}
	}
}

func verifyRFCTestVersions(ctx context.Context, t *testing.T, resources *rfcTestResources) {
	t.Helper()
	checks := []struct {
		name          string
		containerName string
		command       []string
		version       string
	}{
		{name: "GoBGP", containerName: gobgpRFCContainer, command: []string{"gobgp", "--version"}, version: "3.37.0"},
		{
			name: "FRR RFC 7419", containerName: frrRFCContainer,
			command: []string{"vtysh", "-c", "show version"}, version: "10.7.0",
		},
		{
			name: "FRR RFC 9468", containerName: frrUnsolicitedContainer,
			command: []string{"vtysh", "-c", "show version"}, version: "10.7.0",
		},
		{
			name: "FRR RFC 9384", containerName: frrRFCBGPContainer,
			command: []string{"vtysh", "-c", "show version"}, version: "10.7.0",
		},
	}
	for _, check := range checks {
		testContainer := findRFCTestContainer(ctx, t, resources, check.containerName)
		exitCode, output, err := containertest.Exec(ctx, testContainer, check.command)
		if err != nil || exitCode != 0 {
			t.Fatalf("inspect %s version: exit=%d output=%q error=%v", check.name, exitCode, output, err)
		}
		if !strings.Contains(output, check.version) {
			t.Fatalf("%s version output %q does not contain %q", check.name, output, check.version)
		}
	}
}

func findRFCTestContainer(
	ctx context.Context,
	t *testing.T,
	resources *rfcTestResources,
	name string,
) testcontainers.Container {
	t.Helper()
	for _, testContainer := range resources.containers {
		inspection, err := testContainer.Inspect(ctx)
		if err != nil {
			t.Fatalf("inspect RFC test container while resolving %s: %v", name, err)
		}
		if strings.TrimPrefix(inspection.Name, "/") == name {
			return testContainer
		}
	}
	t.Fatalf("RFC test container %s not found", name)
	return nil
}

func runRFCTestAssertions(ctx context.Context, t *testing.T, projectName string) {
	t.Helper()
	functionalTests := []string{
		"TestRFC7419_CommonIntervalAlignment",
		"TestRFC9384_BGPCeaseBFDDown",
		"TestRFC9468_UnsolicitedBFD",
		"TestRFC9747_EchoSession",
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current RFC interop test binary: %v", err)
	}
	filter := "^(" + strings.Join(functionalTests, "|") + ")$"
	cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.count=1", "-test.run="+filter)
	cmd.Env = append(os.Environ(), "INTEROP_PROJECT_NAME="+projectName)
	output, err := cmd.CombinedOutput()
	t.Logf("RFC assertion subprocess:\n%s", output)
	if err != nil {
		t.Fatalf("run RFC Go assertions: %v", err)
	}
}

func captureRFCTestPCAP(ctx context.Context, t *testing.T, tshark testcontainers.Container) {
	t.Helper()
	input, err := tshark.CopyFileFromContainer(ctx, "/captures/bfd.pcapng")
	if err != nil {
		t.Fatalf("copy RFC packet capture: %v", err)
	}
	defer input.Close()
	artifactDirectory := strings.TrimSpace(os.Getenv("INTEROP_RFC_TESTCONTAINERS_ARTIFACT_DIR"))
	if artifactDirectory == "" {
		artifactDirectory = t.TempDir()
	} else if !filepath.IsAbs(artifactDirectory) {
		t.Fatalf("RFC artifact directory %q must be absolute", artifactDirectory)
	} else if mkdirErr := os.MkdirAll(artifactDirectory, 0o700); mkdirErr != nil {
		t.Fatalf("create RFC artifact directory: %v", mkdirErr)
	}
	outputPath := filepath.Join(artifactDirectory, fmt.Sprintf("bfd-rfc-%d.pcapng", time.Now().UnixNano()))
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create RFC packet capture artifact: %v", err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if joinedErr := errors.Join(copyErr, closeErr); joinedErr != nil {
		t.Fatalf("persist RFC packet capture artifact: %v", joinedErr)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("inspect RFC packet capture artifact: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("RFC packet capture artifact is empty")
	}
	t.Logf("RFC packet capture: %s (%d bytes)", outputPath, info.Size())
}

func captureRFCTestLogsOnFailure(
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

func assertRFCTestNamesAvailable(ctx context.Context, t *testing.T, endpoint string) {
	t.Helper()
	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for RFC fixed-name preflight: %v", err)
	}
	containers, err := client.Containers(ctx)
	if err != nil {
		t.Fatalf("list containers for RFC fixed-name preflight: %v", err)
	}
	reserved := map[string]struct{}{
		gobfdRFCContainer: {}, tsharkRFCContainer: {}, frrRFCContainer: {}, gobfdRFC9384Container: {},
		gobgpRFCContainer: {}, frrRFCBGPContainer: {}, frrUnsolicitedContainer: {}, echoReflectorContainer: {},
	}
	for _, existing := range containers {
		for _, name := range existing.Names {
			name = strings.TrimPrefix(name, "/")
			if _, found := reserved[name]; found {
				t.Fatalf("fixed RFC interop container name %s is already in use by %s", name, existing.ID)
			}
		}
	}
}

func buildRFCTestImage(ctx context.Context, t *testing.T, endpoint, contextPath, imageName string) string {
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
			Context: contextPath, Dockerfile: "Containerfile", Repo: repository, Tag: tag, KeepImage: true,
		},
	})
	closeErr := provider.Close()
	if joinedErr := errors.Join(buildErr, closeErr); joinedErr != nil {
		t.Fatalf("build test-owned image %s: %v", imageName, joinedErr)
	}
	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for RFC image cleanup: %v", err)
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

func prepareRFCTestGoContext(t *testing.T, root string) string {
	t.Helper()
	contextPath := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{"cmd/gobfd", "cmd/gobfdctl", "internal", "pkg"} {
		subtree, err := fs.Sub(rootFS, sourceDir)
		if err != nil {
			t.Fatalf("open bounded RFC build source %s: %v", sourceDir, err)
		}
		if err := os.CopyFS(filepath.Join(contextPath, sourceDir), subtree); err != nil {
			t.Fatalf("copy bounded RFC build source %s: %v", sourceDir, err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s for bounded RFC build context: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(contextPath, name), contents, 0o600); err != nil {
			t.Fatalf("write %s into bounded RFC build context: %v", name, err)
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
		t.Fatalf("write bounded RFC interop Containerfile: %v", err)
	}
	return contextPath
}

func rfcTestRepositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve RFC interop test working directory: %v", err)
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
