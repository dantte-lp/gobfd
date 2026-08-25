//go:build e2e_bgp_failover_testcontainers

package bgp_failover_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	bgpStateEstablished = 6
	maxDiagnosticBytes  = 1 << 20
	maxPacketBytes      = 100 << 20
	reportRunTime       = "20060102T150405Z"
)

type bgpFailoverContract struct {
	subnet       string
	gateway      string
	gobfdIP      string
	frrIP        string
	route        string
	gobgpImage   string
	frrImage     string
	gobfdConfig  string
	gobgpConfig  string
	frrDaemons   string
	frrConfig    string
	tsharkSource string
}

type bgpFailoverTopology struct {
	contract       bgpFailoverContract
	endpoint       string
	root           string
	runID          string
	reportDir      string
	networkName    string
	containerNames []string
	containerIDs   []string
	containers     []namedContainer
	imageNames     []string
	imageIDs       []string
	gobgpID        string
	frrID          string
	capture        testcontainers.Container
	analyzer       testcontainers.Container
	client         *podmanapi.Client
	packetEvidence bool
	frrPaused      bool
	startupError   string
	evidenceOnce   sync.Once
}

type namedContainer struct {
	name      string
	container testcontainers.Container
}

type bgpFailoverResourceSnapshot struct {
	ContainerNames []string `json:"container_names"`
	ContainerIDs   []string `json:"container_ids"`
	ImageNames     []string `json:"image_names"`
	ImageIDs       []string `json:"image_ids"`
	NetworkName    string   `json:"network_name"`
	StartupError   string   `json:"startup_error,omitempty"`
}

type ownedImageClient interface {
	ImageExists(ctx context.Context, image string) (bool, error)
	RemoveImage(ctx context.Context, image string) error
}

func TestBGPFastFailoverTestcontainers(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	topology := newBGPFailoverTopology(t, endpoint)
	registerFinalSummary(t.Cleanup, t.Failed, topology.writeSummary, func(err error) {
		t.Errorf("write final BGP fast-failover summary: %v", err)
	})

	if !t.Run("BFD-driven route withdrawal and restoration", func(t *testing.T) {
		topology.armEvidenceCleanup(t)
		if err := startBGPFailoverTopology(t, topology); err != nil {
			if recordErr := topology.recordStartupFailure(err); recordErr != nil {
				t.Errorf("record BGP fast-failover startup failure: %v", recordErr)
			}
			t.Fatalf("start BGP fast-failover topology: %v", err)
		}

		topology.requireVersions(t)
		topology.waitForEstablishedRoute(t, 90*time.Second)

		pauseCtx, pauseCancel := context.WithTimeout(t.Context(), 5*time.Second)
		if err := topology.client.Pause(pauseCtx, topology.frrID); err != nil {
			pauseCancel()
			t.Fatalf("pause exact FRR container %s: %v", topology.frrID, err)
		}
		pauseCancel()
		topology.frrPaused = true
		topology.waitForRoute(t, false, 20*time.Second)

		unpauseCtx, unpauseCancel := context.WithTimeout(t.Context(), 5*time.Second)
		if err := topology.client.Unpause(unpauseCtx, topology.frrID); err != nil {
			unpauseCancel()
			t.Fatalf("unpause exact FRR container %s: %v", topology.frrID, err)
		}
		unpauseCancel()
		topology.frrPaused = false
		topology.waitForEstablishedRoute(t, 90*time.Second)
	}) {
		t.Error("BGP fast-failover testcontainers lifecycle failed")
	}

	for _, containerID := range topology.containerIDs {
		if containerID != "" {
			containertest.AssertContainerRemoved(t, endpoint, containerID)
		}
	}
	assertBGPFailoverContainerNamesRemoved(t, endpoint, topology.containerNames)
	if topology.networkName != "" {
		containertest.AssertNetworkRemoved(t, topology.networkName)
	}
	for _, imageName := range topology.imageNames {
		containertest.AssertImageRemoved(t, endpoint, imageName)
	}
	for _, imageID := range topology.imageIDs {
		if imageID != "" {
			containertest.AssertImageRemoved(t, endpoint, imageID)
		}
	}
}

func newBGPFailoverTopology(t *testing.T, endpoint string) *bgpFailoverTopology {
	t.Helper()
	root := repositoryRoot(t)
	runID, reportDir := bgpFailoverReportDirectory(t, root)
	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create exact-endpoint Podman client: %v", err)
	}
	topology := &bgpFailoverTopology{
		contract: newBGPFailoverContract(root), endpoint: endpoint, root: root,
		runID: runID, reportDir: reportDir, client: client,
	}
	if err := topology.initializeResourceEvidence(); err != nil {
		t.Fatalf("initialize BGP fast-failover evidence ownership: %v", err)
	}
	return topology
}

func (topology *bgpFailoverTopology) initializeResourceEvidence() error {
	if err := initializeBGPFailoverDiagnostics(topology.reportDir); err != nil {
		return err
	}
	return topology.writeResourceSnapshot()
}

