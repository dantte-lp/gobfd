//go:build e2e_core_testcontainers

package core_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
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
	gobfdAIP = "172.30.10.10"
	gobfdBIP = "172.30.10.20"
)

type coreTopology struct {
	daemons     map[string]coreDaemon
	imageName   string
	networkName string
}

type coreDaemon struct {
	container testcontainers.Container
	endpoint  string
}

func TestCoreDaemonTestcontainers(t *testing.T) {
	endpoint := containertest.RequirePodman(t)
	var containerIDs []string
	var imageName string
	var networkName string

	if !t.Run("authenticated session lifecycle", func(t *testing.T) {
		topology := startCoreTopology(t, endpoint)
		imageName = topology.imageName
		networkName = topology.networkName
		for _, name := range []string{"gobfd-a", "gobfd-b"} {
			containerIDs = append(containerIDs, topology.daemons[name].container.GetContainerID())
		}
		topology.requireSessionUp(t, "gobfd-a", gobfdBIP)
		topology.requireSessionUp(t, "gobfd-b", gobfdAIP)
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
	if imageName != "" {
		containertest.AssertImageRemoved(t, endpoint, imageName)
	}
}

func startCoreTopology(t *testing.T, endpoint string) *coreTopology {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	t.Cleanup(cancel)
	root := repositoryRoot(t)
	networkName := fmt.Sprintf("gobfd-e2e-core-%d", time.Now().UnixNano())
	imageName := fmt.Sprintf("localhost/gobfd-e2e-core:test-%d", time.Now().UnixNano())
	buildContext := prepareBuildContext(t, root)
	builtImage := buildCoreImage(ctx, t, endpoint, buildContext, imageName)
	//nolint:staticcheck // Explicit provider and IPAM require the v0.44 API.
	_, err := containertest.NewNetwork(ctx, t, testcontainers.NetworkRequest{
		Name:   networkName,
		Driver: "bridge",
		Labels: map[string]string{"io.gobfd.test": "e2e-core"},
		IPAM: &network.IPAM{Config: []network.IPAMConfig{{
			Subnet:  netip.MustParsePrefix("172.30.10.0/24"),
			Gateway: netip.MustParseAddr("172.30.10.1"),
		}}},
	})
	if err != nil {
		t.Fatalf("create e2e-core network: %v", err)
	}

	topology := &coreTopology{daemons: make(map[string]coreDaemon, 2), imageName: builtImage, networkName: networkName}
	daemonA := startCoreDaemon(ctx, t, builtImage, networkName, "gobfd-a", gobfdAIP, gobfdBIP)
	assertDaemonIsolation(ctx, t, endpoint, daemonA.container, networkName, gobfdAIP)
	topology.daemons["gobfd-a"] = daemonA
	inspection, err := daemonA.container.Inspect(ctx)
	if err != nil {
		t.Fatalf("inspect gobfd-a content-addressed image: %v", err)
	}
	if inspection.Image == "" {
		t.Fatal("gobfd-a inspection returned an empty image ID")
	}
	daemonB := startCoreDaemon(ctx, t, inspection.Image, networkName, "gobfd-b", gobfdBIP, gobfdAIP)
	assertDaemonIsolation(ctx, t, endpoint, daemonB.container, networkName, gobfdBIP)
	topology.daemons["gobfd-b"] = daemonB
	return topology
}

func startCoreDaemon(
	ctx context.Context,
	t *testing.T,
	imageName string,
	networkName, name, localIP, peerIP string,
) coreDaemon {
	t.Helper()

	request := testcontainers.ContainerRequest{
		Image:        imageName,
		Cmd:          []string{"-config", "/etc/gobfd/gobfd.yml"},
		ExposedPorts: []string{"50051/tcp", "9100/tcp"},
		Labels:       map[string]string{"io.gobfd.test": "e2e-core", "io.gobfd.role": name},
		Networks:     []string{networkName},
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
	return coreDaemon{container: daemon, endpoint: endpoint}
}

func prepareBuildContext(t *testing.T, root string) string {
	t.Helper()

	contextDir := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{"cmd/gobfd", "internal", "pkg"} {
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
	const containerfile = `FROM docker.io/library/golang:1.26.6-trixie@sha256:` +
		`b75d466dd608587fd66cca705a307ba65b889827d06ad61d6a75f0482b51b7c7
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/gobfd ./cmd/gobfd
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfd ./cmd/gobfd
ENTRYPOINT ["/bin/gobfd"]
`
	if err := os.WriteFile(filepath.Join(contextDir, "Containerfile"), []byte(containerfile), 0o600); err != nil {
		t.Fatalf("write bounded test Containerfile: %v", err)
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
	builtImage, err := dockerProvider.BuildImage(ctx, &testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    buildContext,
			Dockerfile: "Containerfile",
			Repo:       repo,
			Tag:        tag,
			KeepImage:  true,
		},
	})
	if err != nil {
		t.Fatalf("build bounded e2e-core image: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if removeErr := client.RemoveImage(cleanupCtx, builtImage); removeErr != nil {
			t.Errorf("remove e2e-core image %s: %v", builtImage, removeErr)
		}
	})
	return builtImage
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
	return fmt.Sprintf(`grpc:
  addr: ":50051"
metrics:
  addr: ":9100"
  path: "/metrics"
log:
  level: "debug"
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
`, peerIP, localIP)
}
