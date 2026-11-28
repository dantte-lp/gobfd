// Command interopctl owns the manual interoperability Compose lifecycle.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root, err := os.Getwd()
	if err == nil {
		var controller *interopproject.Controller
		controller, err = interopproject.New(root, os.Stdout, os.Stderr)
		if err == nil {
			err = controller.Run(ctx, os.Args[1:])
		}
	}
	if err == nil {
		return 0
	}
	fmt.Fprintf(os.Stderr, "interopctl: %v\n", err)
	if _, ok := errors.AsType[*interopproject.UsageError](err); ok {
		return 2
	}
	if childErr, ok := errors.AsType[*interopproject.ChildExitError](err); ok {
		return childErr.Code
	}
	return 1
}
