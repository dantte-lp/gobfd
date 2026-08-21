package interop_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const holoImage = "ghcr.io/holo-routing/holo-bundle@sha256:" +
	"5c1f61475b1623b3eab611921f8319fb0a10492ced3f7da05e656418abb5ca4a"

const holodConfig = `user = "holo"
group = "holo"
database_path = "/var/opt/holo/holo.db"

[logging]
  [logging.journald]
    enabled = false
  [logging.file]
    enabled = false
    dir = "/var/log/"
    name = "holod.log"
    rotation = "never"
    style = "full"
    colors = false
    show_thread_id = false
    show_source = false
  [logging.stdout]
    enabled = true
    style = "full"
    colors = false
    show_thread_id = false
    show_source = false

[event_recorder]
  enabled = false
  dir = "/var/opt/holo"

[plugins]
  [plugins.grpc]
    enabled = true
    address = "0.0.0.0:50051"
    [plugins.grpc.tls]
      enabled = false
      certificate = "/etc/ssl/private/holo.pem"
      key = "/etc/ssl/certs/holo.key"
  [plugins.gnmi]
    enabled = false
    address = "0.0.0.0:10161"
    [plugins.gnmi.tls]
      enabled = false
      certificate = "/etc/ssl/private/holo.pem"
      key = "/etc/ssl/certs/holo.key"
`

const holoStartupConfig = `interfaces interface eth0
 type iana-if-type:ethernetCsmacd
 ipv4
!
routing control-plane-protocols control-plane-protocol ietf-bfd-types:bfdv1 main
 bfd ip-sh sessions session eth0 172.20.0.10
  source-addr 172.20.0.50
  local-multiplier 3
  desired-min-tx-interval 300000
  required-min-rx-interval 300000
 !
!
`

type topologyCompose struct {
	Gobfd         composeGobfdService
	Holo          composeHoloService
	HoloConfig    composeHoloConfigService
	BFDNet        composeTopNetwork
	AiobfdPresent bool
}

type composeRaw struct {
	Services map[string]yaml.Node `yaml:"services"`
	Volumes  map[string]yaml.Node `yaml:"volumes"`
	Networks map[string]yaml.Node `yaml:"networks"`
}

type composeGobfdService struct {
	Build         composeBuild                 `yaml:"build"`
	ContainerName string                       `yaml:"container_name"`
	User          string                       `yaml:"user"`
	CapAdd        []string                     `yaml:"cap_add"`
	Volumes       []string                     `yaml:"volumes"`
	Command       []string                     `yaml:"command"`
	DependsOn     map[string]composeDependency `yaml:"depends_on"`
	Networks      map[string]composeAttachment `yaml:"networks"`
}

type composeHoloService struct {
	Image         string                       `yaml:"image"`
	ContainerName string                       `yaml:"container_name"`
	CapAdd        []string                     `yaml:"cap_add"`
	Volumes       []string                     `yaml:"volumes"`
	Networks      map[string]composeAttachment `yaml:"networks"`
	Health        composeHealthcheck           `yaml:"healthcheck"`
}

type composeHoloConfigService struct {
	Image         string                       `yaml:"image"`
	ContainerName string                       `yaml:"container_name"`
	Volumes       []string                     `yaml:"volumes"`
	DependsOn     map[string]composeDependency `yaml:"depends_on"`
	Entrypoint    string                       `yaml:"entrypoint"`
	Command       []string                     `yaml:"command"`
	Networks      map[string]composeAttachment `yaml:"networks"`
}

type composeBuild struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
}

type composeAttachment struct {
	IPv4Address string `yaml:"ipv4_address"`
}

type composeTopNetwork struct {
	Driver string      `yaml:"driver"`
	IPAM   composeIPAM `yaml:"ipam"`
}

type composeIPAM struct {
	Config []composeIPAMConfig `yaml:"config"`
}

type composeIPAMConfig struct {
	Subnet  string `yaml:"subnet"`
	Gateway string `yaml:"gateway"`
}

type composeHealthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period"`
}

