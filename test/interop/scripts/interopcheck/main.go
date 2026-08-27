// Command interopcheck evaluates structured output used by legacy interop runners.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/dantte-lp/gobfd/test/internal/interopcheck"
)

const (
	maxInputSize      = 2 << 20
	peerArgumentCount = 2
	gapArgumentCount  = 3
	usage             = "usage: interopcheck <gobgp-neighbor-state|frr-bfd-peer-status> <peer> | " +
		"detection-gap <down-epoch> <max-gap>"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, input io.Reader, output, errorOutput io.Writer) int {
	if len(args) == 0 {
		return usageError(errorOutput)
	}
	switch args[0] {
	case "gobgp-neighbor-state":
		return runPeerProjection(args, input, output, errorOutput, interopcheck.GoBGPNeighborState)
	case "frr-bfd-peer-status":
		return runPeerProjection(args, input, output, errorOutput, interopcheck.FRRBFDPeerStatus)
	case "detection-gap":
		return runDetectionGap(args, input, output, errorOutput)
	default:
		return usageError(errorOutput)
	}
}

func runPeerProjection(
	args []string,
	input io.Reader,
	output, errorOutput io.Writer,
	project func([]byte, string) (string, error),
) int {
	if len(args) != peerArgumentCount {
		return usageError(errorOutput)
	}
	data, status := readInput(input, errorOutput)
	if status != 0 {
		return status
	}
	value, err := project(data, args[1])
	return writeValue(output, errorOutput, value, err)
}

func runDetectionGap(args []string, input io.Reader, output, errorOutput io.Writer) int {
	if len(args) != gapArgumentCount {
		return usageError(errorOutput)
	}
	downEpoch, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		fmt.Fprintf(errorOutput, "parse Down epoch %q: %v\n", args[1], err)
		return 2
	}
	maxGap, err := strconv.ParseFloat(args[2], 64)
	if err != nil {
		fmt.Fprintf(errorOutput, "parse maximum gap %q: %v\n", args[2], err)
		return 2
	}
	data, status := readInput(input, errorOutput)
	if status != 0 {
		return status
	}
	result, err := interopcheck.DetectionGap(bytes.NewReader(data), downEpoch, maxGap)
	if err != nil {
		fmt.Fprintf(errorOutput, "evaluate detection gap: %v\n", err)
		return 2
	}
	if result.Status == interopcheck.StatusSkip {
		fmt.Fprintln(output, result.Status)
		return 0
	}
	fmt.Fprintf(output, "%s\t%.3f\n", result.Status, result.Gap)
	return 0
}

func readInput(input io.Reader, errorOutput io.Writer) ([]byte, int) {
	data, err := io.ReadAll(io.LimitReader(input, maxInputSize+1))
	if err != nil {
		fmt.Fprintf(errorOutput, "read interop input: %v\n", err)
		return nil, 2
	}
	if len(data) > maxInputSize {
		fmt.Fprintf(errorOutput, "read interop input: exceeds %d bytes\n", maxInputSize)
		return nil, 2
	}
	return data, 0
}

func writeValue(output, errorOutput io.Writer, value string, err error) int {
	if err != nil {
		fmt.Fprintln(errorOutput, err)
		return 2
	}
	if _, err := fmt.Fprintln(output, value); err != nil {
		fmt.Fprintf(errorOutput, "write result: %v\n", err)
		return 2
	}
	return 0
}

func usageError(errorOutput io.Writer) int {
	fmt.Fprintln(errorOutput, usage)
	return 2
}
