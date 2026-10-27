package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
)

func TestSocketQoSReloadRequiresRestart(t *testing.T) {
	t.Parallel()
	startup := config.DefaultConfig()
	contract := newStartupRuntimeContract(startup)
	for _, test := range []struct {
		field string
		value string
	}{
		{"ipv4_dscp", "46"},
		{"ipv6_traffic_class", "184"},
		{"priority", "6"},
		{"mark", "123"},
		{"priority_required", "false"},
		{"mark_required", "false"},
	} {
		t.Run(test.field, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "qos.yml")
			contents := "socket:\n  " + test.field + ": " + test.value + "\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			candidate, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range contract.changedFields(candidate) {
				if field.String() == "socket."+test.field {
					return
				}
			}
			t.Fatalf("changed socket.%s is accepted as reloadable", test.field)
		})
	}
}
