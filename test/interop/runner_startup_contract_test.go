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
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fakeBin := t.TempDir()
			commandLog := filepath.Join(t.TempDir(), "commands.log")
			composeFake := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
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
case "${1:-}" in
    ps)
        printf '%s\n' gobfd-interop frr-interop bird3-interop holo-interop thoro-interop tshark-interop
        ;;
    wait)
        printf '%s\n' "${INTEROP_FAKE_WAIT_STATUS:-}"
        exit "${INTEROP_FAKE_WAIT_EXIT:-0}"
        ;;
    inspect)
        if [[ "${2:-}" == "--format" ]]; then
            printf '%s\n' "${INTEROP_FAKE_INSPECT_STATUS:-}"
            exit "${INTEROP_FAKE_INSPECT_EXIT:-0}"
        fi
        ;;
esac
exit 0
`
			sleepFake := "#!/usr/bin/env bash\nexit 0\n"
			for name, contents := range map[string]string{
				"podman-compose": composeFake,
				"podman":         podmanFake,
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
			)
			output, runErr := cmd.CombinedOutput()
			commands, readErr := os.ReadFile(commandLog)
			if readErr != nil {
				t.Fatalf("read fake command log: %v", readErr)
			}
			assertProjectPreflight(t, string(commands))
			if test.collisionClass != "" {
				assertNoComposeMutation(t, string(commands))
			} else {
				assertHoloStartupSequence(t, root, string(commands), test.wantInspect)
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

func assertHoloStartupSequence(t *testing.T, root, commandLog string, wantInspect bool) {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(commandLog), "\n")
	composePrefix := "-p gobfd-interop -f " + filepath.Join(root, "test", "interop", "compose.yml") + " "
	want := []string{
		"podman ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
		"podman network ls --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
		"podman volume ls --filter label=com.docker.compose.project=gobfd-interop --format {{.Name}}",
		composePrefix + "build --no-cache",
		composePrefix + "up -d holo holo-config",
		"podman wait holo-config-interop",
	}
	if wantInspect {
		want = append(want, "podman inspect --format {{.State.ExitCode}} holo-config-interop")
	}
	if len(lines) < len(want) {
		t.Fatalf("runner logged %d prerequisite commands, want at least %d; commands:\n%s", len(lines), len(want), commandLog)
	}
	for i, command := range want {
		if lines[i] != command {
			t.Fatalf("runner prerequisite command %d = %q, want %q", i+1, lines[i], command)
		}
	}
}

func assertProjectPreflight(t *testing.T, commandLog string) {
	t.Helper()

	want := []string{
		"podman ps -a --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
		"podman network ls --no-trunc --filter label=com.docker.compose.project=gobfd-interop --format {{.ID}}",
		"podman volume ls --filter label=com.docker.compose.project=gobfd-interop --format {{.Name}}",
	}
	lines := strings.Split(strings.TrimSpace(commandLog), "\n")
	if len(lines) < len(want) {
		t.Fatalf("runner logged %d preflight commands, want at least %d; commands:\n%s", len(lines), len(want), commandLog)
	}
	for i, command := range want {
		if lines[i] != command {
			t.Fatalf("runner preflight command %d = %q, want %q", i+1, lines[i], command)
		}
	}
}

func assertNoComposeMutation(t *testing.T, commandLog string) {
	t.Helper()

	for line := range strings.Lines(commandLog) {
		if strings.HasPrefix(line, "-p ") {
			t.Fatalf("runner invoked Compose after detecting a project collision: %q", strings.TrimSpace(line))
		}
	}
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
