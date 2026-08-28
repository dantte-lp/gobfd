package interop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
		{name: "Compose topology", path: filepath.Join(root, "test", "interop", "compose.yml")},
		{name: "FRR configuration", path: filepath.Join(root, "test", "interop", "frr", "frr.conf")},
		{name: "BIRD image", path: filepath.Join(root, "test", "interop", "bird3", "Containerfile")},
		{name: "routing runner", path: filepath.Join(root, "test", "internal", "e2erunner", "routing.go")},
		{name: "target inventory", path: filepath.Join(root, "test", "e2e", "targets.md")},
		{name: "Makefile", path: filepath.Join(root, "Makefile")},
		{name: "tagged Go helper", path: filepath.Join(root, "test", "interop", "interop_test.go")},
		{name: "tagged BGP API helper", path: filepath.Join(root, "test", "interop-bgp", "podman_api_test.go")},
		{name: "project control", path: filepath.Join(root, "test", "internal", "interopproject", "controller.go")},
		{name: "gopls gate", path: filepath.Join(root, "test", "internal", "repoquality", "gopls.go")},
		{name: "English interop guide", path: filepath.Join(root, "docs", "en", "05-interop.md")},
		{name: "Russian interop guide", path: filepath.Join(root, "docs", "ru", "05-interop.md")},
		{name: "BGP Compose topology", path: filepath.Join(root, "test", "interop-bgp", "compose.yml")},
		{name: "RFC Compose topology", path: filepath.Join(root, "test", "interop-rfc", "compose.yml")},
		{name: "tshark image", path: filepath.Join(root, "test", "interop", "tshark", "Containerfile")},
		{name: "BFD fuzz image", path: filepath.Join(root, "test", "interop", "scapy", "Containerfile")},
		{name: "container context", path: filepath.Join(root, ".containerignore")},
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

	fuzzImage := contents["BFD fuzz image"]
	assertContainsAll(t, "BFD fuzz image", fuzzImage, []string{
		"golang:1.27.0-trixie@sha256:",
		"debian:trixie-slim@sha256:",
		"go build -trimpath -o /out/bfd-fuzz ./test/interop/scapy",
		`ENTRYPOINT ["/usr/local/bin/bfd-fuzz"]`,
	})
	for _, forbidden := range []string{"alpine", "python", "scapy==", "uv sync", "bfd_fuzz.py"} {
		if strings.Contains(strings.ToLower(fuzzImage), forbidden) {
			t.Errorf("BFD fuzz image retains forbidden runtime %q", forbidden)
		}
	}
	assertContainsAll(t, "container context", contents["container context"], []string{
		"!test/interop/scapy/*.go",
	})
	if strings.Contains(contents["container context"], "!test/interop/scapy/bfd_fuzz.py") {
		t.Error("container context still permits the removed Python BFD fuzzer")
	}
	makefile := contents["Makefile"]
	assertContainsAll(t, "Makefile", makefile, []string{
		"INTEROP_PROJECT_NAME ?= gobfd-interop",
		"override INTEROP_PROJECT_NAME := $(value INTEROP_PROJECT_NAME)",
		"export INTEROP_PROJECT_NAME",
		"INTEROP_CTL := go run ./test/cmd/interopctl",
		"interop-project-validate",
		`"INTEROP_PROJECT_NAME=$${INTEROP_PROJECT_NAME}"`,
		`bgp_project="$${INTEROP_PROJECT_NAME}-bgp"`,
		`env "INTEROP_PROJECT_NAME=$${bgp_project}"`,
		"FRR 10.7.0 + BIRD 3.3.2 + Holo 0.9.0 + Thoro/bfd",
		"gopls-check: dev-ensure",
		"go run ./test/cmd/repoquality gopls --root .",
		"lint-md: dev-ensure",
		"lint-yaml: dev-ensure",
		"proto-lint: dev-ensure",
	})
	assertContainsAll(t, "gopls gate", contents["gopls gate"], []string{
		"testcontainers",
		"e2e_core_testcontainers",
		"no packages discovered",
		"no Go inputs discovered",
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
			"make e2e-rfc\n", "podman logs ", "podman exec ",
		} {
			if strings.Contains(contents[name], command) {
				t.Errorf("%s documents unsafe legacy lifecycle command %q", name, command)
			}
		}
	}
	for _, name := range []string{"BGP Compose topology", "RFC Compose topology"} {
		if strings.Contains(contents[name], "podman compose") {
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
		"interop: interop-testcontainers",
		"interop-bgp: interop-bgp-testcontainers",
		"interop-rfc: interop-rfc-testcontainers",
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
	startProject := contractSection(
		t,
		projectControl,
		"func (c *Controller) start(ctx context.Context) error {",
		"func (c *Controller) stop(ctx context.Context) error {",
	)
	assertOrdered(t, "direct interop up ownership", startProject, []string{
		"c.acquireLock()",
		"c.queryProjectResources(ctx)",
		"c.assertFixedNamesAvailable(ctx)",
		"c.mutation = true",
		`c.compose(ctx, 10*time.Minute, "build")`,
		`c.compose(ctx, commandTimeout, "up", "-d", "holo", "holo-config")`,
		`c.resolveContainerID(ctx, "holo-config-interop")`,
		`c.podmanText(ctx, 45*time.Second, "wait", loaderID)`,
		`c.podmanText(ctx, 10*time.Second, "inspect", "--format", "{{.State.ExitCode}}", loaderID)`,
		"c.verifyHoloConfiguration(ctx, loaderID)",
		`ctx, commandTimeout, "up", "-d", "--no-deps", "gobfd", "frr", "bird3", "tshark", "thoro"`,
		"c.keepProject = true",
	})
	stopProject := contractSection(
		t,
		projectControl,
		"func (c *Controller) stop(ctx context.Context) error {",
		"func (c *Controller) lockRun(ctx context.Context, args []string) error {",
	)
	assertOrdered(t, "direct interop down ownership", stopProject, []string{
		"c.acquireLock()",
		"c.cleanup(ctx)",
	})
	assertContainsAll(t, "direct locked test runner", projectControl, []string{
		`func (c *Controller) lockRun`,
		`c.assertExistingProject(ctx)`,
		`exec.CommandContext(ctx, command[0], command[1:]...)`,
		`required`,
		`optional`,
	})
	assertContainsAll(t, "direct base mandatory containers", projectControl, []string{
		`"gobfd-interop", "frr-interop", "bird3-interop", "tshark-interop"`,
		`"holo-interop", "holo-config-interop", "thoro-interop"`,
		`optional = []string{"scapy-interop"}`,
	})
	assertContainsAll(t, "direct BGP mandatory containers", projectControl, []string{
		`"gobfd-bgp-interop", "gobgp-interop", "tshark-bgp-interop", "frr-bgp-interop"`,
		`"bird3-bgp-interop", "gobfd-exabgp-interop", "exabgp-interop"`,
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
	assertContainsAll(t, "routing Go runner", routing, []string{
		"func (r *runner) runRouting",
		"interopproject.NewProject",
		"controller.Lifecycle",
		"runRoutingSuite",
		"passedGoTest",
		"collectHoloDiagnostics",
		"collectRoutingPcap",
		"routingartifacts.WriteImageID",
		"routingartifacts.ReadImageID",
		"routingartifacts.Merge",
		"mergeOwnerLabelKey",
		"removeLabelledContainers",
	})
	assertOrdered(t, "routing Go lifecycle", routing, []string{
		"controller.Lifecycle",
		"runRoutingSuite",
		"mergeRoutingArtifacts",
	})
	for _, forbidden := range []string{"sh -c", "python3", "localhost/interop_tshark"} {
		if strings.Contains(routing, forbidden) {
			t.Errorf("routing Go runner retains forbidden dependency %q", forbidden)
		}
	}
	assertContainsAll(t, "routing controller lifecycle", projectControl, []string{
		"func (c *Controller) Lifecycle",
		"c.start(ctx)",
		"c.cleanup(cleanupCtx)",
		"c.releaseLock()",
		`"interop-bgp"`,
		"if c.kind == \"bgp\"",
		`c.compose(ctx, commandTimeout, "up", "-d")`,
	})
	inventory := contents["target inventory"]
	assertContainsAll(t, "target inventory", inventory, []string{
		"GoBFD, FRR, BIRD3, Holo, Holo loader, Thoro/bfd, tshark, Go BFD invalid-vector generator",
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

func TestInteropJitterAnalyzerContract(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	tagged := readContractFile(t, "tagged Go helper", filepath.Join(root, "test", "interop", "interop_test.go"))
	assertContainsAll(t, "tagged jitter analyzer", tagged, []string{
		`"github.com/dantte-lp/gobfd/test/internal/bfdjitter"`,
		`bfdjitter.Evaluate(strings.NewReader(output))`,
		`[]string{"frame.time_epoch", "bfd.sta", "bfd.flags.p", "bfd.flags.f"}, 0`,
	})
	assertOrdered(t, "tagged jitter before mutations", tagged, []string{
		`t.Run("RFC5880_6.8.7_JitterCompliance"`,
		`t.Run("RFC5880_6.8.1_SessionIndependence"`,
		`projectContainerCommand(ctx, "frr-interop", "stop")`,
		`t.Run("RFC5880_6.8.6_FRRAdminDown"`,
		`"peer "+gobfdIP, "shutdown"`,
		`t.Run("RFC5880_6.5_PollFinalParameterChange"`,
		`"peer "+gobfdIP, "transmit-interval 200"`,
	})
}

func TestInteropOwnedInlinePythonPortContract(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	bgp := readContractFile(
		t,
		"Go BGP lifecycle",
		filepath.Join(root, "test", "interop-bgp", "testcontainers_topology_test.go"),
	)

	assertContainsAll(t, "Go BGP lifecycle", bgp, []string{
		`func runBGPBFDTestcontainers`,
		`runBGPTestAssertions`,
		`captureBGPTestPCAP`,
	})
	if strings.Contains(bgp, "UV_"+"PYTHON") {
		t.Error("Go BGP lifecycle retains inline Python invocation")
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
				t.Context(), "podman", "compose", "-p", "gobfd-storage-contract",
				"-f", topology.path, "config",
			)
			render.Dir = root
			render.Env = append(os.Environ(), "PODMAN_COMPOSE_WARNING_LOGS=false")
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

func TestTrackedOperationalTextFallsBackWithoutGitMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     func(string, string) string
		contents func(string) []byte
	}{
		{
			name: "removed content",
			path: func(root, _ string) string {
				return filepath.Join(root, "docs", "notes.md")
			},
			contents: func(removed string) []byte {
				return []byte("obsolete peer: " + removed + "\n")
			},
		},
		{
			name: "removed path",
			path: func(root, removed string) string {
				return filepath.Join(root, "test", removed, "notes.md")
			},
			contents: func(string) []byte {
				return []byte("safe\n")
			},
		},
		{
			name: "nested metadata name is not skipped",
			path: func(root, _ string) string {
				return filepath.Join(root, "nested", "reports", "notes.md")
			},
			contents: func(removed string) []byte {
				return []byte("obsolete peer: " + removed + "\n")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			removed := "aio" + "bfd"
			if err := os.WriteFile(
				filepath.Join(root, ".git"),
				[]byte("gitdir: /inaccessible-host-common-directory\n"),
				0o600,
			); err != nil {
				t.Fatalf("write inaccessible worktree metadata fixture: %v", err)
			}
			path := test.path(root, removed)
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatalf("create fallback fixture directory: %v", err)
			}
			if err := os.WriteFile(path, test.contents(removed), 0o600); err != nil {
				t.Fatalf("write fallback fixture: %v", err)
			}

			err := validateTrackedOperationalText(t.Context(), root, nil, []string{removed})
			if err == nil || !strings.Contains(err.Error(), "retains removed reference") {
				t.Fatalf("fallback error = %v, want removed-reference diagnostic", err)
			}
		})
	}
}

func TestTrackedOperationalTextFallbackSkipsOnlyRepositoryMetadataAndBinary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	removed := "aio" + "bfd"
	for _, relative := range []string{
		filepath.Join(".git", "config"),
		filepath.Join(".beads", "issues.jsonl"),
		filepath.Join("reports", "e2e", "report.txt"),
	} {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create metadata fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(removed+"\n"), 0o600); err != nil {
			t.Fatalf("write metadata fixture: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte(removed+"\x00"), 0o600); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("safe\n"), 0o600); err != nil {
		t.Fatalf("write safe fixture: %v", err)
	}

	if err := validateTrackedOperationalText(t.Context(), root, nil, []string{removed}); err != nil {
		t.Fatalf("fallback rejected exact metadata or binary skips: %v", err)
	}
}

func TestTrackedOperationalTextFallbackRejectsUnsafeEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		setup   func(*testing.T, string)
	}{
		{
			name:    "symlink",
			wantErr: "is a symlink",
			setup: func(t *testing.T, root string) {
				t.Helper()
				target := filepath.Join(root, "target.txt")
				writeFixture(t, target, "safe\n")
				if err := os.Symlink(target, filepath.Join(root, "link.txt")); err != nil {
					t.Fatalf("create symlink fixture: %v", err)
				}
			},
		},
		{
			name:    "nonregular",
			wantErr: "is not a regular file",
			setup: func(t *testing.T, root string) {
				t.Helper()
				var listenConfig net.ListenConfig
				listener, err := listenConfig.Listen(t.Context(), "unix", filepath.Join(root, "socket"))
				if err != nil {
					t.Skipf("Unix socket fixture is unavailable: %v", err)
				}
				t.Cleanup(func() { _ = listener.Close() })
			},
		},
		{
			name:    "oversize",
			wantErr: "limit is",
			setup: func(t *testing.T, root string) {
				t.Helper()
				contents := bytes.Repeat([]byte{'a'}, maxOperationalFileSize+1)
				if err := os.WriteFile(filepath.Join(root, "large.txt"), contents, 0o600); err != nil {
					t.Fatalf("write oversize fixture: %v", err)
				}
			},
		},
		{
			name:    "generated marker",
			wantErr: "lacks its exact generator marker",
			setup: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, "pkg", "bfdpb", "bfd", "v1", "bfd.pb.go")
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatalf("create generated fixture directory: %v", err)
				}
				writeFixture(t, path, "package bfdv1\n")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			test.setup(t, root)
			err := validateTrackedOperationalText(t.Context(), root, nil, []string{"removed"})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("fallback error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestTrackedOperationalTextPrefersGitTrackedList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "tracked.txt"), "safe\n")
	removed := "aio" + "bfd"
	writeFixture(t, filepath.Join(root, "untracked.txt"), removed+"\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "tracked.txt"}} {
		command := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare tracked-list preference fixture: %v\n%s", err, output)
		}
	}

	if err := validateTrackedOperationalText(t.Context(), root, nil, []string{removed}); err != nil {
		t.Fatalf("tracked-list preference scanned untracked content: %v", err)
	}
}

func TestTrackedOperationalTextFallbackIsBounded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "first.txt"), "safe\n")
	writeFixture(t, filepath.Join(root, "second.txt"), "safe\n")

	_, err := walkOperationalPathsBounded(t.Context(), root, 1)
	if err == nil || !strings.Contains(err.Error(), "fallback entry limit 1") {
		t.Fatalf("bounded fallback error = %v, want entry-limit diagnostic", err)
	}
}

