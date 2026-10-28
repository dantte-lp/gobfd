//go:build interop_testcontainers

package interop_test

import (
	"context"
	"crypto/sha256"
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
	"github.com/moby/moby/client"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/interopproject"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

const (
	fourPeerHoloImage = "ghcr.io/holo-routing/holo-bundle@sha256:" +
		"5c1f61475b1623b3eab611921f8319fb0a10492ced3f7da05e656418abb5ca4a"
	fourPeerProjectLabel = "com.docker.compose.project"
)

type fourPeerResources struct {
	containerIDs []string
	imageNames   []string
	networkName  string
	tshark       testcontainers.Container
}

func TestFourPeerTopologyTestcontainers(t *testing.T) {
	runFourPeerTopology(t, fourPeerDefault)
}

func runFourPeerTopology(t *testing.T, profile fourPeerProfile) {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	resources := new(fourPeerResources)

	if !t.Run("test-owned topology", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
		t.Cleanup(cancel)
		projectName := fmt.Sprintf("gobfd-interop-tc-%d", time.Now().UnixNano())
		root := fourPeerRepositoryRoot(t)
		baseline, bird, err := loadFourPeerConfig(root, profile)
		if err != nil {
			t.Fatal(err)
		}
		ensureFourPeerArtifactDirectory(t)
		configPath := writeTimerArtifact(t, "startup-gobfd", "yml", baseline)
		birdPath := writeTimerArtifact(t, "startup-bird", "conf", bird)

		assertFourPeerNamesAvailable(ctx, t, endpoint)
		registerFourPeerImageCleanup(ctx, t, endpoint, bfdFuzzImage, resources)
		startFourPeerTopology(ctx, t, endpoint, root, projectName, resources, configPath, birdPath)
		t.Setenv("INTEROP_PROJECT_NAME", projectName)
		// Register after all containers: artifacts survive assertion failures and precede exact-ID cleanup.
		registerFourPeerArtifactCleanup(ctx, t, func(artifactCtx context.Context) {
			logs, err := projectContainerLogs(artifactCtx, "gobfd-interop")
			if err != nil {
				t.Errorf("retain four-peer daemon logs: %v", err)
			} else {
				writeTimerArtifact(t, "daemon", "jsonl", []byte(logs))
			}
		}, func(artifactCtx context.Context) {
			captureFourPeerPCAP(artifactCtx, t, resources.tshark)
		})
		runFourPeerAssertions(ctx, t, projectName, profile)
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

func ensureFourPeerArtifactDirectory(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("INTEROP_TESTCONTAINERS_ARTIFACT_DIR")) == "" {
		//nolint:usetesting // Retained artifacts must outlive t.TempDir cleanup for post-run inspection.
		artifactDirectory, err := os.MkdirTemp("", "gobfd-interop-artifacts-")
		if err != nil {
			t.Fatalf("create retained four-peer artifact directory: %v", err)
		}
		t.Setenv("INTEROP_TESTCONTAINERS_ARTIFACT_DIR", artifactDirectory)
	}
	t.Logf("retained four-peer artifacts: %s", os.Getenv("INTEROP_TESTCONTAINERS_ARTIFACT_DIR"))
}

func registerFourPeerArtifactCleanup(ctx context.Context, t *testing.T, daemon, pcap func(context.Context)) {
	t.Helper()
	// Independent callbacks keep a fatal JSON write from skipping the PCAP attempt.
	for _, collect := range []func(context.Context){pcap, daemon} {
		t.Cleanup(func() {
			artifactCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			collect(artifactCtx)
		})
	}
}

func registerFourPeerImageCleanup(
	ctx context.Context,
	t *testing.T,
	endpoint, imageName string,
	resources *fourPeerResources,
) *podmanapi.Client {
	t.Helper()

	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create Podman client for image ownership: %v", err)
	}
	exists, err := client.ImageExists(ctx, imageName)
	if err != nil {
		t.Fatalf("inspect image %s before test: %v", imageName, err)
	}
	if exists {
		t.Fatalf("image %s already exists; refusing ambiguous ownership", imageName)
	}
	resources.imageNames = append(resources.imageNames, imageName)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		exists, err := client.ImageExists(cleanupCtx, imageName)
		if err != nil {
			t.Errorf("inspect test-owned image %s during cleanup: %v", imageName, err)
			return
		}
		if exists {
			if err := client.RemoveImage(cleanupCtx, imageName); err != nil {
				t.Errorf("remove test-owned image %s: %v", imageName, err)
			}
		}
	})
	return client
}

