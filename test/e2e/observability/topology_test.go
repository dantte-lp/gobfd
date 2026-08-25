//go:build e2e_observability_testcontainers

package observability_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dantte-lp/gobfd/test/internal/containertest"
	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

type observabilityTopology struct {
	contract                     observabilityContract
	endpoint                     string
	root                         string
	runID                        string
	reportDir                    string
	networkName                  string
	containerNames               []string
	containerIDs                 []string
	containers                   []namedContainer
	imageNames                   []string
	imageIDs                     []string
	volumeNames                  []string
	gobfdID                      string
	frrID                        string
	prometheusID                 string
	grafanaID                    string
	prometheusURL                string
	grafanaURL                   string
	capture                      testcontainers.Container
	analyzer                     testcontainers.Container
	client                       *podmanapi.Client
	frrPaused                    bool
	packetEvidence               bool
	startupError                 string
	evidenceOnce                 sync.Once
	resourceSnapshotBeforeRename func() error
}

type namedContainer struct {
	name      string
	container testcontainers.Container
}

type observabilityResourceSnapshot struct {
	ContainerNames []string `json:"container_names"`
	ContainerIDs   []string `json:"container_ids"`
	ImageNames     []string `json:"image_names"`
	ImageIDs       []string `json:"image_ids"`
	VolumeNames    []string `json:"volume_names"`
	RuntimeImages  []string `json:"runtime_images"`
	NetworkName    string   `json:"network_name"`
	StartupError   string   `json:"startup_error,omitempty"`
}

type ownedImageClient interface {
	ImageExists(ctx context.Context, image string) (bool, error)
	ImageID(ctx context.Context, image string) (string, error)
	RemoveImage(ctx context.Context, image string) error
}

func TestObservabilityTestcontainers(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	endpoint := containertest.RequirePodman(t)
	t.Setenv("PODMAN_HOST", endpoint)
	topology := newObservabilityTopology(t, endpoint)
	registerFinalSummary(t.Cleanup, t.Failed, topology.writeSummary, func(err error) {
		t.Errorf("write final observability summary: %v", err)
	})

	if !t.Run("BFD metrics alert and Grafana provisioning survive FRR failure recovery", func(t *testing.T) {
		topology.armEvidenceCleanup(t.Context(), t)
		if err := startObservabilityTopology(t, topology); err != nil {
			if recordErr := topology.recordStartupFailure(err); recordErr != nil {
				t.Errorf("record observability startup failure: %v", recordErr)
			}
			t.Fatalf("start observability topology: %v", err)
		}

		topology.waitForInitialHealth(t, 2*time.Minute)
		pauseCtx, pauseCancel := context.WithTimeout(t.Context(), 5*time.Second)
		if err := topology.client.Pause(pauseCtx, topology.frrID); err != nil {
			pauseCancel()
			t.Fatalf("pause exact FRR container %s: %v", topology.frrID, err)
		}
		pauseCancel()
		topology.frrPaused = true
		topology.waitForFailureAlert(t, 90*time.Second)

		unpauseCtx, unpauseCancel := context.WithTimeout(t.Context(), 5*time.Second)
		if err := topology.client.Unpause(unpauseCtx, topology.frrID); err != nil {
			unpauseCancel()
			t.Fatalf("unpause exact FRR container %s: %v", topology.frrID, err)
		}
		unpauseCancel()
		topology.frrPaused = false
		// The unchanged rule uses increase(...[1m]); allow one full range
		// window plus the 15-second evaluation cadence without fixed sleeps.
		topology.waitForRecoveredHealth(t, 2*time.Minute)
	}) {
		t.Error("observability testcontainers lifecycle failed")
	}

	for _, containerID := range topology.containerIDs {
		if containerID != "" {
			containertest.AssertContainerRemoved(t, endpoint, containerID)
		}
	}
	assertContainerNamesRemoved(t, endpoint, topology.containerNames)
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
	for _, volumeName := range topology.volumeNames {
		containertest.AssertVolumeRemoved(t, endpoint, volumeName)
	}
}