func TestTrackedOperationalTextFallbackPreservesContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := validateTrackedOperationalText(ctx, t.TempDir(), nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("fallback error = %v, want context cancellation", err)
	}
}

func TestTrackedOperationalTextAllowlistDoesNotBypassSafety(t *testing.T) {
	t.Parallel()

	modes := []struct {
		name    string
		prepare func(*testing.T, string, string)
	}{
		{
			name: "git tracked",
			prepare: func(t *testing.T, root, relative string) {
				t.Helper()
				for _, args := range [][]string{{"init", "-q"}, {"add", "--", relative}} {
					command := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("prepare Git-first allowlist fixture: %v\n%s", err, output)
					}
				}
			},
		},
		{
			name: "forced fallback",
			prepare: func(t *testing.T, root, _ string) {
				t.Helper()
				writeFixture(t, filepath.Join(root, ".git"), "gitdir: /inaccessible-host-common-directory\n")
			},
		},
	}
	unsafeEntries := []struct {
		name    string
		wantErr string
		mutate  func(*testing.T, string)
	}{
		{
			name:    "symlink",
			wantErr: "is a symlink",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				target := path + ".target"
				if err := os.Rename(path, target); err != nil {
					t.Fatalf("move tracked symlink target: %v", err)
				}
				if err := os.Symlink(filepath.Base(target), path); err != nil {
					t.Skipf("symlink fixture is unavailable: %v", err)
				}
			},
		},
		{
			name:    "nonregular",
			wantErr: "is not a regular file",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Rename(path, path+".tracked"); err != nil {
					t.Fatalf("move tracked socket placeholder: %v", err)
				}
				var listenConfig net.ListenConfig
				listener, err := listenConfig.Listen(t.Context(), "unix", path)
				if err != nil {
					t.Skipf("Unix socket fixture is unavailable: %v", err)
				}
				t.Cleanup(func() { _ = listener.Close() })
			},
		},
		{
			name:    "oversize",
			wantErr: "limit is",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				contents := bytes.Repeat([]byte{'a'}, maxOperationalFileSize+1)
				if err := os.WriteFile(path, contents, 0o600); err != nil {
					t.Fatalf("write allowlisted oversize fixture: %v", err)
				}
			},
		},
	}
	for _, mode := range modes {
		for _, unsafeEntry := range unsafeEntries {
			t.Run(mode.name+"/"+unsafeEntry.name, func(t *testing.T) {
				t.Parallel()

				root := t.TempDir()
				const relative = "allowed.txt"
				path := filepath.Join(root, relative)
				writeFixture(t, path, "safe\n")
				mode.prepare(t, root, relative)
				unsafeEntry.mutate(t, path)

				err := validateTrackedOperationalText(
					t.Context(), root, map[string]struct{}{relative: {}}, []string{"removed"},
				)
				if err == nil || !strings.Contains(err.Error(), unsafeEntry.wantErr) {
					t.Fatalf("allowlisted unsafe entry error = %v, want %q", err, unsafeEntry.wantErr)
				}
			})
		}
	}
}

