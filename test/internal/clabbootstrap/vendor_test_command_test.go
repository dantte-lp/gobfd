package clabbootstrap

import (
	"slices"
	"testing"
)

func TestVendorTestsEnableRaceWithCGO(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{}
	if err := runVendorTests(t.Context(), DefaultOptions(t.TempDir()), runner); err != nil {
		t.Fatal(err)
	}
	for _, command := range runner.commands {
		index := slices.Index(command.Arguments, "go")
		if index < 0 || len(command.Arguments) <= index+1 || command.Arguments[index+1] != "test" {
			continue
		}
		if !slices.Contains(command.Arguments[:index], "CGO_ENABLED=1") ||
			!slices.Contains(command.Arguments[index+2:], "-race") {
			t.Fatalf("vendor suite does not force CGO and race detection: %v", command.Arguments)
		}
		return
	}
	t.Fatal("vendor suite did not invoke go test")
}