func newObservabilityTopology(t *testing.T, endpoint string) *observabilityTopology {
	t.Helper()
	root := repositoryRoot(t)
	runID, reportDir := observabilityReportDirectory(t, root)
	client, err := podmanapi.NewClient(strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatalf("create exact-endpoint Podman client: %v", err)
	}
	topology := &observabilityTopology{
		contract: newObservabilityContract(root), endpoint: endpoint, root: root,
		runID: runID, reportDir: reportDir, client: client,
	}
	if err := initializeDiagnostics(reportDir); err != nil {
		t.Fatalf("initialize observability diagnostics: %v", err)
	}
	if err := topology.writeResourceSnapshot(); err != nil {
		t.Fatalf("initialize observability resource ownership: %v", err)
	}
	return topology
}

func startObservabilityTopology(t *testing.T, topology *observabilityTopology) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	t.Cleanup(cancel)
	topology.armEvidenceCleanup(ctx, t)
	contract := topology.contract
	buildID := time.Now().UnixNano()
	gobfdImage, err := buildOwnedImage(
		ctx, t, topology, prepareGoBFDBuildContext(t, topology.root),
		fmt.Sprintf("localhost/gobfd-observability:test-%d", buildID),
	)
	if err != nil {
		return fmt.Errorf("build bounded GoBFD observability image: %w", err)
	}
	tsharkImage, err := buildOwnedImage(
		ctx, t, topology, prepareTsharkBuildContext(t, contract.tsharkContainerfile),
		fmt.Sprintf("localhost/gobfd-observability-tshark:test-%d", buildID),
	)
	if err != nil {
		return fmt.Errorf("build bounded observability tshark image: %w", err)
	}
	networkName := fmt.Sprintf("gobfd-observability-%d", buildID)
	if recordErr := topology.recordOwnedNetwork(networkName); recordErr != nil {
		return recordErr
	}
	//nolint:staticcheck // Explicit ProviderPodman with static IPAM requires this v0.44 API.
	_, err = containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name:   networkName,
		Driver: "bridge",
		Labels: map[string]string{"io.gobfd.test": "observability", "io.gobfd.run": topology.runID},
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet: netip.MustParsePrefix(contract.subnet), Gateway: netip.MustParseAddr(contract.gateway),
		}}},
	})
	topology.armEvidenceCleanup(ctx, t)
	if err != nil {
		return fmt.Errorf("create observability Podman network: %w", err)
	}
	labels := map[string]string{"io.gobfd.test": "observability", "io.gobfd.run": topology.runID}

	gobfd, err := startObservabilityContainer(ctx, t, topology, "gobfd", networkName+"-gobfd",
		testcontainers.ContainerRequest{
			Image: gobfdImage, Labels: labels, Networks: []string{networkName},
			Cmd: []string{"-config", "/etc/gobfd/gobfd.yml"},
			Files: []testcontainers.ContainerFile{{
				HostFilePath: contract.gobfdConfig, ContainerFilePath: "/etc/gobfd/gobfd.yml", FileMode: 0o644,
			}},
			WaitingFor:     wait.ForLog("metrics server listening").WithStartupTimeout(30 * time.Second),
			ConfigModifier: func(config *container.Config) { config.User = "0:0" },
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN")
			},
			EndpointSettingsModifier: staticIP(networkName, contract.gobfdIP),
		})
	if err != nil {
		return err
	}
	topology.gobfdID = gobfd.GetContainerID()

	capture, err := startObservabilityContainer(ctx, t, topology, "tshark-capture", networkName+"-tshark-capture",
		testcontainers.ContainerRequest{
			Image: tsharkImage, Labels: labels,
			WaitingFor: wait.ForExec([]string{"test", "-f", "/captures/bfd.pcapng"}).WithStartupTimeout(20 * time.Second),
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN")
				hostConfig.NetworkMode = container.NetworkMode("container:" + topology.gobfdID)
			},
		})
	if err != nil {
		return err
	}
	topology.capture = capture

	frr, err := startObservabilityContainer(ctx, t, topology, "frr", networkName+"-frr",
		testcontainers.ContainerRequest{
			Image: contract.frrImage, Labels: labels, Networks: []string{networkName},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: contract.frrDaemons, ContainerFilePath: "/etc/frr/daemons", FileMode: 0o644},
				{HostFilePath: contract.frrConfig, ContainerFilePath: "/etc/frr/frr.conf", FileMode: 0o644},
			},
			WaitingFor: wait.ForExec([]string{"vtysh", "-c", "show version"}).WithStartupTimeout(30 * time.Second),
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.CapAdd = append(hostConfig.CapAdd, "NET_RAW", "NET_ADMIN", "SYS_ADMIN")
			},
			EndpointSettingsModifier: staticIP(networkName, contract.frrIP),
		})
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
	topology.armEvidenceCleanup(ctx, t)

	prometheus, err := startObservabilityContainer(ctx, t, topology, "prometheus", networkName+"-prometheus",
		testcontainers.ContainerRequest{
			Image: contract.prometheusImage, Labels: labels, Networks: []string{networkName},
			ExposedPorts: []string{prometheusPort},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: contract.prometheusConfig, ContainerFilePath: "/etc/prometheus/prometheus.yml", FileMode: 0o644},
				{HostFilePath: contract.alertRules, ContainerFilePath: "/etc/prometheus/alert-rules.yml", FileMode: 0o644},
			},
			WaitingFor:               wait.ForHTTP("/-/ready").WithPort(prometheusPort).WithStartupTimeout(45 * time.Second),
			EndpointSettingsModifier: staticIP(networkName, contract.prometheusIP),
			LifecycleHooks: []testcontainers.ContainerLifecycleHooks{{
				PreStarts: []testcontainers.ContainerHook{func(hookCtx context.Context, created testcontainers.Container) error {
					inspection, inspectErr := created.Inspect(hookCtx)
					if inspectErr != nil {
						return fmt.Errorf("inspect Prometheus mounts before start: %w", inspectErr)
					}
					volumeName, mountErr := validateObservabilityMounts("prometheus", inspection.Mounts)
					if mountErr != nil {
						return mountErr
					}
					return topology.recordOwnedVolume(volumeName)
				}},
			}},
		})
	if err != nil {
		return err
	}
	topology.prometheusID = prometheus.GetContainerID()
	topology.prometheusURL, err = prometheus.PortEndpoint(ctx, prometheusPort, "http")
	if err != nil {
		return fmt.Errorf("resolve Prometheus mapped endpoint: %w", err)
	}

	datasource := deriveGrafanaDatasource(t, contract.grafanaDatasource, contract.prometheusIP)
	grafana, err := startObservabilityContainer(ctx, t, topology, "grafana", networkName+"-grafana",
		testcontainers.ContainerRequest{
			Image: contract.grafanaImage, Labels: labels, Networks: []string{networkName},
			ExposedPorts: []string{grafanaPort},
			Env: map[string]string{
				"GF_SECURITY_ADMIN_USER": "admin", "GF_SECURITY_ADMIN_PASSWORD": "admin",
			},
			Files: []testcontainers.ContainerFile{
				{
					HostFilePath:      datasource,
					ContainerFilePath: "/etc/grafana/provisioning/datasources/prometheus.yml",
					FileMode:          0o644,
				},
				{
					HostFilePath:      contract.grafanaDashboardProvider,
					ContainerFilePath: "/etc/grafana/provisioning/dashboards/dashboards.yml",
					FileMode:          0o644,
				},
				{
					HostFilePath:      contract.grafanaDashboard,
					ContainerFilePath: "/var/lib/grafana/dashboards/bfd.json",
					FileMode:          0o644,
				},
			},
			WaitingFor:               wait.ForHTTP("/api/health").WithPort(grafanaPort).WithStartupTimeout(45 * time.Second),
			EndpointSettingsModifier: staticIP(networkName, contract.grafanaIP),
		})
	if err != nil {
		return err
	}
	topology.grafanaID = grafana.GetContainerID()
	topology.grafanaURL, err = grafana.PortEndpoint(ctx, grafanaPort, "http")
	if err != nil {
		return fmt.Errorf("resolve Grafana mapped endpoint: %w", err)
	}

	tsharkImageID := topology.imageIDs[len(topology.imageIDs)-1]
	analyzer, err := startObservabilityContainer(ctx, t, topology, "tshark-analyzer", networkName+"-tshark-analyzer",
		testcontainers.ContainerRequest{
			Image: tsharkImageID, Labels: labels, Entrypoint: []string{"sleep"}, Cmd: []string{"infinity"},
			WaitingFor: wait.ForExec([]string{"test", "-d", "/captures"}).WithStartupTimeout(20 * time.Second),
		})
	if err != nil {
		return err
	}
	topology.analyzer = analyzer
	if err := topology.assertRuntimeContract(ctx); err != nil {
		return fmt.Errorf("validate selected-endpoint observability runtime contract: %w", err)
	}
	return nil
}

