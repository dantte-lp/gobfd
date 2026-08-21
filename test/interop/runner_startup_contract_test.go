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
		collisionClass string
		foreignName    string
		foreignCleanup string
		lockFailure    bool
		wantDiagnostic string
		wantInspect    bool
		wantSecondUp   bool
	}{
		"zero success": {
			waitStatus:    "0",
			inspectStatus: "0",
			wantInspect:   true,
			wantSecondUp:  true,
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
			foreignCleanup: "scapy-interop",
			wantInspect:    true,
			wantSecondUp:   true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fakeBin := t.TempDir()
			commandLog := filepath.Join(t.TempDir(), "commands.log")
			stateDir := t.TempDir()
			composeFake := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
if [[ "$*" == *" up -d holo holo-config" ]]; then
    : > "${INTEROP_FAKE_STATE_DIR}/started"
fi
if [[ "$*" == *" up -d --no-deps gobfd frr bird3 tshark thoro" ]]; then
    : > "${INTEROP_FAKE_STATE_DIR}/phase2"
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
		        prefix="owned-${name}|"
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
			cmd := exec.CommandContext(ctx, "bash", runner)
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+":"+os.Getenv("PATH"),
				"INTEROP_FAKE_COMMAND_LOG="+commandLog,
				"INTEROP_FAKE_WAIT_STATUS="+test.waitStatus,
				"INTEROP_FAKE_INSPECT_STATUS="+test.inspectStatus,
				"INTEROP_FAKE_WAIT_EXIT="+test.waitExit,
				"INTEROP_FAKE_INSPECT_EXIT="+test.inspectExit,
				"INTEROP_FAKE_COLLISION_CLASS="+test.collisionClass,
				"INTEROP_FAKE_FOREIGN_NAME="+test.foreignName,
				"INTEROP_FAKE_FOREIGN_CLEANUP="+test.foreignCleanup,
				fmt.Sprintf("INTEROP_FAKE_LOCK_FAILURE=%t", test.lockFailure),
				"INTEROP_FAKE_STATE_DIR="+stateDir,
				"XDG_RUNTIME_DIR="+t.TempDir(),
			)
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
				assertHoloStartupSequence(t, root, string(commands), test.wantInspect)
			}
			if test.foreignCleanup != "" {
				assertForeignCleanupIsLabelOnly(t, string(commands))
			}
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
				want := "-p gobfd-interop -f " + filepath.Join(root, "test", "interop", "compose.yml") +
					" up -d --no-deps gobfd frr bird3 tshark thoro"
				if len(secondUp) != 1 || secondUp[0] != want {
					t.Fatalf("runner second-phase commands = %q, want [%q]", secondUp, want)
				}
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

func TestInteropProjectLockSerializesRunners(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	guard := filepath.Join(root, "test", "interop", "project_guard.sh")
	runtimeDir := t.TempDir()
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
			runtimeDir := t.TempDir()
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

func assertHoloStartupSequence(t *testing.T, root, commandLog string, wantInspect bool) {
	t.Helper()

	composePrefix := "-p gobfd-interop -f " + filepath.Join(root, "test", "interop", "compose.yml") + " "
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
		"podman wait owned-holo-config-interop",
	}
	if wantInspect {
		want = append(want, "podman inspect --format {{.State.ExitCode}} owned-holo-config-interop")
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
