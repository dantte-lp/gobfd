package interop_test

import (
	"context"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // Contract test executes the repository runner with isolated fake Podman commands.
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const holoInterfaceConfig = `{"name":"eth0","type":"iana-if-type:ethernetCsmacd","ietf-ip:ipv4":{}}`

const holoSessionConfig = `{"interface":"eth0","dest-addr":"172.20.0.10",` +
	`"source-addr":"172.20.0.50","local-multiplier":3,` +
	`"desired-min-tx-interval":300000,"required-min-rx-interval":300000}`

const holoProtocolConfig = `{"type":"ietf-bfd-types:bfdv1","name":"main",` +
	`"ietf-bfd:bfd":{"ietf-bfd-ip-sh:ip-sh":{"sessions":{"session":[` +
	holoSessionConfig + `]}}}}`

const validHoloRunningConfig = `{"ietf-interfaces:interfaces":{"interface":[` +
	holoInterfaceConfig + `]},"ietf-routing:routing":{"control-plane-protocols":` +
	`{"control-plane-protocol":[` + holoProtocolConfig + `]}}}`

const holoSemanticPodmanArgs = "exec immutable-holo-id holo-cli --no-colors --no-pager " +
	"--address http://127.0.0.1:50051 --command show running format json"

const holoSemanticCommandLog = "podman " + holoSemanticPodmanArgs

const (
	interopFakeMode = "GOBFD_INTEROP_FAKE_MODE"
	fakeRaceOptions = "GORACE=atexit_sleep_ms=0"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(interopFakeMode); mode != "" {
		os.Exit(runInteropFakeCommand(mode, filepath.Base(os.Args[0]), os.Args[1:]))
	}
	os.Exit(m.Run())
}

func TestProjectControlHoloSemanticGate(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	tests := map[string]struct {
		loaderLog    string
		wantSecondUp bool
		wantCleanup  bool
		want         string
	}{
		"valid exact configuration": {wantSecondUp: true},
		"partial invalid loader log": {
			loaderLog:   "% failed to parse one startup line",
			wantCleanup: true,
			want:        "Holo configuration loader reported parser or commit errors",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fakeBin := t.TempDir()
			stateDir := t.TempDir()
			commandLog := filepath.Join(t.TempDir(), "commands.log")
			fakeModeEnv := installFakeCommand(t, fakeBin, "podman", "holo-semantic")
			installFakeCommand(t, fakeBin, "docker-compose", "holo-semantic")
			cmd := projectControlCommand(t.Context(), root, "up")
			cmd.Env = append(os.Environ(),
				fakeModeEnv,
				fakeRaceOptions,
				"PATH="+fakeBin+":"+os.Getenv("PATH"),
				"INTEROP_FAKE_COMMAND_LOG="+commandLog,
				"INTEROP_FAKE_STATE_DIR="+stateDir,
				"INTEROP_FAKE_LOADER_LOG="+test.loaderLog,
				"INTEROP_FAKE_SEMANTIC_CONFIG="+validHoloRunningConfig,
				"XDG_RUNTIME_DIR="+secureRuntimeDir(t),
			)
			output, runErr := cmd.CombinedOutput()
			if test.want != "" {
				if runErr == nil {
					t.Fatalf("project control accepted invalid Holo loader output; output:\n%s", output)
				}
				if !strings.Contains(string(output), test.want) {
					t.Fatalf("project control output is missing %q; output:\n%s", test.want, output)
				}
			} else if runErr != nil {
				t.Fatalf("project control rejected valid Holo configuration: %v; output:\n%s", runErr, output)
			}
			commands, readErr := os.ReadFile(commandLog)
			if readErr != nil {
				t.Fatalf("read fake command log: %v", readErr)
			}
			sharedSequence := []string{
				"podman wait immutable-holo-config-id",
				"podman inspect --format {{.State.ExitCode}} immutable-holo-config-id",
				"podman logs immutable-holo-config-id",
				"podman container exists holo-interop",
				"podman inspect --type container --format {{.ID}}|" +
					"{{ index .Config.Labels \"com.docker.compose.project\" }} holo-interop",
				"podman exec immutable-holo-id holo-cli --version",
				holoSemanticCommandLog,
			}
			if test.wantSecondUp {
				sharedSequence = append(sharedSequence,
					"-p gobfd-interop -f "+filepath.Join(root, "test", "interop", "compose.yml")+
						" up -d --no-deps gobfd frr bird3 tshark thoro",
				)
			}
			assertCommandSubsequence(t, string(commands), sharedSequence)
			secondUp := strings.Contains(string(commands), "up -d --no-deps gobfd frr bird3 tshark thoro")
			if secondUp != test.wantSecondUp {
				t.Fatalf("project control second phase = %t, want %t; commands:\n%s", secondUp, test.wantSecondUp, commands)
			}
			if !test.wantCleanup {
				return
			}
			assertCommandSubsequence(t, string(commands), []string{
				"podman inspect --type container --format {{json .}} immutable-holo-id",
				"podman inspect --type container --format {{json .}} immutable-holo-config-id",
				"podman rm -f -- immutable-holo-id",
				"podman container exists immutable-holo-id",
				"podman rm -f -- immutable-holo-config-id",
				"podman container exists immutable-holo-config-id",
				"podman ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
			})
			for _, marker := range []string{"holo-removed", "loader-removed"} {
				if _, statErr := os.Stat(filepath.Join(stateDir, marker)); statErr != nil {
					t.Errorf("cleanup marker %s: %v", marker, statErr)
				}
			}
			absence := exec.CommandContext(
				t.Context(), filepath.Join(fakeBin, "podman"),
				"ps", "-a", "--no-trunc", "--filter",
				"label=com.docker.compose.project=gobfd-interop", "--format", "{{.ID}}",
			)
			absence.Env = cmd.Env
			remaining, absenceErr := absence.CombinedOutput()
			if absenceErr != nil || len(remaining) != 0 {
				t.Fatalf("cleaned helper resources = %q, error = %v; want none", remaining, absenceErr)
			}
		})
	}
}