func TestTrackedOperationalTextAllowlistDoesNotBypassGeneratedMarker(t *testing.T) {
	t.Parallel()

	for _, fallback := range []bool{false, true} {
		name := "git tracked"
		if fallback {
			name = "forced fallback"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			const relative = "pkg/bfdpb/bfd/v1/bfd.pb.go"
			path := filepath.Join(root, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatalf("create generated allowlist fixture directory: %v", err)
			}
			writeFixture(t, path, "package bfdv1\n")
			if fallback {
				writeFixture(t, filepath.Join(root, ".git"), "gitdir: /inaccessible-host-common-directory\n")
			} else {
				for _, args := range [][]string{{"init", "-q"}, {"add", "--", relative}} {
					command := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("prepare generated allowlist fixture: %v\n%s", err, output)
					}
				}
			}

			err := validateTrackedOperationalText(
				t.Context(), root, map[string]struct{}{relative: {}}, []string{"removed"},
			)
			if err == nil || !strings.Contains(err.Error(), "lacks its exact generator marker") {
				t.Fatalf("allowlisted generated file error = %v, want marker diagnostic", err)
			}
		})
	}
}

func TestTrackedOperationalTextRejectsRegularReplacementAfterLstat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const relative = "tracked.txt"
	path := filepath.Join(root, relative)
	writeFixture(t, path, "original\n")
	writeFixture(t, filepath.Join(root, "replacement.txt"), "replacement\n")

	rooted, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("open fixture root: %v", err)
	}
	t.Cleanup(func() { _ = rooted.Close() })
	initial, err := rooted.Lstat(relative)
	if err != nil {
		t.Fatalf("lstat original fixture: %v", err)
	}
	if renameErr := os.Rename(path, path+".original"); renameErr != nil {
		t.Fatalf("move original fixture after lstat: %v", renameErr)
	}
	if renameErr := os.Rename(filepath.Join(root, "replacement.txt"), path); renameErr != nil {
		t.Fatalf("replace tracked path with another regular inode: %v", renameErr)
	}

	_, err = readTrackedOperationalFile(rooted, relative, initial)
	if err == nil || !strings.Contains(err.Error(), "changed after lstat") {
		t.Fatalf("regular replacement error = %v, want changed-after-lstat diagnostic", err)
	}
}

