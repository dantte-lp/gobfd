package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func shellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Start an interactive gobfdctl shell",
		Long:  "Launches a simple REPL that accepts gobfdctl subcommands. Type 'exit' or 'quit' to leave.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			scanner := bufio.NewScanner(os.Stdin)
			fmt.Print("gobfdctl> ")

			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())

				if line == "exit" || line == "quit" {
					return nil
				}

				if line != "" {
					args := strings.Fields(line)
					rootCmd.SetArgs(args)

					if err := rootCmd.Execute(); err != nil {
						fmt.Fprintln(os.Stderr, "Error:", err)
					}
				}

				fmt.Print("gobfdctl> ")
			}

			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}

			return nil
		},
	}
}