type composeDependency struct {
	Condition string `yaml:"condition"`
}

func TestHoloTopologyContract(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	compose, err := loadCompose(root)
	if err != nil {
		t.Fatalf("load Compose topology: %v", err)
	}
	holo := compose.Holo
	if holo.Image != holoImage {
		t.Fatalf("holo image = %q, want immutable %q", holo.Image, holoImage)
	}
	assertEqual(t, "holo container name", holo.ContainerName, "holo-interop")
	if compose.AiobfdPresent {
		t.Fatal("obsolete aiobfd service remains")
	}
	bfdnet := compose.BFDNet
	assertEqual(t, "bfdnet driver", bfdnet.Driver, "bridge")
	assertEqual(t, "bfdnet IPAM", bfdnet.IPAM.Config, []composeIPAMConfig{{
		Subnet:  "172.20.0.0/24",
		Gateway: "172.20.0.1",
	}})
	gobfd := compose.Gobfd
	assertEqual(t, "gobfd networks", gobfd.Networks, map[string]composeAttachment{
		"bfdnet": {IPv4Address: "172.20.0.10"},
	})

	assertEqual(t, "holo capabilities", holo.CapAdd, []string{"NET_RAW", "NET_ADMIN"})
	assertEqual(t, "holo mounts", holo.Volumes, []string{"./holo/holod.toml:/etc/holod.toml:ro,z"})
	assertEqual(t, "holo networks", holo.Networks, map[string]composeAttachment{
		"bfdnet": {IPv4Address: "172.20.0.50"},
	})
	assertEqual(t, "holo healthcheck command", holo.Health.Test, []string{"CMD-SHELL", "netstat -ltn | grep -q ':50051 '"})
	assertEqual(t, "holo healthcheck interval", holo.Health.Interval, "1s")
	assertEqual(t, "holo healthcheck timeout", holo.Health.Timeout, "1s")
	assertEqual(t, "holo healthcheck retries", holo.Health.Retries, 15)
	assertEqual(t, "holo healthcheck start period", holo.Health.StartPeriod, "2s")

	holoConfig := compose.HoloConfig
	assertEqual(t, "holo-config container name", holoConfig.ContainerName, "holo-config-interop")
	assertEqual(t, "holo-config networks", holoConfig.Networks, map[string]composeAttachment{
		"bfdnet": {},
	})
	assertEqual(t, "holo-config image", holoConfig.Image, holoImage)
	assertEqual(t, "holo-config mounts", holoConfig.Volumes, []string{"./holo/holo.startup:/etc/holo.startup:ro,z"})
	assertEqual(t, "holo-config entrypoint", holoConfig.Entrypoint, "holo-cli")
	assertEqual(t, "holo-config command", holoConfig.Command, []string{
		"--address", "http://holo:50051", "--file", "/etc/holo.startup",
	})
	assertEqual(t, "holo-config dependencies", holoConfig.DependsOn, map[string]composeDependency{
		"holo": {Condition: "service_healthy"},
	})
	assertEqual(t, "gobfd dependencies", gobfd.DependsOn, map[string]composeDependency{
		"holo-config": {Condition: "service_completed_successfully"},
	})

	configFiles := []struct {
		name string
		path string
		want string
	}{
		{name: "Holo daemon TOML", path: filepath.Join(root, "test", "interop", "holo", "holod.toml"), want: holodConfig},
		{name: "Holo startup", path: filepath.Join(root, "test", "interop", "holo", "holo.startup"), want: holoStartupConfig},
	}
	for _, configFile := range configFiles {
		if err := validateCanonicalFile(configFile.name, configFile.path, configFile.want); err != nil {
			t.Fatalf("validate %s: %v", configFile.name, err)
		}
	}
}

