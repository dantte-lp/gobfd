//go:build e2e_haproxy_testcontainers

package haproxy_health_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
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

type haproxyHealthTopology struct {
	contract                     haproxyHealthContract
	endpoint, root               string
	runID, reportDir             string
	networkName                  string
	containerNames, containerIDs []string
	containers                   []namedContainer
	roleContainers               map[string]testcontainers.Container
	imageNames, imageIDs         []string
	monitorID, backend1ID        string
	backend1BFDID, backend2ID    string
	backend2BFDID                string
	monitor, haproxy             testcontainers.Container
	backend1, backend2           testcontainers.Container
	capture, analyzer            testcontainers.Container
	client                       *podmanapi.Client
	agent1Address, agent2Address string
	haproxyURL, statsURL         string
	backend1URL                  string
	backend1BFDPaused            bool
	packetEvidence               bool
	startupError                 string
	evidenceOnce                 sync.Once
}

type namedContainer struct {
	name      string
	container testcontainers.Container
}

func TestHAProxyHealthTestcontainers(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	t.Setenv("PODMAN_HOST", endpoint)
	topology := newHAProxyHealthTopology(t, endpoint)
	registerHAProxyFinalSummary(t.Cleanup, t.Failed, topology.writeSummary, func(err error) {
		t.Errorf("write final HAProxy health summary: %v", err)
	})

	if !t.Run("BFD agent-check excludes only the paused sidecar", func(t *testing.T) {
		topology.armEvidenceCleanup(t)
		if err := startHAProxyHealthTopology(t, topology); err != nil {
			if recordErr := topology.recordStartupFailure(err); recordErr != nil {
				t.Errorf("record HAProxy health startup failure: %v", recordErr)
			}
			t.Fatalf("start HAProxy health topology: %v", err)
		}

		topology.waitForInitialHealth(t, 90*time.Second)
		mutationID := mutationContainerID(topology.backend1ID, topology.backend1BFDID)
		if mutationID == topology.backend1ID || mutationID == "" {
			t.Fatalf("failure mutation ID %q is not the exact backend1-bfd sidecar", mutationID)
		}
		pauseCtx, pauseCancel := context.WithTimeout(t.Context(), 5*time.Second)
		if err := topology.client.Pause(pauseCtx, mutationID); err != nil {
			pauseCancel()
			t.Fatalf("pause exact backend1-bfd container %s: %v", mutationID, err)
		}
		pauseCancel()
		topology.backend1BFDPaused = true
		topology.waitForBackend1Excluded(t, 20*time.Second)

		unpauseCtx, unpauseCancel := context.WithTimeout(t.Context(), 5*time.Second)
		if err := topology.client.Unpause(unpauseCtx, mutationID); err != nil {
			unpauseCancel()
			t.Fatalf("unpause exact backend1-bfd container %s: %v", mutationID, err)
		}
		unpauseCancel()
		topology.backend1BFDPaused = false
		topology.waitForInitialHealth(t, 90*time.Second)
	}) {
		t.Error("HAProxy health testcontainers lifecycle failed")
	}

	for _, containerID := range topology.containerIDs {
		if containerID != "" {
			containertest.AssertContainerRemoved(t, endpoint, containerID)
		}
	}
	assertHAProxyContainerNamesRemoved(t, endpoint, topology.containerNames)
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

func newHAProxyHealthTopology(t *testing.T, endpoint string) *haproxyHealthTopology {
	t.Helper()
	root := repositoryRoot(t)
	runID, reportDir := haproxyReportDirectory(t, root)
	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create exact-endpoint Podman client: %v", err)
	}
	topology := &haproxyHealthTopology{
		contract: newHAProxyHealthContract(root), endpoint: endpoint, root: root,
		runID: runID, reportDir: reportDir, client: client,
		roleContainers: make(map[string]testcontainers.Container),
	}
	if err := topology.initializeResourceEvidence(); err != nil {
		t.Fatalf("initialize HAProxy health evidence ownership: %v", err)
	}
	return topology
}

