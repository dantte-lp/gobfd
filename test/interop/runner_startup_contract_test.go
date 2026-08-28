package interop_test

import (
	"context"
	"fmt"
	"os"
	"os/exec" //nolint:depguard // Contract test executes the repository runner with isolated fake Podman commands.
	"path/filepath"
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

func TestProjectControlHoloSemanticGate(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	tests := map[string]struct {
		loaderLog    string
		wantSecondUp bool
		want         string
	}{
		"valid exact configuration": {wantSecondUp: true},
		"partial invalid loader log": {
			loaderLog: "% failed to parse one startup line",
			want:      "Holo configuration loader reported parser or commit errors",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fakeBin := t.TempDir()
			stateDir := t.TempDir()
			commandLog := filepath.Join(t.TempDir(), "commands.log")
			composeFake := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
if [[ "$*" == *" up -d holo holo-config" ]]; then
    : > "${INTEROP_FAKE_STATE_DIR}/started"
fi
if [[ "$*" == *" up -d --no-deps gobfd frr bird3 tshark thoro" ]]; then
    : > "${INTEROP_FAKE_STATE_DIR}/phase2"
fi
`
			podmanFake := `#!/usr/bin/env bash
if [[ "${1:-}" == "compose" ]]; then
    shift
    exec docker-compose "$@"
fi
printf 'podman %s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
label="label=com.docker.compose.project=gobfd-interop"
case "$*" in
    "ps -a --no-trunc --filter ${label} --format {{.ID}}")
        if [[ -f "${INTEROP_FAKE_STATE_DIR}/started" ]]; then
            [[ -f "${INTEROP_FAKE_STATE_DIR}/holo-removed" ]] || printf '%s\n' immutable-holo-id
            [[ -f "${INTEROP_FAKE_STATE_DIR}/loader-removed" ]] || printf '%s\n' immutable-holo-config-id
        fi
        ;;
    "network ls --no-trunc --filter ${label} --format {{.ID}}"|"volume ls --filter ${label} --format {{.Name}}") ;;
    "container exists immutable-holo-id")
        [[ -f "${INTEROP_FAKE_STATE_DIR}/holo-removed" ]] && exit 1
        ;;
    "container exists immutable-holo-config-id")
        [[ -f "${INTEROP_FAKE_STATE_DIR}/loader-removed" ]] && exit 1
        ;;
    "rm -f -- immutable-holo-id") : > "${INTEROP_FAKE_STATE_DIR}/holo-removed" ;;
    "rm -f -- immutable-holo-config-id") : > "${INTEROP_FAKE_STATE_DIR}/loader-removed" ;;
    "wait immutable-holo-config-id") printf '%s\n' 0 ;;
    "inspect --format {{.State.ExitCode}} immutable-holo-config-id") printf '%s\n' 0 ;;
    "logs immutable-holo-config-id") printf '%s' "${INTEROP_FAKE_LOADER_LOG:-}" ;;
    "exec immutable-holo-id holo-cli --version") printf '%s\n' 'Holo command-line interface 0.5.0' ;;
    "${semantic_command}")
        printf '%s\n' "${INTEROP_FAKE_SEMANTIC_CONFIG}"
        ;;
    *)
        if [[ "${1:-}" == container && "${2:-}" == exists ]]; then
            [[ -f "${INTEROP_FAKE_STATE_DIR}/started" ]] && exit 0
            exit 1
        fi
        if [[ "${1:-}" == inspect && "$*" == *"index .Config.Labels"* ]]; then
            container_name="${@: -1}"
            printf 'immutable-%s-id|gobfd-interop\n' "${container_name%-interop}"
            exit 0
        fi
        exit 9
        ;;
