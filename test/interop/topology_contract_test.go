package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // Contract tests execute fixed repository tools with explicit argument vectors.
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
	Gobfd              composeGobfdService
	Holo               composeHoloService
	HoloConfig         composeHoloConfigService
	BFDNet             composeTopNetwork
	RemovedPeerPresent bool
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
	if compose.RemovedPeerPresent {
		t.Fatal("obsolete removed peer service remains")
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
		{name: "Compose topology", path: filepath.Join(root, "test", "interop", "compose.yml")},
		{name: "FRR configuration", path: filepath.Join(root, "test", "interop", "frr", "frr.conf")},
		{name: "BIRD image", path: filepath.Join(root, "test", "interop", "bird3", "Containerfile")},
		{name: "routing runner", path: filepath.Join(root, "test", "e2e", "routing", "run.sh")},
		{name: "target inventory", path: filepath.Join(root, "test", "e2e", "targets.md")},
		{name: "Makefile", path: filepath.Join(root, "Makefile")},
		{name: "tagged Go helper", path: filepath.Join(root, "test", "interop", "interop_test.go")},
		{name: "tagged BGP API helper", path: filepath.Join(root, "test", "interop-bgp", "podman_api_test.go")},
		{name: "project guard", path: filepath.Join(root, "test", "interop", "project_guard.sh")},
		{name: "project control", path: filepath.Join(root, "test", "interop", "projectctl.sh")},
		{name: "gopls gate", path: filepath.Join(root, "scripts", "gopls-check.sh")},
		{name: "English interop guide", path: filepath.Join(root, "docs", "en", "05-interop.md")},
		{name: "Russian interop guide", path: filepath.Join(root, "docs", "ru", "05-interop.md")},
		{name: "BGP Compose topology", path: filepath.Join(root, "test", "interop-bgp", "compose.yml")},
		{name: "RFC Compose topology", path: filepath.Join(root, "test", "interop-rfc", "compose.yml")},
		{name: "tshark image", path: filepath.Join(root, "test", "interop", "tshark", "Containerfile")},
	}
	contents := make(map[string]string, len(files))
	for _, file := range files {
		contents[file.name] = readContractFile(t, file.name, file.path)
		lower := strings.ToLower(contents[file.name])
		for _, removed := range []string{"aio" + "bfd", "bit" + "string"} {
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
		`docker.io/debian:trixie-slim (for BIRD 3.3.2 source build)`,
		`holo-interop`,
		`holo-config`,
		`/tmp/holod.err`,
		`${INTEROP_PROJECT_NAME}_bfdnet`,
		`for svc in gobfd frr bird3 holo thoro`,
		`INTEROP_COMPOSE_OVERRIDE_FILE`,
		`COMPOSE_ARGS`,
		`=== Holo daemon logs ===`,
		`=== Holo daemon /tmp/holod.err ===`,
		`=== Holo configuration loader logs ===`,
		`interop_verify_holo_running_configuration`,
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
		`interop_verify_holo_running_configuration`,
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
		"FRR 10.7.0 + BIRD 3.3.2 + Holo 0.9.0 + Thoro/bfd",
		"gopls-check: dev-ensure",
		"lint-md: dev-ensure",
		"lint-yaml: dev-ensure",
		"proto-lint: dev-ensure",
	})
	assertContainsAll(t, "gopls gate", contents["gopls gate"], []string{
		"testcontainers",
		"e2e_core_testcontainers",
		"gopls-check: no packages discovered",
		"gopls-check: no Go inputs discovered",
	})
	if strings.Contains(contents["gopls gate"], "e2e_core,e2e_core_testcontainers") {
		t.Error("gopls gate combines mutually exclusive core test backends in one tag profile")
	}
	assertContainsAll(t, "Compose topology", contents["Compose topology"], []string{
		"quay.io/frrouting/frr:10.7.0@sha256:65e5967b922572c0565d968388fb06af69d7e9b3b3eea40ad7e3810687667f68",
	})
	if strings.Contains(contents["Compose topology"], "podman exec") {
		t.Error("Compose topology documents unguarded fixed-name Podman access")
	}
	for _, name := range []string{"English interop guide", "Russian interop guide"} {
		for _, command := range []string{
			"make interop-bgp\n", "make interop-rfc\n", "make e2e-rfc\n", "podman logs ", "podman exec ",
		} {
			if strings.Contains(contents[name], command) {
				t.Errorf("%s documents unsafe legacy lifecycle command %q", name, command)
			}
		}
	}
	for _, name := range []string{"BGP Compose topology", "RFC Compose topology"} {
		if strings.Contains(contents[name], "podman-compose") {
			t.Errorf("%s documents unguarded raw Compose lifecycle commands", name)
		}
	}
	assertContainsAll(t, "FRR configuration", contents["FRR configuration"], []string{
		"frr version 10.7.0",
	})
	assertContainsAll(t, "BIRD image", contents["BIRD image"], []string{
		"BIRD 3.3.2",
		"BIRD_COMMIT=e66773c42f115d80aaaef3b7246953b276a88065",
		"BIRD_SOURCE_SHA256=ad74ac8bc970f97a137f5df4e0c96777dfc7137b9773b1f95badec3d93e18754",
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
		"interop_verify_holo_running_configuration",
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
		"interop_verify_holo_running_configuration",
	})
	holoStartup := shellFunctionBody(t, routing, "start_holo_interop_suite")
	assertOrdered(t, "routing Holo provider gate", holoStartup, []string{
		"up -d holo holo-config",
		`resolve_project_container_id "${project_name}" holo-config-interop`,
		`podman wait "${loader_id}"`,
		`podman inspect --format '{{.State.ExitCode}}' "${loader_id}"`,
		`interop_verify_holo_running_configuration`,
		"up -d --no-deps gobfd frr bird3 tshark thoro",
	})
	assertContainsAll(t, "routing Holo provider gate", holoStartup, []string{
		`[[ ! "${wait_status}" =~ ^[0-9]+$ ]]`,
		`[[ ! "${inspect_status}" =~ ^[0-9]+$ ]]`,
		`[ "${wait_status}" != "${inspect_status}" ]`,
		`[ "${wait_status}" -ne 0 ]`,
		`fail_holo_suite_startup`,
	})
	projectGuard := contents["project guard"]
	sharedHoloVerifier := shellFunctionBody(t, projectGuard, "interop_verify_holo_running_configuration")
	assertOrdered(t, "shared Holo semantic verifier", sharedHoloVerifier, []string{
		`"${PODMAN[@]}" logs "${loader_id}"`,
		`grep -q '^% '`,
		`interop_resolve_project_container_id "${project_name}" holo-interop`,
		`holo-cli --version`,
		`--command 'show running format json'`,
	})
	assertContainsAll(t, "shared Holo semantic verifier", sharedHoloVerifier, []string{
		`Holo command-line interface 0.5.0`,
		`Holo configuration loader produced unexpected output`,
		`($interfaces | length) == 1`,
		`($protocols | length) == 1`,
		`($sessions | length) == 1`,
	})
	for _, name := range []string{"legacy runner", "routing runner", "project control"} {
		if got := strings.Count(contents[name], "interop_verify_holo_running_configuration"); got != 1 {
			t.Errorf("%s shared Holo verifier call count = %d, want 1", name, got)
		}
	}
	containerPreflight := shellFunctionBody(t, projectGuard, "interop_validate_container_snapshot")
	assertContainsAll(t, "generic exact container preflight", containerPreflight, []string{
		`podman inspect --type container`,
		`--format '{{json .}}' "${container_id}"`,
		`.Id == $container_id`,
		`.Config.Labels[$label_key] == $label_value`,
		`(.Mounts | type) == "array"`,
		`all(.Mounts[];`,
		`.Type != "volume"`,
		`container ownership or volume-mount preflight failed`,
	})
	projectRemoval := shellFunctionBody(t, projectGuard, "interop_remove_project_resources")
	assertOrdered(t, "project cleanup preflight before mutation", projectRemoval, []string{
		`[[ "${#volume_names[@]}" -ne 0 ]]`,
		`interop_validate_container_snapshot`,
		`interop_remove_container_snapshot`,
		`podman network rm`,
	})
	labelledRemoval := shellFunctionBody(t, projectGuard, "interop_remove_labelled_containers")
	assertOrdered(t, "merge-owner cleanup preflight before mutation", labelledRemoval, []string{
		`snapshot+=("${container_id}")`,
		`interop_validate_container_snapshot`,
		`interop_remove_container_snapshot`,
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

	implementationPlan := readContractFile(
		t,
		"Holo implementation plan",
		filepath.Join(root, "docs", "superpowers", "plans", "2026-08-21-"+"aio"+"bfd-to-holo-interop.md"),
	)
	assertContainsAll(t, "Holo Task 6 plan", implementationPlan, []string{
		`TASK6_ARTIFACT_DIR="$(mktemp -d`,
		"INVALID_STARTUP=",
		"PODMAN=(timeout 2m podman)",
		"INTEROP_COMPOSE_OVERRIDE_FILE",
		`grep -Fq '=== Holo daemon logs ==='`,
		`grep -Fq '=== Holo daemon /tmp/holod.err ==='`,
		`grep -Fq '=== Holo configuration loader logs ==='`,
		`"${PODMAN[@]}" events`,
		`.Name == "holo-interop" and .Status == "create"`,
		`.Name == "holo-interop" and .Status == "start"`,
		`.Name == "holo-config-interop" and .Status == "create"`,
		`.Name == "holo-config-interop" and .Status == "start"`,
		`.Name == "holo-config-interop" and .Status == "died"`,
		`(.ContainerExitCode | type) == "number"`,
		`.ContainerExitCode == 0`,
		`grep -Fq 'Holo running configuration is missing the required BFD session'`,
		`--command 'show running format json'`,
		`Holo command-line interface 0.5.0`,
		`holo-version.txt`,
		`.["ietf-interfaces:interfaces"].interface[]?`,
		`.type == "iana-if-type:ethernetCsmacd"`,
		`.["ietf-ip:ipv4"] | type`,
		`.type == "ietf-bfd-types:bfdv1"`,
		`.["dest-addr"] == "172.20.0.10"`,
		`.["source-addr"] == "172.20.0.50"`,
		"source ./test/interop/project_guard.sh",
		"interop_verify_project_absent",
		"interop_verify_labelled_containers_absent",
		"make interop-up",
		"./test/interop/projectctl.sh lock-run --",
		"go test -json -tags interop",
		`select(.Action == "pass" and .Test == "TestHoloFailureRecoveryLifecycle")`,
		`select(.Action == "skip" and .Test == "TestHoloFailureRecoveryLifecycle")`,
		"make interop-down",
		"make proto-lint",
		"gobfd-interop-negative",
		"gobfd-interop-bgp",
		"v062-testcontainers",
		"io.gobfd.e2e.merge-owner",
		"There is no `podman volume rm` cleanup path.",
	})
	if strings.Contains(implementationPlan, `.ContainerExitCode != 0`) {
		t.Error("Holo Task 6 plan retains the impossible non-zero loader assertion")
	}
	if strings.Contains(implementationPlan, "\nbuf lint\n") {
		t.Error("Holo Task 6 plan invokes an unpinned host buf binary")
	}
}

func TestInteropCaptureStorageContract(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	topologies := []struct {
		name    string
		path    string
		service string
	}{
		{name: "base", path: filepath.Join(root, "test", "interop", "compose.yml"), service: "tshark"},
		{name: "BGP", path: filepath.Join(root, "test", "interop-bgp", "compose.yml"), service: "tshark-bgp"},
	}
	for _, topology := range topologies {
		t.Run(topology.name, func(t *testing.T) {
			t.Parallel()

			data, readErr := os.ReadFile(topology.path)
			if readErr != nil {
				t.Fatalf("read Compose topology: %v", readErr)
			}
			raw, decodeErr := decodeKnownFields[composeRaw](data, "Compose root")
			if decodeErr != nil {
				t.Fatalf("decode Compose topology: %v", decodeErr)
			}
			assertCaptureStorage(t, topology.service, raw)

			render := exec.CommandContext(
				t.Context(), "podman-compose", "-p", "gobfd-storage-contract",
				"-f", topology.path, "config",
			)
			render.Dir = root
			rendered, renderErr := render.CombinedOutput()
			if renderErr != nil {
				t.Fatalf("render Compose topology: %v\n%s", renderErr, rendered)
			}
			var renderedRaw composeRaw
			if decodeErr := yaml.Unmarshal(rendered, &renderedRaw); decodeErr != nil {
				t.Fatalf("decode rendered Compose topology: %v", decodeErr)
			}
			assertCaptureStorage(t, topology.service, renderedRaw)
		})
	}

	tsharkImage := readContractFile(t, "tshark image", filepath.Join(root, "test", "interop", "tshark", "Containerfile"))
	if strings.Contains(tsharkImage, "\nVOLUME ") || strings.Contains(tsharkImage, "\nVOLUME\t") {
		t.Error("tshark image declares anonymous or named volume storage")
	}
}

func assertCaptureStorage(t *testing.T, serviceName string, raw composeRaw) {
	t.Helper()

	if len(raw.Volumes) != 0 {
		t.Fatalf("Compose topology declares mutable named volumes: %v", mapsKeys(raw.Volumes))
	}
	serviceNode, ok := raw.Services[serviceName]
	if !ok {
		t.Fatalf("Compose topology is missing service %s", serviceName)
	}
	var storage struct {
		Volumes []string `yaml:"volumes"`
	}
	if err := serviceNode.Decode(&storage); err != nil {
		t.Fatalf("decode %s storage: %v", serviceName, err)
	}
	if len(storage.Volumes) != 0 {
		t.Fatalf("service %s mounts capture storage: %v", serviceName, storage.Volumes)
	}
}

func mapsKeys[V any](input map[string]V) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestTrackedOperationalTextHasNoRemovedReferences(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	allowed := map[string]struct{}{
		".cspell.json":    {},
		"CHANGELOG.md":    {},
		"CHANGELOG.ru.md": {},
		"docs/superpowers/plans/2026-08-21-" + "aio" + "bfd-to-holo-interop.md":        {},
		"docs/superpowers/specs/2026-08-20-" + "aio" + "bfd-to-holo-interop-design.md": {},
	}
	removed := []string{"aio" + "bfd", "bit" + "string"}
	if err := validateTrackedOperationalText(t.Context(), root, allowed, removed); err != nil {
		t.Fatal(err)
	}
}

func TestTrackedOperationalTextScansNonGeneratedPublicAPIFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	publicAPI := filepath.Join(root, "pkg", "bfdpb", "notes.md")
	if err := os.MkdirAll(filepath.Dir(publicAPI), 0o750); err != nil {
		t.Fatalf("create public API directory: %v", err)
	}
	removed := "aio" + "bfd"
	if err := os.WriteFile(publicAPI, []byte("obsolete peer: "+removed+"\n"), 0o600); err != nil {
		t.Fatalf("write public API notes: %v", err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "pkg/bfdpb/notes.md"}} {
		command := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare tracked public API fixture: %v\n%s", err, output)
		}
	}

	err := validateTrackedOperationalText(t.Context(), root, nil, []string{removed})
	if err == nil {
		t.Fatal("non-generated public API file bypassed the operational reference scan")
	}
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
		if err := validateOperationalSurface(path, removedReferences); err != nil {
			t.Errorf("scan benchmark/report surface %s: %v", path, err)
		}
	}

	makefile := readContractFile(t, "Makefile", filepath.Join(root, "Makefile"))
	if strings.Contains(strings.ToLower(makefile), "bench-"+"python") {
		t.Error("benchmark-cross retains the removed benchmark service")
	}
	generatorPath := filepath.Join(root, "scripts", "gen-report.sh")
	generator := readContractFile(t, "benchmark report generator", generatorPath)
	assertContainsAll(t, "benchmark report generator", generator, []string{
		`python3 - "${GO_INPUT}" "${FRR_INPUT}" "${BIRD_INPUT}"`,
		`"${META_JSON}" "${TEMPLATE}" "${OUTPUT}" <<'PY'`,
		"tempfile.NamedTemporaryFile(",
		"os.fsync(temporary.fileno())",
		"os.replace(temporary_path, output_path)",
		"temporary_path.unlink()",
	})
	if strings.Contains(generator, "python3 -c") {
		t.Error("benchmark report generator interpolates shell paths into Python source")
	}
}

