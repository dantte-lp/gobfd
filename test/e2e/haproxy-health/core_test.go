//go:build e2e_haproxy_testcontainers

package haproxy_health_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	haproxyStatsPort     = "8404/tcp"
	haproxyHTTPPort      = "80/tcp"
	agent1Port           = "9990/tcp"
	agent2Port           = "9991/tcp"
	maxDiagnosticBytes   = 1 << 20
	maxPacketBytes       = 100 << 20
	haproxyReportRunTime = "20060102T150405Z"
)

type haproxyHealthContract struct {
	subnet, gateway                        string
	monitorIP, haproxyIP                   string
	backend1IP, backend2IP                 string
	haproxyImage, nginxImage               string
	monitorConfig, agentConfig             string
	backend1Config, backend2Config         string
	haproxyConfig, tsharkContainerfilePath string
}

type bfdSessionView struct {
	PeerAddress         string `json:"peer_address"`
	LocalAddress        string `json:"local_address"`
	InterfaceName       string `json:"interface_name,omitempty"`
	Type                string `json:"type"`
	LocalState          string `json:"local_state"`
	RemoteState         string `json:"remote_state"`
	LocalDiagnostic     string `json:"local_diagnostic"`
	LocalDiscriminator  uint32 `json:"local_discriminator"`
	RemoteDiscriminator uint32 `json:"remote_discriminator"`
	DetectMultiplier    uint32 `json:"detect_multiplier"`
	DesiredMinTx        string `json:"desired_min_tx_interval,omitempty"`
	RequiredMinRx       string `json:"required_min_rx_interval,omitempty"`
	RemoteMinRx         string `json:"remote_min_rx_interval,omitempty"`
	NegotiatedTx        string `json:"negotiated_tx_interval,omitempty"`
	DetectionTime       string `json:"detection_time,omitempty"`
	AuthType            string `json:"auth_type"`
	LastStateChange     string `json:"last_state_change,omitempty"`
	LastPacketReceived  string `json:"last_packet_received,omitempty"`
}

type haproxyServerState struct {
	Name     string
	Status   string
	Eligible bool
}

type ownedImageClient interface {
	ImageExists(ctx context.Context, image string) (bool, error)
	ImageID(ctx context.Context, image string) (string, error)
	RemoveImage(ctx context.Context, image string) error
}

func newHAProxyHealthContract(root string) haproxyHealthContract {
	base := filepath.Join(root, "deployments/integrations/haproxy-health")
	return haproxyHealthContract{
		subnet: "172.23.0.0/24", gateway: "172.23.0.1",
		monitorIP: "172.23.0.10", haproxyIP: "172.23.0.20",
		backend1IP: "172.23.0.30", backend2IP: "172.23.0.40",
		haproxyImage: "docker.io/library/haproxy:3.4.3-trixie@sha256:" +
			"4def76cf5d2610255d01fa51b37973d67ddee52f979981fc19117e8d0197bbf5",
		nginxImage: "docker.io/library/nginx:1.31.4-trixie@sha256:" +
			"b34848eff6db786b6b1282d3a9c3fd0b5563dfb6d261df4923378b419e0d24f0",
		monitorConfig:           filepath.Join(base, "gobfd-monitor/gobfd.yml"),
		agentConfig:             filepath.Join(base, "haproxy-agent.yml"),
		backend1Config:          filepath.Join(base, "backend1-bfd/gobfd.yml"),
		backend2Config:          filepath.Join(base, "backend2-bfd/gobfd.yml"),
		haproxyConfig:           filepath.Join(base, "haproxy/haproxy.cfg"),
		tsharkContainerfilePath: filepath.Join(root, "test/interop/tshark/Containerfile"),
	}
}

func mutationContainerID(_, backend1BFDID string) string { return backend1BFDID }

func deriveHAProxyConfig(base []byte) ([]byte, error) {
	if len(bytes.TrimSpace(base)) == 0 {
		return nil, errors.New("operational HAProxy config is empty")
	}
	derived := slices.Clone(base)
	if derived[len(derived)-1] != '\n' {
		derived = append(derived, '\n')
	}
	return append(derived, []byte(
		"\nlisten gobfd_test_stats\n"+
			"    bind *:8404\n"+
			"    mode http\n"+
			"    stats enable\n"+
			"    stats uri /stats\n",
	)...), nil
}

