package clabbootstrap

import (
	"slices"
	"testing"
)

func TestVendorSuiteRequiresIsolatedPrecompiledExecution(t *testing.T) {
	t.Parallel()

	options := DefaultOptions(t.TempDir())
	options.DryRun = true
	runner := &recordingRunner{}
	if err := runVendorTests(t.Context(), options, runner); err != nil {
		t.Fatal(err)
	}
	compiled := false
	for _, command := range runner.commands {
		if command.Executable == executablePodman && slices.Contains(command.Arguments, "compose") {
			t.Fatalf("vendor execution uses the engine-enabled Compose development compiler: %v", command.Arguments)
		}
		index := slices.Index(command.Arguments, "go")
		if index < 0 || len(command.Arguments) <= index+1 || command.Arguments[index+1] != "test" {
			continue
		}
		if command.Executable != executablePodman || !slices.Contains(command.Arguments[index+2:], "-c") ||
			!slices.Contains(command.Arguments[index+2:], "-race") ||
			!slices.Contains(command.Arguments[:index], "CGO_ENABLED=1") {
			t.Fatalf("vendor suite must compile its race-enabled payload in a separate container: %v", command)
		}
		compiled = true
	}
	if !compiled {
		t.Fatal("vendor execution did not compile a prebuilt test payload with go test -c")
	}
}