func TestBenchmarkResultMountContract(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	compose := readContractFile(t, "benchmark Compose", filepath.Join(root, "bench", "compose.yml"))
	const resultMount = "${BENCH_RESULTS_DIR:-../bench-results}:/results:z"
	if got := strings.Count(compose, resultMount); got != 2 {
		t.Errorf("benchmark Compose result mount count = %d, want 2", got)
	}
	if strings.Contains(compose, "\nvolumes:\n  bench-results:") {
		t.Error("benchmark Compose retains the obsolete named result volume")
	}
	defaultSource := filepath.Clean(filepath.Join(root, "bench", "..", "bench-results"))
	assertEqual(t, "default rendered benchmark result source", defaultSource, filepath.Join(root, "bench-results"))
	render := exec.CommandContext(t.Context(), "podman-compose", "-f", "bench/compose.yml", "config")
	render.Dir = root
	render.Env = append(os.Environ(), "BENCH_RESULTS_DIR="+defaultSource)
	rendered, err := render.CombinedOutput()
	if err != nil {
		t.Fatalf("render benchmark Compose: %v\n%s", err, rendered)
	}
	renderedMount := defaultSource + ":/results:z"
	if got := strings.Count(string(rendered), renderedMount); got != 2 {
		t.Errorf("rendered benchmark result mount %q count = %d, want 2\n%s", renderedMount, got, rendered)
	}

	makefile := readContractFile(t, "Makefile", filepath.Join(root, "Makefile"))
	assertContainsAll(t, "benchmark Make contract", makefile, []string{
		"BENCH_RESULTS := $(CURDIR)/bench-results",
		`@mkdir -p "$(BENCH_RESULTS)"`,
		`BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) build`,
		`BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) run --rm bench-c`,
		`BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) run --rm bench-go`,
		`./scripts/gen-report.sh "$(BENCH_RESULTS)"`,
	})
	spaceResults := filepath.Join(t.TempDir(), "benchmark results with spaces")
	dryRun := exec.CommandContext(
		t.Context(), "make", "--no-print-directory", "-n", "benchmark-cross", "benchmark-report",
		"BENCH_RESULTS="+spaceResults,
	)
	dryRun.Dir = root
	dryRunOutput, err := dryRun.CombinedOutput()
	if err != nil {
		t.Fatalf("render benchmark Make recipes: %v\n%s", err, dryRunOutput)
	}
	assertContainsAll(t, "benchmark Make recipes with spaces", string(dryRunOutput), []string{
		`mkdir -p "` + spaceResults + `"`,
		`BENCH_RESULTS_DIR="` + spaceResults + `" podman-compose -f bench/compose.yml build`,
		`./scripts/gen-report.sh "` + spaceResults + `"`,
	})
}

