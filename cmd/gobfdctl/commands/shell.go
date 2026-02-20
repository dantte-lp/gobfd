package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// shellCommands lists the available commands for the interactive shell help output.
var shellCommands = []struct {
	name string
	desc string
}{
	{"session list", "List all BFD sessions"},
	{"session show <id>", "Show details of a BFD session"},
	{"session add --peer <ip>", "Create a new BFD session"},
	{"session delete <discr>", "Delete a BFD session"},
	{"monitor [--current]", "Stream BFD session events"},
	{"version", "Print build information"},
	{"help", "Show this help message"},
	{"exit / quit", "Leave the interactive shell"},
}

func shellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Start an interactive gobfdctl shell",
		Long:  "Launches a simple REPL that accepts gobfdctl subcommands. Type 'help', 'exit', or 'quit'.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			printShellBanner()
			scanner := bufio.NewScanner(os.Stdin)
			fmt.Print("gobfdctl> ")

			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())

				switch {
				case line == "exit" || line == "quit":
					return nil
				case line == "help" || line == "?":
					printShellHelp()
				case line != "":
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

// printShellBanner prints a welcome message when the shell starts.
func printShellBanner() {
	fmt.Println("GoBFD interactive shell. Type 'help' for available commands, 'exit' to quit.")
	fmt.Println()
}

// printShellHelp prints a formatted list of available shell commands.
func printShellHelp() {
	fmt.Println("Available commands:")
	fmt.Println()

	for _, cmd := range shellCommands {
		fmt.Printf("  %-30s %s\n", cmd.name, cmd.desc)
	}

	fmt.Println()
}
