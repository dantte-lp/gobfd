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

const holoRunningConfigWithoutInterface = `{"ietf-routing:routing":{"control-plane-protocols":` +
	`{"control-plane-protocol":[` + holoProtocolConfig + `]}}}`

const holoRunningConfigDuplicateInterface = `{"ietf-interfaces:interfaces":{"interface":[` +
	holoInterfaceConfig + `,` + holoInterfaceConfig + `]},"ietf-routing:routing":` +
	`{"control-plane-protocols":{"control-plane-protocol":[` + holoProtocolConfig + `]}}}`

const holoRunningConfigDuplicateProtocol = `{"ietf-interfaces:interfaces":{"interface":[` +
	holoInterfaceConfig + `]},"ietf-routing:routing":{"control-plane-protocols":` +
	`{"control-plane-protocol":[` + holoProtocolConfig + `,` + holoProtocolConfig + `]}}}`

const holoProtocolDuplicateSessionConfig = `{"type":"ietf-bfd-types:bfdv1","name":"main",` +
	`"ietf-bfd:bfd":{"ietf-bfd-ip-sh:ip-sh":{"sessions":{"session":[` +
	holoSessionConfig + `,` + holoSessionConfig + `]}}}}`

const holoRunningConfigDuplicateSession = `{"ietf-interfaces:interfaces":{"interface":[` +
	holoInterfaceConfig + `]},"ietf-routing:routing":{"control-plane-protocols":` +
	`{"control-plane-protocol":[` + holoProtocolDuplicateSessionConfig + `]}}}`

const holoSemanticPodmanArgs = "exec immutable-holo-id holo-cli --no-colors --no-pager " +
	"--address http://127.0.0.1:50051 --command show running format json"

const holoSemanticCommandLog = "podman " + holoSemanticPodmanArgs

