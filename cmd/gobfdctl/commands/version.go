package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	appversion "github.com/dantte-lp/gobfd/internal/version"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print gobfdctl build information",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(appversion.Full("gobfdctl"))
		},
	}
}
