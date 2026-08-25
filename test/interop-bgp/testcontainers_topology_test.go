//go:build interop_bgp_testcontainers

package interop_bgp_test

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
	bgpTestGoBGPImage = "docker.io/jauderho/gobgp:v3.37.0@sha256:" +
		"3bb7304d299c42383c738f5bde2464793e2def9c1ff7fa3f25707a5bb10aee37"
	bgpTestFRRImage = "quay.io/frrouting/frr:10.7.0@sha256:" +
		"65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68"
	bgpTestExaBGPImage = "ghcr.io/exa-networks/exabgp:5.0.13@sha256:" +
		"80f64719841fe6192f5b5a3b46edc27270215521438fae8a704f28d221a4680b"
	bgpTestProjectLabel = "com.docker.compose.project"
)

type bgpTestResources struct {
	containerIDs []string
	containers   []testcontainers.Container
	imageNames   []string
	networkName  string
	tshark       testcontainers.Container
}

func TestBGPBFDTopologyTestcontainers(t *testing.T) {
	runBGPBFDTestcontainers(t)
}

func runBGPBFDTestcontainers(t *testing.T) {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	t.Setenv("PODMAN_HOST", endpoint)
	resources := new(bgpTestResources)

	if !t.Run("test-owned topology", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
		t.Cleanup(cancel)
		projectName := fmt.Sprintf("gobfd-interop-bgp-tc-%d", time.Now().UnixNano())
		root := bgpTestRepositoryRoot(t)

		assertBGPTestNamesAvailable(ctx, t, endpoint)
		startBGPTestTopology(ctx, t, endpoint, root, projectName, resources)
		verifyBGPTestVersions(ctx, t, resources)
		runBGPTestAssertions(ctx, t, projectName)
		captureBGPTestPCAP(ctx, t, resources.tshark)
	}) {
		t.Error("BGP+BFD testcontainers topology failed")
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

func startBGPTestTopology(
	ctx context.Context,
	t *testing.T,
	endpoint, root, projectName string,
	resources *bgpTestResources,
) {
	t.Helper()

	buildID := time.Now().UnixNano()
	gobfdImage := buildBGPTestImage(
		ctx, t, endpoint, prepareBGPTestGoContext(t, root),
		fmt.Sprintf("localhost/gobfd-bgp-interop-test:%d", buildID),
	)
	birdImage := buildBGPTestImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/bird3"),
		fmt.Sprintf("localhost/bird3-bgp-interop-test:%d", buildID),
	)
	tsharkImage := buildBGPTestImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/tshark"),
		fmt.Sprintf("localhost/tshark-bgp-interop-test:%d", buildID),
	)
	resources.imageNames = append(resources.imageNames, gobfdImage, birdImage, tsharkImage)

	networkName := projectName + "-bgpbfdnet"
	resources.networkName = networkName
	//nolint:staticcheck // ProviderPodman plus static IPAM requires this v0.44 API.
	_, err := containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name:   networkName,
		Driver: "bridge",
		Labels: map[string]string{"io.gobfd.test": "bgp-testcontainers"},
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet:  netip.MustParsePrefix("172.21.0.0/24"),
			Gateway: netip.MustParseAddr("172.21.0.1"),
		}}},
	})
	if err != nil {
		t.Fatalf("create BGP+BFD Podman network: %v", err)
	}

	labels := map[string]string{
		bgpTestProjectLabel: projectName,
		"io.gobfd.test":     "bgp-testcontainers",
	}
	gobfdBGP := startBGPTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    gobfdImage,
		Name:     gobfdBGPContainer,
		Labels:   labels,
		Networks: []string{networkName},
		Cmd:      []string{"-config", "/etc/gobfd/gobfd.yml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop-bgp/gobfd-bgp/gobfd.yml"),
			ContainerFilePath: "/etc/gobfd/gobfd.yml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForExec([]string{
			"/bin/gobfdctl", "--addr", "127.0.0.1:50052",
			"session", "list", "--format", "json",
		}).WithStartupTimeout(30 * time.Second),
		ConfigModifier: func(config *container.Config) {
			config.User = "0:0"
		},
		HostConfigModifier:       addBGPTestCapabilities,
		EndpointSettingsModifier: staticBGPTestIP(networkName, gobfdBGPIP),
	})
	resources.containerIDs = append(resources.containerIDs, gobfdBGP.GetContainerID())
	resources.containers = append(resources.containers, gobfdBGP)

	gobgp := startBGPTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image:  bgpTestGoBGPImage,
		Name:   gobgpContainer,
		Labels: labels,
		Cmd:    []string{"gobgpd", "-f", "/etc/gobgp/gobgp.toml", "-l", "info"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop-bgp/gobgp/gobgp.toml"),
			ContainerFilePath: "/etc/gobgp/gobgp.toml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForExec([]string{"gobgp", "neighbor", "-j"}).WithStartupTimeout(30 * time.Second),
		ConfigModifier: func(config *container.Config) {
			config.User = "0:0"
		},
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			hostConfig.NetworkMode = container.NetworkMode("container:" + gobfdBGP.GetContainerID())
			hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_BIND_SERVICE")
		},
	})
	resources.containerIDs = append(resources.containerIDs, gobgp.GetContainerID())
	resources.containers = append(resources.containers, gobgp)

	tshark := startBGPTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image:      tsharkImage,
		Name:       "tshark-bgp-interop",
		Labels:     labels,
		WaitingFor: wait.ForExec([]string{"test", "-f", "/captures/bfd.pcapng"}).WithStartupTimeout(20 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addBGPTestCapabilities(hostConfig)
			hostConfig.NetworkMode = container.NetworkMode("container:" + gobfdBGP.GetContainerID())
		},
	})
	resources.tshark = tshark
	resources.containerIDs = append(resources.containerIDs, tshark.GetContainerID())
	resources.containers = append(resources.containers, tshark)

	frr := startBGPTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    bgpTestFRRImage,
		Name:     frrContainer,
		Labels:   labels,
		Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(root, "test/interop-bgp/frr/daemons"),
				ContainerFilePath: "/etc/frr/daemons",
				FileMode:          0o644,
			},
			{
				HostFilePath:      filepath.Join(root, "test/interop-bgp/frr/frr.conf"),
				ContainerFilePath: "/etc/frr/frr.conf",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForExec([]string{"vtysh", "-c", "show version"}).WithStartupTimeout(30 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addBGPTestCapabilities(hostConfig)
			hostConfig.CapAdd = append(hostConfig.CapAdd, "SYS_ADMIN")
		},
		EndpointSettingsModifier: staticBGPTestIP(networkName, frrBGPIP),
	})
	resources.containerIDs = append(resources.containerIDs, frr.GetContainerID())
	resources.containers = append(resources.containers, frr)

	bird := startBGPTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    birdImage,
		Name:     bird3Container,
		Labels:   labels,
		Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop-bgp/bird3/bird.conf"),
			ContainerFilePath: "/etc/bird/bird.conf",
			FileMode:          0o644,
		}},
		WaitingFor:               wait.ForExec([]string{"birdc", "show", "status"}).WithStartupTimeout(30 * time.Second),
		HostConfigModifier:       addBGPTestCapabilities,
		EndpointSettingsModifier: staticBGPTestIP(networkName, bird3BGPIP),
	})
	resources.containerIDs = append(resources.containerIDs, bird.GetContainerID())
	resources.containers = append(resources.containers, bird)

	gobfdExaBGP := startBGPTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    gobfdImage,
		Name:     gobfdExaBGPContainer,
		Labels:   labels,
		Networks: []string{networkName},
		Cmd:      []string{"-config", "/etc/gobfd/gobfd.yml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop-bgp/gobfd-exabgp/gobfd.yml"),
			ContainerFilePath: "/etc/gobfd/gobfd.yml",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForExec([]string{
			"/bin/gobfdctl", "--addr", "127.0.0.1:50052",
			"session", "list", "--format", "json",
		}).WithStartupTimeout(30 * time.Second),
		ConfigModifier: func(config *container.Config) {
			config.User = "0:0"
		},
		HostConfigModifier:       addBGPTestCapabilities,
		EndpointSettingsModifier: staticBGPTestIP(networkName, gobfdExaBGPIP),
	})
	resources.containerIDs = append(resources.containerIDs, gobfdExaBGP.GetContainerID())
	resources.containers = append(resources.containers, gobfdExaBGP)

	exabgp := startBGPTestContainer(ctx, t, testcontainers.ContainerRequest{
		Image:  bgpTestExaBGPImage,
		Name:   "exabgp-interop",
		Labels: labels,
		Cmd:    []string{"server", "/etc/exabgp/exabgp.conf"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop-bgp/exabgp/exabgp.conf"),
			ContainerFilePath: "/etc/exabgp/exabgp.conf",
			FileMode:          0o644,
		}},
		Env: map[string]string{
			"exabgp.daemon.daemonize": "false",
			"exabgp.log.all":          "true",
			"exabgp.log.level":        "INFO",
			"exabgp.log.destination":  "stdout",
			"exabgp.api.ack":          "false",
			"exabgp.api.cli":          "false",
		},
		WaitingFor: wait.ForLog("connected to peer-1").WithStartupTimeout(30 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			hostConfig.NetworkMode = container.NetworkMode("container:" + gobfdExaBGP.GetContainerID())
		},
	})
	resources.containerIDs = append(resources.containerIDs, exabgp.GetContainerID())
	resources.containers = append(resources.containers, exabgp)
}