func TestInteropRunnerHoloConfigGate(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	tests := map[string]struct {
		waitStatus     string
		inspectStatus  string
		waitExit       string
		inspectExit    string
		semanticConfig string
		semanticExit   string
		loaderLog      string
		holoVersion    string
		collisionClass string
		foreignName    string
		foreignCleanup string
		ownershipSwap  bool
		lockFailure    bool
		wantDiagnostic string
		wantInspect    bool
		wantLoaderLog  bool
		wantVersion    bool
		wantSemantic   bool
		wantSecondUp   bool
		useOverride    bool
	}{
		"zero success": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: validHoloRunningConfig,
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
			wantSecondUp:   true,
		},
		"zero success with override": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: validHoloRunningConfig,
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
			wantSecondUp:   true,
			useOverride:    true,
		},
		"missing BFD running configuration": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: `{"ietf-routing:routing":{"control-plane-protocols":{"control-plane-protocol":[]}}}`,
			wantDiagnostic: "Holo running configuration is missing the required BFD session",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"missing interface running configuration": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: holoRunningConfigWithoutInterface,
			wantDiagnostic: "Holo running configuration is missing the required BFD session",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"duplicate interface running configuration": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: holoRunningConfigDuplicateInterface,
			wantDiagnostic: "Holo running configuration is missing the required BFD session",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"duplicate BFD protocol running configuration": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: holoRunningConfigDuplicateProtocol,
			wantDiagnostic: "Holo running configuration is missing the required BFD session",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"duplicate BFD session running configuration": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: holoRunningConfigDuplicateSession,
			wantDiagnostic: "Holo running configuration is missing the required BFD session",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"invalid running configuration JSON": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: "not-json",
			wantDiagnostic: "Holo running configuration is not valid JSON",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"running configuration probe failure": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticExit:   "17",
			wantDiagnostic: "failed to inspect Holo running configuration",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"partial invalid loader log": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: validHoloRunningConfig,
			loaderLog:      "% failed to parse one startup line",
			wantDiagnostic: "Holo configuration loader reported parser or commit errors",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"unexpected nonempty loader log": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: validHoloRunningConfig,
			loaderLog:      "configuration committed with warning",
			wantDiagnostic: "Holo configuration loader produced unexpected output",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
		},
		"unexpected Holo CLI version": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: validHoloRunningConfig,
			holoVersion:    "Holo command-line interface 0.6.0",
			wantDiagnostic: "unexpected Holo CLI version: Holo command-line interface 0.6.0",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantVersion:    true,
		},
		"non-zero status": {
			waitStatus:     "7",
			inspectStatus:  "7",
			wantDiagnostic: "holo-config exited with status 7",
			wantInspect:    true,
		},
		"invalid status": {
			waitStatus:     "invalid",
			inspectStatus:  "invalid",
			wantDiagnostic: "invalid holo-config wait status: invalid",
			wantInspect:    true,
		},
		"wait inspect mismatch": {
			waitStatus:     "0",
			inspectStatus:  "7",
			wantDiagnostic: "holo-config status mismatch: wait=0, inspect=7",
			wantInspect:    true,
		},
		"wait command failure": {
			waitExit:       "9",
			wantDiagnostic: "timed out or failed waiting for holo-config-interop",
		},
		"inspect command failure": {
			waitStatus:     "0",
			inspectStatus:  "0",
			inspectExit:    "9",
			wantDiagnostic: "failed to inspect holo-config-interop exit status",
			wantInspect:    true,
		},
		"container collision": {
			collisionClass: "container",
			wantDiagnostic: "Compose project gobfd-interop already owns resources",
		},
		"network collision": {
			collisionClass: "network",
			wantDiagnostic: "Compose project gobfd-interop already owns resources",
		},
		"volume collision": {
			collisionClass: "volume",
			wantDiagnostic: "Compose project gobfd-interop already owns resources",
		},
		"foreign fixed name collision": {
			foreignName:    "gobfd-interop",
			wantDiagnostic: "fixed container name gobfd-interop belongs to Compose project foreign-project",
		},
		"lock contention": {
			lockFailure:    true,
			wantDiagnostic: "Compose project gobfd-interop is locked by another runner",
		},
		"foreign fixed name before cleanup": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: validHoloRunningConfig,
			foreignCleanup: "scapy-interop",
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
			wantSecondUp:   true,
		},
		"ownership swap after successful fixed-name inspection": {
			waitStatus:     "0",
			inspectStatus:  "0",
			semanticConfig: validHoloRunningConfig,
			ownershipSwap:  true,
			wantInspect:    true,
			wantLoaderLog:  true,
			wantSemantic:   true,
			wantSecondUp:   true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fakeBin := t.TempDir()
			commandLog := filepath.Join(t.TempDir(), "commands.log")
			stateDir := t.TempDir()
			overrideFile := ""
			if test.useOverride {
				overrideFile = filepath.Join(t.TempDir(), "compose.override.yml")
				if err := os.WriteFile(overrideFile, []byte("services: {}\n"), 0o600); err != nil {
					t.Fatalf("write Compose override: %v", err)
				}
			}
			composeFake := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
if [[ "$*" == *" up -d holo holo-config" ]]; then
    : > "${INTEROP_FAKE_STATE_DIR}/started"
fi
if [[ "$*" == *" up -d --no-deps gobfd frr bird3 tshark thoro" ]]; then
    : > "${INTEROP_FAKE_STATE_DIR}/phase2"
fi
if [[ "$*" == *" down --volumes --remove-orphans" && "${INTEROP_FAKE_OWNERSHIP_SWAP:-}" == "true" ]]; then
    printf '%s\n' 'podman rm -f foreign-swapped-container-name' >> "${INTEROP_FAKE_COMMAND_LOG}"
fi
exit 0
`
			podmanFake := `#!/usr/bin/env bash
printf 'podman %s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
label="label=com.docker.compose.project=gobfd-interop"
if [[ "$*" == "ps -a --no-trunc --filter ${label} --format {{.ID}}" ]]; then
    [[ "${INTEROP_FAKE_COLLISION_CLASS:-}" == "container" ]] && printf '%s\n' owned-container
    exit 0
fi
if [[ "$*" == "network ls --no-trunc --filter ${label} --format {{.ID}}" ]]; then
    [[ "${INTEROP_FAKE_COLLISION_CLASS:-}" == "network" ]] && printf '%s\n' owned-network
    exit 0
fi
if [[ "$*" == "volume ls --filter ${label} --format {{.Name}}" ]]; then
    [[ "${INTEROP_FAKE_COLLISION_CLASS:-}" == "volume" ]] && printf '%s\n' owned-volume
    exit 0
fi
if [[ "${1:-}" == "container" && "${2:-}" == "exists" ]]; then
    name="${3:-}"
    if [[ "${INTEROP_FAKE_FOREIGN_NAME:-}" == "${name}" ]]; then
        exit 0
    fi
    if [[ -f "${INTEROP_FAKE_STATE_DIR}/phase2" && "${INTEROP_FAKE_FOREIGN_CLEANUP:-}" == "${name}" ]]; then
        exit 0
    fi
    if [[ -f "${INTEROP_FAKE_STATE_DIR}/started" ]]; then
        exit 0
    fi
    exit 1
fi
		case "${1:-}" in
    ps)
        printf '%s\n' gobfd-interop frr-interop bird3-interop holo-interop thoro-interop tshark-interop
        ;;
    wait)
        printf '%s\n' "${INTEROP_FAKE_WAIT_STATUS:-}"
        exit "${INTEROP_FAKE_WAIT_EXIT:-0}"
        ;;
		    inspect)
		if [[ "$*" == *"index .Config.Labels"* ]]; then
		    name="${@: -1}"
		    prefix=""
		    if [[ "$*" == *"{{.ID}}|"* ]]; then
		        prefix="immutable-${name%-interop}-id|"
		    fi
		    if [[ "${INTEROP_FAKE_FOREIGN_NAME:-}" == "${name}" ]] || \
		       { [[ -f "${INTEROP_FAKE_STATE_DIR}/phase2" ]] && \
		         [[ "${INTEROP_FAKE_FOREIGN_CLEANUP:-}" == "${name}" ]]; }; then
		        printf '%s%s\n' "${prefix}" foreign-project
		    else
		        printf '%s%s\n' "${prefix}" gobfd-interop
		    fi
		    exit 0
		fi
        if [[ "${2:-}" == "--format" ]]; then
            printf '%s\n' "${INTEROP_FAKE_INSPECT_STATUS:-}"
            exit "${INTEROP_FAKE_INSPECT_EXIT:-0}"
	        fi
	        ;;
	    exec)
	        if [[ "$*" == "exec immutable-holo-id holo-cli --version" ]]; then
	            printf '%s\n' "${INTEROP_FAKE_HOLO_VERSION:-}"
	            exit "${INTEROP_FAKE_VERSION_EXIT:-0}"
	        fi
	        semantic_command="exec immutable-holo-id holo-cli --no-colors --no-pager "
	        semantic_command+="--address http://127.0.0.1:50051 --command show running format json"
	        if [[ "$*" == "${semantic_command}" ]]; then
	            printf '%s\n' "${INTEROP_FAKE_SEMANTIC_CONFIG:-}"
	            exit "${INTEROP_FAKE_SEMANTIC_EXIT:-0}"
	        fi
	        ;;
	    logs)
	        if [[ "${2:-}" == "immutable-holo-config-id" ]]; then
	            printf '%s' "${INTEROP_FAKE_LOADER_LOG:-}"
	            exit 0
	        fi
	        ;;
	esac
