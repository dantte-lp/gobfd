package clabbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
)

const (
	labName           = "gobfd-vendors"
	gobfdContainer    = "clab-" + labName + "-gobfd"
	gobfdImage        = "gobfd-clab:latest"
	ownerLabel        = "io.gobfd.interop-clab.owner"
	runLabel          = "io.gobfd.interop-clab.run"
	containerlabLabel = "containerlab"
	maxReceiptSize    = 1 << 20
	secureFileMode    = os.FileMode(0o600)
	podmanExec        = "exec"
	vendorNokia       = "nokia"
	vendorSONiC       = "sonic"
	topologyLinkEnds  = 2

	gobfdConfigHeader = `grpc:
  addr: ":50052"
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
gobgp:
  enabled: true
  addr: "127.0.0.1:50051"
  strategy: "disable-peer"
  dampening:
    enabled: false
sessions:
`
	gobgpConfigHeader = `[global.config]
as = 65001
router-id = "10.0.0.1"
port = 179

[global.apply-policy.config]
default-import-policy = "accept-route"
default-export-policy = "accept-route"
`
	bfdSessionFormat = `  # %s
  - peer: %q
    local: %q
    type: single_hop
    desired_min_tx: "300ms"
    required_min_rx: "300ms"
    detect_mult: 3
`
	bgpNeighborFormat = `
# %s
[[neighbors]]
[neighbors.config]
neighbor-address = %q
peer-as = %d
[neighbors.transport.config]
local-address = %q
[[neighbors.afi-safis]]
[neighbors.afi-safis.config]
afi-safi-name = %q
`
	inspectOwnershipFormat = `{{.ID}}|` +
		`{{ index .Config.Labels "containerlab" }}|` +
		`{{ index .Config.Labels "io.gobfd.interop-clab.owner" }}|` +
		`{{ index .Config.Labels "io.gobfd.interop-clab.run" }}`
)

var (
	errLifecycleState = errors.New("invalid vendor lab lifecycle state")
	projectSlugRE     = regexp.MustCompile(`[^a-z0-9_-]+`)
)

type vendorProfile struct {
	name      string
	container string
	image     string
	index     int
	peerIPv4  string
	localIPv4 string
	peerIPv6  string
	localIPv6 string
	asn       int
}

type vendorCandidate struct {
	profile vendorProfile
	images  []string
}

type lifecycleReceipt struct {
	SchemaVersion int               `json:"schema_version"`
	RunID         string            `json:"run_id"`
	Topology      string            `json:"topology"`
	Containers    map[string]string `json:"containers"`
}

type topologyDocument struct {
	Name     string `yaml:"name"`
	Topology struct {
		Nodes map[string]map[string]any `yaml:"nodes"`
		Links []struct {
			Endpoints []string `yaml:"endpoints"`
		} `yaml:"links"`
	} `yaml:"topology"`
}

func runTopology(ctx context.Context, options Options, runner Runner) (returnErr error) {
	if !options.Deploy && !options.Test && !options.TestOnly && !options.Down {
		return nil
	}
	if options.DryRun {
		return dryRunTopology(ctx, options, runner)
	}

	lock, err := acquireLifecycleLock(options.ProjectRoot)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, releaseLifecycleLock(lock))
	}()

	switch {
	case options.Down:
		return destroyTopology(ctx, options, runner)
	case options.TestOnly:
		if _, err := loadLifecycleReceipt(options.ProjectRoot); err != nil {
			return err
		}
		return runVendorTests(ctx, options, runner)
	default:
		return deployTopology(ctx, options, runner)
	}
}

func dryRunTopology(ctx context.Context, options Options, runner Runner) error {
	if options.Down {
		return runCommand(ctx, runner, Command{
			Executable: executableContainerlab,
			Arguments: []string{
				"destroy", "--runtime", "podman",
				"--topo", runtimeTopologyPath(options.ProjectRoot), "--cleanup",
			},
			DryRun: true,
		})
	}
	if options.TestOnly {
		return runVendorTests(ctx, options, runner)
	}
	profiles := defaultDryRunProfiles(options, mustFRRReference(options.ProjectRoot))
	if err := startGoBFDContainer(ctx, options, runner, "dry-run"); err != nil {
		return err
	}
	if err := runCommand(ctx, runner, Command{
		Executable: executableContainerlab,
		Arguments:  []string{"deploy", "--runtime", "podman", "--topo", runtimeTopologyPath(options.ProjectRoot)},
		DryRun:     true,
	}); err != nil {
		return err
	}
	if err := configureGoBFD(ctx, options, runner, profiles); err != nil {
		return err
	}
	if options.Test {
		return runVendorTests(ctx, options, runner)
	}
	return nil
}