func TestTrackedOperationalTextRejectsGrowthAfterLstat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const relative = "tracked.txt"
	path := filepath.Join(root, relative)
	writeFixture(t, path, "small\n")

	rooted, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("open fixture root: %v", err)
	}
	t.Cleanup(func() { _ = rooted.Close() })
	initial, err := rooted.Lstat(relative)
	if err != nil {
		t.Fatalf("lstat small fixture: %v", err)
	}
	_, err = readTrackedOperationalFileWithHook(rooted, relative, initial, func(opened *os.File) error {
		openedInfo, statErr := opened.Stat()
		if statErr != nil {
			return fmt.Errorf("stat opened fixture in growth hook: %w", statErr)
		}
		writer, openErr := rooted.OpenFile(relative, os.O_WRONLY, 0)
		if openErr != nil {
			return fmt.Errorf("open same inode for growth: %w", openErr)
		}
		writerInfo, statErr := writer.Stat()
		if statErr != nil {
			_ = writer.Close()
			return fmt.Errorf("stat writer in growth hook: %w", statErr)
		}
		if !os.SameFile(openedInfo, writerInfo) {
			_ = writer.Close()
			return errors.New("growth hook did not open the same inode")
		}
		if truncateErr := writer.Truncate(int64(maxOperationalFileSize) + 1); truncateErr != nil {
			_ = writer.Close()
			return fmt.Errorf("grow opened inode after identity checks: %w", truncateErr)
		}
		if closeErr := writer.Close(); closeErr != nil {
			return fmt.Errorf("close growth writer: %w", closeErr)
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "grew beyond") {
		t.Fatalf("post-identity growth error = %v, want grew-beyond diagnostic", err)
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
	recipeStart := strings.Index(makefile, "benchmark-report:\n")
	recipeEnd := strings.Index(makefile, "\n# === Quality ===")
	if recipeStart < 0 || recipeEnd < recipeStart {
		t.Fatal("Makefile lacks a bounded benchmark-report recipe")
	}
	reportRecipe := makefile[recipeStart:recipeEnd]
	assertContainsAll(t, "benchmark report generator", reportRecipe, []string{
		`go run ./test/cmd/benchreport --`,
		`"$(BENCH_META_JSON)" "$(BENCH_REPORT_TEMPLATE)" "$(BENCH_REPORT_OUTPUT)"`,
	})
	for _, removed := range []string{"uv run", "python -", "<<'PY'", "tempfile.NamedTemporaryFile"} {
		if strings.Contains(reportRecipe, removed) {
			t.Errorf("benchmark report generator retains removed Python renderer marker %q", removed)
		}
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
	render := benchmarkComposeConfigCommand(t.Context(), root)
	render.Env = append(render.Env, "BENCH_RESULTS_DIR="+defaultSource)
	rendered, err := render.CombinedOutput()
	if err != nil {
		t.Fatalf("render benchmark Compose: %v\n%s", err, rendered)
	}
	type renderedBind struct {
		SELinux string `yaml:"selinux"`
	}
	type renderedMount struct {
		Type   string       `yaml:"type"`
		Source string       `yaml:"source"`
		Target string       `yaml:"target"`
		Bind   renderedBind `yaml:"bind"`
	}
	type renderedService struct {
		Volumes []renderedMount `yaml:"volumes"`
	}
	var renderedConfig struct {
		Services map[string]renderedService `yaml:"services"`
	}
	if decodeErr := yaml.Unmarshal(rendered, &renderedConfig); decodeErr != nil {
		t.Fatalf("decode rendered benchmark Compose: %v\n%s", decodeErr, rendered)
	}
	wantMount := renderedMount{
		Type:   "bind",
		Source: defaultSource,
		Target: "/results",
		Bind:   renderedBind{SELinux: "z"},
	}
	for _, serviceName := range []string{"bench-c", "bench-go"} {
		service, ok := renderedConfig.Services[serviceName]
		if !ok {
			t.Errorf("rendered benchmark Compose is missing service %q", serviceName)
			continue
		}
		resultMounts := 0
		for _, mount := range service.Volumes {
			if mount.Target != wantMount.Target {
				continue
			}
			resultMounts++
			if mount != wantMount {
				t.Errorf("rendered %s result mount = %+v, want %+v", serviceName, mount, wantMount)
			}
		}
		if resultMounts != 1 {
			t.Errorf("rendered %s result mount count = %d, want 1", serviceName, resultMounts)
		}
	}

	makefile := readContractFile(t, "Makefile", filepath.Join(root, "Makefile"))
	assertContainsAll(t, "benchmark Make contract", makefile, []string{
		"BENCH_RESULTS := $(CURDIR)/bench-results",
		`@mkdir -p "$(BENCH_RESULTS)"`,
		`BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) build`,
		`BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) run --rm bench-c`,
		`BENCH_RESULTS_DIR="$(BENCH_RESULTS)" $(BENCH_DC) run --rm bench-go`,
		`go run ./test/cmd/benchreport --`,
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
		`BENCH_RESULTS_DIR="` + spaceResults + `" podman compose -f bench/compose.yml build`,
		`"` + filepath.Join(spaceResults, "bench-go.txt") + `"`,
	})
}

func TestBenchmarkComposeConfigCommandUsesAbsolutePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bench"), 0o750); err != nil {
		t.Fatalf("create benchmark fixture directory: %v", err)
	}
	writeFixture(t, filepath.Join(root, "bench", "compose.yml"), "services: {}\n")

	fakeBin := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "compose-provider.log")
	if err := writeExecutable(filepath.Join(fakeBin, "podman"), `#!/usr/bin/env bash
set -euo pipefail
test "$#" -eq 4
test "$1" = "compose"
test "$2" = "-f"
case "$3" in
  /*) ;;
  *) exit 41 ;;
esac
test "$4" = "config"
printf '%s\n' "$*" >"${BENCH_COMMAND_LOG:?}"
`); err != nil {
		t.Fatalf("write fake podman compose: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	command := benchmarkComposeConfigCommand(t.Context(), root)
	wantArgs := []string{"podman", "compose", "-f", filepath.Join(root, "bench", "compose.yml"), "config"}
	if !slices.Equal(command.Args, wantArgs) {
		t.Fatalf("benchmark Compose argv = %q, want %q", command.Args, wantArgs)
	}
	if !filepath.IsAbs(command.Args[3]) {
		t.Fatalf("benchmark Compose file argument is not absolute: %q", command.Args[3])
	}
	if command.Dir != root {
		t.Fatalf("benchmark Compose working directory = %q, want %q", command.Dir, root)
	}
	command.Env = append(command.Env, "BENCH_COMMAND_LOG="+commandLog)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("execute benchmark Compose command: %v\n%s", err, output)
	}
	logged, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read benchmark Compose command log: %v", err)
	}
	wantLog := "compose -f " + filepath.Join(root, "bench", "compose.yml") + " config\n"
	if string(logged) != wantLog {
		t.Fatalf("benchmark Compose command log = %q, want %q", logged, wantLog)
	}
}

