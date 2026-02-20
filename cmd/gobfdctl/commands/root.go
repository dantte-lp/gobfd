package commands

import (
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

var (
	// client is the ConnectRPC BFD service client, initialized in PersistentPreRunE.
	client bfdv1connect.BfdServiceClient

	// outputFormat controls the output format for all commands (table or json).
	outputFormat string

	// serverAddr is the daemon address (host:port) for the ConnectRPC connection.
	serverAddr string
)

// rootCmd is the top-level cobra command for gobfdctl.
var rootCmd = &cobra.Command{
	Use:   "gobfdctl",
	Short: "CLI client for the GoBFD daemon",
	Long:  "gobfdctl communicates with the gobfd daemon via ConnectRPC to manage BFD sessions.",
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		client = bfdv1connect.NewBfdServiceClient(
			http.DefaultClient,
			"http://"+serverAddr,
		)

		return nil
	},
	// Silence cobra's built-in usage/error printing so we control it.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&serverAddr, "addr", "localhost:50051",
		"gobfd daemon address (host:port)")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", "table",
		"output format: table, json")

	rootCmd.AddCommand(sessionCmd())
	rootCmd.AddCommand(monitorCmd())
	rootCmd.AddCommand(versionCmd())
	rootCmd.AddCommand(shellCmd())
}

// Execute runs the root command and exits with code 1 on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
