package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func TestCapabilityFormatsPreserveStatusDimensions(t *testing.T) {
	t.Parallel()

	capabilities := []*bfdv1.CapabilityStatus{
		{
			Kind: bfdv1.CapabilityKind_CAPABILITY_KIND_UNSOLICITED, Rfc: 9468,
			Maturity:   bfdv1.CapabilityMaturity_CAPABILITY_MATURITY_PREVIEW,
			Enablement: bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_DISABLED,
			Readiness:  bfdv1.CapabilityReadiness_CAPABILITY_READINESS_NOT_READY,
		},
		{
			Kind: bfdv1.CapabilityKind_CAPABILITY_KIND_ECHO, Rfc: 9747,
			Maturity:   bfdv1.CapabilityMaturity_CAPABILITY_MATURITY_PREVIEW,
			Enablement: bfdv1.CapabilityEnablement_CAPABILITY_ENABLEMENT_ENABLED,
			Readiness:  bfdv1.CapabilityReadiness_CAPABILITY_READINESS_READY,
		},
		{Kind: bfdv1.CapabilityKind_CAPABILITY_KIND_MICRO_BFD, Rfc: 7130},
		{Kind: bfdv1.CapabilityKind_CAPABILITY_KIND_VXLAN, Rfc: 8971},
		{Kind: bfdv1.CapabilityKind_CAPABILITY_KIND_GENEVE, Rfc: 9521},
	}
	wantRows := []map[string]any{
		{
			"kind": "unsolicited", "rfc": float64(9468), "maturity": "preview",
			"enablement": "disabled", "readiness": "not_ready",
		},
		{"kind": "echo", "rfc": float64(9747), "maturity": "preview", "enablement": "enabled", "readiness": "ready"},
		{"kind": "micro_bfd", "rfc": float64(7130), "maturity": "unknown", "enablement": "unknown", "readiness": "unknown"},
		{"kind": "vxlan", "rfc": float64(8971), "maturity": "unknown", "enablement": "unknown", "readiness": "unknown"},
		{"kind": "geneve", "rfc": float64(9521), "maturity": "unknown", "enablement": "unknown", "readiness": "unknown"},
	}
	for _, format := range []string{formatJSON, formatYAML, formatTable} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			out, err := formatCapabilities(capabilities, format)
			if err != nil {
				t.Fatal(err)
			}
			if format == formatTable {
				lines := strings.Split(strings.TrimSpace(out), "\n")
				if len(lines) != 6 {
					t.Fatalf("table has %d lines, want header and five server rows: %s", len(lines), out)
				}
				if got := strings.Join(strings.Fields(lines[0]), " "); got != "CAPABILITY RFC MATURITY ENABLEMENT READINESS" {
					t.Errorf("table header = %q", got)
				}
				keys := []string{"kind", "rfc", "maturity", "enablement", "readiness"}
				for i, want := range wantRows {
					fields := strings.Fields(lines[i+1])
					if len(fields) != len(keys) {
						t.Fatalf("row %d has %d columns, want %d", i, len(fields), len(keys))
					}
					for j, key := range keys {
						if fields[j] != fmt.Sprint(want[key]) {
							t.Errorf("row %d %s = %q, want %v", i, key, fields[j], want[key])
						}
					}
				}
				return
			}
			var rows []map[string]any
			if format == formatJSON {
				err = json.Unmarshal([]byte(out), &rows)
			} else {
				err = yaml.Unmarshal([]byte(out), &rows)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(wantRows) {
				t.Fatalf("decoded %d rows, want %d: %s", len(rows), len(wantRows), out)
			}
			for i, want := range wantRows {
				for key, value := range want {
					got := rows[i][key]
					if n, ok := got.(int); ok {
						got = float64(n)
					}
					if got != value {
						t.Errorf("row %d %s = %v, want %v", i, key, got, value)
					}
				}
			}
		})
	}
}

func TestCapabilityFormatsPreserveFutureEnums(t *testing.T) {
	t.Parallel()

	capabilities := []*bfdv1.CapabilityStatus{{
		Kind: bfdv1.CapabilityKind(91), Maturity: bfdv1.CapabilityMaturity(92),
		Enablement: bfdv1.CapabilityEnablement(93), Readiness: bfdv1.CapabilityReadiness(94),
	}}
	for _, format := range []string{formatJSON, formatYAML, formatTable} {
		out, err := formatCapabilities(capabilities, format)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"91", "92", "93", "94"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s lost future enum value %q: %s", format, want, out)
			}
		}
		if strings.Contains(out, "ready") || strings.Contains(out, "enabled") || strings.Contains(out, "preview") {
			t.Errorf("%s inferred a known status from a future enum: %s", format, out)
		}
	}
}

func TestCapabilityFormatsRejectUnsupportedAndDoNotInventRows(t *testing.T) {
	t.Parallel()

	if _, err := formatCapabilities(nil, "xml"); !errors.Is(err, errUnsupportedFormat) {
		t.Fatalf("unsupported format error = %v", err)
	}
	for _, format := range []string{formatJSON, formatYAML} {
		out, err := formatCapabilities(nil, format)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) != "[]" {
			t.Fatalf("%s invented capability rows: %s", format, out)
		}
	}
}

func TestCapabilitiesCommandRejectsArgumentsBeforeRPC(t *testing.T) {
	t.Parallel()

	cmd := capabilitiesCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"unexpected"})
	if err := cmd.ExecuteContext(t.Context()); err == nil {
		t.Fatal("capabilities accepted a positional argument")
	}
}

func TestCapabilitiesCommandWiring(t *testing.T) {
	for _, root := range []*cobra.Command{rootCmd, makeShellCommands()} {
		cmd, remaining, err := root.Find([]string{"capabilities"})
		if err != nil || cmd == root || cmd.Name() != "capabilities" || len(remaining) != 0 {
			t.Fatalf("%s capabilities lookup = %v, %v, %v", root.Name(), cmd, remaining, err)
		}
	}
	for _, cmd := range shellCommands {
		if cmd.name == "capabilities" {
			return
		}
	}
	t.Fatal("interactive help omitted capabilities")
}
