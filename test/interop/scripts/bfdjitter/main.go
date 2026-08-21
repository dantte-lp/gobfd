// Command bfdjitter evaluates a tshark BFD epoch/state TSV stream.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/dantte-lp/gobfd/test/internal/bfdjitter"
)

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(input io.Reader, output, errorOutput io.Writer) int {
	report, err := bfdjitter.Evaluate(input)
	if err != nil {
		fmt.Fprintf(errorOutput, "analyze BFD jitter TSV: %v\n", err)
		return 2
	}
	fmt.Fprintf(
		output,
		"%s\t%s\t%d\t%.6f\t%.6f\t%d\n",
		report.Status,
		report.Reason,
		report.UpPackets,
		report.MinDelta,
		report.MaxDelta,
		report.Samples,
	)
	return 0
}