exit 0
`
			flockFake := `#!/usr/bin/env bash
printf 'flock %s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
if [[ "${INTEROP_FAKE_LOCK_FAILURE:-}" == "true" ]]; then
    exit 1
fi
exec /usr/bin/flock "$@"
`
			sleepFake := "#!/usr/bin/env bash\nexit 0\n"
			for name, contents := range map[string]string{
				"podman-compose": composeFake,
				"podman":         podmanFake,
				"flock":          flockFake,
				"sleep":          sleepFake,
			} {
				if err := writeExecutable(filepath.Join(fakeBin, name), contents); err != nil {
					t.Fatalf("write fake command %s: %v", name, err)
				}
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			runner := filepath.Join(root, "test", "interop", "run.sh")
			holoVersion := test.holoVersion
			if holoVersion == "" {
				holoVersion = "Holo command-line interface 0.5.0"
			}
			cmd := exec.CommandContext(ctx, "bash", runner)
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+":"+os.Getenv("PATH"),
				"INTEROP_FAKE_COMMAND_LOG="+commandLog,
				"INTEROP_FAKE_WAIT_STATUS="+test.waitStatus,
				"INTEROP_FAKE_INSPECT_STATUS="+test.inspectStatus,
				"INTEROP_FAKE_WAIT_EXIT="+test.waitExit,
				"INTEROP_FAKE_INSPECT_EXIT="+test.inspectExit,
				"INTEROP_FAKE_SEMANTIC_CONFIG="+test.semanticConfig,
				"INTEROP_FAKE_SEMANTIC_EXIT="+test.semanticExit,
				"INTEROP_FAKE_LOADER_LOG="+test.loaderLog,
				"INTEROP_FAKE_HOLO_VERSION="+holoVersion,
				"INTEROP_FAKE_COLLISION_CLASS="+test.collisionClass,
				"INTEROP_FAKE_FOREIGN_NAME="+test.foreignName,
				"INTEROP_FAKE_FOREIGN_CLEANUP="+test.foreignCleanup,
				fmt.Sprintf("INTEROP_FAKE_OWNERSHIP_SWAP=%t", test.ownershipSwap),
				fmt.Sprintf("INTEROP_FAKE_LOCK_FAILURE=%t", test.lockFailure),
				"INTEROP_FAKE_STATE_DIR="+stateDir,
				"XDG_RUNTIME_DIR="+secureRuntimeDir(t),
			)
			if overrideFile != "" {
				cmd.Env = append(cmd.Env, "INTEROP_COMPOSE_OVERRIDE_FILE="+overrideFile)
			}
			output, runErr := cmd.CombinedOutput()
			commands, readErr := os.ReadFile(commandLog)
			if readErr != nil {
				t.Fatalf("read fake command log: %v", readErr)
			}
			if test.lockFailure {
				assertNoProjectMutation(t, string(commands))
			} else {
				assertProjectPreflight(t, string(commands))
			}
			if test.collisionClass != "" || test.foreignName != "" {
				assertNoProjectMutation(t, string(commands))
			} else if !test.lockFailure {
				assertHoloStartupSequence(
					t, root, overrideFile, string(commands), test.wantInspect, test.wantLoaderLog,
					test.wantVersion || test.wantSemantic, test.wantSemantic, test.wantSecondUp,
				)
			}
			if !test.wantSemantic && strings.Contains(string(commands), holoSemanticCommandLog) {
				t.Fatalf("runner probed Holo semantics before a successful loader wait/inspect gate; commands:\n%s", commands)
			}
			if test.foreignCleanup != "" {
				assertForeignCleanupIsLabelOnly(t, string(commands))
			}
			assertNoComposeCleanup(t, string(commands))
			assertNoNameBasedRuntimeMutation(t, string(commands))
			if test.wantDiagnostic != "" && runErr == nil {
				t.Fatalf("runner reported loader failure but exited successfully; output:\n%s", output)
			}
			if test.wantDiagnostic != "" && !strings.Contains(string(output), test.wantDiagnostic) {
				t.Fatalf("runner output is missing diagnostic %q; output:\n%s", test.wantDiagnostic, output)
			}
			secondUp := secondComposeUpCommands(string(commands))
			if !test.wantSecondUp && len(secondUp) != 0 {
				t.Fatalf("runner issued a second Compose up after Holo failure: %q", secondUp)
			}
			if test.wantSecondUp {
				want := "-p gobfd-interop -f " + filepath.Join(root, "test", "interop", "compose.yml")
				if overrideFile != "" {
					want += " -f " + overrideFile
				}
				want += " up -d --no-deps gobfd frr bird3 tshark thoro"
				if len(secondUp) != 1 || secondUp[0] != want {
					t.Fatalf("runner second-phase commands = %q, want [%q]", secondUp, want)
				}
			}
		})
	}
}

func TestInteropRunnerRejectsInvalidComposeOverride(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	target := filepath.Join(t.TempDir(), "compose.override.yml")
	if err := os.WriteFile(target, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	symlink := filepath.Join(t.TempDir(), "compose.override.yml")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatalf("create Compose override symlink: %v", err)
	}

	tests := map[string]struct {
		override string
		want     string
	}{
		"relative": {
			override: "compose.override.yml",
			want:     "must be absolute",
		},
		"missing": {
			override: filepath.Join(t.TempDir(), "missing.override.yml"),
			want:     "must be an existing regular readable file",
		},
		"symlink": {
			override: symlink,
			want:     "must not be a symlink",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fakeBin := t.TempDir()
			commandMarker := filepath.Join(t.TempDir(), "podman-command")
			fake := "#!/usr/bin/env bash\nprintf '%s\\n' \"$0 $*\" > \"${INTEROP_FAKE_COMMAND_MARKER}\"\nexit 1\n"
			for _, command := range []string{"podman", "podman-compose"} {
				if err := writeExecutable(filepath.Join(fakeBin, command), fake); err != nil {
					t.Fatalf("write fake %s: %v", command, err)
				}
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "test", "interop", "run.sh"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+":"+os.Getenv("PATH"),
				"INTEROP_COMPOSE_OVERRIDE_FILE="+test.override,
				"INTEROP_FAKE_COMMAND_MARKER="+commandMarker,
				"XDG_RUNTIME_DIR="+secureRuntimeDir(t),
			)
			output, runErr := cmd.CombinedOutput()
			if runErr == nil {
				t.Fatalf("runner accepted invalid Compose override %q; output:\n%s", test.override, output)
			}
			if !strings.Contains(string(output), test.want) {
				t.Fatalf("runner output is missing %q; output:\n%s", test.want, output)
			}
			if _, statErr := os.Stat(commandMarker); !os.IsNotExist(statErr) {
				t.Fatalf("invalid Compose override reached Podman or Compose: %v", statErr)
			}
		})
	}
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
				"podman-compose": composeFake,
			} {
				if writeErr := writeExecutable(filepath.Join(fakeBin, command), contents); writeErr != nil {
					t.Fatalf("write fake %s: %v", command, writeErr)
				}
			}
			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "test", "interop", "projectctl.sh"), "up")
			cmd.Dir = root
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

func TestGoplsCheckRejectsEmptyDiscovery(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	tests := map[string]struct {
		goCommand string
		want      string
	}{
		"no packages": {
			goCommand: "exit 0",
			want:      "gopls-check: no packages discovered",
		},
		"no Go inputs": {
			goCommand: `