func startBGPTestContainer(
	ctx context.Context,
	t *testing.T,
	request testcontainers.ContainerRequest,
) testcontainers.Container {
	t.Helper()

	testContainer, err := containertest.Run(ctx, t, request)
	if testContainer != nil {
		captureBGPTestLogsOnFailure(ctx, t, testContainer, request.Name)
	}
	if err != nil {
		t.Fatalf("start %s: %v", request.Name, err)
	}
	return testContainer
}

func addBGPTestCapabilities(hostConfig *container.HostConfig) {
	hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN")
}

func staticBGPTestIP(networkName, address string) func(map[string]*network.EndpointSettings) {
	return func(settings map[string]*network.EndpointSettings) {
		endpoint := settings[networkName]
		if endpoint == nil {
			endpoint = new(network.EndpointSettings)
			settings[networkName] = endpoint
		}
		endpoint.IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(address)}
	}
}

func verifyBGPTestVersions(ctx context.Context, t *testing.T, resources *bgpTestResources) {
	t.Helper()

	checks := []struct {
		name      string
		container testcontainers.Container
		command   []string
		version   string
	}{
		{
			name: "GoBGP", container: findBGPTestContainer(ctx, t, resources, gobgpContainer),
			command: []string{"gobgp", "--version"}, version: "3.37.0",
		},
		{
			name: "FRR", container: findBGPTestContainer(ctx, t, resources, frrContainer),
			command: []string{"vtysh", "-c", "show version"}, version: "10.7.0",
		},
		{
			name: "BIRD", container: findBGPTestContainer(ctx, t, resources, bird3Container),
			command: []string{"birdc", "show", "status"}, version: "3.3.2",
		},
		{
			name: "ExaBGP", container: findBGPTestContainer(ctx, t, resources, "exabgp-interop"),
			command: []string{"exabgp", "version"}, version: "5.0.13",
		},
	}
	for _, check := range checks {
		exitCode, output, err := containertest.Exec(ctx, check.container, check.command)
		if err != nil || exitCode != 0 {
			t.Fatalf("inspect %s version: exit=%d output=%q error=%v", check.name, exitCode, output, err)
		}
		if !strings.Contains(output, check.version) {
			t.Fatalf("%s version output %q does not contain %q", check.name, output, check.version)
		}
	}
}

