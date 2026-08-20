package interop_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const holoImage = "ghcr.io/holo-routing/holo-bundle@sha256:" +
	"5c1f61475b1623b3eab611921f8319fb0a10492ced3f7da05e656418abb5ca4a"

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image      string                    `yaml:"image"`
	CapAdd     []string                  `yaml:"cap_add"`
	Volumes    []string                  `yaml:"volumes"`
	Command    []string                  `yaml:"command"`
	Entrypoint string                    `yaml:"entrypoint"`
	Networks   map[string]composeNetwork `yaml:"networks"`
	Health     composeHealthcheck        `yaml:"healthcheck"`
	DependsOn  composeDependencies       `yaml:"depends_on"`
}

type composeNetwork struct {
	IPv4Address string `yaml:"ipv4_address"`
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

type composeDependencies map[string]composeDependency

func (dependencies *composeDependencies) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		if err := node.Decode((*map[string]composeDependency)(dependencies)); err != nil {
			return fmt.Errorf("decode Compose dependency map: %w", err)
		}
		return nil
	case yaml.SequenceNode:
		var names []string
		if err := node.Decode(&names); err != nil {
			return fmt.Errorf("decode Compose dependency list: %w", err)
		}
		*dependencies = make(composeDependencies, len(names))
		for _, name := range names {
			(*dependencies)[name] = composeDependency{}
		}
		return nil
	default:
		return nil
	}
}

type holoStartup struct {
	Interface             string
	InterfaceType         string
	IPv4                  bool
	ProtocolType          string
	ProtocolName          string
	SessionInterface      string
	PeerAddress           string
	SourceAddress         string
	LocalMultiplier       int
	DesiredMinTXInterval  int
	RequiredMinRXInterval int
}

func TestHoloTopologyContract(t *testing.T) {
	t.Parallel()

	compose := loadCompose(t)
	holo, ok := compose.Services["holo"]
	if !ok {
		t.Fatal("holo service is missing")
	}
	if holo.Image != holoImage {
		t.Fatalf("holo image = %q, want immutable %q", holo.Image, holoImage)
	}
	if _, obsoleteExists := compose.Services["aiobfd"]; obsoleteExists {
		t.Fatal("obsolete aiobfd service remains")
	}

	assertEqual(t, "holo capabilities", holo.CapAdd, []string{"NET_RAW", "NET_ADMIN"})
	assertEqual(t, "holo mounts", holo.Volumes, []string{"./holo/holod.toml:/etc/holod.toml:ro,z"})
	assertEqual(t, "holo IPv4 address", holo.Networks["bfdnet"].IPv4Address, "172.20.0.50")
	assertEqual(t, "holo healthcheck command", holo.Health.Test, []string{"CMD-SHELL", "netstat -ltn | grep -q ':50051 '"})
	assertEqual(t, "holo healthcheck interval", holo.Health.Interval, "1s")
	assertEqual(t, "holo healthcheck timeout", holo.Health.Timeout, "1s")
	assertEqual(t, "holo healthcheck retries", holo.Health.Retries, 15)
	assertEqual(t, "holo healthcheck start period", holo.Health.StartPeriod, "2s")

	holoConfig, ok := compose.Services["holo-config"]
	if !ok {
		t.Fatal("holo-config service is missing")
	}
	if _, attached := holoConfig.Networks["bfdnet"]; !attached {
		t.Fatal("holo-config is not attached to bfdnet")
	}
	assertEqual(t, "holo-config image", holoConfig.Image, holoImage)
	assertEqual(t, "holo-config mounts", holoConfig.Volumes, []string{"./holo/holo.startup:/etc/holo.startup:ro,z"})
	assertEqual(t, "holo-config entrypoint", holoConfig.Entrypoint, "holo-cli")
	assertEqual(t, "holo-config command", holoConfig.Command, []string{
		"--address", "http://holo:50051", "--file", "/etc/holo.startup",
	})
	assertDependency(t, holoConfig, "holo", "service_healthy")
	assertDependency(t, compose.Services["gobfd"], "holo-config", "service_completed_successfully")

	root := repositoryRoot(t)
	holod := loadTOML(t, filepath.Join(root, "test", "interop", "holo", "holod.toml"))
	assertTOML(t, holod, "user", "holo")
	assertTOML(t, holod, "group", "holo")
	assertTOML(t, holod, "database_path", "/var/opt/holo/holo.db")
	assertTOML(t, holod, "logging.journald.enabled", false)
	assertTOML(t, holod, "logging.file.enabled", false)
	assertTOML(t, holod, "logging.file.dir", "/var/log/")
	assertTOML(t, holod, "logging.file.name", "holod.log")
	assertTOML(t, holod, "logging.file.rotation", "never")
	assertTOML(t, holod, "logging.file.style", "full")
	assertTOML(t, holod, "logging.file.colors", false)
	assertTOML(t, holod, "logging.file.show_thread_id", false)
	assertTOML(t, holod, "logging.file.show_source", false)
	assertTOML(t, holod, "logging.stdout.enabled", true)
	assertTOML(t, holod, "logging.stdout.style", "full")
	assertTOML(t, holod, "logging.stdout.colors", false)
	assertTOML(t, holod, "logging.stdout.show_thread_id", false)
	assertTOML(t, holod, "logging.stdout.show_source", false)
	assertTOML(t, holod, "event_recorder.enabled", false)
	assertTOML(t, holod, "event_recorder.dir", "/var/opt/holo")
	assertTOML(t, holod, "plugins.grpc.enabled", true)
	assertTOML(t, holod, "plugins.grpc.address", "0.0.0.0:50051")
	assertTOML(t, holod, "plugins.grpc.tls.enabled", false)
	assertTOML(t, holod, "plugins.grpc.tls.certificate", "/etc/ssl/private/holo.pem")
	assertTOML(t, holod, "plugins.grpc.tls.key", "/etc/ssl/certs/holo.key")
	assertTOML(t, holod, "plugins.gnmi.enabled", false)
	assertTOML(t, holod, "plugins.gnmi.address", "0.0.0.0:10161")
	assertTOML(t, holod, "plugins.gnmi.tls.enabled", false)
	assertTOML(t, holod, "plugins.gnmi.tls.certificate", "/etc/ssl/private/holo.pem")
	assertTOML(t, holod, "plugins.gnmi.tls.key", "/etc/ssl/certs/holo.key")

	startup := loadHoloStartup(t, filepath.Join(root, "test", "interop", "holo", "holo.startup"))
	wantStartup := holoStartup{
		Interface:             "eth0",
		InterfaceType:         "iana-if-type:ethernetCsmacd",
		IPv4:                  true,
		ProtocolType:          "ietf-bfd-types:bfdv1",
		ProtocolName:          "main",
		SessionInterface:      "eth0",
		PeerAddress:           "172.20.0.10",
		SourceAddress:         "172.20.0.50",
		LocalMultiplier:       3,
		DesiredMinTXInterval:  300000,
		RequiredMinRXInterval: 300000,
	}
	assertEqual(t, "Holo startup configuration", startup, wantStartup)
}