func startHAProxyHealthTopology(t *testing.T, topology *haproxyHealthTopology) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	t.Cleanup(cancel)
	topology.armEvidenceCleanup(t)
	buildID := time.Now().UnixNano()
	gobfdImage, err := buildHAProxyOwnedImage(
		ctx, t, topology, prepareHAProxyGoBFDBuildContext(t, topology.root),
		fmt.Sprintf("localhost/gobfd-haproxy-health:test-%d", buildID),
	)
	if err != nil {
		return fmt.Errorf("build bounded GoBFD image: %w", err)
	}
	tsharkImage, err := buildHAProxyOwnedImage(
		ctx, t, topology, prepareHAProxyTsharkBuildContext(t, topology.contract.tsharkContainerfilePath),
		fmt.Sprintf("localhost/gobfd-haproxy-health-tshark:test-%d", buildID),
	)
	if err != nil {
		return fmt.Errorf("build bounded tshark image: %w", err)
	}
	if err := topology.createNetwork(ctx, t, buildID); err != nil {
		return err
	}
	if err := topology.startMonitorGroup(ctx, t, gobfdImage, tsharkImage); err != nil {
		return err
	}
	if err := topology.startBackends(ctx, t, gobfdImage); err != nil {
		return err
	}
	if err := topology.startHAProxy(ctx, t); err != nil {
		return err
	}
	if err := topology.resolveHostEndpoints(ctx); err != nil {
		return err
	}
	if err := wrapRuntimeContractStartupError(topology.assertRuntimeContract(ctx)); err != nil {
		return err
	}
	return nil
}

func wrapRuntimeContractStartupError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("assert HAProxy health runtime contract: %w", err)
}

func (topology *haproxyHealthTopology) createNetwork(ctx context.Context, t *testing.T, buildID int64) error {
	t.Helper()
	topology.networkName = fmt.Sprintf("gobfd-haproxy-health-%d", buildID)
	if err := topology.writeResourceSnapshot(); err != nil {
		return err
	}
	//nolint:staticcheck // Explicit ProviderPodman with static IPAM requires this v0.44 API.
	_, err := containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name: topology.networkName, Driver: "bridge", Labels: topology.labels("network"),
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet: netip.MustParsePrefix(topology.contract.subnet), Gateway: netip.MustParseAddr(topology.contract.gateway),
		}}},
	})
	topology.armEvidenceCleanup(t)
	if err != nil {
		return fmt.Errorf("create HAProxy health Podman network: %w", err)
	}
	return nil
}

