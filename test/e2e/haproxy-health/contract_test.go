//go:build e2e_haproxy_testcontainers

package haproxy_health_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/dantte-lp/gobfd/test/internal/podmanapi"
)

type successfulBuiltImageClient struct {
	imageID string
}

func (client successfulBuiltImageClient) ImageExists(context.Context, string) (bool, error) {
	return true, nil
}

func (client successfulBuiltImageClient) ImageID(context.Context, string) (string, error) {
	return client.imageID, nil
}

func (successfulBuiltImageClient) RemoveImage(context.Context, string) error { return nil }

func TestHAProxyHealthContract(t *testing.T) {
	root := repositoryRoot(t)
	contract := newHAProxyHealthContract(root)

	if contract.subnet != "172.23.0.0/24" || contract.gateway != "172.23.0.1" ||
		contract.monitorIP != "172.23.0.10" || contract.haproxyIP != "172.23.0.20" ||
		contract.backend1IP != "172.23.0.30" || contract.backend2IP != "172.23.0.40" {
		t.Fatalf("HAProxy topology addressing = %+v, want exact operational IPAM", contract)
	}
	if contract.haproxyImage != "docker.io/library/haproxy:3.4.3-trixie@sha256:"+
		"4def76cf5d2610255d01fa51b37973d67ddee52f979981fc19117e8d0197bbf5" {
		t.Fatalf("HAProxy image = %q, want immutable operational pin", contract.haproxyImage)
	}
	if contract.nginxImage != "docker.io/library/nginx:1.31.4-trixie@sha256:"+
		"b34848eff6db786b6b1282d3a9c3fd0b5563dfb6d261df4923378b419e0d24f0" {
		t.Fatalf("nginx image = %q, want immutable operational pin", contract.nginxImage)
	}
	for _, path := range []string{
		contract.monitorConfig, contract.agentConfig, contract.backend1Config,
		contract.backend2Config, contract.haproxyConfig,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("operational config %s is unavailable: %v", path, err)
		}
	}

	if got := mutationContainerID("immutable-backend1", "immutable-backend1-bfd"); got != "immutable-backend1-bfd" {
		t.Fatalf("failure mutation target = %q, want exact backend1-bfd sidecar ID", got)
	}

	baseConfig, err := os.ReadFile(contract.haproxyConfig)
	if err != nil {
		t.Fatalf("read operational HAProxy config: %v", err)
	}
	derived, err := deriveHAProxyConfig(baseConfig)
	if err != nil {
		t.Fatalf("derive test-only HAProxy config: %v", err)
	}
	if !bytes.HasPrefix(derived, baseConfig) || !bytes.Contains(derived, []byte("bind *:8404")) ||
		!bytes.Contains(derived, []byte("stats uri /stats\n")) ||
		bytes.Contains(derived, []byte("stats uri /stats;csv")) {
		t.Fatalf("derived HAProxy config does not preserve the operational prefix and append stats: %q", derived)
	}

	contextDir := prepareHAProxyGoBFDBuildContext(t, root)
	containerfile, err := os.ReadFile(filepath.Join(contextDir, "Containerfile"))
	if err != nil {
		t.Fatalf("read bounded GoBFD Containerfile: %v", err)
	}
	for _, required := range [][]byte{
		[]byte("docker.io/library/golang:1.27.0-trixie@sha256:"),
		[]byte("./cmd/gobfd"), []byte("./cmd/gobfdctl"), []byte("./cmd/gobfd-haproxy-agent"),
	} {
		if !bytes.Contains(containerfile, required) {
			t.Fatalf("bounded GoBFD Containerfile lacks %q", required)
		}
	}
	if _, err := os.Stat(filepath.Join(contextDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("bounded build context unexpectedly archives repository metadata: %v", err)
	}
}

func TestRuntimeOwnerIPv4Contract(t *testing.T) {
	inspection := &container.InspectResponse{NetworkSettings: &container.NetworkSettings{
		Networks: map[string]*network.EndpointSettings{
			"haproxy-test-net": {IPAddress: netip.MustParseAddr("172.23.0.99")},
		},
	}}
	if err := validateRuntimeOwnerIPv4(
		"monitor", inspection, "haproxy-test-net", "172.23.0.10",
	); err == nil {
		t.Fatal("wrong selected-endpoint monitor IPv4 accepted")
	}
}