func TestBenchmarkReportGenerator(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	script := filepath.Join(root, "scripts", "gen-report.sh")

	t.Run("success", func(t *testing.T) {
		results := t.TempDir()
		writeBenchmarkFixtures(t, results)
		outputDir := t.TempDir()
		output := filepath.Join(outputDir, "report.html")
		writeFixture(t, output, "pre-existing report\n")
		stdout, stderr, runErr := runBenchmarkReport(t, script, results, output, "")
		if runErr != nil {
			t.Fatalf("generate report: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout, stderr)
		}
		data := readReportData(t, output)
		got := make([]string, 0, len(data.Implementations))
		for _, implementation := range data.Implementations {
			got = append(got, implementation.ID)
		}
		assertEqual(t, "report implementations", got, []string{"go", "frr", "bird"})
		rendered, readErr := os.ReadFile(output)
		if readErr != nil {
			t.Fatalf("read replaced report: %v", readErr)
		}
		if bytes.Contains(rendered, []byte("__BENCHMARK_DATA__")) {
			t.Error("successful report retains the template marker")
		}
		if !bytes.HasSuffix(rendered, []byte("</html>\n")) {
			t.Error("successful report is incomplete")
		}
		entries, readDirErr := os.ReadDir(outputDir)
		if readDirErr != nil {
			t.Fatalf("read report output directory: %v", readDirErr)
		}
		if len(entries) != 1 || entries[0].Name() != "report.html" {
			t.Errorf("report output directory entries = %v, want only report.html", entries)
		}
		outputInfo, statErr := os.Stat(output)
		if statErr != nil {
			t.Fatalf("stat replaced report: %v", statErr)
		}
		if outputInfo.Mode().Perm() != 0o600 {
			t.Errorf("replaced report mode = %o, want 600", outputInfo.Mode().Perm())
		}
	})

	t.Run("optional metadata defaults", func(t *testing.T) {
		results := t.TempDir()
		writeBenchmarkFixtures(t, results)
		meta := filepath.Join(t.TempDir(), "meta.json")
		writeFixture(t, meta, "{}\n")
		output := filepath.Join(t.TempDir(), "report.html")
		stdout, stderr, runErr := runBenchmarkReport(t, script, results, output, meta)
		if runErr != nil {
			t.Fatalf("generate report: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout, stderr)
		}
		data := readReportData(t, output)
		assertEqual(t, "default report metadata", data.Meta, benchmarkReportMetadata{
			Version: "unknown",
			Go:      "unknown",
		})
	})

	t.Run("output symlink", func(t *testing.T) {
		results := t.TempDir()
		writeBenchmarkFixtures(t, results)
		target := filepath.Join(t.TempDir(), "target.html")
		const targetContents = "existing target\n"
		writeFixture(t, target, targetContents)
		output := filepath.Join(t.TempDir(), "report.html")
		if err := os.Symlink(target, output); err != nil {
			t.Fatalf("create output symlink: %v", err)
		}
		_, stderr, runErr := runBenchmarkReport(t, script, results, output, "")
		if runErr == nil {
			t.Fatal("generator accepted a symlink output target")
		}
		if !strings.Contains(stderr, "regular file path") {
			t.Errorf("stderr = %q, want output target rejection", stderr)
		}
		preserved, readErr := os.ReadFile(target)
		if readErr != nil {
			t.Fatalf("read symlink target: %v", readErr)
		}
		if string(preserved) != targetContents {
			t.Errorf("symlink target changed: %q", preserved)
		}
	})

	tests := []struct {
		name    string
		mutate  func(*testing.T, string) string
		wantErr string
	}{
		{
			name: "missing input",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				if err := os.Remove(filepath.Join(results, "bench-c-bird.txt")); err != nil {
					t.Fatalf("remove BIRD fixture: %v", err)
				}
				return ""
			},
			wantErr: "bench-c-bird.txt",
		},
		{
			name: "malformed record",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				writeFixture(t, filepath.Join(results, "bench-c-frr.txt"), "BENCH\tfrr\tMarshal\tbad\t1000\n")
				return ""
			},
			wantErr: "bench-c-frr.txt",
		},
		{
			name: "malformed Go output",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				writeFixture(t, filepath.Join(results, "bench-go.txt"),
					"BenchmarkControlPacketMarshal-8 1000 5 ns/op 0 B/op 0 allocs/op\n{bad-json}\n")
				return ""
			},
			wantErr: "malformed Go benchmark output",
		},
		{
			name: "not finite",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				writeFixture(t, filepath.Join(results, "bench-c-bird.txt"), "BENCH\tbird\tMarshal\tNaN\t1000\n")
				return ""
			},
			wantErr: "finite",
		},
		{
			name: "negative duration",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				writeFixture(t, filepath.Join(results, "bench-go.txt"),
					"BenchmarkControlPacketMarshal-8 1000 -1 ns/op 0 B/op 0 allocs/op\n")
				return ""
			},
			wantErr: "non-negative",
		},
		{
			name: "negative allocation metric",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				writeFixture(t, filepath.Join(results, "bench-go.txt"),
					"BenchmarkControlPacketMarshal-8 1000 5 ns/op -1 B/op 0 allocs/op\n")
				return ""
			},
			wantErr: "non-negative",
		},
		{
			name: "missing Go headline",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				writeFixture(t, filepath.Join(results, "bench-go.txt"),
					"BenchmarkControlPacketUnmarshal-8 1000 5 ns/op 0 B/op 0 allocs/op\n")
				return ""
			},
			wantErr: "ControlPacketMarshal",
		},
		{
			name: "missing FRR headline",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				writeFixture(t, filepath.Join(results, "bench-c-frr.txt"),
					"BENCH\tfrr\tUnmarshal\t8.0\t1000\n")
				return ""
			},
			wantErr: "FRR Marshal",
		},
		{
			name: "missing BIRD headline",
			mutate: func(t *testing.T, results string) string {
				t.Helper()
				writeFixture(t, filepath.Join(results, "bench-c-bird.txt"),
					"BENCH\tbird\tUnmarshal\t7.0\t1000\n")
				return ""
			},
			wantErr: "BIRD Marshal",
		},
		{
			name: "malformed metadata JSON",
			mutate: func(t *testing.T, _ string) string {
				t.Helper()
				meta := filepath.Join(t.TempDir(), "meta.json")
				writeFixture(t, meta, "{not-json}\n")
				return meta
			},
			wantErr: "meta.json",
		},
		{
			name:    "metadata version boolean",
			mutate:  invalidMetadataMutation(`{"version":true}`),
			wantErr: "version",
		},
		{
			name:    "metadata go number",
			mutate:  invalidMetadataMutation(`{"go":127}`),
			wantErr: "go",
		},
		{
			name:    "metadata version list",
			mutate:  invalidMetadataMutation(`{"version":[]}`),
			wantErr: "version",
		},
		{
			name:    "metadata go object",
			mutate:  invalidMetadataMutation(`{"go":{}}`),
			wantErr: "go",
		},
		{
			name:    "metadata version null",
			mutate:  invalidMetadataMutation(`{"version":null}`),
			wantErr: "version",
		},
		{
			name:    "metadata go empty",
			mutate:  invalidMetadataMutation(`{"go":""}`),
			wantErr: "go",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results := t.TempDir()
			writeBenchmarkFixtures(t, results)
			meta := test.mutate(t, results)
			output := filepath.Join(t.TempDir(), "report.html")
			const previousReport = "previous complete report\n"
			writeFixture(t, output, previousReport)
			stdout, stderr, runErr := runBenchmarkReport(t, script, results, output, meta)
			if runErr == nil {
				t.Fatalf("generator accepted invalid input\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if !strings.Contains(stderr, test.wantErr) {
				t.Errorf("stderr = %q, want actionable reference %q", stderr, test.wantErr)
			}
			preserved, readErr := os.ReadFile(output)
			if readErr != nil {
				t.Fatalf("read preserved report: %v", readErr)
			}
			if string(preserved) != previousReport {
				t.Errorf("invalid input changed the existing report: %q", preserved)
			}
		})
	}
}

func TestOperationalSurfaceScanRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, surface string) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "outside.txt")
				writeFixture(t, target, "safe\n")
				if err := os.Symlink(target, filepath.Join(surface, "link.txt")); err != nil {
					t.Fatalf("create symlink fixture: %v", err)
				}
			},
		},
		{
			name: "binary",
			setup: func(t *testing.T, surface string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(surface, "binary.dat"), []byte{'a', 0, 'b'}, 0o600); err != nil {
					t.Fatalf("write binary fixture: %v", err)
				}
			},
		},
		{
			name: "oversize",
			setup: func(t *testing.T, surface string) {
				t.Helper()
				contents := bytes.Repeat([]byte{'a'}, maxOperationalFileSize+1)
				if err := os.WriteFile(filepath.Join(surface, "large.txt"), contents, 0o600); err != nil {
					t.Fatalf("write oversize fixture: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			surface := t.TempDir()
			test.setup(t, surface)
			if err := validateOperationalSurface(surface, []string{"removed"}); err == nil {
				t.Fatal("unsafe operational surface entry was accepted")
			}
		})
	}
}

const maxOperationalFileSize = 2 << 20

type benchmarkReport struct {
	Meta            benchmarkReportMetadata `json:"meta"`
	Implementations []struct {
		ID string `json:"id"`
	} `json:"implementations"`
}