if [[ "$*" == "list -f {{.ImportPath}} ./..." ]]; then
    printf '%s\n' example.invalid/empty
fi
exit 0`,
			want: "gopls-check: no Go inputs discovered",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fakeBin := t.TempDir()
			goplsMarker := filepath.Join(t.TempDir(), "gopls-called")
			if err := writeExecutable(filepath.Join(fakeBin, "go"), "#!/usr/bin/env bash\n"+test.goCommand+"\n"); err != nil {
				t.Fatalf("write fake go: %v", err)
			}
			goplsFake := "#!/usr/bin/env bash\nprintf called > \"${GOPLS_FAKE_MARKER}\"\n"
			if err := writeExecutable(filepath.Join(fakeBin, "gopls"), goplsFake); err != nil {
				t.Fatalf("write fake gopls: %v", err)
			}

			cmd := exec.CommandContext(t.Context(), "sh", filepath.Join(root, "scripts", "gopls-check.sh"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+":"+os.Getenv("PATH"),
				"GOPLS_FAKE_MARKER="+goplsMarker,
			)
			output, runErr := cmd.CombinedOutput()
			if runErr == nil {
				t.Fatalf("gopls gate accepted empty discovery; output:\n%s", output)
			}
			if !strings.Contains(string(output), test.want) {
				t.Fatalf("gopls gate output is missing %q; output:\n%s", test.want, output)
			}
			if _, statErr := os.Stat(goplsMarker); !os.IsNotExist(statErr) {
				t.Fatalf("gopls ran after empty discovery: %v", statErr)
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
	commandMarker := filepath.Join(t.TempDir(), "podman-compose-called")
	composeFake := "#!/usr/bin/env bash\nprintf '%s\\n' called > \"${INTEROP_FAKE_MAKE_MARKER}\"\n"
	if err := writeExecutable(filepath.Join(fakeBin, "podman-compose"), composeFake); err != nil {
		t.Fatalf("write fake podman-compose: %v", err)
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
		t.Fatalf("invalid project name reached podman-compose: %v", err)
	}
}

func TestMakeDoesNotExpandNestedProjectFunctions(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	commandMarker := filepath.Join(t.TempDir(), "podman-compose-called")
	composeFake := "#!/usr/bin/env bash\nprintf '%s\\n' called > \"${INTEROP_FAKE_MAKE_MARKER}\"\n"
	if err := writeExecutable(filepath.Join(fakeBin, "podman-compose"), composeFake); err != nil {
		t.Fatalf("write fake podman-compose: %v", err)
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
		t.Fatalf("nested-function project name reached podman-compose: %v", err)
	}
}

func TestInteropProjectLockRejectsUnsafeFallbackBase(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	unsafeBase := filepath.Join(t.TempDir(), "unsafe-fallback")
	if err := os.Mkdir(unsafeBase, 0o777); err != nil {
		t.Fatalf("create unsafe fallback base: %v", err)
	}
	if err := os.Chmod(unsafeBase, 0o777); err != nil {
		t.Fatalf("set unsafe fallback mode: %v", err)
	}
	script := `set -euo pipefail