func TestSuccessfulBuildInspectsAndRecordsContentID(t *testing.T) {
	const imageID = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var recorded []string
	err := inspectAndRecordBuiltImage(
		t.Context(), "localhost/gobfd-haproxy-health:test-owned",
		successfulBuiltImageClient{imageID: imageID},
		func(got string) error {
			recorded = append(recorded, got)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("inspect and record successful build: %v", err)
	}
	if len(recorded) != 1 || recorded[0] != imageID {
		t.Fatalf("recorded content IDs = %v, want exact %s", recorded, imageID)
	}
}

func TestContainerSnapshotRejectsOversizedInspection(t *testing.T) {
	reportDir := t.TempDir()
	if err := initializeHAProxyDiagnostics(reportDir); err != nil {
		t.Fatalf("initialize HAProxy diagnostics: %v", err)
	}
	socket := filepath.Join(t.TempDir(), "podman.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatalf("listen on fake Podman socket: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v5.0.0/containers/owned-id/json" {
			http.NotFound(w, request)
			return
		}
		_, _ = w.Write([]byte(`{"payload":"` + strings.Repeat("x", maxDiagnosticBytes) + `"}`))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	client, err := podmanapi.NewClient(socket)
	if err != nil {
		t.Fatalf("create fake exact-endpoint Podman client: %v", err)
	}
	topology := &haproxyHealthTopology{
		reportDir: reportDir, containerIDs: []string{"owned-id"}, client: client,
	}
	if snapshotErr := topology.writeContainerSnapshot(t.Context()); snapshotErr == nil {
		t.Fatal("oversized container inspection produced successful evidence")
	}
	if _, statErr := os.Stat(filepath.Join(reportDir, "containers.json")); !os.IsNotExist(statErr) {
		t.Fatalf("oversized inspection wrote containers.json: %v", statErr)
	}
	diagnostic, err := os.ReadFile(filepath.Join(reportDir, "containers.err"))
	if err != nil {
		t.Fatalf("read container snapshot diagnostic: %v", err)
	}
	if !bytes.Contains(diagnostic, []byte("exceeds")) {
		t.Fatalf("container snapshot diagnostic = %q, want size failure", diagnostic)
	}
}

func TestRuntimeContractStartupFailureWraps(t *testing.T) {
	want := errors.New("runtime contract failed")
	err := wrapRuntimeContractStartupError(want)
	if !errors.Is(err, want) {
		t.Fatalf("startup error = %v, want wrapped runtime contract failure", err)
	}
}

func TestBFDSessionJSONGo127Compatibility(t *testing.T) {
	tests := map[string]struct {
		input string
		peer  string
	}{
		"duplicate member": {
			input: `{"peer_address":"172.23.0.30","peer_address":"172.23.0.30"}`,
			peer:  "172.23.0.30",
		},
		"invalid UTF-8": {
			input: string(append([]byte(`{"peer_address":"`), append([]byte{0xff}, []byte(`"}`)...)...)),
			peer:  "\ufffd",
		},
		"unknown field": {
			input: `{"peer_address":"172.23.0.30","unexpected":true}`,
			peer:  "172.23.0.30",
		},
		"trailing value": {
			input: `{"peer_address":"172.23.0.30"} {}`,
			peer:  "172.23.0.30",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if state, err := parseBFDSessionJSON(test.input, test.peer); err == nil {
				t.Fatalf("parseBFDSessionJSON accepted incompatible input: %+v", state)
			}
		})
	}
}

func TestDecodedPacketEvidenceBoundsAndSeparatesStreams(t *testing.T) {
	csv, diagnostic, err := decodedPacketEvidence(podmanapi.ExecResult{
		Stdout: "time,src,dst\n0.1,172.23.0.10,172.23.0.30\n",
		Stderr: "tshark warning\n",
	}, nil)
	if err != nil {
		t.Fatalf("decode successful tshark evidence: %v", err)
	}
	if strings.Contains(string(csv), "warning") || !strings.Contains(string(csv), "172.23.0.30") {
		t.Fatalf("packet CSV = %q, want stdout only", csv)
	}
	if !strings.Contains(diagnostic, "tshark warning") {
		t.Fatalf("packet diagnostic = %q, want preserved stderr", diagnostic)
	}
	if _, _, err := decodedPacketEvidence(podmanapi.ExecResult{
		Stdout: strings.Repeat("x", maxDiagnosticBytes+1),
	}, nil); err == nil {
		t.Fatal("oversized tshark stdout accepted")
	}
}

func TestResourceSnapshotReplacesWithPrivateMode(t *testing.T) {
	reportDir := t.TempDir()
	path := filepath.Join(reportDir, "resources.json")
	if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("seed stale resource snapshot: %v", err)
	}
	topology := &haproxyHealthTopology{reportDir: reportDir, networkName: "owned-network"}
	if err := topology.writeResourceSnapshot(); err != nil {
		t.Fatalf("write resource snapshot: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat resource snapshot: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("resource snapshot mode = %o, want 600", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read resource snapshot: %v", err)
	}
	if bytes.Contains(contents, []byte("stale")) || !bytes.Contains(contents, []byte("owned-network")) {
		t.Fatalf("resource snapshot = %q, want atomically replaced content", contents)
	}
}