func TestHoloSemanticHelperRejectsExtraComposeServices(t *testing.T) {
	tests := map[string]struct {
		arguments []string
		marker    string
	}{
		"phase one": {
			arguments: []string{"up", "-d", "holo", "holo-config", "unintended-extra-service"},
			marker:    "started",
		},
		"phase two": {
			arguments: []string{
				"up", "-d", "--no-deps", "gobfd", "frr", "bird3", "tshark", "thoro",
				"unintended-extra-service",
			},
			marker: "phase2",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv("INTEROP_FAKE_COMMAND_LOG", filepath.Join(t.TempDir(), "commands.log"))
			t.Setenv("INTEROP_FAKE_STATE_DIR", stateDir)
			arguments := append([]string{
				"-p", "gobfd-interop", "-f", filepath.Join(t.TempDir(), "compose.yml"),
			}, test.arguments...)

			if code := runInteropFakeCommand("holo-semantic", "docker-compose", arguments); code == 0 {
				t.Errorf("helper accepted compose %s arguments with an extra service", name)
			}
			if _, err := os.Stat(filepath.Join(stateDir, test.marker)); err == nil {
				t.Errorf("helper created %s marker for compose %s arguments with an extra service", test.marker, name)
			} else if !os.IsNotExist(err) {
				t.Fatalf("inspect unexpected %s marker: %v", test.marker, err)
			}
		})
	}
}

func TestMakeRejectsInvalidInteropProjectBeforeCommand(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	commandMarker := filepath.Join(t.TempDir(), "compose-provider-called")
	fakeModeEnv := installFakeCommand(t, fakeBin, "podman", "make-marker")
	injectionMarker := filepath.Join(t.TempDir(), "injected")
	projectName := "safe; printf injected > " + injectionMarker + "; #"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "interop-up", "INTEROP_PROJECT_NAME="+projectName)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		fakeModeEnv, fakeRaceOptions, "PATH="+fakeBin+":"+os.Getenv("PATH"),
		"INTEROP_FAKE_MAKE_MARKER="+commandMarker,
	)
	output, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("make accepted invalid INTEROP_PROJECT_NAME; output:\n%s", output)
	}
	if _, err := os.Stat(injectionMarker); !os.IsNotExist(err) {
		t.Fatalf("invalid project name reached injected shell command: %v", err)
	}
	if _, err := os.Stat(commandMarker); !os.IsNotExist(err) {
		t.Fatalf("invalid project name reached podman compose: %v", err)
	}
}