func parseBFDSessionJSON(output, peer string) (bfdSessionView, error) {
	if err := preflightStrictJSON([]byte(output)); err != nil {
		return bfdSessionView{}, fmt.Errorf("preflight strict gobfdctl session JSON for %s: %w", peer, err)
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	var state bfdSessionView
	if err := decoder.Decode(&state); err != nil {
		return bfdSessionView{}, fmt.Errorf("decode strict gobfdctl session JSON for %s: %w", peer, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return bfdSessionView{}, fmt.Errorf("decode strict gobfdctl session JSON for %s: %w", peer, err)
	}
	if state.PeerAddress != peer {
		return bfdSessionView{}, fmt.Errorf("gobfdctl peer = %q, want exact %q", state.PeerAddress, peer)
	}
	return state, nil
}

func preflightStrictJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("JSON contains invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := consumeStrictJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func consumeStrictJSONValue(decoder *json.Decoder) error {
	token, tokenErr := decoder.Token()
	if tokenErr != nil {
		return tokenErr
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			memberToken, memberErr := decoder.Token()
			if memberErr != nil {
				return memberErr
			}
			member, ok := memberToken.(string)
			if !ok {
				return fmt.Errorf("JSON object member token has type %T", memberToken)
			}
			if _, duplicate := members[member]; duplicate {
				return fmt.Errorf("duplicate JSON object member %q", member)
			}
			members[member] = struct{}{}
			if err := consumeStrictJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeStrictJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	closing, closingErr := decoder.Token()
	if closingErr != nil {
		return closingErr
	}
	want := json.Delim('}')
	if delimiter == '[' {
		want = ']'
	}
	if closing != want {
		return fmt.Errorf("JSON delimiter %q closed by %q", delimiter, closing)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("unexpected trailing JSON value")
}

func parseHAProxyStats(data, backend string, required []string) (map[string]haproxyServerState, error) {
	reader := csv.NewReader(strings.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("decode HAProxy stats CSV: %w", err)
	}
	if len(records) < 2 || len(records[0]) == 0 {
		return nil, errors.New("HAProxy stats CSV has no header and data rows")
	}
	records[0][0] = strings.TrimSpace(strings.TrimPrefix(records[0][0], "#"))
	columns := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := columns[name]; exists {
			return nil, fmt.Errorf("duplicate HAProxy stats column %q", name)
		}
		columns[name] = index
	}
	for _, name := range []string{"pxname", "svname", "status"} {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("HAProxy stats CSV lacks %q column", name)
		}
	}
	states := make(map[string]haproxyServerState, len(required))
	maxIndex := max(columns["pxname"], columns["svname"], columns["status"])
	for _, record := range records[1:] {
		if len(record) <= maxIndex || record[columns["pxname"]] != backend {
			continue
		}
		name := record[columns["svname"]]
		if !slices.Contains(required, name) {
			continue
		}
		if _, duplicate := states[name]; duplicate {
			return nil, fmt.Errorf("duplicate HAProxy stats row for %s/%s", backend, name)
		}
		status := record[columns["status"]]
		states[name] = haproxyServerState{Name: name, Status: status, Eligible: status == "UP"}
	}
	for _, name := range required {
		if _, ok := states[name]; !ok {
			return nil, fmt.Errorf("HAProxy stats lacks exact server %s/%s", backend, name)
		}
	}
	return states, nil
}

func prepareHAProxyGoBFDBuildContext(t *testing.T, root string) string {
	t.Helper()
	contextDir := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{
		"cmd/gobfd", "cmd/gobfdctl", "cmd/gobfd-haproxy-agent", "internal", "pkg",
	} {
		subtree, err := fs.Sub(rootFS, sourceDir)
		if err != nil {
			t.Fatalf("open bounded GoBFD source %s: %v", sourceDir, err)
		}
		if err := os.CopyFS(filepath.Join(contextDir, sourceDir), subtree); err != nil {
			t.Fatalf("copy bounded GoBFD source %s: %v", sourceDir, err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := copyHAProxyBuildFile(filepath.Join(root, name), filepath.Join(contextDir, name)); err != nil {
			t.Fatalf("copy %s into bounded GoBFD build context: %v", name, err)
		}
	}
	const containerfile = `FROM docker.io/library/golang:1.27.0-trixie@sha256:` +
		`ae28539d2ef595b9a2930dd7f031d9592376829dc0eae7cb869559f7d5812c3a AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/gobfd ./cmd/gobfd
COPY cmd/gobfdctl ./cmd/gobfdctl
COPY cmd/gobfd-haproxy-agent ./cmd/gobfd-haproxy-agent
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfd ./cmd/gobfd
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfdctl ./cmd/gobfdctl
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfd-haproxy-agent ./cmd/gobfd-haproxy-agent
FROM docker.io/library/debian:trixie-slim@sha256:` +
		`d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132
COPY --from=builder /bin/gobfd /bin/gobfd
COPY --from=builder /bin/gobfdctl /bin/gobfdctl
COPY --from=builder /bin/gobfd-haproxy-agent /bin/gobfd-haproxy-agent
ENTRYPOINT ["/bin/gobfd"]
`
	if err := os.WriteFile(filepath.Join(contextDir, "Containerfile"), []byte(containerfile), 0o600); err != nil {
		t.Fatalf("write bounded GoBFD Containerfile: %v", err)
	}
	return contextDir
}

func prepareHAProxyTsharkBuildContext(t *testing.T, source string) string {
	t.Helper()
	contextDir := t.TempDir()
	if err := copyHAProxyBuildFile(source, filepath.Join(contextDir, "Containerfile")); err != nil {
		t.Fatalf("copy bounded tshark Containerfile: %v", err)
	}
	return contextDir
}

func copyHAProxyBuildFile(source, destination string) error {
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