source "$1"
interop_validate_fallback_lock_base "$2"
`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "fallback-check",
		filepath.Join(root, "test", "interop", "project_guard.sh"), unsafeBase)
	output, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("accepted group/world-writable non-sticky fallback base; output:\n%s", output)
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
			cmd := exec.CommandContext(
				t.Context(),
				filepath.Join(root, "test", "interop", "projectctl.sh"),
				"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
			)
			cmd.Dir = root
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
	cmd := exec.CommandContext(
		t.Context(),
		filepath.Join(root, "test", "interop", "projectctl.sh"),
		"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
	)
	cmd.Dir = root
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
				cmd := exec.CommandContext(
					t.Context(),
					filepath.Join(root, "test", "interop", "projectctl.sh"),
					"lock-run", "--", "bash", "-c", `printf ran > "$1"`, "lock-run-command", marker,
				)
				cmd.Dir = root
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

func TestInteropProjectLockRejectsUnsafePreferredBase(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	unsafeBase := filepath.Join(t.TempDir(), "unsafe-preferred")
	if err := os.Mkdir(unsafeBase, 0o700); err != nil {
		t.Fatalf("create preferred base: %v", err)
	}
	if err := os.Chmod(unsafeBase, 0o777); err != nil {
		t.Fatalf("set unsafe preferred mode: %v", err)
	}
	script := `set -euo pipefail
source "$1"
interop_validate_preferred_lock_base "$2"
`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "preferred-check",
		filepath.Join(root, "test", "interop", "project_guard.sh"), unsafeBase)
	if output, runErr := cmd.CombinedOutput(); runErr == nil {
		t.Fatalf("accepted group/world-writable preferred lock base; output:\n%s", output)
	}
}

func TestLabelledContainerCleanupUsesExactSnapshotID(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	stateDir := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
if [[ "$*" == "ps -a --no-trunc --filter label=io.gobfd.e2e.merge-owner=run-123 --format {{.ID}}" ]]; then
    [[ -f "${INTEROP_FAKE_STATE_DIR}/removed" ]] || printf '%s\n' immutable-merge-container-id
    exit 0
fi
if [[ "$*" == "rm -f -- immutable-merge-container-id" ]]; then
    : > "${INTEROP_FAKE_STATE_DIR}/removed"
    exit 0
fi
if [[ "$*" == "container exists immutable-merge-container-id" ]]; then
    [[ -f "${INTEROP_FAKE_STATE_DIR}/removed" ]] && exit 1
    exit 0
fi
exit 9
`
	if writeErr := writeExecutable(filepath.Join(fakeBin, "podman"), fakePodman); writeErr != nil {
		t.Fatalf("write fake podman: %v", writeErr)
	}
	script := `set -euo pipefail
source "$1"
interop_remove_labelled_containers io.gobfd.e2e.merge-owner run-123
interop_verify_labelled_containers_absent io.gobfd.e2e.merge-owner run-123
`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "merge-cleanup",
		filepath.Join(root, "test", "interop", "project_guard.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"INTEROP_FAKE_PODMAN_LOG="+commandLog,
		"INTEROP_FAKE_STATE_DIR="+stateDir,
	)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("cleanup merge-labelled container: %v; output:\n%s", runErr, output)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman log: %v", err)
	}
	if !strings.Contains(string(commands), "rm -f -- immutable-merge-container-id") {
		t.Fatalf("merge cleanup did not remove immutable snapshot ID; commands:\n%s", commands)
	}
}

func TestEmptyContainerSnapshotCleanupSucceeds(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	script := `set -euo pipefail
source "$1"
interop_remove_container_snapshot
`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "empty-snapshot-cleanup",
		filepath.Join(root, "test", "interop", "project_guard.sh"))
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("empty exact container snapshot was not a successful no-op: %v; output:\n%s", runErr, output)
	}
}