func startObservabilityContainer(
	ctx context.Context,
	t *testing.T,
	topology *observabilityTopology,
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
		topology.containers = append(topology.containers, namedContainer{name: role, container: testContainer})
		recordErr = topology.recordOwnedContainer(name, testContainer.GetContainerID())
	}
	topology.armEvidenceCleanup(ctx, t)
	if err != nil {
		err = fmt.Errorf("start observability %s container: %w", role, err)
	}
	return testContainer, errors.Join(err, recordErr)
}

func staticIP(networkName, address string) func(map[string]*network.EndpointSettings) {
	return func(settings map[string]*network.EndpointSettings) {
		endpoint := settings[networkName]
		if endpoint == nil {
			endpoint = new(network.EndpointSettings)
			settings[networkName] = endpoint
		}
		endpoint.IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(address)}
	}
}

func (topology *observabilityTopology) assertRuntimeContract(ctx context.Context) error {
	wantAddresses := map[string]string{
		"gobfd": topology.contract.gobfdIP, "frr": topology.contract.frrIP,
		"prometheus": topology.contract.prometheusIP, "grafana": topology.contract.grafanaIP,
	}
	for _, item := range topology.containers {
		inspection, err := item.container.Inspect(ctx)
		if err != nil {
			return fmt.Errorf("inspect exact %s runtime container: %w", item.name, err)
		}
		volumeName, mountErr := validateObservabilityMounts(item.name, inspection.Mounts)
		if mountErr != nil {
			return mountErr
		}
		if item.name == "prometheus" &&
			(len(topology.volumeNames) != 1 || topology.volumeNames[0] != volumeName) {
			return fmt.Errorf(
				"exact Prometheus volume %q does not match recorded ownership %v",
				volumeName, topology.volumeNames,
			)
		}
		wantIP, needsIP := wantAddresses[item.name]
		if needsIP {
			if inspection.NetworkSettings == nil || inspection.NetworkSettings.Networks[topology.networkName] == nil ||
				inspection.NetworkSettings.Networks[topology.networkName].IPAddress.String() != wantIP {
				return fmt.Errorf("exact %s container does not have static IPv4 %s", item.name, wantIP)
			}
		}
		if item.name == "tshark-capture" &&
			string(inspection.HostConfig.NetworkMode) != "container:"+topology.gobfdID {
			return fmt.Errorf("capture network mode = %q, want immutable GoBFD ID", inspection.HostConfig.NetworkMode)
		}
	}
	return nil
}