func startFourPeerTopology(
	ctx context.Context,
	t *testing.T,
	endpoint, root, projectName string,
	resources *fourPeerResources,
	configPath, birdPath string,
) {
	t.Helper()

	buildID := time.Now().UnixNano()
	frrImage := buildFourPeerImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/frr"),
		fmt.Sprintf("localhost/frr-interop-test:%d", buildID), resources,
	)
	gobfdImage := buildFourPeerImage(
		ctx, t, endpoint, prepareFourPeerGoContext(t, root),
		fmt.Sprintf("localhost/gobfd-interop-test:%d", buildID), resources,
	)
	birdImage := buildFourPeerImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/bird3"),
		fmt.Sprintf("localhost/bird3-interop-test:%d", buildID), resources,
	)
	thoroImage := buildFourPeerImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/thoro"),
		fmt.Sprintf("localhost/thoro-interop-test:%d", buildID), resources,
	)
	tsharkImage := buildFourPeerImage(
		ctx, t, endpoint, filepath.Join(root, "test/interop/tshark"),
		fmt.Sprintf("localhost/tshark-interop-test:%d", buildID), resources,
	)

	networkName, labels := startFourPeerNetwork(ctx, t, projectName, "four-peer-testcontainers", resources)
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
		WaitingFor: wait.ForExec([]string{"sh", "-c", "netstat -ltn | grep -q ':50051 '"}).
			WithStartupTimeout(30 * time.Second),
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

	gobfd := startFourPeerGoBFD(ctx, t, gobfdImage, configPath,
		networkName, labels, resources)
	resources.tshark = startFourPeerTshark(ctx, t, tsharkImage, gobfd, labels, resources)

	startFourPeerFRR(ctx, t, root, frrImage, networkName, labels, resources)

	bird := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    birdImage,
		Name:     "bird3-interop",
		Labels:   labels,
		Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      birdPath,
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

// startFourPeerNetwork creates the static 172.20.0.0/24 test network and
// returns its name and the container ownership labels.
func startFourPeerNetwork(
	ctx context.Context, t *testing.T, projectName, purpose string, resources *fourPeerResources,
) (string, map[string]string) {
	t.Helper()

	networkName := interopNetworkName(projectName)
	resources.networkName = networkName
	//nolint:staticcheck // ProviderPodman plus static IPAM requires this v0.44 API.
	_, err := containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name:   networkName,
		Driver: "bridge",
		Labels: map[string]string{"io.gobfd.test": purpose},
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet:  netip.MustParsePrefix("172.20.0.0/24"),
			Gateway: netip.MustParseAddr("172.20.0.1"),
		}}},
	})
	if err != nil {
		t.Fatalf("create %s Podman network: %v", purpose, err)
	}
	return networkName, map[string]string{fourPeerProjectLabel: projectName, "io.gobfd.test": purpose}
}

func startFourPeerFRR(
	ctx context.Context, t *testing.T, root, image, networkName string,
	labels map[string]string, resources *fourPeerResources,
) {
	t.Helper()
	frr := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image: image, Name: "frr-interop", Labels: labels, Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      filepath.Join(root, "test/interop/frr/daemons"),
			ContainerFilePath: "/etc/frr/daemons", FileMode: 0o644,
		}, {
			HostFilePath:      filepath.Join(root, "test/interop/frr/frr.conf"),
			ContainerFilePath: "/etc/frr/frr.conf", FileMode: 0o644,
		}},
		WaitingFor: wait.ForExec([]string{"vtysh", "-c", "show version"}).WithStartupTimeout(30 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addFourPeerCapabilities(hostConfig)
			hostConfig.CapAdd = append(hostConfig.CapAdd, "SYS_ADMIN")
		},
		EndpointSettingsModifier: staticFourPeerIP(networkName, frrIP),
	})
	resources.containerIDs = append(resources.containerIDs, frr.GetContainerID())
}