func deployTopology(ctx context.Context, options Options, runner Runner) (returnErr error) {
	profiles, receipt, err := prepareTopologyDeployment(ctx, options, runner)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			returnErr = errors.Join(returnErr, cleanupTopology(ctx, options, runner, receipt))
		}
	}()
	if err := activateTopology(ctx, options, runner, profiles, &receipt); err != nil {
		return err
	}
	if options.Deploy {
		keep = true
		return nil
	}
	return runVendorTests(ctx, options, runner)
}

func prepareTopologyDeployment(
	ctx context.Context,
	options Options,
	runner Runner,
) ([]vendorProfile, lifecycleReceipt, error) {
	if _, err := os.Lstat(receiptPath(options.ProjectRoot)); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return nil, lifecycleReceipt{}, fmt.Errorf(
				"vendor lab receipt already exists; run interop-clab-down: %w",
				errLifecycleState,
			)
		}
		return nil, lifecycleReceipt{}, fmt.Errorf("inspect vendor lab receipt: %w", err)
	}
	profiles, err := availableProfiles(ctx, options, runner)
	if err != nil {
		return nil, lifecycleReceipt{}, err
	}
	if len(profiles) == 0 {
		return nil, lifecycleReceipt{}, fmt.Errorf("no vendor image is available: %w", errLifecycleState)
	}
	topologyPath, err := writeRuntimeTopology(options, profiles)
	if err != nil {
		return nil, lifecycleReceipt{}, err
	}
	receipt := lifecycleReceipt{
		SchemaVersion: 1,
		RunID:         fmt.Sprintf("%d-%d", time.Now().UTC().UnixNano(), os.Getpid()),
		Topology:      topologyPath,
		Containers:    make(map[string]string, len(profiles)+1),
	}
	if err := createLifecycleReceipt(options.ProjectRoot, receipt); err != nil {
		return nil, lifecycleReceipt{}, err
	}
	return profiles, receipt, nil
}

func activateTopology(
	ctx context.Context,
	options Options,
	runner Runner,
	profiles []vendorProfile,
	receipt *lifecycleReceipt,
) error {
	if err := startGoBFDContainer(ctx, options, runner, receipt.RunID); err != nil {
		return err
	}
	if err := runCommand(ctx, runner, Command{
		Executable: executableContainerlab,
		Arguments:  []string{"deploy", "--runtime", "podman", "--topo", receipt.Topology},
		Directory:  filepath.Dir(receipt.Topology),
	}); err != nil {
		return fmt.Errorf("deploy vendor topology: %w", err)
	}
	for _, profile := range profiles {
		if err := waitContainerRunning(ctx, runner, profile.container, 30*time.Second); err != nil {
			return fmt.Errorf("wait for %s container: %w", profile.name, err)
		}
	}
	for _, name := range append([]string{gobfdContainer}, profileContainerNames(profiles)...) {
		id, err := inspectOwnedContainer(ctx, runner, name, receipt.RunID)
		if err != nil {
			return err
		}
		receipt.Containers[name] = id
	}
	if err := writeLifecycleReceipt(options.ProjectRoot, *receipt); err != nil {
		return err
	}
	if err := configureGoBFD(ctx, options, runner, profiles); err != nil {
		return err
	}
	if err := waitAndConfigureVendors(ctx, options, runner, profiles); err != nil {
		return err
	}
	waitForBFD(ctx, options, runner)
	return nil
}