type benchmarkReportMetadata struct {
	Version string `json:"gobfd_version"`
	Go      string `json:"go_version"`
}

func writeBenchmarkFixtures(t *testing.T, results string) {
	t.Helper()

	writeFixture(t, filepath.Join(results, "bench-go.txt"),
		"goos: linux\nBenchmarkControlPacketMarshal-8 1000 5.5 ns/op 0 B/op 0 allocs/op\nPASS\n")
	writeFixture(t, filepath.Join(results, "bench-c-frr.txt"), "BENCH\tfrr\tMarshal\t8.0\t1000\n")
	writeFixture(t, filepath.Join(results, "bench-c-bird.txt"), "BENCH\tbird\tMarshal\t7.0\t1000\n")
}

func writeFixture(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func invalidMetadataMutation(contents string) func(*testing.T, string) string {
	return func(t *testing.T, _ string) string {
		t.Helper()
		meta := filepath.Join(t.TempDir(), "meta.json")
		writeFixture(t, meta, contents+"\n")
		return meta
	}
}

func runBenchmarkReport(t *testing.T, script, results, output, meta string) (string, string, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), script, results)
	cmd.Env = append(os.Environ(), "BENCH_REPORT_OUTPUT="+output)
	if meta != "" {
		cmd.Env = append(cmd.Env, "BENCH_META_JSON="+meta)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func readReportData(t *testing.T, path string) benchmarkReport {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated report %s: %v", path, err)
	}
	const prefix = "const REPORT_DATA = "
	start := bytes.Index(data, []byte(prefix))
	if start < 0 {
		t.Fatal("generated report has no embedded benchmark data")
	}
	start += len(prefix)
	end := bytes.IndexByte(data[start:], ';')
	if end < 0 {
		t.Fatal("generated report benchmark data is unterminated")
	}
	var report benchmarkReport
	if err := json.Unmarshal(data[start:start+end], &report); err != nil {
		t.Fatalf("decode generated report data: %v", err)
	}
	return report
}