func (topology *bgpFailoverTopology) registerEvidenceCleanup(
	register func(func()),
	write func() error,
	report func(error),
) {
	register(func() {
		topology.evidenceOnce.Do(func() {
			if err := write(); err != nil {
				report(fmt.Errorf("write BGP fast-failover evidence before cleanup: %w", err))
			}
		})
	})
}

func (topology *bgpFailoverTopology) armEvidenceCleanup(t *testing.T) {
	t.Helper()
	topology.registerEvidenceCleanup(t.Cleanup, topology.writeEvidence, func(err error) {
		t.Errorf("%v", err)
	})
}

func (topology *bgpFailoverTopology) recordOwnedImage(name string) error {
	if name != "" && !slices.Contains(topology.imageNames, name) {
		topology.imageNames = append(topology.imageNames, name)
	}
	return topology.writeResourceSnapshot()
}

func (topology *bgpFailoverTopology) recordOwnedImageID(imageID string) error {
	if imageID != "" && !slices.Contains(topology.imageIDs, imageID) {
		topology.imageIDs = append(topology.imageIDs, imageID)
	}
	return topology.writeResourceSnapshot()
}

func (topology *bgpFailoverTopology) recordOwnedNetwork(name string) error {
	topology.networkName = name
	return topology.writeResourceSnapshot()
}

func (topology *bgpFailoverTopology) recordOwnedContainer(name, containerID string) error {
	if name != "" && !slices.Contains(topology.containerNames, name) {
		topology.containerNames = append(topology.containerNames, name)
	}
	if containerID != "" && !slices.Contains(topology.containerIDs, containerID) {
		topology.containerIDs = append(topology.containerIDs, containerID)
	}
	return topology.writeResourceSnapshot()
}

func (topology *bgpFailoverTopology) recordStartupFailure(startupErr error) error {
	topology.startupError = startupErr.Error()
	diagnosticErr := writeBGPFailoverDiagnostic(
		topology.reportDir, "containers.err", topology.startupError+"\n",
	)
	return errors.Join(diagnosticErr, topology.writeResourceSnapshot())
}

func (topology *bgpFailoverTopology) writeResourceSnapshot() error {
	snapshot := bgpFailoverResourceSnapshot{
		ContainerNames: slices.Clone(topology.containerNames),
		ContainerIDs:   slices.Clone(topology.containerIDs),
		ImageNames:     slices.Clone(topology.imageNames),
		ImageIDs:       slices.Clone(topology.imageIDs),
		NetworkName:    topology.networkName,
		StartupError:   topology.startupError,
	}
	contents, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mutable BGP fast-failover resource snapshot: %w", err)
	}
	contents = append(contents, '\n')
	if len(contents) > maxDiagnosticBytes {
		return fmt.Errorf("BGP fast-failover resource snapshot exceeds %d bytes", maxDiagnosticBytes)
	}
	if err := os.WriteFile(filepath.Join(topology.reportDir, "resources.json"), contents, 0o600); err != nil {
		return fmt.Errorf("write mutable BGP fast-failover resource snapshot: %w", err)
	}
	return nil
}

func registerFinalSummary(
	register func(func()),
	failed func() bool,
	write func(int) error,
	report func(error),
) {
	register(func() {
		status := 0
		if failed() {
			status = 1
		}
		if err := write(status); err != nil {
			report(err)
		}
	})
}

func newBGPFailoverContract(root string) bgpFailoverContract {
	base := filepath.Join(root, "deployments/integrations/bgp-fast-failover")
	return bgpFailoverContract{
		subnet:  "172.22.0.0/24",
		gateway: "172.22.0.1",
		gobfdIP: "172.22.0.10",
		frrIP:   "172.22.0.20",
		route:   "10.20.0.0/24",
		gobgpImage: "docker.io/jauderho/gobgp:v3.37.0@sha256:" +
			"3bb7304d299c42383c738f5bde2464793e2def9c1ff7fa3f25707a5bb10aee37",
		frrImage: "quay.io/frrouting/frr:10.7.0@sha256:" +
			"65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68",
		gobfdConfig:  filepath.Join(base, "gobfd/gobfd.yml"),
		gobgpConfig:  filepath.Join(base, "gobgp/gobgp.toml"),
		frrDaemons:   filepath.Join(base, "frr/daemons"),
		frrConfig:    filepath.Join(base, "frr/frr.conf"),
		tsharkSource: filepath.Join(root, "test/interop/tshark/Containerfile"),
	}
}

