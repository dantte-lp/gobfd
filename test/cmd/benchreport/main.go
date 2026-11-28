// Command benchreport validates benchmark results and renders the HTML report.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/benchreport"
)

const pathArgumentCount = 6

var errArgumentContract = errors.New("invalid benchreport argument contract")

var errOutputOutsideRoot = errors.New("benchreport output must be within the report ownership root")

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, time.Now); err != nil {
		fmt.Fprintf(os.Stderr, "gen-report: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, now func() time.Time) error {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) != pathArgumentCount {
		return fmt.Errorf("%w: requires six paths, got %d", errArgumentContract, len(args))
	}
	generated := getenv("GENERATED")
	if generated == "" {
		generated = now().UTC().Format(time.RFC3339)
	}
	goMaxProcs := getenv("GOMAXPROCS")
	if goMaxProcs == "" {
		goMaxProcs = "8"
	}
	if err := prepareOutputDirectory(args[5], getenv("BENCH_REPORT_ROOT")); err != nil {
		return err
	}
	if err := benchreport.Render(ctx, benchreport.Options{
		GoInput:    args[0],
		FRRInput:   args[1],
		BIRDInput:  args[2],
		Metadata:   args[3],
		Template:   args[4],
		Output:     args[5],
		Generated:  generated,
		Platform:   getenv("PLATFORM"),
		GCCVersion: getenv("GCC_VERSION"),
		GoMaxProcs: goMaxProcs,
	}); err != nil {
		return fmt.Errorf("render cross-comparison report: %w", err)
	}
	return nil
}

// prepareOutputDirectory creates the output directory beneath the report
// ownership root. BENCH_REPORT_ROOT may name a caller-owned artifact root;
// otherwise the command's working directory is used.
func prepareOutputDirectory(output, configuredRoot string) (returnErr error) {
	outputRoot := configuredRoot
	if outputRoot == "" {
		var err error
		outputRoot, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve benchreport output root: %w", err)
		}
	}
	absoluteRoot, err := filepath.Abs(outputRoot)
	if err != nil {
		return fmt.Errorf("resolve benchreport output root %q: %w", outputRoot, err)
	}
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolve report output path %q: %w", output, err)
	}
	relativeOutput, err := filepath.Rel(absoluteRoot, absoluteOutput)
	if err != nil {
		return fmt.Errorf("resolve report output relative to %s: %w", absoluteRoot, err)
	}
	if !filepath.IsLocal(relativeOutput) {
		return fmt.Errorf("output %q: %w", output, errOutputOutsideRoot)
	}

	root, err := os.OpenRoot(absoluteRoot)
	if err != nil {
		return fmt.Errorf("open benchreport output root %s: %w", absoluteRoot, err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr,
				fmt.Errorf("close benchreport output root %s: %w", absoluteRoot, closeErr))
		}
	}()

	directory := filepath.Dir(relativeOutput)
	if directory == "." {
		return nil
	}
	if err := root.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create report output directory %s: %w", directory, err)
	}
	return nil
}