func findBGPTestContainer(
	ctx context.Context,
	t *testing.T,
	resources *bgpTestResources,
	name string,
) testcontainers.Container {
	t.Helper()

	for _, testContainer := range resources.containers {
		inspection, err := testContainer.Inspect(ctx)
		if err != nil {
			t.Fatalf("inspect BGP test container while resolving %s: %v", name, err)
		}
		if strings.TrimPrefix(inspection.Name, "/") == name {
			return testContainer
		}
	}
	t.Fatalf("BGP test container %s not found", name)
	return nil
}

func runBGPTestAssertions(ctx context.Context, t *testing.T, projectName string) {
	t.Helper()

	functionalTests := []string{
		"TestBGPBFD_AllPeersUp",
		"TestBGPBFD_FRR",
		"TestBGPBFD_BIRD3",
		"TestBGPBFD_ExaBGP",
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current BGP interop test binary: %v", err)
	}
	filter := "^(" + strings.Join(functionalTests, "|") + ")$"
	cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.count=1", "-test.run="+filter)
	cmd.Env = append(os.Environ(), "INTEROP_PROJECT_NAME="+projectName)
	output, err := cmd.CombinedOutput()
	t.Logf("BGP+BFD assertion subprocess:\n%s", output)
	if err != nil {
		t.Fatalf("run BGP+BFD Go assertions: %v", err)
	}
}