func availableProfiles(ctx context.Context, options Options, runner Runner) ([]vendorProfile, error) {
	frrReference, err := loadFRRReference(options.ProjectRoot)
	if err != nil {
		return nil, err
	}
	candidates := vendorCandidates(options, frrReference)
	profiles := make([]vendorProfile, 0, len(candidates))
	for _, candidate := range candidates {
		image, err := firstAvailableImage(ctx, options, runner, candidate.images)
		if err != nil {
			return nil, fmt.Errorf("inspect %s image: %w", candidate.profile.name, err)
		}
		if image == "" {
			if options.Logger != nil {
				options.Logger.InfoContext(
					ctx,
					"vendor profile skipped",
					"vendor",
					candidate.profile.name,
					"images",
					candidate.images,
				)
			}
			continue
		}
		profile := candidate.profile
		profile.image = image
		profile.container = "clab-" + labName + "-" + profile.name
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func vendorCandidates(options Options, frrReference string) []vendorCandidate {
	candidates := licensedVendorCandidates(options)
	return append(candidates, publicVendorCandidates(options, frrReference)...)
}

func licensedVendorCandidates(options Options) []vendorCandidate {
	return []vendorCandidate{
		{
			profile: vendorProfile{
				name: "arista", index: 1, peerIPv4: "10.0.1.2", localIPv4: "10.0.1.1",
				peerIPv6: "fd00:0:1::1", localIPv6: "fd00:0:1::", asn: 65002,
			},
			images: []string{
				options.Tags.Arista, "localhost/ceos:4.36.0.1F", "ceos:4.36.0.1F",
				"localhost/ceos:4.35.2F", "ceos:4.35.2F",
			},
		},
		{
			profile: vendorProfile{
				name: "cisco", index: 3, peerIPv4: "10.0.3.2", localIPv4: "10.0.3.1",
				peerIPv6: "fd00:0:3::1", localIPv6: "fd00:0:3::", asn: 65004,
			},
			images: []string{
				options.Tags.Cisco,
				"ios-xr/xrd-control-plane:24.3.1",
				"ios-xr/xrd-control-plane:latest",
			},
		},
	}
}

func publicVendorCandidates(options Options, frrReference string) []vendorCandidate {
	return []vendorCandidate{
		{
			profile: vendorProfile{
				name: vendorNokia, index: 2, peerIPv4: "10.0.2.2", localIPv4: "10.0.2.1",
				peerIPv6: "fd00:0:2::1", localIPv6: "fd00:0:2::", asn: 65003,
			},
			images: []string{
				"ghcr.io/nokia/srlinux:" + options.Tags.Nokia,
				"ghcr.io/nokia/srlinux:24.10.1",
			},
		},
		{
			profile: vendorProfile{
				name: vendorSONiC, index: 4,
				peerIPv4: "10.0.4.2", localIPv4: "10.0.4.1", asn: 65005,
			},
			images: []string{
				"docker.io/netreplica/docker-sonic-vs:" + options.Tags.Sonic,
				"netreplica/docker-sonic-vs:latest",
				"docker-sonic-vs:latest",
			},
		},
		{
			profile: vendorProfile{
				name: "vyos", index: 5,
				peerIPv4: "10.0.5.2", localIPv4: "10.0.5.1", asn: 65006,
			},
			images: []string{
				vyosTargetImage, "localhost/vyos:latest",
				"docker.io/muruu1/vyos:latest", "muruu1/vyos:latest",
			},
		},
		{
			profile: vendorProfile{
				name: "frr", index: 6, peerIPv4: "10.0.6.2", localIPv4: "10.0.6.1",
				peerIPv6: "fd00:0:6::1", localIPv6: "fd00:0:6::", asn: 65007,
			},
			images: []string{frrReference},
		},
	}
}

func firstAvailableImage(ctx context.Context, options Options, runner Runner, candidates []string) (string, error) {
	for _, image := range candidates {
		if image == "" {
			continue
		}
		exists, err := imageExists(ctx, options, runner, image)
		if err != nil {
			return "", err
		}
		if exists {
			return image, nil
		}
	}
	return "", nil
}

func defaultDryRunProfiles(options Options, frrReference string) []vendorProfile {
	profiles := []vendorProfile{
		{
			name: vendorNokia, image: "ghcr.io/nokia/srlinux:" + options.Tags.Nokia,
			index: 2, peerIPv4: "10.0.2.2", localIPv4: "10.0.2.1",
			peerIPv6: "fd00:0:2::1", localIPv6: "fd00:0:2::", asn: 65003,
		},
		{
			name: vendorSONiC, image: "docker.io/netreplica/docker-sonic-vs:" + options.Tags.Sonic,
			index: 4, peerIPv4: "10.0.4.2", localIPv4: "10.0.4.1", asn: 65005,
		},
		{name: "vyos", image: vyosTargetImage, index: 5, peerIPv4: "10.0.5.2", localIPv4: "10.0.5.1", asn: 65006},
		{
			name: "frr", image: frrReference, index: 6,
			peerIPv4: "10.0.6.2", localIPv4: "10.0.6.1",
			peerIPv6: "fd00:0:6::1", localIPv6: "fd00:0:6::", asn: 65007,
		},
	}
	for index := range profiles {
		profiles[index].container = "clab-" + labName + "-" + profiles[index].name
	}
	return profiles
}

func writeRuntimeTopology(options Options, profiles []vendorProfile) (string, error) {
	source := filepath.Join(options.ProjectRoot, "test", "interop-clab", "gobfd-vendors.clab.yml")
	file, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open vendor topology: %w", err)
	}
	defer file.Close()
	var topology topologyDocument
	decoder := yaml.NewDecoder(io.LimitReader(file, 1<<20))
	if decodeErr := decoder.Decode(&topology); decodeErr != nil {
		return "", fmt.Errorf("decode vendor topology: %w", decodeErr)
	}
	selected := map[string]bool{gobfdContainer: true}
	images := make(map[string]string, len(profiles))
	for _, profile := range profiles {
		selected[profile.name] = true
		images[profile.name] = profile.image
	}
	for name, node := range topology.Topology.Nodes {
		if !selected[name] {
			delete(topology.Topology.Nodes, name)
			continue
		}
		delete(node, podmanExec)
		if image := images[name]; image != "" {
			node["image"] = image
		}
	}
	topology.Topology.Links = slices.DeleteFunc(topology.Topology.Links, func(link struct {
		Endpoints []string `yaml:"endpoints"`
	},
	) bool {
		if len(link.Endpoints) != topologyLinkEnds {
			return true
		}
		for _, endpoint := range link.Endpoints {
			name, _, _ := strings.Cut(endpoint, ":")
			if !selected[name] {
				return true
			}
		}
		return false
	})
	data, err := yaml.Marshal(topology)
	if err != nil {
		return "", fmt.Errorf("encode runtime vendor topology: %w", err)
	}
	path := runtimeTopologyPath(options.ProjectRoot)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write runtime vendor topology: %w", err)
	}
	return path, nil
}