func loadCompose(t *testing.T) composeFile {
	t.Helper()

	path := filepath.Join(repositoryRoot(t), "test", "interop", "compose.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Compose file %s: %v", path, err)
	}

	var compose composeFile
	if err := yaml.Unmarshal(data, &compose); err != nil {
		t.Fatalf("decode Compose file %s: %v", path, err)
	}

	return compose
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve topology contract test path")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func assertDependency(t *testing.T, service composeService, dependency, condition string) {
	t.Helper()

	got, ok := service.DependsOn[dependency]
	if !ok {
		t.Fatalf("dependency %q is missing", dependency)
	}
	assertEqual(t, dependency+" dependency condition", got.Condition, condition)
}

func assertEqual[T any](t *testing.T, name string, got, want T) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

func loadTOML(t *testing.T, path string) map[string]any {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open TOML file %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close TOML file %s: %v", path, err)
		}
	})

	values := make(map[string]any)
	section := ""
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			if section == "" {
				t.Fatalf("decode TOML file %s:%d: empty section", path, lineNumber)
			}
			continue
		}

		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("decode TOML file %s:%d: expected key = value", path, lineNumber)
		}
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)
		if section != "" {
			key = section + "." + key
		}
		if _, duplicate := values[key]; duplicate {
			t.Fatalf("decode TOML file %s:%d: duplicate key %q", path, lineNumber, key)
		}
		values[key] = parseTOMLScalar(t, path, lineNumber, raw)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan TOML file %s: %v", path, err)
	}

	return values
}

func parseTOMLScalar(t *testing.T, path string, lineNumber int, raw string) any {
	t.Helper()

	if raw == "true" || raw == "false" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			t.Fatalf("decode TOML boolean %s:%d: %v", path, lineNumber, err)
		}
		return value
	}
	value, err := strconv.Unquote(raw)
	if err != nil {
		t.Fatalf("decode TOML string %s:%d: %v", path, lineNumber, err)
	}
	return value
}

func assertTOML(t *testing.T, values map[string]any, key string, want any) {
	t.Helper()

	got, ok := values[key]
	if !ok {
		t.Fatalf("TOML key %q is missing", key)
	}
	assertEqual(t, "TOML "+key, got, want)
}

func loadHoloStartup(t *testing.T, path string) holoStartup {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Holo startup file %s: %v", path, err)
	}

	var config holoStartup
	for lineNumber, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (len(fields) == 1 && fields[0] == "!") {
			continue
		}
		switch {
		case len(fields) == 3 && fields[0] == "interfaces" && fields[1] == "interface":
			config.Interface = fields[2]
		case len(fields) == 2 && fields[0] == "type":
			config.InterfaceType = fields[1]
		case len(fields) == 1 && fields[0] == "ipv4":
			config.IPv4 = true
		case len(fields) == 5 &&
			fields[0] == "routing" &&
			fields[1] == "control-plane-protocols" &&
			fields[2] == "control-plane-protocol":
			config.ProtocolType = fields[3]
			config.ProtocolName = fields[4]
		case len(fields) == 6 &&
			fields[0] == "bfd" &&
			fields[1] == "ip-sh" &&
			fields[2] == "sessions" &&
			fields[3] == "session":
			config.SessionInterface = fields[4]
			config.PeerAddress = fields[5]
		case len(fields) == 2 && fields[0] == "source-addr":
			config.SourceAddress = fields[1]
		case len(fields) == 2 && fields[0] == "local-multiplier":
			config.LocalMultiplier = parseStartupInteger(t, path, lineNumber+1, fields[1])
		case len(fields) == 2 && fields[0] == "desired-min-tx-interval":
			config.DesiredMinTXInterval = parseStartupInteger(t, path, lineNumber+1, fields[1])
		case len(fields) == 2 && fields[0] == "required-min-rx-interval":
			config.RequiredMinRXInterval = parseStartupInteger(t, path, lineNumber+1, fields[1])
		default:
			t.Fatalf("decode Holo startup file %s:%d: unsupported directive %q", path, lineNumber+1, strings.TrimSpace(line))
		}
	}

	return config
}

func parseStartupInteger(t *testing.T, path string, lineNumber int, raw string) int {
	t.Helper()

	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("decode Holo startup integer %s:%d: %v", path, lineNumber, err)
	}
	return value
}
