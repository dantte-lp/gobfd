// Package main provides the routing E2E container-inventory merge command.
package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/dantte-lp/gobfd/test/internal/routingartifacts"
)

const (
	mergeArgumentCount = 5
	readArgumentCount  = 3
	writeArgumentCount = 4
)

var errUsage = errors.New(
	"invalid arguments: usage: artifactmerge " +
		"{merge report-root output base.json bgp.json|" +
		"write-image-id report-root path id|read-image-id report-root path}",
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		slog.Error("merge routing artifacts", "error", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "merge":
		if len(args) != mergeArgumentCount {
			return errUsage
		}
		if err := routingartifacts.Merge(args[1], args[2], []routingartifacts.Input{
			{Name: "interop", Path: args[3]},
			{Name: "interop-bgp", Path: args[4]},
		}); err != nil {
			return fmt.Errorf("merge container inventories: %w", err)
		}
	case "write-image-id":
		if len(args) != writeArgumentCount {
			return errUsage
		}
		if err := routingartifacts.WriteImageID(args[1], args[2], args[3]); err != nil {
			return fmt.Errorf("persist tshark image ID: %w", err)
		}
	case "read-image-id":
		if len(args) != readArgumentCount {
			return errUsage
		}
		imageID, err := routingartifacts.ReadImageID(args[1], args[2])
		if err != nil {
			return fmt.Errorf("load tshark image ID: %w", err)
		}
		if _, err := fmt.Fprintln(stdout, imageID); err != nil {
			return fmt.Errorf("write tshark image ID: %w", err)
		}
	default:
		return errUsage
	}
	return nil
}