func startGoBFDContainer(ctx context.Context, options Options, runner Runner, runID string) error {
	return runCommand(ctx, runner, Command{
		Executable: executablePodman,
		Arguments: []string{
			"run", "--detach", "--name", gobfdContainer,
			"--label", ownerLabel + "=" + labName,
			"--label", runLabel + "=" + runID,
			"--cap-add", "NET_RAW", "--cap-add", "NET_ADMIN",
			"--user", "0:0", "--entrypoint", "sleep", gobfdImage, "infinity",
		},
		DryRun: options.DryRun,
	})
}

func configureGoBFD(ctx context.Context, options Options, runner Runner, profiles []vendorProfile) error {
	if err := configureGoBFDLinks(ctx, options, runner, profiles); err != nil {
		return err
	}
	runtimeDir, err := writeDaemonConfigs(options, profiles)
	if err != nil {
		return err
	}
	for _, command := range daemonCommands(options, runtimeDir) {
		if err := runCommand(ctx, runner, command); err != nil {
			return fmt.Errorf("start GoBFD services: %w", err)
		}
	}
	return nil
}

func configureGoBFDLinks(
	ctx context.Context,
	options Options,
	runner Runner,
	profiles []vendorProfile,
) error {
	for _, profile := range profiles {
		interfaceName := fmt.Sprintf("eth%d", profile.index)
		commands := [][]string{
			{podmanExec, gobfdContainer, "ip", "addr", "add", profile.localIPv4 + "/30", "dev", interfaceName},
			{podmanExec, gobfdContainer, "ip", "link", "set", interfaceName, "up"},
		}
		if profile.localIPv6 != "" {
			commands = append(commands, []string{
				podmanExec, gobfdContainer, "ip", "-6", "addr", "add",
				profile.localIPv6 + "/127", "dev", interfaceName,
			})
		}
		for _, arguments := range commands {
			command := Command{
				Executable: executablePodman,
				Arguments:  arguments,
				DryRun:     options.DryRun,
			}
			if err := runCommand(ctx, runner, command); err != nil {
				return fmt.Errorf("configure GoBFD link for %s: %w", profile.name, err)
			}
		}
	}
	return nil
}

