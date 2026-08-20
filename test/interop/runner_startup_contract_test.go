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

func TestInteropRunnerRejectsUnsuccessfulHoloConfig(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, loaderStatus := range []string{"7", "invalid"} {
		t.Run(loaderStatus, func(t *testing.T) {
			t.Parallel()

			fakeBin := t.TempDir()
			commandLog := filepath.Join(t.TempDir(), "commands.log")
			composeFake := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
exit 0
`
			podmanFake := `#!/usr/bin/env bash
printf 'podman %s\n' "$*" >> "${INTEROP_FAKE_COMMAND_LOG}"
case "${1:-}" in
    wait|inspect) printf '%s\n' "${INTEROP_FAKE_LOADER_STATUS}" ;;
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
				"INTEROP_FAKE_LOADER_STATUS="+loaderStatus,
			)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("runner accepted holo-config exit status %q; output:\n%s", loaderStatus, output)
			}
			commands, readErr := os.ReadFile(commandLog)
			if readErr != nil {
				t.Fatalf("read fake command log: %v", readErr)
			}
			if runnerStartedGoBFD(string(commands)) {
				t.Fatalf(
					"runner started gobfd before rejecting holo-config exit status %q; commands:\n%s",
					loaderStatus,
					commands,
				)
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

func runnerStartedGoBFD(commandLog string) bool {
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

		hasExplicitService := false
		for _, field := range fields[upIndex+1:] {
			if strings.HasPrefix(field, "-") {
				continue
			}
			hasExplicitService = true
			if field == "gobfd" {
				return true
			}
		}
		if !hasExplicitService {
			return true
		}
	}
	return false
}
