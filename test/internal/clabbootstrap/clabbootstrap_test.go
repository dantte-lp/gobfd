package clabbootstrap

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestDefaultOptionsPreserveVendorTopologyPins(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "repo")
	options := DefaultOptions(root)

	if options.ProjectRoot != root {
		t.Fatalf("project root = %q, want %q", options.ProjectRoot, root)
	}
	if options.Jobs != 3 {
		t.Fatalf("jobs = %d, want 3", options.Jobs)
	}
	wantTags := ImageTags{
		Nokia:  "25.10.2",
		Sonic:  "latest",
		VyOS:   "latest",
		Arista: "ceos:4.36.0.1F",
		Cisco:  "ios-xr/xrd-control-plane:25.4.1",
	}
	if !reflect.DeepEqual(options.Tags, wantTags) {
		t.Fatalf("image tags = %#v, want %#v", options.Tags, wantTags)
	}
}

func TestVendorCommandIsTheOnlyPythonBoundary(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "repo")
	tests := []struct {
		name      string
		operation VendorOperation
		arguments []string
		wantTail  []string
	}{
		{
			name:      "VyOS image",
			operation: VendorVyOS,
			arguments: []string{"--version", "latest", "--image", "docker.io/muruu1/vyos:latest"},
			wantTail:  []string{"vyos", "--version", "latest", "--image", "docker.io/muruu1/vyos:latest"},
		},
		{
			name:      "Arista archive",
			operation: VendorArista,
			arguments: []string{"--archive", "/images/ceos.tar", "--tag", "ceos:4.36.0.1F"},
			wantTail:  []string{"arista", "--archive", "/images/ceos.tar", "--tag", "ceos:4.36.0.1F"},
		},
		{
			name:      "Cisco archive",
			operation: VendorCisco,
			arguments: []string{"--archive", "/images/xrd.tar", "--tag", "ios-xr/xrd-control-plane:25.4.1"},
			wantTail:  []string{"cisco", "--archive", "/images/xrd.tar", "--tag", "ios-xr/xrd-control-plane:25.4.1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			command, err := VendorCommand(root, test.operation, test.arguments...)
			if err != nil {
				t.Fatalf("build vendor command: %v", err)
			}
			wantPrefix := []string{
				"run", "--frozen", "--no-default-groups", "--", "python",
				filepath.Join(root, "test", "interop-clab", "vendor_images.py"),
			}
			want := slices.Concat(wantPrefix, test.wantTail)
			if command.Executable != "uv" || !reflect.DeepEqual(command.Arguments, want) {
				t.Fatalf("vendor command = %q %q, want %q %q", command.Executable, command.Arguments, "uv", want)
			}
			if command.Directory != root {
				t.Fatalf("vendor command directory = %q, want %q", command.Directory, root)
			}
		})
	}
}

func TestVendorCommandRejectsOwnedOrUnknownOperations(t *testing.T) {
	t.Parallel()

	for _, operation := range []VendorOperation{"", "pull", "build", "inventory", "deploy"} {
		if _, err := VendorCommand("/repo", operation); err == nil {
			t.Errorf("vendor operation %q was accepted", operation)
		}
	}
}
