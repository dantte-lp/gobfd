package clabbootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDryRunTopologyRejectsInvalidFRRBeforeCommands(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		topology string
		wantIs   error
	}{
		{name: "missing topology", wantIs: os.ErrNotExist},
		{name: "malformed topology", topology: "topology: ["},
		{
			name: "unpinned image", wantIs: errInvalidBootstrapOptions,
			topology: "topology:\n  nodes:\n    frr:\n      image: localhost/gobfd-frr:unqualified\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if test.topology != "" {
				directory := filepath.Join(root, "test", "interop-clab")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(directory, "gobfd-vendors.clab.yml")
				if err := os.WriteFile(path, []byte(test.topology), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			options := DefaultOptions(root)
			options.DryRun, options.Deploy = true, true
			runner := &recordingRunner{}
			err := dryRunTopology(t.Context(), options, runner, dryRunImage)
			if err == nil || (test.wantIs != nil && !errors.Is(err, test.wantIs)) {
				t.Fatalf("invalid topology error = %v, want rejection preserving %v", err, test.wantIs)
			}
			if len(runner.commands) != 0 {
				t.Fatalf("invalid topology issued %d commands before rejection", len(runner.commands))
			}
		})
	}
}