func (topology *haproxyHealthTopology) startMonitorGroup(
	ctx context.Context, t *testing.T, gobfdImage, tsharkImage string,
) error {
	t.Helper()
	contract := topology.contract
	monitor, err := topology.startContainer(ctx, t, "monitor", testcontainers.ContainerRequest{
		Image: gobfdImage, Networks: []string{topology.networkName},
		Cmd: []string{"-config", "/etc/gobfd/gobfd.yml"}, ExposedPorts: []string{agent1Port, agent2Port},
		Files: []testcontainers.ContainerFile{{
			HostFilePath: contract.monitorConfig, ContainerFilePath: "/etc/gobfd/gobfd.yml", FileMode: 0o644,
		}},
		WaitingFor:               wait.ForLog("metrics server listening").WithStartupTimeout(30 * time.Second),
		ConfigModifier:           func(config *container.Config) { config.User = "0:0" },
		HostConfigModifier:       addBFDContainerCapabilities,
		EndpointSettingsModifier: staticHAProxyIP(topology.networkName, contract.monitorIP),
	})
	if err != nil {
		return err
	}
	topology.monitor, topology.monitorID = monitor, monitor.GetContainerID()
	gobfdImageID, err := requireHAProxyContainerImageID(ctx, monitor, "GoBFD")
	if err != nil {
		return err
	}
	if recordErr := topology.recordOwnedImageID(gobfdImageID); recordErr != nil {
		return recordErr
	}
	if _, agentErr := topology.startContainer(ctx, t, "agent", testcontainers.ContainerRequest{
		Image: gobfdImage, Entrypoint: []string{"/bin/gobfd-haproxy-agent"},
		Cmd: []string{"--config", "/etc/gobfd-haproxy-agent/config.yml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      contract.agentConfig,
			ContainerFilePath: "/etc/gobfd-haproxy-agent/config.yml", FileMode: 0o644,
		}},
		WaitingFor:         wait.ForLog("gobfd-haproxy-agent started").WithStartupTimeout(30 * time.Second),
		HostConfigModifier: sharedNetworkNamespace(topology.monitorID),
	}); agentErr != nil {
		return agentErr
	}
	capture, err := topology.startContainer(ctx, t, "capture", testcontainers.ContainerRequest{
		Image:      tsharkImage,
		WaitingFor: wait.ForExec([]string{"test", "-f", "/captures/bfd.pcapng"}).WithStartupTimeout(20 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addBFDContainerCapabilities(hostConfig)
			sharedNetworkNamespace(topology.monitorID)(hostConfig)
		},
	})
	if err != nil {
		return err
	}
	topology.capture = capture
	tsharkImageID, err := requireHAProxyContainerImageID(ctx, capture, "tshark")
	if err != nil {
		return err
	}
	if recordErr := topology.recordOwnedImageID(tsharkImageID); recordErr != nil {
		return recordErr
	}
	analyzer, err := topology.startContainer(ctx, t, "analyzer", testcontainers.ContainerRequest{
		Image: tsharkImageID, Entrypoint: []string{"sleep"}, Cmd: []string{"infinity"},
		WaitingFor: wait.ForExec([]string{"test", "-d", "/captures"}).WithStartupTimeout(20 * time.Second),
	})
	if err != nil {
		return err
	}
	topology.analyzer = analyzer
	return nil
}

func (topology *haproxyHealthTopology) startBackends(
	ctx context.Context, t *testing.T, gobfdImage string,
) error {
	t.Helper()
	backend1, err := topology.startNginx(ctx, t, "backend1", topology.contract.backend1IP, "backend1")
	if err != nil {
		return err
	}
	topology.backend1, topology.backend1ID = backend1, backend1.GetContainerID()
	backend1BFD, err := topology.startBFDSidecar(
		ctx, t, "backend1-bfd", gobfdImage, topology.backend1ID, topology.contract.backend1Config,
	)
	if err != nil {
		return err
	}
	topology.backend1BFDID = backend1BFD.GetContainerID()
	backend2, err := topology.startNginx(ctx, t, "backend2", topology.contract.backend2IP, "backend2")
	if err != nil {
		return err
	}
	topology.backend2, topology.backend2ID = backend2, backend2.GetContainerID()
	backend2BFD, err := topology.startBFDSidecar(
		ctx, t, "backend2-bfd", gobfdImage, topology.backend2ID, topology.contract.backend2Config,
	)
	if err != nil {
		return err
	}
	topology.backend2BFDID = backend2BFD.GetContainerID()
	t.Cleanup(func() {
		if !topology.backend1BFDPaused {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if unpauseErr := topology.client.Unpause(cleanupCtx, topology.backend1BFDID); unpauseErr != nil {
			t.Logf("best-effort exact backend1-bfd unpause before removal: %v", unpauseErr)
		}
	})
	topology.armEvidenceCleanup(t)
	return nil
}

func (topology *haproxyHealthTopology) startNginx(
	ctx context.Context, t *testing.T, role, address, identity string,
) (testcontainers.Container, error) {
	t.Helper()
	return topology.startContainer(ctx, t, role, testcontainers.ContainerRequest{
		Image: topology.contract.nginxImage, Networks: []string{topology.networkName},
		ExposedPorts: []string{haproxyHTTPPort},
		Files: []testcontainers.ContainerFile{{
			Reader:            strings.NewReader(identity + "\n"),
			ContainerFilePath: "/usr/share/nginx/html/index.html", FileMode: 0o644,
		}},
		WaitingFor:               wait.ForHTTP("/").WithPort(haproxyHTTPPort).WithStartupTimeout(30 * time.Second),
		EndpointSettingsModifier: staticHAProxyIP(topology.networkName, address),
	})
}

func (topology *haproxyHealthTopology) startBFDSidecar(
	ctx context.Context, t *testing.T, role, image, namespaceID, configPath string,
) (testcontainers.Container, error) {
	t.Helper()
	return topology.startContainer(ctx, t, role, testcontainers.ContainerRequest{
		Image: image, Cmd: []string{"-config", "/etc/gobfd/gobfd.yml"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath: configPath, ContainerFilePath: "/etc/gobfd/gobfd.yml", FileMode: 0o644,
		}},
		WaitingFor:     wait.ForLog("metrics server listening").WithStartupTimeout(30 * time.Second),
		ConfigModifier: func(config *container.Config) { config.User = "0:0" },
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			addBFDContainerCapabilities(hostConfig)
			sharedNetworkNamespace(namespaceID)(hostConfig)
		},
	})
}