func TestDevEnsureUsesComposeServiceID(t *testing.T) {
	root, rootErr := repositoryRoot()
	if rootErr != nil {
		t.Fatalf("resolve repository root: %v", rootErr)
	}

	fakeBin := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	if err := writeExecutable(filepath.Join(fakeBin, "podman"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${PODMAN_COMMAND_LOG:?}"
case "$*" in
  "compose -p dev-contract -f deployments/compose/compose.dev.yml ps --all --quiet dev")
    printf '%s\n' immutable-dev-id
    ;;
  "inspect immutable-dev-id --format "*Mounts*)
    printf '%s\n' "${EXPECTED_ROOT:?}"
    ;;
  "compose -p dev-contract -f deployments/compose/compose.dev.yml up -d --no-build dev")
    ;;
  *)
    exit 97
    ;;
esac
`); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}

	command := exec.CommandContext(
		t.Context(),
		"make", "--no-print-directory", "dev-ensure", "COMPOSE_PROJECT_NAME=dev-contract",
	)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PODMAN_COMMAND_LOG="+commandLog,
		"EXPECTED_ROOT="+root,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run dev-ensure contract: %v\n%s", err, output)
	}

	logged, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake Podman command log: %v", err)
	}
	commands := string(logged)
	assertContainsAll(t, "dev-ensure Podman commands", commands, []string{
		"compose -p dev-contract -f deployments/compose/compose.dev.yml ps --all --quiet dev",
		"inspect immutable-dev-id --format",
		"compose -p dev-contract -f deployments/compose/compose.dev.yml up -d --no-build dev",
	})
	if strings.Contains(commands, "--build") || strings.Contains(commands, "--force-recreate") {
		t.Fatalf("dev-ensure rebuilt an existing Compose v5 service container:\n%s", commands)
	}
}

func TestBenchmarkReportGenerator(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	t.Run("success", func(t *testing.T) {
		results := t.TempDir()
		writeBenchmarkFixtures(t, results)
		outputDir := t.TempDir()
		output := filepath.Join(outputDir, "report.html")
		writeFixture(t, output, "pre-existing report\n")
		stdout, stderr, runErr := runBenchmarkReport(t, root, results, output, "")
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
		stdout, stderr, runErr := runBenchmarkReport(t, root, results, output, meta)
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
		_, stderr, runErr := runBenchmarkReport(t, root, results, output, "")
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
			stdout, stderr, runErr := runBenchmarkReport(t, root, results, output, meta)
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

const (
	maxOperationalFileSize         = 2 << 20
	maxDependencyInventoryFileSize = 3 << 20
	maxOperationalEntries          = 10_000
)

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

func runBenchmarkReport(t *testing.T, root, results, output, meta string) (string, string, error) {
	t.Helper()

	if meta == "" {
		meta = filepath.Join(root, "testdata", "benchmarks", "v0.4.0", "meta.json")
	}
	cmd := exec.CommandContext(
		t.Context(),
		"go", "run", "./test/cmd/benchreport", "--",
		filepath.Join(results, "bench-go.txt"),
		filepath.Join(results, "bench-c-frr.txt"),
		filepath.Join(results, "bench-c-bird.txt"),
		meta,
		filepath.Join(root, "scripts", "report-template.html"),
		output,
	)
	cmd.Dir = root
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
	paths, err := trackedOperationalPaths(ctx, root)
	if err != nil {
		return err
	}
	rooted, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open repository root %s: %w", root, err)
	}

	validationErr := validateTrackedOperationalPaths(ctx, rooted, paths, allowed, removedReferences)
	closeErr := rooted.Close()
	if validationErr != nil {
		return validationErr
	}
	if closeErr != nil {
		return fmt.Errorf("close repository root %s: %w", root, closeErr)
	}
	return nil
}

func validateTrackedOperationalPaths(
	ctx context.Context,
	rooted *os.Root,
	paths []string,
	allowed map[string]struct{},
	removedReferences []string,
) error {
	for _, relative := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateTrackedOperationalPath(rooted, relative, allowed, removedReferences); err != nil {
			return err
		}
	}
	return nil
}

func trackedOperationalPaths(ctx context.Context, root string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z")
	output, gitErr := command.Output()
	if gitErr == nil {
		deletedCommand := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--deleted")
		deletedOutput, deletedErr := deletedCommand.Output()
		if deletedErr != nil {
			return nil, fmt.Errorf("list deleted tracked repository files: %w", deletedErr)
		}
		deleted := make(map[string]struct{}, bytes.Count(deletedOutput, []byte{0}))
		for pathBytes := range bytes.SplitSeq(deletedOutput, []byte{0}) {
			if len(pathBytes) != 0 {
				deleted[filepath.ToSlash(string(pathBytes))] = struct{}{}
			}
		}
		paths := make([]string, 0, bytes.Count(output, []byte{0}))
		for pathBytes := range bytes.SplitSeq(output, []byte{0}) {
			if len(pathBytes) == 0 {
				continue
			}
			path := filepath.ToSlash(string(pathBytes))
			if _, isDeleted := deleted[path]; !isDeleted {
				paths = append(paths, path)
			}
		}
		return paths, nil
	}

	paths, err := walkOperationalPaths(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("list tracked repository files: %w; then walk working tree: %w", gitErr, err)
	}
	return paths, nil
}

func walkOperationalPaths(ctx context.Context, root string) ([]string, error) {
	return walkOperationalPathsBounded(ctx, root, maxOperationalEntries)
}

func walkOperationalPathsBounded(ctx context.Context, root string, maxEntries int) ([]string, error) {
	paths := make([]string, 0, 512)
	entries := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("visit %s: %w", path, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolve working-tree path %s: %w", path, err)
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if fallbackMetadataPath(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		entries++
		if entries > maxEntries {
			return fmt.Errorf("working tree exceeds fallback entry limit %d", maxEntries)
		}
		if !entry.IsDir() {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func fallbackMetadataPath(relative string) bool {
	for _, metadata := range []string{".git", ".beads", ".venv", "reports"} {
		if relative == metadata || strings.HasPrefix(relative, metadata+"/") {
			return true
		}
	}
	return false
}

func validateTrackedOperationalPath(
	rooted *os.Root,
	relative string,
	allowed map[string]struct{},
	removedReferences []string,
) error {
	if relative == ".beads" || strings.HasPrefix(relative, ".beads/") {
		return nil
	}

	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("tracked path %q escapes repository root", relative)
	}
	info, err := rooted.Lstat(clean)
	if err != nil {
		return fmt.Errorf("lstat tracked operational file %s: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("tracked operational path %s is a symlink", relative)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("tracked operational path %s is not a regular file", relative)
	}
	fileSizeLimit := trackedOperationalFileSizeLimit(relative)
	if info.Size() > int64(fileSizeLimit) {
		return fmt.Errorf(
			"tracked operational file %s is %d bytes, limit is %d",
			relative,
			info.Size(),
			fileSizeLimit,
		)
	}
	data, err := readTrackedOperationalFile(rooted, clean, info)
	if err != nil {
		return fmt.Errorf("read tracked operational file %s: %w", relative, err)
	}
	if marker, generated := trackedGeneratedMarker(relative); generated {
		if !bytes.HasPrefix(data, []byte(marker+"\n")) {
			return fmt.Errorf("tracked generated file %s lacks its exact generator marker", relative)
		}
		return nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil
	}
	if _, ok := allowed[relative]; ok {
		return nil
	}
	lower := strings.ToLower(relative + "\n" + string(data))
	for _, removed := range removedReferences {
		if strings.Contains(lower, removed) {
			return fmt.Errorf("tracked operational file %s retains removed reference %q", relative, removed)
		}
	}
	return nil
}

func readTrackedOperationalFile(rooted *os.Root, relative string, initial os.FileInfo) ([]byte, error) {
	return readTrackedOperationalFileWithHook(rooted, relative, initial, nil)
}

func readTrackedOperationalFileWithHook(
	rooted *os.Root,
	relative string,
	initial os.FileInfo,
	afterIdentity func(*os.File) error,
) ([]byte, error) {
	fileSizeLimit := trackedOperationalFileSizeLimit(relative)
	file, err := rooted.Open(relative)
	if err != nil {
		return nil, fmt.Errorf("open rooted file: %w", err)
	}
	opened, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat opened file: %w", statErr)
	}
	if !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("opened path is not a regular file")
	}
	if opened.Size() > int64(fileSizeLimit) {
		_ = file.Close()
		return nil, fmt.Errorf("opened file is %d bytes, limit is %d", opened.Size(), fileSizeLimit)
	}
	current, lstatErr := rooted.Lstat(relative)
	if lstatErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lstat opened path: %w", lstatErr)
	}
	if current.Mode()&os.ModeSymlink != 0 {
		_ = file.Close()
		return nil, errors.New("path changed after lstat to a symlink")
	}
	if !os.SameFile(initial, opened) || !os.SameFile(current, opened) {
		_ = file.Close()
		return nil, errors.New("path changed after lstat")
	}
	if afterIdentity != nil {
		if hookErr := afterIdentity(file); hookErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("run post-identity read hook: %w", hookErr)
		}
	}

	data, readErr := io.ReadAll(io.LimitReader(file, int64(fileSizeLimit)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read bounded file: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close rooted file: %w", closeErr)
	}
	if len(data) > fileSizeLimit {
		return nil, fmt.Errorf("file grew beyond %d bytes", fileSizeLimit)
	}
	return data, nil
}

func trackedOperationalFileSizeLimit(relative string) int {
	if filepath.ToSlash(relative) == "docs/supply-chain/dependency-inventory.json" {
		return maxDependencyInventoryFileSize
	}

	return maxOperationalFileSize
}

func benchmarkComposeConfigCommand(ctx context.Context, root string) *exec.Cmd {
	command := exec.CommandContext(
		ctx,
		"podman", "compose", "-f", filepath.Join(root, "bench", "compose.yml"),
		"config",
	)
	command.Dir = root
	command.Env = append(os.Environ(), "PODMAN_COMPOSE_WARNING_LOGS=false")
	return command
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

func contractSection(t *testing.T, contents, startMarker, endMarker string) string {
	t.Helper()

	start := strings.Index(contents, startMarker)
	if start < 0 {
		t.Fatalf("contract section start %q is missing", startMarker)
	}
	end := strings.Index(contents[start:], endMarker)
	if end < 0 {
		t.Fatalf("contract section end %q is missing", endMarker)
	}
	return contents[start : start+end]
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