func writeDaemonConfigs(options Options, profiles []vendorProfile) (string, error) {
	gobfdConfig, gobgpConfig := renderDaemonConfigs(profiles)
	runtimeDir := runtimeDirectory(options.ProjectRoot)
	if options.DryRun {
		return runtimeDir, nil
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("create vendor runtime directory: %w", err)
	}
	gobfdPath := filepath.Join(runtimeDir, "gobfd.generated.yml")
	if err := os.WriteFile(gobfdPath, []byte(gobfdConfig), 0o600); err != nil {
		return "", fmt.Errorf("write GoBFD runtime config: %w", err)
	}
	gobgpPath := filepath.Join(runtimeDir, "gobgp.generated.toml")
	if err := os.WriteFile(gobgpPath, []byte(gobgpConfig), 0o600); err != nil {
		return "", fmt.Errorf("write GoBGP runtime config: %w", err)
	}
	return runtimeDir, nil
}

func daemonCommands(options Options, runtimeDir string) []Command {
	return []Command{
		{
			Executable: executablePodman,
			Arguments:  []string{podmanExec, gobfdContainer, "mkdir", "-p", "/etc/gobfd", "/etc/gobgp"},
			DryRun:     options.DryRun,
		},
		{
			Executable: executablePodman,
			Arguments: []string{
				"cp", filepath.Join(runtimeDir, "gobfd.generated.yml"),
				gobfdContainer + ":/etc/gobfd/gobfd.yml",
			},
			DryRun: options.DryRun,
		},
		{
			Executable: executablePodman,
			Arguments: []string{
				"cp", filepath.Join(runtimeDir, "gobgp.generated.toml"),
				gobfdContainer + ":/etc/gobgp/gobgp.toml",
			},
			DryRun: options.DryRun,
		},
		{
			Executable: executablePodman,
			Arguments: []string{
				podmanExec, "--detach", gobfdContainer,
				"gobgpd", "-f", "/etc/gobgp/gobgp.toml", "-l", "info",
			},
			DryRun: options.DryRun,
		},
		{
			Executable: executablePodman,
			Arguments: []string{
				podmanExec, "--detach", gobfdContainer,
				"/bin/gobfd", "-config", "/etc/gobfd/gobfd.yml",
			},
			DryRun: options.DryRun,
		},
	}
}

func renderDaemonConfigs(profiles []vendorProfile) (string, string) {
	var bfd strings.Builder
	bfd.WriteString(gobfdConfigHeader)
	var bgp strings.Builder
	bgp.WriteString(gobgpConfigHeader)
	for _, profile := range profiles {
		writeBFDSession(&bfd, profile.name, profile.peerIPv4, profile.localIPv4)
		writeBGPNeighbor(&bgp, profile.name, profile.peerIPv4, profile.localIPv4, profile.asn, "ipv4-unicast")
		if profile.peerIPv6 != "" {
			writeBFDSession(&bfd, profile.name+" IPv6", profile.peerIPv6, profile.localIPv6)
			writeBGPNeighbor(&bgp, profile.name+" IPv6", profile.peerIPv6, profile.localIPv6, profile.asn, "ipv6-unicast")
		}
	}
	return bfd.String(), bgp.String()
}

func writeBFDSession(builder *strings.Builder, name, peer, local string) {
	fmt.Fprintf(builder, bfdSessionFormat, name, peer, local)
}

func writeBGPNeighbor(builder *strings.Builder, name, peer, local string, asn int, family string) {
	fmt.Fprintf(builder, bgpNeighborFormat, name, peer, asn, local, family)
}