func validateObservabilityMounts(role string, mounts []container.MountPoint) (string, error) {
	if role != "prometheus" {
		if len(mounts) != 0 {
			return "", fmt.Errorf("exact %s container has mounts: %+v", role, mounts)
		}
		return "", nil
	}
	if len(mounts) != 1 {
		return "", fmt.Errorf("exact Prometheus container mounts = %+v, want one anonymous data volume", mounts)
	}
	data := mounts[0]
	if data.Type != mount.TypeVolume || data.Destination != "/prometheus" ||
		!data.RW || strings.TrimSpace(data.Name) == "" {
		return "", fmt.Errorf(
			"exact Prometheus mount = %+v, want RW anonymous volume at /prometheus with nonempty name",
			data,
		)
	}
	return data.Name, nil
}

func (topology *observabilityTopology) waitForInitialHealth(t *testing.T, timeout time.Duration) {
	t.Helper()
	topology.waitFor(t, "BFD Up, Prometheus target/series, and Grafana provisioning", timeout,
		func(ctx context.Context) (bool, error) {
			state, sessionJSON, err := topology.bfdSession(ctx)
			if err != nil || state.LocalState != "Up" || state.RemoteState != "Up" {
				return false, err
			}
			targets, targetsJSON, err := topology.prometheusTargets(ctx)
			if err != nil || !prometheusTargetHealthy(targets, topology.contract) {
				return false, err
			}
			query, queryJSON, err := topology.prometheusQuery(ctx)
			if err != nil || !prometheusSessionSeriesHealthy(query, topology.contract) {
				return false, err
			}
			baseline, baselineJSON, err := topology.prometheusTransitionBaseline(ctx)
			if err != nil || !prometheusTransitionBaselineReady(baseline, topology.contract) {
				return false, err
			}
			health, healthJSON, err := topology.grafanaHealth(ctx)
			if err != nil || health.Database != "ok" || health.Version != "13.2.0" {
				return false, err
			}
			datasources, datasourcesJSON, err := topology.grafanaDatasources(ctx)
			if err != nil || !grafanaDatasourceReady(datasources, topology.contract) {
				return false, err
			}
			dashboards, searchJSON, err := topology.grafanaSearch(ctx)
			if err != nil || !grafanaDashboardReady(dashboards) {
				return false, err
			}
			return true, errors.Join(
				topology.writeJSONEvidence("bfd-session-initial.json", sessionJSON),
				topology.writeJSONEvidence("prometheus-targets-initial.json", targetsJSON),
				topology.writeJSONEvidence("prometheus-query.json", queryJSON),
				topology.writeJSONEvidence("prometheus-transition-baseline.json", baselineJSON),
				topology.writeJSONEvidence("grafana-health.json", healthJSON),
				topology.writeJSONEvidence("grafana-datasources.json", datasourcesJSON),
				topology.writeJSONEvidence("grafana-search.json", searchJSON),
			)
		})
}

