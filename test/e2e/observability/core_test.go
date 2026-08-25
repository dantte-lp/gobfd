//go:build e2e_observability_testcontainers

package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	prometheusPort     = "9090/tcp"
	grafanaPort        = "3000/tcp"
	maxDiagnosticBytes = 1 << 20
	maxPacketBytes     = 100 << 20
)

type observabilityContract struct {
	subnet, gateway                    string
	gobfdIP, frrIP                     string
	prometheusIP, grafanaIP            string
	frrImage, prometheusImage          string
	grafanaImage                       string
	gobfdConfig, frrDaemons, frrConfig string
	prometheusConfig, alertRules       string
	grafanaDatasource                  string
	grafanaDashboardProvider           string
	grafanaDashboard                   string
	tsharkContainerfile                string
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

type prometheusTargetResponse struct {
	Status string `json:"status"`
	Data   struct {
		ActiveTargets []struct {
			DiscoveredLabels map[string]string `json:"discoveredLabels"` //nolint:tagliatelle // Prometheus API field.
			Labels           map[string]string `json:"labels"`
			ScrapePool       string            `json:"scrapePool"`         //nolint:tagliatelle // Prometheus API field.
			ScrapeURL        string            `json:"scrapeUrl"`          //nolint:tagliatelle // Prometheus API field.
			GlobalURL        string            `json:"globalUrl"`          //nolint:tagliatelle // Prometheus API field.
			LastError        string            `json:"lastError"`          //nolint:tagliatelle // Prometheus API field.
			LastScrape       string            `json:"lastScrape"`         //nolint:tagliatelle // Prometheus API field.
			LastScrapeDur    float64           `json:"lastScrapeDuration"` //nolint:tagliatelle // Prometheus API field.
			Health           string            `json:"health"`
			ScrapeInterval   string            `json:"scrapeInterval"` //nolint:tagliatelle // Prometheus API field.
			ScrapeTimeout    string            `json:"scrapeTimeout"`  //nolint:tagliatelle // Prometheus API field.
		} `json:"activeTargets"` //nolint:tagliatelle // Prometheus API field.
		DroppedTargets []json.RawMessage `json:"droppedTargets"`      //nolint:tagliatelle // Prometheus API field.
		DroppedCounts  map[string]int    `json:"droppedTargetCounts"` //nolint:tagliatelle // Prometheus API field.
	} `json:"data"`
}

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"` //nolint:tagliatelle // Prometheus API field.
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type prometheusRulesResponse struct {
	Status string `json:"status"`
	Data   struct {
		Groups []struct {
			Name  string `json:"name"`
			File  string `json:"file"`
			Rules []struct {
				State  string `json:"state"`
				Name   string `json:"name"`
				Query  string `json:"query"`
				Health string `json:"health"`
				Type   string `json:"type"`
				Alerts []struct {
					State string `json:"state"`
				} `json:"alerts"`
			} `json:"rules"`
		} `json:"groups"`
	} `json:"data"`
}

type grafanaHealth struct {
	Commit   string `json:"commit"`
	Database string `json:"database"`
	Version  string `json:"version"`
}

type grafanaDatasource struct {
	ID        int    `json:"id"`
	UID       string `json:"uid"`
	OrgID     int    `json:"orgId"` //nolint:tagliatelle // Grafana API field.
	Name      string `json:"name"`
	Type      string `json:"type"`
	Access    string `json:"access"`
	URL       string `json:"url"`
	IsDefault bool   `json:"isDefault"` //nolint:tagliatelle // Grafana API field.
}

type grafanaSearchResult struct {
	ID    uint64 `json:"id"`
	UID   string `json:"uid"`
	Title string `json:"title"`
	Type  string `json:"type"`
	URI   string `json:"uri"`
	URL   string `json:"url"`
}

func newObservabilityContract(root string) observabilityContract {
	base := filepath.Join(root, "deployments/integrations/observability")
	return observabilityContract{
		subnet: "172.25.0.0/24", gateway: "172.25.0.1",
		gobfdIP: "172.25.0.10", frrIP: "172.25.0.20",
		prometheusIP: "172.25.0.30", grafanaIP: "172.25.0.40",
		frrImage: "quay.io/frrouting/frr:10.7.0@sha256:" +
			"65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68",
		prometheusImage: "docker.io/prom/prometheus:v3.14.0@sha256:" +
			"5ce7540c3c00ef4ab0c9d2c995c6a5b9c421f44b4a115d97a2c7af3b1c21cbb0",
		grafanaImage: "docker.io/grafana/grafana:13.2.0@sha256:" +
			"3fd54ae1214669f8355f065ec9f6445d5279a3d77095ab048ca045685272429b",
		gobfdConfig:              filepath.Join(base, "gobfd/gobfd.yml"),
		frrDaemons:               filepath.Join(base, "frr/daemons"),
		frrConfig:                filepath.Join(base, "frr/frr.conf"),
		prometheusConfig:         filepath.Join(base, "configs/prometheus/prometheus.yml"),
		alertRules:               filepath.Join(base, "configs/prometheus/alert-rules.yml"),
		grafanaDatasource:        filepath.Join(base, "configs/grafana/provisioning/datasources/prometheus.yml"),
		grafanaDashboardProvider: filepath.Join(base, "configs/grafana/provisioning/dashboards/dashboards.yml"),
		grafanaDashboard:         filepath.Join(base, "configs/grafana/dashboards/bfd.json"),
		tsharkContainerfile:      filepath.Join(root, "test/interop/tshark/Containerfile"),
	}
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
	token, err := decoder.Token()
	if err != nil {
		return err
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
			if valueErr := consumeStrictJSONValue(decoder); valueErr != nil {
				return valueErr
			}
		}
	case '[':
		for decoder.More() {
			if valueErr := consumeStrictJSONValue(decoder); valueErr != nil {
				return valueErr
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
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

func getJSON(ctx context.Context, endpoint, username, password string, destination any) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create GET %s: %w", endpoint, err)
	}
	if username != "" {
		request.SetBasicAuth(username, password)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxDiagnosticBytes+1))
	if readErr != nil {
		return nil, fmt.Errorf("read GET %s: %w", endpoint, readErr)
	}
	if len(body) == 0 || len(body) > maxDiagnosticBytes {
		return nil, fmt.Errorf("GET %s body size %d is outside 1..%d", endpoint, len(body), maxDiagnosticBytes)
	}
	if response.StatusCode != http.StatusOK {
		return body, fmt.Errorf("GET %s status %d: %s", endpoint, response.StatusCode, body)
	}
	if err := preflightStrictJSON(body); err != nil {
		return body, fmt.Errorf("preflight GET %s JSON: %w", endpoint, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		return body, fmt.Errorf("decode GET %s JSON: %w", endpoint, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return body, fmt.Errorf("decode trailing GET %s JSON: %w", endpoint, err)
	}
	return body, nil
}

func prometheusTargetHealthy(response prometheusTargetResponse, contract observabilityContract) bool {
	if response.Status != "success" {
		return false
	}
	wantURL := "http://" + contract.gobfdIP + ":9100/metrics"
	for _, target := range response.Data.ActiveTargets {
		if target.ScrapePool == "gobfd" && target.ScrapeURL == wantURL &&
			target.Health == "up" && target.LastError == "" {
			return true
		}
	}
	return false
}

func prometheusSessionSeriesHealthy(response prometheusQueryResponse, contract observabilityContract) bool {
	if response.Status != "success" || response.Data.ResultType != "vector" {
		return false
	}
	for _, result := range response.Data.Result {
		if result.Metric["__name__"] != "gobfd_bfd_sessions" ||
			result.Metric["peer_addr"] != contract.frrIP ||
			result.Metric["local_addr"] != contract.gobfdIP || len(result.Value) != 2 {
			continue
		}
		var value string
		if err := json.Unmarshal(result.Value[1], &value); err != nil {
			return false
		}
		number, err := strconv.ParseFloat(value, 64)
		return err == nil && number >= 1
	}
	return false
}

func prometheusTransitionBaselineReady(response prometheusQueryResponse, contract observabilityContract) bool {
	if response.Status != "success" || response.Data.ResultType != "vector" {
		return false
	}
	for _, result := range response.Data.Result {
		if result.Metric["__name__"] != "gobfd_bfd_state_transitions_total" ||
			result.Metric["peer_addr"] != contract.frrIP ||
			result.Metric["local_addr"] != contract.gobfdIP ||
			result.Metric["from_state"] != "Up" || result.Metric["to_state"] != "Down" ||
			len(result.Value) != 2 {
			continue
		}
		var value string
		if err := json.Unmarshal(result.Value[1], &value); err != nil {
			return false
		}
		number, err := strconv.ParseFloat(value, 64)
		return err == nil && number == 0
	}
	return false
}

func prometheusAlertState(response prometheusRulesResponse) (string, bool) {
	if response.Status != "success" {
		return "", false
	}
	for _, group := range response.Data.Groups {
		if group.Name != "gobfd" {
			continue
		}
		for _, rule := range group.Rules {
			if rule.Name == "BFDSessionDownTransition" && rule.Type == "alerting" && rule.Health == "ok" {
				return rule.State, true
			}
		}
	}
	return "", false
}

func grafanaDatasourceReady(datasources []grafanaDatasource, contract observabilityContract) bool {
	wantURL := "http://" + contract.prometheusIP + ":9090"
	for _, datasource := range datasources {
		if datasource.Name == "Prometheus" && datasource.Type == "prometheus" &&
			datasource.Access == "proxy" && datasource.URL == wantURL && datasource.IsDefault {
			return true
		}
	}
	return false
}

func grafanaDashboardReady(results []grafanaSearchResult) bool {
	for _, result := range results {
		if result.UID == "gobfd-bfd-sessions" && result.Title == "GoBFD - BFD Sessions" && result.Type == "dash-db" {
			return true
		}
	}
	return false
}

func prepareGoBFDBuildContext(t *testing.T, root string) string {
	t.Helper()
	contextDir := t.TempDir()
	rootFS := os.DirFS(root)
	for _, sourceDir := range []string{"cmd/gobfd", "cmd/gobfdctl", "internal", "pkg"} {
		subtree, err := fs.Sub(rootFS, sourceDir)
		if err != nil {
			t.Fatalf("open bounded GoBFD source %s: %v", sourceDir, err)
		}
		if err := os.CopyFS(filepath.Join(contextDir, sourceDir), subtree); err != nil {
			t.Fatalf("copy bounded GoBFD source %s: %v", sourceDir, err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := copyBuildFile(filepath.Join(root, name), filepath.Join(contextDir, name)); err != nil {
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
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfd ./cmd/gobfd
RUN CGO_ENABLED=0 go build -trimpath -o /bin/gobfdctl ./cmd/gobfdctl
FROM docker.io/library/debian:trixie-slim@sha256:` +
		`d7e12182ce18b85b93007c1dedf31f2d29e01ccf3182cc4017c709b6259bc132
COPY --from=builder /bin/gobfd /bin/gobfd
COPY --from=builder /bin/gobfdctl /bin/gobfdctl
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
	if err := copyBuildFile(source, filepath.Join(contextDir, "Containerfile")); err != nil {
		t.Fatalf("copy bounded tshark Containerfile: %v", err)
	}
	return contextDir
}

func deriveGrafanaDatasource(t *testing.T, source, prometheusIP string) string {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read operational Grafana datasource: %v", err)
	}
	const operationalURL = "http://prometheus-observability:9090"
	if bytes.Count(contents, []byte(operationalURL)) != 1 {
		t.Fatalf("operational Grafana datasource URL occurrence count != 1")
	}
	derived := bytes.Replace(contents, []byte(operationalURL), []byte("http://"+prometheusIP+":9090"), 1)
	path := filepath.Join(t.TempDir(), "prometheus.yml")
	if err := os.WriteFile(path, derived, 0o600); err != nil {
		t.Fatalf("write minimally derived Grafana datasource: %v", err)
	}
	return path
}

func copyBuildFile(source, destination string) error {
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

func prometheusQueryURL(base, query string) string {
	return base + "/api/v1/query?" + url.Values{"query": []string{query}}.Encode()
}
