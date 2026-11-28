package clabbootstrap

import (
	"path/filepath"
	"reflect"
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