func (topology *observabilityTopology) waitForFailureAlert(t *testing.T, timeout time.Duration) {
	t.Helper()
	topology.waitFor(t, "BFD Down with healthy scrape target and firing transition alert", timeout,
		func(ctx context.Context) (bool, error) {
			state, sessionJSON, err := topology.bfdSession(ctx)
			if err != nil || state.LocalState != "Down" {
				return false, err
			}
			targets, targetsJSON, err := topology.prometheusTargets(ctx)
			if err != nil || !prometheusTargetHealthy(targets, topology.contract) {
				return false, err
			}
			rules, rulesJSON, err := topology.prometheusRules(ctx)
			if err != nil {
				return false, err
			}
			stateName, found := prometheusAlertState(rules)
			if !found || stateName != "firing" {
				return false, nil
			}
			return true, errors.Join(
				topology.writeJSONEvidence("bfd-session-down.json", sessionJSON),
				topology.writeJSONEvidence("prometheus-targets-during-failure.json", targetsJSON),
				topology.writeJSONEvidence("prometheus-rules-firing.json", rulesJSON),
			)
		})
}

func (topology *observabilityTopology) waitForRecoveredHealth(t *testing.T, timeout time.Duration) {
	t.Helper()
	topology.waitFor(t, "BFD Up, healthy scrape target, and inactive transition alert", timeout,
		func(ctx context.Context) (bool, error) {
			state, sessionJSON, err := topology.bfdSession(ctx)
			if err != nil || state.LocalState != "Up" || state.RemoteState != "Up" {
				return false, err
			}
			targets, targetsJSON, err := topology.prometheusTargets(ctx)
			if err != nil || !prometheusTargetHealthy(targets, topology.contract) {
				return false, err
			}
			rules, rulesJSON, err := topology.prometheusRules(ctx)
			if err != nil {
				return false, err
			}
			stateName, found := prometheusAlertState(rules)
			if !found || stateName != "inactive" {
				return false, nil
			}
			return true, errors.Join(
				topology.writeJSONEvidence("bfd-session-recovered.json", sessionJSON),
				topology.writeJSONEvidence("prometheus-targets-recovered.json", targetsJSON),
				topology.writeJSONEvidence("prometheus-rules-resolved.json", rulesJSON),
			)
		})
}

