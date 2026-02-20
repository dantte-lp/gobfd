package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	appversion "github.com/dantte-lp/gobfd/internal/version"
)

// GitCommit is the git commit hash, set at build time via ldflags.
var GitCommit = "unknown"

// BuildDate is the build timestamp, set at build time via ldflags.
var BuildDate = "unknown"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print gobfdctl build information",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("gobfdctl %s\n", appversion.Version)
			fmt.Printf("  commit:  %s\n", GitCommit)
			fmt.Printf("  built:   %s\n", BuildDate)
		},
	}
}