func TestProjectResourceCleanupRetriesExactSnapshotDependencies(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	stateDir := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
label="label=com.docker.compose.project=gobfd-interop"
	case "$*" in
    "ps -a --no-trunc --filter ${label} --format {{.ID}}")
        [[ -f "${INTEROP_FAKE_STATE_DIR}/parent-removed" ]] || printf '%s\n' immutable-parent-id
        [[ -f "${INTEROP_FAKE_STATE_DIR}/child-removed" ]] || printf '%s\n' immutable-child-id
        ;;
    "network ls --no-trunc --filter ${label} --format {{.ID}}")
        [[ -f "${INTEROP_FAKE_STATE_DIR}/network-removed" ]] || printf '%s\n' immutable-network-id
        ;;
	    "volume ls --filter ${label} --format {{.Name}}")
	        ;;
    "rm -f -- immutable-parent-id")
        if [[ ! -f "${INTEROP_FAKE_STATE_DIR}/child-removed" ]]; then
            exit 17
        fi
        : > "${INTEROP_FAKE_STATE_DIR}/parent-removed"
        ;;
    "rm -f -- immutable-child-id")
        : > "${INTEROP_FAKE_STATE_DIR}/child-removed"
        exit 17
        ;;
    "container exists immutable-parent-id")
        [[ -f "${INTEROP_FAKE_STATE_DIR}/parent-removed" ]] && exit 1
        ;;
    "container exists immutable-child-id")
        [[ -f "${INTEROP_FAKE_STATE_DIR}/child-removed" ]] && exit 1
        ;;
    "network rm -- immutable-network-id")
        : > "${INTEROP_FAKE_STATE_DIR}/network-removed"
        ;;
	    *) exit 9 ;;
esac
exit 0
`
	if writeErr := writeExecutable(filepath.Join(fakeBin, "podman"), fakePodman); writeErr != nil {
		t.Fatalf("write fake podman: %v", writeErr)
	}
	script := `set -euo pipefail
source "$1"
interop_cleanup_project_resources gobfd-interop
`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "project-cleanup",
		filepath.Join(root, "test", "interop", "project_guard.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"INTEROP_FAKE_PODMAN_LOG="+commandLog,
		"INTEROP_FAKE_STATE_DIR="+stateDir,
	)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("cleanup dependent exact snapshots: %v; output:\n%s", runErr, output)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman log: %v", err)
	}
	assertCommandSubsequence(t, string(commands), []string{
		"rm -f -- immutable-parent-id",
		"container exists immutable-parent-id",
		"rm -f -- immutable-child-id",
		"container exists immutable-child-id",
		"rm -f -- immutable-parent-id",
		"container exists immutable-parent-id",
		"network rm -- immutable-network-id",
	})
	if strings.Contains(string(commands), "rm -f -- newly-appearing-id") {
		t.Fatalf("cleanup mutated an ID outside its initial immutable snapshot; commands:\n%s", commands)
	}
}

func TestProjectResourceCleanupRejectsLabelledVolumesBeforeMutation(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
label="label=com.docker.compose.project=gobfd-interop"
case "$*" in
    "ps -a --no-trunc --filter ${label} --format {{.ID}}") printf '%s\n' immutable-container-id ;;
    "network ls --no-trunc --filter ${label} --format {{.ID}}") printf '%s\n' immutable-network-id ;;
    "volume ls --filter ${label} --format {{.Name}}") printf '%s\n' mutable-volume-name ;;
    *) exit 9 ;;
esac
`
	if writeErr := writeExecutable(filepath.Join(fakeBin, "podman"), fakePodman); writeErr != nil {
		t.Fatalf("write fake podman: %v", writeErr)
	}
	script := `set -euo pipefail
source "$1"
interop_remove_project_resources gobfd-interop
`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "project-cleanup-volume",
		filepath.Join(root, "test", "interop", "project_guard.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"INTEROP_FAKE_PODMAN_LOG="+commandLog,
	)
	output, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("cleanup accepted a mutable labelled volume; output:\n%s", output)
	}
	if !strings.Contains(string(output), "guarded interop projects must use container storage or bind mounts") {
		t.Fatalf("cleanup output is missing labelled-volume diagnostic; output:\n%s", output)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman log: %v", err)
	}
	for _, mutation := range []string{"rm -f --", "network rm --", "volume rm --"} {
		if strings.Contains(string(commands), mutation) {
			t.Fatalf("cleanup mutated resources after finding a mutable labelled volume: %q\ncommands:\n%s", mutation, commands)
		}
	}
}

func TestProjectResourceCleanupNeverMutatesNewlyAppearingID(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	stateDir := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
label="label=com.docker.compose.project=gobfd-interop"
case "$*" in
    "ps -a --no-trunc --filter ${label} --format {{.ID}}")
        if [[ -f "${INTEROP_FAKE_STATE_DIR}/initial-removed" ]]; then
            printf '%s\n' newly-appearing-id
        else
            printf '%s\n' immutable-initial-id
        fi
        ;;
    "network ls --no-trunc --filter ${label} --format {{.ID}}"|"volume ls --filter ${label} --format {{.Name}}") ;;
    "rm -f -- immutable-initial-id") : > "${INTEROP_FAKE_STATE_DIR}/initial-removed" ;;
    "container exists immutable-initial-id") exit 1 ;;
    *) exit 9 ;;
esac
`
	if writeErr := writeExecutable(filepath.Join(fakeBin, "podman"), fakePodman); writeErr != nil {
		t.Fatalf("write fake podman: %v", writeErr)
	}
	script := `set -euo pipefail