func startBGPFailoverTopology(t *testing.T, topology *bgpFailoverTopology) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	t.Cleanup(cancel)
	topology.armEvidenceCleanup(t)
	contract := topology.contract
	buildID := time.Now().UnixNano()
	gobfdImage, err := buildBGPFailoverImage(
		ctx, t, topology, prepareGoBFDBuildContext(t, topology.root),
		fmt.Sprintf("localhost/gobfd-bgp-failover:test-%d", buildID),
	)
	if err != nil {
		return fmt.Errorf("build bounded GoBFD image: %w", err)
	}
	tsharkImage, err := buildBGPFailoverImage(
		ctx, t, topology, prepareTsharkBuildContext(t, contract.tsharkSource),
		fmt.Sprintf("localhost/gobfd-bgp-failover-tshark:test-%d", buildID),
	)
	if err != nil {
		return fmt.Errorf("build bounded tshark image: %w", err)
	}
	networkName := fmt.Sprintf("gobfd-bgp-failover-%d", buildID)
	if recordErr := topology.recordOwnedNetwork(networkName); recordErr != nil {
		return recordErr
	}
	//nolint:staticcheck // Explicit ProviderPodman with static IPAM requires this v0.44 API.
	_, err = containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name:   networkName,
		Driver: "bridge",
		Labels: map[string]string{"io.gobfd.test": "bgp-fast-failover", "io.gobfd.run": topology.runID},
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet:  netip.MustParsePrefix(contract.subnet),
			Gateway: netip.MustParseAddr(contract.gateway),
		}}},
	})
	topology.armEvidenceCleanup(t)
	if err != nil {
		return fmt.Errorf("create BGP fast-failover Podman network: %w", err)
	}
	labels := map[string]string{"io.gobfd.test": "bgp-fast-failover", "io.gobfd.run": topology.runID}

	gobfd, err := startBGPFailoverContainer(
		ctx, t, topology, "gobfd", networkName+"-gobfd", testcontainers.ContainerRequest{
			Image:    gobfdImage,
			Labels:   labels,
			Networks: []string{networkName},
			Cmd:      []string{"-config", "/etc/gobfd/gobfd.yml"},
			Files: []testcontainers.ContainerFile{{
				HostFilePath: contract.gobfdConfig, ContainerFilePath: "/etc/gobfd/gobfd.yml", FileMode: 0o644,
			}},
			WaitingFor: wait.ForLog("metrics server listening").WithStartupTimeout(30 * time.Second),
			ConfigModifier: func(config *container.Config) {
				config.User = "0:0"
			},
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN")
			},
			EndpointSettingsModifier: staticBGPFailoverIP(networkName, contract.gobfdIP),
		},
	)
	if err != nil {
		return err
	}
	gobfdImageID := requireContainerImageID(ctx, t, gobfd, "GoBFD")
	if recordErr := topology.recordOwnedImageID(gobfdImageID); recordErr != nil {
		return recordErr
	}

	gobgp, err := startBGPFailoverContainer(
		ctx, t, topology, "gobgp", networkName+"-gobgp", testcontainers.ContainerRequest{
			Image:  contract.gobgpImage,
			Labels: labels,
			Cmd:    []string{"gobgpd", "-f", "/etc/gobgp/gobgp.toml", "-l", "info"},
			Files: []testcontainers.ContainerFile{{
				HostFilePath: contract.gobgpConfig, ContainerFilePath: "/etc/gobgp/gobgp.toml", FileMode: 0o644,
			}},
			WaitingFor: wait.ForExec([]string{"gobgp", "neighbor", "-j"}).WithStartupTimeout(30 * time.Second),
			ConfigModifier: func(config *container.Config) {
				config.User = "0:0"
			},
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.NetworkMode = container.NetworkMode("container:" + gobfd.GetContainerID())
				hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_BIND_SERVICE")
			},
		},
	)
	if err != nil {
		return err
	}
	topology.gobgpID = gobgp.GetContainerID()

	capture, err := startBGPFailoverContainer(
		ctx, t, topology, "tshark-capture", networkName+"-tshark-capture", testcontainers.ContainerRequest{
			Image:      tsharkImage,
			Labels:     labels,
			WaitingFor: wait.ForExec([]string{"test", "-f", "/captures/bfd.pcapng"}).WithStartupTimeout(20 * time.Second),
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN")
				hostConfig.NetworkMode = container.NetworkMode("container:" + gobfd.GetContainerID())
			},
		},
	)
	if err != nil {
		return err
	}
	topology.capture = capture
	tsharkImageID := requireContainerImageID(ctx, t, capture, "tshark")
	if recordErr := topology.recordOwnedImageID(tsharkImageID); recordErr != nil {
		return recordErr
	}

	analyzer, err := startBGPFailoverContainer(
		ctx, t, topology, "tshark-analyzer", networkName+"-tshark-analyzer", testcontainers.ContainerRequest{
			Image:      tsharkImageID,
			Labels:     labels,
			Entrypoint: []string{"sleep"},
			Cmd:        []string{"infinity"},
			WaitingFor: wait.ForExec([]string{"test", "-d", "/captures"}).WithStartupTimeout(20 * time.Second),
		},
	)
	if err != nil {
		return err
	}
	topology.analyzer = analyzer

	frr, err := startBGPFailoverContainer(
		ctx, t, topology, "frr", networkName+"-frr", testcontainers.ContainerRequest{
			Image:    contract.frrImage,
			Labels:   labels,
			Networks: []string{networkName},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: contract.frrDaemons, ContainerFilePath: "/etc/frr/daemons", FileMode: 0o644},
				{HostFilePath: contract.frrConfig, ContainerFilePath: "/etc/frr/frr.conf", FileMode: 0o644},
			},
			WaitingFor: wait.ForExec([]string{"vtysh", "-c", "show version"}).WithStartupTimeout(30 * time.Second),
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN", "SYS_ADMIN")
			},
			EndpointSettingsModifier: staticBGPFailoverIP(networkName, contract.frrIP),
		},
	)
	if err != nil {
		return err
	}
	topology.frrID = frr.GetContainerID()
	t.Cleanup(func() {
		if !topology.frrPaused {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		if unpauseErr := topology.client.Unpause(cleanupCtx, topology.frrID); unpauseErr != nil {
			t.Logf("best-effort exact FRR unpause before removal: %v", unpauseErr)
		}
	})
	topology.armEvidenceCleanup(t)

	assertBGPFailoverTopology(ctx, t, topology, gobfd, gobfdImageID, tsharkImageID)
	return nil
}