func validateOperationalSurface(path string, removedReferences []string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("operational surface %s is a symlink", path)
	}
	if info.Mode().IsRegular() {
		return validateOperationalFile(path, info, removedReferences)
	}
	if !info.IsDir() {
		return fmt.Errorf("operational surface %s is not a regular file or directory", path)
	}

	return filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", candidate, walkErr)
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect %s: %w", candidate, err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("operational surface entry %s is a symlink", candidate)
		}
		if entryInfo.IsDir() {
			return nil
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("operational surface entry %s is not a regular file", candidate)
		}
		return validateOperationalFile(candidate, entryInfo, removedReferences)
	})
}

func validateOperationalFile(path string, info os.FileInfo, removedReferences []string) error {
	if info.Size() > maxOperationalFileSize {
		return fmt.Errorf("operational surface file %s is %d bytes, limit is %d", path, info.Size(), maxOperationalFileSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read operational surface file %s: %w", path, err)
	}
	if len(data) > maxOperationalFileSize {
		return fmt.Errorf("operational surface file %s grew beyond %d bytes", path, maxOperationalFileSize)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return fmt.Errorf("operational surface file %s contains binary NUL data", path)
	}
	lower := strings.ToLower(path + "\n" + string(data))
	for _, removed := range removedReferences {
		if strings.Contains(lower, removed) {
			return fmt.Errorf("operational surface file %s retains active removed reference %q", path, removed)
		}
	}
	return nil
}

func validateTrackedOperationalText(
	ctx context.Context,
	root string,
	allowed map[string]struct{},
	removedReferences []string,
) error {
	command := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("list tracked repository files: %w", err)
	}

	for pathBytes := range bytes.SplitSeq(output, []byte{0}) {
		if len(pathBytes) == 0 {
			continue
		}
		relative := filepath.ToSlash(string(pathBytes))
		if _, ok := allowed[relative]; ok {
			continue
		}
		if relative == ".beads" || strings.HasPrefix(relative, ".beads/") {
			continue
		}
		for _, removed := range removedReferences {
			if strings.Contains(strings.ToLower(relative), removed) {
				return fmt.Errorf("tracked operational path %s retains removed reference %q", relative, removed)
			}
		}

		clean := filepath.Clean(filepath.FromSlash(relative))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("tracked path %q escapes repository root", relative)
		}
		path := filepath.Join(root, clean)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("lstat tracked operational file %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tracked operational path %s is a symlink", relative)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("tracked operational path %s is not a regular file", relative)
		}
		if info.Size() > maxOperationalFileSize {
			return fmt.Errorf(
				"tracked operational file %s is %d bytes, limit is %d",
				relative,
				info.Size(),
				maxOperationalFileSize,
			)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read tracked operational file %s: %w", relative, err)
		}
		if len(data) > maxOperationalFileSize {
			return fmt.Errorf("tracked operational file %s grew beyond %d bytes", relative, maxOperationalFileSize)
		}
		if marker, generated := trackedGeneratedMarker(relative); generated {
			if !bytes.HasPrefix(data, []byte(marker+"\n")) {
				return fmt.Errorf("tracked generated file %s lacks its exact generator marker", relative)
			}
			continue
		}
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		lower := strings.ToLower(string(data))
		for _, removed := range removedReferences {
			if strings.Contains(lower, removed) {
				return fmt.Errorf("tracked operational file %s retains removed reference %q", relative, removed)
			}
		}
	}
	return nil
}

func trackedGeneratedMarker(path string) (string, bool) {
	switch path {
	case "pkg/bfdpb/bfd/v1/bfd.pb.go":
		return "// Code generated by protoc-gen-go. DO NOT EDIT.", true
	case "pkg/bfdpb/bfd/v1/bfdv1connect/bfd.connect.go":
		return "// Code generated by protoc-gen-connect-go. DO NOT EDIT.", true
	default:
		return "", false
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
	_, removedPeerPresent := raw.Services["aio"+"bfd"]
	return topologyCompose{
		Gobfd:              gobfd,
		Holo:               holo,
		HoloConfig:         holoConfig,
		BFDNet:             bfdnet,
		RemovedPeerPresent: removedPeerPresent,
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