func TestMakeDoesNotExpandNestedProjectFunctions(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	commandMarker := filepath.Join(t.TempDir(), "compose-provider-called")
	fakeModeEnv := installFakeCommand(t, fakeBin, "podman", "make-marker")
	shellMarker := filepath.Join(t.TempDir(), "make-shell-expanded")
	projectName := "$(info MAKE-INFO-EXPANDED)$(shell printf expanded > " + shellMarker + ")safe"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "interop-up", "INTEROP_PROJECT_NAME="+projectName)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		fakeModeEnv, fakeRaceOptions, "PATH="+fakeBin+":"+os.Getenv("PATH"),
		"INTEROP_FAKE_MAKE_MARKER="+commandMarker,
	)
	output, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("make accepted nested-function INTEROP_PROJECT_NAME; output:\n%s", output)
	}
	for line := range strings.Lines(string(output)) {
		if strings.TrimSpace(line) == "MAKE-INFO-EXPANDED" {
			t.Fatalf("make expanded nested info function; output:\n%s", output)
		}
	}
	if _, err := os.Stat(shellMarker); !os.IsNotExist(err) {
		t.Fatalf("make expanded nested shell function: %v", err)
	}
	if _, err := os.Stat(commandMarker); !os.IsNotExist(err) {
		t.Fatalf("nested-function project name reached podman compose: %v", err)
	}
}

func TestProjectControlLockRunRejectsArbitraryLabelledResource(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, resourceKind := range []string{"container", "network", "volume"} {
		t.Run(resourceKind, func(t *testing.T) {
			fakeBin := t.TempDir()
			commandLog := filepath.Join(t.TempDir(), "podman.log")
			marker := filepath.Join(t.TempDir(), "command-ran")
			fakeModeEnv := installFakeCommand(t, fakeBin, "podman", "arbitrary-resource")
			cmd := projectControlCommand(
				t.Context(), root,
				"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
			)
			cmd.Env = append(os.Environ(),
				fakeModeEnv,
				fakeRaceOptions,
				"PATH="+fakeBin+":"+os.Getenv("PATH"),
				"INTEROP_FAKE_PODMAN_LOG="+commandLog,
				"INTEROP_FAKE_RESOURCE_KIND="+resourceKind,
				"XDG_RUNTIME_DIR="+secureRuntimeDir(t),
			)
			if output, runErr := cmd.CombinedOutput(); runErr == nil {
				t.Fatalf("arbitrary exact-labelled %s authorized command; output:\n%s", resourceKind, output)
			}
			if _, markerErr := os.Stat(marker); !os.IsNotExist(markerErr) {
				t.Fatalf("unauthorized locked command created marker: %v", markerErr)
			}
			commands, readErr := os.ReadFile(commandLog)
			if readErr != nil {
				t.Fatalf("read fake podman log: %v", readErr)
			}
			if !strings.Contains(string(commands), "label=com.docker.compose.project=gobfd-interop") {
				t.Fatalf("lock-run did not query exact project resources; commands:\n%s", commands)
			}
		})
	}
}

func TestProjectControlLockRunRequiresAllMandatoryContainers(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "command-ran")
	fakeModeEnv := installFakeCommand(t, fakeBin, "podman", "mandatory-containers")
	cmd := projectControlCommand(
		t.Context(), root,
		"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
	)
	cmd.Env = append(os.Environ(),
		fakeModeEnv,
		fakeRaceOptions,
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"XDG_RUNTIME_DIR="+secureRuntimeDir(t),
	)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("run command with all mandatory containers: %v; output:\n%s", runErr, output)
	}
	contents, readErr := os.ReadFile(marker)
	if readErr != nil || string(contents) != "ran" {
		t.Fatalf("locked command marker = %q, %v", contents, readErr)
	}
}