func (topology *haproxyHealthTopology) startHAProxy(ctx context.Context, t *testing.T) error {
	t.Helper()
	baseConfig, err := os.ReadFile(topology.contract.haproxyConfig)
	if err != nil {
		return fmt.Errorf("read operational HAProxy config: %w", err)
	}
	config, err := deriveHAProxyConfig(baseConfig)
	if err != nil {
		return err
	}
	haproxy, err := topology.startContainer(ctx, t, "haproxy", testcontainers.ContainerRequest{
		Image: topology.contract.haproxyImage, Networks: []string{topology.networkName},
		ExposedPorts: []string{haproxyHTTPPort, haproxyStatsPort},
		Files: []testcontainers.ContainerFile{{
			Reader:            bytes.NewReader(config),
			ContainerFilePath: "/usr/local/etc/haproxy/haproxy.cfg", FileMode: 0o644,
		}},
		WaitingFor:               wait.ForListeningPort(haproxyHTTPPort).WithStartupTimeout(30 * time.Second),
		ConfigModifier:           func(config *container.Config) { config.User = "0:0" },
		EndpointSettingsModifier: staticHAProxyIP(topology.networkName, topology.contract.haproxyIP),
	})
	if err != nil {
		return err
	}
	topology.haproxy = haproxy
	return nil
}

func (topology *haproxyHealthTopology) startContainer(
	ctx context.Context, t *testing.T, role string, request testcontainers.ContainerRequest,
) (testcontainers.Container, error) {
	t.Helper()
	name := topology.networkName + "-" + role
	request.Name, request.Labels = name, topology.labels(role)
	if err := topology.recordOwnedContainer(name, ""); err != nil {
		return nil, err
	}
	testContainer, err := containertest.Run(ctx, t, request)
	var recordErr error
	if testContainer != nil {
		topology.containers = append(topology.containers, namedContainer{name: role, container: testContainer})
		topology.roleContainers[role] = testContainer
		recordErr = topology.recordOwnedContainer(name, testContainer.GetContainerID())
	}
	topology.armEvidenceCleanup(t)
	if err != nil {
		err = fmt.Errorf("start HAProxy health %s container: %w", role, err)
	}
	return testContainer, errors.Join(err, recordErr)
}

func (topology *haproxyHealthTopology) labels(role string) map[string]string {
	return map[string]string{
		"io.gobfd.test": "haproxy-health-testcontainers",
		"io.gobfd.run":  topology.networkName,
		"io.gobfd.role": role,
	}
}

func addBFDContainerCapabilities(hostConfig *container.HostConfig) {
	hostConfig.CapDrop = []string{"ALL"}
	hostConfig.CapAdd = []string{"NET_RAW", "NET_ADMIN"}
}

