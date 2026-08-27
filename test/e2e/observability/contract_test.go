//go:build e2e_observability_testcontainers

package observability_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec" //nolint:depguard // Contract regression executes fixed Bash pipeline semantics without invoking Podman.
	"path/filepath"
	"strings"
	"testing"
)

func TestObservabilityCompatibilityGateContract(t *testing.T) {
	root := repositoryRoot(t)
	makefile := readContractFile(t, filepath.Join(root, "Makefile"))
	for _, required := range []string{
		"int-observability:\n\tgo run ./test/cmd/integrationctl observability",
		"int-observability-testcontainers:",
		"-tags e2e_observability_testcontainers ./test/e2e/observability",
	} {
		if !strings.Contains(makefile, required) {
			t.Fatalf("Makefile lacks observability compatibility contract %q", required)
		}
	}

	gopls := readContractFile(t, filepath.Join(root, "scripts", "gopls-check.sh"))
	if !strings.Contains(gopls, "e2e_observability_testcontainers") {
		t.Fatal("gopls tag profiles lack e2e_observability_testcontainers")
	}

	contract := newObservabilityContract(root)
	if contract.subnet != "172.25.0.0/24" || contract.gateway != "172.25.0.1" ||
		contract.gobfdIP != "172.25.0.10" || contract.frrIP != "172.25.0.20" ||
		contract.prometheusIP != "172.25.0.30" || contract.grafanaIP != "172.25.0.40" {
		t.Fatalf("observability addressing = %+v, want exact static IPAM", contract)
	}
	for name, image := range map[string]string{
		"FRR": contract.frrImage, "Prometheus": contract.prometheusImage, "Grafana": contract.grafanaImage,
	} {
		if !strings.Contains(image, "@sha256:") {
			t.Fatalf("%s image %q is not immutable", name, image)
		}
	}
	for _, path := range []string{
		contract.gobfdConfig, contract.frrDaemons, contract.frrConfig,
		contract.prometheusConfig, contract.alertRules, contract.grafanaDatasource,
		contract.grafanaDashboardProvider, contract.grafanaDashboard, contract.tsharkContainerfile,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("operational observability asset %s is unavailable: %v", path, err)
		}
	}

	buildContext := prepareGoBFDBuildContext(t, root)
	containerfile := readContractFile(t, filepath.Join(buildContext, "Containerfile"))
	for _, required := range []string{
		"docker.io/library/golang:1.27.0-trixie@sha256:",
		"./cmd/gobfd", "./cmd/gobfdctl", "go build -trimpath",
	} {
		if !strings.Contains(containerfile, required) {
			t.Fatalf("bounded GoBFD Containerfile lacks %q", required)
		}
	}
	if _, err := os.Stat(filepath.Join(buildContext, ".git")); !os.IsNotExist(err) {
		t.Fatalf("bounded build context unexpectedly archives repository metadata: %v", err)
	}

	derived := readContractFile(t, deriveGrafanaDatasource(t, contract.grafanaDatasource, contract.prometheusIP))
	if !strings.Contains(derived, "http://172.25.0.30:9090") ||
		strings.Contains(derived, "http://prometheus-observability:9090") {
		t.Fatalf("minimally derived Grafana datasource has unexpected URL: %q", derived)
	}
}

func TestObservabilityMakeRecipePreservesPipelineFailures(t *testing.T) {
	makefile := readContractFile(t, filepath.Join(repositoryRoot(t), "Makefile"))
	for _, required := range []string{
		`report_dir="$$(mktemp -d "$${report_parent}/run.XXXXXXXX")"`,
		`pipeline_status=("$${PIPESTATUS[@]}")`,
		`test "$${#pipeline_status[@]}" -eq 2`,
		`test "$${pipeline_status[0]}" -eq 0`,
		`test "$${pipeline_status[1]}" -eq 0`,
		`E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER`,
	} {
		if !strings.Contains(makefile, required) {
			t.Fatalf("observability Make recipe lacks failure-safe contract %q", required)
		}
	}
}

