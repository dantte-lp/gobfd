package commands

import (
	"fmt"
	"os"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/spf13/cobra"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

var (
	// client is the ConnectRPC BFD service client, initialized in PersistentPreRunE.
	client bfdv1connect.BfdServiceClient

	// echoClient is the ConnectRPC EchoService client (RFC 9747).
	echoClient bfdv1connect.EchoServiceClient

	// microClient is the ConnectRPC MicroBFDService client (RFC 7130).
	microClient bfdv1connect.MicroBFDServiceClient

	// outputFormat controls the output format for all commands (table or json).
	outputFormat string

	// serverAddr is the daemon endpoint: host:port, http://, https:// or
	// unix:///path.
	serverAddr string

	// tlsFiles are the client TLS files for an https:// endpoint.
	tlsFiles apiclient.TLSFiles
)

// rootCmd is the top-level cobra command for gobfdctl.
var rootCmd = &cobra.Command{
	Use:   "gobfdctl",
	Short: "CLI client for the GoBFD daemon",
	Long:  "gobfdctl communicates with the gobfd daemon via ConnectRPC to manage BFD sessions.",
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		ep, err := apiclient.ParseEndpoint(serverAddr)
		if err != nil {
			return fmt.Errorf("--addr: %w", err)
		}
		httpClient, err := ep.HTTPClientTLS(tlsFiles)
		if err != nil {
			return fmt.Errorf("--tls-*: %w", err)
		}
		rpc := connect.NewClient(connecthttp.NewTransport(httpClient, ep.BaseURL))
		client = bfdv1connect.NewBfdServiceClient(rpc)
		echoClient = bfdv1connect.NewEchoServiceClient(rpc)
		microClient = bfdv1connect.NewMicroBFDServiceClient(rpc)

		return nil
	},
	// Silence cobra's built-in usage/error printing so we control it.
	SilenceUsage:  true,
	SilenceErrors: true,
}

//nolint:gochecknoinits // Cobra command flags and subcommands are registered during package initialization.
func init() {
	rootCmd.PersistentFlags().StringVar(&serverAddr, "addr", "unix:///run/gobfd/gobfd.sock",
		"gobfd daemon endpoint: host:port, http://host:port, https://host:port or unix:///path")
	rootCmd.PersistentFlags().StringVar(&tlsFiles.CAFile, "tls-ca", "",
		"CA file that signed the daemon certificate (https:// endpoints)")
	rootCmd.PersistentFlags().StringVar(&tlsFiles.CertFile, "tls-cert", "",
		"client certificate file for mTLS (https:// endpoints)")
	rootCmd.PersistentFlags().StringVar(&tlsFiles.KeyFile, "tls-key", "",
		"client key file for mTLS (https:// endpoints)")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", "table",
		"output format: table, json, yaml")

	rootCmd.AddCommand(sessionCmd())
	rootCmd.AddCommand(echoCmd())
	rootCmd.AddCommand(microCmd())
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