func TestInteropOperationalContract(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	files := []struct {
		name string
		path string
	}{
		{name: "legacy runner", path: filepath.Join(root, "test", "interop", "run.sh")},
		{name: "routing runner", path: filepath.Join(root, "test", "e2e", "routing", "run.sh")},
		{name: "target inventory", path: filepath.Join(root, "test", "e2e", "targets.md")},
		{name: "Makefile", path: filepath.Join(root, "Makefile")},
		{name: "tagged Go helper", path: filepath.Join(root, "test", "interop", "interop_test.go")},
		{name: "tagged BGP API helper", path: filepath.Join(root, "test", "interop-bgp", "podman_api_test.go")},
		{name: "project guard", path: filepath.Join(root, "test", "interop", "project_guard.sh")},
		{name: "project control", path: filepath.Join(root, "test", "interop", "projectctl.sh")},
	}
	contents := make(map[string]string, len(files))
	for _, file := range files {
		contents[file.name] = readContractFile(t, file.name, file.path)
		lower := strings.ToLower(contents[file.name])
		for _, removed := range []string{"aiobfd", "bitstring"} {
			if strings.Contains(lower, removed) {
				t.Errorf("%s retains active removed reference %q", file.name, removed)
			}
		}
	}

	runner := contents["legacy runner"]
	assertContainsAll(t, "legacy runner", runner, []string{
		`INTEROP_PROJECT_NAME="${INTEROP_PROJECT_NAME:-gobfd-interop}"`,
		`PROJECT_LABEL="com.docker.compose.project=${INTEROP_PROJECT_NAME}"`,
		`-p "${INTEROP_PROJECT_NAME}"`,
		`assert_project_available`,
		`remove_project_resources`,
		`verify_project_absent`,
		`acquire_project_lock`,
		`release_project_lock`,
		`assert_fixed_names_available`,
		`HOLO_IP="172.20.0.50"`,
		`holo-interop`,
		`holo-config`,
		`/tmp/holod.err`,
		`${INTEROP_PROJECT_NAME}_bfdnet`,
		`for svc in gobfd frr bird3 holo thoro`,
	})
	assertOrdered(t, "legacy runner preflight and startup", runner, []string{
		"acquire_project_lock",
		"assert_project_available",
		"assert_fixed_names_available",
		"PROJECT_OWNED=true",
		"build --no-cache",
		"up -d holo holo-config",
		"resolve_project_container_id holo-config-interop",
		`podman wait "${holo_config_id}"`,
		`podman inspect --format '{{.State.ExitCode}}' "${holo_config_id}"`,
		"up -d --no-deps gobfd frr bird3 tshark thoro",
	})
	assertOrdered(t, "legacy runner cleanup", runner, []string{
		"remove_project_resources",
		"verify_project_absent",
		"release_project_lock",
	})
	if strings.Contains(runner, "podman rm -f scapy-interop") {
		t.Error("legacy runner removes an unlabelled Scapy container by name")
	}
	for _, name := range []string{"legacy runner", "routing runner"} {
		if strings.Contains(contents[name], "podman network ls --no-trunc") ||
			strings.Contains(contents[name], "podman volume rm --") {
			t.Errorf("%s duplicates exact-label query/removal implementation outside project_guard.sh", name)
		}
	}

	makefile := contents["Makefile"]
	assertContainsAll(t, "Makefile", makefile, []string{
		"INTEROP_PROJECT_NAME ?= gobfd-interop",
		"override INTEROP_PROJECT_NAME := $(value INTEROP_PROJECT_NAME)",
		"export INTEROP_PROJECT_NAME",
		"INTEROP_CTL := ./test/interop/projectctl.sh",
		"interop-project-validate",
		`"INTEROP_PROJECT_NAME=$${INTEROP_PROJECT_NAME}"`,
		`bgp_project="$${INTEROP_PROJECT_NAME}-bgp"`,
		`env "INTEROP_PROJECT_NAME=$${bgp_project}"`,
		"FRR + BIRD3 + Holo + Thoro/bfd",
	})
	for _, forbidden := range []string{
		"-p $(INTEROP_PROJECT_NAME)",
		"INTEROP_PROJECT_NAME=$(INTEROP_PROJECT_NAME)",
	} {
		if strings.Contains(makefile, forbidden) {
			t.Errorf("Makefile interpolates INTEROP_PROJECT_NAME into shell source: %q", forbidden)
		}
	}
	assertContainsAll(t, "Make interop validation prerequisites", makefile, []string{
		"interop: interop-project-validate",
		"interop-test: interop-project-validate",
		"interop-up: interop-project-validate",
		"interop-down: interop-project-validate",
		"interop-logs: interop-project-validate",
		"interop-capture: interop-project-validate",
		"interop-pcap: interop-project-validate",
		"interop-pcap-summary: interop-project-validate",
		"e2e-routing: interop-project-validate",
		"e2e-routing-test: interop-project-validate",
		`$(INTEROP_CTL) lock-run --`,
	})
	projectControl := contents["project control"]
	startProject := shellFunctionBody(t, projectControl, "start_project")
	assertOrdered(t, "direct interop up ownership", startProject, []string{
		"acquire_lock",
		"assert_empty_project",
		"build",
		"up -d holo holo-config",
		"podman wait",
		"podman inspect --format '{{.State.ExitCode}}'",
		"up -d --no-deps gobfd frr bird3 tshark thoro",
	})
	stopProject := shellFunctionBody(t, projectControl, "stop_project")
	assertOrdered(t, "direct interop down ownership", stopProject, []string{
		"acquire_lock",
		"interop_cleanup_project_resources",
	})
	assertContainsAll(t, "direct locked test runner", projectControl, []string{
		`lock-run)`,
		`"$@"`,
		`interop_assert_existing_project`,
		`REQUIRED_CONTAINER_NAMES`,
		`OPTIONAL_CONTAINER_NAMES`,
	})
	assertContainsAll(t, "direct base mandatory containers", projectControl, []string{
		`gobfd-interop frr-interop bird3-interop tshark-interop`,
		`holo-interop holo-config-interop thoro-interop`,
		`OPTIONAL_CONTAINER_NAMES=(scapy-interop)`,
	})
	assertContainsAll(t, "direct BGP mandatory containers", projectControl, []string{
		`gobfd-bgp-interop gobgp-interop tshark-bgp-interop frr-bgp-interop`,
		`bird3-bgp-interop gobfd-exabgp-interop exabgp-interop`,
	})

	taggedGo := contents["tagged Go helper"]
	assertContainsAll(t, "tagged Go helper", taggedGo, []string{
		`defaultInteropProjectName = "gobfd-interop"`,
		`return projectName + "_bfdnet"`,
		`"--network", interopNetworkName(projectName)`,
		`projectContainerCommand`,
		`resolveProjectContainerID`,
	})
	if strings.Contains(taggedGo, "podmanCompose(") {
		t.Error("tagged Go helper retains name-based Compose runtime operations")
	}
	bgpAPI := contents["tagged BGP API helper"]
	assertContainsAll(t, "tagged BGP exact ownership", bgpAPI, []string{
		`defaultInteropBGPProjectName = "gobfd-interop-bgp"`,
		`errForeignProjectContainer`,
		`resolveProjectContainerID`,
		`client.Inspect`,
	})

	routing := contents["routing runner"]
	assertContainsAll(t, "routing runner", routing, []string{
		`date -u +%Y%m%dT%H%M%S%NZ`,
		`RUN_ID="${RUN_TIMESTAMP}-$$"`,
		`MERGE_OWNER_LABEL_VALUE="${RUN_ID}"`,
		`INTEROP_PROJECT_NAME="${INTEROP_PROJECT_NAME:-gobfd-interop}"`,
		`-p "${project_name}"`,
		`"INTEROP_PROJECT_NAME=${project_name}"`,
		`jq -s -e '[.[] | select(.Action == "pass" and has("Test"))] | length > 0'`,
		"start_holo_interop_suite",
		"start_generic_suite",
		"collect_holo_diagnostics",
		"acquire_project_lock",
		"release_project_lock",
		"assert_fixed_names_available",
		"resolve_project_container_id",
		"holo-interop",
		"holo-config-interop",
	})
	holoStartup := shellFunctionBody(t, routing, "start_holo_interop_suite")
	assertOrdered(t, "routing Holo provider gate", holoStartup, []string{
		"up -d holo holo-config",
		`resolve_project_container_id "${project_name}" holo-config-interop`,
		`podman wait "${loader_id}"`,
		`podman inspect --format '{{.State.ExitCode}}' "${loader_id}"`,
		"up -d --no-deps gobfd frr bird3 tshark thoro",
	})
	assertContainsAll(t, "routing Holo provider gate", holoStartup, []string{
		`[[ ! "${wait_status}" =~ ^[0-9]+$ ]]`,
		`[[ ! "${inspect_status}" =~ ^[0-9]+$ ]]`,
		`[ "${wait_status}" != "${inspect_status}" ]`,
		`[ "${wait_status}" -ne 0 ]`,
		`fail_holo_suite_startup`,
	})
	runSuite := shellFunctionBody(t, routing, "run_suite")
	assertOrdered(t, "routing base suite startup dispatch", runSuite, []string{
		`acquire_project_lock "${project_name}"`,
		`assert_project_available "${project_name}"`,
		`assert_fixed_names_available "${project_name}"`,
		`build --no-cache`,
		`start_holo_interop_suite`,
	})
	assertContainsAll(t, "routing suite startup modes", routing, []string{
		`"holo" "${INTEROP_PROJECT_NAME}"`,
		`"generic" "${INTEROP_BGP_PROJECT_NAME}"`,
	})
	assertOrdered(t, "routing lock lifecycle", runSuite, []string{
		`acquire_project_lock "${project_name}"`,
		`assert_project_available "${project_name}"`,
		`cleanup_project "${project_name}"`,
		`release_project_lock "${project_name}"`,
	})
	holoDiagnostics := shellFunctionBody(t, routing, "collect_holo_diagnostics")
	assertContainsAll(t, "routing Holo artifact diagnostics", holoDiagnostics, []string{
		`resolve_project_container_id "${project_name}" holo-interop`,
		`resolve_project_container_id "${project_name}" holo-config-interop`,
		`logs --tail 100 "${holo_id}"`,
		`logs --tail 100 "${loader_id}"`,
		`exec "${holo_id}" sh -c`,
		`/tmp/holod.err`,
		`"${suite_dir}/holo.log"`,
		`"${suite_dir}/holo-config.log"`,
		`"${suite_dir}/holod.err"`,
	})
	recordContainers := shellFunctionBody(t, routing, "record_containers")
	assertContainsAll(t, "routing container inventory ownership", recordContainers, []string{
		`resolve_project_container_id "${project_name}" "${container_name}"`,
		`inspect "${container_ids[@]}"`,
	})
	cleanupProject := shellFunctionBody(t, routing, "cleanup_project")
	assertOrdered(t, "routing label-safe cleanup", cleanupProject, []string{
		`remove_project_resources "${project_name}"`,
		`verify_project_absent "${project_name}"`,
	})
	mergeArtifacts := shellFunctionBody(t, routing, "merge_artifacts")
	assertContainsAll(t, "routing merge ownership", routing, []string{
		`MERGE_OWNER_LABEL_KEY="io.gobfd.e2e.merge-owner"`,
	})
	assertContainsAll(t, "routing merge ownership", mergeArtifacts, []string{
		`query_labelled_container_ids`,
		`remove_labelled_containers`,
		`verify_labelled_containers_absent`,
	})
	assertOrdered(t, "routing merge ownership lifecycle", mergeArtifacts, []string{
		`interop_query_labelled_container_ids`,
		`merge ownership label collision`,
		`"${PODMAN[@]}" run`,
		`interop_remove_labelled_containers`,
		`interop_verify_labelled_containers_absent`,
	})
	if strings.Contains(mergeArtifacts, "com.docker.compose.project") {
		t.Error("merge_artifacts reuses a Compose project ownership label")
	}
	if strings.Contains(mergeArtifacts, "run --rm") {
		t.Error("merge_artifacts delegates cleanup to name-based or implicit removal")
	}
	failHoloStartup := shellFunctionBody(t, routing, "fail_holo_suite_startup")
	assertContainsAll(t, "routing Holo failure diagnostics", failHoloStartup, []string{
		`collect_holo_diagnostics "${project_name}" "${compose_file}" "${suite_dir}"`,
		`for artifact in holo.log holo-config.log holod.err`,
	})
	assertOrdered(t, "routing non-zero test guard", routing, []string{
		`>"${suite_dir}/go-test.json"`,
		`jq -s -e '[.[] | select(.Action == "pass" and has("Test"))] | length > 0'`,
		`collect_holo_diagnostics`,
		`collect_pcap "${suite}"`,
	})
	for _, name := range []string{"legacy runner", "routing runner", "project control"} {
		if strings.Contains(contents[name], "down --volumes --remove-orphans") {
			t.Errorf("%s retains name-based Compose cleanup", name)
		}
	}

	inventory := contents["target inventory"]
	assertContainsAll(t, "target inventory", inventory, []string{
		"GoBFD, FRR, BIRD3, Holo, Holo loader, Thoro/bfd, tshark, Scapy fuzzer",
		"Exact Compose project label",
	})
}