func startBGPFailoverContainer(
	ctx context.Context,
	t *testing.T,
	topology *bgpFailoverTopology,
	role, name string,
	request testcontainers.ContainerRequest,
) (testcontainers.Container, error) {
	t.Helper()
	request.Name = name
	if err := topology.recordOwnedContainer(name, ""); err != nil {
		return nil, err
	}
	testContainer, err := containertest.Run(ctx, t, request)
	var recordErr error
	if testContainer != nil {
		containerID := testContainer.GetContainerID()
		topology.containers = append(topology.containers, namedContainer{name: role, container: testContainer})
		recordErr = topology.recordOwnedContainer(name, containerID)
	}
	topology.armEvidenceCleanup(t)
	if err != nil {
		err = fmt.Errorf("start BGP fast-failover %s container: %w", role, err)
	}
	return testContainer, errors.Join(err, recordErr)
}

func assertBGPFailoverContainerNamesRemoved(t *testing.T, endpoint string, names []string) {
	t.Helper()
	if len(names) == 0 {
		return
	}
	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create exact-endpoint container-name cleanup client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		containers, listErr := client.Containers(ctx)
		if listErr != nil {
			t.Fatalf("list containers while verifying exact owned names: %v", listErr)
		}
		remaining := make([]string, 0, len(names))
		for _, ownedName := range names {
			for _, existing := range containers {
				for _, existingName := range existing.Names {
					if strings.TrimPrefix(existingName, "/") == ownedName {
						remaining = append(remaining, ownedName)
					}
				}
			}
		}
		if len(remaining) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("containers with exact owned names %v still exist after cleanup: %v", remaining, ctx.Err())
		case <-ticker.C:
		}
	}
}

func staticBGPFailoverIP(networkName, address string) func(map[string]*network.EndpointSettings) {
	return func(settings map[string]*network.EndpointSettings) {
		endpoint := settings[networkName]
		if endpoint == nil {
			endpoint = new(network.EndpointSettings)
			settings[networkName] = endpoint
		}
		endpoint.IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(address)}
	}
}

func (topology *bgpFailoverTopology) requireVersions(t *testing.T) {
	t.Helper()
	for _, check := range []struct {
		name      string
		container string
		command   []string
		version   string
	}{
		{name: "GoBGP", container: topology.gobgpID, command: []string{"gobgp", "--version"}, version: "3.37.0"},
		{name: "FRR", container: topology.frrID, command: []string{"vtysh", "-c", "show version"}, version: "10.7.0"},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		result, err := topology.client.Exec(ctx, check.container, check.command)
		cancel()
		if err != nil {
			t.Fatalf("inspect pinned %s version in exact container %s: %v", check.name, check.container, err)
		}
		if !strings.Contains(result.Stdout, check.version) {
			t.Fatalf("%s version output %q does not contain %q", check.name, result.Stdout, check.version)
		}
	}
}

func (topology *bgpFailoverTopology) waitForEstablishedRoute(t *testing.T, timeout time.Duration) {
	t.Helper()
	topology.waitFor(
		t, "exact GoBGP peer Established and route present", timeout,
		func(ctx context.Context) (bool, error) {
			state, err := topology.goBGPNeighborState(ctx)
			if err != nil || state != bgpStateEstablished {
				return false, err
			}
			return topology.goBGPRouteExists(ctx)
		},
	)
}

func (topology *bgpFailoverTopology) waitForRoute(t *testing.T, want bool, timeout time.Duration) {
	t.Helper()
	description := "route restoration"
	if !want {
		description = "BFD-driven route withdrawal"
	}
	topology.waitFor(t, description, timeout, func(ctx context.Context) (bool, error) {
		exists, err := topology.goBGPRouteExists(ctx)
		return exists == want, err
	})
}

func (topology *bgpFailoverTopology) waitFor(
	t *testing.T,
	description string,
	timeout time.Duration,
	condition func(context.Context) (bool, error),
) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		requestCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		ready, err := condition(requestCtx)
		cancel()
		if err != nil {
			lastErr = err
		} else if ready {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("%s not observed within %s: last error=%v", description, timeout, lastErr)
		case <-t.Context().Done():
			t.Fatalf("wait for %s: %v", description, t.Context().Err())
		}
	}
}

