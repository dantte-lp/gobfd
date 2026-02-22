package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/reeflective/console"
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
		Long:  "Launches an interactive REPL with tab completion. Type 'exit' or Ctrl-D to quit.",
		Args:  cobra.NoArgs,
		RunE:  runShell,
	}
}

// runShell initializes and starts the reeflective/console REPL.
// Tab completions are derived automatically from the Cobra command tree.
func runShell(_ *cobra.Command, _ []string) error {
	app := console.New("gobfdctl")

	menu := app.ActiveMenu()
	menu.SetCommands(makeShellCommands)

	// Ctrl-D (EOF) exits the shell cleanly.
	menu.AddInterrupt(io.EOF, func(_ *console.Console) {
		os.Exit(0)
	})

	printShellBanner()

	if err := app.Start(); err != nil {
		return fmt.Errorf("run interactive shell: %w", err)
	}

	return nil
}

// makeShellCommands returns a fresh Cobra command tree for the interactive shell.
// reeflective/console calls this before each prompt cycle to rebuild completions.
func makeShellCommands() *cobra.Command {
	root := &cobra.Command{
		Use:           "gobfdctl",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(sessionCmd())
	root.AddCommand(monitorCmd())
	root.AddCommand(versionCmd())
	root.AddCommand(shellHelpCmd())
	root.AddCommand(exitCmd())

	return root
}

// shellHelpCmd returns a "help" command that prints shell-specific help.
func shellHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help",
		Short: "Show available commands",
		Run: func(_ *cobra.Command, _ []string) {
			printShellHelp()
		},
	}
}

// exitCmd returns an "exit" command that terminates the shell.
func exitCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "exit",
		Aliases: []string{"quit"},
		Short:   "Leave the interactive shell",
		Run: func(_ *cobra.Command, _ []string) {
			os.Exit(0)
		},
	}
}

// printShellBanner prints a welcome message when the shell starts.
func printShellBanner() {
	fmt.Println("GoBFD interactive shell. Type 'help' for available commands, 'exit' to quit.")
	fmt.Println("Press Tab for autocomplete suggestions.")
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
