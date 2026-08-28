// Command junitreport renders a bounded standalone HTML report from JUnit XML.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/dantte-lp/gobfd/test/internal/junitreport"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("JUnit report generation failed", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("junitreport", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	input := flags.String("input", "", "repository-relative JUnit XML input")
	output := flags.String("output", "", "repository-relative HTML output")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse JUnit report flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected JUnit report arguments: %w", flag.ErrHelp)
	}
	if err := junitreport.Render(*root, *input, *output); err != nil {
		return fmt.Errorf("render JUnit report: %w", err)
	}
	if _, err := fmt.Fprintf(os.Stdout, "junitreport: wrote %s from %s\n", *output, *input); err != nil {
		return fmt.Errorf("write JUnit report result: %w", err)
	}
	return nil
}