func (topology *observabilityTopology) waitFor(
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

func (topology *observabilityTopology) bfdSession(ctx context.Context) (bfdSessionView, []byte, error) {
	result, err := topology.client.Exec(ctx, topology.gobfdID, []string{
		"/bin/gobfdctl", "--addr", "127.0.0.1:50052", "--format", "json",
		"session", "show", topology.contract.frrIP,
	})
	if err != nil {
		return bfdSessionView{}, nil, fmt.Errorf(
			"query BFD session through exact container %s: %w", topology.gobfdID, err,
		)
	}
	state, err := parseBFDSessionJSON(result.Stdout, topology.contract.frrIP)
	return state, []byte(result.Stdout), err
}

func (topology *observabilityTopology) prometheusTargets(ctx context.Context) (
	prometheusTargetResponse, []byte, error,
) {
	var response prometheusTargetResponse
	body, err := getJSON(ctx, topology.prometheusURL+"/api/v1/targets", "", "", &response)
	return response, body, err
}

func (topology *observabilityTopology) prometheusQuery(ctx context.Context) (
	prometheusQueryResponse, []byte, error,
) {
	var response prometheusQueryResponse
	body, err := getJSON(
		ctx, prometheusQueryURL(topology.prometheusURL, "gobfd_bfd_sessions"), "", "", &response,
	)
	return response, body, err
}

func (topology *observabilityTopology) prometheusTransitionBaseline(ctx context.Context) (
	prometheusQueryResponse, []byte, error,
) {
	query := fmt.Sprintf(
		`gobfd_bfd_state_transitions_total{peer_addr=%q,local_addr=%q,from_state="Up",to_state="Down"}`,
		topology.contract.frrIP,
		topology.contract.gobfdIP,
	)
	var response prometheusQueryResponse
	body, err := getJSON(ctx, prometheusQueryURL(topology.prometheusURL, query), "", "", &response)
	return response, body, err
}

func (topology *observabilityTopology) prometheusRules(ctx context.Context) (
	prometheusRulesResponse, []byte, error,
) {
	var response prometheusRulesResponse
	body, err := getJSON(ctx, topology.prometheusURL+"/api/v1/rules?type=alert", "", "", &response)
	return response, body, err
}

func (topology *observabilityTopology) grafanaHealth(ctx context.Context) (grafanaHealth, []byte, error) {
	var response grafanaHealth
	body, err := getJSON(ctx, topology.grafanaURL+"/api/health", "", "", &response)
	return response, body, err
}

func (topology *observabilityTopology) grafanaDatasources(ctx context.Context) (
	[]grafanaDatasource, []byte, error,
) {
	var response []grafanaDatasource
	body, err := getJSON(ctx, topology.grafanaURL+"/api/datasources", "admin", "admin", &response)
	return response, body, err
}

func (topology *observabilityTopology) grafanaSearch(ctx context.Context) (
	[]grafanaSearchResult, []byte, error,
) {
	var response []grafanaSearchResult
	body, err := getJSON(ctx, topology.grafanaURL+"/api/search?query=GoBFD", "admin", "admin", &response)
	return response, body, err
}

func (topology *observabilityTopology) recordOwnedImage(name string) error {
	if name != "" && !slices.Contains(topology.imageNames, name) {
		topology.imageNames = append(topology.imageNames, name)
	}
	return topology.writeResourceSnapshot()
}

func (topology *observabilityTopology) recordOwnedImageID(imageID string) error {
	if imageID != "" && !slices.Contains(topology.imageIDs, imageID) {
		topology.imageIDs = append(topology.imageIDs, imageID)
	}
	return topology.writeResourceSnapshot()
}

func (topology *observabilityTopology) recordOwnedNetwork(name string) error {
	topology.networkName = name
	return topology.writeResourceSnapshot()
}

func (topology *observabilityTopology) recordOwnedVolume(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("record empty observability volume name")
	}
	if len(topology.volumeNames) != 0 && !slices.Contains(topology.volumeNames, name) {
		return fmt.Errorf("refuse multiple observability volumes: recorded %v, new %q", topology.volumeNames, name)
	}
	if !slices.Contains(topology.volumeNames, name) {
		topology.volumeNames = append(topology.volumeNames, name)
	}
	return topology.writeResourceSnapshot()
}

func (topology *observabilityTopology) recordOwnedContainer(name, containerID string) error {
	if name != "" && !slices.Contains(topology.containerNames, name) {
		topology.containerNames = append(topology.containerNames, name)
	}
	if containerID != "" && !slices.Contains(topology.containerIDs, containerID) {
		topology.containerIDs = append(topology.containerIDs, containerID)
	}
	return topology.writeResourceSnapshot()
}