func sharedNetworkNamespace(containerID string) func(*container.HostConfig) {
	return func(hostConfig *container.HostConfig) {
		hostConfig.NetworkMode = container.NetworkMode("container:" + containerID)
	}
}

func staticHAProxyIP(networkName, address string) func(map[string]*network.EndpointSettings) {
	return func(settings map[string]*network.EndpointSettings) {
		endpoint := settings[networkName]
		if endpoint == nil {
			endpoint = new(network.EndpointSettings)
			settings[networkName] = endpoint
		}
		endpoint.IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(address)}
	}
}

func (topology *haproxyHealthTopology) resolveHostEndpoints(ctx context.Context) error {
	host, err := topology.monitor.Host(ctx)
	if err != nil {
		return fmt.Errorf("resolve monitor host: %w", err)
	}
	agent1Mapped, err := topology.monitor.MappedPort(ctx, agent1Port)
	if err != nil {
		return fmt.Errorf("resolve monitor agent port 9990: %w", err)
	}
	agent2Mapped, err := topology.monitor.MappedPort(ctx, agent2Port)
	if err != nil {
		return fmt.Errorf("resolve monitor agent port 9991: %w", err)
	}
	topology.agent1Address = net.JoinHostPort(host, agent1Mapped.Port())
	topology.agent2Address = net.JoinHostPort(host, agent2Mapped.Port())
	topology.haproxyURL, err = topology.haproxy.PortEndpoint(ctx, haproxyHTTPPort, "http")
	if err != nil {
		return fmt.Errorf("resolve HAProxy mapped HTTP endpoint: %w", err)
	}
	statsEndpoint, err := topology.haproxy.PortEndpoint(ctx, haproxyStatsPort, "http")
	if err != nil {
		return fmt.Errorf("resolve HAProxy mapped stats endpoint: %w", err)
	}
	topology.statsURL = statsEndpoint + "/stats;csv"
	topology.backend1URL, err = topology.backend1.PortEndpoint(ctx, haproxyHTTPPort, "http")
	if err != nil {
		return fmt.Errorf("resolve backend1 direct mapped endpoint: %w", err)
	}
	return nil
}

func (topology *haproxyHealthTopology) waitForInitialHealth(t *testing.T, timeout time.Duration) {
	t.Helper()
	topology.waitFor(t, "both BFD/agent/server paths Up and both identities served", timeout,
		func(ctx context.Context) (bool, error) {
			for _, peer := range []string{topology.contract.backend1IP, topology.contract.backend2IP} {
				state, err := topology.bfdSession(ctx, peer)
				if err != nil || state.LocalState != "Up" || state.RemoteState != "Up" {
					return false, err
				}
			}
			for _, address := range []string{topology.agent1Address, topology.agent2Address} {
				response, err := queryAgent(ctx, address)
				if err != nil || response != "up ready" {
					return false, err
				}
			}
			servers, err := topology.haproxyServers(ctx)
			if err != nil || !servers["srv1"].Eligible || !servers["srv2"].Eligible {
				return false, err
			}
			return topology.servesBothIdentities(ctx)
		},
	)
}

func (topology *haproxyHealthTopology) waitForBackend1Excluded(t *testing.T, timeout time.Duration) {
	t.Helper()
	topology.waitFor(t, "backend1 BFD/agent/HAProxy exclusion with nginx1 reachable", timeout,
		func(ctx context.Context) (bool, error) {
			state1, err := topology.bfdSession(ctx, topology.contract.backend1IP)
			if err != nil || state1.LocalState != "Down" {
				return false, err
			}
			state2, err := topology.bfdSession(ctx, topology.contract.backend2IP)
			if err != nil || state2.LocalState != "Up" || state2.RemoteState != "Up" {
				return false, err
			}
			agent1, err := queryAgent(ctx, topology.agent1Address)
			if err != nil || agent1 != "down" {
				return false, err
			}
			agent2, err := queryAgent(ctx, topology.agent2Address)
			if err != nil || agent2 != "up ready" {
				return false, err
			}
			servers, err := topology.haproxyServers(ctx)
			if err != nil || servers["srv1"].Eligible || !servers["srv2"].Eligible {
				return false, err
			}
			identity, err := queryIdentity(ctx, topology.backend1URL)
			if err != nil || identity != "backend1" {
				return false, err
			}
			return topology.servesOnlyIdentity(ctx, "backend2", 4)
		},
	)
}