func TestBenchmarkReportOperationalContract(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	paths := []string{
		filepath.Join(root, "bench"),
		filepath.Join(root, "scripts", "gen-report.sh"),
		filepath.Join(root, "scripts", "report-template.html"),
	}
	removedReferences := []string{"aio" + "bfd", "bit" + "string"}
	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Errorf("stat benchmark/report surface %s: %v", path, statErr)
			continue
		}
		if !info.IsDir() {
			assertNoRemovedBenchmarkReferences(t, root, path, removedReferences)
			continue
		}

		walkErr := filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			assertNoRemovedBenchmarkReferences(t, root, candidate, removedReferences)
			return nil
		})
		if walkErr != nil {
			t.Errorf("scan benchmark/report surface %s: %v", path, walkErr)
		}
	}

	makefile := readContractFile(t, "Makefile", filepath.Join(root, "Makefile"))
	if strings.Contains(strings.ToLower(makefile), "bench-"+"python") {
		t.Error("benchmark-cross retains the removed benchmark service")
	}
}

func assertNoRemovedBenchmarkReferences(t *testing.T, root, path string, removedReferences []string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read benchmark/report surface %s: %v", path, err)
		return
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Errorf("resolve benchmark/report path %s: %v", path, err)
		return
	}
	lower := strings.ToLower(relative + "\n" + string(data))
	for _, removed := range removedReferences {
		if strings.Contains(lower, removed) {
			t.Errorf("%s retains active removed reference %q", relative, removed)
		}
	}
}