func TestProjectControlLockRunRejectsEveryMissingMandatoryContainer(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	testCases := []struct {
		kind       string
		project    string
		containers []string
	}{
		{
			kind:    "base",
			project: "gobfd-interop",
			containers: []string{
				"gobfd-interop", "frr-interop", "bird3-interop", "tshark-interop",
				"holo-interop", "holo-config-interop", "thoro-interop",
			},
		},
		{
			kind:    "bgp",
			project: "gobfd-interop-bgp",
			containers: []string{
				"gobfd-bgp-interop", "gobgp-interop", "tshark-bgp-interop", "frr-bgp-interop",
				"bird3-bgp-interop", "gobfd-exabgp-interop", "exabgp-interop",
			},
		},
	}
	for _, testCase := range testCases {
		for _, missingContainer := range testCase.containers {
			t.Run(testCase.kind+"/"+missingContainer, func(t *testing.T) {
				fakeBin := t.TempDir()
				marker := filepath.Join(t.TempDir(), "command-ran")
				fakeModeEnv := installFakeCommand(t, fakeBin, "podman", "missing-mandatory")
				cmd := projectControlCommand(
					t.Context(), root,
					"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
				)
				cmd.Env = append(os.Environ(),
					fakeModeEnv,
					fakeRaceOptions,
					"PATH="+fakeBin+":"+os.Getenv("PATH"),
					"INTEROP_PROJECT_KIND="+testCase.kind,
					"INTEROP_PROJECT_NAME="+testCase.project,
					"INTEROP_FAKE_MISSING_CONTAINER="+missingContainer,
					"XDG_RUNTIME_DIR="+secureRuntimeDir(t),
				)
				if output, runErr := cmd.CombinedOutput(); runErr == nil {
					t.Fatalf("missing mandatory container %s authorized command; output:\n%s", missingContainer, output)
				}
				if _, markerErr := os.Stat(marker); !os.IsNotExist(markerErr) {
					t.Fatalf("unauthorized locked command created marker: %v", markerErr)
				}
			})
		}
	}
}

func assertCommandSubsequence(t *testing.T, commandLog string, want []string) {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(commandLog), "\n")
	next := 0
	for _, line := range lines {
		if next >= len(want) {
			break
		}
		if line == want[next] || (strings.HasSuffix(want[next], " ") && strings.HasPrefix(line, want[next])) {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("runner command sequence is missing %q after %d matches; commands:\n%s", want[next], next, commandLog)
	}
}

func TestForbiddenProjectMutation(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		commandLog string
		want       string
	}{
		"exact label queries are read only": {
			commandLog: "podman ps -a --filter label=project --format {{.ID}}\n" +
				"podman network ls --filter label=project --format {{.ID}}\n" +
				"podman volume ls --filter label=project --format {{.Name}}\n",
		},
		"Compose build": {
			commandLog: "-p gobfd-interop -f compose.yml build\n",
			want:       "-p gobfd-interop -f compose.yml build",
		},
		"Compose down": {
			commandLog: "-p gobfd-interop -f compose.yml down --volumes --remove-orphans\n",
			want:       "-p gobfd-interop -f compose.yml down --volumes --remove-orphans",
		},
		"container removal": {
			commandLog: "podman rm -f -- owned-container\n",
			want:       "podman rm -f -- owned-container",
		},
		"network removal": {
			commandLog: "podman network rm -- owned-network\n",
			want:       "podman network rm -- owned-network",
		},
		"volume removal": {
			commandLog: "podman volume rm -- owned-volume\n",
			want:       "podman volume rm -- owned-volume",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := forbiddenProjectMutation(test.commandLog); got != test.want {
				t.Fatalf("forbiddenProjectMutation() = %q, want %q", got, test.want)
			}
		})
	}
}

func forbiddenProjectMutation(commandLog string) string {
	for line := range strings.Lines(commandLog) {
		command := strings.TrimSpace(line)
		for _, forbidden := range []string{
			"-p ",
			"podman rm ",
			"podman network rm ",
			"podman volume rm ",
		} {
			if strings.HasPrefix(command, forbidden) {
				return command
			}
		}
	}
	return ""
}

func TestSecondComposeUpCommands(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		commandLog string
		want       []string
	}{
		"Holo phase only": {
			commandLog: "-f compose.yml up -d holo holo-config\n",
		},
		"different service after Holo": {
			commandLog: "-f compose.yml up -d holo holo-config\n" +
				"-f compose.yml up -d frr\n",
			want: []string{"-f compose.yml up -d frr"},
		},
		"repeated Holo phase": {
			commandLog: "-f compose.yml up -d holo holo-config\n" +
				"-f compose.yml up -d holo holo-config\n",
			want: []string{"-f compose.yml up -d holo holo-config"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := secondComposeUpCommands(test.commandLog)
			if strings.Join(got, "\n") != strings.Join(test.want, "\n") {
				t.Fatalf("second Compose up commands = %q, want %q", got, test.want)
			}
		})
	}
}

func secureRuntimeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("secure runtime directory: %v", err)
	}
	return dir
}

func projectControlCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"run", "./test/cmd/interopctl"}, args...)
	cmd := exec.CommandContext(ctx, "go", commandArgs...)
	cmd.Dir = root
	return cmd
}

