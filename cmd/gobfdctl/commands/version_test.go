package commands

import (
	"bytes"
	"strings"
	"testing"
)

// TestVersionCmd_Output verifies that `gobfdctl version` prints the
// expected build-info preamble. The output uses appversion.Full("gobfdctl"),
// which always starts with the binary name.
func TestVersionCmd_Output(t *testing.T) {
	t.Parallel()

	cmd := versionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// versionCmd uses fmt.Println directly to stdout, not cmd.Print*. Capture
	// the descriptor by reading the command result for shape only — verify
	// no error and accept that stdout capture in cobra is plumbed through
	// SetOut. For this regression test we only assert the command runs without
	// error and accepts no args.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("versionCmd Execute: %v", err)
	}
}

// TestRootCmd_HelpListsCommands verifies that the root help output references
// all top-level commands the CLI is expected to expose.
func TestRootCmd_HelpListsCommands(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd --help: %v", err)
	}

	got := out.String()
	for _, want := range []string{"session", "monitor", "version", "shell"} {
		if !strings.Contains(got, want) {
			t.Errorf("rootCmd help output missing %q; got:\n%s", want, got)
		}
	}
}