source "$1"
interop_remove_project_resources gobfd-interop
`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "project-cleanup-new-id",
		filepath.Join(root, "test", "interop", "project_guard.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"INTEROP_FAKE_PODMAN_LOG="+commandLog,
		"INTEROP_FAKE_STATE_DIR="+stateDir,
	)
	output, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("cleanup accepted a newly appearing exact-labelled ID; output:\n%s", output)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman log: %v", err)
	}
	if strings.Contains(string(commands), "rm -f -- newly-appearing-id") {
		t.Fatalf("cleanup mutated an ID outside its initial immutable snapshot; commands:\n%s", commands)
	}
}

func TestProjectResourceCleanupFailsOnNoProgress(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fakeBin := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "podman.log")
	fakePodman := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_PODMAN_LOG}"
label="label=com.docker.compose.project=gobfd-interop"
case "$*" in
    "ps -a --no-trunc --filter ${label} --format {{.ID}}") printf '%s\n' immutable-parent-id immutable-child-id ;;
    "network ls --no-trunc --filter ${label} --format {{.ID}}") printf '%s\n' immutable-network-id ;;
    "volume ls --filter ${label} --format {{.Name}}") ;;
    "rm -f -- immutable-parent-id"|"rm -f -- immutable-child-id") exit 17 ;;
    "container exists immutable-parent-id"|"container exists immutable-child-id") exit 0 ;;
    *) exit 9 ;;
esac
`
	if writeErr := writeExecutable(filepath.Join(fakeBin, "podman"), fakePodman); writeErr != nil {
		t.Fatalf("write fake podman: %v", writeErr)
	}
	script := `set -euo pipefail
source "$1"
interop_remove_project_resources gobfd-interop
`
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script, "project-cleanup-no-progress",
		filepath.Join(root, "test", "interop", "project_guard.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"INTEROP_FAKE_PODMAN_LOG="+commandLog,
	)
	output, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("cleanup accepted a pass with no exact-ID progress; output:\n%s", output)
	}
	if ctx.Err() != nil {
		t.Fatalf("cleanup did not terminate after a bounded no-progress pass: %v", ctx.Err())
	}
	if !strings.Contains(string(output), "no progress removing exact container snapshot") {
		t.Fatalf("cleanup output is missing no-progress diagnostic; output:\n%s", output)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read fake podman log: %v", err)
	}
	if strings.Contains(string(commands), "network rm ") || strings.Contains(string(commands), "volume rm ") {
		t.Fatalf("cleanup mutated networks or volumes before removing all exact container IDs; commands:\n%s", commands)
	}
}