func shellFunctionBody(t *testing.T, script, name string) string {
	t.Helper()

	startMarker := name + "() {\n"
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatalf("shell function %s is missing", name)
	}
	start += len(startMarker)
	end := strings.Index(script[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("shell function %s has no closing brace", name)
	}
	return script[start : start+end]
}

func TestInteropComposeServiceInventory(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	path := filepath.Join(root, "test", "interop", "compose.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Compose file %s: %v", path, err)
	}
	raw, err := decodeKnownFields[composeRaw](data, "Compose root")
	if err != nil {
		t.Fatalf("decode Compose service inventory: %v", err)
	}

	got := make([]string, 0, len(raw.Services))
	for name := range raw.Services {
		got = append(got, name)
	}
	slices.Sort(got)
	want := []string{"bird3", "frr", "gobfd", "holo", "holo-config", "scapy", "thoro", "tshark"}
	assertEqual(t, "Compose services", got, want)
}

func loadCompose(root string) (topologyCompose, error) {
	path := filepath.Join(root, "test", "interop", "compose.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return topologyCompose{}, fmt.Errorf("read Compose file %s: %w", path, err)
	}
	compose, err := decodeCompose(data)
	if err != nil {
		return topologyCompose{}, fmt.Errorf("decode Compose file %s: %w", path, err)
	}
	return compose, nil
}