func TestPipelineStatusRejectsTeeFailure(t *testing.T) {
	startCommand := exec.CommandContext(t.Context(), filepath.Join(t.TempDir(), "missing-bash"))
	if startErr := startCommand.Run(); commandExitedAsExpected(startErr, nil) {
		t.Fatal("pipeline contract misclassified a command start error as the expected process exit")
	}

	command := exec.CommandContext(t.Context(), "bash", "-o", "pipefail", "-c",
		`printf x | tee /dev/full >/dev/null; `+
			`pipeline_status=("${PIPESTATUS[@]}"); `+
			`test "${#pipeline_status[@]}" -eq 2 && `+
			`test "${pipeline_status[0]}" -eq 0 && test "${pipeline_status[1]}" -eq 0`)
	output, err := command.CombinedOutput()
	if !commandExitedAsExpected(err, output) {
		t.Fatalf("tee failure result = %v, output = %q; want process exit 1", err, output)
	}
}

func TestObservabilityAPIBoundaries(t *testing.T) {
	contract := newObservabilityContract(repositoryRoot(t))
	var targets prometheusTargetResponse
	targetFixture := []byte(`{"status":"success","data":{"activeTargets":[{` +
		`"scrapePool":"gobfd","scrapeUrl":"http://172.25.0.10:9100/metrics",` +
		`"health":"up","lastError":""}]}}`)
	if err := json.Unmarshal(targetFixture, &targets); err != nil {
		t.Fatalf("decode Prometheus target fixture: %v", err)
	}
	if !prometheusTargetHealthy(targets, contract) {
		t.Fatal("exact healthy GoBFD Prometheus target was rejected")
	}
	targets.Data.ActiveTargets[0].LastError = "scrape failed"
	if prometheusTargetHealthy(targets, contract) {
		t.Fatal("error-bearing Prometheus target was accepted as healthy")
	}

	query := prometheusQueryResponse{Status: "success"}
	query.Data.ResultType = "vector"
	query.Data.Result = append(query.Data.Result, struct {
		Metric map[string]string `json:"metric"`
		Value  []json.RawMessage `json:"value"`
	}{
		Metric: map[string]string{
			"__name__": "gobfd_bfd_sessions", "peer_addr": contract.frrIP, "local_addr": contract.gobfdIP,
		},
		Value: []json.RawMessage{json.RawMessage("1"), json.RawMessage(`"1"`)},
	})
	if !prometheusSessionSeriesHealthy(query, contract) {
		t.Fatal("exact GoBFD session series was rejected")
	}
	transition := prometheusQueryResponse{Status: "success"}
	transition.Data.ResultType = "vector"
	transition.Data.Result = append(transition.Data.Result, struct {
		Metric map[string]string `json:"metric"`
		Value  []json.RawMessage `json:"value"`
	}{
		Metric: map[string]string{
			"__name__":  "gobfd_bfd_state_transitions_total",
			"peer_addr": contract.frrIP, "local_addr": contract.gobfdIP,
			"from_state": "Up", "to_state": "Down",
		},
		Value: []json.RawMessage{json.RawMessage("1"), json.RawMessage(`"0"`)},
	})
	if !prometheusTransitionBaselineReady(transition, contract) {
		t.Fatal("exact zero Up-to-Down transition baseline was rejected")
	}
	transition.Data.Result[0].Value[1] = json.RawMessage(`"1"`)
	if prometheusTransitionBaselineReady(transition, contract) {
		t.Fatal("nonzero Up-to-Down transition baseline was accepted")
	}

	validSession := `{"peer_address":"172.25.0.20","local_address":"172.25.0.10","type":"single_hop",` +
		`"local_state":"Up","remote_state":"Up","local_diagnostic":"None",` +
		`"local_discriminator":1,"remote_discriminator":2,"detect_multiplier":3,"auth_type":"None"}`
	if _, err := parseBFDSessionJSON(validSession, contract.frrIP); err != nil {
		t.Fatalf("decode exact BFD session: %v", err)
	}
	for name, input := range map[string]string{
		"duplicate": `{"peer_address":"172.25.0.20","peer_address":"172.25.0.20"}`,
		"unknown":   `{"peer_address":"172.25.0.20","unexpected":true}`,
		"trailing":  `{"peer_address":"172.25.0.20"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if state, err := parseBFDSessionJSON(input, contract.frrIP); err == nil {
				t.Fatalf("incompatible BFD session JSON accepted: %+v", state)
			}
		})
	}
	if err := preflightStrictJSON(bytes.Repeat([]byte{0xff}, 1)); err == nil {
		t.Fatal("invalid UTF-8 JSON accepted")
	}
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

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract file %s: %v", path, err)
	}
	return string(contents)
}