func waitAndConfigureVendors(ctx context.Context, options Options, runner Runner, profiles []vendorProfile) error {
	for _, profile := range profiles {
		var err error
		switch profile.name {
		case "arista":
			err = waitExec(ctx, runner, profile.container, []string{"Cli", "-p", "15", "-c", "show version"}, 3*time.Minute)
		case vendorNokia:
			err = waitExec(ctx, runner, profile.container, []string{"sr_cli", "-d", "show version"}, 3*time.Minute)
		case "cisco":
			err = waitExec(ctx, runner, profile.container, []string{"xr_cli", "show version"}, 5*time.Minute)
		case vendorSONiC:
			err = configureSONiC(ctx, options, runner, profile.container)
		}
		if err != nil {
			return fmt.Errorf("configure %s: %w", profile.name, err)
		}
	}
	return nil
}

func configureSONiC(ctx context.Context, options Options, runner Runner, container string) error {
	commands := [][]string{
		{podmanExec, container, "config", "interface", "ip", "add", "Ethernet0", "10.0.4.2/30"},
		{podmanExec, container, "config", "interface", "startup", "Ethernet0"},
		{podmanExec, container, "supervisorctl", "start", "bgpd"},
	}
	for _, arguments := range commands {
		result, err := runner.Run(ctx, Command{
			Executable: executablePodman,
			Arguments:  arguments,
			DryRun:     options.DryRun,
		})
		if err != nil {
			return fmt.Errorf("run SONiC command %q: %w", arguments, err)
		}
		if result.ExitCode != 0 && !slices.Contains(arguments, "supervisorctl") {
			return fmt.Errorf("SONiC command %q exited %d: %w", arguments, result.ExitCode, ErrBootstrapFailed)
		}
	}
	if err := runCommand(ctx, runner, Command{
		Executable: executablePodman,
		Arguments: []string{
			podmanExec, "--detach", container,
			"/usr/lib/frr/bfdd", "-A", "127.0.0.1",
		},
		DryRun: options.DryRun,
	}); err != nil {
		return err
	}
	if options.DryRun {
		return nil
	}
	if err := waitExec(ctx, runner, container, []string{"vtysh", "-c", "show bgp summary"}, 2*time.Minute); err != nil {
		return err
	}
	arguments := []string{
		podmanExec, container, "vtysh",
		"-c", "configure terminal",
		"-c", "ip route 10.20.4.0/24 blackhole",
		"-c", "router bgp 65005",
		"-c", "bgp router-id 10.0.4.2",
		"-c", "neighbor 10.0.4.1 remote-as 65001",
		"-c", "neighbor 10.0.4.1 bfd",
		"-c", "address-family ipv4 unicast",
		"-c", "network 10.20.4.0/24",
		"-c", "exit-address-family",
		"-c", "exit",
		"-c", "bfd",
		"-c", "peer 10.0.4.1 interface Ethernet0",
		"-c", "detect-multiplier 3",
		"-c", "receive-interval 300",
		"-c", "transmit-interval 300",
		"-c", "no shutdown",
		"-c", "exit",
		"-c", "exit",
		"-c", "end",
	}
	return runCommand(ctx, runner, Command{Executable: executablePodman, Arguments: arguments})
}

func waitExec(ctx context.Context, runner Runner, container string, argv []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		arguments := append([]string{podmanExec, container}, argv...)
		result, err := runner.Run(ctx, Command{
			Executable: executablePodman,
			Arguments:  arguments,
		})
		if err == nil && result.ExitCode == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("command %q did not succeed within %s: %w", argv, timeout, ErrBootstrapFailed)
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for %s: %w", container, ctx.Err())
		case <-timer.C:
		}
	}
}

func waitContainerRunning(ctx context.Context, runner Runner, container string, timeout time.Duration) error {
	return waitHostCommand(
		ctx,
		runner,
		[]string{"inspect", "--type", "container", "--format", "{{.State.Running}}", container},
		"true",
		timeout,
	)
}

func waitForBFD(ctx context.Context, options Options, runner Runner) {
	if options.DryRun {
		return
	}
	command := []string{"gobfdctl", "--addr", "localhost:50052", "session", "list"}
	if err := waitExec(ctx, runner, gobfdContainer, command, 3*time.Minute); err != nil {
		if options.Logger != nil {
			options.Logger.WarnContext(ctx, "BFD convergence wait expired; Go tests retain per-vendor verdicts", "error", err)
		}
	}
}