func installFakeCommand(t *testing.T, directory, name, mode string) string {
	t.Helper()
	if strings.ContainsRune(name, filepath.Separator) {
		t.Fatalf("invalid fake executable name %q", name)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	if err := os.Symlink(executable, filepath.Join(directory, name)); err != nil {
		t.Fatalf("install fake %s: %v", name, err)
	}
	return interopFakeMode + "=" + mode
}

func runInteropFakeCommand(mode, command string, args []string) int {
	joined := strings.Join(args, " ")
	switch mode {
	case "benchmark-compose":
		if len(args) != 4 || args[0] != "compose" || args[1] != "-f" || args[3] != "config" {
			return 1
		}
		if !filepath.IsAbs(args[2]) {
			return 41
		}
		return fakeAppend("BENCH_COMMAND_LOG", "", joined)
	case "dev-ensure":
		if code := fakeAppend("PODMAN_COMMAND_LOG", "", joined); code != 0 {
			return code
		}
		switch {
		case joined == "compose -p dev-contract -f deployments/compose/compose.dev.yml ps --all --quiet dev":
			fmt.Fprintln(os.Stdout, "immutable-dev-id")
		case args[0] == "inspect" && strings.Contains(joined, "Mounts"):
			fmt.Fprintln(os.Stdout, os.Getenv("EXPECTED_ROOT"))
		case joined == "compose -p dev-contract -f deployments/compose/compose.dev.yml up -d --no-build dev":
		default:
			return 97
		}
		return 0
	case "holo-semantic":
		if command == "docker-compose" || len(args) != 0 && args[0] == "compose" {
			if len(args) != 0 && args[0] == "compose" {
				args = args[1:]
				joined = strings.Join(args, " ")
			}
			if code := fakeAppend("INTEROP_FAKE_COMMAND_LOG", "", joined); code != 0 {
				return code
			}
			validPrefix := len(args) >= 4 &&
				args[0] == "-p" && args[1] == "gobfd-interop" &&
				args[2] == "-f" && filepath.IsAbs(args[3])
			if validPrefix && slices.Equal(args[4:], []string{"up", "-d", "holo", "holo-config"}) {
				return fakeStateMarker("started")
			}
			if validPrefix && slices.Equal(args[4:], []string{
				"up", "-d", "--no-deps", "gobfd", "frr", "bird3", "tshark", "thoro",
			}) {
				return fakeStateMarker("phase2")
			}
			if len(args) > 4 && args[4] == "up" {
				return 9
			}
			return 0
		}
		return runHoloPodmanFake(args, joined)
	case "make-marker":
		return fakeWrite("INTEROP_FAKE_MAKE_MARKER", []byte("called\n"))
	case "arbitrary-resource":
		if code := fakeAppend("INTEROP_FAKE_PODMAN_LOG", "", joined); code != 0 {
			return code
		}
		switch joined {
		case "ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}":
			if os.Getenv("INTEROP_FAKE_RESOURCE_KIND") == "container" {
				fmt.Fprintln(os.Stdout, "arbitrary-container-id")
			}
		case "network ls --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}":
			if os.Getenv("INTEROP_FAKE_RESOURCE_KIND") == "network" {
				fmt.Fprintln(os.Stdout, "arbitrary-network-id")
			}
		case "volume ls --filter label=com.docker.compose.project=gobfd-interop --format {{.Name}}":
			if os.Getenv("INTEROP_FAKE_RESOURCE_KIND") == "volume" {
				fmt.Fprintln(os.Stdout, "arbitrary-volume-name")
			}
		default:
			if len(args) >= 2 && args[0] == "container" && args[1] == "exists" {
				return 1
			}
		}
		return 0
	case "mandatory-containers":
		return runMandatoryContainersFake(args, joined)
	case "missing-mandatory":
		return runMissingMandatoryFake(args, joined)
	case "owned-container":
		if code := fakeAppend("INTEROP_FAKE_PODMAN_LOG", "", joined); code != 0 {
			return code
		}
		if fakeLabelInspect(args, joined) {
			if args[len(args)-1] == "foreign-interop" {
				fmt.Fprintln(os.Stdout, "foreign-id|foreign-project")
			} else {
				fmt.Fprintln(os.Stdout, "immutable-owned-id|gobfd-interop")
			}
		}
		return 0
	case "holo-stop":
		return runHoloStopFake(args, joined)
	case "tshark-cancel":
		if code := fakeAppend("TSHARK_QUERY_COMMAND_LOG", "", joined); code != 0 {
			return code
		}
		if len(args) != 0 && args[0] == "inspect" && args[len(args)-1] == "tshark-interop" {
			fmt.Fprintln(os.Stdout, "immutable-tshark-id|gobfd-interop")
			return 0
		}
		if joined == "exec immutable-tshark-id tshark -r /captures/bfd.pcapng" {
			if code := fakeWrite("TSHARK_QUERY_STARTED", nil); code != 0 {
				return code
			}
			return fakeBlock()
		}
		return 9
	case "tshark-streams":
		if code := fakeAppend("INTEROP_FAKE_PODMAN_LOG", "", joined); code != 0 {
			return code
		}
		if fakeLabelInspect(args, joined) {
			fmt.Fprintln(os.Stdout, "immutable-tshark-id|gobfd-interop")
			return 0
		}
		tsharkBaseArgs := []string{
			"exec", "immutable-tshark-id", "tshark", "-r", "/captures/bfd.pcapng",
		}
		successArgs := append(slices.Clone(tsharkBaseArgs),
			"-Y", "bfd", "-T", "fields",
			"-e", "frame.number", "-e", "bfd.min_rx",
			"-E", "separator=\t", "-E", "header=n",
		)
		failureArgs := append(slices.Clone(tsharkBaseArgs), "-Y", "bfd")
		if slices.Equal(args, successArgs) || slices.Equal(args, failureArgs) {
			fmt.Fprintln(os.Stderr, `Running as user "root" and group "root". This could be dangerous.`)
			if os.Getenv("INTEROP_FAKE_TSHARK_FAIL") == "true" {
				fmt.Fprintln(os.Stderr, "capture read failed")
				return 17
			}
			fmt.Fprint(os.Stdout, "41\t300000\n42\t300000\n")
			return 0
		}
		return 9
	default:
		fmt.Fprintf(os.Stderr, "unknown interop fake mode %q\n", mode)
		return 125
	}
}

func runHoloPodmanFake(args []string, joined string) int {
	if code := fakeAppend("INTEROP_FAKE_COMMAND_LOG", "podman ", joined); code != 0 {
		return code
	}
	stateDir := os.Getenv("INTEROP_FAKE_STATE_DIR")
	label := "label=com.docker.compose.project=gobfd-interop"
	switch joined {
	case "ps -a --no-trunc --filter " + label + " --format {{.ID}}":
		if fakeExists(filepath.Join(stateDir, "started")) {
			if !fakeExists(filepath.Join(stateDir, "holo-removed")) {
				fmt.Fprintln(os.Stdout, "immutable-holo-id")
			}
			if !fakeExists(filepath.Join(stateDir, "loader-removed")) {
				fmt.Fprintln(os.Stdout, "immutable-holo-config-id")
			}
		}
	case "network ls --no-trunc --filter " + label + " --format {{.ID}}",
		"volume ls --filter " + label + " --format {{.Name}}":
	case "container exists immutable-holo-id":
		if fakeExists(filepath.Join(stateDir, "holo-removed")) {
			return 1
		}
	case "container exists immutable-holo-config-id":
		if fakeExists(filepath.Join(stateDir, "loader-removed")) {
			return 1
		}
	case "rm -f -- immutable-holo-id":
		return fakeStateMarker("holo-removed")
	case "rm -f -- immutable-holo-config-id":
		return fakeStateMarker("loader-removed")
	case "wait immutable-holo-config-id", "inspect --format {{.State.ExitCode}} immutable-holo-config-id":
		fmt.Fprintln(os.Stdout, "0")
	case "logs immutable-holo-config-id":
		fmt.Fprint(os.Stdout, os.Getenv("INTEROP_FAKE_LOADER_LOG"))
	case "exec immutable-holo-id holo-cli --version":
		fmt.Fprintln(os.Stdout, "Holo command-line interface 0.5.0")
	case holoSemanticPodmanArgs:
		fmt.Fprintln(os.Stdout, os.Getenv("INTEROP_FAKE_SEMANTIC_CONFIG"))
	default:
		if len(args) != 0 && joined == "inspect --type container --format {{json .}} "+args[len(args)-1] {
			fmt.Fprintf(os.Stdout,
				`{"Id":%q,"Config":{"Labels":{"com.docker.compose.project":"gobfd-interop"}},`+
					`"Mounts":[{"Type":"bind"}]}`+"\n",
				args[len(args)-1],
			)
			return 0
		}
		if len(args) >= 2 && args[0] == "container" && args[1] == "exists" {
			if fakeExists(filepath.Join(stateDir, "started")) {
				return 0
			}
			return 1
		}
		if fakeLabelInspect(args, joined) {
			name := strings.TrimSuffix(args[len(args)-1], "-interop")
			fmt.Fprintf(os.Stdout, "immutable-%s-id|gobfd-interop\n", name)
			return 0
		}
		return 9
	}
	return 0
}

func runMandatoryContainersFake(args []string, joined string) int {
	if joined == "ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}" {
		fmt.Fprintln(os.Stdout, "immutable-gobfd-id")
		return 0
	}
	if len(args) != 0 && (args[0] == "network" || args[0] == "volume") {
		return 0
	}
	if len(args) >= 3 && args[0] == "container" && args[1] == "exists" {
		if args[2] == "scapy-interop" {
			return 1
		}
		return 0
	}
	if fakeLabelInspect(args, joined) {
		name := strings.TrimSuffix(args[len(args)-1], "-interop")
		fmt.Fprintf(os.Stdout, "immutable-%s-id|gobfd-interop\n", name)
		return 0
	}
	return 9
}

func runMissingMandatoryFake(args []string, joined string) int {
	project := os.Getenv("INTEROP_PROJECT_NAME")
	if len(args) != 0 && args[0] == "ps" && strings.Contains(joined, "label=com.docker.compose.project="+project) {
		fmt.Fprintln(os.Stdout, "arbitrary-project-container-id")
		return 0
	}
	if len(args) != 0 && (args[0] == "network" || args[0] == "volume") {
		return 0
	}
	if len(args) >= 3 && args[0] == "container" && args[1] == "exists" {
		if args[2] == os.Getenv("INTEROP_FAKE_MISSING_CONTAINER") {
			return 1
		}
		return 0
	}
	if fakeLabelInspect(args, joined) {
		fmt.Fprintf(os.Stdout, "immutable-%s-id|%s\n", args[len(args)-1], project)
		return 0
	}
	return 9
}

func runHoloStopFake(args []string, joined string) int {
	if code := fakeAppend("INTEROP_FAKE_PODMAN_LOG", "", joined); code != 0 {
		return code
	}
	if fakeLabelInspect(args, joined) {
		fmt.Fprintln(os.Stdout, "immutable-holo-id|gobfd-interop")
		return 0
	}
	if joined != "stop --time 5 immutable-holo-id" {
		return 9
	}
	switch os.Getenv("INTEROP_FAKE_STOP_MODE") {
	case "", "success":
		fmt.Fprintln(os.Stdout, "immutable-holo-id")
		return 0
	case "failure":
		fmt.Fprintln(os.Stderr, "daemon refused bounded stop")
		return 17
	case "block":
		if code := fakeWrite("INTEROP_FAKE_STOP_STARTED", nil); code != 0 {
			return code
		}
		return fakeBlock()
	default:
		return 9
	}
}

func fakeLabelInspect(args []string, joined string) bool {
	return len(args) != 0 && args[0] == "inspect" && strings.Contains(joined, "index .Config.Labels")
}

func fakeAppend(environmentName, prefix, line string) int {
	path := os.Getenv(environmentName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	_, err = fmt.Fprintln(file, prefix+line)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	return 0
}

func fakeWrite(environmentName string, contents []byte) int {
	if err := os.WriteFile(os.Getenv(environmentName), contents, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	return 0
}

func fakeStateMarker(name string) int {
	if err := os.WriteFile(filepath.Join(os.Getenv("INTEROP_FAKE_STATE_DIR"), name), nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	return 0
}

func fakeExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fakeBlock() int {
	for {
		time.Sleep(time.Hour)
	}
}

func secondComposeUpCommands(commandLog string) []string {
	holoPhaseSeen := false
	var commands []string
	for line := range strings.Lines(commandLog) {
		fields := strings.Fields(line)
		upIndex := -1
		for i, field := range fields {
			if field == "up" {
				upIndex = i
				break
			}
		}
		if upIndex == -1 {
			continue
		}
		if !holoPhaseSeen && strings.Join(fields[upIndex:], " ") == "up -d holo holo-config" {
			holoPhaseSeen = true
			continue
		}
		if holoPhaseSeen {
			commands = append(commands, strings.TrimSpace(line))
		}
	}
	return commands
}
