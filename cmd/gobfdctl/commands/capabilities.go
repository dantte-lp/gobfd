package commands

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

func capabilitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "Show preview capability maturity, enablement, and readiness",
		Long: "Show daemon-reported preview capability maturity, enablement, and readiness. " +
			"Readiness does not establish peer health or RFC compliance.",
		Args: cobra.NoArgs,
		// Use a local service client in both the CLI and interactive shell.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			rpc, cleanup, err := managementRPC()
			if err != nil {
				return err
			}
			defer cleanup()
			resp, err := bfdv1connect.NewCapabilityServiceClient(rpc).ListCapabilities(
				cmd.Context(), &bfdv1.ListCapabilitiesRequest{})
			if err != nil {
				return fmt.Errorf("list capabilities: %w", err)
			}
			out, err := formatCapabilities(resp.GetCapabilities(), outputFormat)
			if err != nil {
				return fmt.Errorf("format capabilities: %w", err)
			}
			if _, err := fmt.Fprint(cmd.OutOrStdout(), out); err != nil {
				return fmt.Errorf("write capabilities: %w", err)
			}
			return nil
		},
	}
}

type capabilityView struct {
	Kind       string `json:"kind"       yaml:"kind"`
	RFC        uint32 `json:"rfc"        yaml:"rfc"`
	Maturity   string `json:"maturity"   yaml:"maturity"`
	Enablement string `json:"enablement" yaml:"enablement"`
	Readiness  string `json:"readiness"  yaml:"readiness"`
}

func formatCapabilities(capabilities []*bfdv1.CapabilityStatus, format string) (string, error) {
	rows := make([]capabilityView, 0, len(capabilities))
	for _, capability := range capabilities {
		rows = append(rows, capabilityView{
			Kind:       capabilityValue(capability.GetKind().String(), "CAPABILITY_KIND_"),
			RFC:        capability.GetRfc(),
			Maturity:   capabilityValue(capability.GetMaturity().String(), "CAPABILITY_MATURITY_"),
			Enablement: capabilityValue(capability.GetEnablement().String(), "CAPABILITY_ENABLEMENT_"),
			Readiness:  capabilityValue(capability.GetReadiness().String(), "CAPABILITY_READINESS_"),
		})
	}
	return formatOutput(rows, format, func() (string, error) {
		var buf strings.Builder
		w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "CAPABILITY\tRFC\tMATURITY\tENABLEMENT\tREADINESS"); err != nil {
			return "", fmt.Errorf("write capability header: %w", err)
		}
		for _, row := range rows {
			if _, err := fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n",
				row.Kind, row.RFC, row.Maturity, row.Enablement, row.Readiness); err != nil {
				return "", fmt.Errorf("write capability row: %w", err)
			}
		}
		if err := w.Flush(); err != nil {
			return "", fmt.Errorf("flush capability table: %w", err)
		}
		return buf.String(), nil
	})
}

func capabilityValue(value, prefix string) string {
	value = strings.TrimPrefix(value, prefix)
	if value == "UNSPECIFIED" {
		return "unknown"
	}
	return strings.ToLower(value)
}
