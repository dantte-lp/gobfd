package clabbootstrap

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMakeClabLauncherRecoversInterruptedCheckpointAndRetries(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "empty", data: ""},
		{name: "matching_prefix", data: launcherTestID[:12]},
		{name: "complete_id_without_newline", data: launcherTestID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newLauncherTestFixture(t)
			seedLauncherTestIntent(t, root, launcherTestOwner, launcherTestNonce)
			checkpoint := launcherCheckpointPath(root)
			if err := os.WriteFile(checkpoint, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			state := readLauncherTestState(t, root)
			state.FailRemove = true
			writeLauncherTestState(t, root, state)
			output, code := runLauncherTestMake(t, root, "interop-clab-down")
			if code == 0 {
				t.Fatalf("failed exact-ID removal unexpectedly succeeded: %s", output)
			}
			state = readLauncherTestState(t, root)
			assertLauncherCheckpointRemovalAttempt(t, state)
			assertLauncherTestIntentRetained(t, root)
			checkpointData, err := os.ReadFile(checkpoint)
			if err != nil {
				t.Fatalf("failed removal lost its checkpoint: %v", err)
			}
			if string(checkpointData) != launcherTestID+"\n" {
				t.Fatalf("failed removal retained incomplete checkpoint %q", checkpointData)
			}
			state.FailRemove, state.Calls = false, nil
			writeLauncherTestState(t, root, state)
			output, code = runLauncherTestMake(t, root, "interop-clab-down")
			if code != 0 {
				t.Fatalf("interrupted checkpoint recovery retry failed: exit=%d\n%s", code, output)
			}
			state = readLauncherTestState(t, root)
			assertLauncherCheckpointFreshController(t, root, state)
			assertLauncherTestCompiler(t, state)
			assertLauncherTestClean(t, root, state)
		})
	}
}

func launcherCheckpointPath(root string) string {
	return filepath.Join(runtimeDirectory(root), "controller-build-"+launcherTestNonce, "compiler.cid")
}

func assertLauncherCheckpointRemovalAttempt(t *testing.T, state launcherTestState) {
	t.Helper()
	removed := false
	for _, call := range state.Calls {
		if call.Executable != "podman" || len(call.Arguments) == 0 {
			t.Fatalf("checkpoint recovery escaped the fixed Podman boundary: %+v", call)
		}
		switch call.Arguments[0] {
		case "rm":
			if call.Arguments[len(call.Arguments)-1] != launcherTestID {
				t.Fatalf("checkpoint recovery removed an inexact or foreign reference: %+v", call)
			}
			removed = true
		case "create", "run", "start", "compose":
			t.Fatalf("new work preceded recovery of the interrupted checkpoint: %+v", call)
		}
	}
	if !removed || len(state.Containers) != 1 || len(launcherControllerCalls(state)) != 0 {
		t.Fatal("interrupted checkpoint prevented an exact-ID recovery attempt or lost retained state")
	}
}

func assertLauncherCheckpointFreshController(t *testing.T, root string, state launcherTestState) {
	t.Helper()
	controllers := launcherControllerCalls(state)
	if len(controllers) != 1 || !slices.Equal(controllers[0].Arguments, []string{"--down"}) {
		t.Fatalf("recovery did not run exactly one fresh --down controller: %+v", controllers)
	}
	oldDirectory := filepath.Dir(launcherCheckpointPath(root))
	if filepath.Dir(controllers[0].Path) == oldDirectory {
		t.Fatal("recovery reused the interrupted compiler's output directory")
	}
	recovered, created := -1, -1
	for index, call := range state.Calls {
		if call.Executable != "podman" || len(call.Arguments) == 0 {
			continue
		}
		if call.Arguments[0] == "rm" && call.Arguments[len(call.Arguments)-1] == launcherTestID {
			recovered = index
		}
		if created < 0 && (call.Arguments[0] == "create" || call.Arguments[0] == "run") {
			created = index
		}
	}
	if recovered < 0 || created <= recovered {
		t.Fatal("fresh compiler creation preceded exact-ID recovery of the interrupted checkpoint")
	}
}

func TestMakeClabLauncherRejectsUntrustedCheckpointBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    string
		mode    os.FileMode
		owner   string
		runID   string
		symlink bool
	}{
		{name: "mismatched_prefix", data: "eeeeeeeeeeee", mode: 0o600},
		{name: "nonhex_prefix", data: "not-an-id", mode: 0o600},
		{name: "unsafe_permissions", data: launcherTestID + "\n", mode: 0o644},
		{name: "symlink", data: launcherTestID + "\n", mode: 0o600, symlink: true},
		{name: "foreign_owner", data: launcherTestID[:12], mode: 0o600, owner: "foreign"},
		{name: "foreign_run", data: launcherTestID, mode: 0o600, runID: "foreign"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newLauncherTestFixture(t)
			owner, runID := launcherTestOwner, launcherTestNonce
			if tc.owner != "" {
				owner = tc.owner
			}
			if tc.runID != "" {
				runID = tc.runID
			}
			seedLauncherTestIntent(t, root, owner, runID)
			checkpoint := launcherCheckpointPath(root)
			written := checkpoint
			if tc.symlink {
				written = filepath.Join(root, "foreign-checkpoint")
				if err := os.Symlink(written, checkpoint); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(written, []byte(tc.data), tc.mode); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			output, code := runLauncherTestMake(t, root, "interop-clab-down")
			if code == 0 {
				t.Fatalf("untrusted checkpoint was accepted: %s", output)
			}
			state := readLauncherTestState(t, root)
			assertLauncherCheckpointNoMutation(t, state)
			assertLauncherTestIntentRetained(t, root)
			after, err := os.Lstat(checkpoint)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("rejected checkpoint was replaced or changed: %v", err)
			}
			data, err := os.ReadFile(written)
			if err != nil || string(data) != tc.data {
				t.Fatalf("rejected checkpoint or foreign symlink target changed: %v", err)
			}
		})
	}
}

func assertLauncherCheckpointNoMutation(t *testing.T, state launcherTestState) {
	t.Helper()
	for _, call := range state.Calls {
		if call.Executable != "podman" || len(call.Arguments) == 0 {
			t.Fatalf("rejected checkpoint escaped the fixed Podman boundary: %+v", call)
		}
		if slices.Contains([]string{"rm", "create", "run", "start", "compose"}, call.Arguments[0]) {
			t.Fatalf("rejected checkpoint allowed engine mutation: %+v", call)
		}
		if strings.Contains(strings.Join(call.Arguments, " "), "foreign-checkpoint") {
			t.Fatalf("checkpoint supplied a foreign Podman reference: %+v", call)
		}
	}
	if len(state.Containers) != 1 || len(launcherControllerCalls(state)) != 0 {
		t.Fatal("rejected checkpoint changed container ownership or executed a controller")
	}
}