func decodeCompose(data []byte) (topologyCompose, error) {
	raw, err := decodeKnownFields[composeRaw](data, "Compose root")
	if err != nil {
		return topologyCompose{}, err
	}
	gobfdNode, err := requiredComposeNode(raw.Services, "gobfd", "Compose services")
	if err != nil {
		return topologyCompose{}, err
	}
	holoNode, err := requiredComposeNode(raw.Services, "holo", "Compose services")
	if err != nil {
		return topologyCompose{}, err
	}
	holoConfigNode, err := requiredComposeNode(raw.Services, "holo-config", "Compose services")
	if err != nil {
		return topologyCompose{}, err
	}
	bfdnetNode, err := requiredComposeNode(raw.Networks, "bfdnet", "Compose networks")
	if err != nil {
		return topologyCompose{}, err
	}

	gobfd, err := decodeComposeNode[composeGobfdService](gobfdNode, "gobfd service")
	if err != nil {
		return topologyCompose{}, err
	}
	holo, err := decodeComposeNode[composeHoloService](holoNode, "holo service")
	if err != nil {
		return topologyCompose{}, err
	}
	holoConfig, err := decodeComposeNode[composeHoloConfigService](holoConfigNode, "holo-config service")
	if err != nil {
		return topologyCompose{}, err
	}
	bfdnet, err := decodeComposeNode[composeTopNetwork](bfdnetNode, "bfdnet network")
	if err != nil {
		return topologyCompose{}, err
	}
	_, aiobfdPresent := raw.Services["aiobfd"]
	return topologyCompose{
		Gobfd:         gobfd,
		Holo:          holo,
		HoloConfig:    holoConfig,
		BFDNet:        bfdnet,
		AiobfdPresent: aiobfdPresent,
	}, nil
}