func (topology *haproxyHealthTopology) waitFor(
	t *testing.T, description string, timeout time.Duration, condition func(context.Context) (bool, error),
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

func (topology *haproxyHealthTopology) bfdSession(ctx context.Context, peer string) (bfdSessionView, error) {
	result, err := topology.client.Exec(ctx, topology.monitorID, []string{
		"/bin/gobfdctl", "--addr", "127.0.0.1:50052", "--format", "json", "session", "show", peer,
	})
	if err != nil {
		return bfdSessionView{}, fmt.Errorf(
			"query monitor session %s through exact container %s: %w", peer, topology.monitorID, err,
		)
	}
	return parseBFDSessionJSON(result.Stdout, peer)
}

func queryAgent(ctx context.Context, address string) (string, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", fmt.Errorf("connect to mapped agent endpoint %s: %w", address, err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if deadlineErr := connection.SetReadDeadline(deadline); deadlineErr != nil {
			return "", fmt.Errorf("set agent response deadline: %w", deadlineErr)
		}
	}
	payload, err := io.ReadAll(io.LimitReader(connection, 64))
	if err != nil {
		return "", fmt.Errorf("read mapped agent response from %s: %w", address, err)
	}
	switch string(payload) {
	case "up ready\n":
		return "up ready", nil
	case "down\n":
		return "down", nil
	default:
		return "", fmt.Errorf("agent response from %s = %q", address, payload)
	}
}

func (topology *haproxyHealthTopology) haproxyServers(ctx context.Context) (map[string]haproxyServerState, error) {
	body, err := queryHTTP(ctx, topology.statsURL)
	if err != nil {
		return nil, fmt.Errorf("query mapped HAProxy stats: %w", err)
	}
	return parseHAProxyStats(body, "http_back", []string{"srv1", "srv2"})
}

func (topology *haproxyHealthTopology) servesBothIdentities(ctx context.Context) (bool, error) {
	seen := make(map[string]bool, 2)
	for range 6 {
		identity, err := queryIdentity(ctx, topology.haproxyURL)
		if err != nil {
			return false, err
		}
		if identity != "backend1" && identity != "backend2" {
			return false, fmt.Errorf("HAProxy served unknown backend identity %q", identity)
		}
		seen[identity] = true
	}
	return seen["backend1"] && seen["backend2"], nil
}

func (topology *haproxyHealthTopology) servesOnlyIdentity(
	ctx context.Context, want string, attempts int,
) (bool, error) {
	for range attempts {
		identity, err := queryIdentity(ctx, topology.haproxyURL)
		if err != nil {
			return false, err
		}
		if identity != want {
			return false, nil
		}
	}
	return true, nil
}

func queryIdentity(ctx context.Context, endpoint string) (string, error) {
	body, err := queryHTTP(ctx, endpoint)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(body), nil
}

func queryHTTP(ctx context.Context, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create HTTP request for %s: %w", endpoint, err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxDiagnosticBytes+1))
	if readErr != nil {
		return "", fmt.Errorf("read HTTP response from %s: %w", endpoint, readErr)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s status %d: %s", endpoint, response.StatusCode, body)
	}
	if len(body) == 0 || len(body) > maxDiagnosticBytes {
		return "", fmt.Errorf("GET %s body size %d outside 1..%d", endpoint, len(body), maxDiagnosticBytes)
	}
	return string(body), nil
}