func runVendorTests(ctx context.Context, options Options, runner Runner) error {
	rootName := strings.ToLower(filepath.Base(options.ProjectRoot))
	project := strings.Trim(projectSlugRE.ReplaceAllString(rootName, "-"), "-")
	if project == "" {
		return fmt.Errorf("derive Compose project name from %s: %w", options.ProjectRoot, errInvalidBootstrapOptions)
	}
	return runCommand(ctx, runner, Command{
		Executable: executablePodman,
		Arguments: []string{
			"compose", "-p", project, "-f", filepath.Join(options.ProjectRoot, "deployments", "compose", "compose.dev.yml"),
			podmanExec, "-T", "dev", "env", "GOBFD_INTEROP_CLAB_TEST_IN_CONTAINER=1",
			"go", "test", "-tags", "interop_clab", "-v", "-count=1", "-timeout", "600s", "./test/interop-clab/",
		},
		Directory: options.ProjectRoot,
		DryRun:    options.DryRun,
	})
}

func destroyTopology(ctx context.Context, options Options, runner Runner) error {
	receipt, err := loadLifecycleReceipt(options.ProjectRoot)
	if err != nil {
		return err
	}
	return cleanupTopology(ctx, options, runner, receipt)
}

func cleanupTopology(ctx context.Context, options Options, runner Runner, receipt lifecycleReceipt) error {
	var cleanupErr error
	for name, wantID := range receipt.Containers {
		gotID, err := inspectOwnedContainer(ctx, runner, name, receipt.RunID)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		if gotID != wantID {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("container %s changed identity: %w", name, errLifecycleState))
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	cleanupErr = errors.Join(cleanupErr, runCommand(ctx, runner, Command{
		Executable: executableContainerlab,
		Arguments:  []string{"destroy", "--runtime", "podman", "--topo", receipt.Topology, "--cleanup"},
		Directory:  filepath.Dir(receipt.Topology),
	}))
	if id := receipt.Containers[gobfdContainer]; id != "" {
		result, err := runner.Run(ctx, Command{
			Executable: executablePodman,
			Arguments:  []string{"container", "exists", id},
		})
		switch {
		case err != nil:
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect GoBFD container during cleanup: %w", err))
		case result.ExitCode == 0:
			cleanupErr = errors.Join(cleanupErr, runCommand(ctx, runner, Command{
				Executable: executablePodman,
				Arguments:  []string{"rm", "--force", id},
			}))
		case result.ExitCode != 1:
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf(
				"inspect GoBFD container during cleanup: exit %d: %w",
				result.ExitCode,
				ErrBootstrapFailed,
			))
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	for _, path := range []string{receiptPath(options.ProjectRoot), receipt.Topology} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove vendor runtime path %s: %w", path, err))
		}
	}
	return cleanupErr
}

func inspectOwnedContainer(ctx context.Context, runner Runner, name, runID string) (string, error) {
	result, err := runner.Run(ctx, Command{
		Executable: executablePodman,
		Arguments: []string{
			"inspect", "--type", "container", "--format",
			inspectOwnershipFormat,
			name,
		},
	})
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w", name, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("inspect container %s: exit %d: %w", name, result.ExitCode, ErrBootstrapFailed)
	}
	fields := strings.Split(strings.TrimSpace(result.Stdout), "|")
	if len(fields) != 4 || fields[0] == "" {
		return "", fmt.Errorf("decode ownership for container %s: %w", name, errLifecycleState)
	}
	if name == gobfdContainer {
		if fields[2] != labName || fields[3] != runID {
			return "", fmt.Errorf("GoBFD container %s is not owned by run %s: %w", name, runID, errLifecycleState)
		}
	} else if fields[1] != labName {
		return "", fmt.Errorf("vendor container %s lacks Containerlab ownership: %w", name, errLifecycleState)
	}
	return fields[0], nil
}

func profileContainerNames(profiles []vendorProfile) []string {
	names := make([]string, len(profiles))
	for index, profile := range profiles {
		names[index] = profile.container
	}
	return names
}

