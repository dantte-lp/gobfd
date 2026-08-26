// Command benchreport validates benchmark results and renders the HTML report.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/benchreport"
)

const pathArgumentCount = 6

var errArgumentContract = errors.New("invalid benchreport argument contract")

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