func (topology *observabilityTopology) recordStartupFailure(startupErr error) error {
	topology.startupError = startupErr.Error()
	return errors.Join(
		writeDiagnostic(topology.reportDir, "containers.err", topology.startupError+"\n"),
		topology.writeResourceSnapshot(),
	)
}

func (topology *observabilityTopology) writeResourceSnapshot() error {
	snapshot := observabilityResourceSnapshot{
		ContainerNames: slices.Clone(topology.containerNames),
		ContainerIDs:   slices.Clone(topology.containerIDs),
		ImageNames:     slices.Clone(topology.imageNames),
		ImageIDs:       slices.Clone(topology.imageIDs),
		VolumeNames:    slices.Clone(topology.volumeNames),
		RuntimeImages: []string{
			topology.contract.frrImage, topology.contract.prometheusImage, topology.contract.grafanaImage,
		},
		NetworkName: topology.networkName, StartupError: topology.startupError,
	}
	contents, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mutable observability resource snapshot: %w", err)
	}
	contents = append(contents, '\n')
	if len(contents) > maxDiagnosticBytes {
		return fmt.Errorf("observability resource snapshot exceeds %d bytes", maxDiagnosticBytes)
	}
	if err := writeAtomicResourceSnapshot(
		topology.reportDir, contents, topology.resourceSnapshotBeforeRename,
	); err != nil {
		return fmt.Errorf("write mutable observability resource snapshot: %w", err)
	}
	return nil
}

func writeAtomicResourceSnapshot(
	reportDir string,
	contents []byte,
	beforeRename func() error,
) (returnErr error) {
	root, openErr := os.OpenRoot(reportDir)
	if openErr != nil {
		return fmt.Errorf("open observability report root: %w", openErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close observability report root: %w", closeErr))
		}
	}()

	var random [8]byte
	if _, randomErr := rand.Read(random[:]); randomErr != nil {
		return fmt.Errorf("generate resource snapshot temporary name: %w", randomErr)
	}
	temporaryName := ".resources.json.tmp-" + hex.EncodeToString(random[:])
	temporary, createErr := root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if createErr != nil {
		return fmt.Errorf("create exclusive resource snapshot temporary: %w", createErr)
	}
	keepTemporary := true
	defer func() {
		if keepTemporary {
			if removeErr := root.Remove(temporaryName); removeErr != nil {
				returnErr = errors.Join(
					returnErr, fmt.Errorf("remove resource snapshot temporary: %w", removeErr),
				)
			}
		}
	}()
	if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
		return errors.Join(
			fmt.Errorf("chmod resource snapshot temporary: %w", chmodErr),
			closeResourceSnapshotTemporary(temporary),
		)
	}
	written, writeErr := temporary.Write(contents)
	if writeErr == nil && written != len(contents) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		return errors.Join(
			fmt.Errorf("write resource snapshot temporary: %w", writeErr),
			closeResourceSnapshotTemporary(temporary),
		)
	}
	if syncErr := temporary.Sync(); syncErr != nil {
		return errors.Join(
			fmt.Errorf("sync resource snapshot temporary: %w", syncErr),
			closeResourceSnapshotTemporary(temporary),
		)
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return fmt.Errorf("close resource snapshot temporary: %w", closeErr)
	}
	if beforeRename != nil {
		if hookErr := beforeRename(); hookErr != nil {
			return fmt.Errorf("run resource snapshot pre-rename hook: %w", hookErr)
		}
	}
	if renameErr := root.Rename(temporaryName, "resources.json"); renameErr != nil {
		return fmt.Errorf("atomically replace resource snapshot: %w", renameErr)
	}
	keepTemporary = false
	published, statErr := root.Lstat("resources.json")
	if statErr != nil {
		return fmt.Errorf("lstat published resource snapshot: %w", statErr)
	}
	if !published.Mode().IsRegular() || published.Mode().Perm() != 0o600 {
		return fmt.Errorf("published resource snapshot mode/type = %v, want regular 0600", published.Mode())
	}
	return nil
}

func closeResourceSnapshotTemporary(file *os.File) error {
	if err := file.Close(); err != nil {
		return fmt.Errorf("close resource snapshot temporary: %w", err)
	}
	return nil
}