func acquireLifecycleLock(projectRoot string) (*os.File, error) {
	directory := runtimeDirectory(projectRoot)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create vendor runtime directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("validate vendor runtime directory %s: %w", directory, errLifecycleState)
	}
	path := filepath.Join(directory, "lifecycle.lock")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open vendor lifecycle lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		closeErr := file.Close()
		return nil, fmt.Errorf("acquire vendor lifecycle lock: %w", errors.Join(err, closeErr))
	}
	return file, nil
}

func releaseLifecycleLock(file *os.File) error {
	if file == nil {
		return nil
	}
	if err := errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close()); err != nil {
		return fmt.Errorf("release vendor lifecycle lock: %w", err)
	}
	return nil
}

func createLifecycleReceipt(projectRoot string, receipt lifecycleReceipt) error {
	path := receiptPath(projectRoot)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create vendor lifecycle receipt: %w", err)
	}
	encoder := json.NewEncoder(file)
	encodeErr := encoder.Encode(receipt)
	closeErr := file.Close()
	if err := errors.Join(encodeErr, closeErr); err != nil {
		return fmt.Errorf("write vendor lifecycle receipt: %w", err)
	}
	return nil
}

func writeLifecycleReceipt(projectRoot string, receipt lifecycleReceipt) error {
	path := receiptPath(projectRoot)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("validate vendor lifecycle receipt: %w", errLifecycleState)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode vendor lifecycle receipt: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("update vendor lifecycle receipt: %w", err)
	}
	return nil
}

func loadLifecycleReceipt(projectRoot string) (lifecycleReceipt, error) {
	path := receiptPath(projectRoot)
	info, err := os.Lstat(path)
	if err != nil {
		return lifecycleReceipt{}, fmt.Errorf("inspect vendor lifecycle receipt: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	invalidFile := !ok || !info.Mode().IsRegular() || info.Mode().Perm() != secureFileMode
	invalidOwner := ok && int(stat.Uid) != os.Getuid()
	invalidSize := info.Size() <= 0 || info.Size() > maxReceiptSize
	if invalidFile || invalidOwner || invalidSize {
		return lifecycleReceipt{}, fmt.Errorf("validate vendor lifecycle receipt %s: %w", path, errLifecycleState)
	}
	file, err := os.Open(path)
	if err != nil {
		return lifecycleReceipt{}, fmt.Errorf("open vendor lifecycle receipt: %w", err)
	}
	defer file.Close()
	var receipt lifecycleReceipt
	decoder := json.NewDecoder(io.LimitReader(file, maxReceiptSize))
	if err := decoder.Decode(&receipt); err != nil {
		return lifecycleReceipt{}, fmt.Errorf("decode vendor lifecycle receipt: %w", err)
	}
	invalidReceipt := receipt.SchemaVersion != 1 || receipt.RunID == "" ||
		!filepath.IsAbs(receipt.Topology) || len(receipt.Containers) == 0
	if invalidReceipt {
		return lifecycleReceipt{}, fmt.Errorf("validate vendor lifecycle receipt fields: %w", errLifecycleState)
	}
	return receipt, nil
}

func waitHostCommand(ctx context.Context, runner Runner, arguments []string, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		result, err := runner.Run(ctx, Command{Executable: executablePodman, Arguments: arguments})
		if err == nil && result.ExitCode == 0 && strings.TrimSpace(result.Stdout) == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("podman %q did not return %q within %s: %w", arguments, want, timeout, ErrBootstrapFailed)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for podman command: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func runtimeDirectory(projectRoot string) string {
	return filepath.Join(projectRoot, "test", "interop-clab", "gobfd", ".runtime")
}

func receiptPath(projectRoot string) string {
	return filepath.Join(runtimeDirectory(projectRoot), "lifecycle.json")
}

func runtimeTopologyPath(projectRoot string) string {
	return filepath.Join(projectRoot, "test", "interop-clab", "gobfd-vendors.generated.clab.yml")
}

func mustFRRReference(projectRoot string) string {
	reference, err := loadFRRReference(projectRoot)
	if err != nil {
		return "quay.io/frrouting/frr:10.7.0"
	}
	return reference
}