func startFourPeerGoBFD(
	ctx context.Context,
	t *testing.T,
	image, configPath, networkName string,
	labels map[string]string,
	resources *fourPeerResources,
) testcontainers.Container {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve existing interop executable: %v", err)
	}

	gobfd := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:    image,
		Name:     "gobfd-interop",
		Labels:   labels,
		Networks: []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"gobfd"},
		},
		Cmd: []string{"-config", "/etc/gobfd/gobfd.yml"},
		Env: map[string]string{"GOBFD_LOG_FORMAT": "json"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      configPath,
			ContainerFilePath: "/etc/gobfd/gobfd.yml",
			FileMode:          0o644,
		}, {
			HostFilePath:      executable,
			ContainerFilePath: "/tmp/gobfd-interop.test",
			FileMode:          0o755,
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
	return gobfd
}

// startFourPeerTshark captures BFD traffic inside the GoBFD network namespace.
func startFourPeerTshark(
	ctx context.Context,
	t *testing.T,
	image string,
	gobfd testcontainers.Container,
	labels map[string]string,
	resources *fourPeerResources,
) testcontainers.Container {
	t.Helper()

	tshark := startFourPeerContainer(ctx, t, testcontainers.ContainerRequest{
		Image:      image,
		Name:       "tshark-interop",
		Labels:     labels,
		WaitingFor: wait.ForExec([]string{"test", "-f", "/captures/bfd.pcapng"}).WithStartupTimeout(20 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addFourPeerCapabilities(hostConfig)
			hostConfig.NetworkMode = container.NetworkMode("container:" + gobfd.GetContainerID())
		},
	})
	resources.containerIDs = append(resources.containerIDs, tshark.GetContainerID())
	return tshark
}

func startFourPeerContainer(
	ctx context.Context,
	t *testing.T,
	request testcontainers.ContainerRequest,
) testcontainers.Container {
	t.Helper()

	modifier := request.HostConfigModifier
	request.HostConfigModifier = func(hostConfig *container.HostConfig) {
		if modifier != nil {
			modifier(hostConfig)
		}
		hostConfig.NanoCPUs = 1_000_000_000
		hostConfig.Memory = 256 << 20
		if request.Name == "holo-interop" {
			hostConfig.Memory = 512 << 20
		}
		hostConfig.MemorySwap = hostConfig.Memory
		hostConfig.PidsLimit = new(int64(128))
	}
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

func runFourPeerAssertions(ctx context.Context, t *testing.T, projectName string, profile fourPeerProfile) {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current interop test binary: %v", err)
	}
	err = runFourPeerAssertionBatches(ctx, executable, projectName, profile,
		func(index int, output []byte, exitCode int, runErr error) {
			writeTimerArtifact(t, fmt.Sprintf("assertions-%d", index+1), "txt",
				fmt.Appendf(output, "\nexit_code=%d error=%v\n", exitCode, runErr))
			t.Logf("four-peer assertion subprocess %d:\n%s", index+1, output)
		})
	if err != nil {
		t.Fatalf("run four-peer Go assertions: %v", err)
	}
}