func (topology *bgpFailoverTopology) goBGPNeighborState(ctx context.Context) (int, error) {
	result, err := topology.client.Exec(ctx, topology.gobgpID, []string{"gobgp", "neighbor", "-j"})
	if err != nil {
		return 0, fmt.Errorf("query exact GoBGP neighbor: %w", err)
	}
	return parseGoBGPNeighborState(result.Stdout, topology.contract.frrIP)
}

func parseGoBGPNeighborState(output, peerIP string) (int, error) {
	var neighbors []struct {
		State struct {
			NeighborAddress string `json:"neighbor_address"`
			SessionState    int    `json:"session_state"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(output), &neighbors); err != nil {
		return 0, fmt.Errorf("parse GoBGP v3.37 neighbor JSON: %w", err)
	}
	for _, neighbor := range neighbors {
		if neighbor.State.NeighborAddress == peerIP {
			return neighbor.State.SessionState, nil
		}
	}
	return 0, fmt.Errorf("exact GoBGP peer %s is absent", peerIP)
}

func (topology *bgpFailoverTopology) goBGPRouteExists(ctx context.Context) (bool, error) {
	result, err := topology.client.Exec(ctx, topology.gobgpID, []string{"gobgp", "global", "rib", "-j"})
	if err != nil {
		return false, fmt.Errorf("query exact GoBGP global RIB: %w", err)
	}
	return parseGoBGPRIB(result.Stdout, topology.contract.route)
}

func parseGoBGPRIB(output, prefix string) (bool, error) {
	var destinations map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &destinations); err != nil {
		return false, fmt.Errorf("parse GoBGP v3.37 RIB JSON: %w", err)
	}
	_, exists := destinations[prefix]
	return exists, nil
}

func (topology *bgpFailoverTopology) writeEvidence() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var evidenceErr error
	if !topology.packetEvidence && topology.capture != nil && topology.analyzer != nil && topology.client != nil {
		evidenceErr = errors.Join(evidenceErr, topology.collectPacketEvidence(ctx))
	} else if !topology.packetEvidence {
		evidenceErr = errors.Join(evidenceErr, writeBGPFailoverDiagnostic(
			topology.reportDir, "packets.err", "packet evidence unavailable: topology startup incomplete\n",
		))
	}
	evidenceErr = errors.Join(evidenceErr, topology.writeContainerLogs(ctx))
	evidenceErr = errors.Join(evidenceErr, topology.writeContainerSnapshot(ctx))
	evidenceErr = errors.Join(evidenceErr, topology.writeEnvironment())
	evidenceErr = errors.Join(evidenceErr, topology.writeResourceSnapshot())
	return evidenceErr
}

func (topology *bgpFailoverTopology) collectPacketEvidence(ctx context.Context) error {
	stopTimeout := 3 * time.Second
	if err := topology.capture.Stop(ctx, &stopTimeout); err != nil {
		return topology.packetEvidenceError(fmt.Errorf("stop exact tshark capture: %w", err))
	}
	reader, err := topology.capture.CopyFileFromContainer(ctx, "/captures/bfd.pcapng")
	if err != nil {
		return topology.packetEvidenceError(fmt.Errorf("copy BFD packet capture: %w", err))
	}
	packetData, readErr := io.ReadAll(io.LimitReader(reader, maxPacketBytes+1))
	closeErr := reader.Close()
	if joinedErr := errors.Join(readErr, closeErr); joinedErr != nil {
		return topology.packetEvidenceError(fmt.Errorf("read BFD packet capture: %w", joinedErr))
	}
	if len(packetData) == 0 || len(packetData) > maxPacketBytes {
		return topology.packetEvidenceError(fmt.Errorf(
			"BFD packet capture size %d is outside 1..%d", len(packetData), maxPacketBytes,
		))
	}
	if writeErr := os.WriteFile(
		filepath.Join(topology.reportDir, "packets.pcapng"), packetData, 0o600,
	); writeErr != nil {
		return topology.packetEvidenceError(fmt.Errorf("write BFD packet capture: %w", writeErr))
	}
	if copyErr := topology.analyzer.CopyToContainer(
		ctx, packetData, "/captures/bfd.pcapng", 0o600,
	); copyErr != nil {
		return topology.packetEvidenceError(fmt.Errorf("copy packet capture to exact analyzer: %w", copyErr))
	}
	filter := fmt.Sprintf(
		"bfd && ((ip.src == %s && ip.dst == %s) || (ip.src == %s && ip.dst == %s))",
		topology.contract.gobfdIP, topology.contract.frrIP,
		topology.contract.frrIP, topology.contract.gobfdIP,
	)
	result, err := topology.client.Exec(ctx, topology.analyzer.GetContainerID(), []string{
		"tshark", "-r", "/captures/bfd.pcapng", "-Y", filter, "-T", "fields",
		"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst",
		"-e", "udp.srcport", "-e", "udp.dstport", "-e", "bfd.sta", "-e", "bfd.diag",
		"-e", "bfd.my_discriminator", "-e", "bfd.your_discriminator",
		"-E", "header=y", "-E", "separator=,",
	})
	if err != nil {
		return topology.packetEvidenceError(fmt.Errorf("decode exact BFD packet evidence: %w", err))
	}
	if len(strings.Split(strings.TrimSpace(result.Stdout), "\n")) < 2 {
		return topology.packetEvidenceError(errors.New("decoded BFD packet evidence has no packet row"))
	}
	if err := os.WriteFile(filepath.Join(topology.reportDir, "packets.csv"), []byte(result.Stdout), 0o600); err != nil {
		return topology.packetEvidenceError(fmt.Errorf("write decoded BFD packet evidence: %w", err))
	}
	if err := writeBGPFailoverDiagnostic(topology.reportDir, "packets.err", ""); err != nil {
		return err
	}
	topology.packetEvidence = true
	return nil
}

func (topology *bgpFailoverTopology) packetEvidenceError(err error) error {
	diagnosticErr := writeBGPFailoverDiagnostic(topology.reportDir, "packets.err", err.Error()+"\n")
	return errors.Join(err, diagnosticErr)
}

func (topology *bgpFailoverTopology) writeContainerLogs(ctx context.Context) error {
	var output strings.Builder
	for _, item := range topology.containers {
		logs, err := item.container.Logs(ctx)
		if err != nil {
			return fmt.Errorf("open %s logs: %w", item.name, err)
		}
		contents, readErr := io.ReadAll(io.LimitReader(logs, maxDiagnosticBytes+1))
		closeErr := logs.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return fmt.Errorf("read bounded %s logs: %w", item.name, err)
		}
		if len(contents) > maxDiagnosticBytes {
			return fmt.Errorf("%s logs exceed %d bytes", item.name, maxDiagnosticBytes)
		}
		fmt.Fprintf(&output, "===== %s (%s) =====\n%s\n", item.name, item.container.GetContainerID(), contents)
	}
	if err := os.WriteFile(
		filepath.Join(topology.reportDir, "containers.log"), []byte(output.String()), 0o600,
	); err != nil {
		return fmt.Errorf("write bounded container logs: %w", err)
	}
	return nil
}

func (topology *bgpFailoverTopology) writeContainerSnapshot(ctx context.Context) error {
	inspections := make([]json.RawMessage, 0, len(topology.containerIDs))
	var inspectionErr error
	for _, containerID := range topology.containerIDs {
		if topology.client == nil {
			inspectionErr = errors.Join(inspectionErr, errors.New("exact-endpoint Podman client unavailable"))
			break
		}
		inspection, err := topology.client.Inspect(ctx, containerID)
		if err != nil {
			inspectionErr = errors.Join(inspectionErr, fmt.Errorf("inspect exact owned container %s: %w", containerID, err))
			continue
		}
		inspections = append(inspections, inspection)
	}
	contents, err := json.MarshalIndent(inspections, "", "  ")
	if err != nil {
		return fmt.Errorf("encode exact container snapshot: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(filepath.Join(topology.reportDir, "containers.json"), contents, 0o600); err != nil {
		return fmt.Errorf("write exact container snapshot: %w", err)
	}
	var diagnostic strings.Builder
	if topology.startupError != "" {
		diagnostic.WriteString(topology.startupError)
		diagnostic.WriteByte('\n')
	}
	if inspectionErr != nil {
		diagnostic.WriteString(inspectionErr.Error())
		diagnostic.WriteByte('\n')
	}
	writeErr := writeBGPFailoverDiagnostic(topology.reportDir, "containers.err", diagnostic.String())
	return errors.Join(inspectionErr, writeErr)
}

func (topology *bgpFailoverTopology) writeEnvironment() error {
	document := struct {
		Target         string   `json:"target"`
		RunID          string   `json:"run_id"`
		PodmanEndpoint string   `json:"podman_endpoint"`
		Network        string   `json:"network"`
		ContainerIDs   []string `json:"container_ids"`
		ImageIDs       []string `json:"image_ids"`
	}{
		Target: "bgp-fast-failover-testcontainers", RunID: topology.runID,
		PodmanEndpoint: topology.endpoint, Network: topology.networkName,
		ContainerIDs: topology.containerIDs, ImageIDs: topology.imageIDs,
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode BGP fast-failover environment: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(filepath.Join(topology.reportDir, "environment.json"), contents, 0o600); err != nil {
		return fmt.Errorf("write BGP fast-failover environment: %w", err)
	}
	return nil
}

func (topology *bgpFailoverTopology) writeSummary(status int) error {
	contents := fmt.Sprintf(`# BGP Fast Failover Testcontainers Summary

| Field | Value |
|---|---|
| Target | %s |
| Run ID | %s |
| Exit code | %d |
| Packet capture | %s |
| Packet CSV | %s |
| Container evidence | %s |
`, "`make int-bgp-failover-testcontainers`", "`"+topology.runID+"`", status,
		"`packets.pcapng`", "`packets.csv`", "`containers.json`, `containers.log`")
	if err := os.WriteFile(filepath.Join(topology.reportDir, "summary.md"), []byte(contents), 0o600); err != nil {
		return fmt.Errorf("write BGP fast-failover summary: %w", err)
	}
	return nil
}

func writeBGPFailoverDiagnostic(reportDir, name, contents string) error {
	truncated := len(contents) > maxDiagnosticBytes
	if truncated {
		contents = contents[:maxDiagnosticBytes]
	}
	file, err := os.OpenFile(filepath.Join(reportDir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create BGP failover diagnostic %s: %w", name, err)
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod BGP failover diagnostic %s: %w", name, errors.Join(err, file.Close()))
	}
	_, writeErr := io.WriteString(file, contents)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write BGP failover diagnostic %s: %w", name, err)
	}
	if truncated {
		return fmt.Errorf("BGP failover diagnostic %s truncated to %d bytes", name, maxDiagnosticBytes)
	}
	return nil
}

func initializeBGPFailoverDiagnostics(reportDir string) error {
	var diagnosticsErr error
	for _, name := range []string{"containers.err", "packets.err"} {
		diagnosticsErr = errors.Join(diagnosticsErr, writeBGPFailoverDiagnostic(reportDir, name, ""))
	}
	if diagnosticsErr != nil {
		return fmt.Errorf("initialize BGP fast-failover diagnostics: %w", diagnosticsErr)
	}
	return nil
}

func bgpFailoverReportDirectory(t *testing.T, root string) (string, string) {
	t.Helper()
	runID := time.Now().UTC().Format(reportRunTime)
	reportDir := strings.TrimSpace(os.Getenv("E2E_BGP_FAILOVER_TESTCONTAINERS_ARTIFACT_DIR"))
	switch {
	case reportDir == "":
		reportDir = filepath.Join(root, "reports/e2e/bgp-fast-failover", runID)
	case !filepath.IsAbs(reportDir):
		t.Fatalf("BGP fast-failover artifact directory %q must be absolute", reportDir)
	default:
		runID = filepath.Base(filepath.Clean(reportDir))
	}
	if err := os.MkdirAll(reportDir, 0o700); err != nil {
		t.Fatalf("create BGP fast-failover artifact directory: %v", err)
	}
	return runID, reportDir
}

func assertBGPFailoverTopology(
	ctx context.Context,
	t *testing.T,
	topology *bgpFailoverTopology,
	gobfd testcontainers.Container,
	gobfdImageID, tsharkImageID string,
) {
	t.Helper()
	gobfdInspection, err := gobfd.Inspect(ctx)
	if err != nil {
		t.Fatalf("inspect GoBFD topology contract: %v", err)
	}
	if gobfdInspection.Image != gobfdImageID || len(gobfdInspection.Mounts) != 0 {
		t.Fatalf("GoBFD image/mount contract = image %q mounts %+v", gobfdInspection.Image, gobfdInspection.Mounts)
	}
	if gobfdInspection.NetworkSettings == nil ||
		gobfdInspection.NetworkSettings.Networks[topology.networkName] == nil ||
		gobfdInspection.NetworkSettings.Networks[topology.networkName].IPAddress.String() != topology.contract.gobfdIP {
		t.Fatalf("GoBFD static IP contract is not %s", topology.contract.gobfdIP)
	}
	for _, item := range []struct {
		name      string
		container testcontainers.Container
		wantMode  string
		wantImage string
	}{
		{name: "gobgp", container: topology.containers[1].container, wantMode: "container:" + gobfd.GetContainerID()},
		{
			name: "capture", container: topology.capture,
			wantMode: "container:" + gobfd.GetContainerID(), wantImage: tsharkImageID,
		},
		{name: "analyzer", container: topology.analyzer, wantImage: tsharkImageID},
	} {
		inspection, inspectErr := item.container.Inspect(ctx)
		if inspectErr != nil {
			t.Fatalf("inspect %s topology contract: %v", item.name, inspectErr)
		}
		if item.wantMode != "" && string(inspection.HostConfig.NetworkMode) != item.wantMode {
			t.Fatalf("%s network mode = %q, want immutable %q", item.name, inspection.HostConfig.NetworkMode, item.wantMode)
		}
		if item.wantImage != "" && inspection.Image != item.wantImage {
			t.Fatalf("%s image = %q, want content ID %q", item.name, inspection.Image, item.wantImage)
		}
		if len(inspection.Mounts) != 0 {
			t.Fatalf("%s mounts = %+v, want copied files only", item.name, inspection.Mounts)
		}
	}
}

func requireContainerImageID(
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
		t.Fatalf("%s image ID = %q, want sha256 content ID", description, inspection.Image)
	}
	for _, character := range imageID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			t.Fatalf("%s image ID = %q, want lowercase hexadecimal", description, inspection.Image)
		}
	}
	return inspection.Image
}

func buildBGPFailoverImage(
	ctx context.Context,
	t *testing.T,
	topology *bgpFailoverTopology,
	buildContext, imageName string,
) (string, error) {
	t.Helper()
	repository, tag, found := strings.Cut(imageName, ":")
	if !found {
		return "", fmt.Errorf("split test-owned image %q", imageName)
	}
	if err := installOwnedImageCleanup(ctx, imageName, topology.client, t.Cleanup, func(err error) {
		t.Errorf("%v", err)
	}); err != nil {
		return "", err
	}
	if err := topology.recordOwnedImage(imageName); err != nil {
		return "", err
	}
	topology.armEvidenceCleanup(t)
	provider, err := testcontainers.ProviderPodman.GetProvider()
	if err != nil {
		return "", fmt.Errorf("create explicit Podman image provider: %w", err)
	}
	dockerProvider, ok := provider.(*testcontainers.DockerProvider)
	if !ok {
		closeErr := provider.Close()
		return "", errors.Join(
			fmt.Errorf("Podman provider type = %T, want *testcontainers.DockerProvider", provider),
			closeErr,
		)
	}
	//nolint:modernize // ContainerRequest implements ImageBuildInfo in testcontainers v0.44.
	_, buildErr := dockerProvider.BuildImage(ctx, &testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context: buildContext, Dockerfile: "Containerfile", Repo: repository, Tag: tag, KeepImage: true,
		},
	})
	closeErr := provider.Close()
	if err := errors.Join(buildErr, closeErr); err != nil {
		return "", fmt.Errorf("build bounded test-owned image %s: %w", imageName, err)
	}
	return imageName, nil
}

func installOwnedImageCleanup(
	ctx context.Context,
	imageName string,
	client ownedImageClient,
	register func(func()),
	report func(error),
) error {
	exists, err := client.ImageExists(ctx, imageName)
	if err != nil {
		return fmt.Errorf("inspect unique test-owned image %s before build: %w", imageName, err)
	}
	if exists {
		return fmt.Errorf("refuse ownership of pre-existing image %s", imageName)
	}
	register(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		exists, inspectErr := client.ImageExists(cleanupCtx, imageName)
		if inspectErr != nil {
			report(fmt.Errorf("inspect exact test-owned image %s during cleanup: %w", imageName, inspectErr))
			return
		}
		if !exists {
			return
		}
		if removeErr := client.RemoveImage(cleanupCtx, imageName); removeErr != nil {
			report(fmt.Errorf("remove exact test-owned image %s: %w", imageName, removeErr))
		}
	})
	return nil
}

func prepareGoBFDBuildContext(t *testing.T, root string) string {
	t.Helper()
	contextDir := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{"cmd/gobfd", "internal", "pkg"} {
		subtree, err := fs.Sub(rootFS, sourceDir)
		if err != nil {
			t.Fatalf("open bounded GoBFD source %s: %v", sourceDir, err)
		}
		if err := os.CopyFS(filepath.Join(contextDir, sourceDir), subtree); err != nil {
			t.Fatalf("copy bounded GoBFD source %s: %v", sourceDir, err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := copyBGPFailoverFile(filepath.Join(root, name), filepath.Join(contextDir, name)); err != nil {
			t.Fatalf("copy %s into bounded GoBFD build context: %v", name, err)
		}
	}
	const containerfile = `FROM docker.io/library/golang:1.27.0-trixie@sha256:` +
		`ae28539d2ef595b9a2930dd7f031d9592376829dc0eae7cb869559f7d5812c3a AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/gobfd ./cmd/gobfd
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfd ./cmd/gobfd
FROM docker.io/library/debian:trixie-slim@sha256:` +
		`d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132
COPY --from=builder /bin/gobfd /bin/gobfd
ENTRYPOINT ["/bin/gobfd"]
`
	if err := os.WriteFile(filepath.Join(contextDir, "Containerfile"), []byte(containerfile), 0o600); err != nil {
		t.Fatalf("write bounded GoBFD Containerfile: %v", err)
	}
	return contextDir
}

func prepareTsharkBuildContext(t *testing.T, source string) string {
	t.Helper()
	contextDir := t.TempDir()
	if err := copyBGPFailoverFile(source, filepath.Join(contextDir, "Containerfile")); err != nil {
		t.Fatalf("copy bounded tshark Containerfile: %v", err)
	}
	return contextDir
}

func copyBGPFailoverFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source %s: %w", source, err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create destination %s: %w", destination, err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("copy %s to %s: %w", source, destination, err)
	}
	return nil
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve test working directory: %v", err)
	}
	for {
		module, readErr := os.ReadFile(filepath.Join(directory, "go.mod"))
		if readErr == nil {
			if !strings.HasPrefix(string(module), "module github.com/dantte-lp/gobfd\n") {
				t.Fatalf("resolved repository root %s has unexpected module", directory)
			}
			return directory
		}
		if !os.IsNotExist(readErr) {
			t.Fatalf("inspect repository root candidate %s: %v", directory, readErr)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatalf("find repository root from %s: go.mod not found", directory)
		}
		directory = parent
	}
}
