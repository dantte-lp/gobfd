package interop_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

type composeFile struct {
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

func loadCompose(root string) (composeFile, error) {
	path := filepath.Join(root, "test", "interop", "compose.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return composeFile{}, fmt.Errorf("read Compose file %s: %w", path, err)
	}
	compose, err := decodeCompose(data)
	if err != nil {
		return composeFile{}, fmt.Errorf("decode Compose file %s: %w", path, err)
	}
	return compose, nil
}

func decodeCompose(data []byte) (composeFile, error) {
	raw, err := decodeKnownFields[composeRaw](data, "Compose root")
	if err != nil {
		return composeFile{}, err
	}
	gobfdNode, err := requiredComposeNode(raw.Services, "gobfd", "Compose services")
	if err != nil {
		return composeFile{}, err
	}
	holoNode, err := requiredComposeNode(raw.Services, "holo", "Compose services")
	if err != nil {
		return composeFile{}, err
	}
	holoConfigNode, err := requiredComposeNode(raw.Services, "holo-config", "Compose services")
	if err != nil {
		return composeFile{}, err
	}
	bfdnetNode, err := requiredComposeNode(raw.Networks, "bfdnet", "Compose networks")
	if err != nil {
		return composeFile{}, err
	}

	gobfd, err := decodeComposeNode[composeGobfdService](gobfdNode, "gobfd service")
	if err != nil {
		return composeFile{}, err
	}
	holo, err := decodeComposeNode[composeHoloService](holoNode, "holo service")
	if err != nil {
		return composeFile{}, err
	}
	holoConfig, err := decodeComposeNode[composeHoloConfigService](holoConfigNode, "holo-config service")
	if err != nil {
		return composeFile{}, err
	}
	bfdnet, err := decodeComposeNode[composeTopNetwork](bfdnetNode, "bfdnet network")
	if err != nil {
		return composeFile{}, err
	}
	_, aiobfdPresent := raw.Services["aiobfd"]
	return composeFile{
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