func captureBGPTestPCAP(ctx context.Context, t *testing.T, tshark testcontainers.Container) {
	t.Helper()

	input, err := tshark.CopyFileFromContainer(ctx, "/captures/bfd.pcapng")
	if err != nil {
		t.Fatalf("copy BGP+BFD packet capture: %v", err)
	}
	defer input.Close()
	artifactDirectory := strings.TrimSpace(os.Getenv("INTEROP_BGP_TESTCONTAINERS_ARTIFACT_DIR"))
	if artifactDirectory == "" {
		artifactDirectory = t.TempDir()
	} else {
		if !filepath.IsAbs(artifactDirectory) {
			t.Fatalf("BGP+BFD artifact directory %q must be absolute", artifactDirectory)
		}
		if mkdirErr := os.MkdirAll(artifactDirectory, 0o700); mkdirErr != nil {
			t.Fatalf("create BGP+BFD artifact directory: %v", mkdirErr)
		}
	}
	outputPath := filepath.Join(
		artifactDirectory,
		fmt.Sprintf("bfd-bgp-%d.pcapng", time.Now().UnixNano()),
	)
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create BGP+BFD packet capture artifact: %v", err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if joinedErr := errors.Join(copyErr, closeErr); joinedErr != nil {
		t.Fatalf("persist BGP+BFD packet capture artifact: %v", joinedErr)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("inspect BGP+BFD packet capture artifact: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("BGP+BFD packet capture artifact is empty")
	}
	t.Logf("BGP+BFD packet capture: %s (%d bytes)", outputPath, info.Size())
}

func captureBGPTestLogsOnFailure(
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

func assertBGPTestNamesAvailable(ctx context.Context, t *testing.T, endpoint string) {
	t.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for BGP fixed-name preflight: %v", err)
	}
	containers, err := client.Containers(ctx)
	if err != nil {
		t.Fatalf("list containers for BGP fixed-name preflight: %v", err)
	}
	reserved := map[string]struct{}{
		gobfdBGPContainer: {}, gobgpContainer: {}, frrContainer: {}, bird3Container: {},
		gobfdExaBGPContainer: {}, "exabgp-interop": {}, "tshark-bgp-interop": {},
	}
	for _, existing := range containers {
		for _, name := range existing.Names {
			name = strings.TrimPrefix(name, "/")
			if _, found := reserved[name]; found {
				t.Fatalf("fixed BGP interop container name %s is already in use by %s", name, existing.ID)
			}
		}
	}
}

func buildBGPTestImage(
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
		t.Fatalf("create Podman client for BGP image cleanup: %v", err)
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

func prepareBGPTestGoContext(t *testing.T, root string) string {
	t.Helper()

	contextPath := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{"cmd/gobfd", "cmd/gobfdctl", "internal", "pkg"} {
		subtree, err := fs.Sub(rootFS, sourceDir)
		if err != nil {
			t.Fatalf("open bounded BGP build source %s: %v", sourceDir, err)
		}
		if err := os.CopyFS(filepath.Join(contextPath, sourceDir), subtree); err != nil {
			t.Fatalf("copy bounded BGP build source %s: %v", sourceDir, err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s for bounded BGP build context: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(contextPath, name), contents, 0o600); err != nil {
			t.Fatalf("write %s into bounded BGP build context: %v", name, err)
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
		t.Fatalf("write bounded BGP interop Containerfile: %v", err)
	}
	return contextPath
}

func bgpTestRepositoryRoot(t *testing.T) string {
	t.Helper()

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve BGP interop test working directory: %v", err)
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
