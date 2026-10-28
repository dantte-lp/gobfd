package commands

import (
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/dantte-lp/gobfd/internal/stepca"
)

func certificateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "certificate",
		Short: "Manage local management certificates",
		Args:  cobra.NoArgs,
		// Certificate renewal uses its own CA transport, independent of daemon RPC.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
	}
	cmd.AddCommand(certificateRenewCmd())

	return cmd
}

func certificateRenewCmd() *cobra.Command {
	var opts stepca.Options
	cmd := &cobra.Command{
		Use:   "renew",
		Short: "Renew an existing management certificate through an external CA",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			changed, err := stepca.RenewFile(ctx, opts)
			if err != nil {
				return fmt.Errorf("renew certificate: %w", err)
			}
			result := "certificate unchanged"
			if changed {
				result = "certificate renewed"
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), result); err != nil {
				if changed {
					return fmt.Errorf("write renewal result: %w: %w", stepca.ErrCommitted, err)
				}
				return fmt.Errorf("write renewal result: %w", err)
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&opts.CAURL, "ca-url", "", "HTTPS origin of the external CA (required)")
	cmd.Flags().StringVar(&opts.RootFile, "ca-root", "", "CA root certificate PEM file (required)")
	cmd.Flags().StringVar(&opts.CertFile, "cert", "", "Existing certificate PEM file to replace (required)")
	cmd.Flags().StringVar(&opts.KeyFile, "key", "", "Existing private-key PEM file (required)")
	for _, name := range []string{"ca-url", "ca-root", "cert", "key"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(fmt.Errorf("mark certificate renewal flag required: %w", err))
		}
	}

	return cmd
}