func TestInteropProjectLockSerializesRunners(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	guard := filepath.Join(root, "test", "interop", "project_guard.sh")
	runtimeDir := secureRuntimeDir(t)
	ready := filepath.Join(t.TempDir(), "holder-ready")
	holderScript := `set -euo pipefail
source "$1"
interop_acquire_project_lock gobfd-interop
: > "$2"
read -r _ || true
`
	holder := exec.CommandContext(t.Context(), "bash", "-c", holderScript, "lock-holder", guard, ready)
	holder.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	holderInput, err := holder.StdinPipe()
	if err != nil {
		t.Fatalf("open lock holder stdin: %v", err)
	}
	if err := holder.Start(); err != nil {
		t.Fatalf("start lock holder: %v", err)
	}
	t.Cleanup(func() {
		_ = holderInput.Close()
		_ = holder.Wait()
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect lock holder readiness: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("lock holder did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	contenderScript := `set -euo pipefail
source "$1"
interop_acquire_project_lock gobfd-interop
`
	contender := exec.CommandContext(t.Context(), "bash", "-c", contenderScript, "lock-contender", guard)
	contender.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	output, runErr := contender.CombinedOutput()
	if runErr == nil {
		t.Fatalf("contending runner acquired a held project lock; output:\n%s", output)
	}
	if !strings.Contains(string(output), "Compose project gobfd-interop is locked by another runner") {
		t.Fatalf("contending runner output is missing lock diagnostic; output:\n%s", output)
	}

	if err := holderInput.Close(); err != nil {
		t.Fatalf("release lock holder: %v", err)
	}
	if err := holder.Wait(); err != nil {
		t.Fatalf("wait for lock holder: %v", err)
	}
	lockFile := filepath.Join(runtimeDir, fmt.Sprintf("gobfd-interop-%d.locks", os.Getuid()), "gobfd-interop.lock")
	if _, err := os.Stat(lockFile); err != nil {
		t.Fatalf("persistent project lock file is missing after release: %v", err)
	}
	retry := exec.CommandContext(t.Context(), "bash", "-c", contenderScript, "lock-retry", guard)
	retry.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	if output, err := retry.CombinedOutput(); err != nil {
		t.Fatalf("runner could not reacquire persistent project lock: %v; output:\n%s", err, output)
	}
}

func TestInteropProjectLockRejectsUnsafeDirectory(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	guard := filepath.Join(root, "test", "interop", "project_guard.sh")
	tests := map[string]func(t *testing.T, path string){
		"wrong mode": func(t *testing.T, path string) {
			t.Helper()
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("create unsafe lock directory: %v", err)
			}
		},
		"symlink": func(t *testing.T, path string) {
			t.Helper()
			if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatalf("create unsafe lock directory symlink: %v", err)
			}
		},
	}
	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runtimeDir := secureRuntimeDir(t)
			lockDir := filepath.Join(runtimeDir, fmt.Sprintf("gobfd-interop-%d.locks", os.Getuid()))
			prepare(t, lockDir)
			script := `set -euo pipefail
source "$1"
interop_acquire_project_lock gobfd-interop
`
			cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "unsafe-lock", guard)
			cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
			output, runErr := cmd.CombinedOutput()
			if runErr == nil {
				t.Fatalf("runner accepted unsafe lock directory; output:\n%s", output)
			}
			if !strings.Contains(string(output), "unsafe interop lock directory") {
				t.Fatalf("runner output is missing unsafe lock diagnostic; output:\n%s", output)
			}
		})
	}
}

func assertHoloStartupSequence(
	t *testing.T,
	root, overrideFile, commandLog string,
	wantInspect, wantLoaderLog, wantVersion, wantSemantic, wantSecondUp bool,
) {
	t.Helper()

	composePrefix := "-p gobfd-interop -f " + filepath.Join(root, "test", "interop", "compose.yml")
	if overrideFile != "" {
		composePrefix += " -f " + overrideFile
	}
	composePrefix += " "
	want := []string{
		"flock -n ",
		"podman ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
		"podman network ls --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
		"podman volume ls --filter label=com.docker.compose.project=gobfd-interop --format {{.Name}}",
		"podman container exists gobfd-interop",
		"podman container exists frr-interop",
		"podman container exists bird3-interop",
		"podman container exists tshark-interop",
		"podman container exists holo-interop",
		"podman container exists holo-config-interop",
		"podman container exists thoro-interop",
		"podman container exists scapy-interop",
		composePrefix + "build --no-cache",
		composePrefix + "up -d holo holo-config",
		"podman container exists holo-config-interop",
		"podman inspect --type container --format {{.ID}}|" +
			"{{ index .Config.Labels \"com.docker.compose.project\" }} holo-config-interop",
		"podman wait immutable-holo-config-id",
	}
	if wantInspect {
		want = append(want, "podman inspect --format {{.State.ExitCode}} immutable-holo-config-id")
	}
	if wantLoaderLog {
		want = append(want, "podman logs immutable-holo-config-id")
	}
	if wantVersion {
		want = append(want,
			"podman container exists holo-interop",
			"podman inspect --type container --format {{.ID}}|"+
				"{{ index .Config.Labels \"com.docker.compose.project\" }} holo-interop",
			"podman exec immutable-holo-id holo-cli --version",
		)
	}
	if wantSemantic {
		want = append(want,
			holoSemanticCommandLog,
		)
	}
	if wantSecondUp {
		want = append(want, composePrefix+"up -d --no-deps gobfd frr bird3 tshark thoro")
	}
	assertCommandSubsequence(t, commandLog, want)
}

func assertProjectPreflight(t *testing.T, commandLog string) {
	t.Helper()

	want := []string{
		"flock -n ",
		"podman ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
		"podman network ls --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
		"podman volume ls --filter label=com.docker.compose.project=gobfd-interop --format {{.Name}}",
	}
	assertCommandSubsequence(t, commandLog, want)
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

func assertNoProjectMutation(t *testing.T, commandLog string) {
	t.Helper()

	if command := forbiddenProjectMutation(commandLog); command != "" {
		t.Fatalf("runner mutated resources after detecting a project collision: %q", command)
	}
}

func assertForeignCleanupIsLabelOnly(t *testing.T, commandLog string) {
	t.Helper()

	for line := range strings.Lines(commandLog) {
		command := strings.TrimSpace(line)
		if strings.HasPrefix(command, "-p ") && strings.Contains(command, " down ") {
			t.Fatalf("runner invoked Compose down after a foreign fixed name appeared: %q", command)
		}
		for _, forbidden := range []string{"podman rm ", "podman network rm ", "podman volume rm "} {
			if strings.HasPrefix(command, forbidden) {
				t.Fatalf("runner removed a resource outside exact-label fallback proof: %q", command)
			}
		}
	}
	query := "podman ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}"
	if strings.Count(commandLog, query) < 2 {
		t.Fatalf("runner did not re-query exact project labels during fallback cleanup; commands:\n%s", commandLog)
	}
}

func assertNoComposeCleanup(t *testing.T, commandLog string) {
	t.Helper()

	for line := range strings.Lines(commandLog) {
		command := strings.TrimSpace(line)
		if strings.HasPrefix(command, "-p ") && strings.Contains(command, " down ") {
			t.Fatalf("runner invoked name-based Compose cleanup: %q", command)
		}
	}
}

func assertNoNameBasedRuntimeMutation(t *testing.T, commandLog string) {
	t.Helper()

	for line := range strings.Lines(commandLog) {
		command := strings.TrimSpace(line)
		for _, action := range []string{"podman rm ", "podman stop ", "podman start "} {
			if strings.HasPrefix(command, action) && strings.Contains(command, "-interop") {
				t.Fatalf("runner used a mutable container name for runtime mutation: %q", command)
			}
		}
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