func (topology *haproxyHealthTopology) assertRuntimeContract(ctx context.Context) error {
	for _, owner := range []struct {
		name, containerID, wantIPv4 string
	}{
		{name: "monitor", containerID: topology.monitorID, wantIPv4: topology.contract.monitorIP},
		{name: "haproxy", containerID: topology.haproxy.GetContainerID(), wantIPv4: topology.contract.haproxyIP},
		{name: "backend1", containerID: topology.backend1ID, wantIPv4: topology.contract.backend1IP},
		{name: "backend2", containerID: topology.backend2ID, wantIPv4: topology.contract.backend2IP},
	} {
		inspection, err := topology.inspectSelectedEndpointContainer(ctx, owner.containerID)
		if err != nil {
			return fmt.Errorf("inspect selected-endpoint %s runtime owner: %w", owner.name, err)
		}
		if err := validateRuntimeOwnerIPv4(
			owner.name, inspection, topology.networkName, owner.wantIPv4,
		); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		name, wantMode string
	}{
		{name: "agent", wantMode: "container:" + topology.monitorID},
		{name: "capture", wantMode: "container:" + topology.monitorID},
		{name: "backend1-bfd", wantMode: "container:" + topology.backend1ID},
		{name: "backend2-bfd", wantMode: "container:" + topology.backend2ID},
	} {
		inspection, err := topology.inspectSelectedEndpointContainer(
			ctx, topology.roleContainers[item.name].GetContainerID(),
		)
		if err != nil {
			return fmt.Errorf("inspect %s topology contract: %w", item.name, err)
		}
		if string(inspection.HostConfig.NetworkMode) != item.wantMode {
			return fmt.Errorf(
				"%s network mode = %q, want immutable %q", item.name, inspection.HostConfig.NetworkMode, item.wantMode,
			)
		}
	}
	for _, item := range topology.containers {
		inspection, err := topology.inspectSelectedEndpointContainer(ctx, item.container.GetContainerID())
		if err != nil {
			return fmt.Errorf("inspect %s mount contract: %w", item.name, err)
		}
		if len(inspection.Mounts) != 0 {
			return fmt.Errorf("%s mounts = %+v, want no host mounts", item.name, inspection.Mounts)
		}
	}
	return nil
}

func (topology *haproxyHealthTopology) inspectSelectedEndpointContainer(
	ctx context.Context, containerID string,
) (*container.InspectResponse, error) {
	raw, err := topology.client.Inspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect exact container %s: %w", containerID, err)
	}
	var inspection container.InspectResponse
	if err := json.Unmarshal(raw, &inspection); err != nil {
		return nil, fmt.Errorf("decode exact container %s inspection: %w", containerID, err)
	}
	return &inspection, nil
}

func validateRuntimeOwnerIPv4(
	role string, inspection *container.InspectResponse, networkName, wantIPv4 string,
) error {
	if inspection == nil || inspection.NetworkSettings == nil {
		return fmt.Errorf("selected-endpoint %s inspection lacks network settings", role)
	}
	endpoint := inspection.NetworkSettings.Networks[networkName]
	if endpoint == nil {
		return fmt.Errorf("selected-endpoint %s inspection lacks exact network %s", role, networkName)
	}
	if got := endpoint.IPAddress.String(); got != wantIPv4 {
		return fmt.Errorf("selected-endpoint %s IPv4 = %q, want %q", role, got, wantIPv4)
	}
	return nil
}

func requireHAProxyContainerImageID(
	ctx context.Context, testContainer testcontainers.Container, description string,
) (string, error) {
	inspection, err := testContainer.Inspect(ctx)
	if err != nil {
		return "", fmt.Errorf("inspect %s content-addressed image: %w", description, err)
	}
	imageID := strings.TrimPrefix(inspection.Image, "sha256:")
	if len(imageID) != 64 {
		return "", fmt.Errorf("%s image ID = %q, want sha256 content ID", description, inspection.Image)
	}
	for _, character := range imageID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", fmt.Errorf("%s image ID = %q, want lowercase hexadecimal", description, inspection.Image)
		}
	}
	return inspection.Image, nil
}