esac
`
			podmanFake = strings.Replace(
				podmanFake,
				"label=\"label=com.docker.compose.project=gobfd-interop\"",
				"label=\"label=com.docker.compose.project=gobfd-interop\"\n"+
					"semantic_command=\""+holoSemanticPodmanArgs+"\"",
				1,
			)
			for command, contents := range map[string]string{
				"podman":         podmanFake,
				"docker-compose": composeFake,
			} {
				if writeErr := writeExecutable(filepath.Join(fakeBin, command), contents); writeErr != nil {
					t.Fatalf("write fake %s: %v", command, writeErr)
				}
			}
			cmd := projectControlCommand(t.Context(), root, "up")
			cmd.Env = append(os.Environ(),
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
	composeFake := "#!/usr/bin/env bash\nprintf '%s\\n' called > \"${INTEROP_FAKE_MAKE_MARKER}\"\n"
	if err := writeExecutable(filepath.Join(fakeBin, "podman"), composeFake); err != nil {
		t.Fatalf("write fake podman compose: %v", err)
	}
	injectionMarker := filepath.Join(t.TempDir(), "injected")
	projectName := "safe; printf injected > " + injectionMarker + "; #"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "interop-up", "INTEROP_PROJECT_NAME="+projectName)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "INTEROP_FAKE_MAKE_MARKER="+commandMarker)
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
	composeFake := "#!/usr/bin/env bash\nprintf '%s\\n' called > \"${INTEROP_FAKE_MAKE_MARKER}\"\n"
	if err := writeExecutable(filepath.Join(fakeBin, "podman"), composeFake); err != nil {
		t.Fatalf("write fake podman compose: %v", err)
	}
	shellMarker := filepath.Join(t.TempDir(), "make-shell-expanded")
	projectName := "$(info MAKE-INFO-EXPANDED)$(shell printf expanded > " + shellMarker + ")safe"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "interop-up", "INTEROP_PROJECT_NAME="+projectName)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "INTEROP_FAKE_MAKE_MARKER="+commandMarker)
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
			fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
if [[ "$*" == "ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}" ]]; then
    [[ "${INTEROP_FAKE_RESOURCE_KIND}" == container ]] && printf '%s\n' arbitrary-container-id
    exit 0
fi
if [[ "$*" == "network ls --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}" ]]; then
    [[ "${INTEROP_FAKE_RESOURCE_KIND}" == network ]] && printf '%s\n' arbitrary-network-id
    exit 0
fi
if [[ "$*" == "volume ls --filter label=com.docker.compose.project=gobfd-interop --format {{.Name}}" ]]; then
    [[ "${INTEROP_FAKE_RESOURCE_KIND}" == volume ]] && printf '%s\n' arbitrary-volume-name
    exit 0
fi
if [[ "${1:-}" == "container" && "${2:-}" == "exists" ]]; then
    exit 1
fi
exit 0
`
			if writeErr := writeExecutable(filepath.Join(fakeBin, "podman"), fakePodman); writeErr != nil {
				t.Fatalf("write fake podman: %v", writeErr)
			}
			cmd := projectControlCommand(
				t.Context(), root,
				"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
			)
			cmd.Env = append(os.Environ(),
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
	fakePodman := `#!/usr/bin/env bash
if [[ "$*" == "ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}" ]]; then
    printf '%s\n' immutable-gobfd-id
    exit 0
fi
if [[ "${1:-}" == "network" || "${1:-}" == "volume" ]]; then
    exit 0
fi
if [[ "${1:-}" == "container" && "${2:-}" == "exists" ]]; then
    [[ "${3:-}" == "scapy-interop" ]] && exit 1
    exit 0
fi
if [[ "${1:-}" == "inspect" && "$*" == *"index .Config.Labels"* ]]; then
    name="${@: -1}"
    printf 'immutable-%s-id|gobfd-interop\n' "${name%-interop}"
    exit 0
fi
exit 9
`
	if writeErr := writeExecutable(filepath.Join(fakeBin, "podman"), fakePodman); writeErr != nil {
		t.Fatalf("write fake podman: %v", writeErr)
	}
	cmd := projectControlCommand(
		t.Context(), root,
		"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
	)
	cmd.Env = append(os.Environ(),
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
	fakePodman := `#!/usr/bin/env bash
if [[ "${1:-}" == "ps" && "$*" == *"label=com.docker.compose.project=${INTEROP_PROJECT_NAME}"* ]]; then
    printf '%s\n' arbitrary-project-container-id
    exit 0
fi
if [[ "${1:-}" == "network" || "${1:-}" == "volume" ]]; then
    exit 0
fi
if [[ "${1:-}" == "container" && "${2:-}" == "exists" ]]; then
    [[ "${3:-}" == "${INTEROP_FAKE_MISSING_CONTAINER}" ]] && exit 1
    exit 0
fi
if [[ "${1:-}" == "inspect" && "$*" == *"index .Config.Labels"* ]]; then
    name="${@: -1}"
    printf 'immutable-%s-id|%s\n' "${name}" "${INTEROP_PROJECT_NAME}"
    exit 0
fi
exit 9
`
	for _, testCase := range testCases {
		for _, missingContainer := range testCase.containers {
			t.Run(testCase.kind+"/"+missingContainer, func(t *testing.T) {
				fakeBin := t.TempDir()
				marker := filepath.Join(t.TempDir(), "command-ran")
				if writeErr := writeExecutable(filepath.Join(fakeBin, "podman"), fakePodman); writeErr != nil {
					t.Fatalf("write fake podman: %v", writeErr)
				}
				cmd := projectControlCommand(
					t.Context(), root,
					"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
				)
				cmd.Env = append(os.Environ(),
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

func writeExecutable(path, contents string) error {
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		return fmt.Errorf("write executable %s: %w", path, err)
	}
	return nil
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