func runFourPeerAssertionBatches(
	ctx context.Context, executable, projectName string, profile fourPeerProfile, record func(int, []byte, int, error),
) error {
	if profile != fourPeerDefault && profile != fourPeerMeticulous {
		return fmt.Errorf("unknown assertion profile %d: %w", profile, errTimerEvidence)
	}
	// Separate processes order RFC baseline analysis before timer mutation and destructive tests.
	batches := [][]string{
		{"TestFRRHandshake", "TestBIRD3Handshake", "TestHoloHandshake", "TestThoroHandshake", "TestRFCCompliance"},
		{"TestTimerReload"},
		{"TestHoloFailureRecoveryLifecycle", "TestFRRDetectionTimeout", "TestBFDInvalidVectors", "TestGracefulShutdown"},
	}
	if profile == fourPeerMeticulous {
		batches[0][4], batches[1][0] = "TestMeticulousRFCCompliance", "TestMeticulousTimerReload"
	}
	for index, names := range batches {
		filter := "^(" + strings.Join(names, "|") + ")$"
		cmd := exec.CommandContext(ctx, executable, "-test.v", "-test.count=1", "-test.run="+filter)
		cmd.Env = append(os.Environ(), "INTEROP_PROJECT_NAME="+projectName)
		output, runErr := cmd.CombinedOutput()
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		record(index, output, exitCode, runErr)
		if runErr != nil {
			return fmt.Errorf("assertion batch %d: %w", index+1, runErr)
		}
	}
	return nil
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
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hash), input)
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
	checksum := fmt.Sprintf("%x  %s\n", hash.Sum(nil), filepath.Base(outputPath))
	writeTimerArtifact(t, "pcap-checksum", "txt", []byte(checksum))
	if retainErr := copyTimerConfig(ctx, "tshark-interop", outputPath, "/captures/retained.pcapng"); retainErr != nil {
		t.Errorf("copy saved capture for readability validation: %v", retainErr)
		return
	}
	validation, err := tsharkQueryFile(ctx, "/captures/retained.pcapng", "-Y", "bfd", "-T", "fields", "-e", "frame.number")
	writeTimerArtifact(t, "pcap-readback", "txt", []byte(validation))
	if err != nil || strings.TrimSpace(validation) == "" {
		t.Errorf("saved capture readability: error=%v output=%q", err, validation)
	}
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
	resources *fourPeerResources,
) string {
	t.Helper()

	imageClient := registerFourPeerImageCleanup(ctx, t, endpoint, imageName, resources)
	ensureFourPeerArtifactDirectory(t)
	root := fourPeerRepositoryRoot(t)
	revision, buildDate, err := interopproject.BuildMetadata(ctx, root, os.Getenv("GOBFD_BUILD_REVISION"))
	if err != nil {
		t.Fatalf("resolve image build metadata: %v", err)
	}
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
			BuildArgs:  map[string]*string{"VCS_REF": &revision, "BUILD_DATE": &buildDate},
			BuildOptionsModifier: func(options *client.ImageBuildOptions) {
				options.CPUPeriod = 100000
				options.CPUQuota = 200000
				options.Memory = 2 << 30
				options.MemorySwap = 2 << 30
			},
		},
	})
	closeErr := provider.Close()
	if joinedErr := errors.Join(buildErr, closeErr); joinedErr != nil {
		t.Fatalf("build test-owned image %s: %v", imageName, joinedErr)
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	metadata, inspectErr := imageClient.ImageMetadata(inspectCtx, builtImage)
	var labels map[string]string
	if inspectErr == nil {
		labels, inspectErr = validateFourPeerImageMetadata(metadata, revision)
	}
	record := struct {
		Requested string            `json:"requested"`
		Built     string            `json:"built"`
		ID        string            `json:"id"`
		Labels    map[string]string `json:"labels"`
		Error     string            `json:"error,omitempty"`
	}{Requested: imageName, Built: builtImage, ID: metadata.ID, Labels: labels}
	if inspectErr != nil {
		record.Error = inspectErr.Error()
	}
	encoded, marshalErr := json.MarshalIndent(record, "", "  ")
	var writeErr error
	if marshalErr == nil {
		_, writeErr = persistTimerArtifact("image-provenance", "json", encoded)
	}
	if err := errors.Join(inspectErr, marshalErr, writeErr); err != nil {
		t.Fatalf("retain test-owned image %s provenance: %v", imageName, err)
	}

	return builtImage
}

func buildInvalidVectorImage(ctx context.Context, t *testing.T) string {
	t.Helper()
	endpoint := containertest.RequirePodman(t)
	root := fourPeerRepositoryRoot(t)
	return buildFourPeerImage(ctx, t, endpoint, prepareCraftedPeerContext(t, root),
		bfdFuzzImage, new(fourPeerResources))
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
	const containerfile = "FROM docker.io/library/golang:1.27.3-trixie@sha256:" +
		"604106102d33b1084e37ca08adf98ac0813624ae8595bd0d7f3965ecebded09c AS builder\n" +
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
		"62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad\n" +
		"ARG VCS_REF\nARG BUILD_DATE\n" +
		"LABEL org.opencontainers.image.source=\"https://github.com/dantte-lp/gobfd\" " +
		"org.opencontainers.image.revision=\"${VCS_REF}\" " +
		"org.opencontainers.image.created=\"${BUILD_DATE}\" " +
		"org.opencontainers.image.version=\"${VCS_REF}\" " +
		"org.opencontainers.image.licenses=\"Apache-2.0\" " +
		"org.opencontainers.image.title=\"GoBFD interoperability daemon\" " +
		"org.opencontainers.image.description=\"GoBFD daemon and CLI from the qualified candidate\" " +
		"org.opencontainers.image.base.name=\"docker.io/library/debian:trixie-slim\" " +
		"org.opencontainers.image.base.digest=\"sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad\"\n" +
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