func decodeComposeNode[T any](node yaml.Node, label string) (T, error) {
	data, err := yaml.Marshal(&node)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("encode %s for strict decoding: %w", label, err)
	}
	value, err := decodeKnownFields[T](data, label)
	if err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}

func decodeKnownFields[T any](data []byte, label string) (T, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var value T
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("strictly decode %s: %w", label, err)
	}
	return value, nil
}

func requiredComposeNode(nodes map[string]yaml.Node, name, label string) (yaml.Node, error) {
	node, ok := nodes[name]
	if !ok {
		return yaml.Node{}, fmt.Errorf("%s is missing %q", label, name)
	}
	return node, nil
}

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	start := dir
	for {
		goModPath := filepath.Join(dir, "go.mod")
		goMod, readErr := os.ReadFile(goModPath)
		if readErr == nil && strings.HasPrefix(string(goMod), "module github.com/dantte-lp/gobfd\n") {
			return dir, nil
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return "", fmt.Errorf("read module file %s: %w", goModPath, readErr)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("find repository root from %s: %w", start, os.ErrNotExist)
		}
		dir = parent
	}
}

func assertEqual[T any](t *testing.T, name string, got, want T) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

func validateCanonicalFile(name, path, want string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s file %s: %w", name, path, err)
	}
	if err := validateCanonicalConfig(name, data, want); err != nil {
		return fmt.Errorf("validate %s file %s: %w", name, path, err)
	}
	return nil
}

func validateCanonicalConfig(name string, got []byte, want string) error {
	if string(got) != want {
		return fmt.Errorf("%s does not match the canonical configuration", name)
	}
	return nil
}

func readContractFile(t *testing.T, name, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s %s: %v", name, path, err)
	}
	return string(data)
}

func assertContainsAll(t *testing.T, name, contents string, wants []string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(contents, want) {
			t.Errorf("%s is missing contract text %q", name, want)
		}
	}
}

func assertOrdered(t *testing.T, name, contents string, wants []string) {
	t.Helper()

	position := 0
	for _, want := range wants {
		index := strings.Index(contents[position:], want)
		if index < 0 {
			t.Fatalf("%s is missing ordered contract text %q after byte %d", name, want, position)
		}
		position += index + len(want)
	}
}
